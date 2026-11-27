# Configuration Rules (PART 5, 6, 12)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never use inline comments in YAML; comments go above the setting.
- Never accept unvalidated paths or identifiers derived from input.
- Never let invalid configuration crash startup or disable security checks.

## CRITICAL - ALWAYS DO
- Use `server.yml` as the canonical configuration file and follow the documented `yaml.d/` and `secrets.yml` split.
- Normalize and validate paths before use, including containment checks for user-supplied paths.
- Preserve middleware ordering and the original TCP peer when resolving trusted proxy addresses.
- Warn and use the documented default for invalid settings.

## KEY DECISIONS
| Question | Answer | Spec Reference |
|----------|--------|----------------|
| What is the main config filename? | `server.yml` | PART 5 |
| What happens to invalid config values? | Warn and replace with the specified default | PART 12 |

## TERMINOLOGY
| Term | Meaning |
|------|---------|
| Safe path | A validated path that stays within its configured bounds |

## QUICK REFERENCE
Read the setting's schema, defaults, file-only/hot-reload behavior, and validation contract before editing configuration.

---
For complete details, see AI.md PART 5, PART 6, and PART 12.
