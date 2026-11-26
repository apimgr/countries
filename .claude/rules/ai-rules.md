# AI Assistant Rules (PART 0, 1)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never treat code, `IDEA.md`, TODO files, audit reports, or derivative documentation as the specification when they conflict with `AI.md`.
- Never guess business behavior, silently skip requirements, or claim unverified work.
- Never add credentials or private data to project files, or AI authorship attribution anywhere.
- Never edit `AI.md`, create general report files, or put project implementation tasks in the template-maintainer inbox.

## CRITICAL - ALWAYS DO
- Read the current applicable `AI.md` PART and the target file immediately before every write.
- Search for reusable project code before adding new code.
- Complete each started requirement, synchronize documentation, and verify observable behavior.
- Track project work in `TODO.AI.md`; report `AI.md` defects only in the parent `TODO.API.md` after searching for duplicates.
- Use `AUDIT.AI.md` only for an explicitly requested audit with more than five findings, and remove it after all findings are resolved.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Which specification controls? | `AI.md` controls implementation and product behavior; use `IDEA.md` only for values or context that `AI.md` explicitly directs you to resolve. | PART 0 |
| Is existing implementation authoritative? | No; align it to the specification. | PART 0 |
| May `AI.md` be changed in this repository? | No; it is read-only. | PART 0 |

## TERMINOLOGY
| Term | Meaning |
|---|---|
| `{project_dir}` | Git repository root. |
| User-facing | API, CLI, webpage, or published documentation behavior. |

## QUICK REFERENCE
Read current AI.md PART and target → search → edit → synchronize docs → validate in the required container → report evidence.

---
For complete details, see `AI.md` PARTS 0 and 1.
