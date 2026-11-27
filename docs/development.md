# Development

Read [`AI.md`](https://github.com/apimgr/countries/blob/main/AI.md) as the sole source of truth for project requirements and behavior. Use [`IDEA.md`](https://github.com/apimgr/countries/blob/main/IDEA.md) only for values or context that AI.md explicitly asks you to resolve. Existing code, audit reports, TODO files, and derivative docs never override AI.md.

## Build and verify

The Go toolchain runs in the `casjaysdev/go:latest` container with `CGO_ENABLED=0`.

```sh
make build
make test
```

`make build` creates debug server and CLI binaries for the local platform and all eight supported OS/architecture targets under `binaries/`. `make test` runs `go vet`, `staticcheck`, `govulncheck`, ShellCheck, `shfmt`, the Go tests, the 60% coverage gate, and `./tests/run_tests.sh` for the basic binary checks. Keep API, CLI, web, and documentation changes synchronized.

The debug server and CLI are built with symbols for debugging; `make release` creates stripped production binaries, a source archive, and checksums under `releases/`. `make docker` builds and verifies the host-platform container image locally without publishing it.

The GitHub license workflow runs `scripts/verify-licenses.sh` in `casjaysdev/go:latest`. It scans shipped packages under `src/`, applies only reviewed module overrides from `.github/license-overrides.txt`, and writes a CSV report and license notices to a project-scoped temporary directory. It does not change tracked files. When dependencies change, review the temporary report against `licenses.csv`, `third_party_licenses/`, and [`LICENSE.md`](https://github.com/apimgr/countries/blob/main/LICENSE.md), then update those tracked inventories separately as needed. An override requires reading the dependency's license terms and recording the license identifier and review note.

## CI/CD providers

The repository includes CI and release definitions for GitHub Actions, Gitea Actions, Forgejo Actions, GitLab CI, and Jenkins. GitHub Actions runs for this GitHub repository; the other definitions support self-managed mirrors. Go checks use `casjaysdev/go:latest` with CGO disabled. Jenkins requires agents labeled `amd64` and `arm64`, Docker, and Docker Buildx; its registry token is configured in Jenkins credentials and is not stored in the repository.

## Build the documentation

MkDocs and its plugins are listed in `docs/requirements.txt`. Keep the Python environment and generated site outside the repository:

```sh
mkdir -p "${TMPDIR:-/tmp}/apimgr"
DOCS_TMP=$(mktemp -d "${TMPDIR:-/tmp}/apimgr/countries-XXXXXX")
python3 -m venv "$DOCS_TMP/venv"
"$DOCS_TMP/venv/bin/python" -m pip install -r docs/requirements.txt
"$DOCS_TMP/venv/bin/python" -m mkdocs build --config-file mkdocs.yml --site-dir "$DOCS_TMP/site"
```

Remove the temporary directory after previewing the generated site.

## Binary integration testing

The basic Docker binary tier runs automatically at the end of `make test` through `./tests/run_tests.sh`; it requires Docker and runs `tests/docker.sh`. It does not select or fall back to Incus. Run `./tests/incus.sh` separately for the full systemd and OpenRC integration suite.

The Docker suite requires Docker, Make, GNU `timeout`, and locally available `alpine:latest` and `casjaysdev/go:latest` images. It builds the local-platform debug server and CLI through `make build` in the required Go container, then runs the binaries in an isolated Alpine test container. It also builds and inspects the production Docker image, verifies the required runtime commands, and checks the running image's health and overlay behavior. The binary suite checks help, version and status, SSR server pages, REST and GraphQL endpoints, discovery files and text responses, error handling, and CLI list, lookup, search, coordinate, nearby, and statistics commands. Tor and I2P bootstrap checks require outbound network access; unavailable networking is reported as `SKIP`, and `REQUIRE_OVERLAY=1` makes a missing bootstrap fail. Artifacts are placed in the project-scoped OS temp directory, removed after success, and preserved with logs on failure.

## Browser E2E testing

Run `./tests/e2e.sh` on demand for browser-level coverage of the server interface. This standalone suite is not part of `make test` or the commit gate. It requires Docker, network access for Go module downloads, and the locally available `casjaysdev/go:latest` and `chromedp/headless-shell:latest` images; the script does not pull missing images. It downloads modules before building and running the tagged Go suite in containers on an internal Docker network. Temporary artifacts are stored under the project-scoped OS temp directory and removed after success; artifacts are preserved and their location printed when the suite fails.

## Contributions

See [CONTRIBUTING.md](https://github.com/apimgr/countries/blob/main/.github/CONTRIBUTING.md). Report source-code vulnerabilities through [private vulnerability reporting](https://github.com/apimgr/countries/security/advisories/new), not public issues.
