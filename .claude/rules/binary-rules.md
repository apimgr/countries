# Binary Rules (PART 7, 8, 32)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never require CGO, an external interpreter, or an external data file for core operation; embed required static assets and data.
- Never let `--help`, `--version`, shell completion, or other immediate-exit commands require privilege, configuration, or network access.
- Never hand-roll primary command-line parsing or add unlisted server top-level flags.
- Never put CLI tokens in URLs or logs, or create CLI configuration with broader-than-user permissions.
- Never omit the companion client or make GUI/TUI selection flags part of the CLI interface.

## CRITICAL - ALWAYS DO
- Build static binaries with `CGO_ENABLED=0`; build server and client for all eight OS/architecture targets in AI.md.
- Keep the server flag/subcommand contract, help output, command completions, environment/config precedence, and exit codes aligned with PART 8.
- Detect CLI display mode automatically: GUI when supported and available, otherwise TUI for an interactive terminal, plain CLI for non-interactive use; explicit config may select a mode.
- Launch the client TUI with no command when interactive; help/version and explicit commands exit or execute without launching it.
- Implement the PART 32 client command surface, setup/config behavior, token-safe API client, per-command update checks, and corresponding TUI and GUI views for every server-facing CLI command.
- Store client config, data, cache, and logs in the invoking user's paths; protect `cli.yml` containing tokens with `0600` on Unix and user-only ACLs on Windows.
- Honor `NO_COLOR`, `TERM=dumb`, terminal size, remote-session detection, keyboard accessibility, themes, and the specified User-Agent.
- Build and test Go code only in `casjaysdev/go:latest` with `CGO_ENABLED=0`.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Binary model | Self-contained static Go server and companion client binaries. | PART 7 |
| Server flag parsing | Standard `flag` package; server is single-command. | PART 8 |
| Client interaction | Auto-detected CLI/TUI/GUI; terminal mode is not selected by a UI flag. | PART 32 |
| Client authentication | Public reads need no token; owner tokens use `Authorization: Bearer` for owned operations. | PART 32 |

## TERMINOLOGY
| Term | Meaning |
|---|---|
| Immediate-exit flag | A help, version, completion, or command-help option handled before server startup. |
| Companion client | The project CLI binary that accesses the server API and provides the required interactive views. |

## QUICK REFERENCE
Check server and client naming, commands, modes, config paths/permissions, completions, eight-platform builds, accessibility, and documentation against the current PART 7, 8, and 32.

---
For complete details, see `AI.md` PARTS 7, 8, and 32.
