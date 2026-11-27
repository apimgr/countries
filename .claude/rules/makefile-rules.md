# Makefile Rules (PART 25)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never add Makefile targets beyond `build`, `test`, `release`, `docker`, `clean`, and `help`.
- Never use Makefile targets from CI/CD workflows; CI runs explicit commands.
- Never let local-only version metadata override an explicitly supplied version.

## CRITICAL - ALWAYS DO
- Keep build, test, and Docker compilation inside the specified containers.
- Make `make test` run the required analysis, script checks, vulnerability scan, unit tests with coverage, and the basic binary checks through `./tests/run_tests.sh`.
- Keep local development and CI/CD responsibilities separate.

## KEY DECISIONS
| Question | Answer | Spec Reference |
|----------|--------|----------------|
| May CI call `make test`? | No; workflow jobs run explicit commands | PART 25, PART 27 |
| What are the six targets? | `build`, `test`, `release`, `docker`, `clean`, `help` | PART 25 |

## TERMINOLOGY
| Term | Meaning |
|------|---------|
| Local target | A Makefile command for developer use, not invoked from CI/CD |

## QUICK REFERENCE
Preserve target names, container limits, `binaries/` and `releases/` output paths, and cleanup behavior.

---
For complete details, see AI.md PART 25.
