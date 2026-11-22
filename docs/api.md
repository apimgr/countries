# API reference

The REST API is public, read-only, and versioned under `/api/v1/`. JSON responses are indented by two spaces and end with a newline. Successful responses use `ok: true` and put results under `data`. Errors use `ok: false`, a stable `error` code, and a `message`. Browser clients may use cross-origin requests.

## Country records

Records expose `name.common`, `name.official`, `cca2`, `cca3`, `ccn3`, `capital`, `region`, `subregion`, `currencies`, `languages`, nullable `population`, `population_year`, `area`, `flag`, `flag_svg_url`, `latlng`, `borders`, `timezones`, and `tld`. Population observations use 2024 where available; both population and its year are omitted when the source has no observation.

| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/countries` | Return paginated records (`page`, `limit`; default limit 250, maximum 1000) with optional `q`, `code`, `region`, `currency`, and `language` filters. |
| GET | `/api/v1/countries/{code}` | Read a record by alpha-2, alpha-3, or numeric code. |
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

The browser server pages are available at `/server/about`, `/server/help`, `/server/healthz`, `/server/preferences`, `/server/preferences/sync`, `/server/privacy`, `/server/terms`, `/server/security`, `/server/contact`, and `/server/dpo`. `/server` redirects to `/server/about`. The API mirrors the informational pages as JSON; contact is currently an informational page and does not accept submissions.

| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/server/healthz` | Health status and loaded country count. |
| GET | `/api/v1/server/about` | Service information. |
| GET | `/api/v1/server/help` | Usage help. |
| GET | `/api/v1/server/privacy` | Privacy notice. |
| GET | `/api/v1/server/terms` | Service terms. |
| GET | `/api/v1/server/security` | Security reporting policy. |
| GET | `/api/v1/server/contact` | Project contact information. |
| GET | `/api/v1/server/dpo` | Data protection contact (`name`, `email`, and absolute `privacy_url`). The email falls back from `server.contact.dpo.email` to `server.contact.general.email`; no address is invented. |
| GET | `/api/v1/server/preferences` | Read effective theme and language preferences and the empty guest preference set. |
| POST | `/api/v1/server/preferences` | Save supported theme and language cookies; JSON and form bodies are accepted. This API mirror does not use CSRF tokens. |
| GET | `/api/v1/server/preferences/sync` | Export current preferences as a canonical share URL and unpadded base64url code. |
| GET | `/api/v1/server/preferences/sync/import` | Apply preferences from direct query values or the optional `code` value. Invalid values are ignored. |
| GET | `/server/preferences` | Browser page for the current theme and language preferences. |
| GET | `/server/preferences/sync` | Browser page with an inline export URL/code and import form; accepts HTML, JSON, or plain text. |
| POST | `/server/preferences` | Save supported theme and language cookies. Browser forms require the CSRF token rendered by server pages. |
| GET | `/api/v1/server/swagger` | OpenAPI 3.1 document. |
| POST | `/api/v1/server/graphql` | Read-only GraphQL query endpoint. |
| POST | `/api/v1/server/reports/{report}` | Receive CSP/Reporting API reports. |
| POST | `/server/consent` | Save a browser's accept/decline choice for optional cookies; requires the page's CSRF token and redirects to a validated local path. |

## Discovery aliases

`/api/healthz`, `/api/swagger`, and `/api/graphql` are compatibility/discovery aliases. Use versioned REST paths for integrations. The server-rendered pages are `/server/docs/swagger` and `/server/docs/graphql`. The GraphQL explorer submits through a server-rendered POST form when JavaScript is disabled; its optional enhancement sends the same read-only query to the API endpoint.

## Well-known discovery

`/.well-known/security.txt` publishes the GitHub private reporting channel, security policy URL, and expiration date. `/llms.txt` and `/.well-known/llms.txt` publish a plain-text API index. Unsupported well-known paths return 404.

## Browser preferences

The `/server/preferences` page saves theme (`dark`, `light`, or `auto`), language (`en`, `es`, `zh`, `fr`, `ar`, `de`, or `ja`), cookie-consent choices, and the optional CCPA opt-out setting. A first-visit banner offers accept and decline actions through `POST /server/consent`; both browser flows require the CSRF token supplied by the page. The theme and language cookies are client-readable, last one year, and use `SameSite=Lax`; they are never persisted server-side. The page links to `/server/preferences/sync`, which exports a canonical URL and unpadded base64url code and accepts a pasted URL, query string, or code. Sync imports apply valid theme/language values additively; they do not alter consent, CCPA, or credentials. API preference writes accept JSON or form values without CSRF because they do not use ambient credentials. Web preferences writes redirect with `303`; API preferences writes return JSON.
