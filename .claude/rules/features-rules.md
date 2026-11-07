# Feature Rules (PART 17-22)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never enable external providers or send data to them without explicit configuration.
- Never download security or GeoIP databases into the binary or source tree.
- Never schedule mutating work without safe retry, retention, and observability.

## CRITICAL - ALWAYS DO
- Make optional notification, scheduler, GeoIP, and metrics providers configurable.
- Keep backup/restore bounded, validated, and recoverable; preserve data across updates.
- Document update channels, signatures/checksums, and rollback behavior.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Runtime external calls | None for core country data; optional features are configured. | IDEA.md |

## TERMINOLOGY
| Term | Meaning |
|---|---|
| Optional provider | Disabled unless explicitly configured or detected per the spec. |

## QUICK REFERENCE
Expose optional-feature status without leaking secrets; keep disabled operation safe.

---
For complete details, see `AI.md` PARTS 17-22.
