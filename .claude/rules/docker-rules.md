# Docker Rules (PART 26)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never run production containers as root when a dedicated user is specified.
- Never bake secrets or mutable runtime data into the image.
- Never publish a multi-platform image without verifying each target.

## CRITICAL - ALWAYS DO
- Use multi-stage builds, minimal runtime contents, health checks, and explicit volumes.
- Preserve persistent config/data/log/database paths and document their behavior.
- Label release images with accurate metadata.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Runtime data | Mounted volumes, outside the image. | PART 26 |
| Build toolchain | Go build stage with CGO disabled. | PARTS 7, 26 |

## TERMINOLOGY
| Term | Meaning |
|---|---|
| Runtime image | Minimal container that runs the built server binary. |

## QUICK REFERENCE
Validate non-root behavior, signals, health checks, volumes, and image labels.

---
For complete details, see `AI.md` PART 26.
