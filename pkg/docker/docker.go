package docker

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/robfig/cron/v3"
	"github.com/zekrotja/leuchtturm/pkg/util"
)

const (
	labelKeyEnable       = "leuchtturm.enable"
	labelKeyKeepOldImage = "leuchtturm.keep-old-image"
	labelKeySchedule     = "leuchtturm.schedule"
)

// Controller manages the scheduling of container updates using Docker events and cron jobs.
type Controller struct {
	mtx sync.Mutex
	wg  sync.WaitGroup

	client        *client.Client
	scheduler     *cron.Cron
	ctxCancelFunc context.CancelCauseFunc
	scheduledJobs map[string]cron.EntryID

	defaultSchedule string
	keepOldImage    bool
}

// New creates a new Controller instance with the given schedule and keepOldImage as default settings.
// The connection to the Docker daemon is established and tested with a ping. The Cron scheduler is
// created and started. All enabled containers are listed and scheduled for updates and the Docker
// event listener loop is started in a separate go-routine which is canceled on calling Close.
func New(schedule string, keepOldImage bool) (*Controller, error) {
	cl, err := client.New(client.FromEnv)
	if err != nil {
		return nil, err
	}

	{
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err = cl.Ping(ctx, client.PingOptions{})
		if err != nil {
			return nil, err
		}
	}

	scheduler := cron.New()
	scheduler.Start()

	ctx, cancel := context.WithCancelCause(context.Background())

	t := &Controller{
		client:          cl,
		scheduler:       scheduler,
		ctxCancelFunc:   cancel,
		scheduledJobs:   make(map[string]cron.EntryID),
		defaultSchedule: schedule,
		keepOldImage:    keepOldImage,
	}

	err = t.scheduleUpdates(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed scheduling container updates: %w", err)
	}

	go t.eventListener(ctx)

	return t, nil
}

// Close waits for all running container updates to be finished. After that, the root context is canceled,
// the Cron scheduler is stopped and the Docker client is shut down.
func (t *Controller) Close() error {
	t.wg.Wait()
	t.ctxCancelFunc(errors.New("controller closed"))
	t.scheduler.Stop()
	return t.client.Close()
}

// eventListener creates a loop listening to container lifecycle events from the Docker engine. When a container
// is stopped, dies or is destroyed, the coresponding schedule is removed. When a new container is started, the
// container is scheduled for updates.
func (t *Controller) eventListener(ctx context.Context) {
	res := t.client.Events(ctx, client.EventsListOptions{
		Filters: client.Filters{
			"type":  {"container": true},
			"label": {labelKeyEnable: true},
			"event": {
				"start":   true,
				"stop":    true,
				"die":     true,
				"destroy": true,
			},
		},
	})

	go func() {
		for err := range res.Err {
			slog.Error("error receiving docker event", "err", err)
		}
	}()

	for event := range res.Messages {
		t.handleEvent(ctx, &event)
	}
}

// handleEvent handles a Docker container lifecycle event. On a stop, die or destroy event, the scheduled update job
// is removed. On a start event, an update job is scheduled.
func (t *Controller) handleEvent(ctx context.Context, event *events.Message) {
	t.mtx.Lock()
	defer t.mtx.Unlock()

	switch event.Action {
	case "stop", "die", "destroy":
		if jobId, ok := t.scheduledJobs[event.Actor.ID]; ok {
			slog.Info("unscheduling container", "container", event.Actor.ID, "action", event.Action)
			t.scheduler.Remove(jobId)
			delete(t.scheduledJobs, event.Actor.ID)
		}
	case "start":
		slog.Info("scheduling container", "container", event.Actor.ID, "action", event.Action)
		container := newContainer(event.Actor.ID, event.Actor.Attributes)
		err := t.scheduleContainerUpdate(ctx, container)
		if err != nil {
			slog.Error("failed scheduling container update", "container", container, "err", err)
			return
		}
	}
}

// scheduleUpdate takes the list of labeled and started containers and schedules them using scheduleContainerUpdate.
func (t *Controller) scheduleUpdates(ctx context.Context) error {
	t.mtx.Lock()
	defer t.mtx.Unlock()

	containers, err := t.getContainers(ctx)
	if err != nil {
		return fmt.Errorf("failed listing containers: %w", err)
	}

	if len(containers) == 0 {
		slog.Warn("no containers found tagged for updates")
		return nil
	}

	for _, container := range containers {
		err = errors.Join(err, t.scheduleContainerUpdate(ctx, container))
	}

	return err
}

// scheduleContainerUpdate takes a container, reads the schedule from the container label or uses the default configured
// schedule and adds or replaces the container update job to the Cron scheduler.
func (t *Controller) scheduleContainerUpdate(ctx context.Context, container *Container) error {
	schedule := cmp.Or(container.Schedule, t.defaultSchedule)
	jobId, err := t.scheduler.AddFunc(schedule, t.updateContainerJob(ctx, container))
	if err != nil {
		slog.Error("failed scheduling container update", "schedule", schedule, "container", container, "err", err)
		return fmt.Errorf("failed scheduling container update: %w", err)
	}
	slog.Info("scheduled container update", "schedule", schedule, "container", container, "jobid", jobId)
	if oldJobId, ok := t.scheduledJobs[container.ID]; ok {
		slog.Debug("removed container schedule", "schedule", schedule, "container", container)
		t.scheduler.Remove(oldJobId)
	}
	t.scheduledJobs[container.ID] = jobId
	return err
}

// getContainers lists all running containers with the enabled label. Configuration labels are read and stored in the
// result Container list.
func (t *Controller) getContainers(ctx context.Context) ([]*Container, error) {
	list, err := t.client.ContainerList(ctx, client.ContainerListOptions{
		Filters: client.Filters{
			"label":  {labelKeyEnable: true},
			"status": {"running": true},
		},
	})
	if err != nil {
		return nil, err
	}

	results := make([]*Container, 0, len(list.Items))
	for _, item := range list.Items {
		if !util.IsTrue(item.Labels[labelKeyEnable]) {
			continue
		}
		results = append(results, newContainer(item.ID, item.Labels))
	}

	return results, nil
}

// updateContainer takes a Container reference and checks if there is a new image available by pulling the container
// image. If a new image is available, the target contaienr is stopped, removed and re-created and started with the
// new image version.
func (t *Controller) updateContainer(ctx context.Context, container *Container) error {
	start := time.Now()
	defer func() {
		slog.Debug("finished container update", "container", container, "took", time.Since(start))
	}()

	t.wg.Add(1)
	defer t.wg.Done()

	containerInspect, err := t.client.ContainerInspect(ctx, container.ID, client.ContainerInspectOptions{})
	if err != nil {
		return err
	}

	image := containerInspect.Container.Config.Image
	if strings.HasPrefix(image, "sha256:") {
		slog.Warn("skipping container with image hash", "container", container.ID, "image", image)
		return nil
	}

	imgInspect, err := t.client.ImageInspect(ctx, image)
	var oldImgID string
	if err == nil {
		oldImgID = imgInspect.ID
	}

	pullResp, err := t.client.ImagePull(ctx, image, client.ImagePullOptions{})
	if err != nil {
		return err
	}
	if err = pullResp.Wait(ctx); err != nil {
		return err
	}

	imgInspect, err = t.client.ImageInspect(ctx, image)
	if err != nil {
		return err
	}

	if oldImgID == imgInspect.ID {
		slog.Info("image is up to date", "container", container.ID, "image", image)
		return nil
	}

	slog.Info("updating image", "container", container.ID, "image", image)

	oldContainerName := strings.TrimPrefix(containerInspect.Container.Name, "/")
	slog.Debug("renaming old container", "container", container.ID, "oldName", oldContainerName)
	_, err = t.client.ContainerRename(ctx, container.ID, client.ContainerRenameOptions{
		NewName: oldContainerName + "_old",
	})
	if err != nil {
		slog.Warn("failed renaming old container", "container", container.ID, "err", err)
	}

	slog.Debug("stopping container", "container", container.ID)
	_, err = t.client.ContainerStop(ctx, container.ID, client.ContainerStopOptions{})
	if err != nil {
		return err
	}

	slog.Debug("removing container", "container", container.ID)
	_, err = t.client.ContainerRemove(ctx, container.ID, client.ContainerRemoveOptions{Force: true})
	if err != nil {
		return err
	}

	slog.Debug("re-creating container", "container", container.ID)
	createResp, err := t.client.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:     containerInspect.Container.Config,
		HostConfig: containerInspect.Container.HostConfig,
		NetworkingConfig: &network.NetworkingConfig{
			EndpointsConfig: containerInspect.Container.NetworkSettings.Networks,
		},
		Name: oldContainerName,
	})
	if err != nil {
		return err
	}

	slog.Debug("starting container", "container", createResp.ID)
	_, err = t.client.ContainerStart(ctx, createResp.ID, client.ContainerStartOptions{})
	if err != nil {
		return err
	}

	keepOldImage := t.keepOldImage
	if container.KeepOldImage != nil {
		// container label overrides global config
		keepOldImage = *container.KeepOldImage
	}

	if !keepOldImage {
		slog.Debug("removing old image", "container", createResp.ID, "old-image", oldImgID)
		_, err = t.client.ImageRemove(ctx, oldImgID, client.ImageRemoveOptions{})
		if err != nil {
			return err
		}
	}

	return nil
}

// updateContainerJob returns a job function for updating the given Container.
func (t *Controller) updateContainerJob(ctx context.Context, container *Container) func() {
	return func() {
		err := t.updateContainer(ctx, container)
		if err != nil {
			slog.Error("failed to update container", "container", container.ID, "error", err)
		}
	}
}
