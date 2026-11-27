# Remaining AI.md Alignment Verification

- [ ] Run `./tests/incus.sh` against the current commit and fix every failure. AI.md defines Incus as a standalone, developer-initiated full-app check. The prescribed test-lint wrapper refused both `./tests/incus.sh` and `make test-incus` as unsupported test commands, so the integration suite has not run. The current-tree wrapped `make test` and separate lint gate passed; coverage is 61.0%.
