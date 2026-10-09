package docker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/moby/moby/api/types/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryFromImage(t *testing.T) {
	tests := []struct {
		name     string
		image    string
		expected string
	}{
		{"single name defaults to docker hub", "nginx", dockerHubRegistry},
		{"official namespaced defaults to docker hub", "library/nginx", dockerHubRegistry},
		{"user image defaults to docker hub", "zekrotja/leuchtturm:latest", dockerHubRegistry},
		{"private registry with domain", "registry.example.com/app:latest", "registry.example.com"},
		{"private registry with port", "registry.example.com:5000/app", "registry.example.com:5000"},
		{"localhost registry", "localhost/app:latest", "localhost"},
		{"localhost with port", "localhost:5000/app", "localhost:5000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, registryFromImage(tt.image))
		})
	}
}

func TestDockerConfigAuthToAuthConfig(t *testing.T) {
	t.Run("decodes base64 auth into username and password", func(t *testing.T) {
		encoded := base64.StdEncoding.EncodeToString([]byte("user:secret"))
		auth := dockerConfigAuth{Auth: encoded}

		result, err := auth.toAuthConfig("registry.example.com")
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "user", result.Username)
		assert.Equal(t, "secret", result.Password)
		assert.Equal(t, "registry.example.com", result.ServerAddress)
	})

	t.Run("uses explicit username and password", func(t *testing.T) {
		auth := dockerConfigAuth{Username: "user", Password: "secret"}

		result, err := auth.toAuthConfig("registry.example.com")
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "user", result.Username)
		assert.Equal(t, "secret", result.Password)
	})

	t.Run("empty entry yields nil", func(t *testing.T) {
		auth := dockerConfigAuth{}

		result, err := auth.toAuthConfig("registry.example.com")
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("invalid base64 auth yields error", func(t *testing.T) {
		auth := dockerConfigAuth{Auth: "not-base64!!!"}

		_, err := auth.toAuthConfig("registry.example.com")
		assert.Error(t, err)
	})

	t.Run("auth without colon yields error", func(t *testing.T) {
		auth := dockerConfigAuth{Auth: base64.StdEncoding.EncodeToString([]byte("nocolon"))}

		_, err := auth.toAuthConfig("registry.example.com")
		assert.Error(t, err)
	})
}

func TestEncodeAuthConfig(t *testing.T) {
	authConfig := &registry.AuthConfig{
		Username:      "user",
		Password:      "secret",
		ServerAddress: "registry.example.com",
	}

	encoded, err := encodeAuthConfig(authConfig)
	require.NoError(t, err)

	decoded, err := base64.URLEncoding.DecodeString(encoded)
	require.NoError(t, err)

	var roundtrip registry.AuthConfig
	require.NoError(t, json.Unmarshal(decoded, &roundtrip))
	assert.Equal(t, *authConfig, roundtrip)
}

func TestResolveRegistryAuth(t *testing.T) {
	t.Run("no config file yields empty auth", func(t *testing.T) {
		t.Setenv("DOCKER_CONFIG", t.TempDir())

		encoded, err := resolveRegistryAuth(context.Background(), "registry.example.com/app:latest")
		require.NoError(t, err)
		assert.Empty(t, encoded)
	})

	t.Run("resolves credentials for matching registry", func(t *testing.T) {
		dir := t.TempDir()
		writeDockerConfig(t, dir, dockerConfig{
			Auths: map[string]dockerConfigAuth{
				"registry.example.com": {
					Auth: base64.StdEncoding.EncodeToString([]byte("user:secret")),
				},
			},
		})
		t.Setenv("DOCKER_CONFIG", dir)

		encoded, err := resolveRegistryAuth(context.Background(), "registry.example.com/app:latest")
		require.NoError(t, err)
		require.NotEmpty(t, encoded)

		authConfig := decodeAuthConfig(t, encoded)
		assert.Equal(t, "user", authConfig.Username)
		assert.Equal(t, "secret", authConfig.Password)
		assert.Equal(t, "registry.example.com", authConfig.ServerAddress)
	})

	t.Run("resolves docker hub credentials for unqualified image", func(t *testing.T) {
		dir := t.TempDir()
		writeDockerConfig(t, dir, dockerConfig{
			Auths: map[string]dockerConfigAuth{
				dockerHubRegistry: {
					Auth: base64.StdEncoding.EncodeToString([]byte("hubuser:hubsecret")),
				},
			},
		})
		t.Setenv("DOCKER_CONFIG", dir)

		encoded, err := resolveRegistryAuth(context.Background(), "zekrotja/leuchtturm:latest")
		require.NoError(t, err)
		require.NotEmpty(t, encoded)

		authConfig := decodeAuthConfig(t, encoded)
		assert.Equal(t, "hubuser", authConfig.Username)
		assert.Equal(t, "hubsecret", authConfig.Password)
	})

	t.Run("no credentials for registry yields empty auth", func(t *testing.T) {
		dir := t.TempDir()
		writeDockerConfig(t, dir, dockerConfig{
			Auths: map[string]dockerConfigAuth{
				"other.example.com": {
					Auth: base64.StdEncoding.EncodeToString([]byte("user:secret")),
				},
			},
		})
		t.Setenv("DOCKER_CONFIG", dir)

		encoded, err := resolveRegistryAuth(context.Background(), "registry.example.com/app:latest")
		require.NoError(t, err)
		assert.Empty(t, encoded)
	})
}

func writeDockerConfig(t *testing.T, dir string, config dockerConfig) {
	t.Helper()

	data, err := json.Marshal(config)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600))
}

func decodeAuthConfig(t *testing.T, encoded string) registry.AuthConfig {
	t.Helper()

	decoded, err := base64.URLEncoding.DecodeString(encoded)
	require.NoError(t, err)

	var authConfig registry.AuthConfig
	require.NoError(t, json.Unmarshal(decoded, &authConfig))

	return authConfig
}
