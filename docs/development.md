# Development

Read [`AI.md`](https://github.com/apimgr/countries/blob/main/AI.md) for the implementation specification and [`IDEA.md`](https://github.com/apimgr/countries/blob/main/IDEA.md) for product behavior. AI.md takes precedence over existing code when they disagree.

## Build and verify

The Go toolchain runs in the `casjaysdev/go:latest` container with `CGO_ENABLED=0`.

```sh
make dev
make test
```

`make test` runs `go vet`, `govulncheck`, the Go tests, and the 60% coverage gate. Keep API, CLI, web, and documentation changes synchronized.

## Contributions

See [CONTRIBUTING.md](https://github.com/apimgr/countries/blob/main/.github/CONTRIBUTING.md). Report source-code vulnerabilities through [private vulnerability reporting](https://github.com/apimgr/countries/security/advisories/new), not public issues.
