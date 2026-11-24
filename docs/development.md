# Development

Read [`AI.md`](https://github.com/apimgr/countries/blob/main/AI.md) as the sole source of truth for project requirements and behavior. Use [`IDEA.md`](https://github.com/apimgr/countries/blob/main/IDEA.md) only for values or context that AI.md explicitly asks you to resolve. Existing code, audit reports, TODO files, and derivative docs never override AI.md.

## Build and verify

The Go toolchain runs in the `casjaysdev/go:latest` container with `CGO_ENABLED=0`.

```sh
make dev
make test
```

`make test` runs `go vet`, `govulncheck`, the Go tests, and the 60% coverage gate. Keep API, CLI, web, and documentation changes synchronized.

The standard `make dev`, `make local`, and `make build` outputs include a static server and a static CLI/TUI binary. To also build the optional GUI client, use `BUILD_GUI=1 make dev`, `BUILD_GUI=1 make local`, or `BUILD_GUI=1 make build`. GUI binaries are separate `*-cli-gui*` artifacts for Linux, macOS, and Windows and require the dynamic loader used by the Go build container.

The GitHub license workflow runs `scripts/verify-licenses.sh` in `casjaysdev/go:latest`. When dependencies change, run that script in the same container to refresh `licenses.csv` and `third_party_licenses/`, then review the results and update [`LICENSE.md`](https://github.com/apimgr/countries/blob/main/LICENSE.md) as needed. The script requires the repository to be mounted writable because it updates those tracked inventories.

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

After the toolchain gate, run `./tests/run_tests.sh` for manual binary validation. This basic tier requires Docker and runs `tests/docker.sh`; it does not select or fall back to Incus. Run `./tests/incus.sh` separately for the full systemd and OpenRC integration suite. The `./tests/docker.sh` entry point also runs the Docker suite directly.

The Docker suite requires Docker, Make, GNU `timeout`, and locally available `alpine:latest` and `casjaysdev/go:latest` images. It builds the server and CLI through `make dev` in the required Go container, then runs the binaries in an isolated Alpine test container. It also builds and inspects the production Docker image, verifies the required runtime commands, and checks the running image's health and overlay behavior. The binary suite checks help, version and status, SSR server pages, REST and GraphQL endpoints, discovery files and text responses, error handling, and CLI list, lookup, search, coordinate, nearby, and statistics commands. Tor and I2P bootstrap checks require outbound network access; unavailable networking is reported as `SKIP`, and `REQUIRE_OVERLAY=1` makes a missing bootstrap fail. Artifacts are placed in the project-scoped OS temp directory, removed after success, and preserved with logs on failure. These scripts provide phase-two binary integration coverage; they do not replace `make test` or the separate browser suite below.

## Browser E2E testing

Run `./tests/e2e.sh` on demand for browser-level coverage of the server interface. This standalone suite is not part of `make test` or the commit gate. It requires Docker, network access for Go module downloads, and the locally available `casjaysdev/go:latest` and `chromedp/headless-shell:latest` images; the script does not pull missing images. It downloads modules before building and running the tagged Go suite in containers on an internal Docker network. Temporary artifacts are stored under the project-scoped OS temp directory and removed after success; artifacts are preserved and their location printed when the suite fails.

## Contributions

See [CONTRIBUTING.md](https://github.com/apimgr/countries/blob/main/.github/CONTRIBUTING.md). Report source-code vulnerabilities through [private vulnerability reporting](https://github.com/apimgr/countries/security/advisories/new), not public issues.
