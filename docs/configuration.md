# Configuration

The server reads `server.yml` from its configuration directory and creates a default file when absent. The file is restricted to owner read/write access. Use `--config DIR`, `--data DIR`, and `--log DIR` to override paths. The matching environment overrides are `CONFIG_DIR`, `DATA_DIR`, and `LOG_DIR` (the legacy `LOGS_DIR` spelling is also accepted). `DATABASE_DIR` selects the SQLite directory.

## Server settings

| YAML key | Default | Purpose |
|---|---|---|
| `server.port` | `8080` | HTTP listener port; `--port` and `PORT` override it. |
| `server.address` | `0.0.0.0` | Listener address; `--address` and `ADDRESS` override it. |
| `server.fqdn` | `localhost` | Configured public host name. |
| `server.mode` | `production` | Application mode when neither `--mode` nor `MODE` is set. |
| `server.database.driver` | `sqlite` | Database driver. SQLite is currently supported. |
| `server.database.url` | `{data_dir}/db/server.db` | SQLite database file path. `DATABASE_DIR` overrides the default directory. |
| `server.update.branch` | `stable` | Update channel (`stable`, `beta`, or `devel`; `daily` is normalized to `devel`). |
| `server.update.auto_install` | `false` | Parsed but currently unused; no scheduled updater consumes this setting. |
| `server.update.defer_days` | `0` | Parsed but currently unused; scheduled update deferral is not implemented. |
| `server.update.cli_min_version` | empty | Parsed but currently unused; the server does not enforce a minimum companion CLI version. |
| `server.trusted_proxies.additional` | `[]` | Additional trusted proxy IPs, CIDRs, or hostnames. |
| `server.contact.dpo.name` | empty | Optional public name shown on `/server/dpo`. |
| `server.contact.dpo.email` | empty | Optional DPO email shown on `/server/dpo`; falls back to `server.contact.general.email`. |
| `server.contact.general.email` | empty | General public contact email used as the DPO fallback. |
| `server.contact.abuse.email` | empty | Public recipient for abuse and DMCA reports; falls back to general, then admin. |
| `server.contact.admin.email` | empty | Private fallback recipient for report delivery. It is never shown on the report chooser. |
| `server.contact.security.email` | empty | Private recipient for deployment security reports. |
| `server.pages.contact.enabled` | `true` | Enable the report form and its security.txt contact entry. Disabled forms return 404. |
| `server.pages.contact.captcha` | `simple` | `simple`, `recaptcha`, or `hcaptcha`; external providers require keys in `secrets.yml` and retain a simple no-JavaScript fallback. Onion and I2P requests use `simple`. |
| `server.pages.contact.antispam.*` | configured defaults | Fill age, token age, per-IP/email/global limits, markup/link rules, blocked terms/domains, strikes, duplicate window, and review timing. |
| `server.notifications.email.smtp.host` | environment/autodetect | SMTP host; `SMTP_HOST` overrides it. `server.notifications.email.smtp.password` is stored only in `secrets.yml`. |
| `server.notifications.email.smtp.from_email` | `no-reply@{fqdn}` | Server sender address. Submitted email is used only for `Reply-To` on ordinary reports. |

The server generates `server.security.encryption_key` into `secrets.yml` for encrypted security reports. Secret values are not written to `server.yml` or returned by APIs.
| `server.privacy.data.sold` | `false` | Enables the CCPA opt-out control on `/server/preferences`. |
| `server.metrics.enabled` | `false` | Parsed but currently unused; no metrics endpoint or collector is registered. |
| `server.metrics.endpoint` | `/metrics` | Parsed but currently unused. |
| `server.rate_limit.enabled` | `true` | Enable persistent SQLite request limits. |
| `server.rate_limit.read.requests` | `120` | Read requests per client in the configured window. |
| `server.rate_limit.write.requests` | `10` | Non-read requests per client in the configured window. |
| `server.rate_limit.health.requests` | `120` | Health requests per client in the configured window. |
| `server.rate_limit.global_burst` | `240` | Total requests per client per minute across classes. |
| `server.logging.access_format` | `apache` | Parsed but currently unused; access logs use the project's fixed request log format. |
| `server.logging.level` | `info` | Parsed but currently unused; runtime log level is not configured from this field. |
| `web-ui.theme` | `dark` | Theme used by server-rendered pages. |
| `web-ui.notifications.enabled` | `true` | Parsed but currently unused; no notification subsystem consumes it. |
| `web-security.cors` | `*` | CORS allowed origin. |

Boolean configuration accepts the truthy and falsy values defined by the project parser, including `yes/no`, `enable/disable`, `on/off`, and `1/0`.

Runtime mode precedence is `--mode`, then `MODE`, then `server.mode`, then `production`. Accepted values are `production` (`prod`), `development` (`dev`, `devel`), and `debug`. The selected mode is process-local; `--mode` does not rewrite configuration. Debug endpoint enablement is separate: explicit `--debug` wins over `DEBUG`, which wins over the default enabled by `debug` mode. When enabled, `/debug/pprof/*` and `/debug/vars` are available. Remaining mode-specific logging, request diagnostics, caching, and security-header behavior are incomplete.

## CLI settings

The client optionally reads `cli.yml` under the operating system's user config directory at `apimgr/countries/cli.yml`. It supports `server.primary`, `server.token`, `defaults.output` (`table`, `plain`, or `json`), and `lang`. Config files are restricted to owner access. `--server` and `COUNTRIES_SERVER_PRIMARY` take precedence over the file.

