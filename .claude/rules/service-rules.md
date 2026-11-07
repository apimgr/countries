# Service Rules (PART 23, 24)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never assume systemd, root access, or a specific init system is present.
- Never run service commands through a shell with interpolated user input.

## CRITICAL - ALWAYS DO
- Detect platform and privilege context; keep install/uninstall explicit and reversible.
- Support service lifecycle operations only where the platform supports them.
- Run with least privilege and correct file ownership.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Privilege escalation | Least privilege; elevate only a narrowly scoped operation. | PART 23 |

## TERMINOLOGY
| Term | Meaning |
|---|---|
| Service manager | Platform facility supervising the server process. |

## QUICK REFERENCE
Report unsupported operations clearly and return accurate exit codes.

---
For complete details, see `AI.md` PARTS 23 and 24.
