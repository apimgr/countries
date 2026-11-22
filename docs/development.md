# Development

Read [`AI.md`](https://github.com/apimgr/countries/blob/main/AI.md) for the implementation specification and [`IDEA.md`](https://github.com/apimgr/countries/blob/main/IDEA.md) for product behavior. AI.md takes precedence over existing code when they disagree.

## Build and verify

The Go toolchain runs in the `casjaysdev/go:latest` container with `CGO_ENABLED=0`.

```sh
make dev
make test
```

`make test` runs `go vet`, `govulncheck`, the Go tests, and the 60% coverage gate. Keep API, CLI, web, and documentation changes synchronized.

## Browser E2E testing

Run `./tests/e2e.sh` on demand for browser-level coverage of the server interface. This standalone suite is not part of `make test` or the commit gate. It requires Docker and the locally available `casjaysdev/go:latest` and `chromedp/headless-shell:latest` images; the script does not pull missing images. It builds and runs the tagged Go suite in containers on an internal Docker network. Temporary artifacts are stored under the project-scoped OS temp directory and removed after success; artifacts are preserved and their location printed when the suite fails.

## Contributions

See [CONTRIBUTING.md](https://github.com/apimgr/countries/blob/main/.github/CONTRIBUTING.md). Report source-code vulnerabilities through [private vulnerability reporting](https://github.com/apimgr/countries/security/advisories/new), not public issues.
