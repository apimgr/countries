# Backend Rules (PART 9, 10, 11, 31)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never expose secrets, credentials, private paths, stack traces, or internal diagnostics on public surfaces.
- Never make schema migrations destructive, non-idempotent, or dependent on an operator command.
- Never use GeoIP as the sole access-control signal.
- Never silently weaken authentication or security behavior in debug mode.

## CRITICAL - ALWAYS DO
- Return the canonical error contract and log actionable internal context without exposing it to clients.
- Make migrations additive, idempotent, and automatic at startup.
- Keep public output limited to public-safe operational data; gate detailed diagnostics as specified.
- Implement required Tor support and follow the distinct runtime defaults for optional I2P support.

## KEY DECISIONS
| Question | Answer | Spec Reference |
|----------|--------|----------------|
| Can debug mode bypass auth? | No; all security checks remain enforced | PART 6, PART 11 |
| Can GeoIP grant access? | No; it is a deny-only risk signal | PART 19 |

## TERMINOLOGY
| Term | Meaning |
|------|---------|
| Public-safe data | Information explicitly allowed on unauthenticated surfaces |

## QUICK REFERENCE
Treat logs, errors, caches, migrations, and overlay-network data as security-sensitive.

---
For complete details, see AI.md PART 9, PART 10, PART 11, and PART 31.
