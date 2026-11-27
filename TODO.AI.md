# Remaining AI.md Alignment Verification

- [x] Complete the wrapped `make test` gate. Go quality, vulnerability, shell checks, and the coverage gate passed (61.2%); the Alpine binary tier passed its CLI, operator authorization, page, API, discovery, Tor, and I2P checks.
- [ ] Run `./tests/incus.sh` against the current tree and fix every failure. AI.md defines Incus as a standalone, developer-initiated full-app check. The prescribed test-lint wrapper refused both `./tests/incus.sh` and `bash ./tests/incus.sh` as unsupported test commands, so the integration suite has not run.
