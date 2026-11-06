# Remaining AI.md alignment work

AI.md is authoritative; IDEA.md defines country-specific behavior. Items stay open until implementation, documentation, and required verification agree.

## Server and shared foundations

- [ ] Complete config coverage and validation for all server settings, including API version, database backends, cache, localization, and update policy.
- [ ] Complete database-backed state and migrations for config, rate limits, audit events, scheduler jobs, backups, owners, and API tokens where the product's read-only model requires them.
- [ ] Complete rate limiting, trusted-proxy handling, security headers, access logs, request IDs, health/status output, and error response requirements from AI.md.
- [ ] Complete update-channel selection, release freshness/defer rules, scheduled updates, checksums, atomic replacement, restart, and recovery on every supported platform.
- [ ] Complete portable service lifecycle behavior and verify every supported service manager and operating system.
- [ ] Finish API path/version configuration and all required discovery, binary, status, and metadata endpoints; keep REST, OpenAPI, GraphQL, and docs in sync.
- [ ] Compare every country field, query/filter, web route, GraphQL field, and dataset-generation/provenance requirement against IDEA.md and AI.md.

## Client

- [ ] Implement the required CLI flags and commands, including `--lang`, `--token`, config selection, completions, and accurate runtime binary naming.
- [ ] Replace the line-oriented prompt with the required auto-detected Bubble Tea TUI and first-run wizard; implement the supported native GUI mode with `gogpu/ui` and its platform capability checks.
- [ ] Implement client display-mode detection, config override, remote-session behavior, theme/color handling, and terminal-size responsiveness.
- [ ] Implement server autodiscovery and per-invocation CLI update checks, download authorization behavior, checksum verification, atomic replacement, re-exec, and minimum-version enforcement.
- [ ] Ensure every CLI command and output required by the project works with the public, read-only country API.

## Internationalization and web

- [ ] Translate every user-facing server, web, API error, OpenAPI, GraphQL, CLI, and legal string, with complete parity across `en`, `es`, `zh`, `fr`, `ar`, `de`, and `ja`.
- [ ] Add the required language selection to both binaries and config, validate unsupported-language fallback, and complete plural/RTL/accessibility behavior.
- [ ] Finish server-rendered, no-JavaScript paths and accessible responsive behavior for every page and preference form.
- [ ] Complete cache policy, static asset versioning, PWA behavior, SEO/discovery, and all security/reporting endpoints specified by AI.md.

## Build, deployment, and documentation

- [ ] Finish Makefile targets and release outputs exactly as specified, including reproducible build metadata and all supported OS/architecture pairs for both binaries.
- [ ] Complete production/development/test container behavior, including a working configured cache integration, OCI metadata, image build and validation workflows, and the required test scripts.
- [ ] Complete CI, security scanning, release/beta/daily workflows, repository support files, licenses, and contribution guidance against AI.md and provider conventions.
- [ ] Synchronize README.md, docs/, OpenAPI, GraphQL, CLI help, config examples, IDEA.md, and project rules with verified behavior.
- [ ] Run all required container-based tests, linters, cross-platform builds, documentation checks, workflow checks, and container checks; resolve all failures.
- [ ] Reconcile this backlog and AUDIT.AI.md with the final verified state, then follow the gitcommit workflow and check the pushed CI run.
