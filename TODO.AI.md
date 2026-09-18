# TODO.AI.md

Issues discovered while authoring `.github/workflows/` CI/CD for this
project (per AI.md PART 27). Logged here per policy — not fixed inline,
since they are outside the scope of CI/CD workflow authoring.

## 1. Legacy Node.js artifacts contradict AI.md's Go-only stack

`go.mod` and `Makefile` confirm the real stack is Go 1.24
(`github.com/apimgr/countries`), with a full `src/**/*.go` tree. The
following files are stale/legacy Node/Express artifacts that predate the
Go rewrite and should be removed:

- `package.json` (Express-shaped: `main: server.js`, ISC license)
- `server.js`
- `node_modules/` (empty — never actually installed)
- `healthcheck.js`
- `views/`
- `public/`
- `tests/` (Node-style, distinct from the Go `src/**/*_test.go` tests)

## 2. RESOLVED — `src/paths/paths_test.go` tests now pass

Previously 3 tests (`TestGetConfigDir_ContainsAppName`,
`TestGetDataDir_ContainsAppName`, `TestGetLogsDir_ContainsAppName`)
failed when run inside a container. The test file (still untracked in
git: `?? src/paths/paths_test.go`) has since been updated to branch on
`isContainer()` and assert the correct hardcoded `/config`, `/data`,
`/logs` paths in that case. Verified: `go test ./src/paths/...` passes
inside `casjaysdev/go:latest`.

## 3. Test coverage is 54.7%, still below AI.md's 60% CI gate

`ci.yml`'s `test` job enforces a hard 60% coverage threshold. Test files
have been added for every package (`src` 0.3%, `src/admin` 96.0%,
`src/config` 91.2%, `src/countries` 98.9%, `src/data` 100%, `src/mode`
100%, `src/paths` 40.6%, `src/scheduler` 82.4%, `src/server` 86.7%,
`src/service` 2.0%, `src/ssl` 73.2%), all passing. Total coverage
measured locally: 54.7%, still under the 60% gate. Remaining gaps:

- `src` (main) — 0.3% coverage (only `printHelp()` is exercised;
  `main()` itself is untestable as written — see item 9's CLI flag
  gaps)
- `src/service` — 2.0% coverage (far below the rest)
- `src/paths` — 40.6% coverage

## 4. staticcheck findings

- `src/mode/mode.go:10:7` — unused `const appName` (U1000)
- `src/service/service.go:362:17` — deprecated `strings.Title` (SA1019)

## 5. govulncheck findings — 3 CVEs, called code paths

- `GO-2026-5777` and `GO-2026-5775` in `github.com/go-chi/chi/v5@v5.0.11`'s
  `RealIP` middleware (IP spoofing via X-Forwarded-For), reachable via
  `src/admin/handlers.go:36:8`. Fixed in chi v5.3.0.
- `GO-2026-5026` in `golang.org/x/net@v0.47.0`'s `idna` package,
  reachable via `src/ssl/ssl.go:101:37`. Fixed in x/net v0.55.0.

## 6. `docker/Dockerfile.dev` is missing

`.github/workflows/docker.yml`'s `build-devel` job (per AI.md PART 27)
builds `docker/Dockerfile.dev` on schedule, workflow_dispatch, and every
non-tag push. This file does not exist in the repo, so `build-devel`
will fail until it is authored per AI.md's Dockerfile Requirements
(multi-stage, `casjaysdev/go:latest` builder, `alpine:latest` runtime,
app binary + debug tooling, `:devel` tag).

## 7. Existing `docker/Dockerfile` deviates from AI.md's Dockerfile Requirements

- Uses `golang:alpine` as the builder stage instead of the mandated
  `casjaysdev/go:latest`.
- Embeds hardcoded `LABEL` blocks; AI.md requires Dockerfiles stay clean
  with no `LABEL` blocks — OCI labels are applied by CI
  (`docker/build-push-action`'s `labels:`/`annotations:` inputs), which
  `docker.yml` now does correctly.
- `docker/Dockerfile:9` uses `VCS_REF` as a build-arg name — must be
  `COMMIT_ID` per convention.
- `docker/Dockerfile:20` sets `-X main.CommitID=...` and
  `-X main.BuildEpoch=...`, but `src/main.go` declares `main.Commit` and
  `main.BuildDate` — these ldflags silently no-op. (The same mismatch was
  found and fixed in the new `release.yml`/`beta.yml`/`daily.yml`
  workflows during this session; the Dockerfile itself was left
  untouched, out of scope.)

## 8. `Makefile` convention violations (go-lint findings, all pre-existing)

- Line 4-5: `PROJECTNAME`/`PROJECTORG` hardcoded as `countries`/`apimgr`
  instead of inferred from `git remote`.
- Line 13: `-trimpath` missing from `LDFLAGS` in the `build` target.
- Line 29: `golang:alpine` used instead of `casjaysdev/go:latest`.
- Line 29: `docker run` missing `-e GOFLAGS=-buildvcs=false`.
- Line 32: `go build` missing `-buildvcs=false`.
- Lines 41/43: binary output name uses `macos` — must be `darwin`.
- Lines 45/48: binary output name uses `bsd` — must be `freebsd`.
- Line 89: `golang:alpine` used instead of `casjaysdev/go:latest` in the
  `test` target.
- Line 89: `docker run` missing `-e GOFLAGS=-buildvcs=false` in the `test`
  target.
- Line 90: `go test` missing `-buildvcs=false`.
- Missing a required `dev` target.

## 9. `src/main.go` CLI flag convention violations (go-lint findings, pre-existing)

- Line 42: `--help` is missing its short form `-h`.
- Line 42: `--version` is missing its short form `-v`.
- `--debug` flag is missing entirely.
- `--color` flag (values `auto`/`yes`/`no`) is missing entirely.
