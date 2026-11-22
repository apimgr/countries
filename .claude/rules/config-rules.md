# Configuration Rules (PART 5, 6, 12)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never store runtime state or operator-editable configuration in the database; never store credential values in public config or expose them in logs/errors/API output.
- Never use `strconv.ParseBool`; use the project truthy/falsy boolean parser.
- Never let malformed or invalid config prevent normal server startup; invalid values reset to documented defaults with one warning, while an unreadable drop-in retains its last good copy or defaults.
- Never write inline YAML comments or put feature-specific settings in the wrong config area.
- Never treat `server.port`, `server.address`, or `server.daemonize` as hot-reloadable after the privileged bind.

## CRITICAL - ALWAYS DO
- Keep node settings in `server.yml`, area settings in byte-wise ordered `yaml.d/*.yml`, and credentials in `secrets.yml`; merge defaults → `server.yml` → drop-ins → `secrets.yml`, then apply the specified CLI/environment overrides.
- Set config files to `0600` and `yaml.d/` to `0700`; validate and normalize every path before filesystem access.
- Watch and re-merge config files as specified; apply safe settings live and clearly require restart for listeners, database, and other restart-only settings.
- Resolve runtime mode as `--mode` > `MODE` > `server.mode` > production; resolve debug separately using explicit CLI/environment/config precedence.
- Keep the operator-editable config file-only with no admin UI; use documented environment variables for overrides and first-run seeding only where specified.
- Use `config.ParseBool()` or `config.IsTruthy()` for every boolean input.
- Document every setting, default, valid values, secret handling, reload behavior, and runtime consumer.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Configuration source | The merged config files are authoritative; the database stores application state, never operator configuration. | PART 5, 12 |
| Config merge order | Defaults, `server.yml`, sorted drop-ins, then `secrets.yml`; later files replace scalar/list values and merge maps. | PART 5 |
| Invalid values | Reset invalid values to their defaults and warn once; configuration errors alone do not stop startup. | PART 5, 12 |
| Mode precedence | CLI flag, environment, config, then production default. | PART 6 |

## TERMINOLOGY
| Term | Meaning |
|---|---|
| Area file | A feature-scoped YAML file in `yaml.d/`, merged by byte-wise filename order. |
| Restart-only setting | A value that cannot safely take effect without restarting listeners/process state. |
| Init-only environment value | A setting read only while seeding the first configuration. |

## QUICK REFERENCE
Use the current config schema and validation path; preserve permissions, merge precedence, reload behavior, and secret boundaries; keep docs and UI copy synchronized.

---
For complete details, see `AI.md` PARTS 5, 6, and 12.
