# API reference

The canonical REST API is versioned under `/api/v1/`. Country data endpoints are public and read-only; server endpoints also provide preference operations, owner-token management, reports, GraphQL, and MCP. JSON responses use two-space indentation and end with one newline. Success responses use `{"ok":true,"data":{...}}`; list responses also include `pagination`. Errors use `{"ok":false,"error":"CODE","message":"...","details":{...}}` when structured details apply. HTTP status carries the status; it is not repeated in the JSON body. Health endpoints return the bare health object, and protocol compatibility endpoints retain their native response format. Browser cross-origin behavior follows the configured CORS policy.

### Response negotiation

API endpoints return JSON by default. `Accept: application/json` explicitly selects JSON, `Accept: text/plain` selects a text serialization, and HTTP tools such as curl and wget receive text when no explicit format is requested. A `.txt` suffix selects text for GET API routes; the suffix is removed before route matching. Text API responses are serialized from the JSON response model and end with one newline. Text errors use `ERROR: CODE: message`. The health text form is flattened `key: value` data. `/api/v1/stats.txt` and `/api/v1/count.txt` are documented explicit text routes; the suffix form is available for other GET routes too.

Frontend pages with matching public GET APIs use the same response model: explicit `Accept: application/json` returns the API envelope, explicit `Accept: text/plain` returns formatted text, and curl/wget or an empty user agent defaults to text. Explicit `Accept: text/html` and text-browser user agents receive server-rendered HTML; the `countries-cli` user agent receives JSON. HTML remains the default for ordinary browsers. API auth and query parameters are preserved during negotiation.

Metrics endpoints keep their protocol media types: Prometheus endpoints return the exposition text format, while Grafana and Loki endpoints return JSON. When `server.metrics.enabled` is true, these paths appear in the runtime-generated OpenAPI document: `/server/metrics[/{prometheus,grafana,loki}]`, `/api/v1/server/metrics[/{prometheus,grafana,loki}]`, and `/api/metrics[/{prometheus,grafana,loki}]`. `/metrics[/{prometheus,grafana,loki}]` is included when `server.metrics.root.enabled` is true. Each service requires its configured bearer token unless unauthenticated access is enabled.

Successful public GET responses for country data, search, coordinates, nearby, statistics, and count endpoints use `Cache-Control: public, max-age=60` and vary by language and cookie. Error responses, writes, server pages, and other private API responses use `no-store` or `private, no-store`.

When debug endpoints are enabled with `--debug`, a truthy `DEBUG`, or `MODE=debug` with `DEBUG` unset, every root `/debug/*` route requires the operator bearer token. The routes include `/debug/config`, `/debug/routes`, `/debug/cache`, `/debug/db`, `/debug/scheduler`, `/debug/error500`, `/debug/memory`, `/debug/goroutines`, `/debug/vars`, and `/debug/pprof` with its profile subpaths. `server.debug.pprof` and `server.debug.runtime_endpoints` can disable their endpoint groups. Debug routes have no API aliases and are absent when the runtime debug flag is off. Configuration output omits secret-only fields; goroutine stacks are returned as plain text.

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

The API also provides `GET /api/v1/server/preferences/tokens` (masked token list), token issue/reveal/rotate/create/update/revoke operations under `/api/v1/server/preferences/tokens`, and `POST /api/v1/server/preferences` (save guest preferences). Owner-token management requires the primary owner's bearer token except initial token issuance. Full method and response metadata is published at `/api/v1/server/swagger`.

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

The browser interface also includes `/`, `/search`, and `/coordinates`. Server pages include `/server/about`, `/server/contact`, `/server/dpo`, `/server/help`, `/server/help/mcp`, `/server/healthz`, `/server/preferences`, `/server/preferences/sync`, `/server/preferences/tokens`, `/server/privacy`, `/server/security`, `/server/security/policy`, `/server/security/thanks`, and `/server/terms`. `/server/security/report/{tracking_id}?token={status_token}` is a one-time-per-day emailed status page and has no API mirror. `/server/docs/swagger` renders the OpenAPI explorer and `/server/docs/graphql` renders the GraphQL explorer. `/server` redirects to `/server/about`; `/healthz` is available only when `server.healthz.root.enabled` is true. The contact page is a reporting and privacy-request channel, not general support. It handles security reports, abuse/terms reports, DMCA notices, and privacy requests.

| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/server/healthz` | Public-safe health status, version/build information, enabled features, component checks, and aggregate request statistics. |
| GET | `/api/v1/server/auth/check` | Validate an operator or owner bearer token without changing token activity metadata; returns `kind` or 401. |
| GET | `/api/v1/server/about` | Service information. |
| GET | `/api/v1/server/help` | Help page data: title, configured Markdown source for the resolved language (then `default`), or built-in localized guidance when no configured entry matches; `format: markdown`; and registered help pages (`title` and `path`) in hub order. |
| GET | `/api/v1/server/privacy` | Privacy notice. |
| GET | `/api/v1/server/terms` | Service terms. |
| GET | `/api/v1/server/security` | Security overview: configured reporting contacts, expiry, PGP key availability and fingerprint, and absolute policy, thanks, and report URLs. Missing channels are omitted; the PGP URL and fingerprint are `null` when no key exists. |
| GET | `/api/v1/server/security/policy` | Disclosure policy Markdown. |
| GET | `/api/v1/server/security/thanks` | Researcher acknowledgments (`credit`, ISO date, and severity), newest first; returns an empty list when none are published. |
| GET | `/api/v1/server/contact` | Report form field descriptors, a new CAPTCHA challenge, a single-use `form_token`, offered types, security URL, and public abuse email. This route returns 404 when disabled. |
| POST | `/api/v1/server/contact` | Submit a report for review with the single-use `form_token`, exactly one CAPTCHA proof, and `step: "submit"`; confirm with all original fields, `step: "confirm"`, the returned `review_token`, and `confirmed: true`. The first call never delivers. Security mode requires a valid `security_id`. |
| GET | `/api/v1/server/dpo` | Data protection contact (`name`, `email`, and absolute `privacy_url`). The email falls back from `server.contact.dpo.email` to `server.contact.general.email`; no address is invented. |
| GET | `/api/v1/server/preferences` | Read effective theme and language preferences and the empty guest preference set. |
| POST | `/api/v1/server/preferences` | Save supported theme and language cookies; JSON and form bodies are accepted. This API mirror does not use CSRF tokens. |
| GET | `/api/v1/server/preferences/sync` | Return the current theme and language plus an unpadded base64url code for browser-to-browser transfer. |
| POST | `/api/v1/server/preferences/sync` | Apply guest preferences from JSON or form fields (`code`, `theme`, `lang`); no CSRF token is used. Returns `400 VALIDATION_FAILED` if no supported preference was applied. |
| GET | `/api/v1/server/preferences/sync/import` | Apply supported preferences from direct query values or an exported code; return `applied` and `ignored` parameter arrays. |
| GET | `/server/preferences` | Browser page for the current theme and language preferences. |
| GET | `/server/preferences/sync` | Browser page to copy a code representing the current theme and language preferences and apply a code from another browser. |
| POST | `/server/preferences` | Save supported theme and language cookies. Browser forms require the CSRF token rendered by server pages. |
| GET | `/api/v1/server/swagger` | OpenAPI 3.1 document. |
| GET | `/api/v1/server/graphql?query=...` | Read-only GraphQL query; optional `variables` and `operationName` parameters are JSON and text respectively. Without `query`, returns a short plain-text usage message. |
| POST | `/api/v1/server/graphql` | Read-only GraphQL query endpoint. The `read(path:)` field executes allowlisted GET JSON APIs with the caller's bearer token and request context; state-changing GET, text, internal, and documentation paths are excluded. |
| POST | `/api/v1/server/mcp` | Streamable HTTP MCP endpoint; requires a valid owner or linked bearer token and protocol revision `2026-07-28`. |
| GET | `/api/v1/server/help/mcp` | MCP connection details, endpoint URL, aliases, and available tools. |
| GET | `/api/v1/server/preferences/tokens` | List masked primary and linked tokens with their scope; primary global-scope owner bearer token required. |
| POST | `/api/v1/server/preferences/tokens/primary` | Issue a primary owner token for a caller without a valid bearer token. |
| POST | `/api/v1/server/preferences/tokens/primary/reveal` | Reveal the primary token; primary owner bearer token required. |
| POST | `/api/v1/server/preferences/tokens/primary/rotate` | Rotate the primary token; primary owner bearer token required. |
| DELETE | `/api/v1/server/preferences/tokens/primary` | Revoke the primary token and delete its owner, linked tokens, and resources; primary owner bearer token and the last four token characters are required. |
| POST | `/api/v1/server/preferences/tokens` | Create a linked token with `read` or `read-write` scope; primary global-scope owner bearer token required. |
| POST | `/api/v1/server/preferences/tokens/{id}/reveal` | Reveal a linked token; primary owner bearer token required. |
| POST | `/api/v1/server/preferences/tokens/{id}/rotate` | Rotate a linked token; primary owner bearer token required. |
| PATCH | `/api/v1/server/preferences/tokens/{id}` | Rename a linked token or change its expiry; primary owner bearer token required. |
| DELETE | `/api/v1/server/preferences/tokens/{id}` | Revoke a linked token; primary owner bearer token required. |
| POST | `/api/v1/server/reports/{report}` | Receive CSP/Reporting API reports. |
| POST | `/server/consent` | Save a browser's accept/decline choice for optional cookies; requires the page's CSRF token and redirects to a validated local path. |
| POST | `/server/ccpa` | Record a browser's Global Privacy Control opt-out choice when the configured privacy policy enables data sales. |
| POST | `/announcements/dismiss` | Dismiss a displayed announcement for the browser; requires the page's CSRF token. |
| GET | `/server/security/report/{tracking_id}?token={status_token}` | Show the limited status for a security report using its report-status token. A valid token is single-use per UTC day. |

The server-rendered browser forms also accept `POST /server/contact`, `POST /server/preferences`, `POST /server/preferences/sync`, and the owner-token actions under `/server/preferences/token/*` and `/server/preferences/tokens/*`. Token-management mutations require the owner cookie and CSRF token; other state-changing browser forms require CSRF where applicable. Initial owner-token create/restore use their one-time browser flow. `/server/docs/graphql` serves a same-origin GraphiQL explorer and a server-rendered query form that remains usable without JavaScript; the GraphQL endpoint accepts read-only GET queries and JSON POST requests.

## Other public routes

| Method | Path | Description |
|---|---|---|
| GET | `/manifest.json` | Web app manifest. |
| GET | `/sw.js` | Service worker for the PWA. |
| GET | `/robots.txt` | Configured crawler rules and sitemap URL. |
| GET | `/sitemap.xml` | Sitemap of eligible public HTML pages. |
| GET | `/.well-known/security.txt` | Security contact and disclosure policy. |
| GET | `/.well-known/pgp-key.asc` | Security-report public key when one is configured; otherwise 404. |
| GET | `/llms.txt`, `/.well-known/llms.txt` | Generated plain-text API and integration index. |
| GET | `/server/tracking.js` | Consent-aware tracking bootstrap script. |
| GET | `/api/autodiscover` | Unversioned server, API, and CLI discovery for clients. |
| GET | `/cli/binaries/{binary}` | Download an allowlisted companion CLI release binary. |

The stable unversioned API aliases are `/api/healthz`, `/api/healthz.txt`, `/api/swagger`, `/api/graphql`, `/api/mcp`, and `/mcp`; they call the same handlers as their canonical routes without redirects. The GraphQL alias accepts both GET queries and JSON POST requests, matching `/api/v1/server/graphql`. Metrics and debug routes are feature/configuration gated and documented in [Integrations](integrations.md) and [Configuration](configuration.md).

## Well-known discovery

`/.well-known/security.txt` publishes the GitHub private reporting channel, the enabled deployment reporting form, any configured security email, the policy URL, and an expiration date. `/llms.txt` and `/.well-known/llms.txt` publish a plain-text API index. Unsupported well-known paths return 404.

`/.well-known/pgp-key.asc` serves the ASCII-armored public security-report key as `application/pgp-keys` when a key exists; it returns 404 when no key has been generated. The security overview links to the disclosure policy and acknowledgments pages.

`/robots.txt` reflects the configured public crawl rules and includes an absolute `Sitemap:` URL. `/sitemap.xml` is generated per request, includes only allowed public HTML pages, and excludes API, contact, and preference endpoints.

## Browser preferences

The `/server/preferences` page saves theme (`dark`, `light`, or `auto`), language (`en`, `es`, `fr`, `de`, `it`, `ar`, `zh`, or `ja`), and cookie-consent choices. The CCPA opt-out control is available according to configured privacy settings. A first-visit banner offers accept and decline actions through `POST /server/consent`; browser forms require the page's CSRF token. Theme and language cookies are client-readable, last one year, use `SameSite=Lax`, and are not persisted server-side. The `/server/preferences/sync` page displays a code for current theme and language and accepts a pasted code to apply those values in another browser. `GET /api/v1/server/preferences/sync` returns current values and a transfer code; `POST /api/v1/server/preferences/sync` applies a valid JSON or form update without CSRF; `GET /api/v1/server/preferences/sync/import` applies supported preferences from direct query values or an exported code and returns `applied` and `ignored` arrays. Consent, CCPA choices, and credentials are not included. API preference writes accept JSON or form values without CSRF because they do not use ambient credentials. Web preference writes redirect with `303`; API preference writes return JSON.

### Owner token and MCP authentication

Owner tokens identify anonymous resource owners; there are no user accounts. Primary tokens have `global` scope and manage linked tokens. Linked tokens have `read` or `read-write` scope, defaulting to `read-write`; they can access their owner's resources but cannot manage tokens. A `read` token is limited to GET, HEAD and OPTIONS. Generate a primary token with `POST /api/v1/server/preferences/tokens/primary`, then create a linked token for each MCP client. Send the token only in the `Authorization: Bearer` header. A valid token without the required scope receives `403` and `WWW-Authenticate: Bearer error="insufficient_scope"`. `GET /api/v1/server/auth/check` validates operator and owner tokens without updating last-used timestamps. MCP does not accept cookies and never exposes token management as tools. Keep raw tokens out of URLs and logs. The token preferences page at `/server/preferences/tokens` provides the browser management flow.

For MCP `POST` requests, send the required `MCP-Protocol-Version` and `Mcp-Method` headers matching the request body; tool calls also require `Mcp-Name`. Only protocol revision `2026-07-28` is supported. See `/server/help/mcp` for the deployment-specific endpoint URL and the live tool list.

The token-list response has `data.primary`, `data.linked`, and `data.max_linked`. Each token object includes `id`, `name`, `kind`, `scope`, `token_masked`, `created_at`, `last_used_at`, `expires_at`, and `status`. Token `id` values are opaque UUID v7 identifiers, never sequential database integers. On upgraded databases that retain an integer primary key internally, the public UUID is stored as `public_id`; only that UUID appears in URLs, JSON, logs, and audit events. Linked-token create, reveal, and rotate responses include the raw credential as `linked_token` alongside the masked token object; primary-token reveal and rotate use `owner_token`. These no-store responses never expose a raw token in a later list response.

Create a linked token using the primary token and a JSON body. `scope` accepts `read` or `read-write` (default `read-write`). `expires` accepts `never`, `7d`, `30d`, `90d`, or `1y` (default `never`); alternatively, supply `expires_on` as a UTC date in `YYYY-MM-DD` format. Do not send both expiry fields. A date may be today or later in UTC and remains valid through that day; it expires at 00:00 UTC the following day. An update can independently set `name`, `expires`, or `expires_on`; omitted fields remain unchanged.

```sh
curl -q -LSsf -X POST \
  -H "Authorization: Bearer PRIMARY_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"mcp-client","scope":"read-write","expires":"90d"}' \
  http://YOUR_SERVER/api/v1/server/preferences/tokens
```

The raw value in the successful create, reveal, or rotate response is a credential. Store it in the MCP client's secret configuration; do not put it in a URL or log.
