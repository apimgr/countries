# Makefile Rules (PART 25)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never make CI depend on the developer Makefile.
- Never run Go toolchain commands directly on the host.
- Never leave build/test artifacts in the source tree.

## CRITICAL - ALWAYS DO
- Run Go commands in `casjaysdev/go:latest` with `CGO_ENABLED=0`.
- Keep targets bounded and explicit about output locations.
- Use the required wrapper for test and lint gate commands.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Go toolchain | Docker container only. | PART 25 |
| CI commands | CI invokes toolchain directly, not `make`. | PARTS 25, 27 |

## TERMINOLOGY
| Term | Meaning |
|---|---|
| Local target | Developer convenience, not a CI interface. |

## QUICK REFERENCE
Use project-scoped temporary storage; remove only task-created artifacts.

---
For complete details, see `AI.md` PART 25.
