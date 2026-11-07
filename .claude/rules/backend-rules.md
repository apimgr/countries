# Backend Rules (PART 9, 10, 11, 31)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never concatenate untrusted data into SQL or trust forwarded headers from an untrusted peer.
- Never log credentials, tokens, request bodies, or other sensitive values.
- Never execute or serve user-controlled active content on the application origin.

## CRITICAL - ALWAYS DO
- Use parameterized SQL, bounded queries, connection pooling, and idempotent schema setup.
- Use SQLite via `modernc.org/sqlite` for local state; keep remote database support explicit.
- Apply request IDs, trusted proxy checks, security headers, rate limits, structured audit/security logging, and log rotation.
- Keep Tor/I2P optional and activate only when configured and available.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| SQLite driver | `modernc.org/sqlite`; never CGO sqlite. | PART 10 |
| Rate-limit state | Persist in `server.db`; optional remote Valkey only when configured. | PARTS 10, 12 |

## TERMINOLOGY
| Term | Meaning |
|---|---|
| Trusted proxy | An immediate peer explicitly allowed to supply client identity headers. |

## QUICK REFERENCE
Bound inputs and bodies; hide internal errors; correlate requests by UUIDv4.

---
For complete details, see `AI.md` PARTS 9, 10, 11, and 31.
