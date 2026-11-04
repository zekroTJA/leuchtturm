package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestController(defaultSchedule string) *Controller {
	return &Controller{
		scheduler:       cron.New(),
		scheduledJobs:   make(map[string]cron.EntryID),
		defaultSchedule: defaultSchedule,
	}
}

func TestScheduleContainerUpdate(t *testing.T) {
	t.Run("uses container schedule when provided", func(t *testing.T) {
		controller := newTestController("@daily")
		container := &Container{ID: "c1", Schedule: "@hourly"}

		err := controller.scheduleContainerUpdate(context.Background(), container)

		assert.NoError(t, err)
		jobId, ok := controller.scheduledJobs[container.ID]
		assert.True(t, ok)
		entry := controller.scheduler.Entry(jobId)
		assert.True(t, entry.Valid())
	})

	t.Run("falls back to default schedule when container schedule is empty", func(t *testing.T) {
		controller := newTestController("@weekly")
		container := &Container{ID: "c1"}

		err := controller.scheduleContainerUpdate(context.Background(), container)

		assert.NoError(t, err)
		_, ok := controller.scheduledJobs[container.ID]
		assert.True(t, ok)
	})

	t.Run("returns error on invalid schedule", func(t *testing.T) {
		controller := newTestController("not-a-valid-cron")
		container := &Container{ID: "c1"}

		err := controller.scheduleContainerUpdate(context.Background(), container)

		assert.Error(t, err)
		_, ok := controller.scheduledJobs[container.ID]
		assert.False(t, ok)
	})

	t.Run("replaces existing job for same container ID", func(t *testing.T) {
		controller := newTestController("@daily")
		container := &Container{ID: "c1", Schedule: "@hourly"}

		err := controller.scheduleContainerUpdate(context.Background(), container)
		assert.NoError(t, err)
		firstJobId := controller.scheduledJobs[container.ID]

		container.Schedule = "@every 5m"
		err = controller.scheduleContainerUpdate(context.Background(), container)
		assert.NoError(t, err)
		secondJobId := controller.scheduledJobs[container.ID]

		assert.NotEqual(t, firstJobId, secondJobId)
		assert.False(t, controller.scheduler.Entry(firstJobId).Valid(),
			"old job should have been removed from the scheduler")
		assert.True(t, controller.scheduler.Entry(secondJobId).Valid())
	})

	t.Run("schedules independent jobs for different container IDs", func(t *testing.T) {
		controller := newTestController("@daily")

		err := controller.scheduleContainerUpdate(context.Background(), &Container{ID: "c1", Schedule: "@hourly"})
		assert.NoError(t, err)
		err = controller.scheduleContainerUpdate(context.Background(), &Container{ID: "c2", Schedule: "@hourly"})
		assert.NoError(t, err)

		assert.Len(t, controller.scheduledJobs, 2)
		assert.NotEqual(t, controller.scheduledJobs["c1"], controller.scheduledJobs["c2"])
	})
}

func TestScheduleContainerUpdateEdgeCases(t *testing.T) {
	t.Run("invalid container schedule is not replaced by valid default", func(t *testing.T) {
		controller := newTestController("@daily")
		container := &Container{ID: "c1", Schedule: "every now and then"}

		err := controller.scheduleContainerUpdate(context.Background(), container)

		assert.Error(t, err)
		assert.Empty(t, controller.scheduledJobs)
		assert.Empty(t, controller.scheduler.Entries())
	})

	t.Run("failed rescheduling keeps the previous job", func(t *testing.T) {
		controller := newTestController("@daily")
		container := &Container{ID: "c1", Schedule: "@hourly"}

		require.NoError(t, controller.scheduleContainerUpdate(context.Background(), container))
		firstJobId := controller.scheduledJobs[container.ID]

		container.Schedule = "* * *"
		err := controller.scheduleContainerUpdate(context.Background(), container)

		assert.Error(t, err)
		assert.Equal(t, firstJobId, controller.scheduledJobs[container.ID])
		assert.True(t, controller.scheduler.Entry(firstJobId).Valid())
	})

	t.Run("empty container and default schedule yields error", func(t *testing.T) {
		controller := newTestController("")

		err := controller.scheduleContainerUpdate(context.Background(), &Container{ID: "c1"})

		assert.Error(t, err)
		assert.Empty(t, controller.scheduledJobs)
	})

	t.Run("six field cron expression is rejected", func(t *testing.T) {
		controller := newTestController("0 0 12 * * *")

		err := controller.scheduleContainerUpdate(context.Background(), &Container{ID: "c1"})

		assert.Error(t, err)
	})
}

func TestHandleEvent(t *testing.T) {
	startEvent := func(id string, attributes map[string]string) *events.Message {
		return &events.Message{
			Type:   events.ContainerEventType,
			Action: events.ActionStart,
			Actor:  events.Actor{ID: id, Attributes: attributes},
		}
	}

	t.Run("start event schedules container", func(t *testing.T) {
		controller := newTestController("@daily")

		controller.handleEvent(context.Background(), startEvent("c1", map[string]string{
			labelKeyEnable: "true",
		}))

		jobId, ok := controller.scheduledJobs["c1"]
		require.True(t, ok)
		assert.True(t, controller.scheduler.Entry(jobId).Valid())
	})

	t.Run("start event uses schedule label", func(t *testing.T) {
		controller := newTestController("@daily")

		controller.handleEvent(context.Background(), startEvent("c1", map[string]string{
			labelKeyEnable:   "true",
			labelKeySchedule: "@every 1h",
		}))

		jobId, ok := controller.scheduledJobs["c1"]
		require.True(t, ok)
		assert.Equal(t, time.Hour, controller.scheduler.Entry(jobId).Schedule.(cron.ConstantDelaySchedule).Delay)
	})

	t.Run("start event with invalid schedule label does not schedule", func(t *testing.T) {
		controller := newTestController("@daily")

		controller.handleEvent(context.Background(), startEvent("c1", map[string]string{
			labelKeyEnable:   "true",
			labelKeySchedule: "invalid",
		}))

		assert.Empty(t, controller.scheduledJobs)
		assert.Empty(t, controller.scheduler.Entries())
	})

	t.Run("start event of disabled container does not schedule", func(t *testing.T) {
		controller := newTestController("@daily")

		controller.handleEvent(context.Background(), startEvent("c1", map[string]string{
			labelKeyEnable: "false",
		}))

		assert.Empty(t, controller.scheduledJobs,
			"containers with %s=false must not be scheduled (getContainers filters them as well)", labelKeyEnable)
	})

	t.Run("repeated start events keep a single job", func(t *testing.T) {
		controller := newTestController("@daily")
		attributes := map[string]string{labelKeyEnable: "true"}

		controller.handleEvent(context.Background(), startEvent("c1", attributes))
		controller.handleEvent(context.Background(), startEvent("c1", attributes))

		assert.Len(t, controller.scheduledJobs, 1)
		assert.Len(t, controller.scheduler.Entries(), 1)
	})

	for _, action := range []events.Action{events.ActionStop, events.ActionDie, events.ActionDestroy} {
		t.Run(string(action)+" event unschedules container", func(t *testing.T) {
			controller := newTestController("@daily")
			controller.handleEvent(context.Background(), startEvent("c1", map[string]string{labelKeyEnable: "true"}))
			controller.handleEvent(context.Background(), startEvent("c2", map[string]string{labelKeyEnable: "true"}))
			jobId := controller.scheduledJobs["c1"]

			controller.handleEvent(context.Background(), &events.Message{
				Action: action,
				Actor:  events.Actor{ID: "c1"},
			})

			assert.NotContains(t, controller.scheduledJobs, "c1")
			assert.Contains(t, controller.scheduledJobs, "c2")
			assert.False(t, controller.scheduler.Entry(jobId).Valid())
		})
	}

	t.Run("stop event for unknown container is a no-op", func(t *testing.T) {
		controller := newTestController("@daily")
		controller.handleEvent(context.Background(), startEvent("c1", map[string]string{labelKeyEnable: "true"}))

		controller.handleEvent(context.Background(), &events.Message{
			Action: events.ActionStop,
			Actor:  events.Actor{ID: "unknown"},
		})

		assert.Len(t, controller.scheduledJobs, 1)
	})

	t.Run("die after stop is a no-op", func(t *testing.T) {
		controller := newTestController("@daily")
		controller.handleEvent(context.Background(), startEvent("c1", map[string]string{labelKeyEnable: "true"}))

		controller.handleEvent(context.Background(), &events.Message{Action: events.ActionStop, Actor: events.Actor{ID: "c1"}})
		controller.handleEvent(context.Background(), &events.Message{Action: events.ActionDie, Actor: events.Actor{ID: "c1"}})

		assert.Empty(t, controller.scheduledJobs)
	})

	t.Run("unrelated actions are ignored", func(t *testing.T) {
		controller := newTestController("@daily")
		controller.handleEvent(context.Background(), startEvent("c1", map[string]string{labelKeyEnable: "true"}))

		for _, action := range []events.Action{events.ActionPause, events.ActionRestart, events.ActionCreate, ""} {
			controller.handleEvent(context.Background(), &events.Message{Action: action, Actor: events.Actor{ID: "c1"}})
		}

		assert.Len(t, controller.scheduledJobs, 1)
	})
}

func TestGetContainers(t *testing.T) {
	t.Run("returns enabled containers with parsed labels", func(t *testing.T) {
		daemon := newFakeDaemon(t)
		daemon.handleJSON(http.MethodGet, "/containers/json", http.StatusOK, []map[string]any{
			{"Id": "c1", "Labels": map[string]string{labelKeyEnable: "true", labelKeySchedule: "@hourly"}},
			{"Id": "c2", "Labels": map[string]string{labelKeyEnable: "yes", labelKeyKeepOldImage: "false"}},
		})
		controller := daemon.newController(t, "@daily")

		containers, err := controller.getContainers(context.Background())

		require.NoError(t, err)
		require.Len(t, containers, 2)
		assert.Equal(t, "c1", containers[0].ID)
		assert.Equal(t, "@hourly", containers[0].Schedule)
		assert.Nil(t, containers[0].KeepOldImage)
		assert.Equal(t, "c2", containers[1].ID)
		if assert.NotNil(t, containers[1].KeepOldImage) {
			assert.False(t, *containers[1].KeepOldImage)
		}
	})

	t.Run("filters containers not explicitly enabled", func(t *testing.T) {
		daemon := newFakeDaemon(t)
		daemon.handleJSON(http.MethodGet, "/containers/json", http.StatusOK, []map[string]any{
			{"Id": "c1", "Labels": map[string]string{labelKeyEnable: "false"}},
			{"Id": "c2", "Labels": map[string]string{labelKeyEnable: ""}},
			{"Id": "c3", "Labels": map[string]string{labelKeyEnable: "nope"}},
			{"Id": "c4", "Labels": map[string]string{}},
			{"Id": "c5", "Labels": map[string]string{labelKeyEnable: " TRUE "}},
		})
		controller := daemon.newController(t, "@daily")

		containers, err := controller.getContainers(context.Background())

		require.NoError(t, err)
		require.Len(t, containers, 1)
		assert.Equal(t, "c5", containers[0].ID)
	})

	t.Run("sends label and status filters", func(t *testing.T) {
		daemon := newFakeDaemon(t)
		daemon.handleJSON(http.MethodGet, "/containers/json", http.StatusOK, []any{})
		controller := daemon.newController(t, "@daily")

		_, err := controller.getContainers(context.Background())
		require.NoError(t, err)

		request, ok := daemon.findRequest(http.MethodGet, "/containers/json")
		require.True(t, ok)
		filters := request.Query["filters"]
		require.Len(t, filters, 1)
		assert.Contains(t, filters[0], labelKeyEnable)
		assert.Contains(t, filters[0], "running")
	})

	t.Run("returns daemon error", func(t *testing.T) {
		daemon := newFakeDaemon(t)
		daemon.handleJSON(http.MethodGet, "/containers/json", http.StatusInternalServerError,
			map[string]string{"message": "boom"})
		controller := daemon.newController(t, "@daily")

		containers, err := controller.getContainers(context.Background())

		assert.ErrorContains(t, err, "boom")
		assert.Nil(t, containers)
	})
}

func TestScheduleUpdates(t *testing.T) {
	t.Run("schedules all enabled containers", func(t *testing.T) {
		daemon := newFakeDaemon(t)
		daemon.handleJSON(http.MethodGet, "/containers/json", http.StatusOK, []map[string]any{
			{"Id": "c1", "Labels": map[string]string{labelKeyEnable: "true"}},
			{"Id": "c2", "Labels": map[string]string{labelKeyEnable: "true", labelKeySchedule: "@hourly"}},
		})
		controller := daemon.newController(t, "@daily")

		err := controller.scheduleUpdates(context.Background())

		require.NoError(t, err)
		assert.Len(t, controller.scheduledJobs, 2)
	})

	t.Run("no containers is not an error", func(t *testing.T) {
		daemon := newFakeDaemon(t)
		daemon.handleJSON(http.MethodGet, "/containers/json", http.StatusOK, []any{})
		controller := daemon.newController(t, "@daily")

		err := controller.scheduleUpdates(context.Background())

		require.NoError(t, err)
		assert.Empty(t, controller.scheduledJobs)
	})

	t.Run("invalid schedule on one container still schedules the others", func(t *testing.T) {
		daemon := newFakeDaemon(t)
		daemon.handleJSON(http.MethodGet, "/containers/json", http.StatusOK, []map[string]any{
			{"Id": "c1", "Labels": map[string]string{labelKeyEnable: "true", labelKeySchedule: "bogus"}},
			{"Id": "c2", "Labels": map[string]string{labelKeyEnable: "true"}},
		})
		controller := daemon.newController(t, "@daily")

		err := controller.scheduleUpdates(context.Background())

		assert.Error(t, err)
		assert.NotContains(t, controller.scheduledJobs, "c1")
		assert.Contains(t, controller.scheduledJobs, "c2")
	})

	t.Run("listing error is wrapped", func(t *testing.T) {
		daemon := newFakeDaemon(t)
		daemon.handleJSON(http.MethodGet, "/containers/json", http.StatusInternalServerError,
			map[string]string{"message": "daemon unavailable"})
		controller := daemon.newController(t, "@daily")

		err := controller.scheduleUpdates(context.Background())

		assert.ErrorContains(t, err, "failed listing containers")
		assert.ErrorContains(t, err, "daemon unavailable")
	})
}

func TestEventListener(t *testing.T) {
	daemon := newFakeDaemon(t)
	daemon.handle(http.MethodGet, "/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		encoder := json.NewEncoder(w)
		_ = encoder.Encode(events.Message{
			Type:   events.ContainerEventType,
			Action: events.ActionStart,
			Actor:  events.Actor{ID: "c1", Attributes: map[string]string{labelKeyEnable: "true"}},
		})
		_ = encoder.Encode(events.Message{
			Type:   events.ContainerEventType,
			Action: events.ActionStart,
			Actor:  events.Actor{ID: "c2", Attributes: map[string]string{labelKeyEnable: "true"}},
		})
		_ = encoder.Encode(events.Message{
			Type:   events.ContainerEventType,
			Action: events.ActionDie,
			Actor:  events.Actor{ID: "c1"},
		})
	})
	controller := daemon.newController(t, "@daily")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go controller.eventListener(ctx)

	assert.Eventually(t, func() bool {
		controller.mtx.Lock()
		defer controller.mtx.Unlock()
		_, hasFirst := controller.scheduledJobs["c1"]
		_, hasSecond := controller.scheduledJobs["c2"]
		return !hasFirst && hasSecond
	}, 2*time.Second, 10*time.Millisecond)

	request, ok := daemon.findRequest(http.MethodGet, "/events")
	require.True(t, ok)
	filters := request.Query["filters"]
	require.Len(t, filters, 1)
	for _, expected := range []string{labelKeyEnable, "container", "start", "stop", "die", "destroy"} {
		assert.Contains(t, filters[0], expected)
	}
}

func TestClose(t *testing.T) {
	daemon := newFakeDaemon(t)
	controller := daemon.newController(t, "@daily")
	ctx, cancel := context.WithCancelCause(context.Background())
	controller.ctxCancelFunc = cancel
	controller.scheduler.Start()

	err := controller.Close()

	assert.NoError(t, err)
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
	assert.EqualError(t, context.Cause(ctx), "controller closed")
}
