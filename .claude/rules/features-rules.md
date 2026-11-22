# Feature Rules (PARTS 17-22)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never put operator notifications in the public WebUI or leak internal operational details through public responses.
- Never disable the built-in scheduler or use cron, external schedulers, or empty task placeholders.
- Never treat GeoIP as the sole access-control gate or fail closed when a GeoIP lookup/database is unavailable.
- Never download GeoIP/CVE/Trivy data into source or embed it in the binary; verify published checksums before replacing live databases.
- Never leave scheduled backups unencrypted when compliance mode is enabled, bypass backup verification/retention, or perform destructive restore without required authorization and validation.
- Never auto-install an update unless configured; never install an unverified binary or disclose update availability to public API clients.

## CRITICAL - ALWAYS DO
- Embed customizable email templates with working defaults; validate SMTP messages and TLS settings, autodetect configured relays, and keep email disabled when delivery is unavailable without stopping core service.
- Separate public announcement/toast behavior from operator log/email events; honor notification preferences and configured event switches.
- Keep the persistent built-in scheduler always running with embedded timezone data, catch-up, retries, panic isolation, durable task/run state, log-only activity, and bounded graceful shutdown.
- Register every required real scheduled task; only omit feature-bound tasks when the corresponding feature is off. Persist task failures and notify operators when configured.
- Enable GeoIP by default for server-capable projects, download from the specified ip-location-db GitHub release assets, verify checksums, expose attribution, and fail open on missing/stale lookup data.
- Provide Prometheus metrics with required app/HTTP/database/auth and enabled-feature metrics, configured access control, and the specified aliases.
- Implement full/incremental backup, configured encryption, bounded retention/size caps, post-create verification, and safe restore with path/size limits and no partial modification.
- Honor update channel/defer rules; scheduled checks notify by default, stage/install only when enabled and authorized, verify checksums, atomically replace binaries, preserve rollback, and audit outcomes.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Scheduler availability | Always on; task enablement is per-task and feature-gated. | PART 18 |
| GeoIP policy | Enabled by default; risk signal only, fail open when data is unavailable. | PART 19 |
| Backup encryption | Optional except when compliance mode requires it; passwords are key material and are never logged or stored in archives. | PART 21 |
| Scheduled update | Notify only by default; auto-install requires explicit configuration and correct process privileges. | PART 22 |

## TERMINOLOGY
| Term | Meaning |
|---|---|
| Operator notification | Private log/email event for the server operator. |
| Catch-up window | Maximum age of a missed scheduled execution eligible to run after startup. |
| Incremental backup | Cumulative differential snapshot with one full base, not a chain. |
| Defer window | Minimum release age before a scheduled stable/beta update is eligible. |

## QUICK REFERENCE
Keep these subsystems concrete, observable, configurable, and recoverable; keep sensitive information private and synchronize operator docs with real behavior.

---
For complete details, see `AI.md` PARTS 17-22.
