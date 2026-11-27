# Remaining AI.md Alignment Verification

- [ ] Run `./tests/incus.sh` against the current commit and fix every failure. AI.md defines Incus as a standalone, developer-initiated full-app check. The prescribed test-lint wrapper refused both `./tests/incus.sh` and `make test-incus` as unsupported test commands, so the integration suite has not run. The wrapped Go test/coverage (61.0%), lint, Docker integration, MkDocs, locale, and cross-platform build checks passed.
- [ ] Recheck GitHub Actions for commit `dc373de9c51b`. CI, Docker Build, License Check, and Daily Build runs `46005655891`, `46005655761`, `46005655964`, and `46005655760` are queued; resolve any failures.
