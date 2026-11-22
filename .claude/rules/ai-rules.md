# AI Assistant Rules (PART 0, 1)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never treat code, `IDEA.md`, TODO files, audit reports, or derivative documentation as the specification when they conflict with `AI.md`.
- Never guess business behavior, silently skip requirements, or claim unverified work.
- Never add credentials, private data, or vendor attribution to project files.

## CRITICAL - ALWAYS DO
- Read applicable specification sections and the current file before editing.
- Search for reusable project code before adding new code.
- Complete each started requirement, synchronize documentation, and verify observable behavior.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Which specification controls? | `AI.md` controls implementation and product behavior; use `IDEA.md` only for values or context that `AI.md` explicitly directs you to resolve. | PART 0 |
| Is existing implementation authoritative? | No; align it to the specification. | PART 0 |

## TERMINOLOGY
| Term | Meaning |
|---|---|
| `{project_dir}` | Git repository root. |
| User-facing | API, CLI, webpage, or published documentation behavior. |

## QUICK REFERENCE
Read → search → edit → synchronize docs → validate → report evidence.

---
For complete details, see `AI.md` PARTS 0 and 1.
