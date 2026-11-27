# Configuration

The server loads `server.yml`, ordered `yaml.d/*.yml` and `yaml.d/*.yaml` area files, and `secrets.yml` into one validated configuration. Area files merge by dotted key; later files replace earlier scalar and list values. `server.yml` holds node-specific settings; the area file table below identifies the canonical home for each other section. Files are restricted to owner access. Use `--config DIR`, `--data DIR`, `--cache DIR`, `--log DIR`, `--backup DIR`, and `--pid FILE` to override paths. The matching environment overrides are `CONFIG_DIR`, `DATA_DIR`, `CACHE_DIR`, `LOG_DIR`, and `BACKUP_DIR`; `DATABASE_DIR` selects the SQLite directory.

## Server settings

| YAML key | Default | Purpose |
|---|---|---|
| `server.port` | First-run native: random available `64000`–`64999`; container: `80` | Listener port, or an HTTP,HTTPS pair such as `80,443`. `PORT` seeds first-run config; `--port` overrides the listener for that invocation. |
| `server.address` | `[::]` | Dual-stack listener address; `--address` and `LISTEN` override it. IPv4-only machines fall back to `0.0.0.0`. |
| `server.fqdn` | Loopback name recorded during first run | Detected name retained for operator reference. Public links use request origin resolution or the `DOMAIN` environment variable; this setting does not supply the public name. |
| `server.mode` | `production` | Application mode when neither `--mode` nor `MODE` is set. |
| `server.debug.pprof` | `true` | Allow pprof endpoints when the per-run debug flag is enabled. |
| `server.debug.log_queries` | `true` | Log SQL queries only while the per-run debug flag is enabled. |
| `server.debug.log_cache` | `true` | Log asset-cache hits and misses only while the per-run debug flag is enabled. |
| `server.debug.log_bodies` | `false` | Capture and redact request and response bodies in debug logs. |
| `server.debug.max_body_log_size` | `10KB` | Maximum captured request or response body size. |
| `server.debug.block_profile_rate` | `1` | Block profiling rate while debug is enabled; zero disables block profiling. |
| `server.debug.mutex_profile_fraction` | `1` | Mutex profiling fraction while debug is enabled; zero disables mutex profiling. |
| `server.debug.runtime_endpoints` | `true` | Allow `/debug/vars`, `/debug/memory`, and `/debug/goroutines` when debug is enabled. |
| `server.database.driver` | `sqlite` | Database driver (`sqlite` or remote `libsql`/Turso). |
| `server.database.url` | `{data_dir}/db/server.db` | SQLite database file path or remote libsql URL. `DATABASE_DIR` overrides the default SQLite directory. |
| `server.tokens.max_linked` | `25` | Maximum active linked tokens per owner. |
| `server.tokens.cookie_max_age` | `1y` | Sliding lifetime of the primary owner token cookie. |
| `server.tokens.idle_purge_days` | `0` | Days before purging an owner with no resources and no token use; zero disables idle purge. |
| `server.update.branch` | `stable` | Update channel (`stable`, `beta`, or `devel`; `daily` is normalized to `devel`). |
| `server.update.auto_install` | `false` | Allows scheduled checks to stage a checksum-verified update when the server started elevated on Unix. The supervisor applies it on restart. User-mode and Windows services receive a manual update command instead. |
| `server.update.defer_days` | `0` | Scheduled stable/beta update notices ignore releases newer than this many days; devel ignores the setting. Manual update commands are never deferred. |
| `server.update.cli_min_version` | empty | Minimum companion CLI version. The server returns this value through autodiscovery; a CLI below it refuses further API requests and prints the update command. |
| `server.trusted_proxies.additional` | `[]` | Additional trusted proxy IPs, CIDRs, or domain names. |
| `server.contact.dpo.name` | empty | Optional public name shown on `/server/dpo`. |
| `server.contact.dpo.email` | empty | Optional DPO email shown on `/server/dpo`; falls back to `server.contact.general.email`. |
| `server.contact.general.email` | empty | General public contact email used as the DPO fallback. |
| `server.contact.abuse.email` | empty | Public recipient for abuse and DMCA reports; falls back to general, then admin. |
| `server.contact.admin.email` | empty | Private fallback recipient for report delivery. It is never shown on the report chooser. |
| `server.contact.security.email` | empty | Private recipient for deployment security reports. |
| `server.contact.{admin,security,abuse,general}.webhooks.{transport}` | empty | Role-specific HTTPS webhook destinations for operational events and report summaries. Supported transports are Telegram, Discord, Slack, Mattermost, Pushover, Gotify, and generic JSON. Empty role transports fall back through the same role chain as email. Each configured destination has a generated signing secret in `secrets.yml`; webhook messages never contain report bodies or sender contact details. Failed deliveries retry at 1m, 5m, 15m, 1h, 6h, and 24h, then are dropped and rate-limited in logs. |
| `server.notifications.database_error.enabled` | `true` | Enable email alerts for sustained database probe failures; the connection check still runs when alerts are disabled. |
| `server.notifications.database_error.failures` | `3` | Consecutive failed probes before the first alert. |
| `server.notifications.database_error.check_interval` | `1m` | Database connection probe interval. |
| `server.notifications.database_error.repeat` | `24h` | Repeat alert interval while a failure continues. |
| `server.pages.contact.enabled` | `true` | Enable the report form and its security.txt contact entry. Disabled forms return 404. |
| `server.pages.contact.captcha` | `simple` | `simple`, `recaptcha`, or `hcaptcha`; external providers retain a simple no-JavaScript fallback. Onion and I2P requests always use `simple`. |
| `server.pages.contact.captcha_site_key` | empty | Public site key rendered for reCAPTCHA or hCaptcha; not needed for `simple`. |
| `server.pages.contact.captcha_secret_key` | empty | Private provider key stored in `secrets.yml`; never returned by the API or written to logs. |
| `server.pages.contact.success_message` | `Thank you for your report.` | Confirmation text shown after report submission. |
| `server.pages.help.content` | built-in help | Markdown string for all languages or language map; maps resolve by request language, then `default`, then built-in localized help. |
| `server.pages.help.mcp` | empty | Project-specific Markdown appended after the generated MCP help sections. |
| `server.pages.contact.antispam.honeypot` | `true` | Enable the hidden website field trap. |
| `server.pages.contact.antispam.min_fill_seconds` | `3` | Minimum form-open time before accepting a submission. |
| `server.pages.contact.antispam.max_age_seconds` | `43200` | Maximum age of a rendered form. |
| `server.pages.contact.antispam.per_ip` | `5` | Accepted submissions per IP and window. |
| `server.pages.contact.antispam.per_email` | `3` | Accepted submissions per sender email and window. |
| `server.pages.contact.antispam.global` | `60` | Accepted submissions across all senders and window. |
| `server.pages.contact.antispam.window` | `1h` | Rate-limit window. |
| `server.pages.contact.antispam.max_links` | `10` | Maximum same-site links allowed in free-text fields. |
| `server.pages.contact.antispam.reject_markup` | `true` | Reject HTML, encoded angle brackets, and BBCode in text fields. |
| `server.pages.contact.antispam.blocked_words` | `[]` | Case-insensitive blocked substrings. |
| `server.pages.contact.antispam.blocked_email_domains` | `[]` | Sender email domains to reject. |
| `server.pages.contact.antispam.strikes` | `3` | Rejections from one IP before a form ban; zero disables strike bans. |
| `server.pages.contact.antispam.ban_hours` | `24` | Duration of a strike-based form ban. |
| `server.pages.contact.antispam.review_min_seconds` | `2` | Minimum time the review step must be shown before confirmation. |
| `server.pages.contact.antispam.review_max_seconds` | `900` | Review token lifetime. |
| `server.pages.contact.antispam.duplicate_window_hours` | `24` | Window for dropping duplicate messages from the same email. |
| `server.notifications.email.smtp.host` | autodetected | SMTP host; empty enables first-run autodetection. `SMTP_HOST` overrides it. |
| `server.notifications.email.smtp.port` | `587` | SMTP port; `SMTP_PORT` overrides it. |
| `server.notifications.email.smtp.username` | empty | SMTP authentication username; `SMTP_USERNAME` overrides it. |
| `server.notifications.email.smtp.password` | empty | SMTP authentication password; `SMTP_PASSWORD` overrides it and the value is stored only in `secrets.yml`. |
| `server.notifications.email.smtp.tls` | `auto` | SMTP TLS mode: `auto`, `starttls`, `tls`, or `none`; `SMTP_TLS` overrides it. |
| `server.notifications.email.from.name` | app title | Sender display name; `SMTP_FROM_NAME` overrides it. |
| `server.notifications.email.from.email` | `no-reply@{fqdn}` | Sender address; `SMTP_FROM_EMAIL` overrides it. |

### Node, TLS, cache, and request settings

| YAML key | Default | Purpose |
|---|---|---|
| `server.api_version` | `v1` | API version path segment. |
| `server.baseurl` | `/` | URL path prefix for all routes; trusted proxy prefix headers take precedence. |
| `server.daemonize` | `false` | Detach from the terminal at startup. |
| `server.pidfile` | `true` | Write the server PID file; `--pid FILE` selects its path. Native system mode defaults to `/var/run/apimgr/countries/countries.pid`, user mode to `{data_dir}/countries.pid`, and application containers and Windows skip it. Incus/LXC system containers running systemd or OpenRC use native system mode. Stale or corrupt files are removed at startup, and the PID file is removed at shutdown. |
| `server.ssl.enabled` | `false` | Enable TLS for the configured listener. |
| `server.ssl.cert` | empty | Optional manual certificate path; otherwise use certificate auto-detection. |
| `server.ssl.key` | empty | Optional manual private-key path; otherwise use certificate auto-detection. |
| `server.ssl.min_version` | `TLS1.2` | Minimum TLS version (`TLS1.2` or `TLS1.3`). |
| `server.ssl.letsencrypt.enabled` | `false` | Enable automatic certificate management. |
| `server.ssl.letsencrypt.email` | `admin@{fqdn}` | ACME account email. |
| `server.ssl.letsencrypt.challenge` | `http-01` | ACME challenge: `http-01`, `tls-alpn-01`, or `dns-01`. |
| `server.ssl.letsencrypt.staging` | `false` | Use the ACME staging service. |
| `server.ssl.letsencrypt.dns_provider` | empty | lego DNS provider name when using DNS-01. |
| `server.ssl.letsencrypt.dns_credentials` | `{}` | Flat map of lego provider environment-variable names to secret values; stored encrypted in `secrets.yml`. |
| `server.cache.type` | `memory` | Cache backend: `none`, `memory`, `valkey`, or `redis`. |
| `server.cache.url` | empty | Full Valkey/Redis connection URL; takes precedence over separate connection fields. |
| `server.cache.host` | Loopback address | Cache host when no URL is configured. |
| `server.cache.port` | `6379` | Cache port when no URL is configured. |
| `server.cache.username` | empty | Optional Redis ACL username. |
| `server.cache.password` | empty | Cache password; secret values belong in `secrets.yml`. |
| `server.cache.db` | `0` | Cache database number. |
| `server.cache.tls` | `false` | Request TLS for field-based cache connections. |
| `server.cache.tls_skip_verify` | `true` | Skip certificate verification when TLS is enabled. |
| `server.cache.tls_ca` | empty | Private CA filename under `{config_dir}/ssl/`. |
| `server.cache.tls_cert` | empty | Client certificate filename under `{config_dir}/ssl/`. |
| `server.cache.tls_key` | empty | Client private-key filename under `{config_dir}/ssl/`. |
| `server.cache.pool_size` | `10` | Connection-pool size. |
| `server.cache.min_idle` | `2` | Minimum idle cache connections. |
| `server.cache.timeout` | `5s` | Cache operation timeout. |
| `server.cache.prefix` | `{project_name}:` | Prefix for cache keys. |
| `server.cache.ttl` | `1h` | Default cache entry lifetime. |
| `server.limits.max_body_size` | `10MB` | Maximum HTTP request body size. |
| `server.limits.read_timeout` | `30s` | HTTP read timeout. |
| `server.limits.write_timeout` | `30s` | HTTP write timeout. |
| `server.limits.idle_timeout` | `120s` | HTTP idle-connection timeout. |
| `server.compression.enabled` | `true` | Enable compressed responses. |
| `server.compression.level` | `5` | Compression level, from 1 through 9. |
| `server.compression.types` | HTML, CSS, JavaScript, JSON, XML | MIME types eligible for compression. |
| `server.trusted_proxies.additional` | `[]` | Additional trusted proxy IPs, CIDRs, or DNS names. Private and loopback ranges are trusted by default. |
| `server.trusted_proxies.client_ip_header` | `auto` | Select automatic or pinned client-IP header detection. |
| `server.healthz.root.enabled` | `false` | Add `/healthz` as an alias to `/server/healthz`. |
| `server.i18n.default_language` | `en` | Language used where no request language applies. |
| `server.i18n.available_languages` | All embedded locales (`en`, `es`, `fr`, `de`, `it`, `ar`, `zh`, `ja`) | Languages available for server-rendered pages and notifications; operators may narrow the list but cannot remove any of the six required minimum languages. |

The `server.yml` node-specific keys include `server.debug.*` diagnostic settings, but the runtime debug flag itself has no configuration key. Debug diagnostics require `--debug`, a truthy `DEBUG` value, or `MODE=debug` with `DEBUG` unset; the nested settings only control which diagnostics run after that explicit opt-in.

## Overlay network settings

Tor settings live in `yaml.d/tor.yml`. The managed Tor hidden service starts automatically when a Tor binary is found; there is no enable switch.

| YAML key | Default | Purpose |
|---|---|---|
| `server.tor.binary` | auto-detected | Tor executable path. |
| `server.tor.expose` | `false` | Advertise the onion address in API autodiscovery. |
| `server.tor.contact_email` | empty | Email shown only in Tor responses; unset means no email is shown over Tor. |
| `server.tor.use_network` | `false` | Use the managed Tor network for app-owned outbound connections. |
| `server.tor.max_circuits` | `32` | Maximum concurrent circuits being built. |
| `server.tor.startup_timeout` | `60` | Seconds to wait for the control-port file. |
| `server.tor.circuit_timeout` | `60` | Circuit build timeout in seconds. |
| `server.tor.bootstrap_timeout` | `180` | Seconds to wait for Tor network bootstrap. |
| `server.tor.safe_logging` | `true` | Scrub sensitive information from Tor logs. |
| `server.tor.max_streams_per_circuit` | `100` | Maximum concurrent streams per circuit. |
| `server.tor.close_circuit_on_stream_limit` | `true` | Close a circuit when its stream limit is exceeded. |
| `server.tor.bandwidth_rate` | `1 MB` | Maximum bandwidth rate. |
| `server.tor.bandwidth_burst` | `2 MB` | Maximum bandwidth burst. |
| `server.tor.max_monthly_bandwidth` | `100 GB` | Monthly bandwidth budget and warning thresholds. |
| `server.tor.num_intro_points` | `3` | Number of hidden-service introduction points. |
| `server.tor.virtual_port` | `80` | Virtual port exposed by the hidden service. |
| `server.i2p.enabled` | `true` in containers; `false` elsewhere | Opt in to the I2P eepsite; the setting is also controllable with `I2P_ENABLED` or `--i2p`. |
| `server.i2p.expose` | `false` | Advertise the I2P address in API autodiscovery. |
| `server.i2p.binary` | auto-detected | Optional `i2pd` executable; if absent, use the configured external SAM bridge. |
| `server.i2p.sam_address` | `127.0.0.1:7656` | External I2P SAMv3 bridge address. |
| `server.i2p.virtual_port` | `80` | Virtual port exposed by the eepsite. |
| `server.i2p.inbound_length` | `3` | Number of inbound tunnel hops (0–7). |
| `server.i2p.outbound_length` | `3` | Number of outbound tunnel hops (0–7). |
| `server.i2p.inbound_quantity` | `5` | Number of parallel inbound tunnels (1–16). |
| `server.i2p.outbound_quantity` | `5` | Number of parallel outbound tunnels (1–16). |
| `server.i2p.signature_type` | `7` | SAM destination signature type (EdDSA-SHA512-Ed25519). |
| `server.i2p.bootstrap_timeout` | `300` | Seconds to wait for destination and tunnels to become ready. |

Tor data/configuration and I2P config/data directories are derived from the standard config/data directories; they are not individually configurable.

### Configuration file layout

| File | Configuration sections |
|---|---|
| `server.yml` | Node identity/listener settings, database, cache, SSL, and trusted proxies. |
| `yaml.d/branding.yml` | `server.branding`, `server.seo`, `web.ui.theme`, and `web.ui.accent`. |
| `yaml.d/web.yml` | `web.*` except `web.ui`, plus `server.cors`, `server.csrf`, `server.url_detection`, `server.compression`, `server.healthz`, and `server.i18n`. |
| `yaml.d/pages.yml` | `server.pages`. |
| `yaml.d/contact.yml` | `server.contact`. |
| `yaml.d/privacy.yml` | `server.privacy`, `server.compliance`, and `server.tracking`. |
| `yaml.d/security.yml` | `server.security`, `server.rate_limit`, `server.geoip`, `server.limits`, `server.tokens`, and `data.*`. |
| `yaml.d/logs.yml` | `server.logs`. |
| `yaml.d/metrics.yml` | `server.metrics`. |
| `yaml.d/notifications.yml` | `server.notifications`. |
| `yaml.d/tor.yml` | `server.tor` and top-level `tor`. |
| `yaml.d/i2p.yml` | `server.i2p`. |
| `yaml.d/maintenance.yml` | `server.maintenance`, `server.scheduler`, `server.update`, `server.auto_update`, `server.backup`, and `server.restore`. |
| `yaml.d/app.yml` | Other project-defined sections. |
| `secrets.yml` | Credentials and generated secrets only. |

Both a legacy `server.yml` containing area settings and the drop-in layout are accepted; migration moves the area settings automatically. Drop-ins merge in filename byte order after `server.yml`; `secrets.yml` merges last. Maps merge recursively, while a later scalar or list replaces the earlier value. Configuration files are mode `0600`, and `yaml.d/` is mode `0700`.

## Web pages and discovery

### Help and contact pages

The help page uses built-in, localized Countries guidance when `server.pages.help.content` is empty or a language map has no exact or `default` entry. Set it to a Markdown string to replace that content in every language, or provide a language-to-Markdown map. Map lookup uses the resolved request language, then `default`, then built-in guidance; it never selects an arbitrary map entry. The API returns the selected Markdown source. `server.pages.help.mcp` adds project-specific Markdown after the generated MCP connection and tool documentation. Both settings belong in `yaml.d/pages.yml`.

The contact form is enabled by default. It is reached from the security-reporting page and is not linked from the site's general navigation. `server.pages.contact.enabled: false` disables both the form and its form entry in `security.txt`. Contact page settings belong in `yaml.d/pages.yml`:

| YAML key | Default | Purpose |
|---|---|---|
| `server.pages.contact.enabled` | `true` | Enable the report form. Disabled form GET and POST requests return 404. |
| `server.pages.contact.captcha` | `simple` | CAPTCHA provider: `simple`, `recaptcha`, or `hcaptcha`. Requests over Tor and I2P always use the simple challenge. |
| `server.pages.contact.captcha_site_key` | empty | Public site key for reCAPTCHA or hCaptcha. |
| `server.pages.contact.captcha_secret_key` | empty | Private provider key stored only in `secrets.yml`. |
| `server.pages.contact.success_message` | `Thank you for your report.` | Confirmation shown after a report is accepted. |
| `server.pages.help.content` | built-in help | A Markdown string shared by all languages or a language-to-Markdown map for `/server/help`; maps resolve by request language, then `default`, then built-in localized guidance. |
| `server.pages.help.mcp` | empty | Optional project-specific Markdown appended to `/server/help/mcp`. |

The simple CAPTCHA works without JavaScript and does not contact a third party. reCAPTCHA and hCaptcha require both site and secret keys; the secret is never exposed in responses or logs. Those providers retain a simple no-JavaScript fallback, and a provider verification failure rejects the submission.

### Announcements

Announcements are configured under `web.announcements` in `yaml.d/web.yml`. Active notices render as server-side banners in configuration order. Visitors can dismiss a dismissible notice without JavaScript; dismissal is kept in the `dismissed_announcements` cookie and is keyed by the announcement ID.

```yaml
web:
  announcements:
    enabled: true
    messages:
      - id: maintenance-2026-11
        type: warning
        title: Scheduled maintenance
        message: Service will be unavailable during the maintenance window.
        start: "2026-11-25T01:00:00Z"
        end: "2026-11-25T02:00:00Z"
        dismissible: true
```

Each message requires a unique `id`, non-empty `title` and `message`, and uses `type` `info`, `warning`, `error`, or `success` (`info` by default). Optional `start` and `end` values are RFC 3339 timestamps; when both are set, `end` must be later than `start`. `enabled` defaults to `true`, and an empty `messages` list shows no banner.

### AI discovery (`llms.txt`)

The generated plain-text AI discovery index is always served at `/llms.txt` and `/.well-known/llms.txt`; there is no enable/disable switch. It is generated for each request so absolute URLs match the request origin. Configure these settings in `yaml.d/web.yml`:

| YAML key | Default | Purpose |
|---|---|---|
| `web.llms.include_endpoints` | `true` | Include endpoint entries discovered from API route registrations. Public and authenticated API operations are included; metrics and debug routes are omitted. |
| `web.llms.include_schemas` | `false` | Include operation parameters, request and response schemas, and the API schema definitions. This makes the text output larger. |
| `web.llms.custom_sections` | `[]` | Additional Markdown sections appended to the generated capabilities section. |
| `web.api_contact` | empty | Public contact text for API questions. |
| `web.security.contact` | empty | Public security contact text for the generated index. |

The generated index uses the configured branding title and description, lists the REST and MCP base URLs and bearer-auth requirements, and describes the Countries dataset and lookup capabilities. Do not put private recipient addresses or secrets in public contact fields.

```yaml
web:
  api_contact: "API support: https://github.com/apimgr/countries/issues"
  security:
    contact: "Security reports: https://github.com/apimgr/countries/security/advisories/new"
  llms:
    include_endpoints: true
    include_schemas: false
    custom_sections: []
```

### Branding and SEO settings

| YAML key | Default | Purpose |
|---|---|---|
| `server.branding.title` | `{project_name}` | Display name. |
| `server.branding.tagline` | empty | Short slogan. |
| `server.branding.description` | empty | Longer description for SEO and about content. |
| `server.branding.favicon` | embedded image | HTTPS URL, `data:` URI, or asset filename. |
| `server.branding.logo` | embedded image | HTTPS URL, `data:` URI, or asset filename. |
| `server.branding.content.header` | absent | Optional HTML appended at the end of the public header. |
| `server.branding.content.about` | built-in content | Optional content appended to the About page. |
| `server.branding.content.privacy` | built-in content | Optional content appended to the built-in privacy sections. |
| `server.branding.content.footer` | built-in block | Optional HTML replacing the custom branding block above the application footer. |
| `server.branding.content.terms` | built-in terms | Optional content replacing the built-in Terms of Service. |
| `server.seo.keywords` | `[]` | Project keywords. |
| `server.seo.author` | empty | Author or organization name. |
| `server.seo.og_image` | empty | Open Graph image URL. |
| `server.seo.twitter_handle` | empty | Twitter creator handle. |
| `server.seo.title_separator` | ` - ` | Separator between title and tagline (maximum 8 characters). |
| `server.seo.meta_tags` | `{}` | Additional meta tags, limited to 20 entries. |
| `server.seo.canonical` | request origin | Absolute HTTPS canonical origin. |
| `server.seo.noindex` | `false` | Mark all pages `noindex,nofollow`. |
| `server.seo.opengraph.enabled` | `true` | Enable Open Graph tags. |
| `server.seo.opengraph.type` | `website` | Open Graph type. |
| `server.seo.opengraph.site_name` | branding title | Open Graph site name. |
| `server.seo.twitter.enabled` | `true` | Enable Twitter card tags. |
| `server.seo.twitter.card` | `summary_large_image` | Twitter card type. |
| `server.seo.twitter.creator` | empty | Content creator handle. |
| `server.seo.verification.google` | empty | Google Search Console verification value. |
| `server.seo.verification.bing` | empty | Bing Webmaster verification value. |
| `server.seo.verification.yandex` | empty | Yandex Webmaster verification value. |
| `server.seo.verification.baidu` | empty | Baidu Webmaster verification value. |
| `server.seo.verification.pinterest` | empty | Pinterest verification value. |
| `server.seo.verification.facebook` | empty | Facebook domain verification value. |
| `server.seo.verification.custom` | `{}` | Additional verification meta tags. |

Branding content entries accept a `format` (`md` by default, `html`, or `txt`) and a `body`; `header` and `footer` are HTML-only. The body may be a single string or a map by language with an optional `default` entry. Every body is sanitized before rendering; data URLs are kept only for approved raster image types on images, and SVG/XML previews and data links are removed.

### Web security and browser settings

| YAML key | Default | Purpose |
|---|---|---|
| `server.cors.allowed_origins` | `[*]` | Allowed CORS origins; an empty string entry disables CORS. |
| `server.cors.allow_credentials` | `true` | Allow credentials only for explicitly matched origins; never with `*`. |
| `server.cors.max_age` | `86400` | CORS preflight cache age in seconds. |
| `server.csrf.enabled` | `true` | Enable frontend CSRF protection; only disable for deployments without browser forms. |
| `server.csrf.token_length` | `32` | CSRF token length in bytes. |
| `server.csrf.cookie_name` | `csrf_token` | CSRF cookie name. |
| `server.csrf.header_name` | `X-CSRF-Token` | CSRF request header name. |
| `server.csrf.secure` | `auto` | Set the cookie Secure flag based on HTTPS detection, or force true/false. |
| `server.csrf.exempt_paths` | `[]` | Frontend-only paths exempt from CSRF validation; glob patterns are supported. |
| `web.hsts.enabled` | `true` | Emit HSTS; disable only for HTTP-only deployments. |
| `web.hsts.max_age_seconds` | `63072000` | HSTS max age in seconds. |
| `web.hsts.include_subdomains` | `true` | Include subdomains in HSTS policy. |
| `web.hsts.preload` | `true` | Include the preload directive. |
| `web.csp.enabled` | `true` | Enable Content Security Policy. |
| `web.csp.mode` | `enforce` | CSP mode: `enforce` or `report-only`. |
| `web.csp.script_src_extra`, `style_src_extra`, `img_src_extra`, `font_src_extra`, `connect_src_extra`, `frame_src_extra`, `form_action_extra` | empty | Append allowed sources to the corresponding policy directives. |
| `web.csp.script_src_override` | empty | Replace the default script source directive. Other directive overrides follow the same naming pattern. |
| `web.csp.reports_enabled` | `true` | Send CSP violation reports to the server reports endpoint. |
| `web.reports.rate_limit_per_minute` | `60` | Maximum public report submissions per IP per minute. |
| `web.reports.rate_limit_per_ip_burst` | `10` | Short burst allowance for public reports. |
| `web.headers.coop` | `unsafe-none` | Cross-Origin-Opener-Policy value. |
| `web.headers.coep` | `unsafe-none` | Cross-Origin-Embedder-Policy value. |
| `web.headers.corp` | `cross-origin` | Cross-Origin-Resource-Policy value. |
| `web.headers.origin_agent_cluster` | `true` | Emit `Origin-Agent-Cluster: ?1`. |
| `web.headers.cross_domain_policies` | `none` | X-Permitted-Cross-Domain-Policies value. |
| `web.headers.dns_prefetch_control` | empty | Omit the header when empty; `off` disables DNS prefetching. |
| `web.headers.honor_sec_gpc` | `true` | Honor `Sec-GPC: 1` as an opt-out signal. |
| `web.headers.honor_dnt` | `false` | Honor the legacy Do Not Track signal. |
| `web.headers.sec_fetch_validation` | `true` | Reject cross-site state-changing requests to frontend routes. |
| `web.headers.server_timing_in_debug_only` | `true` | Restrict Server-Timing to debug mode. |
| `web.headers.clear_site_data.on_token_revocation` | `true` | Clear site data after linked-token revocation. |
| `web.headers.clear_site_data.on_consent_withdrawal` | `true` | Clear site data after consent withdrawal. |
| `web.headers.clear_site_data.execution_contexts` | `false` | Also reload browser execution contexts on token revocation. |
| `web.headers.nel.enabled` | `true` | Enable Network Error Logging. |
| `web.headers.nel.max_age_seconds` | `2592000` | NEL policy lifetime (30 days). |
| `web.headers.nel.include_subdomains` | `true` | Apply NEL to subdomains. |
| `web.headers.nel.sample_rate` | `1.0` | Failure sampling rate from 0 to 1. |
| `web.robots.allow` | project defaults | Public paths allowed for crawlers and sitemap inclusion. |
| `web.robots.deny` | project defaults | Paths disallowed for crawlers and omitted from the sitemap. |
| `web.security.policy` | built-in policy | Markdown security disclosure policy. |
| `web.security.report_url` | GitHub private reporting URL | Primary security reporting destination. |
| `web.security.keyservers` | `[]` | OpenPGP keyserver addresses for key publication. |
| `web.security.publish_pgp_key` | `true` | Publish the configured PGP public key in security.txt. |

`server.cors`, `server.csrf`, and other `server.*` web controls belong in `yaml.d/web.yml`; the `web.*` settings also belong there, except `web.ui.*`, which belongs in `yaml.d/branding.yml`.

The server generates `server.security.encryption_key` into `secrets.yml` for researcher contact details and as the AES-256-GCM fallback for security report bodies when no valid OpenPGP public key exists at `config/security/pgp.pub.asc`. When that key exists, report bodies and maintainer notifications use OpenPGP. Secret values are not written to `server.yml` or returned by APIs.

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
| `server.geoip.enabled` | `true` | Enable GeoIP lookups; missing data fails open and does not block requests. |
| `server.geoip.deny_countries` | `[]` | ISO 3166-1 alpha-2 country codes to block. |
| `server.geoip.allow_countries` | `[]` | ISO 3166-1 alpha-2 country codes to allow exclusively; takes precedence if both lists are set. |
| `server.geoip.presets` | `{}` | Named operator-authored country lists for reuse. |

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

## Scheduler settings

The built-in scheduler is always running and has no global enable/disable setting. Each task below is configured under `server.scheduler.tasks`; the scheduler timezone and catch-up window are configured under `server.scheduler`.

| YAML key | Default | Purpose |
|---|---|---|
| `server.scheduler.timezone` | `America/New_York` | IANA timezone for scheduled tasks. |
| `server.scheduler.catch_up_window` | `1h` | Run missed tasks on startup when their due time is within this window. |
| `server.scheduler.tasks.ssl_renewal.schedule` | `0 3 * * *` | Daily certificate renewal check. |
| `server.scheduler.tasks.ssl_renewal.enabled` | `true` | Enable certificate renewal checks. |
| `server.scheduler.tasks.geoip_update.schedule` | `0 3 * * 0` | Weekly GeoIP database update. |
| `server.scheduler.tasks.geoip_update.enabled` | `true` | Enable GeoIP updates; this task may be disabled. |
| `server.scheduler.tasks.blocklist_update.schedule` | `0 4 * * *` | Daily blocklist update. |
| `server.scheduler.tasks.blocklist_update.enabled` | `true` | Enable blocklist updates; this task may be disabled. |
| `server.scheduler.tasks.blocklist_update.retry_on_fail` | `true` | Retry a failed update. |
| `server.scheduler.tasks.blocklist_update.retry_delay` | `1h` | Delay before retrying a failed update. |
| `server.scheduler.tasks.security_cleanup.schedule` | `30 3 * * *` | Daily security-report retention and retired PGP key cleanup. |
| `server.scheduler.tasks.security_cleanup.enabled` | `true` | Security cleanup is required and cannot be disabled. |
| `server.scheduler.tasks.cve_update.schedule` | `0 5 * * *` | Daily NVD and optional Trivy database update. |
| `server.scheduler.tasks.cve_update.enabled` | `true` | Enable vulnerability database updates; this task may be disabled. |
| `server.scheduler.tasks.cve_update.retry_on_fail` | `true` | Retry a failed vulnerability database update. |
| `server.scheduler.tasks.cve_update.retry_delay` | `1h` | Delay before retrying a failed update. |
| `server.scheduler.tasks.update_check.schedule` | `0 6 * * *` | Daily update check; notify-only unless auto-install is enabled. |
| `server.scheduler.tasks.update_check.enabled` | `true` | Enable scheduled update checks; this task may be disabled. |
| `server.scheduler.tasks.token_cleanup.schedule` | `@every 15m` | Remove expired captcha challenges and apply owner idle-purge policy. |
| `server.scheduler.tasks.token_cleanup.enabled` | `true` | Token cleanup is required and cannot be disabled. |
| `server.scheduler.tasks.log_rotation.schedule` | `0 0 * * *` | Daily log rotation. |
| `server.scheduler.tasks.log_rotation.enabled` | `true` | Log rotation is required and cannot be disabled. |
| `server.scheduler.tasks.backup_daily.schedule` | `0 2 * * *` | Daily full and incremental backup. |
| `server.scheduler.tasks.backup_daily.enabled` | `true` | Enable daily backup; this task may be disabled. |
| `server.scheduler.tasks.backup_hourly.schedule` | `@hourly` | Hourly incremental backup schedule. |
| `server.scheduler.tasks.backup_hourly.enabled` | `false` | Hourly incrementals are disabled by default; this task may be enabled or disabled. |
| `server.scheduler.tasks.disk_check.schedule` | `@every 5m` | Check available disk space and report low space. |
| `server.scheduler.tasks.disk_check.enabled` | `true` | Enable disk checks; this task may be disabled. |
| `server.scheduler.tasks.database_check.schedule` | `@every 1m` | Probe database connectivity. |
| `server.scheduler.tasks.database_check.enabled` | `true` | Required database connection monitoring task. |
| `server.scheduler.tasks.healthcheck_self.schedule` | `@every 5m` | Run the server's self-health check. |
| `server.scheduler.tasks.healthcheck_self.enabled` | `true` | Self-health checks are required and cannot be disabled. |
| `server.scheduler.tasks.tor_health.schedule` | `@every 10m` | Check Tor connectivity when Tor support is active. |
| `server.scheduler.tasks.tor_health.enabled` | `true` | Registered only when Tor support is active; otherwise it does not run. |
| `server.scheduler.tasks.i2p_health.schedule` | `@every 10m` | Check the I2P provider when I2P is enabled. |
| `server.scheduler.tasks.i2p_health.enabled` | `true` | Registered only when I2P is enabled; otherwise it does not run. |

Each task accepts `enabled` and `schedule`; `retry_on_fail` and `retry_delay` apply to tasks that support retries. The scheduler forces required tasks to remain enabled. GeoIP, blocklist, CVE, update-check, daily/hourly backup, and disk-check tasks can be disabled. Tor and I2P checks are registered only when their respective features are active. Backup retention is configured under `server.backup.retention.*`, documented in [Backup and restore](#backup-and-restore).

## Privacy, tracking, and web settings

| YAML key | Default | Purpose |
|---|---|---|
| `server.privacy.data.sold` | `false` | Controls sold/not-sold privacy text and availability of the CCPA opt-out control. |
| `server.privacy.data.stored_on_server` | configured default | States whether user data is stored on the deployment. |
| `server.privacy.data.sharing[]` | project defaults | Conditions, purposes, and data categories for sharing. |
| `server.privacy.consent.message` | configured default | Consent banner message when data is not sold. |
| `server.privacy.consent.message_if_sold` | configured default | Consent banner message when data is sold. |
| `server.privacy.consent.policy.url` | configured default | Privacy-policy link URL. |
| `server.privacy.consent.policy.text` | configured default | Privacy-policy link label. |
| `server.privacy.consent.buttons.decline` | configured default | Decline button label. |
| `server.privacy.consent.buttons.accept` | configured default | Accept button label. |
| `server.privacy.consent.preferences_text` | configured default | Preferences control label. |
| `server.privacy.cookies.essential` | configured default | Essential-cookie description and enabled state. |
| `server.privacy.cookies.preferences` | configured default | Preferences-cookie description and enabled state. |
| `server.privacy.cookies.analytics` | configured default | Analytics-cookie description and enabled state. |
| `server.privacy.retention.period` | configured default | Data-retention period description. |
| `server.privacy.retention.export_available` | configured default | Whether data export is available. |
| `server.privacy.retention.deletion_available` | configured default | Whether data deletion is available. |
| `server.privacy.third_party.services[]` | `[]` | Third-party service entries with `name`, `purpose`, `data_sent`, and `policy_url`. |
| `server.tracking.type` | empty | Optional analytics provider: `google`, `matomo`, `piwik`, `owa`, `fathom`, `plausible`, `umami`, `simple`, or `cloudflare`; empty or `none` disables tracking. |
| `server.tracking.id` | empty | Provider-specific tracking/site identifier. |
| `server.tracking.url` | empty | Self-managed provider URL where required; optional for self-managed Fathom and Plausible. |
| `server.metrics.enabled` | `false` | Enable metrics endpoints. |
| `server.metrics.root.enabled` | `true` | Mount root `/metrics[/{service}]` aliases. |
| `server.metrics.auth.allow_unauthenticated` | `false` | Skip metrics bearer-token checks; use only behind a firewall. |
| `server.metrics.auth.tokens.prometheus` | generated secret | Prometheus bearer token. |
| `server.metrics.auth.tokens.grafana` | generated secret | Grafana dashboard bearer token. |
| `server.metrics.auth.tokens.loki` | generated secret | Loki log-stream bearer token. |
| `server.metrics.include_system` | `true` | Include CPU, memory, and disk metrics. |
| `server.metrics.include_runtime` | `true` | Include Go runtime statistics. |
| `server.metrics.loki.max_entries` | `1000` | Maximum recent log entries served. |
| `server.metrics.loki.max_age` | `1h` | Maximum age of log entries served. |
| `server.logs.level` | empty (mode-derived) | Global log level: `debug`, `info`, `warn`, or `error`. |
| `server.logs.defaults.custom` | empty | Custom format string used by log kinds configured with `format: custom`. |
| `server.logs.defaults.rotate` | `weekly,50MB` | Default rotation policy inherited by each log kind. |
| `server.logs.defaults.keep` | `none` | Default retention policy; `none`, a count, or a duration-based policy. |
| `server.logs.access.format` | `apache` | Access log format: `apache`, `nginx`, `json`, or `custom`. |
| `server.logs.access.filename` | `access.log` | Access log file name within the log directory. |
| `server.logs.access.rotate` | `monthly` | Access log rotation policy. |
| `server.logs.access.keep` | `none` | Number or age policy for rotated access logs. |
| `server.logs.server.format` | `text` | Server log format: `text` or `json`. |
| `server.logs.server.filename` | `server.log` | Application event log file name within the log directory. |
| `server.logs.server.rotate` | inherited | Server log rotation policy; inherits `server.logs.defaults.rotate`. |
| `server.logs.server.keep` | inherited | Server log retention policy; inherits `server.logs.defaults.keep`. |
| `server.logs.error.format` | `text` | Error log format: `text` or `json`. |
| `server.logs.error.filename` | `error.log` | Error log file name within the log directory. |
| `server.logs.error.rotate` | inherited | Error log rotation policy; inherits `server.logs.defaults.rotate`. |
| `server.logs.error.keep` | inherited | Error log retention policy; inherits `server.logs.defaults.keep`. |
| `server.logs.audit.enabled` | `true` | Enable the append-only audit log. |
| `server.logs.audit.filename` | `audit.log` | Audit file name within the log directory. |
| `server.logs.audit.format` | `json` | Audit log format; JSON is required. |
| `server.logs.audit.rotate` | `daily` | Audit log rotation policy. |
| `server.logs.audit.keep` | `none` | Retention policy for rotated audit logs. |
| `server.logs.audit.compress` | `false` | Compress retained audit logs. |
| `server.logs.audit.group` | empty | Existing Unix group allowed to read audit files in system mode; the service never creates groups, unknown names warn once and are ignored, and user mode always ignores this setting. |
| `server.logs.audit.token_usage` | `false` | Include individual token usage events in the audit log. |
| `server.logs.audit.events.configuration` | `true` | Include configuration audit events. |
| `server.logs.audit.events.security` | `true` | Include security audit events. |
| `server.logs.audit.events.backup` | `true` | Include backup and restore audit events. |
| `server.logs.audit.events.server` | `true` | Include server lifecycle and maintenance audit events. |
| `server.logs.audit.include_user_agent` | `true` | Include a sanitized user agent in network audit events. |
| `server.logs.security.format` | `fail2ban` | Security log format: `fail2ban`, `syslog`, `cef`, `json`, or `text`. |
| `server.logs.security.filename` | `security.log` | Security event log file name within the log directory. |
| `server.logs.security.rotate` | inherited | Security log rotation policy; inherits `server.logs.defaults.rotate`. |
| `server.logs.security.keep` | inherited | Security log retention policy; inherits `server.logs.defaults.keep`. |
| `server.logs.debug.enabled` | `false` | Enable the troubleshooting debug log. |
| `server.logs.debug.format` | `text` | Debug log format: `text` or `json`. |
| `server.logs.debug.filename` | `debug.log` | Debug log file name within the log directory. |
| `server.logs.debug.rotate` | inherited | Debug log rotation policy; inherits `server.logs.defaults.rotate`. |
| `server.logs.debug.keep` | inherited | Debug log retention policy; inherits `server.logs.defaults.keep`. |
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
| `server.logs.access.format` | `apache` | Access-log format (`apache`, `nginx`, `json`, or `custom`). |
| `server.logs.level` | mode-derived | Application log level; an explicit value overrides the mode default. |
| `server.cors.allowed_origins` | inferred trusted origins, then `*` | Explicit CORS origin allow-list; credentials are sent only for explicitly listed origins, never with `*`. |
| `web.ui.theme` | `dark` | Default page theme, configured in `yaml.d/branding.yml`. |
| `web.ui.accent` | built-in accent | Accent color, configured in `yaml.d/branding.yml`; must be a six-digit hex value. |
| `web.headers.clear_site_data.on_token_revocation` | `true` | Send `Clear-Site-Data` when a linked owner token is revoked. |
| `web.headers.clear_site_data.on_consent_withdrawal` | `true` | Send `Clear-Site-Data` when a visitor withdraws stored preferences or analytics consent. |
| `web.headers.clear_site_data.execution_contexts` | `false` | Also reload browser execution contexts when a token is revoked. |

Rate limiting is always enabled. Anonymous requests are limited per client IP, valid bearer-token requests are limited per token, and failed authentication is counted per IP. Boolean configuration accepts the truthy and falsy values defined by the project parser, including `yes/no`, `enable/disable`, `on/off`, and `1/0`.

Runtime mode precedence is `--mode`, then `MODE`, then `server.mode`, then `production`. Accepted values are `production` (`prod`), `development` (`dev`, `devel`), and `debug`. The selected mode is process-local; `--mode` does not rewrite configuration. Debug endpoint enablement is separate: explicit `--debug` wins over non-empty `DEBUG`, which wins over the default enabled by `debug` mode. When enabled, every `/debug/*` route requires the operator bearer token. `server.debug.pprof` and `server.debug.runtime_endpoints` gate their endpoint groups; body logging, query logging, cache logging, and profiling use their corresponding nested settings. Logging level and security-header behavior follow the selected mode and their `server.logs` and `web.*` settings.

### Analytics tracking

Tracking scripts load only when a provider is configured, the visitor has consented to analytics cookies, and the request is not served through Tor or I2P. Provider tags and generated script are omitted otherwise; `/server/tracking.js` returns 404 when tracking is disabled, consent is missing, or the request uses an onion or I2P address. Tracking scripts are served from the same origin where possible, and the configured provider domains are added to the Content Security Policy. Analytics consent can be withdrawn from the preferences page.

```yaml
server:
  tracking:
    type: plausible
    id: your-site-domain
    url: "" # Optional self-managed endpoint; empty uses plausible.io
```

Use `server.tracking.url` for Matomo, Piwik, Open Web Analytics, and Umami. It is optional for Fathom and Plausible when self-managed. Google, Simple Analytics, and Cloudflare use their provider endpoints and do not use this URL field. IDs are validated for the selected provider. Analytics is included in the privacy page and third-party service details when configured.

## CLI settings

The client reads `cli.yml` under the operating system's user config directory at `apimgr/countries/cli.yml`. It supports `server.primary`, `auth.token`, `defaults.output` (`table`, `plain`, or `json`), `lang`, `display.mode`, `tui.theme`, and `update.auto`/`update.channel`. Config files are restricted to owner access. `--server` and `COUNTRIES_SERVER_PRIMARY` take precedence over the file. The first-run setup can save to a file selected by `--config`; “Set as default server” also saves to the default file.
