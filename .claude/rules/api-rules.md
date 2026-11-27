# API Rules (PART 13, 14, 15)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never retain obsolete project routes or duplicate old and new route trees.
- Never accept auth tokens from query parameters or undocumented headers.
- Never expose non-public-safe health or version data.

## CRITICAL - ALWAYS DO
- Keep backend and frontend health fields in the exact specified order and shape.
- Version canonical API routes; use only the explicitly documented unversioned exceptions.
- Mount an unversioned exception to the same handler as its canonical route; do not redirect it.
- Provide built-in Let's Encrypt support and follow the documented challenge and credential handling.

## KEY DECISIONS
| Question | Answer | Spec Reference |
|----------|--------|----------------|
| Where does a protected API token go? | `Authorization: Bearer {token}` | PART 8, PART 14 |
| How should API aliases behave? | Same handler as canonical route, with no redirect | PART 14 |

## TERMINOLOGY
| Term | Meaning |
|------|---------|
| Canonical route | The versioned API path defined as the route's source of truth |

## QUICK REFERENCE
Keep REST, GraphQL, OpenAPI, and page/API contracts synchronized.

---
For complete details, see AI.md PART 13, PART 14, and PART 15.
