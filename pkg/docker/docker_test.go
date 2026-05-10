package docker

import (
	"context"
	"testing"

	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
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
