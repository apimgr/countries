# Countries

[![CI](https://github.com/apimgr/countries/actions/workflows/ci.yml/badge.svg)](https://github.com/apimgr/countries/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/apimgr/countries)](https://github.com/apimgr/countries/releases)
[![License](https://img.shields.io/github/license/apimgr/countries)](LICENSE.md)

## About

Countries is a read-only country reference service with a REST API, GraphQL API, server-rendered pages, and a command-line client. It embeds 249 ISO 3166-1 records and does not fetch country data at runtime.

## Features

- Search and filter country records by codes, region, currency, and language.
- Look up records by alpha-2, alpha-3, or numeric country code.
- Find the nearest country centroid and countries within a radius.
- Query the dataset through REST, GraphQL, or `countries-cli`.
- Serve a self-hosted web interface without an account system.
- Manage anonymous owner and linked bearer tokens for MCP access and owned resources.
- Refresh external IP/domain blocklists and vulnerability databases on the built-in schedule.
- Create verified, optionally encrypted scheduled backups with bounded restore support.
- Use the built-in help, privacy, security, terms, preferences, and report pages.
- Use the server and CLI in eight embedded languages: English, Spanish, French, German, Italian, Arabic, Chinese, and Japanese.

## Production

### Docker Compose

The production Compose file is [`docker/docker-compose.yml`](docker/docker-compose.yml). Copy it to a deployment directory outside the source checkout so its relative `volumes/` paths keep runtime data out of the repository, then review the published port and host values:

```sh
mkdir -p /path/to/countries/volumes/config /path/to/countries/volumes/data
cp docker/docker-compose.yml /path/to/countries/docker-compose.yml
cd /path/to/countries && docker compose up -d
```

The Compose deployment publishes the service on `172.17.0.1:64580` and keeps configuration and runtime data in `docker/volumes/`. Review those paths and the bind address before deployment.

### Docker

```sh
docker run -d --name countries \
  -p 172.17.0.1:64580:80 \
  -v /path/to/countries/volumes/config:/config:z \
  -v /path/to/countries/volumes/data:/data:z \
  ghcr.io/apimgr/countries:latest
```

### Binary

Download the server binary for your operating system and architecture from [GitHub Releases](https://github.com/apimgr/countries/releases/latest). For Linux amd64:

```sh
curl -q -LSsf https://github.com/apimgr/countries/releases/latest/download/countries-linux-amd64 -o countries
chmod +x countries
./countries
```

The server listens on `0.0.0.0:8080` by default and creates `server.yml` on first start. See the [installation](docs/installation.md) and [configuration](docs/configuration.md) guides before exposing a deployment.

### System service

On supported systems, install the service with `sudo countries --service --install`, then start it with the service manager. See [installation](docs/installation.md) for platform notes.

## Client

Download a `countries-cli` release for your operating system and architecture from [GitHub Releases](https://github.com/apimgr/countries/releases/latest). The client supports `list`, `get`, `search`, `nearest`, `nearby`, and `stats`.

```sh
countries-cli --help
countries-cli search united
countries-cli get US
```

See the [CLI reference](docs/cli.md) for flags and output formats.

## Configuration

The server creates `server.yml` on first start. Runtime settings also use `yaml.d/` area files and `secrets.yml`; do not put credentials in `server.yml`. The client optionally reads `cli.yml` from the user's OS configuration directory. See the [configuration reference](docs/configuration.md) for paths, supported settings, and precedence.

Scheduled updates honor `server.update.defer_days` for stable and beta releases and notify once per eligible version. Privilege-safe automatic server installation is not yet implemented; leave `server.update.auto_install` disabled. Backups run daily and can be encrypted using `server.backup.encryption_password` in `secrets.yml`.

Key server settings include `server.address`, `server.port`, `server.mode`, `server.tokens.*`, `server.rate_limit.*`, `server.contact.*`, `server.privacy.*`, and optional consent-gated `server.tracking.*`. The operator token and other credentials belong in `secrets.yml`.

## API

The service exposes versioned REST routes under `/api/v1/`, a GraphQL endpoint, and server-rendered API documentation. Use the base URL of your own deployment in examples.

| Endpoint | Description |
|---|---|
| `GET /api/v1/countries` | List country records |
| `GET /api/v1/countries/{code}` | Look up a country by code |
| `GET /api/v1/countries/search?q={query}` | Search country names |
| `GET /api/v1/coordinates?lat={lat}&lon={lon}` | Find the nearest country centroid |
| `GET /api/v1/nearby?lat={lat}&lon={lon}&radius={km}` | Find countries within a radius |
| `GET /api/v1/stats` | Read dataset counts and version |
| `POST /api/v1/server/graphql` | Execute a GraphQL query |
| `GET /api/v1/server/healthz` | Check service health |
| `POST /api/v1/server/preferences/tokens/primary` | Create a primary owner token |
| `GET /api/v1/server/preferences/tokens` | List owned tokens (primary bearer token required) |
| `POST /api/v1/server/mcp` | Use the MCP endpoint (bearer token required) |

Interactive references are available at `/server/docs/swagger` and `/server/docs/graphql`. Read the [API documentation](docs/api.md) for response formats, token management, direct aliases, and discovery routes. Supply your deployment's base URL in place of `YOUR_SERVER` in command examples.

```sh
curl -q -LSsf http://YOUR_SERVER/api/v1/server/healthz
```

## Other

### Troubleshooting

- Check the server's configured logs for startup or request errors.
- Check `http://YOUR_SERVER/server/healthz` to confirm the service is responding.
- A `429` response means the deployment's request limit was reached; wait for `Retry-After` before retrying.
- See [configuration](docs/configuration.md) for listener, proxy, privacy, token, and logging settings.

## Development

The Go toolchain runs in `casjaysdev/go:latest` with CGO disabled. Docker is required for builds and tests.

```sh
git clone https://github.com/apimgr/countries.git
cd countries
make dev
make test
```

`make dev` builds the server and client into an OS temporary directory. `make test` runs the project test gate. The binary integration suite is `./tests/run_tests.sh`; the browser E2E suite is `./tests/e2e.sh`. See the [development guide](docs/development.md) for prerequisites and details.

## Disclaimer

This software is provided as is, without warranty of any kind. Use it at your own risk.

- **No warranty:** The authors are not responsible for damages, data loss, or issues arising from use of this software.
- **Not professional advice:** This software does not provide legal, financial, medical, or other professional advice.
- **Third-party services:** Any external APIs or services used by a deployment have their own terms.
- **Security:** No software can be guaranteed free of vulnerabilities.
- **Production use:** Evaluate the software and configure it for your environment before deployment.

By using this software, you acknowledge that you have read and understood this disclaimer.

## License

MIT — see [LICENSE.md](LICENSE.md). Embedded data and third-party dependencies have separate attribution requirements listed there and in [`src/data/README.md`](src/data/README.md).
