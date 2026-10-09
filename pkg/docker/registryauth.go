package docker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/moby/moby/api/types/registry"
)

// dockerHubRegistry is the server address under which Docker Hub credentials are stored in the
// Docker config file and expected by credential helpers.
const dockerHubRegistry = "https://index.docker.io/v1/"

// credentialTokenUsername is the sentinel username returned by credential helpers to signal that
// the returned secret is an identity token instead of a password.
const credentialTokenUsername = "<token>"

// dockerConfig represents the subset of the Docker CLI configuration file (config.json) which is
// relevant for resolving registry credentials.
type dockerConfig struct {
	Auths       map[string]dockerConfigAuth `json:"auths"`
	CredsStore  string                      `json:"credsStore"`
	CredHelpers map[string]string           `json:"credHelpers"`
}

// dockerConfigAuth represents a single entry of the "auths" section of the Docker config file.
type dockerConfigAuth struct {
	Auth          string `json:"auth"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	IdentityToken string `json:"identitytoken"`
}

// credentialHelperResponse represents the JSON output of a `docker-credential-*` helper on a `get`
// request.
type credentialHelperResponse struct {
	ServerURL string `json:"ServerURL"`
	Username  string `json:"Username"`
	Secret    string `json:"Secret"`
}

// resolveRegistryAuth resolves the registry credentials for the given image reference from the
// Docker CLI configuration (config.json) and credential helpers as configured by `docker login`.
//
// The returned string is the base64url encoded authentication configuration ready to be passed as
// RegistryAuth to the Docker client. An empty string is returned when no credentials are configured
// for the image's registry, which causes an anonymous pull.
func resolveRegistryAuth(ctx context.Context, image string) (encodedAuth string, err error) {
	registryHost := registryFromImage(image)

	config, err := loadDockerConfig()
	if err != nil {
		return "", err
	}
	if config == nil {
		return "", nil
	}

	authConfig, err := config.authConfigFor(ctx, registryHost)
	if err != nil {
		return "", err
	}
	if authConfig == nil {
		return "", nil
	}

	return encodeAuthConfig(authConfig)
}

// registryFromImage extracts the registry server address from an image reference. If the reference
// does not contain an explicit registry, the Docker Hub registry address is returned.
func registryFromImage(image string) string {
	slashIdx := strings.IndexRune(image, '/')
	if slashIdx == -1 {
		return dockerHubRegistry
	}

	host := image[:slashIdx]
	if host != "localhost" && !strings.ContainsAny(host, ".:") {
		return dockerHubRegistry
	}

	return host
}

// loadDockerConfig reads and parses the Docker CLI configuration file. nil is returned (without an
// error) when no configuration file exists.
func loadDockerConfig() (config *dockerConfig, err error) {
	path := dockerConfigPath()
	if path == "" {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed reading docker config %q: %w", path, err)
	}

	var parsed dockerConfig
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("failed parsing docker config %q: %w", path, err)
	}

	slog.Debug("loaded docker config", "path", path)

	return &parsed, nil
}

// dockerConfigPath returns the path to the Docker CLI configuration file honoring the DOCKER_CONFIG
// environment variable and falling back to the user's home directory.
func dockerConfigPath() string {
	if dir := os.Getenv("DOCKER_CONFIG"); dir != "" {
		return filepath.Join(dir, "config.json")
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}

	return filepath.Join(home, ".docker", "config.json")
}

// authConfigFor resolves the authentication configuration for the given registry host. Credential
// helpers take precedence over a configured credential store which in turn takes precedence over
// statically stored credentials in the "auths" section. nil is returned when no credentials are
// configured for the registry.
func (t *dockerConfig) authConfigFor(ctx context.Context, registryHost string) (authConfig *registry.AuthConfig, err error) {
	if helper, ok := t.CredHelpers[registryHost]; ok && helper != "" {
		return runCredentialHelper(ctx, helper, registryHost)
	}

	if t.CredsStore != "" {
		authConfig, err = runCredentialHelper(ctx, t.CredsStore, registryHost)
		if err != nil {
			return nil, err
		}
		if authConfig != nil {
			return authConfig, nil
		}
	}

	for _, key := range candidateAuthKeys(registryHost) {
		if auth, ok := t.Auths[key]; ok {
			return auth.toAuthConfig(registryHost)
		}
	}

	return nil, nil
}

// candidateAuthKeys returns the list of possible keys under which credentials for the given registry
// host may be stored in the "auths" section of the Docker config file.
func candidateAuthKeys(registryHost string) []string {
	if registryHost == dockerHubRegistry {
		return []string{dockerHubRegistry, "index.docker.io", "registry-1.docker.io", "docker.io"}
	}

	return []string{registryHost, "https://" + registryHost, "http://" + registryHost}
}

// toAuthConfig converts a statically stored credential entry into an AuthConfig. nil is returned
// when the entry does not contain any usable credentials.
func (t dockerConfigAuth) toAuthConfig(serverAddress string) (authConfig *registry.AuthConfig, err error) {
	result := &registry.AuthConfig{
		Username:      t.Username,
		Password:      t.Password,
		ServerAddress: serverAddress,
		IdentityToken: t.IdentityToken,
	}

	if t.Auth != "" {
		decoded, err := base64.StdEncoding.DecodeString(t.Auth)
		if err != nil {
			return nil, fmt.Errorf("failed decoding auth for %q: %w", serverAddress, err)
		}
		username, password, ok := strings.Cut(string(decoded), ":")
		if !ok {
			return nil, fmt.Errorf("invalid auth format for %q", serverAddress)
		}
		result.Username = username
		result.Password = password
	}

	if result.Username == "" && result.Password == "" && result.IdentityToken == "" {
		return nil, nil
	}

	return result, nil
}

// runCredentialHelper invokes the given Docker credential helper to resolve the credentials for the
// given registry host. nil is returned (without an error) when the helper reports that no
// credentials are stored for the registry.
func runCredentialHelper(ctx context.Context, helper string, registryHost string) (authConfig *registry.AuthConfig, err error) {
	binary := "docker-credential-" + helper

	cmd := exec.CommandContext(ctx, binary, "get")
	cmd.Stdin = strings.NewReader(registryHost)

	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			message := strings.ToLower(string(exitErr.Stderr) + string(output))
			if strings.Contains(message, "not found") || strings.Contains(message, "no credentials") {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("credential helper %q failed: %w", binary, err)
	}

	var response credentialHelperResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("failed parsing output of credential helper %q: %w", binary, err)
	}

	if response.Username == "" && response.Secret == "" {
		return nil, nil
	}

	result := &registry.AuthConfig{
		ServerAddress: registryHost,
	}
	if response.Username == credentialTokenUsername {
		result.IdentityToken = response.Secret
	} else {
		result.Username = response.Username
		result.Password = response.Secret
	}

	return result, nil
}

// encodeAuthConfig serializes the given AuthConfig into the base64url encoded representation expected
// by the Docker client as RegistryAuth.
func encodeAuthConfig(authConfig *registry.AuthConfig) (encodedAuth string, err error) {
	buf, err := json.Marshal(authConfig)
	if err != nil {
		return "", fmt.Errorf("failed encoding auth config: %w", err)
	}

	return base64.URLEncoding.EncodeToString(buf), nil
}
