## Project description

Countries is a full-stack Go web application providing detailed information about all 249 countries worldwide. It exposes country data — names, ISO codes, capital, region, currencies, languages, population, area, flags, borders, and timezones — through a versioned REST API, GraphQL endpoint, and a server-side rendered web UI. All data is embedded in the binary at build time. Deployed as a single self-contained static binary.

## Project variables

project_name: countries
project_org: apimgr
internal_name: countries
internal_org: apimgr
app_name: Countries API
repo: https://github.com/apimgr/countries
license: MIT
binary: countries
client_binary: countries-cli

## Business logic

### Product scope & non-goals

**In scope:**
- 249 countries with comprehensive per-country data (names, ISO codes, capital, region, currencies, languages, population, area, flag, borders, timezones, TLDs)
- Search/filter by name, code, region, currency, or language
- Full web frontend (server-side Go templates, dark/light/auto theme, PWA, mobile-first)
- Server pages: `/server/about`, `/server/contact`, `/server/dpo`, `/server/help`, `/server/help/mcp` when MCP is enabled, `/server/healthz`, `/server/preferences`, `/server/privacy`, `/server/security`, and `/server/terms`
- CLI client (`countries-cli`) for queries from the terminal
- OpenAPI/Swagger docs at `/api/{api_version}/server/swagger`
- GraphQL at `/api/graphql` (with its explorer at `/server/docs/graphql`)

**Non-goals:**
- No user accounts, registration, or login of any kind
- No admin web panel (server configured via `server.yml` only)
- No write/mutation API (data is read-only, embedded at build time)
- No real-time data (exchange rates, political events, population census updates)
- No paid tiers, no API keys, no rate-limited access tiers

### Roles & permissions

There are no user roles. All endpoints are public and require no authentication.

| Actor | Access |
|-------|--------|
| **Anonymous visitor (browser)** | Full read access to all web pages and API endpoints |
| **Anonymous API client (curl/CLI)** | Full read access to all API endpoints |
| **Server operator** | Configures server via `server.yml` only; no web management interface |

### Data model & sensitivity

**Country record** (embedded at build time, no PII):

| Field | Type | Sensitivity |
|-------|------|-------------|
| `name.common` / `name.official` | string | Public |
| `cca2` / `cca3` | string — ISO codes | Public |
| `capital` | string[] | Public |
| `region` / `subregion` | string | Public |
| `currencies` | map[code]{ name, symbol } | Public |
| `languages` | map[code]name | Public |
| `population` | integer | Public |
| `area` | float — km² | Public |
| `flag` | string — emoji + SVG URL | Public |
| `latlng` | float[2] | Public |
| `borders` | string[] — cca3 codes | Public |
| `timezones` | string[] — IANA identifiers | Public |
| `tld` | string[] | Public |

Country records contain no PII. Contact form submissions contain the visitor's name, email address, subject, and message; they are validated and sent through configured SMTP without being stored in the country dataset or database.

### Trust boundaries & external services

| Boundary | Trust level | Notes |
|----------|-------------|-------|
| Country dataset (embedded at build) | Fully trusted | Static, compiled into binary |
| Incoming HTTP requests | **Untrusted** | All query parameters validated |
| Configured SMTP server | Configured by operator | Receives only visitor-submitted contact messages |
| Configured reCAPTCHA or hCaptcha service | Configured by operator | Receives contact-form challenge verification data; the self-hosted challenge is used by default |

Core country lookups make no external runtime calls. Contact delivery and third-party challenge verification make network calls only when the visitor submits the contact form and the operator has configured those providers.

### Threat model & abuse cases

**Primary assets:** service availability.

**Attacker/abuser goals:**
- DoS via high-rate requests or expensive fuzzy name searches
- Bulk scraping of the full dataset
- Spam or abuse the public contact form, including attempts to inject headers or submit oversized messages
- Disrupt contact delivery by causing SMTP or challenge-verification failures

**Defenses:**
- Rate limiting on all endpoints
- Request size limits on all inputs
- Validate and bound contact fields, use the configured fixed recipient, and reject invalid challenge responses; if SMTP is unavailable or unconfigured, report that contact delivery is unavailable without affecting country lookup
- No user accounts eliminates credential stuffing and privilege escalation entirely

### Security decisions & exceptions

- **No authentication on any endpoint**: intentional. Public read-only reference API.
- **All responses include `Access-Control-Allow-Origin: *`**: intentional. Public data API designed for cross-origin browser use.
- **Public contact form**: intentional; it has no accounts or message storage, accepts bounded submissions protected by the configured challenge, and sends only to the operator-configured recipient when SMTP is configured.

### API routes

All routes below are public and require no authentication. Responses use the documented JSON envelope unless marked plain text.

| Method | Path | Purpose and request/response summary |
|---|---|---|
| GET | `/api/{api_version}/countries` | List countries with pagination and optional search, code, region, currency, and language filters; returns country records and pagination metadata. |
| GET | `/api/{api_version}/countries/{code}` | Read a country by alpha-2, alpha-3, or numeric code; returns one record or not found. |
| GET | `/api/{api_version}/countries/search` | Search country names and fields using `q` and optional code, region, currency, or language filters; returns matching records. |
| GET | `/api/{api_version}/coordinates` | Find the nearest country using `lat` and `lon` query parameters. |
| POST | `/api/{api_version}/coordinates` | Find the nearest country using a JSON body containing `lat` and `lon`. |
| GET | `/api/{api_version}/nearby` | Find country centroids within a radius using `lat`, `lon`, and `radius` query parameters. |
| GET | `/api/{api_version}/stats` | Return dataset count, version, and data source metadata. |
| GET | `/api/{api_version}/stats.txt` | Return dataset statistics as plain text. |
| GET | `/api/{api_version}/count` | Return the dataset count in the JSON envelope. |
| GET | `/api/{api_version}/count.txt` | Return the dataset count as plain text. |
| GET | `/api/{api_version}/server/healthz` | Return service health. |
| GET | `/api/{api_version}/server/about` | Return service information. |
| GET | `/api/{api_version}/server/help` | Return API usage help. |
| GET | `/api/{api_version}/server/privacy` | Return the privacy notice. |
| GET | `/api/{api_version}/server/terms` | Return the service terms. |
| GET | `/api/{api_version}/server/security` | Return security reporting information. |
| GET | `/api/{api_version}/server/contact` | Return contact information; contact submissions are specified for the browser form. |
| GET | `/api/{api_version}/server/dpo` | Return configured DPO contact details and the absolute privacy URL. |
| GET | `/api/{api_version}/server/preferences` | Read effective guest theme and language preferences. |
| POST | `/api/{api_version}/server/preferences` | Save the caller's guest theme and language cookies. |
| GET | `/api/{api_version}/server/preferences/sync` | Export guest preferences as a share URL and short code. |
| GET | `/api/{api_version}/server/preferences/sync/import` | Apply supported preferences from query values or a share code. |
| POST | `/api/{api_version}/server/graphql` | Execute read-only GraphQL queries against the country dataset. |
