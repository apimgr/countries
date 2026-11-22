# Docker Rules (PART 26)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never put Dockerfiles or Compose files at repository root; use `docker/`.
- Never run production or development Compose stacks as an AI agent; automated tests use only `docker-compose.test.yml` through `tests/` scripts.
- Never mount project paths as runtime data or create runtime files in the source tree.
- Never override the required entrypoint/CMD contract, bake secrets or mutable data into images, or add a Dockerfile `USER` directive.
- Never publish an image whose final stage lacks `bash`, `curl`, `tor`, `i2pd`, `tini`, `tzdata`, and CA certificates.

## CRITICAL - ALWAYS DO
- Use a multi-stage Go builder (`casjaysdev/go:latest`) and minimal Alpine runtime, plus `docker/rootfs/` for build-time overlays.
- Keep production, devel, and test Compose semantics, image tags, networks, labels, health checks, and mounts aligned with AI.md.
- Start the container with `docker/rootfs/usr/local/bin/entrypoint.sh`; the binary owns initialization, account creation, permissions, and privilege drop.
- Keep production MODE/DEBUG unset; dev and test set development mode and debug explicitly.
- Validate required binaries and Tor health after image builds.

## KEY DECISIONS
| Question | Answer | Spec Reference |
|---|---|---|
| Dockerfile location | `docker/Dockerfile` and `docker/Dockerfile.dev` | PART 26 |
| Runtime state | Explicit external config/data volumes; no source-tree volumes | PART 26 |
| Runtime user | Binary performs privilege drop; no Dockerfile user | PART 26 |
| AI test stack | `docker/docker-compose.test.yml` via project test scripts only | PART 28 |

## QUICK REFERENCE
Inspect every image and Compose file against the current AI.md before changes. CI must build and check every shipped image variant and platform it claims.

---
For complete details, see `AI.md` PARTS 26 and 28.
