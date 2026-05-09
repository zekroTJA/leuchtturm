package docker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
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

type Controller struct {
	client        *client.Client
	scheduler     *cron.Cron
	ctxCancelFunc context.CancelCauseFunc

	defaultSchedule string
	keepOldImage    bool
}

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

func (t *Controller) Close() error {
	t.ctxCancelFunc(errors.New("controller closed"))
	t.scheduler.Stop()
	return t.client.Close()
}

type Container struct {
	*container.Summary
	KeepOldImage *bool
	Schedule     string
}

func (t *Container) String() string {
	return t.ID
}

func (t *Controller) eventListener(ctx context.Context) {
	res := t.client.Events(ctx, client.EventsListOptions{
		Filters: client.Filters{
			"type":  {"container": true},
			"label": {labelKeyEnable: true},
			"event": {
				"create":  true,
				"start":   true,
				"stop":    true,
				"die":     true,
				"destroy": true,
			},
		},
	})
	for event := range res.Messages {
		slog.Info("received event - rescheduling", "event.action", event.Action, "event.type", event.Type)
		err := t.scheduleUpdates(ctx)
		if err != nil {
			slog.Error("failed scheduling container update", "err", err)
		}
	}
}

func (t *Controller) scheduleUpdates(ctx context.Context) error {
	containers, err := t.getContainers(ctx)
	if err != nil {
		return fmt.Errorf("failed listing containers: %w", err)
	}

	if len(containers) == 0 {
		slog.Warn("no containers found tagged for updates")
		return nil
	}

	schedules := map[string][]*Container{}
	for _, container := range containers {
		if container.Schedule != "" {
			schedules[container.Schedule] = append(schedules[container.Schedule], container)
		} else {
			schedules[t.defaultSchedule] = append(schedules[t.defaultSchedule], container)
		}
	}

	for _, entry := range t.scheduler.Entries() {
		t.scheduler.Remove(entry.ID)
	}

	for schedule, containers := range schedules {
		slog.Debug("scheduling container update", "schedule", schedule, "containers", containers)
		_, err := t.scheduler.AddFunc(schedule, t.updateContainersJob(ctx, containers))
		if err != nil {
			slog.Error("failed scheduling container update", "schedule", schedule, "containers", containers, "err", err)
			err = errors.Join(err, fmt.Errorf("failed scheduling container update: %w", err))
		}
	}

	return err
}

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
		results = append(results, &Container{
			Summary:      &item,
			KeepOldImage: util.IsTruePtr(item.Labels[labelKeyKeepOldImage]),
			Schedule:     item.Labels[labelKeySchedule],
		})
	}

	return results, nil
}

func (t *Controller) updateContainer(ctx context.Context, container *Container) error {
	containerInspect, err := t.client.ContainerInspect(ctx, container.ID, client.ContainerInspectOptions{})
	if err != nil {
		return err
	}

	image := container.Image
	if strings.HasPrefix(image, "sha256:") {
		image = containerInspect.Container.Config.Image
	}

	if strings.HasPrefix(image, "sha256:") {
		slog.Warn("skipping container with image hash", "id", container.ID, "image", image)
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
	_, _ = t.client.ContainerRename(ctx, container.ID, client.ContainerRenameOptions{
		NewName: oldContainerName + "_old",
	})

	slog.Debug("stopping container", "container", container.ID)
	_, err = t.client.ContainerStop(ctx, container.ID, client.ContainerStopOptions{})
	if err != nil {
		return err
	}

	slog.Debug("removing container", "container", container.ID)
	_, _ = t.client.ContainerRemove(ctx, container.ID, client.ContainerRemoveOptions{Force: true})

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
		_, err = t.client.ImageRemove(ctx, oldImgID, client.ImageRemoveOptions{
			PruneChildren: true,
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (t *Controller) updateContainersJob(ctx context.Context, containers []*Container) func() {
	return func() {
		for _, container := range containers {
			err := t.updateContainer(ctx, container)
			if err != nil {
				slog.Error("failed to update container", "container", container.ID, "error", err)
			}
		}
	}
}
