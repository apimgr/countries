# API reference

The country REST API is public, read-only, and versioned under `/api/v1/`. Server operations also include authenticated owner-token management and the MCP endpoint. JSON responses are indented by two spaces and end with a newline. Successful responses use `ok: true` and put results under `data`. Errors use `ok: false`, a stable `error` code, and a `message`. Browser clients may use cross-origin requests.

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

### Example requests

Replace `YOUR_SERVER` with the base URL of the deployment you use:

```sh
curl -q -LSsf "http://YOUR_SERVER/api/v1/countries?region=Europe&limit=10"
curl -q -LSsf http://YOUR_SERVER/api/v1/countries/US
curl -q -LSsf http://YOUR_SERVER/api/v1/server/healthz
```

Issue a primary owner token when you need an owner identity or an MCP credential. The successful response includes the raw token; protect it as a credential:

```sh
curl -q -LSsf -X POST \
  -H "Content-Type: application/json" \
  -d '{}' \
  http://YOUR_SERVER/api/v1/server/preferences/tokens/primary
```

## Server endpoints

The browser server pages include `/server/about`, `/server/help`, `/server/healthz`, `/server/preferences`, `/server/preferences/sync`, `/server/privacy`, `/server/terms`, `/server/security`, and `/server/dpo`. `/server` redirects to `/server/about`. The report form is available only from the security policy and the security.txt reporting contact. It handles security reports, abuse/terms reports, DMCA notices, and privacy requests; it is not a support or feedback channel.

| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/server/healthz` | Health status and loaded country count. |
| GET | `/api/v1/server/about` | Service information. |
| GET | `/api/v1/server/help` | Usage help. |
| GET | `/api/v1/server/privacy` | Privacy notice. |
| GET | `/api/v1/server/terms` | Service terms. |
| GET | `/api/v1/server/security` | Security reporting policy. |
| GET | `/api/v1/server/contact` | Report form field descriptors, a new CAPTCHA challenge, a single-use `form_token`, offered types, security URL, and public abuse email. This route returns 404 when disabled. |
| POST | `/api/v1/server/contact` | Submit a report for review with `step: "submit"`; confirm with all original fields, `step: "confirm"`, the returned `review_token`, and `confirmed: true`. The first call never delivers. Security mode requires a valid `security_id`. |
| GET | `/api/v1/server/dpo` | Data protection contact (`name`, `email`, and absolute `privacy_url`). The email falls back from `server.contact.dpo.email` to `server.contact.general.email`; no address is invented. |
| GET | `/api/v1/server/preferences` | Read effective theme and language preferences and the empty guest preference set. |
| POST | `/api/v1/server/preferences` | Save supported theme and language cookies; JSON and form bodies are accepted. This API mirror does not use CSRF tokens. |
| GET | `/api/v1/server/preferences/sync` | Return the current theme and language plus an unpadded base64url code for browser-to-browser transfer. |
| GET | `/api/v1/server/preferences/sync/import` | Apply supported preferences from direct query values or an exported code; return `applied` and `ignored` parameter arrays. |
| GET | `/server/preferences` | Browser page for the current theme and language preferences. |
| GET | `/server/preferences/sync` | Browser page to copy a code representing the current theme and language preferences and apply a code from another browser. |
| POST | `/server/preferences` | Save supported theme and language cookies. Browser forms require the CSRF token rendered by server pages. |
| GET | `/api/v1/server/swagger` | OpenAPI 3.1 document. |
| POST | `/api/v1/server/graphql` | Read-only GraphQL query endpoint. |
| POST | `/api/v1/server/mcp` | Streamable HTTP MCP endpoint; requires a valid owner or linked bearer token and protocol revision `2026-07-28`. |
| GET | `/api/v1/server/help/mcp` | MCP connection details, endpoint URL, aliases, and available tools. |
| GET | `/api/v1/server/preferences/tokens` | List masked owner and linked tokens; primary owner bearer token required. |
| POST | `/api/v1/server/preferences/tokens/primary` | Issue a primary owner token for a caller without a valid bearer token. |
| POST | `/api/v1/server/preferences/tokens/primary/reveal` | Reveal the primary token; primary owner bearer token required. |
| POST | `/api/v1/server/preferences/tokens/primary/rotate` | Rotate the primary token; primary owner bearer token required. |
| POST | `/api/v1/server/preferences/tokens` | Create a linked token; primary owner bearer token required. |
| POST | `/api/v1/server/preferences/tokens/{id}/reveal` | Reveal a linked token; primary owner bearer token required. |
| POST | `/api/v1/server/preferences/tokens/{id}/rotate` | Rotate a linked token; primary owner bearer token required. |
| PATCH | `/api/v1/server/preferences/tokens/{id}` | Rename a linked token or change its expiry; primary owner bearer token required. |
| DELETE | `/api/v1/server/preferences/tokens/{id}` | Revoke a linked token; primary owner bearer token required. |
| POST | `/api/v1/server/reports/{report}` | Receive CSP/Reporting API reports. |
| POST | `/server/consent` | Save a browser's accept/decline choice for optional cookies; requires the page's CSRF token and redirects to a validated local path. |

## Discovery aliases

`/api/healthz`, `/api/swagger`, `/api/graphql`, `/api/mcp`, and `/mcp` are direct aliases. They invoke the same handlers and do not redirect. Use versioned REST paths for integrations. The server-rendered pages are `/server/docs/swagger` and `/server/docs/graphql`. The GraphQL explorer submits through a server-rendered POST form when JavaScript is disabled; its optional enhancement sends the same read-only query to the API endpoint.

## Well-known discovery

`/.well-known/security.txt` publishes the GitHub private reporting channel, the enabled deployment reporting form, any configured security email, the policy URL, and an expiration date. `/llms.txt` and `/.well-known/llms.txt` publish a plain-text API index. Unsupported well-known paths return 404.

`/robots.txt` reflects the configured public crawl rules and includes an absolute `Sitemap:` URL. `/sitemap.xml` is generated per request, includes only allowed public HTML pages, and excludes API, contact, and preference endpoints.

## Browser preferences

The `/server/preferences` page saves theme (`dark`, `light`, or `auto`), language (`en`, `es`, `fr`, `de`, `it`, `ar`, `zh`, or `ja`), and cookie-consent choices. The CCPA opt-out control is available according to the configured privacy settings. A first-visit banner offers accept and decline actions through `POST /server/consent`; both browser flows require the CSRF token supplied by the page. The theme and language cookies are client-readable, last one year, and use `SameSite=Lax`; they are never persisted server-side. The `/server/preferences/sync` page displays a code for the current theme and language and accepts a pasted code to apply those values in another browser. `GET /api/v1/server/preferences/sync` returns the current values and transfer code as JSON. `GET /api/v1/server/preferences/sync/import` applies supported preferences from direct query values or an exported code and returns `applied` and `ignored` arrays. Consent, CCPA choices, and credentials are not included. API preference writes accept JSON or form values without CSRF because they do not use ambient credentials. Web preferences writes redirect with `303`; API preference writes and imports return JSON.

### Owner token and MCP authentication

Owner tokens identify anonymous resource owners; there are no user accounts. A primary token manages linked tokens, while a linked token has access to its owner's resources and cannot manage tokens. Generate a primary token with `POST /api/v1/server/preferences/tokens/primary`, then create a linked token for each MCP client. Send the token only in the `Authorization: Bearer` header. MCP does not accept cookies and never exposes token management as tools. Keep raw tokens out of URLs and logs. The token preferences page at `/server/preferences/tokens` provides the browser management flow.

For MCP `POST` requests, send the required `MCP-Protocol-Version` and `Mcp-Method` headers matching the request body; tool calls also require `Mcp-Name`. Only protocol revision `2026-07-28` is supported. See `/server/help/mcp` for the deployment-specific endpoint URL and the live tool list.

The token-list response contains masked values and metadata only: `id`, `kind`, `name`, `prefix`, `last4`, `masked`, `created_at`, `rotated_at`, `expires_at`, `last_used_at`, and `status`. Reveal and rotate actions return the raw token only in that action's no-store response. They never return it in a later list response.

Create a linked token using the primary token and a JSON body. `expiry` accepts `never`, `7d`, `30d`, `90d`, `1y`, or `custom`; for `custom`, supply a future `expires_on` date in `YYYY-MM-DD` format.

```sh
curl -q -LSsf -X POST \
  -H "Authorization: Bearer PRIMARY_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"mcp-client","expiry":"90d"}' \
  http://YOUR_SERVER/api/v1/server/preferences/tokens
```

The raw value in the successful create, reveal, or rotate response is a credential. Store it in the MCP client's secret configuration; do not put it in a URL or log.
