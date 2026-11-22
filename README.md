# Countries

Countries is a read-only country reference service with a REST API, GraphQL API, server-rendered web interface, and command-line client. It embeds 249 ISO 3166-1 records and does not fetch country data at runtime.

[![CI](https://github.com/apimgr/countries/actions/workflows/ci.yml/badge.svg)](https://github.com/apimgr/countries/actions/workflows/ci.yml)
[![License](https://img.shields.io/github/license/apimgr/countries)](LICENSE.md)

---

## 📦 Install

Download a server or client binary from [GitHub Releases](https://github.com/apimgr/countries/releases/latest).

### Linux

| Arch | Server | Client |
|---|---|---|
| amd64 | `countries-linux-amd64` | `countries-cli-linux-amd64` |
| arm64 | `countries-linux-arm64` | `countries-cli-linux-arm64` |

```sh
curl -q -LSsf https://github.com/apimgr/countries/releases/latest/download/countries-linux-amd64 -o /usr/local/bin/countries && chmod +x /usr/local/bin/countries
curl -q -LSsf https://github.com/apimgr/countries/releases/latest/download/countries-cli-linux-amd64 -o /usr/local/bin/countries-cli && chmod +x /usr/local/bin/countries-cli
```

### macOS

| Arch | Server | Client |
|---|---|---|
| Intel (x86_64) | `countries-darwin-amd64` | `countries-cli-darwin-amd64` |
| Apple Silicon (arm64) | `countries-darwin-arm64` | `countries-cli-darwin-arm64` |

```sh
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
curl -q -LSsf "https://github.com/apimgr/countries/releases/latest/download/countries-darwin-${ARCH}" -o /usr/local/bin/countries && chmod +x /usr/local/bin/countries
curl -q -LSsf "https://github.com/apimgr/countries/releases/latest/download/countries-cli-darwin-${ARCH}" -o /usr/local/bin/countries-cli && chmod +x /usr/local/bin/countries-cli
xattr -d com.apple.quarantine /usr/local/bin/countries /usr/local/bin/countries-cli 2>/dev/null || true
```

### Windows

| Arch | Server | Client |
|---|---|---|
| amd64 | `countries-windows-amd64.exe` | `countries-cli-windows-amd64.exe` |
| arm64 | `countries-windows-arm64.exe` | `countries-cli-windows-arm64.exe` |

Download the required `.exe` files from the release page and add their directory to `%PATH%`.

### FreeBSD

| Arch | Server | Client |
|---|---|---|
| amd64 | `countries-freebsd-amd64` | `countries-cli-freebsd-amd64` |
| arm64 | `countries-freebsd-arm64` | `countries-cli-freebsd-arm64` |

## 🐳 Docker

The production Compose file is [`docker/docker-compose.yml`](docker/docker-compose.yml). Start it with `docker compose -f docker/docker-compose.yml up -d`. It persists runtime configuration and data in the configured volumes; review the file before deployment and set the published port and host values for your environment.

## 🖥️ Command-line client

The `countries-cli` binary supports `list`, `get`, `search`, `nearest`, `nearby`, and `stats`. Run `countries-cli --help` for flags and examples. See the [CLI reference](docs/cli.md).

## 🤖 Server

Start the `countries` binary to serve the web interface and APIs. By default it listens on `0.0.0.0:8080`; it creates `server.yml` on first start. See the [installation](docs/installation.md) and [configuration](docs/configuration.md) guides before exposing a deployment.

- Web interface: `/`, `/search`, and `/coordinates`
- REST reference: `/server/docs/swagger`; OpenAPI JSON: `/api/v1/server/swagger`
- GraphQL explorer: `/server/docs/graphql`; endpoint: `/api/v1/server/graphql`
- Health: `/server/healthz` and `/api/v1/server/healthz`
- Service pages: `/server/about`, `/server/help`, `/server/healthz`, `/server/preferences` (including `/server/preferences/sync`), `/server/privacy`, `/server/terms`, `/server/security`, `/server/contact`, and `/server/dpo`
- REST routes, response formats, discovery files, and preference behavior: [API reference](docs/api.md)

## Data and attribution

The embedded dataset includes common and official names, ISO codes, capitals, regions, currencies, languages, population observations when available, area, flags, coordinates, borders, time zones, and top-level domains. Source versions, field provenance, and licensing are documented in [`src/data/README.md`](src/data/README.md).

## Development

The Go toolchain runs in `casjaysdev/go:latest` with CGO disabled. Docker is required for local builds and tests.

```sh
make dev
make test
```

`make dev` builds the server and client into an OS temporary directory. `make test` runs vet, vulnerability scanning, unit tests, and the configured coverage gate. Read the [development guide](docs/development.md) before contributing.

Run `./tests/run_tests.sh` for phase-two binary integration checks; select a runtime with `--engine auto`, `--engine incus`, `--engine docker`, or the `TEST_ENGINE` environment variable. See the [development guide](docs/development.md) for coverage and requirements.

Run the standalone browser E2E suite on demand with `./tests/e2e.sh`; see the [development guide](docs/development.md) for its Docker image requirements.

## License

MIT — see [LICENSE.md](LICENSE.md). The embedded data and third-party dependencies have separate attribution requirements listed there and in [`src/data/README.md`](src/data/README.md).
