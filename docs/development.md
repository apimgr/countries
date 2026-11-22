# Development

Read [`AI.md`](https://github.com/apimgr/countries/blob/main/AI.md) as the sole source of truth for project requirements and behavior. Use [`IDEA.md`](https://github.com/apimgr/countries/blob/main/IDEA.md) only for values or context that AI.md explicitly asks you to resolve. Existing code, audit reports, TODO files, and derivative docs never override AI.md.

## Build and verify

The Go toolchain runs in the `casjaysdev/go:latest` container with `CGO_ENABLED=0`.

```sh
make dev
make test
```

`make test` runs `go vet`, `govulncheck`, the Go tests, and the 60% coverage gate. Keep API, CLI, web, and documentation changes synchronized.

## Binary integration testing

After the toolchain gate, run `./tests/run_tests.sh` for manual binary validation. It accepts `--engine auto`, `--engine incus`, or `--engine docker`; `auto` prefers a usable Incus runtime and falls back to a usable Docker runtime. Set `TEST_ENGINE=incus` or `TEST_ENGINE=docker` to choose the default without a command-line option. The `./tests/docker.sh` entry point runs the Docker suite directly.

The Docker suite requires Docker, Make, GNU `timeout`, and a locally available `alpine:latest` image. It builds the server and CLI through `make dev` in the required Go container, then runs them in an isolated Alpine container. It checks binary help, version and status, SSR server pages, REST and GraphQL endpoints, discovery files and text responses, error handling, and CLI list, lookup, search, coordinate, nearby, and statistics commands. Artifacts are placed in the project-scoped OS temp directory, removed after success, and preserved with the container log on failure. These scripts provide phase-two binary integration coverage; they do not replace `make test` or the separate browser suite below.

## Browser E2E testing

Run `./tests/e2e.sh` on demand for browser-level coverage of the server interface. This standalone suite is not part of `make test` or the commit gate. It requires Docker and the locally available `casjaysdev/go:latest` and `chromedp/headless-shell:latest` images; the script does not pull missing images. It builds and runs the tagged Go suite in containers on an internal Docker network. Temporary artifacts are stored under the project-scoped OS temp directory and removed after success; artifacts are preserved and their location printed when the suite fails.

## Contributions

See [CONTRIBUTING.md](https://github.com/apimgr/countries/blob/main/.github/CONTRIBUTING.md). Report source-code vulnerabilities through [private vulnerability reporting](https://github.com/apimgr/countries/security/advisories/new), not public issues.
