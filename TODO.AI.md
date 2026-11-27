# Remaining AI.md Alignment Verification

- [ ] Finish the current wrapped `make test` gate and fix every reported failure. The previous completed run passed Go validation and most binary checks; the remaining sitemap, Swagger alias, and GraphQL GET checks hit the default per-IP read limit. The isolated smoke-test configuration now raises its read budget.
- [ ] Complete and run the vendored axe-core browser layout/accessibility matrix from `tests/e2e.sh` against every registered page, error page, viewport, theme, and direction. Fix the reported layout, hit-target, contrast, console, CLS, and component-gallery state or size failures.
- [ ] Reconcile project docs, server-page help, OpenAPI/GraphQL contracts, README, and IDEA.md with the final AI.md-aligned behavior; verify every public route and config example against its implementation.
- [ ] Validate the required CI/CD provider workflows, image/build metadata, security scans, and documented branch protections against AI.md; fix every workflow validation or pipeline error before completion.
- [ ] Run `./tests/incus.sh` against the current tree and fix every failure. AI.md defines Incus as a standalone, developer-initiated full-app check. The prescribed test-lint wrapper refused both `./tests/incus.sh` and `bash ./tests/incus.sh` as unsupported test commands, so the integration suite has not run.
