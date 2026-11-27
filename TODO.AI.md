# Remaining AI.md Alignment Verification

- [ ] Complete the wrapped `make test` gate. Go quality, vulnerability, shell checks, and coverage (61.2%) passed; the Alpine binary tier showed the owner-token endpoint negotiated YAML because the test omitted `Accept: application/json`. The Docker suite now requests JSON explicitly; rerun it.
- [ ] Run `./tests/incus.sh` against the current tree and fix every failure. AI.md defines Incus as a standalone, developer-initiated full-app check. The prescribed test-lint wrapper refused both `./tests/incus.sh` and `make test-incus` as unsupported test commands, so the integration suite has not run.
