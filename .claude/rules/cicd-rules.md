# CI/CD Rules (PART 27)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never bypass required security checks or publish unverified artifacts.
- Never build only the host platform when a release claims multiple platforms.
- Never commit secrets or use mutable third-party actions contrary to pinning rules.

## CRITICAL - ALWAYS DO
- Run the provider’s native workflow directly; do not depend on Makefile targets.
- Verify formatting, static analysis, vulnerability checks, tests, coverage, and release outputs.
- Keep release, beta, and daily workflows consistent with supported branches and tags.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Dependency updates | Renovate. | PART 27 |
| CI provider | GitHub Actions for this GitHub repository. | PART 27 |

## TERMINOLOGY
| Term | Meaning |
|---|---|
| Release workflow | Workflow publishing versioned binaries and images. |

## QUICK REFERENCE
Inspect provider and workflow files before making CI claims.

---
For complete details, see `AI.md` PART 27.
