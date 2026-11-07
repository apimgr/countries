# Configuration Rules (PART 5, 6, 12)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never use `strconv.ParseBool`; use the project’s truthy/falsy boolean parser.
- Never expose secrets or internal paths in public configuration, errors, or health output.
- Never silently ignore invalid security-sensitive settings.

## CRITICAL - ALWAYS DO
- Store server configuration in `server.yml`, create sane defaults, and validate loaded values.
- Document whether a setting reloads at runtime or requires restart.
- Keep server, web UI, proxy, rate limit, and database settings explicit.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Config filename | `server.yml`. | PART 5 |
| Default database | Local SQLite at the platform database path. | PARTS 10, 12 |

## TERMINOLOGY
| Term | Meaning |
|---|---|
| Safe reload | Change applied without restarting listeners or database connections. |

## QUICK REFERENCE
Defaults → parse → validate → persist with restrictive permissions where needed.

---
For complete details, see `AI.md` PARTS 5, 6, and 12.
