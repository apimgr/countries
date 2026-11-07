# API Rules (PART 13, 14, 15)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never publish undocumented or unversioned APIs as canonical endpoints.
- Never expose internal health details, stack traces, host paths, or credentials.
- Never diverge REST, GraphQL, OpenAPI, and published documentation.

## CRITICAL - ALWAYS DO
- Use `/api/{api_version}/` for REST and consistent `{ok,data,error,message}` response envelopes.
- Validate query, path, body, pagination, sorting, and filter inputs; apply endpoint rate limits.
- Provide accurate API schemas, status codes, and SSL/TLS behavior.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Current API version | `/api/v1/`. | PART 14 |
| API access | Public, read-only, cross-origin access as defined by IDEA.md. | IDEA.md |

## TERMINOLOGY
| Term | Meaning |
|---|---|
| Canonical endpoint | Versioned API path described by OpenAPI. |

## QUICK REFERENCE
Document every route and keep route definitions, schemas, responses, and frontend calls aligned.

---
For complete details, see `AI.md` PARTS 13, 14, and 15.
