# Makefile Rules (PART 25)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never add Makefile targets beyond the six specified core targets and `clean`.
- Never make CI depend on the developer Makefile or run Go commands on the host.
- Never put build, runtime, test, or release artifacts in an unapproved location.
- Never infer or invent an official site or override user-supplied version data.

## CRITICAL - ALWAYS DO
- Run Go commands in `casjaysdev/go:latest` with `CGO_ENABLED=0` and `GOFLAGS=-buildvcs=false`.
- Keep `dev` artifacts in project-scoped OS temp; use `binaries/` and `releases/` only for their specified targets.
- Build the full eight-platform server and CLI matrix when the full build target requires it.
- Derive project identity from the provider/path; use `site.txt` only when the user supplied it.
- Keep CI's direct toolchain commands independent of the Makefile.

## KEY DECISIONS
| Question | Answer | Spec Reference |
|---|---|---|
| Core targets | `build`, `local`, `release`, `docker`, `test`, `dev`, plus housekeeping `clean` | PART 25 |
| Local version | Explicit user `VERSION`, then nonempty `release.txt`, then short commit/devel | PART 25 |
| Platforms | Linux, Darwin, Windows, FreeBSD; amd64 and arm64 each | PART 25 |
| Site URL | Omit when not user-provided; never infer from repo/domain | PART 25 |

## QUICK REFERENCE
Read the target's AI.md contract before changing it. Keep commands bounded, name output paths explicitly, and remove only task-created temporary data.

---
For complete details, see `AI.md` PART 25.
