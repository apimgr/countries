# API Rules (PART 13, 14, 15)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never retain legacy project endpoints or publish a non-versioned route as canonical; unversioned aliases must mount the exact canonical handler.
- Never expose host paths, secrets, stack traces, internal service status, database details, or unsafe dataset errors through public health/API responses.
- Never hand-edit generated OpenAPI JSON or GraphQL schema, or allow REST, GraphQL, routes, specs, and documentation to drift.
- Never treat a compatibility protocol as a partial implementation; follow its applicable standard fully.
- Never serve a certificate for a mismatched hostname, silently downgrade a failed ACME challenge, or renew certificates managed by system certbot/user configuration.

## CRITICAL - ALWAYS DO
- Use versioned, lowercase, plural canonical API paths with no trailing slash except the explicit unversioned alias list; authenticate protected native routes with `Authorization: Bearer` only.
- Validate and bound every input; apply rate limits and the current content-negotiation contract to each route.
- Return the prescribed success/error response formats and newline/indentation rules; health responses follow their explicit bare health payload exception.
- Keep public API and SSR behavior usable without JavaScript where required, with matching routes and generated API specifications.
- Reconcile the route registry, router, generated OpenAPI/GraphQL, endpoint tests, and documentation; test all applicable methods, aliases, formats, and status codes.
- Provide built-in TLS/Let's Encrypt support using the specified HTTP-01, TLS-ALPN-01, and DNS-01 challenge behavior; keep app-managed renewals in the built-in scheduler.
- Resolve the FQDN from the specified sources; validate configured DNS providers at startup and before requests, keep credentials encrypted, and report credential names without values.
- Respect certificate ownership and lookup order: user/system certs are not renewed by the app; only app-managed Let's Encrypt certs are auto-renewed seven days before expiry.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Canonical REST paths | Versioned lowercase plural paths; only the listed aliases are unversioned. | PART 14 |
| Health output | Public-safe cached data; health endpoints use the explicitly documented bare response contract. | PART 13 |
| API specification files | Generate OpenAPI and GraphQL from route/source annotations and verify route parity. | PART 14 |
| Certificate ownership | The app renews only its own stored Let's Encrypt certificates. | PART 15 |

## TERMINOLOGY
| Term | Meaning |
|---|---|
| Canonical API route | Versioned native REST path used by clients and generated API specifications. |
| Direct alias | Explicit compatibility path mounted to the same handler without redirect. |
| App-managed certificate | Let's Encrypt certificate stored under the app's config SSL directory and renewed by the scheduler. |

## QUICK REFERENCE
Keep route methods/auth/shapes, status codes, content negotiation, OpenAPI, GraphQL, docs, and TLS behavior aligned with the current AI.md contract.

---
For complete details, see `AI.md` PARTS 13, 14, and 15.
