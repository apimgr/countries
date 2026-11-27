# Binary Rules (PART 7, 8, 32)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never assume the executable retains its build-time name; users may rename binaries.
- Never hand-roll primary CLI argument parsing or parse `os.Args` manually.
- Never mix diagnostics with machine-readable stdout output.

## CRITICAL - ALWAYS DO
- Provide both the server binary and its companion client.
- Show the actual executable name in user-facing help and usage output.
- Send errors, warnings, and diagnostics to stderr; keep JSON and other machine output clean on stdout.
- Follow the documented flag, output, and client authentication contracts.

## KEY DECISIONS
| Question | Answer | Spec Reference |
|----------|--------|----------------|
| Is a companion client required? | Yes, for every server project | PART 32 |
| Which token presentation is accepted by protected APIs? | `Authorization: Bearer ...` only | PART 8, PART 14 |

## TERMINOLOGY
| Term | Meaning |
|------|---------|
| Companion client | The required CLI that accesses the server's documented functionality |

## QUICK REFERENCE
Use the project CLI conventions and test renamed-binary behavior where the spec requires it.

---
For complete details, see AI.md PART 7, PART 8, and PART 32.
