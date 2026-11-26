package graphql

import (
	"fmt"
	"strconv"
	"strings"

	commontheme "github.com/apimgr/countries/src/common/theme"
)

// CSSVariables returns GraphiQL's HSL variables derived from the shared palette.
func CSSVariables() string {
	var css strings.Builder
	writeGraphiQLPalette(&css, ".graphiql-container, .CodeMirror-info, .CodeMirror-lint-tooltip, reach-portal", commontheme.ThemePaletteDark)
	writeGraphiQLPalette(&css, "body.graphiql-dark .graphiql-container, body.graphiql-dark .CodeMirror-info, body.graphiql-dark .CodeMirror-lint-tooltip, body.graphiql-dark reach-portal", commontheme.ThemePaletteDark)
	writeGraphiQLPalette(&css, "body.graphiql-light .graphiql-container, body.graphiql-light .CodeMirror-info, body.graphiql-light .CodeMirror-lint-tooltip, body.graphiql-light reach-portal", commontheme.ThemePaletteLight)
	css.WriteString("@media (prefers-color-scheme: light) {\n")
	writeGraphiQLPalette(&css, "html.theme-auto .graphiql-container, html.theme-auto .CodeMirror-info, html.theme-auto .CodeMirror-lint-tooltip, html.theme-auto reach-portal", commontheme.ThemePaletteLight)
	css.WriteString("}\n")
	return css.String()
}

func writeGraphiQLPalette(css *strings.Builder, selector string, palette commontheme.ThemePalette) {
	colors := []string{
		hexToHSL(palette.Primary), hexToHSL(palette.Secondary), hexToHSL(palette.Accent), hexToHSL(palette.Info),
		hexToHSL(palette.Success), hexToHSL(palette.Warning), hexToHSL(palette.Error),
		hexToHSL(palette.Foreground), hexToHSL(palette.Background),
	}
	fmt.Fprintf(css, `%s {
  --color-primary: %s;
  --color-secondary: %s;
  --color-tertiary: %s;
  --color-info: %s;
  --color-success: %s;
  --color-warning: %s;
  --color-error: %s;
  --color-neutral: %s;
  --color-base: %s;
}
`, selector, colors[0], colors[1], colors[2], colors[3], colors[4], colors[5], colors[6], colors[7], colors[8])
}

func hexToHSL(value string) string {
	value = strings.TrimPrefix(value, "#")
	if len(value) != 6 {
		return "0, 0%, 0%"
	}
	raw, err := strconv.ParseUint(value, 16, 24)
	if err != nil {
		return "0, 0%, 0%"
	}
	r, g, b := float64(raw>>16)/255, float64((raw>>8)&0xff)/255, float64(raw&0xff)/255
	max := max(r, g, b)
	min := min(r, g, b)
	delta := max - min
	hue := 0.0
	if delta != 0 {
		switch max {
		case r:
			hue = 60 * ((g - b) / delta)
			if g < b {
				hue += 360
			}
		case g:
			hue = 60 * (((b - r) / delta) + 2)
		case b:
			hue = 60 * (((r - g) / delta) + 4)
		}
	}
	lightness := (max + min) / 2
	saturation := 0.0
	if delta != 0 {
		saturation = delta / (1 - abs(2*lightness-1))
	}
	return fmt.Sprintf("%.1f, %.1f%%, %.1f%%", hue, saturation*100, lightness*100)
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
