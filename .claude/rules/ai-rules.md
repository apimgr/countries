# AI Rules (PART 0, 1)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never guess, skip reading, or claim completion without verifying the result.
- Never modify `AI.md`; it is the authoritative, read-only specification.
- Never add features outside the specification or leave placeholders in user-facing pages.
- Never let code, existing docs, audit reports, or TODO files override `AI.md`.

## CRITICAL - ALWAYS DO
- Read the relevant `AI.md` PART and the current file before changing it.
- Keep `README.md`, `docs/`, REST, GraphQL, OpenAPI, and server pages in sync.
- Preserve user changes and report actual validation results.
- Track project implementation work in `TODO.AI.md`; report template defects only in the parent `TODO.API.md` inbox.

## KEY DECISIONS
| Question | Answer | Spec Reference |
|----------|--------|----------------|
| What defines implementation behavior? | `AI.md`; project-specific values come from `IDEA.md` where requested | PART 0, PART 1 |
| What if a requirement is unclear? | Read the relevant section; ask rather than guess | PART 0 |

## TERMINOLOGY
| Term | Meaning |
|------|---------|
| Authoritative spec | The project's read-only `AI.md` |

## QUICK REFERENCE
Do not claim done until requirements are implemented and their applicable checks pass.

---
For complete details, see AI.md PART 0 and PART 1.
