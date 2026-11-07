# Security and privacy

## Service model

The API is public and read-only, with no user accounts or write operations. Request inputs and bodies are bounded and validated. The service embeds country data and does not call external services for lookups.

Every response receives a request ID. The server applies configurable request limits, only honors client-address headers from a trusted immediate proxy, and sends security headers including Content-Security-Policy. Rate-limit counters currently live in process memory and reset when the process restarts.

Access logs include request ID, method, path, status, and duration. Do not put secrets or sensitive personal data into URLs or request IDs.

## Health and reporting

`/server/healthz` serves a browser health page; `/api/v1/server/healthz` returns JSON. Health output includes service version and loaded record count.

The service publishes canonical `/.well-known/security.txt` with contact, policy, and annual expiration fields; `/security.txt` is not served. `/llms.txt` and `/.well-known/llms.txt` provide API discovery. The policy page is `/server/security`. The CSP/Reporting API receiver is `/api/v1/server/reports/{report}` and accepts only supported report media types with a 1 MiB body limit.

## Report a vulnerability

Use [GitHub private vulnerability reporting](https://github.com/apimgr/countries/security/advisories/new) for vulnerabilities in the project. Do not file vulnerability details in public issues. For a self-hosted deployment, contact its operator about deployment-specific concerns.

## Data and privacy

The service stores no user accounts or profiles. Its access logging may retain connection metadata such as paths and status codes. Operators control deployment logs and retention. Country data provenance and redistribution terms are documented in [`src/data/README.md`](../src/data/README.md).
