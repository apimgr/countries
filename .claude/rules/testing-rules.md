# Testing Rules (PART 28, 29, 30)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never report checks that did not run or lower coverage thresholds to hide missing tests.
- Never ship untranslated or inaccessible user-facing text.

## CRITICAL - ALWAYS DO
- Test behavior at unit, API, integration, and end-to-end levels as required.
- Keep coverage at or above the specified threshold.
- Build documentation with the configured tool and keep translations complete.
- Verify accessibility, keyboard navigation, locale formatting, and pluralization.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Coverage gate | At least 60%. | PART 28 |
| Documentation | ReadTheDocs-compatible project docs. | PART 29 |

## TERMINOLOGY
| Term | Meaning |
|---|---|
| I18N | Translation and locale-aware formatting. |
| A11Y | Accessible operation and presentation. |

## QUICK REFERENCE
Run relevant verification, capture actual results, and disclose limitations.

---
For complete details, see `AI.md` PARTS 28, 29, and 30.
