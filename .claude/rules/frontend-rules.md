# Frontend Rules (PART 16)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never inject untrusted HTML or rely on client-side validation alone.
- Never make core product use depend on JavaScript where server rendering can support it.
- Never ship inaccessible controls, unlabeled inputs, or nonresponsive layouts.
- Never add inline styles, hard-coded theme colors, fixed/sticky page chrome, or magic z-index values.
- Never concatenate translated text fragments or use a language selector outside `/server/preferences`.
- Never allow a fixed overlay to sit under an ancestor with `transform`, `filter`, `backdrop-filter`, `contain`, or `will-change`.

## CRITICAL - ALWAYS DO
- Provide dark, light, and automatic theme behavior, with dark as the default.
- Use semantic HTML, keyboard operation, visible focus, accessible names, and WCAG AA contrast.
- Keep pages server-rendered, responsive and progressively enhanced; core content and forms must work without JavaScript.
- Give every page exactly one `<h1>`, localized skip links, labeled controls, opaque overlay surfaces and a shared public layout, including error pages.
- Use the single shared palette and spacing/stacking tokens. Use `100dvh`; fixed positioning is reserved for overlays and notification surfaces with safe-area insets.
- Keep layout within 320 px and wider without horizontal overflow even when the `overflow-x` backstop is disabled; support RTL, text zoom and long values.
- Load external stylesheets in `common.css`, `components.css`, `public.css` order. Use local, vendored assets for documentation interfaces.
- Localize every human-readable frontend string and keep all supported catalogs key-complete; `?lang=` is one-shot unless preferences consent allows persistence.
- Keep theme selection on `<html>` as exactly one of `theme-dark`, `theme-light`, or `theme-auto`; server-render the class from the theme cookie/config.

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
Check the full AI.md review matrix: required viewports, both directions, all themes, 200% zoom, empty/loading/error/success states, and JavaScript disabled. Verify contrast, reduced motion, focus rings, touch target size, no horizontal overflow, and shared behavior across every server page and error response.

---
For complete details, see `AI.md` PART 16.
