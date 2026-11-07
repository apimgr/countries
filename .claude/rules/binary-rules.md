# Binary Rules (PART 7, 8, 32)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never require a runtime interpreter, external data file, or CGO library for core operation.
- Never make version/help commands require privileges or network access.

## CRITICAL - ALWAYS DO
- Build static binaries with `CGO_ENABLED=0` and embed required assets.
- Support all eight specified OS/architecture targets for server and client.
- Keep CLI output, config behavior, and commands documented and testable.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Binary model | Self-contained Go binaries. | PART 7 |
| CLI architecture | Server and client are separate binaries. | PARTS 8, 32 |

## TERMINOLOGY
| Term | Meaning |
|---|---|
| Server binary | Hosts the HTTP API and web UI. |
| Client binary | User-facing CLI/TUI client. |

## QUICK REFERENCE
Help, version, status, paths, service, maintenance, update, and client features must match PARTS 8 and 32.

---
For complete details, see `AI.md` PARTS 7, 8, and 32.
