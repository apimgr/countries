# Security and privacy

## Service model

Country data reads are public and read-only. There are no user accounts. Owner-token management and report submission are separate server operations; MCP requires a bearer token. Request inputs and bodies are bounded and validated. The service embeds country data and does not call external services for lookups.

Optional analytics can be configured with `server.tracking`. Provider scripts are rendered only after analytics consent; they are absent for requests served through onion and I2P addresses. The same-origin `/server/tracking.js` endpoint returns 404 if tracking is disabled, consent is absent, or the request uses an overlay address. See [configuration](configuration.md#analytics-tracking) for provider options and privacy behavior.

Every response receives a request ID. Rate limiting is always on and its request/window values are configurable. Anonymous traffic is keyed by client IP, valid bearer-token traffic by token, and failed authentication by IP. The server honors client-address headers only from a trusted immediate proxy and sends security headers including Content-Security-Policy. When started by the server binary, rate-limit counters are stored in the configured SQLite database and survive process restarts. The in-memory limiter is used only when the HTTP server is constructed without a database, such as in isolated tests.

Linked-token revocation and withdrawal of a previously granted non-essential consent send `Clear-Site-Data: "cache", "cookies", "storage"` by default. Configure `web.headers.clear_site_data` to control those events; `execution_contexts` is off by default because it reloads browser tabs.

Access logs include request ID, method, path, status, and duration. Do not put secrets or sensitive personal data into URLs or request IDs.

## Health and reporting

`/server/healthz` serves a browser health page; `/api/v1/server/healthz` returns JSON. Health output includes service version and loaded record count.

The service publishes canonical `/.well-known/security.txt` with GitHub private reporting, the deployment report form when enabled, a configured security email when present, policy, and annual expiration fields; `/security.txt` is not served. `/llms.txt` and `/.well-known/llms.txt` provide API discovery. The human-readable policy page is `/server/security`. The CSP/Reporting API receiver is `/api/v1/server/reports/{report}` and accepts only supported report media types with a 1 MiB body limit.

## Report a vulnerability

Use [GitHub private vulnerability reporting](https://github.com/apimgr/countries/security/advisories/new) for vulnerabilities in the project. Do not file vulnerability details in public issues. For a self-hosted deployment, use the reporting link on `/server/security`; it issues a rotating security identifier and stores report bodies encrypted at rest. The report form requires a confirmation step before ordinary reports are delivered. It handles abuse and terms violations at URLs on the site, DMCA notices, and privacy requests only. It does not accept support, bug reports, feature requests, or general questions.

## Data and privacy

The service stores no user accounts or profiles. Contact submissions are sent to the configured report recipients and are not added to the country dataset. Security report bodies and researcher contact details are encrypted at rest; status access uses a one-shot token delivered by email. Access logs contain request IDs, methods, URL paths, response statuses, and durations; they do not include client IP addresses or query-string values. Operators control deployment logs and retention. Browser theme and language preferences are stored in client-readable cookies; server-rendered state-changing forms use CSRF protection. `/server/dpo` displays only the DPO/general address supplied in configuration and otherwise directs visitors to the deployment's security page; no contact address is invented. Country data provenance and redistribution terms are documented in [`src/data/README.md`](https://github.com/apimgr/countries/blob/main/src/data/README.md).

## Owner tokens and MCP

There are no accounts or passwords. A primary owner token identifies an anonymous owner and manages linked tokens; linked tokens access the same owner's resources but cannot manage tokens. The operator bearer token grants global management access and is stored in `secrets.yml`. Owner and linked tokens are encrypted at rest and masked in token lists. Keep token values in the `Authorization: Bearer` header; the `/api/` routes ignore browser cookies. The MCP endpoint requires an owner or linked bearer token and never exposes token-management actions as tools. Give each MCP client its own linked token and revoke it when no longer needed.
