# leuchtturm

A lightweight Docker container update agent that watches labeled containers and automatically pulls and re-creates them on a cron schedule when a newer image is available.

> [!NOTE]  
> This project is very much inspired by [watchtower](https://github.com/containrrr/watchtower), which was archived on the 17th of December 2025 and a lot of the forks are ["full of AI slop"](https://github.com/containrrr/watchtower/discussions/2135). So I thought I take the core functionality of the original project and build my own service for that. Though I also use AI for coding assistance, all committed and deployed code is reviewed by me and holds up to the standarts of code I would write myself by hand.

## Usage

The Docker image is published to GitHub Container Registry and can be pulled from:

```
ghcr.io/zekrotja/leuchtturm
```

leuchtturm needs access to the Docker daemon socket to inspect, pull, and re-create containers. It only acts on containers that are **running** and carry the **`leuchtturm.enable=true` label**, so other containers on the host are unaffected.

### Docker Compose example

```yaml
services:
  leuchtturm:
    image: ghcr.io/zekrotja/leuchtturm:latest
    container_name: leuchtturm
    restart: unless-stopped
    environment:
      LT_LOG_LEVEL: info
      LT_LOG_FORMAT: text
      LT_SCHEDULE: "0 4 * * *"
      LT_KEEP_OLD_IMAGE: "false"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock

  # Example of a container that will be auto-updated by leuchtturm.
  nginx:
    image: nginx:latest
    restart: unless-stopped
    labels:
      leuchtturm.enable: "true"

  # Example of a container with per-container overrides.
  redis:
    image: redis:7
    restart: unless-stopped
    labels:
      leuchtturm.enable: "true"
      leuchtturm.schedule: "*/30 * * * *"
      leuchtturm.keep-old-image: "true"
```

### Configuration

leuchtturm is configured via command-line flags or environment variables.

| Flag               | Environment         | Default      | Description                                                                 |
| ------------------ | ------------------- | ------------ | --------------------------------------------------------------------------- |
| `--log-level`      | `LT_LOG_LEVEL`      | `info`       | Log level (`debug`, `info`, `warn`, `error`).                               |
| `--log-format`     | `LT_LOG_FORMAT`     | `text`       | Log format (`text`, `json`).                                                |
| `--schedule`       | `LT_SCHEDULE`       | `2 12 * * *` | Default cron schedule used for all enabled containers.                      |
| `--keep-old-image` | `LT_KEEP_OLD_IMAGE` | `false`      | Keep old images on disk after a successful update instead of removing them. |

### Container labels

Set these labels on the containers you want leuchtturm to manage.

| Label                       | Required | Description                                                                        |
| --------------------------- | -------- | ---------------------------------------------------------------------------------- |
| `leuchtturm.enable`         | yes      | Set to `true` to opt the container into automatic updates.                         |
| `leuchtturm.schedule`       | no       | Per-container cron schedule. Overrides `LT_SCHEDULE`.                              |
| `leuchtturm.keep-old-image` | no       | Per-container override for `LT_KEEP_OLD_IMAGE` (`true` to keep, `false` to prune). |

When the schedule fires, leuchtturm pulls the image referenced by the running container. If the pulled image differs from the running one, the container is stopped, removed, and re-created from the new image while preserving its previous configuration, networks, and name.
