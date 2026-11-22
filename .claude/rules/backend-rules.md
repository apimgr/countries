# Backend Rules (PARTS 9, 10, 11, 31)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never concatenate untrusted data into SQL, run unbounded queries, expose stack traces, or return internal paths, secrets, or attacker-useful details publicly.
- Never log credentials, raw tokens, passwords, encryption keys, request bodies, sensitive query values, or user PII; redact sensitive fields at every log boundary.
- Never trust proxy identity headers unless the immediate peer is trusted; never let GeoIP/overlay identity bypass normal auth, validation, rate limits, or audit controls.
- Never put operator-editable configuration in the database, persist plaintext sensitive payloads, or weaken SQLite durability/concurrency controls.
- Never expose user-controlled active content on the application origin.
- Never treat Tor and I2P as interchangeable: Tor starts when its binary is detected; I2P follows its computed container/bare-metal opt-in default and must not contact a provider when disabled.

## CRITICAL - ALWAYS DO
- Use structured, correlated errors and safe response envelopes; hide implementation details while logging sanitized context with request IDs.
- Use pure-Go `modernc.org/sqlite`, parameterized SQL, bounded transactions/queries, idempotent schema setup, connection pooling, and the AI.md SQLite single-writer/WAL contract.
- Enforce request IDs, trusted-proxy parsing, security headers, rate limits, CSRF, audit/security logging, and configured log rotation.
- Treat public surfaces under the three-tier safety policy; apply authentication consistently and avoid account/resource enumeration.
- Keep secrets encrypted at rest using the canonical key, support required rotation/grace behavior, and never expose or log secrets.
- For Tor, manage the project-owned hidden service, state/keys, loopback backend, isolation, lifecycle, and health checks according to PART 31.
- For I2P, honor container-aware enablement, persistent destination keys, provider model rules, isolated listeners, safe SAM monitoring, and clean process shutdown; never manage an external router.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Local database | Pure-Go SQLite with idempotent schema and explicit concurrency/timeout behavior. | PART 10 |
| Configuration storage | File-backed config only; database holds application state and server-generated app secrets. | PART 5, 10, 11 |
| Proxy identity | Use forwarding headers only from an explicitly trusted immediate peer. | PART 11 |
| Tor default | Auto-enable when the Tor binary is available and manage the app's own hidden service. | PART 31.1 |
| I2P default | Computed: opt-in outside containers and on in containers unless explicitly overridden; no image-set default. | PART 31.2 |

## TERMINOLOGY
| Term | Meaning |
|---|---|
| Public-safe | Data allowed by PART 11's tiered disclosure rules on unauthenticated surfaces. |
| Trusted proxy | An immediate peer explicitly permitted to supply client identity headers. |
| App secret | Server-generated secret state stored in `server.db` with its specified rotation and disclosure rules. |

## QUICK REFERENCE
Bound every input and operation; sanitize logs and public errors; preserve database invariants, secret handling, rate limits, and overlay provider ownership.

---
For complete details, see `AI.md` PARTS 9, 10, 11, and 31.
