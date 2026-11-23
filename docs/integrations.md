# Integrations and discovery

## Public interfaces

- Versioned read-only REST API under `/api/v1/`.
- Read-only GraphQL at `/api/v1/server/graphql` and OpenAPI JSON at `/api/v1/server/swagger`.
- Streamable HTTP MCP at `/api/v1/server/mcp` (aliases: `/api/mcp` and `/mcp`); every request requires a bearer token.
- MCP connection details and tool inventory at `/server/help/mcp` and `/api/v1/server/help/mcp`.
- `/api/autodiscover` for server, API, and companion CLI discovery.
- `/.well-known/security.txt` for security reporting policy and `/.well-known/pgp-key.asc` for the security-report public key when one has been generated; the key endpoint returns 404 otherwise.
- `/llms.txt` and `/.well-known/llms.txt` for the generated plain-text API index. Both entries are always enabled and resolve their absolute URLs for the current request.
- `/robots.txt` and `/sitemap.xml` for crawler discovery.
- Progressive web app manifest and service worker at `/manifest.json` and `/sw.js`.

## API autodiscovery

`GET /api/autodiscover` is unversioned so a client can learn the current API base path before constructing versioned requests. It requires no authentication and returns a JSON object containing the server name, version and build date; `api_base_path`; `cli_versions`; and `cli_min_version`.

Each `cli_versions` entry is keyed by platform and architecture, such as `linux-amd64`, and contains the served version and SHA-256 checksum. The server discovers regular `countries-cli-*` files beside its working directory in `binaries/`, beside the server executable, or in that executable directory's `binaries/` subdirectory. If no client binaries are installed, `cli_versions` is empty. `cli_min_version` is the configured minimum companion-client version; an empty value means the server does not enforce a minimum.

## Companion CLI downloads and updates

Download a client binary from the deployment with a platform-specific path:

```sh
SERVER_URL="https://your-server.example"
curl --fail --location "$SERVER_URL/cli/binaries/countries-cli-linux-amd64" -o countries-cli
```

The download route streams only the named `countries-cli-{os}-{arch}` binary and returns 404 when that file is unavailable. Downloads are public by default. To restrict them, set this top-level setting in `server.yml`:

```yaml
cli:
  binary_download:
    require_auth: true
```

When enabled, downloads require `Authorization: Bearer <operator-token>`. The server otherwise accepts the request without a token so new users can obtain the client before creating an owner token.

The companion CLI checks autodiscovery on each invocation. When a newer compatible client is available, it prompts before updating in an interactive session. Automatic non-interactive updates require both `update.auto: true` in the user's `cli.yml` and an explicit `--update yes`. The client downloads from the server, checks the binary against the discovered SHA-256, atomically replaces its installed executable, and restarts with the original arguments. If `cli_min_version` is set and the installed client is older, the client stops API requests and directs the user to update.

## AI discovery with llms.txt

The server generates the API index from its route and configuration data and serves it at `/llms.txt` and `/.well-known/llms.txt`. It lists public and authenticated API operations, marks operations that require bearer authentication, omits metrics and debug routes, describes the country API and MCP access, and includes configured API and security contacts. Operators can control endpoint and schema detail and append custom sections through the `web.llms` settings described in the [configuration reference](configuration.md#web-pages-and-discovery). The index is plain text; it does not expose metrics, debug information, or report contents.

## Security reporting

The deployment publishes its RFC 9116 contact details at `/.well-known/security.txt` and on `/server/security`. The overview links to the disclosure policy at `/server/security/policy`, researcher acknowledgments at `/server/security/thanks`, and the public key at `/.well-known/pgp-key.asc` when a key is available. The JSON mirrors are `/api/v1/server/security`, `/api/v1/server/security/policy`, and `/api/v1/server/security/thanks`.

## MCP clients

Create a primary owner token in the token preferences page or with `POST /api/v1/server/preferences/tokens/primary`, then create one linked token for each MCP client. Configure the client with the URL returned by `/api/v1/server/help/mcp` and send its token as `Authorization: Bearer ...`. The server accepts protocol version `2026-07-28`; each request includes matching `MCP-Protocol-Version` and `Mcp-Method` headers, and tool calls include `Mcp-Name`. Tokens are credentials: do not put them in URLs, tool results, or logs. Token management is available through the preferences page and its REST mirror, never as MCP tools.

## Other integrations

The server supports configured SMTP and role-based contact webhooks for report delivery. This project does not enable OAuth/OIDC federation, Android App Links, or Apple Universal Links. Country lookups remain local to the embedded dataset and make no external data requests.

## Optional analytics

The server can send page-view analytics to Google Analytics, Matomo/Piwik, Open Web Analytics, Fathom, Plausible, Umami, Simple Analytics, or Cloudflare Web Analytics when an operator configures `server.tracking` and a visitor consents to analytics cookies. Provider scripts are omitted for onion and I2P requests. See [analytics configuration](configuration.md#analytics-tracking) for provider-specific IDs and URLs.
