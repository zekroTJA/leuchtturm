package docker

import (
	"context"
	"log/slog"
	"strings"

	"github.com/k0kubun/pp/v3"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/zekrotja/leuchtturm/pkg/util"
)

const lableKeyEnable = "leuchtturm.enable"

type Controller struct {
	client *client.Client
}

func New() (*Controller, error) {
	client, err := client.New(client.FromEnv)
	if err != nil {
		return nil, err
	}

	t := &Controller{
		client: client,
	}

	return t, nil
}

func (t *Controller) Close() error {
	return t.client.Close()
}

func (t *Controller) UpdateAll(ctx context.Context) error {
	list, err := t.client.ContainerList(ctx, client.ContainerListOptions{})
	if err != nil {
		return err
	}

	for _, item := range list.Items {
		if !util.IsTrue(item.Labels[lableKeyEnable]) {
			continue
		}

		pp.Println(item)

		err := t.Update(ctx, &item)
		if err != nil {
			return err
		}

	}

	return nil
}

func (t *Controller) Update(ctx context.Context, csum *container.Summary) error {
	cinspect, err := t.client.ContainerInspect(ctx, csum.ID, client.ContainerInspectOptions{})
	if err != nil {
		return err
	}

	image := csum.Image
	if strings.HasPrefix(image, "sha256:") {
		image = cinspect.Container.Config.Image
	}

	if strings.HasPrefix(image, "sha256:") {
		slog.Warn("skipping container with image hash", "id", csum.ID, "image", image)
		return nil
	}

	imgInspect, err := t.client.ImageInspect(ctx, image)
	var oldID string
	if err == nil {
		oldID = imgInspect.ID
	}

	resp, err := t.client.ImagePull(ctx, image, client.ImagePullOptions{})
	if err != nil {
		return err
	}
	if err = resp.Wait(ctx); err != nil {
		return err
	}

	imgInspect, err = t.client.ImageInspect(ctx, image)
	if err != nil {
		return err
	}

	if oldID == imgInspect.ID {
		slog.Info("image is up to date", "container", csum.ID, "image", image)
		return nil
	}

	slog.Info("updating image", "container", csum.ID, "image", image)

	oldName := strings.TrimPrefix(cinspect.Container.Name, "/")
	slog.Debug("renaming old container", "container", csum.ID, "oldName", oldName)
	_, _ = t.client.ContainerRename(ctx, csum.ID, client.ContainerRenameOptions{
		NewName: oldName + "_old",
	})

	slog.Debug("stopping container", "container", csum.ID)
	_, err = t.client.ContainerStop(ctx, csum.ID, client.ContainerStopOptions{})
	if err != nil {
		return err
	}

	slog.Debug("removing container", "container", csum.ID)
	_, _ = t.client.ContainerRemove(ctx, csum.ID, client.ContainerRemoveOptions{Force: true})

	slog.Debug("re-creating container", "container", csum.ID)
	createResp, err := t.client.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:     cinspect.Container.Config,
		HostConfig: cinspect.Container.HostConfig,
		NetworkingConfig: &network.NetworkingConfig{
			EndpointsConfig: cinspect.Container.NetworkSettings.Networks,
		},
		Name: oldName,
	})
	if err != nil {
		return err
	}

	slog.Debug("starting container", "container", createResp.ID)
	_, err = t.client.ContainerStart(ctx, createResp.ID, client.ContainerStartOptions{})
	return err
}
