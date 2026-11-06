# Security policy

## Supported versions

Security fixes are made for the latest stable release. Users should update to the latest stable release before reporting an issue.

## Reporting a vulnerability

Use [GitHub private vulnerability reporting](https://github.com/apimgr/countries/security/advisories/new) as the primary channel. Do not open a public issue or pull request containing vulnerability details. GitHub’s private advisory flow allows maintainers to investigate and coordinate a fix before public disclosure.

The service also publishes its runtime reporting policy at `/server/security` and `/.well-known/security.txt`. Those endpoints point source-code vulnerability reports to GitHub private vulnerability reporting.

If private vulnerability reporting is unavailable on a mirror, report the issue against the [GitHub origin repository](https://github.com/apimgr/countries).

Please include the affected version or commit, deployment context, impact, and steps to reproduce. Redact credentials, tokens, personal data, and private keys. The maintainers will acknowledge reports through GitHub and coordinate disclosure after a fix is available.

The service’s runtime security report endpoint is not a source-code vulnerability reporting channel.
