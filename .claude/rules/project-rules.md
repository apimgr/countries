# Project Rules (PART 2, 3, 4)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never copy third-party data or code without verifying its license and attribution requirements.
- Never store runtime state, generated binaries, or secrets in source directories.
- Never invent project values or use placeholder contact information in public files.

## CRITICAL - ALWAYS DO
- Keep the application license, embedded licenses, dependencies, and data provenance explicit.
- Resolve paths by platform and keep application data, config, cache, logs, and database paths separate.
- Embed required static assets and keep runtime directories writable by the service user.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Project license | MIT, with third-party notices retained. | PART 2 |
| Country data | Embedded at build time; record source and license. | IDEA.md; PART 2 |

## TERMINOLOGY
| Term | Meaning |
|---|---|
| Embedded data | Static data compiled into the binary. |
| Runtime data | Mutable state outside the source tree. |

## QUICK REFERENCE
Document data source, version, license, attribution, and update date.

---
For complete details, see `AI.md` PARTS 2, 3, and 4.
