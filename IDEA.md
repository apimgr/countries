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
- Server pages: `/server/about`, `/server/help`, `/server/healthz`, `/server/privacy`, `/server/terms`
- CLI client (`countries-cli`) for queries from the terminal
- OpenAPI/Swagger docs at `/api/{api_version}/server/swagger`
- GraphQL at `/graphql`

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

No PII stored or served.

### Trust boundaries & external services

| Boundary | Trust level | Notes |
|----------|-------------|-------|
| Country dataset (embedded at build) | Fully trusted | Static, compiled into binary |
| Incoming HTTP requests | **Untrusted** | All query parameters validated |

No external services called at runtime.

### Threat model & abuse cases

**Primary assets:** service availability.

**Attacker/abuser goals:**
- DoS via high-rate requests or expensive fuzzy name searches
- Bulk scraping of the full dataset

**Defenses:**
- Rate limiting on all endpoints
- Request size limits on all inputs
- No user accounts eliminates credential stuffing and privilege escalation entirely

### Security decisions & exceptions

- **No authentication on any endpoint**: intentional. Public read-only reference API.
- **All responses include `Access-Control-Allow-Origin: *`**: intentional. Public data API designed for cross-origin browser use.
