# Testing, Documentation, and I18N Rules (PART 28, 29, 30)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never run project runtime or test data from the repository tree.
- Never let `make test` invoke the on-demand browser E2E or Incus suites.
- Never leave human-readable application strings untranslated or add non-English keys missing from English.
- Never put non-ReadTheDocs content under `docs/`.

## CRITICAL - ALWAYS DO
- Put runtime/test artifacts in project-scoped temporary directories and clean them according to the script contract.
- Keep `make test` as the unit/route commit gate; run Docker, Incus, and browser E2E through their standalone scripts as specified.
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
| Basic test tier | `make test` unit and route tests with the commit-gate checks |

## QUICK REFERENCE
The required standalone scripts include `run_tests.sh`, `docker.sh`, `incus.sh`, and `e2e.sh`; they are not interchangeable.

---
For complete details, see AI.md PART 28, PART 29, and PART 30.
