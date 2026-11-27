# Contributing

## Development

The project is implemented in Go. Go toolchain commands run in the supported `casjaysdev/go:latest` container with `CGO_ENABLED=0`.

- `make build` builds debug server and CLI binaries for the local platform and all eight supported targets.
- `make test` runs lint, vulnerability scanning, Go tests with coverage, and the basic Docker binary suite.
- `./tests/incus.sh` runs the separate full systemd/OpenRC integration suite.
- Update or add tests and user documentation with behavior changes.

Before contributing, read [`AI.md`](../AI.md) and [`IDEA.md`](../IDEA.md). AI.md is the implementation specification; IDEA.md defines intended product behavior.

## Pull requests

Use a focused branch and pull request. Explain the user-visible behavior, link related issues, and include the exact verification commands and results. Keep REST, GraphQL, OpenAPI, CLI, and web documentation synchronized. Do not include generated binaries, runtime data, secrets, or private keys.

## Security issues

Do not report vulnerabilities in public issues. Use [GitHub private vulnerability reporting](https://github.com/apimgr/countries/security/advisories/new). See [SECURITY.md](SECURITY.md) for the disclosure process.
