# Docker Rules (PART 26)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never place Dockerfiles or Compose files at the project root.
- Never use a single-stage build or depend on a prebuilt host binary.
- Never leave containers or other task-created runtime artifacts behind.

## CRITICAL - ALWAYS DO
- Keep Docker files under `docker/` and build-time overlay files under `docker/rootfs/`.
- Use a multi-stage build and the required `entrypoint.sh` for container startup.
- Run containers with the required project-prefixed names, bounded resources, and cleanup behavior.

## KEY DECISIONS
| Question | Answer | Spec Reference |
|----------|--------|----------------|
| Where do Dockerfiles live? | `docker/` | PART 26 |
| How does a container start? | Through `entrypoint.sh` | PART 26 |

## TERMINOLOGY
| Term | Meaning |
|------|---------|
| Build-time overlay | Files under `docker/rootfs/` copied into the image during its build |

## QUICK REFERENCE
Keep the shipped image, local integration image, runtime filesystem, and health checks aligned with PART 26.

---
For complete details, see AI.md PART 26.
