# AI.md Alignment Work

This is the implementation backlog for project requirements. Issues in AI.md itself belong only in `~/Projects/github/apimgr/TODO.API.md`.

## Critical (P0)

- [ ] Complete a requirement-by-requirement review of AI.md PARTS 1–32 and record implementation gaps here.
- [ ] Resolve all security, data integrity, configuration-secret, TLS, authentication, authorization, rate-limit, and abuse-prevention gaps identified by that review.
- [ ] Verify all public API and server routes enforce the AI.md method, content-type, size, validation, error, and response contracts.
- [ ] Verify database migrations, backups/restores, startup/shutdown, scheduler jobs, notifications, and service lifecycle preserve data and fail safely.

## High (P1)

- [ ] Implement the project-wide theme modules and shared palette required by AI.md (`src/common/theme`, `src/server/theme.go`, `src/swagger/theme.go`, and `src/graphql/theme.go`); validated by unit tests and the browser light/dark-mode check.
- [ ] Split the frontend stylesheet into `common.css`, `components.css`, and `public.css`, load them in the required order, and use shared palette variables across public pages, Swagger, and GraphiQL; validated by Docker asset checks and browser E2E.
- [ ] Finish page-by-page alignment of server-rendered pages, shared layouts, error pages, no-JavaScript behavior, keyboard access, responsive behavior, localization, and consent/preferences.
- [ ] Complete CLI, TUI, GUI, and cross-platform behavior required by the project specification and IDEA.md.
- [ ] Complete Docker, Compose, multi-platform build, service-package, and all-provider CI/CD requirements; fix any remaining workflow drift.
- [ ] Complete browser coverage for every IDEA.md feature and the required SSR, no-JavaScript, and full-JavaScript tiers.

## Medium (P2)

- [ ] Audit every AI.md configuration key against defaults, validation, environment variables, generated config, docs, and runtime behavior.
- [ ] Reconcile README, MkDocs/ReadTheDocs pages, OpenAPI, GraphQL, examples, and operational guides with implemented behavior.
- [ ] Verify locale catalogs, translation completeness, accessibility names, and localized server pages against AI.md.
- [ ] Verify license and provenance records for all shipped and vendored assets.
- [ ] Complete every remaining AI.md FINAL CHECKPOINT item and replace this backlog with only verified outstanding work.

## Low (P3)

- [ ] Review AI.md-required polish and optional improvements after all P0–P2 requirements are complete.

## Validation

- [ ] Run the prescribed container-based build, unit/coverage, lint, vulnerability, Docker, Incus, browser, accessibility, locale, and documentation checks.
- [ ] Record actual commands and results, fix failures, then repeat the complete required validation after the final code and documentation changes.
