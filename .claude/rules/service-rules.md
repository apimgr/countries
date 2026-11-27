# Service Rules (PART 23, 24)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never prompt for privilege escalation when the user cannot actually escalate.
- Never run service installation or host-mutating operations outside the specified privilege-escalation policy.
- Never assume only systemd is available.

## CRITICAL - ALWAYS DO
- Detect escalation capability before prompting; use a user service when system escalation is unavailable and supported.
- Support all service managers required by the specification.
- Follow the documented service account, privilege-drop, init detection, and lifecycle behavior.

## KEY DECISIONS
| Question | Answer | Spec Reference |
|----------|--------|----------------|
| Should the program always ask for sudo? | No; ask only when escalation is possible and needed | PART 23 |
| Is service support systemd-only? | No; implement every manager required by PART 24 | PART 24 |

## TERMINOLOGY
| Term | Meaning |
|------|---------|
| Privilege escalation | A documented request to perform an operation requiring elevated permissions |

## QUICK REFERENCE
Test install, start, stop, restart, enablement, and persistence under the required init systems.

---
For complete details, see AI.md PART 23 and PART 24.
