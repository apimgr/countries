# Installation

## Build from source

The Go build runs in the `casjaysdev/go:latest` container with CGO disabled.

```sh
make dev
```

The command builds the server and CLI in a temporary directory and prints its path. Run the server binary from that directory. The default listener is `0.0.0.0:8080`.

## Docker

Build the image from the repository root:

```sh
docker build -f docker/Dockerfile -t countries:local .
```

The container stores configuration, application data, and logs in mounted directories. See [`docker/docker-compose.yml`](https://github.com/apimgr/countries/blob/main/docker/docker-compose.yml) for the compose service and volume mapping.

## Configuration on first run

The server creates `server.yml` in its config directory when the file is missing. Use `--config`, `--data`, and `--log` to select directories. The container uses `/config`, `/data`, and `/logs`.
