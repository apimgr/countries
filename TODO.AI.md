# Remaining AI.md Alignment Verification

- [ ] Move startup orchestration into `src/app` with a testable `app.Run(ctx, cfg)` entry, leaving `src/main.go` as the thin flag/config entry point, and add the specified cancellation/readiness/request unit test.
- [ ] Complete per-kind log output, formatting, rotation, and retention from `server.logs`; currently only access formatting is being aligned and the application still routes most events through the standard server logger.
- [ ] Run the tagged browser E2E suite after the test/lint gate accepts the command; the session wrapper rejected the earlier invocation before running it.
- [ ] Run the optional Incus integration suite when a suitable Incus test environment is available; the session wrapper rejected the earlier invocation before running it.
- [ ] After commit and push, inspect all configured provider CI runs and resolve any failures.
