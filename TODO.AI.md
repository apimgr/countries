# TODO.AI.md

Remaining work required to bring Countries into full agreement with `IDEA.md`
and the shared `AI.md` specification. Items remain open until implemented
and verified by the primary session.

## Data and attribution

- [ ] Identify and approve a redistributable authoritative dataset covering the IDEA.md country scope. The current embedded records only support common name, alpha-2 code, capital, timezones, and representative coordinates. No source/license attribution is present, so do not invent official names, regions, currencies, languages, population, area, flags, borders, or TLD data.
- [ ] Record the chosen dataset version, source URL, license, attribution, and update date; retain field-level provenance and document any country-count differences from IDEA.md's 249-record target.

## Product and shared-spec gaps

- [ ] Extend the embedded model and REST/GraphQL/search surfaces to all IDEA.md fields and filters after the data source is approved.
- [ ] Complete the Part 32 CLI requirements: interactive TUI, optional native GUI, automatic display detection, language and shell completion support, full server-function coverage, secure config creation, and update/discovery support. Current client is command-only and supports the existing subset.
- [ ] Finish the Part 27 workflow audit and verify release artifacts against the server/CLI build outputs. The six-target Makefile now builds both binaries across all eight platforms and enforces the shared coverage gate.
- [ ] Complete server-wide AI.md requirements that are not yet implemented, including API rate limiting, request IDs, full OpenAPI route/schema coverage generated from route definitions, remaining standard pages and accessibility/frontend requirements, plus the required Tor integration when an available provider is detected.
- [ ] Remove or explicitly document legacy unversioned/raw-data routes where AI.md Part 14 requires them removed; preserve only explicitly required compatibility aliases.

## CI and quality follow-up

- [ ] Raise the full-suite Go coverage to the required 60% gate. The latest measured result before the expanded GraphQL, Swagger, and CLI tests was 47.4%; rerun the coverage target after those tests and keep adding focused tests until it passes.
- [ ] Re-run the repository's CI checks after implementation to identify any remaining compiler, vet, staticcheck, vulnerability-scan, workflow, or container-image failures.
