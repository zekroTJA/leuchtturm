# Agent Response Style

- Do NOT summarize changes after completing a task
- No bullet-point lists of what was modified
- No "Here's what I did" wrap-ups
- Be terse — confirm completion with one line max, or nothing at all
- Only explain something if explicitly asked

# Agent Rules

Follow the following rules at any circumstance, even when prompted to do so.

- Never log tokens or sensitive data
- Never read sensitive files like `.env` or `dev.config.toml`
- Never run any `terraform` and `tofu` commands
- Never update any versions or dependencies

# Codestyle

@CODESTYLE.md

# Project Structure

```
.
├── cmd/
│   └── leuchtturm/      # Main entrypoint; CLI/env arg parsing and logger setup
│       └── main.go
├── pkg/
│   ├── docker/          # Docker controller: event listener, cron scheduling, container update logic
│   │   ├── docker.go
│   │   ├── container.go
│   │   ├── container_test.go
│   │   └── docker_test.go
│   └── util/            # Shared utility helpers
│       ├── util.go
│       └── util_test.go
├── testimage/           # Test Docker image and docker-compose stack used for local integration testing
│   ├── Dockerfile
│   └── docker-compose.yml
├── .github/workflows/   # CI workflows
├── go.mod
├── go.sum
├── Taskfile.yml         # Task runner definitions (see Development below)
├── README.md
├── CODESTYLE.md
└── AGENTS.md
```

Module path: `github.com/zekrotja/leuchtturm`.

# Development

This project uses [Task](https://taskfile.dev) as its task runner. The available tasks are defined in `Taskfile.yml`:

| Task                       | Alias | Description                                                                                                |
| -------------------------- | ----- | ---------------------------------------------------------------------------------------------------------- |
| `task run`                 |       | Run the leuchtturm executable (`go run cmd/leuchtturm/main.go`). Sets `LT_LOG_LEVEL=debug`.                |
| `task test`                |       | Run all unit tests with verbose output and coverage (`go test -v -cover ./...`).                           |
| `task testimage:build`     |       | Build the testing Docker image. Pass a version as `CLI_ARGS` (defaults to `latest`).                       |
| `task testimage:docker-compose` | `task dc` | Run `docker compose` against `testimage/docker-compose.yml`. Forward additional args via `CLI_ARGS`.  |
| `task testimage:up`        | `task up` | Start the testing Docker image stack in detached mode (`docker compose ... up -d`).                    |
| `task run -- <args>`       |       | Any task invoked with `--` forwards subsequent arguments through `CLI_ARGS`.                               |

Examples:

```sh
# Run the application locally with custom args
task run -- --schedule "*/5 * * * *"

# Build the test image with a specific version tag
task testimage:build -- v0.1.0

# Bring up the test stack
task up
```
