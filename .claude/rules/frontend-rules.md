# Frontend Rules (PART 16)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never ship placeholder server pages, nonfunctional controls, or a JavaScript-only feature flow.
- Never combine wildcard CORS origins with credentials.
- Never create an admin dashboard or settings page unless the specification explicitly requires it.

## CRITICAL - ALWAYS DO
- Build a complete, professional, mobile-first frontend that follows the API/page pairing rule.
- Make every feature usable without JavaScript; JavaScript may enhance the experience.
- Keep page data and behavior consistent with its public API endpoint.
- Follow the documented CORS, accessibility, localization, layout, and asset-loading contracts.

## KEY DECISIONS
| Question | Answer | Spec Reference |
|----------|--------|----------------|
| Do public pages have paired API data? | Yes, except the system-page exceptions documented in PART 1 | PART 1, PART 16 |
| Are credentials allowed with `Access-Control-Allow-Origin: *`? | No | PART 16 |

## TERMINOLOGY
| Term | Meaning |
|------|---------|
| Progressive enhancement | The page works without JavaScript and improves when JavaScript is available |

## QUICK REFERENCE
Verify pages with JavaScript enabled and disabled, and keep static assets and page metadata current.

---
For complete details, see AI.md PART 16.
