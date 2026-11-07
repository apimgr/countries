# CLI reference

The `countries-cli` client makes read-only HTTP requests to a Countries server. Running it without a command in a terminal opens an interactive menu. On first use, it asks for a server URL and optional bearer token, verifies the health endpoint, and saves the connection in `cli.yml`.

```text
countries-cli [flags] COMMAND [arguments]
```

## Commands

| Command | Purpose |
|---|---|
| `list` | List country records. |
| `get CODE` | Read one alpha-2 country code. |
| `search QUERY` | Search country names. |
| `nearest LAT LON` | Find the nearest representative coordinate. |
| `nearby LAT LON RADIUS_KM` | Find records within a radius. |
| `stats` | Read dataset statistics. |

## Flags

| Flag | Purpose |
|---|---|
| `--server URL` | Server base URL. |
| `--output table\|plain\|json` | Choose output format. |
| `--color auto\|yes\|no` | Choose color behavior. |
| `--debug` | Write request diagnostics to stderr. |
| `--update [check\|yes\|branch CHANNEL]` | Check for or install verified server-provided releases, or select `stable`, `beta`, or `devel`. |
| `--help`, `-h` | Show help. |
| `--version`, `-v` | Show build version. |

Select the server with `--server`, `COUNTRIES_SERVER_PRIMARY`, or `server.primary` in `cli.yml`, in that order. The optional config file is `cli.yml` under the OS user config directory at `apimgr/countries/`. A configured token is sent as a bearer credential. The client has a 15-second HTTP timeout and rejects response bodies larger than 16 MiB.

The client configuration is created with owner-only permissions. The server URL can be set in advance with `COUNTRIES_SERVER_PRIMARY` or `server.primary`.
