# Current AI.md Alignment Audit

Audit date: 2026-11-05  
Authority: `AI.md`; country behavior: `IDEA.md`  
Status: implementation in progress; no final validation or commit gate has run.

## Open findings

- [ ] Server configuration does not yet cover or validate the complete AI.md settings surface. API version and remote database/cache configuration are not wired end to end.
- [ ] SQLite state is only partially implemented. Review schema ownership and use for scheduler, backups, audit events, and API token/owner tables against the read-only product behavior.
- [ ] Rate limiting has persistent SQLite support, but its edge behavior, configured policy, and full AI.md middleware/security integration still need review.
- [ ] Update handling has release lookup, branch filtering, checksum verification, and atomic replacement, but scheduled policy/defer behavior, restart/recovery, and CLI update/discovery requirements remain incomplete.
- [ ] Service commands dispatch through platform-specific implementations, but install/control behavior still needs a complete per-platform compliance review.
- [ ] API path helpers currently resolve to `/api/v1`; configured API-version support and required autodiscovery/client-binary endpoints are not complete.
- [ ] The CLI has list/get/search/coordinate/stat commands and a line-oriented first-run/menu flow. It still lacks required mode detection, Bubble Tea TUI, native GUI, full flags/config behavior, language support, completions, and self-update.
- [ ] Internationalization catalogs contain only a small subset of the application's strings. Legal/info, API errors, OpenAPI, GraphQL, server CLI, and client CLI are not fully translated.
- [ ] Server-rendered search and coordinate forms and theme cookies exist. Verify all pages and complete the remaining accessibility, responsive, cache, and PWA requirements.
- [ ] Production, development, and test Compose files now exist and include required service names/logging/cache declarations. The application does not yet consume the declared `CACHE_URL`, so production cache behavior is not verified.
- [ ] `Dockerfile` and `Dockerfile.dev` still need full comparison with AI.md build/runtime requirements, including site/build metadata and platform behavior.
- [ ] README and user docs were partly synchronized, but endpoint, configuration, CLI, operations, and product behavior still require a full source-of-truth review.
- [ ] Makefile, CI/release workflows, repository support files, dependency/license inventory, and generated artifacts still need full compliance review.
- [ ] Required validation has not yet been rerun after the latest edits. Container tests, lint, cross-platform builds, documentation, Compose/test workflows, and final integration checks remain open.

## Corrected findings from the previous audit

- [x] The country-data generator has been moved out of the forbidden root `cmd/` directory to `src/countrydata/`.
- [x] Web search and coordinate lookup have server-rendered GET forms for JavaScript-free use.
- [x] Theme preferences support server-rendered dark/light/auto selection and cookies.
- [x] API success responses use indented JSON and the standard pagination envelope with a 250 default and 1000 maximum.
- [x] API route construction is centralized through a shared helper; configurable API version support remains open.
- [x] SQLite-backed rate-limit state and secure database/config file permissions have been added; broader database alignment remains open.
- [x] Server update flow includes checksum verification and atomic replacement; scheduled and client update requirements remain open.
- [x] Service command dispatch no longer assumes `systemctl`; cross-platform completeness remains open.
- [x] Production, development, and test Compose files now exist; cache integration and full validation remain open.
- [x] Current dataset count, source versions, licenses, and field-level provenance are documented in `src/data/README.md`.
