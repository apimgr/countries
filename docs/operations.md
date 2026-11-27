# Operations

This guide covers the background services and operational workflows that are not part of ordinary country lookups. Runtime state lives in the configured OS-specific config, data, log, cache, and backup directories; it does not belong in the source checkout. See the [configuration reference](configuration.md) for directory resolution and setting defaults.

## Scheduler

The built-in scheduler runs in the server process. It uses `server.scheduler.timezone` for cron schedules and `server.scheduler.catch_up_window` to bound missed work after a restart. Each configured task has an `enabled` flag, a schedule, and optional retry behavior. Do not run a second external scheduler for these tasks.

The default task set includes certificate renewal, weekly GeoIP data refresh, daily blocklist and vulnerability-data refresh, security and token cleanup, update checks, log rotation, daily backups, disk, database, and health checks, and Tor/I2P health checks. The database check runs every minute by default and reports sustained connection failures after the configured consecutive-failure threshold; it clears the alert after a successful probe. Hourly incremental backups are disabled by default. Task status and scheduler health are visible through the health endpoints; failed scheduled tasks are written to the application logs and configured notifications.

Configure task schedules and retry behavior in `server.yml` under `server.scheduler`. The server validates schedules and timezone values at startup. See the [configuration reference](configuration.md#external-security-data) for data refresh behavior and [backup and restore settings](configuration.md#backup-and-restore).

## Maintenance mode and recovery

If the database cannot open, connect, or apply its schema at startup, the listener starts in maintenance mode and the health endpoint stays available. The server retries initialization using `server.maintenance.self_healing.retry_interval` (30 seconds by default); after recovery it completes startup and serves the full API. During startup recovery, health, static assets, and database-independent country lookups and information pages remain available; other routes return `503 Service Unavailable`. During a database failure after startup, reads continue where their handlers can operate and writes receive `503` with `Retry-After` and maintenance headers. `/server/healthz` reports the reason, attempt count, and next retry. `server.maintenance.self_healing.max_attempts: 0` retries without a limit; a positive limit stops retries and leaves the service in maintenance mode until an operator fixes the cause and restarts it. Exhaustion is audited and produces one operator notification when email is configured.

For a remote libSQL database with the SQLite read cache enabled and `offline_mode` enabled, a previously synchronized cache can serve eligible reads while the remote is unavailable. Writes and routes that require generated secrets remain unavailable with `503`. Application secrets are remote-only and are never included in the cache or generated on an offline node. A cache with no successful sync cannot serve reads.

The current recovery loop checks database connectivity. It does not yet detect filesystem write failures or apply automatic disk-pressure cleanup. For filesystem or disk-space problems, check the service logs, filesystem permissions, free space, and backup retention manually; do not rely on automatic cleanup.

## Backups and restore

Use the server maintenance commands, not `countries-cli`:

```sh
countries --maintenance backup
countries --maintenance backup /path/to/backup.tar.gz --include-ssl --include-data
countries --maintenance restore [/path/to/backup.tar.gz]
```

If no archive is named, restore selects the newest full backup by its UTC filename date (later modification time breaks ties) and requires typed confirmation. `countries --maintenance backup test` uses the same default selection without changing live data.

SQLite backups contain the server database and configuration. With a remote database, the provider retains the database and the archive contains configuration but omits the remote database and its derived cache. Optional data and SSL files are included only when requested. New backups use AES-256-GCM when the backup encryption password is configured in `secrets.yml`. Compliance mode requires that password before creating a backup. The password is not accepted as a command-line argument.

Backups also include every database-managed application secret, including previous-key values and their grace-period expiry timestamps. With SQLite, restore replaces the local application secret rows. With a remote database, the archive omits both the remote database and its derived `cache.db`; its `app_secrets.json` is applied only when the remote application-secrets table is empty. An archive made with SQLite can seed an empty remote database from `server.db`; an existing remote database is left unchanged.

Scheduled full and incremental backups run daily at 02:00 in the configured scheduler timezone. Hourly incrementals are disabled by default. Retention settings control the number of daily full backups, weekly/monthly/yearly checkpoints, and the total backup-volume size cap. The server verifies archives after creation. Restore checks archive limits and integrity before staging replacement files; stop the service before a restore and keep an independent copy of the backup until the service has restarted successfully.

See the [server CLI reference](cli.md#server-maintenance-commands) and [backup configuration](configuration.md#backup-and-restore) for all options and defaults.

## GeoIP and security data

GeoIP country data is downloaded into the runtime security-data directory. Country blocking is disabled when both `server.geoip.deny_countries` and `server.geoip.allow_countries` are empty. A deny list blocks only listed ISO alpha-2 codes; an allow list permits only listed codes and takes precedence if both are set. Allowlisted IPs and private/internal addresses bypass country checks. If the country database is unavailable, country blocking fails open and the server logs the condition.

GeoIP refreshes run weekly by default. Built-in and configured IP/domain blocklists and vulnerability databases have their own scheduled updates. Failed refreshes retain the last valid data; missing or stale security data is reported through health status. Optional NVD credentials and Trivy data are configured through the documented `secrets.yml` and `server.yml` settings. The application does not bundle jurisdiction-specific country presets.

See [external security-data configuration](configuration.md#external-security-data), [security policy](security.md), and the public `/server/healthz` page for current status and attribution.

## Metrics and monitoring

Successful health probes are omitted from access logs and console request lines, including probes made by `countries --status`; failed probes are logged. The status command reports its result through stdout and its exit status (0 healthy, 1 unhealthy), and does not require authentication.

Metrics are internal operational endpoints. Keep them off public reverse-proxy routes and restrict access at the firewall or network-policy layer. Configure a separate bearer token for each service in `server.yml` and keep those credentials out of URLs, source control, and logs.

| Endpoint | Purpose |
|---|---|
| `/server/metrics/prometheus` | Prometheus text exposition for all exported metrics. |
| `/server/metrics/grafana` | Importable Grafana dashboard JSON. |
| `/server/metrics/loki` | Bounded recent log entries in Loki-compatible stream format. |
| `/server/metrics` | Prometheus-compatible alias. |

Versioned and unversioned API aliases are also available. Root `/metrics` aliases are controlled by `server.metrics.root.enabled`. A failed authorization is not a reason to expose these endpoints publicly; configure the appropriate service token and restrict network access. Health endpoints are separate and provide deployment health rather than detailed monitoring data.

## Tor and I2P

When a Tor binary is available, the server manages its own hidden service and loopback HTTP listener. It does not operate as an exit relay. Tor configuration and key material are kept in the configured runtime directories. The `/server/help` and health pages show the onion address and current status when Tor is enabled.

I2P is enabled by the computed container-aware default or explicit `server.i2p.enabled` / `I2P_ENABLED` / `--i2p` configuration. The server manages its own I2P eepsite and SAM session; it does not manage an external router. Persistent destination keys preserve the address across restarts. The I2P destination address and provider state are shown in `/server/help` and health output when the service is enabled and running.

Neither overlay is required for ordinary clearnet operation. If an overlay reports unavailable, check its feature status and the project-owned logs in the configured log directory. Do not expose the Tor control port or I2P SAM listener to the network.

## TLS certificates

The server can use operator-provided certificates or automatic ACME/Let's Encrypt issuance and renewal. Automatic issuance is configured under `server.ssl.letsencrypt`; choose HTTP-01 or a supported DNS-01 provider and store provider credentials in the documented secrets file. DNS-01 TSIG key files must remain inside the SSL runtime directory. Certificate and account keys are stored with restricted permissions outside the source tree. HTTP-01 and TLS-ALPN-01 are served by the existing HTTP and HTTPS listeners; the server does not open separate challenge ports. Direct validation requires ports 80 and/or 443 to reach those listeners. Configure `server.port: "80,443"` as needed or forward those ports through a reverse proxy or port mapping; use DNS-01 when that routing is unavailable.

Renewal is performed by the built-in scheduler. Renewal failures leave the existing certificate in place and are reported to logs and configured notifications. Review the effective listener and certificate paths in the [configuration reference](configuration.md) before enabling HTTPS.

## Updates

The server checks its configured release branch on the scheduled update task. Automatic updates, when enabled, download and verify a release, stage it in the runtime data directory, and apply it at startup with the required privileges. Manual maintenance commands are:

```sh
countries --update check
sudo countries --update yes
countries --update branch stable
```

The accepted branch names are `stable`, `beta`, and `devel`. Updates are checksum-verified. A staged update is applied during restart/startup, and update outcomes are recorded in the update audit log. The web page detects a new build and offers a user-approved reload; it does not perform a privileged server update.

See the [CLI reference](cli.md#updates) for client update behavior and [security documentation](security.md) for credential and audit-log handling.
