# Installation

## Download a release

Download the server binary and, when needed, the companion `countries-cli` binary from [GitHub Releases](https://github.com/apimgr/countries/releases/latest). Release assets include Linux, macOS, Windows, and FreeBSD builds for amd64 and arm64. SHA-256 checksums are published as `sha256.txt` with each release.

| Platform | Arch | Server asset | Client asset |
|---|---|---|---|
| Linux | amd64 | `countries-linux-amd64` | `countries-cli-linux-amd64` |
| Linux | arm64 | `countries-linux-arm64` | `countries-cli-linux-arm64` |
| macOS | Intel (x86_64) | `countries-darwin-amd64` | `countries-cli-darwin-amd64` |
| macOS | Apple Silicon (arm64) | `countries-darwin-arm64` | `countries-cli-darwin-arm64` |
| Windows | amd64 | `countries-windows-amd64.exe` | `countries-cli-windows-amd64.exe` |
| Windows | arm64 | `countries-windows-arm64.exe` | `countries-cli-windows-arm64.exe` |
| FreeBSD | amd64 | `countries-freebsd-amd64` | `countries-cli-freebsd-amd64` |
| FreeBSD | arm64 | `countries-freebsd-arm64` | `countries-cli-freebsd-arm64` |

On Linux, download the server binary and make it executable:

```sh
curl -q -LSsf https://github.com/apimgr/countries/releases/latest/download/countries-linux-amd64 -o /usr/local/bin/countries
chmod +x /usr/local/bin/countries
```

For macOS, replace `linux` with `darwin` in the asset name and use `amd64` or `arm64`. After installing, remove the quarantine attribute when needed:

```sh
xattr -d com.apple.quarantine /usr/local/bin/countries 2>/dev/null || true
```

On Windows, download the matching `.exe` file and add its directory to `%PATH%`.

## Docker Compose

The production Compose file is [`docker/docker-compose.yml`](https://github.com/apimgr/countries/blob/main/docker/docker-compose.yml). It publishes the service on `172.17.0.1:64580`, stores configuration under `docker/volumes/config`, and stores runtime data under `docker/volumes/data` relative to the Compose file.

```sh
docker compose -f docker/docker-compose.yml up -d
```

The image exposes port 80 inside the container. Review and adjust the bind address, volumes, and environment for the deployment before starting it.

## Build from source

Docker and the `casjaysdev/go:latest` image are required. The project Makefile runs Go inside that container with CGO disabled.

```sh
make dev
```

The command builds the server and CLI into a project-scoped OS temporary directory and prints its path. Run the server binary from that directory. The default host listener is `0.0.0.0:8080`.

## First start

The server creates `server.yml` in its configuration directory when the file is missing. The default listener is `0.0.0.0:8080`; `--port` or `PORT` changes the port. Use `--config`, `--data`, and `--log` to select runtime directories. See the [configuration reference](configuration.md) for path defaults and settings.
