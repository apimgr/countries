# Remaining AI.md Alignment Verification

- [ ] Run the required Incus integration suite and browser E2E suite (JavaScript enabled and disabled) against the current commit; fix every failure. The test-lint gate refused the Incus and E2E entry points as unsupported commands, so neither ran against this commit. The wrapped Go test/coverage (61.0%), lint, Docker integration, MkDocs, locale, and cross-platform build checks passed.
- [ ] Recheck GitHub Actions for commit `f8c2465667fe` after runner capacity returns. Main CI, Docker Build, and License Check passed; Daily Build run `45993359461` remains queued with 0/8 matrix builds complete.
