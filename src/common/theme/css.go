package theme

import (
	"fmt"
	"strings"
)

// CSSVariables returns the canonical theme variables consumed by project stylesheets.
func CSSVariables() string {
	var css strings.Builder
	writeCSSPalette(&css, "html.theme-dark, html.theme-light, html.theme-auto", ThemePaletteDark, "#282a36", "linear-gradient(135deg, #bd93f9 0%, #ff79c6 100%)")
	writeCSSPalette(&css, "html.theme-light", ThemePaletteLight, "#ffffff", "linear-gradient(135deg, #0969da 0%, #8250df 100%)")
	css.WriteString("@media (prefers-color-scheme: light) {\n")
	writeCSSPalette(&css, "html.theme-auto", ThemePaletteLight, "#ffffff", "linear-gradient(135deg, #0969da 0%, #8250df 100%)")
	css.WriteString("}\n")
	return css.String()
}

func writeCSSPalette(css *strings.Builder, selector string, palette ThemePalette, inverse, gradient string) {
	fmt.Fprintf(css, `%s {
  --color-bg-primary: %s;
  --color-bg-secondary: %s;
  --color-bg-tertiary: %s;
  --color-bg-elevated: %s;
  --color-bg-hover: %s;
  --color-text-primary: %s;
  --color-text-secondary: %s;
  --color-text-muted: %s;
  --color-text-inverse: %s;
  --color-accent-primary: %s;
  --color-accent-secondary: %s;
  --color-accent-gradient: %s;
  --color-accent-hover: %s;
  --color-accent-glow: %s;
  --color-success: %s;
  --color-warning: %s;
  --color-error: %s;
  --color-info: %s;
  --color-border: %s;
  --color-border-hover: %s;
  --color-border-focus: var(--color-accent-primary);
}
`, selector, palette.Background, palette.Surface, palette.SurfaceAlt, palette.Surface, palette.SurfaceHover,
		palette.Foreground, palette.Muted, palette.Muted, inverse, palette.Primary, palette.Secondary, gradient,
		palette.AccentHover, palette.AccentGlow, palette.Success, palette.Warning, palette.Error, palette.Info,
		palette.Border, palette.Muted)
}
