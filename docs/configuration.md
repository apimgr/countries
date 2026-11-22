# Configuration

The server reads `server.yml` from its configuration directory and creates a default file when absent. The file is restricted to owner read/write access. Use `--config DIR`, `--data DIR`, and `--log DIR` to override paths. The matching environment overrides are `CONFIG_DIR`, `DATA_DIR`, and `LOG_DIR` (the legacy `LOGS_DIR` spelling is also accepted). `DATABASE_DIR` selects the SQLite directory.

## Server settings

| YAML key | Default | Purpose |
|---|---|---|
| `server.port` | `8080` | HTTP listener port; `--port` and `PORT` override it. |
| `server.address` | `0.0.0.0` | Listener address; `--address` and `ADDRESS` override it. |
| `server.fqdn` | `localhost` | Configured public host name. |
| `server.mode` | `production` | Parsed application mode. Runtime mode selection is not currently wired into the server process. |
| `server.database.driver` | `sqlite` | Database driver. SQLite is currently supported. |
| `server.database.url` | `{data_dir}/db/server.db` | SQLite database file path. `DATABASE_DIR` overrides the default directory. |
| `server.update.branch` | `stable` | Update channel (`stable`, `beta`, or `devel`; `daily` is normalized to `devel`). |
| `server.update.auto_install` | `false` | Parsed but currently unused; no scheduled updater consumes this setting. |
| `server.update.defer_days` | `0` | Parsed but currently unused; scheduled update deferral is not implemented. |
| `server.update.cli_min_version` | empty | Minimum accepted companion CLI version. |
| `server.trusted_proxies.additional` | `[]` | Additional trusted proxy IPs, CIDRs, or hostnames. |
| `server.contact.dpo.name` | empty | Optional public name shown on `/server/dpo`. |
| `server.contact.dpo.email` | empty | Optional DPO email shown on `/server/dpo`; falls back to `server.contact.general.email`. |
| `server.contact.general.email` | empty | General public contact email used as the DPO fallback. |
| `server.privacy.data.sold` | `false` | Enables the CCPA opt-out control on `/server/preferences`. |
| `server.metrics.enabled` | `false` | Parsed but currently unused; no metrics endpoint or collector is registered. |
| `server.metrics.endpoint` | `/metrics` | Parsed but currently unused. |
| `server.rate_limit.enabled` | `true` | Enable persistent SQLite request limits. |
| `server.rate_limit.read.requests` | `120` | Read requests per client in the configured window. |
| `server.rate_limit.write.requests` | `10` | Non-read requests per client in the configured window. |
| `server.rate_limit.health.requests` | `120` | Health requests per client in the configured window. |
| `server.rate_limit.global_burst` | `240` | Total requests per client per minute across classes. |
| `server.logging.access_format` | `apache` | Access log format setting. |
| `server.logging.level` | `info` | Log verbosity setting. |
| `web-ui.theme` | `dark` | Theme used by server-rendered pages. |
| `web-ui.notifications.enabled` | `true` | Parsed but currently unused; no notification subsystem consumes it. |
| `web-security.cors` | `*` | CORS allowed origin. |

Boolean configuration accepts the truthy and falsy values defined by the project parser, including `yes/no`, `enable/disable`, `on/off`, and `1/0`.

Runtime mode precedence is `--mode`, then `MODE`, then `server.mode`, then `production`. Accepted values are `production` (`prod`), `development` (`dev`, `devel`), and `debug`. The selected mode is process-local; `--mode` does not rewrite configuration. Debug endpoint enablement is a separate setting: `--debug` wins over `DEBUG`, while `debug` mode enables it by default. Full mode-specific server behavior and `/debug/*` endpoints remain incomplete.

## CLI settings

The client optionally reads `cli.yml` under the operating system's user config directory at `apimgr/countries/cli.yml`. It supports `server.primary`, `server.token`, `defaults.output` (`table`, `plain`, or `json`), and `lang`. Config files are restricted to owner access. `--server` and `COUNTRIES_SERVER_PRIMARY` take precedence over the file.

