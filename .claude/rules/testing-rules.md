# Testing Rules (PART 28, 29, 30)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never run Go, built binaries, tests, or runtime data in the host or project tree; use the required container or isolated Incus environment and project-scoped OS temp directories.
- Never treat unit tests, curl smoke tests, `make test`, or coverage alone as a substitute for the required end-to-end route and browser behavior coverage.
- Never lower the 60% Go unit coverage threshold, leave an API or frontend route untested, or claim a check that did not run.
- Never ship untranslated user-facing text, invalid locale catalogs, inaccessible controls, or broken no-JavaScript flows.
- Never use the source tree for test runtime output, coverage files, temporary configs, or generated binaries.

## CRITICAL - ALWAYS DO
- Run unit tests for every package with logic and enforce at least 60% Go statement coverage.
- Provide the required Docker and Incus integration scripts; exercise all endpoints, frontend routes, content-negotiation variants, auth flows, and CLI/server interactions.
- Test each route with all applicable `Accept` headers and test every `.txt` endpoint explicitly.
- Run browser E2E against the actual server with and without JavaScript, including responsive layouts, theme behavior, and accessibility checks.
- Build the MkDocs site and keep the ReadTheDocs configuration, pages, links, routes, settings, and integrations accurate to the shipped behavior.
- Keep all configured locale catalogs complete and valid; verify fallback behavior, nested plural rules, number/date formatting, RTL, keyboard access, labels, contrast, and reduced motion.
- Run Go build, tests, direct lint, and vulnerability checks only inside `casjaysdev/go:latest` with `CGO_ENABLED=0`; invoke test/lint commands through the required gate wrapper.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Go unit coverage | At least 60%; do not weaken the threshold. | PART 28 |
| Integration coverage | Every API route, server page, content-negotiation mode, and applicable `.txt` route is exercised. | PART 28 |
| Browser verification | Required end-to-end checks use the real binary/server with JavaScript enabled and disabled. | PART 16, 28 |
| Documentation | MkDocs Material hosted by ReadTheDocs, with a real project URL and current product docs. | PART 29 |
| Localization | Every supported locale has complete messages and locale-aware formatting/pluralization. | PART 30 |

## TERMINOLOGY
| Term | Meaning |
|---|---|
| Unit test phase | Deterministic Go package tests with the required coverage gate. |
| Binary integration phase | Isolated tests of the built server/client and all exposed routes and flows. |
| Browser E2E | User-observable web behavior tested against the running server, including no-JS behavior. |

## QUICK REFERENCE
Run the project test scripts in their prescribed container; enforce coverage and complete endpoint checks; build docs; validate every locale and accessibility requirement; report only actual results.

---
For complete details, see `AI.md` PARTS 28, 29, and 30.
