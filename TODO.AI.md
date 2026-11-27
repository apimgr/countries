# Remaining AI.md Alignment Verification

- [ ] Complete API authentication check, startup maintenance behavior, configuration/main loader, audit rotation and client first-run/update alignment with AI.md; keep all API, GUI and setup text translated in every supported locale.
- [ ] Add persisted owner-token scopes, migrate primary tokens to `global`, support linked-token scope selection, and enforce read/read-write/global authorization in server handlers.
- [ ] Align backup redaction and the Windows database path with the latest AI.md requirements.
- [ ] Verify all changed project pages and documentation against the implemented API, configuration, maintenance, audit and client behavior.
- [ ] Pass the required Go test/coverage, lint, Docker and Incus integration, browser E2E with JavaScript enabled and disabled, MkDocs, locale, and cross-platform build checks; fix every failure.
- [ ] Review the complete diff and AI.md alignment, including route coverage and project TODO hygiene, before preparing the commit.
- [ ] After commit and push, inspect all configured provider CI runs and resolve any failures.
