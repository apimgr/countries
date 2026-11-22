# CI/CD Rules (PART 27)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never make CI call Makefile targets or depend on local host caches.
- Never use unpinned third-party GitHub Actions, omit mandatory secret/security scans, or publish unverified artifacts.
- Never let an older push or same-tag release run race a newer run.
- Never claim a platform or image variant that the workflow has not built and verified.

## CRITICAL - ALWAYS DO
- Ship equivalent workflows for GitHub Actions, GitLab CI, Gitea Actions, Forgejo Actions, and Jenkins; the actual hosting provider runs its native workflow.
- Set VERSION, COMMIT_ID, and BUILD_EPOCH explicitly; derive BUILD_DATE from BUILD_EPOCH for image labels only.
- Run toolchain jobs in the required Go container with CI-native caching; set `options: "--user 0:0"` on GitHub container jobs.
- Pin third-party GitHub Actions to full 40-character commit SHAs; keep policy checks and security scans active on required events.
- Enforce tests, lint/static analysis, vulnerability scanning, coverage, and release artifact parity.
- Separate PR validation from publish workflows and apply ref-specific concurrency cancellation.

## KEY DECISIONS
| Question | Answer | Spec Reference |
|---|---|---|
| Repository provider | GitHub; equivalent CI files remain required for the other four providers | PART 27 |
| Dependency automation | Renovate | PART 27 |
| Stable release | Numeric semver tag only; CI derives version from the tag | PART 27 |

## QUICK REFERENCE
Read the provider-specific PART 27 section before changing workflow syntax. Verify SHA pins at three points as required by project conventions.

---
For complete details, see `AI.md` PART 27.
