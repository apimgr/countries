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
owner_token: countries_owner_token_gId2fHet

## Business logic

### Product scope & non-goals

**In scope:**
- 249 countries with comprehensive per-country data (names, ISO codes, capital, region, currencies, languages, population, area, flag, borders, timezones, TLDs)
- Search/filter by name, code, region, currency, or language
- Full mobile-first browser interface with server-rendered country search, coordinate lookup, service information, preferences, documentation explorers, privacy, and security reporting pages; pages work without JavaScript for core navigation and forms
- CLI client (`countries-cli`) for queries from the terminal
- OpenAPI reference and interactive REST explorer
- Read-only GraphQL API and interactive query explorer
- Optional authenticated MCP access with anonymous owner-token management, plus guest preference and consent controls, public privacy requests, contact delivery, and encrypted security-report handling

**Non-goals:**
- No user accounts, registration, or login of any kind
- No admin web panel (server configured via `server.yml` only)
- No write or mutation API for country records; the embedded country dataset is read-only. Separate server endpoints support preferences, anonymous owner-token management, report submission, and CSP/reporting intake.
- No real-time data (exchange rates, political events, population census updates)
- No paid tiers or key-based usage plans; owner tokens only identify resource ownership and authorize the mandatory MCP and token-management flows

### Roles & permissions

There are no user accounts or login roles. Public country reads and server information do not require authentication. Anonymous owners may obtain a primary token to bind resources to an owner identity; the primary manages linked tokens, linked tokens access the same owner's resources without token-management rights, and each linked token has `read` or `read-write` scope and may have an expiry. The operator token authorizes global management actions. MCP requires a bearer token.

| Actor | Access |
|-------|--------|
| **Anonymous visitor (browser)** | Full read access to all web pages and API endpoints |
| **Anonymous API client (curl/CLI)** | Full read access to public country and information endpoints; owner token routes require a primary token, and MCP requires a bearer token |
| **Owner token holder** | Accesses resources associated with its owner; the primary token manages linked tokens, while linked tokens cannot manage tokens |
| **Operator** | Global management access using `server.token`; no operator account or web administration panel |
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

Country records contain no PII. Ordinary contact requests are sent through configured SMTP without being stored in the country dataset or database. Security reports are retained in the database as encrypted content with separate metadata, according to the security-report retention rules in AI.md.

### Trust boundaries & external services

| Boundary | Trust level | Notes |
|----------|-------------|-------|
| Country dataset (embedded at build) | Fully trusted | Static, compiled into binary |
| Incoming HTTP requests | **Untrusted** | All query parameters validated |
| Configured SMTP server | Configured by operator | Receives only visitor-submitted contact messages |
| Configured HTTPS notification webhooks | Configured by operator | Receive signed operational notices and report summaries only; failures are retried then dropped without affecting country reads or stored reports |
| Configured reCAPTCHA or hCaptcha service | Configured by operator | Receives contact-form challenge verification data; the self-hosted challenge is used by default |

Core country lookups make no external runtime calls. Contact delivery and third-party challenge verification make network calls only when a visitor submits the contact form and the operator has configured those providers. Webhook calls occur only for configured notification events and report submissions.

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

- **No user accounts or login**: intentional. Anonymous owner and linked bearer tokens are credentials for resource ownership, token management and MCP only.
- **Public country data is cross-origin by default**: the default policy allows `*` without credentials; operators may configure an explicit origin list, which is required for credentialed browser requests.
- **Public contact form**: intentional; it has no accounts. Ordinary contact requests are not stored by the service; security reports are retained as encrypted content. Submissions are bounded, protected by the configured challenge, and delivered only to configured recipients when SMTP is configured.

### API routes

Country-data and public information routes below require no authentication. Owner-token API operations require the owner bearer token; browser mutations require the owner cookie and CSRF token except initial token issue/restore, which use the one-time browser flow. Contact and CCPA forms are public and antispam-protected. API responses use the documented JSON envelope unless marked plain text. The `{api_version}` placeholder currently resolves to `v1`.

| Method | Path | Auth | Purpose and request/response summary |
|---|---|---|
| GET | `/api/{api_version}/countries` | Public | List countries with pagination and optional search, code, region, currency, and language filters; returns country records and pagination metadata. |
| GET | `/api/{api_version}/countries/{code}` | Public | Read a country by alpha-2, alpha-3, or numeric code; returns one record or not found. |
| GET | `/api/{api_version}/countries/search` | Public | Search country names and fields using `q` and optional code, region, currency, or language filters; returns matching records. |
| GET | `/api/{api_version}/coordinates` | Public | Find the nearest country using `lat` and `lon` query parameters. |
| POST | `/api/{api_version}/coordinates` | Public | Find the nearest country using a JSON body containing `lat` and `lon`. |
| GET | `/api/{api_version}/nearby` | Public | Find country centroids within a radius using `lat`, `lon`, and `radius` query parameters. |
| GET | `/api/{api_version}/stats` | Public | Return dataset count, version, and data source metadata. |
| GET | `/api/{api_version}/stats.txt` | Public | Return dataset statistics as plain text. |
| GET | `/api/{api_version}/count` | Public | Return the dataset count in the JSON envelope. |
| GET | `/api/{api_version}/count.txt` | Public | Return the dataset count as plain text. |
| GET | `/api/{api_version}/server/healthz` and `.txt` | Public | Return service health as negotiated JSON or plain text. |
| GET | `/api/{api_version}/server/about` | Public | Return service information. |
| GET | `/api/{api_version}/server/help` | Public | Return API usage help. |
| GET | `/api/{api_version}/server/help/mcp` | Public | Return MCP endpoint and tool documentation. |
| GET | `/api/{api_version}/server/privacy` | Public | Return the privacy notice. |
| GET | `/api/{api_version}/server/terms` | Public | Return the service terms. |
| GET | `/api/{api_version}/server/security` | Public | Return security reporting information. |
| GET | `/api/{api_version}/server/security/policy` | Public | Return the disclosure policy. |
| GET | `/api/{api_version}/server/security/thanks` | Public | Return disclosed researcher acknowledgments. |
| GET | `/api/{api_version}/server/contact` | Public | Return contact form field definitions and validation metadata. |
| POST | `/api/{api_version}/server/contact` | Public, antispam | Submit a two-step contact request or security report; returns delivery status and, for a report, its tracking ID. |
| POST | `/api/{api_version}/server/reports/{report}` | Security report credential | Submit or confirm a security-report step; returns validation/review or the resulting tracking ID. |
| GET | `/api/{api_version}/server/dpo` | Public | Return configured DPO contact details and the absolute privacy URL. |
| GET | `/api/{api_version}/server/preferences` | Public | Read effective guest theme and language preferences. |
| POST | `/api/{api_version}/server/preferences` | Public, CSRF | Save the caller's guest theme and language preferences. |
| GET | `/api/{api_version}/server/preferences/sync` | Public | Export guest preferences as a share URL and short code. |
| POST | `/api/{api_version}/server/preferences/sync` | Public, CSRF | Save selected guest preferences for synchronization. |
| GET | `/api/{api_version}/server/preferences/sync/import` | Public | Apply supported preferences from query values or a share code. |
| GET | `/api/{api_version}/server/preferences/tokens` | Primary owner token (`global` scope) | List primary and linked token metadata, including scope, without exposing raw tokens. |
| POST | `/api/{api_version}/server/preferences/tokens/primary` | Public one-time issue flow | Create the owner and primary token once; later attempts conflict. |
| POST | `/api/{api_version}/server/preferences/tokens/primary/reveal` | Owner token | Reveal the primary token through the protected one-time display flow. |
| POST | `/api/{api_version}/server/preferences/tokens/primary/rotate` | Owner token | Rotate the primary token while preserving linked tokens. |
| DELETE | `/api/{api_version}/server/preferences/tokens/primary` | Owner token | Revoke the primary token, its linked tokens, and all resources owned by the token. |
| POST | `/api/{api_version}/server/preferences/tokens` | Primary owner token (`global` scope) | Create a linked token with a name, `read` or `read-write` scope, and optional expiry. |
| POST | `/api/{api_version}/server/preferences/tokens/{id}/reveal` | Owner token | Reveal a linked token through the protected one-time display flow. |
| POST | `/api/{api_version}/server/preferences/tokens/{id}/rotate` | Owner token | Rotate one linked token. |
| PATCH | `/api/{api_version}/server/preferences/tokens/{id}` | Owner token | Rename or change the expiry of one linked token. |
| DELETE | `/api/{api_version}/server/preferences/tokens/{id}` | Owner token | Revoke one linked token. |
| GET, POST | `/api/{api_version}/server/graphql` | Public | Execute read-only GraphQL queries against the country dataset. |
| POST | `/api/{api_version}/server/mcp` | Owner token | Handle MCP initialize, discovery and tool calls scoped to the owner token. |
| GET | `/api/{api_version}/server/swagger` | Public | Return the OpenAPI document. |
| GET | `/api/{api_version}` | Public | Return the API and CLI discovery envelope; the trailing-slash path redirects here. |
| GET | `/` | Public | Render the country-service home page. |
| GET | `/search` | Public | Render country search results and search form. |
| GET | `/coordinates` | Public | Render the nearest-country lookup form and result. |
| GET | `/server` | Public | Redirect to `/server/about`. |
| GET | `/server/healthz` | Public | Render the service health page. |
| GET | `/healthz` | Public when enabled by configuration | Render the service health page. |
| GET | `/server/about`, `/server/help`, `/server/privacy`, `/server/terms`, `/server/security`, `/server/security/policy`, `/server/security/thanks`, `/server/help/mcp` | Public | Render the named server information page. |
| GET | `/server/contact` | Public | Render the contact form or security-report form when the rotating security ID is valid. |
| POST | `/server/contact` | Public, CSRF and antispam | Submit a two-step contact request or security report. |
| POST | `/server/ccpa` | Public, CSRF | Record a Global Privacy Control opt-out request. |
| GET | `/server/security/report/{tracking_id}` | Public with report status token | Render the limited triage state for the matching tracking ID. |
| GET | `/server/dpo` | Public | Render configured DPO contact details. |
| GET | `/server/preferences` | Public | Render language, appearance, and consent preferences. |
| POST | `/server/preferences` | Public, CSRF | Save guest language, appearance, and consent preferences. |
| GET | `/server/preferences/sync`, `/server/preferences/sync/import` | Public | Render or apply the guest preference synchronization flow. |
| POST | `/server/preferences/sync` | Public, CSRF | Save selected guest preferences for synchronization. |
| GET | `/server/preferences/tokens` | Owner cookie | Render primary and linked owner-token management. |
| POST | `/server/preferences/token/create`, `/server/preferences/token/restore` | Public one-time issue/restore flow | Create an owner token once or restore an owner session by pasting its primary token. |
| POST | `/server/preferences/token/forget` | Owner cookie, CSRF | Clear the current browser's owner-token session. |
| POST | `/server/preferences/token/reveal`, `/server/preferences/token/rotate` | Owner cookie, CSRF | Reveal or rotate the primary token. |
| POST | `/server/preferences/tokens` | Owner cookie, CSRF | Create a linked token. |
| POST | `/server/preferences/tokens/{id}/reveal`, `/server/preferences/tokens/{id}/rotate`, `/server/preferences/tokens/{id}/update`, `/server/preferences/tokens/{id}/revoke` | Owner cookie, CSRF | Reveal, rotate, rename/expire, or revoke a linked token. |
| POST | `/server/consent` | Public, CSRF | Save or withdraw consent choices. |
| POST | `/announcements/dismiss` | Public, CSRF | Dismiss a visible announcement for the browser. |
| GET | `/server/docs/swagger` | Public | Render REST API documentation. |
| GET, POST | `/server/docs/graphql` | Public | Render the GraphiQL explorer or execute a query from its form. |
| GET | `/manifest.json`, `/sw.js`, `/robots.txt`, `/sitemap.xml` | Public | Serve PWA metadata, service worker, crawler policy, or sitemap. |
| GET | `/.well-known/security.txt`, `/.well-known/pgp-key.asc`, `/.well-known/llms.txt`, `/llms.txt` | Public | Serve security contact, feature-gated PGP key, or generated agent-discovery text. |
| GET | `/server/tracking.js` | Public | Serve the consent-aware tracking bootstrap script. |
| GET | `/api/healthz`, `/api/healthz.txt`, `/api/swagger` | Public | Stable unversioned health and OpenAPI discovery aliases. |
| GET, POST | `/api/graphql` | Public | Execute read-only GraphQL queries through the stable unversioned alias; GET accepts `query`, optional `variables`, and `operationName`, and POST accepts a JSON request body. |
| GET | `/api/autodiscover` | Public | Return server, API, overlay-address, and CLI version discovery data. |
| GET | `/cli/binaries/{binary}` | Public | Download an allowlisted CLI binary. |
| POST | `/mcp`, `/api/mcp` | Owner token | Handle MCP protocol requests using the compatibility endpoint aliases. |
