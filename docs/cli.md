# CLI reference

The `countries-cli` client makes read-only HTTP requests to a Countries server. In an interactive terminal, running it without a command starts the setup flow and menu. Otherwise, provide a command and configure a server URL.

```text
countries-cli [flags] COMMAND [arguments]
```

## Commands

| Command | Purpose |
|---|---|
| `list` | List country records. |
| `get CODE` | Read one record by alpha-2, alpha-3, or numeric country code. |
| `search QUERY` | Search country names and supported country fields. |
| `nearest LAT LON` | Find the nearest representative coordinate. |
| `nearby LAT LON RADIUS_KM` | Find country centroids within a radius. |
| `stats` | Read dataset statistics. |

## Flags

| Flag | Purpose |
|---|---|
| `--server URL` | Server base URL. |
| `--token TOKEN` | Optional bearer token for servers that require one. The public Countries API does not require authentication. |
| `--config PATH` | Select a config profile or YAML file. |
| `--lang CODE` | Select output language. |
| `--output table\|plain\|json` | Choose output format. |
| `--color auto\|yes\|no` | Choose color behavior. |
| `--debug` | Write request diagnostics to stderr. |
| `--help`, `-h` | Show help. |
| `--version`, `-v` | Show build version. |

## Server and configuration

The server URL is selected from `--server`, `COUNTRIES_SERVER_PRIMARY`, `server.primary` in `cli.yml`, then the compiled official site when defined. The default config directory is the invoking user's OS config directory under `apimgr/countries/`. Config files are written with owner-only permissions.

The client reads `server.token` from its config file unless overridden with `--token`. Tokens are sent only in the `Authorization: Bearer` header; the project's public API does not use them. Output language precedence is `--lang`, `lang` in `cli.yml`, `LANG`, then English. Supported language codes are `en`, `es`, `zh`, `fr`, `ar`, `de`, and `ja`. The HTTP client timeout is 15 seconds, and response bodies larger than 16 MiB are rejected.

See the [installation guide](installation.md) for release assets and the [API reference](api.md) for route and response details.
