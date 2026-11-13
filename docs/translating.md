# Translation guide

The application currently includes English (`en`), Spanish (`es`), Chinese (`zh`), French (`fr`), Arabic (`ar`), German (`de`), and Japanese (`ja`) catalogs in `src/common/i18n/locales/`.

## Update a catalog

1. Find the English key in `src/common/i18n/locales/en.json`.
2. Add the same key to each supported locale file, preserving the existing JSON structure.
3. Translate the complete message. Keep format placeholders and HTML-safe text requirements intact.
4. Use each language's natural punctuation and phrasing; do not translate product names, API paths, country codes, or command names.
5. For Arabic, preserve right-to-left language metadata and check mixed-direction text containing Latin paths, codes, and numbers.
6. Add or update tests for catalog parity and the translated behavior.

## Review checklist

- All seven locale files parse as JSON and expose the same keys.
- User-visible text contains no untranslated keys or template placeholders.
- Plurals, dates, numbers, and right-to-left layout remain readable in context.
- New CLI flags and web controls have translated labels and help text.
- API error messages and generated API documentation remain consistent across supported languages.

Run the project's test target in the required Go container after changing locale catalogs.
