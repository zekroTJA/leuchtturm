package docker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

func writeRawDockerConfig(t *testing.T, dir string, content string) {
	t.Helper()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o600))
}

// installCredentialHelper creates a fake `docker-credential-<name>` executable which writes the
// given stdout and stderr and exits with the given exit code. The received stdin is written to
// a file next to the executable whose path is returned. The helper directory is prepended to PATH.
func installCredentialHelper(t *testing.T, name string, stdout string, stderr string, exitCode int) (stdinPath string) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("fake credential helpers require a POSIX shell")
	}

	dir := t.TempDir()
	stdinPath = filepath.Join(dir, "stdin")
	script := fmt.Sprintf("#!/bin/sh\n[ \"$1\" = \"get\" ] || exit 99\ncat > %q\nprintf '%%s' %q\nprintf '%%s' %q >&2\nexit %d\n",
		stdinPath, stdout, stderr, exitCode)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker-credential-"+name), []byte(script), 0o755))

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return stdinPath
}

func TestRegistryFromImageEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		image    string
		expected string
	}{
		{"empty image", "", dockerHubRegistry},
		{"image with digest", "nginx@sha256:abcdef", dockerHubRegistry},
		{"namespaced image with digest", "registry.example.com/app@sha256:abcdef", "registry.example.com"},
		{"nested repository path", "ghcr.io/org/team/app:1.0", "ghcr.io"},
		{"ipv4 registry with port", "127.0.0.1:5000/app", "127.0.0.1:5000"},
		{"namespace with dash and underscore", "my-org_x/app", dockerHubRegistry},
		{"uppercase localhost is not treated as registry", "LOCALHOST/app", dockerHubRegistry},
		{"fully qualified docker hub image", "docker.io/library/nginx:latest", dockerHubRegistry},
		{"fully qualified docker hub index image", "index.docker.io/library/nginx", dockerHubRegistry},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, registryFromImage(tt.image))
		})
	}
}

func TestCandidateAuthKeys(t *testing.T) {
	t.Run("docker hub includes legacy aliases", func(t *testing.T) {
		assert.Equal(t,
			[]string{dockerHubRegistry, "index.docker.io", "registry-1.docker.io", "docker.io"},
			candidateAuthKeys(dockerHubRegistry))
	})

	t.Run("custom registry includes scheme variants", func(t *testing.T) {
		assert.Equal(t,
			[]string{"registry.example.com", "https://registry.example.com", "http://registry.example.com"},
			candidateAuthKeys("registry.example.com"))
	})
}

func TestDockerConfigPath(t *testing.T) {
	t.Run("honors DOCKER_CONFIG", func(t *testing.T) {
		t.Setenv("DOCKER_CONFIG", "/custom/docker")
		assert.Equal(t, filepath.Join("/custom/docker", "config.json"), dockerConfigPath())
	})

	t.Run("falls back to home directory", func(t *testing.T) {
		if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
			t.Skip("HOME based lookup only tested on unix")
		}
		t.Setenv("DOCKER_CONFIG", "")
		t.Setenv("HOME", "/home/tester")
		assert.Equal(t, filepath.Join("/home/tester", ".docker", "config.json"), dockerConfigPath())
	})

	t.Run("empty when home is unknown", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("HOME based lookup only tested on linux")
		}
		t.Setenv("DOCKER_CONFIG", "")
		t.Setenv("HOME", "")
		assert.Empty(t, dockerConfigPath())
	})
}

func TestLoadDockerConfig(t *testing.T) {
	t.Run("missing file yields nil without error", func(t *testing.T) {
		t.Setenv("DOCKER_CONFIG", t.TempDir())

		config, err := loadDockerConfig()

		require.NoError(t, err)
		assert.Nil(t, config)
	})

	t.Run("missing config directory yields nil without error", func(t *testing.T) {
		t.Setenv("DOCKER_CONFIG", filepath.Join(t.TempDir(), "does", "not", "exist"))

		config, err := loadDockerConfig()

		require.NoError(t, err)
		assert.Nil(t, config)
	})

	t.Run("unknown home yields nil without error", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("HOME based lookup only tested on linux")
		}
		t.Setenv("DOCKER_CONFIG", "")
		t.Setenv("HOME", "")

		config, err := loadDockerConfig()

		require.NoError(t, err)
		assert.Nil(t, config)
	})

	t.Run("parses all relevant sections", func(t *testing.T) {
		dir := t.TempDir()
		writeRawDockerConfig(t, dir, `{
			"auths": {"registry.example.com": {"auth": "dXNlcjpzZWNyZXQ="}},
			"credsStore": "desktop",
			"credHelpers": {"gcr.io": "gcloud"},
			"currentContext": "default"
		}`)
		t.Setenv("DOCKER_CONFIG", dir)

		config, err := loadDockerConfig()

		require.NoError(t, err)
		require.NotNil(t, config)
		assert.Equal(t, "dXNlcjpzZWNyZXQ=", config.Auths["registry.example.com"].Auth)
		assert.Equal(t, "desktop", config.CredsStore)
		assert.Equal(t, map[string]string{"gcr.io": "gcloud"}, config.CredHelpers)
	})

	t.Run("empty object is valid", func(t *testing.T) {
		dir := t.TempDir()
		writeRawDockerConfig(t, dir, `{}`)
		t.Setenv("DOCKER_CONFIG", dir)

		config, err := loadDockerConfig()

		require.NoError(t, err)
		require.NotNil(t, config)
		assert.Empty(t, config.Auths)
	})

	t.Run("invalid JSON yields error", func(t *testing.T) {
		dir := t.TempDir()
		writeRawDockerConfig(t, dir, `{"auths": `)
		t.Setenv("DOCKER_CONFIG", dir)

		config, err := loadDockerConfig()

		assert.ErrorContains(t, err, "failed parsing docker config")
		assert.Nil(t, config)
	})

	t.Run("empty file yields error", func(t *testing.T) {
		dir := t.TempDir()
		writeRawDockerConfig(t, dir, ``)
		t.Setenv("DOCKER_CONFIG", dir)

		_, err := loadDockerConfig()

		assert.Error(t, err)
	})

	t.Run("wrong field types yield error", func(t *testing.T) {
		dir := t.TempDir()
		writeRawDockerConfig(t, dir, `{"auths": ["registry.example.com"]}`)
		t.Setenv("DOCKER_CONFIG", dir)

		_, err := loadDockerConfig()

		assert.Error(t, err)
	})

	t.Run("unreadable path yields error", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dir, "config.json"), 0o755))
		t.Setenv("DOCKER_CONFIG", dir)

		config, err := loadDockerConfig()

		assert.ErrorContains(t, err, "failed reading docker config")
		assert.Nil(t, config)
	})
}

func TestDockerConfigAuthToAuthConfigEdgeCases(t *testing.T) {
	t.Run("password containing colons is preserved", func(t *testing.T) {
		auth := dockerConfigAuth{Auth: base64.StdEncoding.EncodeToString([]byte("user:se:cr:et"))}

		result, err := auth.toAuthConfig("registry.example.com")

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "user", result.Username)
		assert.Equal(t, "se:cr:et", result.Password)
	})

	t.Run("auth takes precedence over explicit username and password", func(t *testing.T) {
		auth := dockerConfigAuth{
			Auth:     base64.StdEncoding.EncodeToString([]byte("authuser:authsecret")),
			Username: "user",
			Password: "secret",
		}

		result, err := auth.toAuthConfig("registry.example.com")

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "authuser", result.Username)
		assert.Equal(t, "authsecret", result.Password)
	})

	t.Run("identity token only is usable", func(t *testing.T) {
		auth := dockerConfigAuth{IdentityToken: "token"}

		result, err := auth.toAuthConfig("registry.example.com")

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "token", result.IdentityToken)
		assert.Empty(t, result.Username)
	})

	t.Run("auth decoding to only a colon yields nil", func(t *testing.T) {
		auth := dockerConfigAuth{Auth: base64.StdEncoding.EncodeToString([]byte(":"))}

		result, err := auth.toAuthConfig("registry.example.com")

		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("url encoded base64 auth yields error", func(t *testing.T) {
		// "??>" encodes to "Pz8+" in std and "Pz8-" in url encoding.
		auth := dockerConfigAuth{Auth: base64.URLEncoding.EncodeToString([]byte("u:??>"))}

		_, err := auth.toAuthConfig("registry.example.com")

		assert.Error(t, err)
	})

	t.Run("error does not leak the credentials", func(t *testing.T) {
		auth := dockerConfigAuth{Auth: base64.StdEncoding.EncodeToString([]byte("supersecretvalue"))}

		_, err := auth.toAuthConfig("registry.example.com")

		require.Error(t, err)
		assert.NotContains(t, err.Error(), "supersecretvalue")
	})
}

func TestRunCredentialHelper(t *testing.T) {
	t.Run("returns username and password", func(t *testing.T) {
		stdinPath := installCredentialHelper(t, "fake",
			`{"ServerURL":"registry.example.com","Username":"user","Secret":"secret"}`, "", 0)

		result, err := runCredentialHelper(context.Background(), "fake", "registry.example.com")

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "user", result.Username)
		assert.Equal(t, "secret", result.Password)
		assert.Empty(t, result.IdentityToken)
		assert.Equal(t, "registry.example.com", result.ServerAddress)

		stdin, err := os.ReadFile(stdinPath)
		require.NoError(t, err)
		assert.Equal(t, "registry.example.com", string(stdin))
	})

	t.Run("token username yields identity token", func(t *testing.T) {
		installCredentialHelper(t, "fake",
			`{"ServerURL":"registry.example.com","Username":"<token>","Secret":"refresh-token"}`, "", 0)

		result, err := runCredentialHelper(context.Background(), "fake", "registry.example.com")

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "refresh-token", result.IdentityToken)
		assert.Empty(t, result.Username)
		assert.Empty(t, result.Password)
	})

	t.Run("empty credentials yield nil", func(t *testing.T) {
		installCredentialHelper(t, "fake", `{"ServerURL":"registry.example.com","Username":"","Secret":""}`, "", 0)

		result, err := runCredentialHelper(context.Background(), "fake", "registry.example.com")

		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("credentials not found yields nil", func(t *testing.T) {
		installCredentialHelper(t, "fake", "credentials not found in native keychain", "", 1)

		result, err := runCredentialHelper(context.Background(), "fake", "registry.example.com")

		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("no credentials on stderr yields nil", func(t *testing.T) {
		installCredentialHelper(t, "fake", "", "No Credentials for this server", 1)

		result, err := runCredentialHelper(context.Background(), "fake", "registry.example.com")

		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("other helper failure yields error", func(t *testing.T) {
		installCredentialHelper(t, "fake", "", "keychain locked", 1)

		result, err := runCredentialHelper(context.Background(), "fake", "registry.example.com")

		assert.ErrorContains(t, err, "docker-credential-fake")
		assert.Nil(t, result)
	})

	t.Run("invalid output yields error", func(t *testing.T) {
		installCredentialHelper(t, "fake", "this is not json", "", 0)

		result, err := runCredentialHelper(context.Background(), "fake", "registry.example.com")

		assert.ErrorContains(t, err, "failed parsing output")
		assert.Nil(t, result)
	})

	t.Run("missing helper binary yields error", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())

		result, err := runCredentialHelper(context.Background(), "does-not-exist", "registry.example.com")

		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("canceled context yields error", func(t *testing.T) {
		installCredentialHelper(t, "fake", `{"Username":"user","Secret":"secret"}`, "", 0)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := runCredentialHelper(ctx, "fake", "registry.example.com")

		assert.Error(t, err)
	})
}

func TestAuthConfigFor(t *testing.T) {
	t.Run("credential helper takes precedence over creds store and auths", func(t *testing.T) {
		installCredentialHelper(t, "helper", `{"Username":"helperuser","Secret":"helpersecret"}`, "", 0)
		installCredentialHelper(t, "store", `{"Username":"storeuser","Secret":"storesecret"}`, "", 0)
		config := &dockerConfig{
			CredHelpers: map[string]string{"registry.example.com": "helper"},
			CredsStore:  "store",
			Auths: map[string]dockerConfigAuth{
				"registry.example.com": {Username: "authuser", Password: "authsecret"},
			},
		}

		result, err := config.authConfigFor(context.Background(), "registry.example.com")

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "helperuser", result.Username)
	})

	t.Run("credential helper for other registry is ignored", func(t *testing.T) {
		installCredentialHelper(t, "store", `{"Username":"storeuser","Secret":"storesecret"}`, "", 0)
		config := &dockerConfig{
			CredHelpers: map[string]string{"other.example.com": "does-not-exist"},
			CredsStore:  "store",
		}

		result, err := config.authConfigFor(context.Background(), "registry.example.com")

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "storeuser", result.Username)
	})

	t.Run("empty credential helper name falls through", func(t *testing.T) {
		config := &dockerConfig{
			CredHelpers: map[string]string{"registry.example.com": ""},
			Auths: map[string]dockerConfigAuth{
				"registry.example.com": {Username: "authuser", Password: "authsecret"},
			},
		}

		result, err := config.authConfigFor(context.Background(), "registry.example.com")

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "authuser", result.Username)
	})

	t.Run("credential helper without credentials does not fall back", func(t *testing.T) {
		installCredentialHelper(t, "helper", "credentials not found", "", 1)
		config := &dockerConfig{
			CredHelpers: map[string]string{"registry.example.com": "helper"},
			Auths: map[string]dockerConfigAuth{
				"registry.example.com": {Username: "authuser", Password: "authsecret"},
			},
		}

		result, err := config.authConfigFor(context.Background(), "registry.example.com")

		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("creds store without credentials falls back to auths", func(t *testing.T) {
		installCredentialHelper(t, "store", "credentials not found", "", 1)
		config := &dockerConfig{
			CredsStore: "store",
			Auths: map[string]dockerConfigAuth{
				"https://registry.example.com": {Username: "authuser", Password: "authsecret"},
			},
		}

		result, err := config.authConfigFor(context.Background(), "registry.example.com")

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "authuser", result.Username)
		assert.Equal(t, "registry.example.com", result.ServerAddress)
	})

	t.Run("creds store failure yields error", func(t *testing.T) {
		installCredentialHelper(t, "store", "", "boom", 2)
		config := &dockerConfig{
			CredsStore: "store",
			Auths: map[string]dockerConfigAuth{
				"registry.example.com": {Username: "authuser", Password: "authsecret"},
			},
		}

		result, err := config.authConfigFor(context.Background(), "registry.example.com")

		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("matches docker hub legacy keys", func(t *testing.T) {
		for _, key := range []string{"index.docker.io", "registry-1.docker.io", "docker.io"} {
			t.Run(key, func(t *testing.T) {
				config := &dockerConfig{
					Auths: map[string]dockerConfigAuth{key: {Username: "hubuser", Password: "hubsecret"}},
				}

				result, err := config.authConfigFor(context.Background(), dockerHubRegistry)

				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Equal(t, "hubuser", result.Username)
				assert.Equal(t, dockerHubRegistry, result.ServerAddress)
			})
		}
	})

	t.Run("prefers exact key over scheme variants", func(t *testing.T) {
		config := &dockerConfig{
			Auths: map[string]dockerConfigAuth{
				"http://registry.example.com": {Username: "httpuser", Password: "secret"},
				"registry.example.com":        {Username: "exactuser", Password: "secret"},
			},
		}

		result, err := config.authConfigFor(context.Background(), "registry.example.com")

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "exactuser", result.Username)
	})

	t.Run("registry port must match", func(t *testing.T) {
		config := &dockerConfig{
			Auths: map[string]dockerConfigAuth{
				"registry.example.com": {Username: "user", Password: "secret"},
			},
		}

		result, err := config.authConfigFor(context.Background(), "registry.example.com:5000")

		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("invalid auth entry yields error", func(t *testing.T) {
		config := &dockerConfig{
			Auths: map[string]dockerConfigAuth{
				"registry.example.com": {Auth: "%%%"},
			},
		}

		_, err := config.authConfigFor(context.Background(), "registry.example.com")

		assert.Error(t, err)
	})

	t.Run("empty config yields nil", func(t *testing.T) {
		config := &dockerConfig{}

		result, err := config.authConfigFor(context.Background(), "registry.example.com")

		require.NoError(t, err)
		assert.Nil(t, result)
	})
}

func TestResolveRegistryAuthErrors(t *testing.T) {
	t.Run("invalid config yields error", func(t *testing.T) {
		dir := t.TempDir()
		writeRawDockerConfig(t, dir, "not json")
		t.Setenv("DOCKER_CONFIG", dir)

		encoded, err := resolveRegistryAuth(context.Background(), "registry.example.com/app")

		assert.Error(t, err)
		assert.Empty(t, encoded)
	})

	t.Run("invalid auth entry yields error", func(t *testing.T) {
		dir := t.TempDir()
		writeDockerConfig(t, dir, dockerConfig{
			Auths: map[string]dockerConfigAuth{"registry.example.com": {Auth: "!!!"}},
		})
		t.Setenv("DOCKER_CONFIG", dir)

		encoded, err := resolveRegistryAuth(context.Background(), "registry.example.com/app")

		assert.Error(t, err)
		assert.Empty(t, encoded)
	})

	t.Run("resolves credentials via creds store", func(t *testing.T) {
		installCredentialHelper(t, "store", `{"Username":"<token>","Secret":"identity"}`, "", 0)
		dir := t.TempDir()
		writeDockerConfig(t, dir, dockerConfig{CredsStore: "store"})
		t.Setenv("DOCKER_CONFIG", dir)

		encoded, err := resolveRegistryAuth(context.Background(), "ghcr.io/org/app:1.0")

		require.NoError(t, err)
		authConfig := decodeAuthConfig(t, encoded)
		assert.Equal(t, "identity", authConfig.IdentityToken)
		assert.Equal(t, "ghcr.io", authConfig.ServerAddress)
	})
}

func TestEncodeAuthConfigIsURLSafe(t *testing.T) {
	encoded, err := encodeAuthConfig(&registry.AuthConfig{
		Username: "user",
		Password: "??>>??>>~~~",
	})

	require.NoError(t, err)
	assert.NotContains(t, encoded, "+")
	assert.NotContains(t, encoded, "/")

	authConfig := decodeAuthConfig(t, encoded)
	assert.Equal(t, "??>>??>>~~~", authConfig.Password)
}
