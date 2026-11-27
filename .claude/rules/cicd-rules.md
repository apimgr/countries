# CI/CD Rules (PART 27)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never use local Makefile targets in CI/CD workflows.
- Never bind host cache paths into CI containers.
- Never use unpinned third-party actions or expose secrets to untrusted jobs.

## CRITICAL - ALWAYS DO
- Keep local development and CI workflows distinct; use explicit tool commands in workflows.
- Run required build, test, lint, coverage, security, and workflow-policy checks.
- Pin third-party workflow actions to full commit SHAs and preserve required permissions and triggers.

## KEY DECISIONS
| Question | Answer | Spec Reference |
|----------|--------|----------------|
| Should CI invoke Makefile targets? | No; workflows run explicit commands | PART 25, PART 27 |
| How are third-party actions pinned? | Full 40-character commit SHA | PART 27 |

## TERMINOLOGY
| Term | Meaning |
|------|---------|
| Provider-native cache | A cache managed by the CI platform rather than a host path |

## QUICK REFERENCE
Use provider-native cache only where specified and keep provider workflows in sync.

---
For complete details, see AI.md PART 27.
