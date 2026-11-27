# Project Rules (PART 2, 3, 4)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never hardcode project name or organization when they can be resolved from `IDEA.md` project variables.
- Never omit license notices for third-party dependencies or use GPL/AGPL/LGPL dependencies.
- Never put runtime data, build output, test artifacts, or reports in the repository tree.

## CRITICAL - ALWAYS DO
- Use the MIT license in root `LICENSE.md`; attribute third-party dependencies there.
- Follow the required project layout and OS-specific configuration, data, and log paths.
- Resolve project identity consistently from `IDEA.md`, then the documented remote/path fallbacks.
- Keep team AI configuration committed and personal/runtime state out of version control.

## KEY DECISIONS
| Question | Answer | Spec Reference |
|----------|--------|----------------|
| Project license? | MIT for project code; third-party notices remain separate | PART 2 |
| Where do runtime artifacts go? | Project-scoped OS temporary directories | PART 3, PART 4 |

## TERMINOLOGY
| Term | Meaning |
|------|---------|
| Project identity | Names and organization resolved using the documented source order |

## QUICK REFERENCE
Check root inventory, naming, ownership, and platform paths against the spec before adding files.

---
For complete details, see AI.md PART 2, PART 3, and PART 4.
