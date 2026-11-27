# Feature Rules (PART 17-22)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never send configured SMTP credentials before TLS is established.
- Never let one scheduled task panic crash the scheduler or server.
- Never treat GeoIP as the sole access gate.
- Never make backup copies expose secrets that the backup contract requires blanked or protected.
- Never install updates automatically when the specification requires explicit operator action.

## CRITICAL - ALWAYS DO
- Provide customizable email templates and follow the documented TLS behavior.
- Start the built-in scheduler with the application and isolate each task run.
- Use the required GeoIP data source and update it through the scheduler; apply it as a risk signal.
- Provide Prometheus-compatible metrics and the specified backup/restore and update behavior.

## KEY DECISIONS
| Question | Answer | Spec Reference |
|----------|--------|----------------|
| Does GeoIP alone authorize a request? | No; it may deny or raise risk, never grant access | PART 19 |
| Is update installation automatic by default? | No; follow the explicit operator approval contract | PART 22 |

## TERMINOLOGY
| Term | Meaning |
|------|---------|
| Risk signal | An input that may raise risk or deny access but cannot grant access by itself |

## QUICK REFERENCE
Keep optional capabilities opt-in where the spec says so; preserve backup confidentiality and restore semantics.

---
For complete details, see AI.md PART 17, PART 18, PART 19, PART 20, PART 21, and PART 22.
