# Countries API

[![License](https://img.shields.io/github/license/apimgr/countries)](LICENSE.md)

Countries is a Go service that embeds a static country dataset and exposes it through a versioned REST API and server-rendered web interface. It has no runtime dependency on external data services.

## Current dataset

The embedded records currently provide a common name, two-letter country code, capital, timezones, and representative latitude/longitude. The richer fields listed in `IDEA.md` (official names, region, currencies, languages, population, area, flags, borders, and top-level domains) are not present in the embedded source yet. The source and redistribution terms for the current dataset also need to be established before expanding it; see `TODO.AI.md`.

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
| POST | `/api/graphql` | GraphQL alias |
| GET | `/api/v1/countries` | List country records |
| GET | `/api/v1/countries/{code}` | Read a record by two-letter code |
| GET | `/api/v1/countries/search?q={query}` | Search names |
| GET | `/api/v1/coordinates?lat={lat}&lon={lon}` | Find nearest country |
| GET | `/api/v1/nearby?lat={lat}&lon={lon}&radius={km}` | Find nearby country centroids |
| GET | `/api/v1/stats` | Dataset count and build version |

The `/server/about`, `/server/help`, `/server/privacy`, and `/server/terms` pages are available. The `countries-cli` client supports list, get, search, nearest, nearby, and stats commands. The dataset and client still do not cover all IDEA.md requirements; see `TODO.AI.md` for the exact remaining scope.

## License and data

Application code is MIT licensed. Third-party Go dependency licenses are listed in `LICENSE.md`. The embedded country data is separate from the application code; its source and redistribution license must be documented before any new data is added.
