# Security and privacy

## Service model

The API is public and read-only, with no user accounts or write operations. Request inputs and bodies are bounded and validated. The service embeds country data and does not call external services for lookups.

Every response receives a request ID. The server applies configurable request limits, only honors client-address headers from a trusted immediate proxy, and sends security headers including Content-Security-Policy. When started by the server binary, rate-limit counters are stored in the configured SQLite database and survive process restarts. The in-memory limiter is used only when the HTTP server is constructed without a database, such as in isolated tests.

Access logs include request ID, method, path, status, and duration. Do not put secrets or sensitive personal data into URLs or request IDs.

## Health and reporting

`/server/healthz` serves a browser health page; `/api/v1/server/healthz` returns JSON. Health output includes service version and loaded record count.

The service publishes canonical `/.well-known/security.txt` with contact, policy, and annual expiration fields; `/security.txt` is not served. `/llms.txt` and `/.well-known/llms.txt` provide API discovery. The policy page is `/server/security`. The CSP/Reporting API receiver is `/api/v1/server/reports/{report}` and accepts only supported report media types with a 1 MiB body limit.

## Report a vulnerability

Use [GitHub private vulnerability reporting](https://github.com/apimgr/countries/security/advisories/new) for vulnerabilities in the project. Do not file vulnerability details in public issues. For a self-hosted deployment, contact its operator about deployment-specific concerns.

## Data and privacy

The service stores no user accounts or profiles. Access logs contain request IDs, methods, URL paths, response statuses, and durations; they do not include client IP addresses or query-string values. Operators control deployment logs and retention. Browser theme and language preferences are stored in cookies. Country data provenance and redistribution terms are documented in [`src/data/README.md`](https://github.com/apimgr/countries/blob/main/src/data/README.md).
