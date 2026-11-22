# Translation guide

The application currently includes English (`en`), Spanish (`es`), French (`fr`), German (`de`), Italian (`it`), Arabic (`ar`), Chinese (`zh`), and Japanese (`ja`) catalogs in `src/common/i18n/locales/`.

## Update a catalog

1. Find the English key in `src/common/i18n/locales/en.json`.
2. Add the same key to each supported locale file, preserving the existing JSON structure.
3. Translate the complete message. Keep format placeholders and HTML-safe text requirements intact.
4. Use each language's natural punctuation and phrasing; do not translate product names, API paths, country codes, or command names.
5. For Arabic, preserve right-to-left language metadata and check mixed-direction text containing Latin paths, codes, and numbers.
6. Add or update tests for catalog parity and the translated behavior.

## Glossary

Keep recurring terms consistent across pages and commands:

| English term | Translation rule |
|---|---|
| `token` | Keep the literal word `token` in user-facing translations. |
| Owner token | Translate “owner” naturally; retain `token`. |
| Linked token | Translate “linked” naturally; retain `token`. |
| Rotate / revoke | Use the same verb consistently for credential rotation and revocation. |
| API, GraphQL, MCP, JSON | Preserve protocol and product identifiers. |
| Country codes and API paths | Preserve exact values and spelling. |

## Review and credits

Check placeholder names and plural categories against the English catalog, then review translated pages at 320, 768, and 1280 pixels. For Arabic, also check right-to-left layout and mixed-script paths and numbers. Contributor credit is optional; when a translator requests credit, add their preferred name or handle to the locale's `meta.translators` field and this guide. Human review is welcome and corrections take precedence over earlier wording.

## Review checklist

- All eight locale files parse as JSON and expose the same keys.
- User-visible text contains no untranslated keys or template placeholders.
- Every required plural category is present and its placeholders are preserved.
- Plurals, dates, numbers, and right-to-left layout remain readable in context.
- Long translations fit at phone, tablet, and desktop widths.
- New CLI flags and web controls have translated labels and help text.
- API error messages and generated API documentation remain consistent across supported languages.

Run the project's test target in the required Go container after changing locale catalogs.
