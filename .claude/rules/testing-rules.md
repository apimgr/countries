# Testing, Documentation, and I18N Rules (PART 28, 29, 30)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never run project runtime or test data from the repository tree.
- Never let `make test` invoke the on-demand browser E2E or Incus suites.
- Never leave human-readable application strings untranslated or add non-English keys missing from English.
- Never put non-ReadTheDocs content under `docs/`.

## CRITICAL - ALWAYS DO
- Put runtime and test data in project-scoped temporary directories and clean them according to the script contract; compiled build artifacts go in `binaries/` as required by AI.md.
- Keep `make test` as the commit gate: it runs Go quality checks and unit coverage, then the basic Docker binary tier through `./tests/run_tests.sh`.
- Run `./tests/incus.sh` as the separate full-app init-system check; run `./tests/e2e.sh` on demand, outside the commit gate.
- Maintain ReadTheDocs documentation that describes the shipped product accurately.
- Keep localization, English fallback behavior, accessibility, and JavaScript-on/off behavior compliant.

## KEY DECISIONS
| Question | Answer | Spec Reference |
|----------|--------|----------------|
| Is browser E2E a commit gate? | No; it is developer-initiated and on demand | PART 28 |
| What belongs in `docs/`? | ReadTheDocs documentation only | PART 29 |

## TERMINOLOGY
| Term | Meaning |
|------|---------|
| Basic test tier | `./tests/run_tests.sh`, invoked by `make test`, for compiled-binary checks in Docker |

## QUICK REFERENCE
The required scripts include `run_tests.sh`, `docker.sh`, `incus.sh`, and `e2e.sh`; the first two make up the basic binary tier, while Incus and E2E are explicit standalone runs.

---
For complete details, see AI.md PART 28, PART 29, and PART 30.
