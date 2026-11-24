# CLI reference

The `countries-cli` client queries a Countries server. The default build is fully static and includes the TUI but not the GUI. An optional GUI build opens the native interface when a local display is available; otherwise an interactive terminal uses the TUI. SSH and Mosh sessions always use the terminal UI. In headless or scripted use, provide a command and a server URL.

```text
countries-cli [flags] COMMAND [arguments]
countries-cli [flags] QUERY
```

## Commands

| Command | Purpose |
|---|---|
| `list` | List country records. |
| `get CODE` | Read one record by alpha-2, alpha-3, or numeric country code. |
| `search QUERY` | Search country names and supported country fields. |
| `nearest LAT LON` | Find the nearest representative coordinate. |
| `nearby LAT LON RADIUS_KM` | Find country centroids within a radius. |
| `stats` | Read dataset statistics. |
| `request METHOD /api/v1/PATH [JSON_BODY]` | Send a supported native API request to the configured server. Absolute URLs, redirects, and machine-only/server-binary endpoints are rejected. |

The interactive menu and GUI expose these same seven commands in this order. The `request` command accepts a versioned `/api/v1/` path and an optional JSON body; it uses the configured server origin and does not follow redirects. Any positional arguments that do not start with a recognized command are treated as a search query. For example, `countries-cli --server https://countries.example "South Korea"` is equivalent to `countries-cli --server https://countries.example search "South Korea"`.

## Flags

| Flag | Purpose |
|---|---|
| `--server URL` | Server base URL. |
| `--token TOKEN` | Optional bearer token for servers that require one. |
| `--token-file PATH` | Read a bearer token from a file. Used when `--token` is not provided. |
| `--config PATH` | Select a config profile or YAML file. |
| `--lang CODE` | Select output language. |
| `--output table\|plain\|json` | Choose output format. |
| `--color auto\|yes\|no` | Choose color behavior. |
| `--update check` | Check whether the configured server offers a newer CLI. |
| `--update yes` | Permit automatic installation in non-interactive mode when `update.auto: true`; interactive runs prompt before installing. |
| `--debug` | Write request diagnostics to stderr. |
| `--help`, `-h` | Show help and exit. |
| `--version`, `-v` | Show build version and exit. |
| `--shell completions [SHELL]` | Print shell completion code for bash, zsh, fish, sh, dash, ksh, powershell, or pwsh. If omitted, the shell is inferred from `$SHELL` (bash is the fallback). |
| `--shell init [SHELL]` | Print the command that loads completions in the selected shell. |
| `--shell --help` | Show shell integration help. |

Completion setup examples:

```sh
eval "$(countries-cli --shell init bash)"
countries-cli --shell completions zsh > ~/.zsh/completions/_countries-cli
```

## Interactive interfaces

On first run, the setup screen asks for a server URL, checks the health endpoint, accepts an optional token, and saves the connection settings. The TUI and GUI each provide one screen for every command above. TUI controls include Up/Down or `j`/`k` to move, `/` to filter, Enter to select or submit, Tab and Shift+Tab to move between fields, `?` for help, Esc to return, and `q` or Ctrl+C to exit. Results remain visible on the result screen; TUI errors appear in its status area and GUI errors open a dismissible dialog that shows the server message.

The GUI is implemented with the pure-Go `gogpu/ui` and `gogpu/gogpu` toolkits and follows the detected operating system light or dark theme. It is an optional, separately named `countries-cli-gui` artifact built with `BUILD_GUI=1`; Linux, macOS, and Windows GUI artifacts use the `-cli-gui-{os}-{arch}` suffix. These binaries require the dynamic loader from their Go build toolchain. BSD builds have no GUI support and use the TUI when a terminal is available. A forced `display.mode: gui` requires a local display and GUI-enabled binary. Forced `display.mode: tui` requires a terminal.

## Server and configuration

The server URL is selected from `--server`, `COUNTRIES_SERVER_PRIMARY`, `server.primary` in `cli.yml`, then the compiled official site when defined. The default config directory is the invoking user's OS config directory under `apimgr/countries/`. Config files are written with owner-only permissions.

The client reads `server.token` from its config file unless overridden with `--token` or `--token-file`. Tokens are sent only in the `Authorization: Bearer` header. Output language precedence is `--lang`, `lang` in `cli.yml`, `LANG`, then English. Supported language codes are `en`, `es`, `fr`, `de`, `it`, `ar`, `zh`, and `ja`. The HTTP client timeout is 15 seconds, and response bodies larger than 16 MiB are rejected. `--version` and `--help` print the invoked binary's filename, so those labels remain accurate if the binary is renamed.

`display.mode` in `cli.yml` accepts `auto` (default), `gui`, or `tui`. With no command, auto mode chooses a local GUI when the optional GUI build is available, then an interactive TUI; it reports an error when neither is available. Any explicit command runs in CLI mode regardless of display settings. Help, version, and shell integration options exit without opening an interface.

## Updates

The CLI checks `/api/autodiscover` on startup when it has a server URL. An available interactive update prompts before download. A non-interactive update is installed only when both `update.auto: true` and `--update yes` are set. Downloads require HTTPS, are checked against the published SHA-256, and replace the current executable using a platform-specific staged replacement. If the server sets `cli_min_version` above the installed version, the client refuses API requests and instructs the user to run `countries-cli --update yes`.

See the [installation guide](installation.md) for release assets and the [API reference](api.md) for route and response details.

## Server maintenance commands

The `countries` server binary has separate maintenance commands. These commands do not belong to the `countries-cli` client:

```sh
countries --maintenance backup [filename] [--include-ssl] [--include-data]
countries --maintenance restore BACKUP_FILE
countries --maintenance update [check|yes|branch {stable|beta|devel}]
countries --maintenance mode
countries --maintenance setup
countries --maintenance pgp generate
countries --maintenance pgp rotate
countries --maintenance pgp publish
countries --maintenance pgp export public [path]
countries --maintenance pgp export private PATH
countries --maintenance pgp import FILE
countries --maintenance pgp delete
countries --maintenance secret rotate installation_secret
countries --maintenance secret rotate encryption_key
countries --maintenance token list
countries --maintenance token revoke PREFIX [--yes]
countries --maintenance data export --owner PREFIX [--output FILE]
countries --maintenance data delete --owner PREFIX [--yes]
countries --maintenance security list [--status STATUS]
countries --maintenance security show TRACKING_ID
countries --maintenance security update TRACKING_ID [--status STATUS] [--comment TEXT] [--disclosure-date YYYY-MM-DD] [--severity LEVEL]
countries --maintenance compliance report
```

`mode` prints the current application mode. `setup` loads the selected server configuration and prints its setup summary.

Without a filename, `backup` creates a timestamped archive in the default backup directory. It always includes `server.yml` and the server database; configuration drop-ins, secrets, assets, and custom templates are included when present. SSL certificate files and the full data directory are optional. If `server.backup.encryption_password` is configured in `secrets.yml`, the archive is encrypted. Compliance mode refuses to create a backup without that secret.

The archive is verified before it is installed. Restore verifies the selected archive and its incremental base, applies configured size and expansion limits, stages the files, and then replaces the configured files. Encrypted restores prompt for the backup password in an interactive terminal; passwords are never accepted as command-line arguments. Restore is destructive and should be run by the deployment operator with the service stopped.

Scheduled backups run daily at 02:00. Daily full backups and the daily incremental are enabled by default; hourly incremental backups are disabled. Retention and disk-pressure checks use the `server.backup.retention.*` settings. See [configuration](configuration.md#backup-and-restore).

PGP key management is operator-only and requires the operator token or root. Private-key export, import, and deletion also require an interactive confirmation. Generate stores the encrypted private key and public key under `security/` in the configuration directory and publishes the public key to configured keyservers. Public export writes to standard output unless a path is supplied. Private export requires a destination path, an operator-supplied reason, writes mode `0600`, and is limited to one export per hour per operator. Import validates the key and warns before accepting a key whose identity differs from the configured security identity. Delete warns that PGP-encrypted security reports will become unreadable. See [security](security.md#gpg-keypair-management) for the full workflow.

All maintenance commands require the configured operator token or root. Secret rotation and token revocation also require typed confirmation. Secret rotation re-encrypts dependent values, stores the previous key for its grace period, resumes interrupted work before server startup, and retires expired keys during daily security cleanup. Rotate only `installation_secret` or `encryption_key`.

The `token list` command shows token metadata and prefixes, never token values. Revoking a linked token requires typing its prefix; revoking a primary token deletes that owner's tokens and resources and requires typing the owner ID. `--yes` skips that typed confirmation for scripted use.

Data subject commands use the token prefix to identify an owner. Export writes a mode `0600` JSON file (or stdout with `--output -`) containing owner metadata and token metadata, never token values. Delete requires typing the owner ID unless `--yes` is supplied. Compliance reporting prints configured standards, retention settings, aggregate counts, recent compliance events, last backup time, and audit-log integrity without personal data.

Security report listing and updates never print the encrypted report body. `show` displays metadata and the researcher's comment; `update` can advance status, update severity, set a disclosure date, or add a researcher-visible comment. A successful update is recorded in the security and audit logs, and a status notice is sent when email is configured and contact data is available. See [security and privacy](security.md#operator-maintenance) for the data-handling details.
