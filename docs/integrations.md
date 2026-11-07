# Integrations and discovery

## Public interfaces

- Versioned read-only REST API under `/api/v1/`.
- Read-only GraphQL at `/api/v1/server/graphql`.
- OpenAPI JSON at `/api/v1/server/swagger`.
- `/.well-known/security.txt` for security reporting policy.
- Progressive web app manifest and service worker at `/manifest.json` and `/sw.js`.

## External providers

No external data, identity, webhook, federation, Tor, or I2P provider is currently enabled. The embedded country lookup path makes no network calls. Optional integrations are not implied by the current public API.
