# Configuration

The server reads `server.yml` from its configuration directory and creates a default file when absent. The file is restricted to owner read/write access. Use `--config DIR`, `--data DIR`, and `--log DIR` to override paths. The matching environment overrides are `CONFIG_DIR`, `DATA_DIR`, and `LOG_DIR` (the legacy `LOGS_DIR` spelling is also accepted). `DATABASE_DIR` selects the SQLite directory. Branding, SEO, theme, and page content belong in `yaml.d/branding.yml`; privacy settings belong in `yaml.d/privacy.yml`. Secrets are kept in `secrets.yml`.

## Server settings

| YAML key | Default | Purpose |
|---|---|---|
| `server.port` | `8080` | HTTP listener port; `--port` and `PORT` override it. |
| `server.address` | `0.0.0.0` | Listener address; `--address` and `ADDRESS` override it. |
| `server.fqdn` | `localhost` | Configured public host name. |
| `server.mode` | `production` | Application mode when neither `--mode` nor `MODE` is set. |
| `server.database.driver` | `sqlite` | Database driver. SQLite is currently supported. |
| `server.database.url` | `{data_dir}/db/server.db` | SQLite database file path. `DATABASE_DIR` overrides the default directory. |
| `server.tokens.max_linked` | `25` | Maximum active linked tokens per owner. |
| `server.tokens.cookie_max_age` | `1y` | Sliding lifetime of the primary owner token cookie. |
| `server.tokens.idle_purge_days` | `0` | Days before purging an owner with no resources and no token use; zero disables idle purge. |
| `server.update.branch` | `stable` | Update channel (`stable`, `beta`, or `devel`; `daily` is normalized to `devel`). |
| `server.update.auto_install` | `false` | Controls whether scheduled update checks may install an eligible release. The privilege-safe staged server update flow is not yet implemented; when enabled, the server logs this limitation and sends the normal update notice. |
| `server.update.defer_days` | `0` | Scheduled stable/beta update notices ignore releases newer than this many days; devel ignores the setting. Manual update commands are never deferred. |
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

## External security data

The server stores downloaded blocklists and vulnerability data under `data.security_dir`, which defaults to `{data_dir}/security`. Downloads that fail leave the last valid copy in place and do not prevent startup.

| YAML key | Default | Purpose |
|---|---|---|
| `data.security_dir` | `{data_dir}/security` | Root directory for blocklists, NVD, and optional Trivy data. |
| `data.cve.source` | NVD 2.0 API | HTTPS NVD endpoint. |
| `data.cve.filter_by_cpe` | `false` | Kept unfiltered because this project has no verified dependency-to-CPE mapping. |
| `data.trivy.enabled` | `false` | Enable the Trivy OCI database pull as part of `cve_update`. |
| `data.trivy.source` | `ghcr.io/aquasecurity/trivy-db:2` | OCI reference for the Trivy database. |
| `server.security.blocklists.enabled` | `true` | Enables configured extra sources. The built-in FireHOL IP list remains active. |
| `server.security.blocklists.extra_sources[]` | `[]` | Additional HTTPS IP or domain lists with a unique lowercase ID. |
| `server.security.reports.retention_days` | `365` | Days after closure before a security report is removed by `security_cleanup`; open reports are retained. |

Set the optional NVD API key in `secrets.yml` as `data.cve.api_key`. Blocklist updates run daily. NVD and optional Trivy updates run through the daily `cve_update` task; missing data is also fetched on first startup.

## Backup and restore

| YAML key | Default | Purpose |
|---|---|---|
| `server.backup.encryption.enabled` | Derived from secret presence | Indicates whether new backups use AES-256-GCM encryption. |
| `server.backup.retention.max_backups` | `1` | Number of daily full backups retained. |
| `server.backup.retention.keep_weekly` | `0` | Sunday full backups to retain. |
| `server.backup.retention.keep_monthly` | `0` | First-of-month full backups to retain. |
| `server.backup.retention.keep_yearly` | `0` | January 1 full backups to retain. |
| `server.backup.retention.max_total_size` | `10%` | Maximum backup volume usage, as a percentage or byte size; zero disables the cap. |
| `server.restore.max_archive_size` | `1G` | Maximum compressed archive size accepted. |
| `server.restore.max_expanded_size` | `4G` | Maximum expanded archive size. |
| `server.restore.max_files` | `10000` | Maximum files accepted from an archive. |
| `server.restore.max_file_size` | `512M` | Maximum size of one restored file. |
| `server.restore.max_ratio` | `100` | Maximum expanded-to-compressed ratio. |

Set the unattended backup password in `secrets.yml` as `server.backup.encryption_password`. Compliance mode blocks backup creation until this secret is set. The password is not stored in `server.yml`, the database, the archive manifest, or logs. Restore prompts for the password when the selected archive is encrypted.

Scheduled full and incremental backups run daily at 02:00; hourly incrementals are disabled by default. Manual backups support optional `--include-ssl` and `--include-data` flags. The default backup directory is selected by the operating system path resolver. See the [server CLI reference](cli.md#server-maintenance-commands) for commands.
| `server.privacy.data.sold` | `false` | Controls sold/not-sold privacy text and whether the CCPA opt-out control is available. |
| `server.privacy.data.stored_on_server` | configured default | States whether user data is stored on the deployment. |
| `server.privacy.data.sharing[]` | project defaults | Describes conditions, purposes, and data for sharing. |
| `server.privacy.consent.*` | configured defaults | Consent messages, policy link/text, button labels, and banner behavior. |
| `server.privacy.cookies.{essential,preferences,analytics}` | configured defaults | Enables each cookie category and supplies its description. Analytics description includes the sold/not-sold suffix. |
| `server.privacy.retention.*` | configured defaults | Retention period and data export/deletion availability. |
| `server.privacy.third_party.services[]` | `[]` | Third-party services, their purposes, data sent, and privacy-policy URLs. |
| `server.tracking.type` | empty | Optional analytics provider: `google`, `matomo`, `piwik`, `owa`, `fathom`, `plausible`, `umami`, `simple`, or `cloudflare`; empty or `none` disables tracking. |
| `server.tracking.id` | empty | Provider-specific tracking/site identifier. |
| `server.tracking.url` | empty | Self-hosted provider URL where required; optional for self-hosted Fathom and Plausible. |
| `server.metrics.*` | configured defaults | Prometheus, Grafana, and Loki metrics endpoints use separate bearer tokens and are internal operational endpoints. |
| `server.rate_limit.read.requests` | `120` | Read requests per client in the configured window. |
| `server.rate_limit.read.window` | `60` | Read-request window in seconds. |
| `server.rate_limit.write.requests` | `10` | Non-read requests per client in the configured window. |
| `server.rate_limit.write.window` | `60` | Write-request window in seconds. |
| `server.rate_limit.health.requests` | `120` | Health requests per client in the configured window. |
| `server.rate_limit.health.window` | `60` | Health-request window in seconds. |
| `server.rate_limit.strict.requests` | `10` | Sensitive token-operation attempts per client/owner. Limits are always on; there is no `enabled` switch. |
| `server.rate_limit.strict.window` | `900` | Strict-operation window in seconds. |
| `server.rate_limit.auth_failures.requests` | `5` | Failed authentication attempts per IP before lockout. |
| `server.rate_limit.auth_failures.window` | `900` | Failed-authentication window in seconds. |
| `server.rate_limit.global_burst` | `240` | Total requests per client per minute across classes. |
| `server.logging.access_format` | `apache` | Parsed but currently unused; access logs use the project's fixed request log format. |
| `server.logging.level` | `info` | Parsed but currently unused; runtime log level is not configured from this field. |
| `web-ui.theme` | `dark` | Theme used by server-rendered pages. |
| `web-ui.notifications.enabled` | `true` | Parsed but currently unused; no notification consumer uses this flag. |
| `web-security.cors` | `*` | CORS allowed origin. |
| `web.headers.clear_site_data.on_token_revocation` | `true` | Send `Clear-Site-Data` when a linked owner token is revoked. |
| `web.headers.clear_site_data.on_consent_withdrawal` | `true` | Send `Clear-Site-Data` when a visitor withdraws stored preferences or analytics consent. |
| `web.headers.clear_site_data.execution_contexts` | `false` | Also reload browser execution contexts when a token is revoked. |

Rate limiting is always enabled. Anonymous requests are limited per client IP, valid bearer-token requests are limited per token, and failed authentication is counted per IP. Boolean configuration accepts the truthy and falsy values defined by the project parser, including `yes/no`, `enable/disable`, `on/off`, and `1/0`.

Runtime mode precedence is `--mode`, then `MODE`, then `server.mode`, then `production`. Accepted values are `production` (`prod`), `development` (`dev`, `devel`), and `debug`. The selected mode is process-local; `--mode` does not rewrite configuration. Debug endpoint enablement is separate: explicit `--debug` wins over `DEBUG`, which wins over the default enabled by `debug` mode. When enabled, `/debug/pprof/*` and `/debug/vars` are available. Remaining mode-specific logging, request diagnostics, caching, and security-header behavior are incomplete.

### Analytics tracking

Tracking scripts load only when a provider is configured, the visitor has consented to analytics cookies, and the request is not served through Tor or I2P. Provider tags and generated script are omitted otherwise; `/server/tracking.js` returns 404 when tracking is disabled, consent is missing, or the request uses an onion or I2P address. Tracking scripts are served from the same origin where possible, and the configured provider hosts are added to the Content Security Policy. Analytics consent can be withdrawn from the preferences page.

```yaml
server:
  tracking:
    type: plausible
    id: example.com
    url: "" # Optional self-hosted endpoint; empty uses plausible.io
```

Use `server.tracking.url` for Matomo, Piwik, Open Web Analytics, and Umami. It is optional for Fathom and Plausible when self-hosted. Google, Simple Analytics, and Cloudflare use their hosted endpoints and do not use this URL field. IDs are validated for the selected provider. Analytics is included in the privacy page and third-party service details when configured.

## CLI settings

The client optionally reads `cli.yml` under the operating system's user config directory at `apimgr/countries/cli.yml`. It supports `server.primary`, `server.token`, `defaults.output` (`table`, `plain`, or `json`), and `lang`. Config files are restricted to owner access. `--server` and `COUNTRIES_SERVER_PRIMARY` take precedence over the file.

