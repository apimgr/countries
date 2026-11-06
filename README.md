# Countries API

[![License](https://img.shields.io/github/license/apimgr/countries)](LICENSE.md)
User and operator documentation is available in [`docs/`](docs/index.md). ReadTheDocs configuration is included in the repository.

Countries is a Go service that embeds a static country dataset and exposes it through a versioned REST API and server-rendered web interface. It has no runtime dependency on external data services.

## Current dataset

The embedded dataset contains 249 ISO 3166-1 records with common and official names, ISO codes, capitals, regions, currencies, languages, population values where available, area, flag emoji and SVG links, representative coordinates, borders, time zones, and top-level domains. Field-level source versions, attribution, and licenses are documented in [`src/data/README.md`](src/data/README.md). Population values include their observation year; unavailable source observations remain absent.

## Run

The `make dev` target builds the server and CLI in an isolated temporary directory with the `casjaysdev/go:latest` container image. It prints the output directory when the build finishes. Start the server binary from that directory; it listens on `0.0.0.0:8080` by default. Configuration is read from `server.yml` in the config directory, and the server creates a default file on first start.

## HTTP endpoints

| Method | Path | Description |
|---|---|---|
| GET | `/` | Country search home page |
| GET | `/search` | Search page |
| GET | `/coordinates` | Nearest-country lookup page |
| GET | `/server/docs/swagger` | OpenAPI documentation page |
| GET | `/server/docs/graphql` | GraphQL explorer |
| GET | `/server/healthz` | Human-readable service health page |
| GET | `/api/v1/server/healthz` | Versioned JSON health endpoint |
| GET | `/api/v1/server/swagger` | OpenAPI 3.1 JSON document |
| POST | `/api/v1/server/graphql` | Read-only GraphQL endpoint |
| GET | `/.well-known/security.txt` | RFC 9116 security reporting details |
| GET | `/.well-known/llms.txt` | API discovery for AI clients |
| POST | `/api/graphql` | GraphQL alias |
| GET | `/api/v1/countries` | List country records |
| GET | `/api/v1/countries/{code}` | Read a record by two-letter code |
| GET | `/api/v1/countries/search?q={query}` | Search names |
| GET | `/api/v1/coordinates?lat={lat}&lon={lon}` | Find nearest country |
| GET | `/api/v1/nearby?lat={lat}&lon={lon}&radius={km}` | Find nearby country centroids |
| GET | `/api/v1/stats` | Dataset count and build version |

The `/server/about`, `/server/help`, `/server/privacy`, `/server/terms`, `/server/security`, and `/server/contact` pages are available. The `countries-cli` client supports list, get, search, nearest, nearby, and stats commands. The client and broader shared AI.md server requirements still have open work; see `TODO.AI.md` for the current implementation backlog.

## License and data

Application code is MIT licensed. Third-party Go dependencies are listed in `LICENSE.md`. The embedded country database has separate ODbL and CC BY attribution terms documented in [`src/data/README.md`](src/data/README.md).

## Disclaimer

This software is provided "as is" without warranty of any kind. Use it at your own risk. The maintainers are not responsible for damages, data loss, or other issues arising from its use. Evaluate it thoroughly before production deployment. While the project follows security practices, no software is guaranteed to be free of vulnerabilities. Third-party services, if configured, remain subject to their own terms.
