# Contributing

## Development

The project is implemented in Go. Go toolchain commands run in the supported `casjaysdev/go:latest` container with `CGO_ENABLED=0`.

- `make dev` builds the server and CLI into a temporary directory.
- `make test` runs vet, vulnerability scanning, the Go tests, and the coverage gate.
- Update or add tests and user documentation with behavior changes.

Before contributing, read [`AI.md`](../AI.md) and [`IDEA.md`](../IDEA.md). AI.md is the implementation specification; IDEA.md defines intended product behavior.

## Pull requests

Use a focused branch and pull request. Explain the user-visible behavior, link related issues, and include the exact verification commands and results. Keep REST, GraphQL, OpenAPI, CLI, and web documentation synchronized. Do not include generated binaries, runtime data, secrets, or private keys.

## Security issues

Do not report vulnerabilities in public issues. Use [GitHub private vulnerability reporting](https://github.com/apimgr/countries/security/advisories/new). See [SECURITY.md](SECURITY.md) for the disclosure process.
