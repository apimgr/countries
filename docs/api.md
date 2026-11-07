# API reference

The REST API is public, read-only, and versioned under `/api/v1/`. JSON responses are indented by two spaces and end with a newline. Successful responses use `ok: true` and put results under `data`. Errors use `ok: false`, a stable `error` code, and a `message`. Browser clients may use cross-origin requests.

## Country records

Records expose `name.common`, `name.official`, `cca2`, `cca3`, `ccn3`, `capital`, `region`, `subregion`, `currencies`, `languages`, nullable `population`, `population_year`, `area`, `flag`, `flag_svg_url`, `latlng`, `borders`, `timezones`, and `tld`. Population observations use 2024 where available; both population and its year are omitted when the source has no observation.

| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/countries` | Return paginated records (`page`, `limit`; default limit 250, maximum 1000) with optional `q`, `code`, `region`, `currency`, and `language` filters. |
| GET | `/api/v1/countries/{code}` | Read a record by alpha-2 code. |
| GET | `/api/v1/countries/search?q={query}` | Search names and other fields. `code`, `region`, `currency`, and `language` filters are also supported. |
| GET | `/api/v1/coordinates?lat={lat}&lon={lon}` | Return the nearest representative coordinate. |
| POST | `/api/v1/coordinates` | Find the nearest record from a JSON `lat` and `lon` body. |
| GET | `/api/v1/nearby?lat={lat}&lon={lon}&radius={km}` | Return country centroids within a radius. |
| GET | `/api/v1/stats` | Return dataset count and version. |
| GET | `/api/v1/stats.txt` | Return text statistics. |
| GET | `/api/v1/count` | Return the dataset count. |
| GET | `/api/v1/count.txt` | Return the count as text. |

Coordinates are bounded to latitude −90..90 and longitude −180..180. Nearby radius is bounded to 0..20,038 kilometers. Search accepts 1–256 characters.

List responses include `pagination` with `page`, `limit`, `total`, and `pages`. For example, `GET /api/v1/countries?page=2&limit=25` returns the second page of up to 25 records.

## Server endpoints

| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/server/healthz` | Health status and loaded country count. |
| GET | `/api/v1/server/about` | Service information. |
| GET | `/api/v1/server/help` | Usage help. |
| GET | `/api/v1/server/privacy` | Privacy notice. |
| GET | `/api/v1/server/terms` | Service terms. |
| GET | `/api/v1/server/security` | Security reporting policy. |
| GET | `/api/v1/server/contact` | Project contact information. |
| GET | `/api/v1/server/swagger` | OpenAPI 3.1 document. |
| POST | `/api/v1/server/graphql` | Read-only GraphQL query endpoint. |
| POST | `/api/v1/server/reports/{report}` | Receive CSP/Reporting API reports. |

## Discovery aliases

`/api/healthz`, `/api/swagger`, and `/api/graphql` are compatibility/discovery aliases. Use versioned REST paths for integrations. The server-rendered pages are `/server/docs/swagger` and `/server/docs/graphql`.

## Well-known discovery

`/.well-known/security.txt` publishes the GitHub private reporting channel, security policy URL, and expiration date. `/llms.txt` and `/.well-known/llms.txt` publish a plain-text API index. Unsupported well-known paths return 404.

## Browser preferences

Web pages accept `?lang=` for one of the embedded languages (`en`, `es`, `zh`, `fr`, `ar`, `de`, `ja`). A supported value is saved in the `lang` cookie. Theme preferences are submitted with `POST /server/preferences`; accepted values are `dark`, `light`, and `auto`, saved in the `theme` cookie.
