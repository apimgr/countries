# Countries

[![CI](https://github.com/apimgr/countries/actions/workflows/ci.yml/badge.svg)](https://github.com/apimgr/countries/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/apimgr/countries)](https://github.com/apimgr/countries/releases)
[![License](https://img.shields.io/github/license/apimgr/countries)](LICENSE.md)
[![Documentation](https://readthedocs.org/projects/apimgr-countries/badge/?version=latest)](https://apimgr-countries.readthedocs.io/)

## About

Countries provides read-only information about 249 ISO 3166-1 country records through a REST API, GraphQL, a server-rendered web interface, and the `countries-cli` client. The country dataset is embedded in the server binary and is not fetched at runtime.

## Features

- Search country records by name, code, region, currency, or language, and find countries by coordinates.
- Read the dataset through REST, read-only GraphQL, interactive API explorers, and authenticated MCP access.
- Browse the mobile-friendly server-rendered interface; core pages and forms work without JavaScript.
- Use the shared dark, light, or system-following theme across server pages, Swagger, and GraphiQL.
- Use the companion CLI/TUI client or its optional GUI.

## Production

Download the server binary and, if needed, the `countries-cli` client from [GitHub Releases](https://github.com/apimgr/countries/releases/latest). Releases include static server and client binaries for Linux, macOS, Windows, and FreeBSD on amd64 and arm64, plus optional GUI client binaries for Linux, macOS, and Windows.

| Platform | Architecture | Server | Client |
|---|---|---|---|
| Linux | amd64 | `countries-linux-amd64` | `countries-cli-linux-amd64` |
| Linux | arm64 | `countries-linux-arm64` | `countries-cli-linux-arm64` |
| macOS | Intel (amd64) | `countries-darwin-amd64` | `countries-cli-darwin-amd64` |
| macOS | Apple Silicon (arm64) | `countries-darwin-arm64` | `countries-cli-darwin-arm64` |
| Windows | amd64 | `countries-windows-amd64.exe` | `countries-cli-windows-amd64.exe` |
| Windows | arm64 | `countries-windows-arm64.exe` | `countries-cli-windows-arm64.exe` |
| FreeBSD | amd64 | `countries-freebsd-amd64` | `countries-cli-freebsd-amd64` |
| FreeBSD | arm64 | `countries-freebsd-arm64` | `countries-cli-freebsd-arm64` |

Optional GUI clients use names such as `countries-cli-gui-linux-amd64`. They require the dynamic loader from the Go toolchain used to build them and are not available for FreeBSD.

Linux binary example:

```sh
curl -q -LSsf https://github.com/apimgr/countries/releases/latest/download/countries-linux-amd64 -o countries
chmod +x countries
```

For macOS, download the matching `darwin` asset for `amd64` or `arm64`, then make it executable and clear quarantine if needed:

```sh
curl -q -LSsf https://github.com/apimgr/countries/releases/latest/download/countries-darwin-arm64 -o countries
chmod +x countries
xattr -d com.apple.quarantine countries 2>/dev/null || true
```

On Windows, download the matching `.exe` asset and add its directory to `%PATH%`. For FreeBSD, download the matching `amd64` or `arm64` asset and run `chmod +x` before use.

### Docker

The production Compose file is [`docker/docker-compose.yml`](docker/docker-compose.yml). Copy it to a deployment directory outside the source checkout so runtime data stays out of the repository:

```sh
DEPLOY_DIR=/path/to/countries-deployment
mkdir -p "$DEPLOY_DIR/volumes/config" "$DEPLOY_DIR/volumes/data"
cp docker/docker-compose.yml "$DEPLOY_DIR/docker-compose.yml"
cd "$DEPLOY_DIR" && docker compose up -d
```

The service is published on `172.17.0.1:64580` by default. Review the bind address and deployment paths before starting it. The image is `ghcr.io/apimgr/countries:latest`.

### Server service

Run the `countries` binary to start the service. On first start, it creates `server.yml` in the operating system's configuration directory and selects an available native port in the `64000`–`64999` range; containers listen on port `80`. Configuration, database, logs, cache, and backups are stored in the operating system's application directories.

On supported systems, install a service with `sudo countries --service --install`. See the [installation guide](docs/installation.md) for supported service managers and lifecycle commands.

## Client

The companion client supports country listing, lookup, search, coordinate and nearby queries, statistics, and supported versioned API requests.

```sh
countries-cli --help
SERVER_URL="${COUNTRIES_SERVER_PRIMARY:?Set this to your deployment URL}"
countries-cli --server "$SERVER_URL" search united
countries-cli --server "$SERVER_URL" get US
countries-cli --server "$SERVER_URL" request GET /api/v1/server/healthz
```

With no command, the client opens its GUI when available, otherwise its terminal interface when attached to an interactive terminal. See the [CLI reference](docs/cli.md) for commands, flags, configuration, and output formats.

## Configuration

Configuration is stored in `server.yml` with sensitive values kept in `secrets.yml`; see the [configuration reference](docs/configuration.md) for file locations, options, environment overrides, and generated defaults.

## API

Country data is available under `/api/v1/`. The service also provides a read-only GraphQL endpoint with the local GraphiQL explorer at `/server/docs/graphql`, the OpenAPI explorer at `/server/docs/swagger`, MCP access for bearer-token holders, and server-rendered pages.

| Endpoint | Description |
|---|---|
| `GET /api/v1/countries` | List and filter country records |
| `GET /api/v1/countries/{code}` | Look up a country by alpha-2, alpha-3, or numeric code |
| `GET /api/v1/countries/search?q={query}` | Search country records |
| `GET /api/v1/coordinates?lat={lat}&lon={lon}` | Find the nearest country |
| `GET /api/v1/nearby?lat={lat}&lon={lon}&radius={km}` | Find countries within a radius |
| `GET /api/v1/stats` | Read dataset statistics |
| `GET /api/v1/server/healthz` | Read service health |
| `GET /api/v1/server/graphql?query=...` | Execute a read-only GraphQL query |
| `POST /api/v1/server/graphql` | Execute a GraphQL query |
| `POST /api/v1/server/mcp` | Call MCP with a bearer token |

The web interface includes country search, coordinate lookup, API explorers, help, privacy, security, terms, contact/reporting, guest preference and consent controls, and anonymous owner-token management for MCP access. Browse `/` on your deployment; the API and browser route details are in the [API guide](docs/api.md).

## Other

- [Configuration](docs/configuration.md)
- [Security and reporting](docs/security.md)
- [Integrations and discovery](docs/integrations.md)
- [Operations, backups, and updates](docs/operations.md)
- [Development and testing](docs/development.md)
- [Translation guide](docs/translating.md)

## Development

Builds and tests run in the `casjaysdev/go:latest` container with CGO disabled. Docker is required.

```sh
git clone https://github.com/apimgr/countries.git
cd countries
make dev
make test
```

`make dev` builds a static server and CLI/TUI client into a project-scoped operating system temporary directory. Set `BUILD_GUI=1` to also build the optional, dynamically linked GUI client. The Docker binary integration suite is `./tests/run_tests.sh`, the Incus service suite is `./tests/incus.sh`, and browser E2E is `./tests/e2e.sh`. See [development](docs/development.md) for details.

## Disclaimer

This software is provided as is, without warranty of any kind. Evaluate and configure it for your environment before production use. Third-party services used by a deployment have their own terms.

## License

MIT — see [LICENSE.md](LICENSE.md). Embedded data and third-party dependencies have separate attribution requirements listed there and in [`src/data/README.md`](src/data/README.md).
