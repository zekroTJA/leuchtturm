package docker

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testContainerID    = "c1"
	testNewContainerID = "c2"
	testContainerName  = "web"
	testImage          = "nginx:latest"
	testOldImageID     = "sha256:old"
	testNewImageID     = "sha256:new"
)

// updateScenario configures the fake daemon for a container update of testContainerID running
// testImage. The image ID returned on the first image inspect is oldImageID (or 404 when empty)
// and newImageID on all subsequent inspects.
type updateScenario struct {
	oldImageID string
	newImageID string
}

func setupUpdateScenario(t *testing.T, scenario updateScenario) (daemon *fakeDaemon, controller *Controller) {
	t.Helper()

	t.Setenv("DOCKER_CONFIG", t.TempDir())

	daemon = newFakeDaemon(t)

	daemon.handleJSON(http.MethodGet, "/containers/"+testContainerID+"/json", http.StatusOK, map[string]any{
		"Id":   testContainerID,
		"Name": "/" + testContainerName,
		"Config": map[string]any{
			"Image": testImage,
		},
		"HostConfig": map[string]any{},
		"NetworkSettings": map[string]any{
			"Networks": map[string]any{
				"bridge": map[string]any{},
			},
		},
	})

	var imageInspectCount atomic.Int32
	daemon.handle(http.MethodGet, "/images/"+testImage+"/json", func(w http.ResponseWriter, r *http.Request) {
		if imageInspectCount.Add(1) == 1 {
			if scenario.oldImageID == "" {
				writeJSON(w, http.StatusNotFound, map[string]string{"message": "no such image"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"Id": scenario.oldImageID})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"Id": scenario.newImageID})
	})

	daemon.handle(http.MethodPost, "/images/create", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"Pulling from library/nginx"}` + "\n" + `{"status":"Done"}` + "\n"))
	})

	noContent := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }
	daemon.handle(http.MethodPost, "/containers/"+testContainerID+"/rename", noContent)
	daemon.handle(http.MethodPost, "/containers/"+testContainerID+"/stop", noContent)
	daemon.handle(http.MethodDelete, "/containers/"+testContainerID, noContent)
	daemon.handleJSON(http.MethodPost, "/containers/create", http.StatusCreated, map[string]any{
		"Id":       testNewContainerID,
		"Warnings": []string{},
	})
	daemon.handle(http.MethodPost, "/containers/"+testNewContainerID+"/start", noContent)
	daemon.handleJSON(http.MethodDelete, "/images/"+scenario.oldImageID, http.StatusOK, []any{})

	controller = daemon.newController(t, "@daily")

	return daemon, controller
}

func boolPtr(value bool) *bool {
	return &value
}

func TestUpdateContainer(t *testing.T) {
	t.Run("recreates container when a new image is available", func(t *testing.T) {
		daemon, controller := setupUpdateScenario(t, updateScenario{
			oldImageID: testOldImageID,
			newImageID: testNewImageID,
		})

		err := controller.updateContainer(context.Background(), &Container{ID: testContainerID})

		require.NoError(t, err)
		assert.Equal(t, []string{
			"GET /containers/" + testContainerID + "/json",
			"GET /images/" + testImage + "/json",
			"POST /images/create",
			"GET /images/" + testImage + "/json",
			"POST /containers/" + testContainerID + "/rename",
			"POST /containers/" + testContainerID + "/stop",
			"DELETE /containers/" + testContainerID,
			"POST /containers/create",
			"POST /containers/" + testNewContainerID + "/start",
			"DELETE /images/" + testOldImageID,
		}, daemon.requestKeys())

		renameRequest, _ := daemon.findRequest(http.MethodPost, "/containers/"+testContainerID+"/rename")
		assert.Equal(t, []string{testContainerName + "_old"}, renameRequest.Query["name"])

		createRequest, _ := daemon.findRequest(http.MethodPost, "/containers/create")
		assert.Equal(t, []string{testContainerName}, createRequest.Query["name"])

		removeRequest, _ := daemon.findRequest(http.MethodDelete, "/containers/"+testContainerID)
		assert.Equal(t, []string{"1"}, removeRequest.Query["force"])
		assert.NotContains(t, removeRequest.Query, "v")

		pullRequest, _ := daemon.findRequest(http.MethodPost, "/images/create")
		assert.Equal(t, []string{"docker.io/library/nginx"}, pullRequest.Query["fromImage"])
		assert.Equal(t, []string{"latest"}, pullRequest.Query["tag"])
	})

	t.Run("does nothing when the image is up to date", func(t *testing.T) {
		daemon, controller := setupUpdateScenario(t, updateScenario{
			oldImageID: testOldImageID,
			newImageID: testOldImageID,
		})

		err := controller.updateContainer(context.Background(), &Container{ID: testContainerID})

		require.NoError(t, err)
		assert.Equal(t, []string{
			"GET /containers/" + testContainerID + "/json",
			"GET /images/" + testImage + "/json",
			"POST /images/create",
			"GET /images/" + testImage + "/json",
		}, daemon.requestKeys())
	})

	t.Run("skips containers referencing an image by hash", func(t *testing.T) {
		daemon := newFakeDaemon(t)
		daemon.handleJSON(http.MethodGet, "/containers/"+testContainerID+"/json", http.StatusOK, map[string]any{
			"Id":     testContainerID,
			"Config": map[string]any{"Image": "sha256:0123456789abcdef"},
		})
		controller := daemon.newController(t, "@daily")

		err := controller.updateContainer(context.Background(), &Container{ID: testContainerID})

		require.NoError(t, err)
		assert.Equal(t, []string{"GET /containers/" + testContainerID + "/json"}, daemon.requestKeys())
	})

	t.Run("returns error when container inspect fails", func(t *testing.T) {
		daemon := newFakeDaemon(t)
		controller := daemon.newController(t, "@daily")

		err := controller.updateContainer(context.Background(), &Container{ID: "missing"})

		assert.Error(t, err)
		assert.Len(t, daemon.requestKeys(), 1)
	})

	t.Run("returns error and keeps container when pull fails", func(t *testing.T) {
		daemon, controller := setupUpdateScenario(t, updateScenario{
			oldImageID: testOldImageID,
			newImageID: testNewImageID,
		})
		daemon.handleJSON(http.MethodPost, "/images/create", http.StatusInternalServerError,
			map[string]string{"message": "registry unreachable"})

		err := controller.updateContainer(context.Background(), &Container{ID: testContainerID})

		assert.ErrorContains(t, err, "registry unreachable")
		_, stopped := daemon.findRequest(http.MethodPost, "/containers/"+testContainerID+"/stop")
		assert.False(t, stopped)
	})

	t.Run("pull stream error with unchanged image does not recreate container", func(t *testing.T) {
		daemon, controller := setupUpdateScenario(t, updateScenario{
			oldImageID: testOldImageID,
			newImageID: testOldImageID,
		})
		daemon.handle(http.MethodPost, "/images/create", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"errorDetail":{"message":"manifest unknown"},"error":"manifest unknown"}` + "\n"))
		})

		_ = controller.updateContainer(context.Background(), &Container{ID: testContainerID})

		_, stopped := daemon.findRequest(http.MethodPost, "/containers/"+testContainerID+"/stop")
		assert.False(t, stopped)
	})

	t.Run("malformed pull stream yields error", func(t *testing.T) {
		daemon, controller := setupUpdateScenario(t, updateScenario{
			oldImageID: testOldImageID,
			newImageID: testNewImageID,
		})
		daemon.handle(http.MethodPost, "/images/create", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":`))
		})

		err := controller.updateContainer(context.Background(), &Container{ID: testContainerID})

		assert.Error(t, err)
		_, stopped := daemon.findRequest(http.MethodPost, "/containers/"+testContainerID+"/stop")
		assert.False(t, stopped)
	})

	t.Run("pull uses registry auth from docker config", func(t *testing.T) {
		daemon, controller := setupUpdateScenario(t, updateScenario{
			oldImageID: testOldImageID,
			newImageID: testOldImageID,
		})
		dir := t.TempDir()
		writeDockerConfig(t, dir, dockerConfig{
			Auths: map[string]dockerConfigAuth{
				dockerHubRegistry: {Username: "user", Password: "secret"},
			},
		})
		t.Setenv("DOCKER_CONFIG", dir)

		var receivedAuth atomic.Value
		daemon.handle(http.MethodPost, "/images/create", func(w http.ResponseWriter, r *http.Request) {
			receivedAuth.Store(r.Header.Get("X-Registry-Auth"))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"Done"}` + "\n"))
		})

		err := controller.updateContainer(context.Background(), &Container{ID: testContainerID})

		require.NoError(t, err)
		encoded, _ := receivedAuth.Load().(string)
		require.NotEmpty(t, encoded)
		authConfig := decodeAuthConfig(t, encoded)
		assert.Equal(t, "user", authConfig.Username)
		assert.Equal(t, "secret", authConfig.Password)
	})

	t.Run("broken docker config falls back to anonymous pull", func(t *testing.T) {
		daemon, controller := setupUpdateScenario(t, updateScenario{
			oldImageID: testOldImageID,
			newImageID: testOldImageID,
		})
		dir := t.TempDir()
		writeRawDockerConfig(t, dir, "{not json")
		t.Setenv("DOCKER_CONFIG", dir)

		err := controller.updateContainer(context.Background(), &Container{ID: testContainerID})

		require.NoError(t, err)
		_, pulled := daemon.findRequest(http.MethodPost, "/images/create")
		assert.True(t, pulled)
	})

	t.Run("continues when renaming the old container fails", func(t *testing.T) {
		daemon, controller := setupUpdateScenario(t, updateScenario{
			oldImageID: testOldImageID,
			newImageID: testNewImageID,
		})
		daemon.handleJSON(http.MethodPost, "/containers/"+testContainerID+"/rename", http.StatusConflict,
			map[string]string{"message": "name already in use"})

		err := controller.updateContainer(context.Background(), &Container{ID: testContainerID})

		require.NoError(t, err)
		_, started := daemon.findRequest(http.MethodPost, "/containers/"+testNewContainerID+"/start")
		assert.True(t, started)
	})

	t.Run("returns error and does not remove container when stop fails", func(t *testing.T) {
		daemon, controller := setupUpdateScenario(t, updateScenario{
			oldImageID: testOldImageID,
			newImageID: testNewImageID,
		})
		daemon.handleJSON(http.MethodPost, "/containers/"+testContainerID+"/stop", http.StatusInternalServerError,
			map[string]string{"message": "cannot stop"})

		err := controller.updateContainer(context.Background(), &Container{ID: testContainerID})

		assert.ErrorContains(t, err, "cannot stop")
		_, removed := daemon.findRequest(http.MethodDelete, "/containers/"+testContainerID)
		assert.False(t, removed)
	})

	t.Run("returns error and does not create container when remove fails", func(t *testing.T) {
		daemon, controller := setupUpdateScenario(t, updateScenario{
			oldImageID: testOldImageID,
			newImageID: testNewImageID,
		})
		daemon.handleJSON(http.MethodDelete, "/containers/"+testContainerID, http.StatusInternalServerError,
			map[string]string{"message": "cannot remove"})

		err := controller.updateContainer(context.Background(), &Container{ID: testContainerID})

		assert.ErrorContains(t, err, "cannot remove")
		_, created := daemon.findRequest(http.MethodPost, "/containers/create")
		assert.False(t, created)
	})

	t.Run("returns error when create fails", func(t *testing.T) {
		daemon, controller := setupUpdateScenario(t, updateScenario{
			oldImageID: testOldImageID,
			newImageID: testNewImageID,
		})
		daemon.handleJSON(http.MethodPost, "/containers/create", http.StatusConflict,
			map[string]string{"message": "name conflict"})

		err := controller.updateContainer(context.Background(), &Container{ID: testContainerID})

		assert.ErrorContains(t, err, "name conflict")
		_, imageRemoved := daemon.findRequest(http.MethodDelete, "/images/"+testOldImageID)
		assert.False(t, imageRemoved)
	})

	t.Run("returns error when start fails", func(t *testing.T) {
		daemon, controller := setupUpdateScenario(t, updateScenario{
			oldImageID: testOldImageID,
			newImageID: testNewImageID,
		})
		daemon.handleJSON(http.MethodPost, "/containers/"+testNewContainerID+"/start", http.StatusInternalServerError,
			map[string]string{"message": "port already allocated"})

		err := controller.updateContainer(context.Background(), &Container{ID: testContainerID})

		assert.ErrorContains(t, err, "port already allocated")
	})

	t.Run("removes volumes when configured by controller default", func(t *testing.T) {
		daemon, controller := setupUpdateScenario(t, updateScenario{
			oldImageID: testOldImageID,
			newImageID: testNewImageID,
		})
		controller.removeVolumes = true

		require.NoError(t, controller.updateContainer(context.Background(), &Container{ID: testContainerID}))

		removeRequest, _ := daemon.findRequest(http.MethodDelete, "/containers/"+testContainerID)
		assert.Equal(t, []string{"1"}, removeRequest.Query["v"])
	})

	t.Run("container label overrides remove volumes default", func(t *testing.T) {
		daemon, controller := setupUpdateScenario(t, updateScenario{
			oldImageID: testOldImageID,
			newImageID: testNewImageID,
		})
		controller.removeVolumes = true

		require.NoError(t, controller.updateContainer(context.Background(), &Container{
			ID:            testContainerID,
			RemoveVolumes: boolPtr(false),
		}))

		removeRequest, _ := daemon.findRequest(http.MethodDelete, "/containers/"+testContainerID)
		assert.NotContains(t, removeRequest.Query, "v")
	})

	t.Run("keep old image retains the previous image", func(t *testing.T) {
		daemon, controller := setupUpdateScenario(t, updateScenario{
			oldImageID: testOldImageID,
			newImageID: testNewImageID,
		})
		controller.keepOldImage = true

		require.NoError(t, controller.updateContainer(context.Background(), &Container{ID: testContainerID}))

		_, imageRemoved := daemon.findRequest(http.MethodDelete, "/images/"+testOldImageID)
		assert.False(t, imageRemoved, "old image must not be removed when keep-old-image is enabled")
	})

	t.Run("keep old image label overrides controller default", func(t *testing.T) {
		daemon, controller := setupUpdateScenario(t, updateScenario{
			oldImageID: testOldImageID,
			newImageID: testNewImageID,
		})
		controller.keepOldImage = false

		require.NoError(t, controller.updateContainer(context.Background(), &Container{
			ID:           testContainerID,
			KeepOldImage: boolPtr(true),
		}))

		_, imageRemoved := daemon.findRequest(http.MethodDelete, "/images/"+testOldImageID)
		assert.False(t, imageRemoved, "old image must not be removed when keep-old-image label is set")
	})

	t.Run("updates container whose image was not present locally", func(t *testing.T) {
		daemon, controller := setupUpdateScenario(t, updateScenario{
			oldImageID: "",
			newImageID: testNewImageID,
		})

		err := controller.updateContainer(context.Background(), &Container{ID: testContainerID})

		assert.NoError(t, err, "there is no old image to remove, so the update must not fail")
		_, started := daemon.findRequest(http.MethodPost, "/containers/"+testNewContainerID+"/start")
		assert.True(t, started)
	})

	t.Run("canceled context aborts update", func(t *testing.T) {
		daemon, controller := setupUpdateScenario(t, updateScenario{
			oldImageID: testOldImageID,
			newImageID: testNewImageID,
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := controller.updateContainer(ctx, &Container{ID: testContainerID})

		assert.ErrorIs(t, err, context.Canceled)
		assert.Empty(t, daemon.requestKeys())
	})
}

func TestUpdateContainerJob(t *testing.T) {
	daemon, controller := setupUpdateScenario(t, updateScenario{
		oldImageID: testOldImageID,
		newImageID: testOldImageID,
	})

	job := controller.updateContainerJob(context.Background(), &Container{ID: testContainerID})
	job()

	_, pulled := daemon.findRequest(http.MethodPost, "/images/create")
	assert.True(t, pulled)

	assert.NotPanics(t, controller.updateContainerJob(context.Background(), &Container{ID: "missing"}))
}
