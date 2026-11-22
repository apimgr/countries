# Project Rules (PART 2, 3, 4)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never edit the read-only AI.md template, invent project values, or copy third-party code/data without checking license and attribution requirements.
- Never use GPL, AGPL, or LGPL dependencies; never omit required third-party and embedded-data license attribution.
- Never store runtime state, secrets, test data, or generated binaries in source directories.
- Never use one OS's native runtime path rules on another OS or use container-only `/config` and `/data` paths on native installations.
- Never use CGO-dependent Go libraries or `github.com/mattn/go-sqlite3`.

## CRITICAL - ALWAYS DO
- Keep the root `LICENSE.md` as MIT and include the required third-party dependency/data provenance and attribution inventory.
- Keep project source under `src/`, repository integration scripts under `tests/`, static assets embedded, and `docs/` limited to ReadTheDocs documentation.
- Keep config, data, cache, logs, and database directories separate and derive platform-specific paths from the stable internal org/name.
- Support Linux, macOS, FreeBSD, Windows, amd64, and arm64 as enumerated in the spec; use `modernc.org/sqlite` for embedded SQLite.
- Keep downloaded security/GeoIP data in runtime security data directories, outside the source tree and binary.
- Include required Docker ignore/config files and keep mutable runtime volumes external to images.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Application license | MIT, with all required dependency/data notices retained. | PART 2 |
| Go SQLite driver | Pure-Go `modernc.org/sqlite`; CGO sqlite is prohibited. | PART 3 |
| Runtime path identity | Use stable `{internal_org}/{internal_name}` values and OS-specific directories. | PARTS 3, 4 |
| Security data | Download and maintain externally at runtime; never embed. | PART 2, 7 |

## TERMINOLOGY
| Term | Meaning |
|---|---|
| Embedded asset | Read-only static project file compiled into the binary. |
| Runtime data | Mutable application state kept outside the repository/source tree. |
| Internal name | Stable on-disk identifier used for paths and service identity. |

## QUICK REFERENCE
Preserve license/provenance, platform paths, pure-Go portability, clean source/runtime separation, and the required repository layout.

---
For complete details, see `AI.md` PARTS 2, 3, and 4.
