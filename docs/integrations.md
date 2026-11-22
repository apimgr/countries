# Integrations and discovery

## Public interfaces

- Versioned read-only REST API under `/api/v1/`.
- Read-only GraphQL at `/api/v1/server/graphql`.
- OpenAPI JSON at `/api/v1/server/swagger`.
- Streamable HTTP MCP at `/api/v1/server/mcp` (aliases: `/api/mcp` and `/mcp`); every request requires a bearer token.
- MCP connection details and tool inventory at `/server/help/mcp` and `/api/v1/server/help/mcp`.
- `/.well-known/security.txt` for security reporting policy.
- `/llms.txt` and `/.well-known/llms.txt` for a plain-text API index.
- `/robots.txt` and `/sitemap.xml` for crawler discovery.
- Progressive web app manifest and service worker at `/manifest.json` and `/sw.js`.

## MCP clients

Create a primary owner token in the token preferences page or with `POST /api/v1/server/preferences/tokens/primary`, then create one linked token for each MCP client. Configure the client with the URL returned by `/api/v1/server/help/mcp` and send its token as `Authorization: Bearer ...`. The server accepts protocol version `2026-07-28`; each request includes matching `MCP-Protocol-Version` and `Mcp-Method` headers, and tool calls include `Mcp-Name`. Tokens are credentials: do not put them in URLs, tool results, or logs. Token management is available through the preferences page and its REST mirror, never as MCP tools.

## Optional analytics

The server can send page-view analytics to Google Analytics, Matomo/Piwik, Open Web Analytics, Fathom, Plausible, Umami, Simple Analytics, or Cloudflare Web Analytics when an operator configures `server.tracking` and a visitor consents to analytics cookies. Provider scripts are omitted for onion and I2P requests. See [analytics configuration](configuration.md#analytics-tracking) for provider-specific IDs and URLs. Country lookups remain local to the embedded dataset and make no external data requests.
