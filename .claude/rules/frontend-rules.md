# Frontend Rules (PART 16)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never inject untrusted HTML or rely on client-side validation alone.
- Never make core product use depend on JavaScript where server rendering can support it.
- Never ship inaccessible controls, unlabeled inputs, or nonresponsive layouts.

## CRITICAL - ALWAYS DO
- Provide dark, light, and automatic theme behavior, with dark as the default.
- Use semantic HTML, keyboard operation, visible focus, accessible names, and WCAG AA contrast.
- Keep pages responsive and progressively enhanced.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Default theme | Dark. | PART 16 |
| Country data | Render from the embedded server dataset. | IDEA.md |

## TERMINOLOGY
| Term | Meaning |
|---|---|
| Progressive enhancement | Core page content works without client-side scripting. |

## QUICK REFERENCE
Check keyboard use, narrow viewports, screen-reader labels, and reduced motion.

---
For complete details, see `AI.md` PART 16.
