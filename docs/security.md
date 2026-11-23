# Security and privacy

## Service model

Country data reads are public and read-only. There are no user accounts. Owner-token management and report submission are separate server operations; MCP requires a bearer token. Request inputs and bodies are bounded and validated. The service embeds country data and does not call external services for lookups.

Optional analytics can be configured with `server.tracking`. Provider scripts are rendered only after analytics consent; they are absent for requests served through onion and I2P addresses. The same-origin `/server/tracking.js` endpoint returns 404 if tracking is disabled, consent is absent, or the request uses an overlay address. See [configuration](configuration.md#analytics-tracking) for provider options and privacy behavior.

Every response receives a request ID. Rate limiting is always on and its request/window values are configurable. Anonymous traffic is keyed by client IP, valid bearer-token traffic by token, and failed authentication by IP. The server honors client-address headers only from a trusted immediate proxy and sends security headers including Content-Security-Policy. When started by the server binary, rate-limit counters are stored in the configured SQLite database and survive process restarts. The in-memory limiter is used only when the HTTP server is constructed without a database, such as in isolated tests.

Linked-token revocation and withdrawal of a previously granted non-essential consent send `Clear-Site-Data: "cache", "cookies", "storage"` by default. Configure `web.headers.clear_site_data` to control those events; `execution_contexts` is off by default because it reloads browser tabs.

Access logs include request ID, method, path, status, and duration. Do not put secrets or sensitive personal data into URLs or request IDs.

## Health and reporting

`/server/healthz` serves a browser health page; `/api/v1/server/healthz` returns JSON. Health output includes service version and loaded record count.

The service publishes canonical `/.well-known/security.txt` with GitHub private reporting, the deployment report form when enabled, a configured security email when present, policy, and annual expiration fields; `/security.txt` is not served. `/llms.txt` and `/.well-known/llms.txt` provide API discovery. The human-readable overview is `/server/security`, the disclosure policy is `/server/security/policy`, and researcher acknowledgments are listed at `/server/security/thanks`. When a security-report PGP public key exists, `/.well-known/pgp-key.asc` serves it as `application/pgp-keys`; otherwise that endpoint returns 404. The overview links to the machine-readable security.txt, policy, acknowledgments, and key when available. The CSP/Reporting API receiver is `/api/v1/server/reports/{report}` and accepts only supported report media types with a 1 MiB body limit.

## Report a vulnerability

Use [GitHub private vulnerability reporting](https://github.com/apimgr/countries/security/advisories/new) for vulnerabilities in the project. Do not file vulnerability details in public issues. For a self-hosted deployment, use the reporting link on `/server/security`; it issues a rotating security identifier and stores report bodies encrypted at rest. When `security/pgp.pub.asc` in the configured configuration directory is present and valid, the body and maintainer notification use that OpenPGP key. Without a key, the body and notification attachment use AES-256-GCM with `server.security.encryption_key`. Researcher contact details always use AES-256-GCM because the server needs to read them for status notices. The report form requires a confirmation step before ordinary reports are delivered. It handles abuse and terms violations at URLs on the site, DMCA notices, and privacy requests only. It does not accept support, bug reports, feature requests, or general questions.

## GPG keypair management

Manage the project security key on the server host with `countries --maintenance pgp`. The command requires the operator token or root; importing, deleting, and exporting the private key also require an interactive typed confirmation. `generate` creates the project identity and stores its encrypted private key at `security/pgp.priv.asc.enc` and public key at `security/pgp.pub.asc` under the configured directory. `rotate` signs the replacement public key with the old key, publishes it, re-encrypts retained PGP report bodies in one transaction, and keeps the old encrypted private key for 30 days. The detached rotation signature is stored as `security/pgp.rotation.asc.sig`. `publish` submits the public key to configured `web.security.keyservers`; publication attempts are recorded in `security/keyservers.state` and checked by the built-in scheduler for due retries. Use `export public [path]` to print or copy the public key, `export private PATH` for a rate-limited, audited private-key export, `import FILE` to install a private key, or `delete` to remove the pair. Deleting the key makes retained PGP-encrypted reports unreadable. See [CLI maintenance](cli.md#server-maintenance-commands).

## Data and privacy

The service stores no user accounts or profiles. Contact submissions are sent to the configured report recipients and are not added to the country dataset. Security report bodies use OpenPGP when a valid project public key exists and otherwise use AES-256-GCM; researcher contact details always use AES-256-GCM. Status access uses a token delivered by email, valid once per UTC day. Access logs contain the normalized client IP (resolved only through configured trusted proxies), request ID, method, URL path, response status, and duration. They omit query-string values. Operators control deployment logs and retention. Browser theme and language preferences are stored in client-readable cookies; server-rendered state-changing forms use CSRF protection. `/server/dpo` displays only the DPO/general address supplied in configuration and otherwise directs visitors to the deployment's security page; no contact address is invented. Country data provenance and redistribution terms are documented in [`src/data/README.md`](https://github.com/apimgr/countries/blob/main/src/data/README.md).

## Owner tokens and MCP

There are no accounts or passwords. A primary owner token identifies an anonymous owner and manages linked tokens; linked tokens access the same owner's resources but cannot manage tokens. The operator bearer token grants global management access and is stored in `secrets.yml`. Owner and linked tokens are encrypted at rest and masked in token lists. Keep token values in the `Authorization: Bearer` header; the `/api/` routes ignore browser cookies. The MCP endpoint requires an owner or linked bearer token and never exposes token-management actions as tools. Give each MCP client its own linked token and revoke it when no longer needed.
