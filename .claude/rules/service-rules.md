# Service Rules (PARTS 23, 24)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never assume systemd, root access, or a particular init system is present.
- Never shell-interpolate user input into service commands.
- Never install an OpenRC script as SysVinit or treat an application container as a service host.
- Never run a service permanently as root when the service manager supports a dedicated account.
- Never remove user data as part of uninstall.

## CRITICAL - ALWAYS DO
- Detect OS, init system, privilege context, and user-service fallback at install time.
- Keep service install/uninstall explicit, reversible, idempotent, and least-privileged.
- Use the AI.md platform-specific account, privilege-drop, service template, writable-path, and lifecycle requirements.
- Support supported system and user service managers; report unsupported operations with accurate exit codes.
- Preserve config, data, database, logs, cache, and backups on disable/uninstall.

## KEY DECISIONS
| Question | Answer | Spec Reference |
|---|---|---|
| Service managers | systemd, OpenRC, SysVinit, runit, s6, FreeBSD rc.d, macOS launchd, Windows Service, with supported user-level fallback | PARTS 23-24 |
| Linux detection order | systemd, OpenRC, runit, s6, SysVinit | PART 23 |
| Privilege | Dedicated service account; privileged startup then drop privileges where needed | PARTS 23-24 |
| Uninstall | Remove service integration only; retain user data and binary | PART 23 |

## QUICK REFERENCE
Before changing service behavior, reread the applicable platform section and test only inside the required container/VM. Never infer support from a service file alone.

---
For complete details, see `AI.md` PARTS 23 and 24.
