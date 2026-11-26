// Package theme contains the shared semantic palettes used by project interfaces.
package theme

// ThemePalette defines the color roles shared by the web interface and API explorers.
type ThemePalette struct {
	Background   string `json:"background"`
	Foreground   string `json:"foreground"`
	Primary      string `json:"primary"`
	Secondary    string `json:"secondary"`
	Accent       string `json:"accent"`
	Success      string `json:"success"`
	Warning      string `json:"warning"`
	Error        string `json:"error"`
	Info         string `json:"info"`
	Surface      string `json:"surface"`
	SurfaceAlt   string `json:"surface_alt"`
	SurfaceHover string `json:"surface_hover"`
	Border       string `json:"border"`
	Muted        string `json:"muted"`
	AccentHover  string `json:"accent_hover"`
	AccentGlow   string `json:"accent_glow"`
}

// ThemePaletteDark is the shared default palette.
var ThemePaletteDark = ThemePalette{
	Background: "#282a36", Foreground: "#f8f8f2",
	Primary: "#bd93f9", Secondary: "#50fa7b", Accent: "#ff79c6",
	Success: "#50fa7b", Warning: "#ffb86c", Error: "#ff5555", Info: "#8be9fd",
	Surface: "#2b2d3a", SurfaceAlt: "#21222c", SurfaceHover: "#3b3e50",
	Border: "#44475a", Muted: "#939fcc", AccentHover: "#d6b8ff", AccentGlow: "rgba(189, 147, 249, 0.4)",
}

// ThemePaletteLight is the shared light palette.
var ThemePaletteLight = ThemePalette{
	Background: "#ffffff", Foreground: "#1f2328",
	Primary: "#0969da", Secondary: "#1a7f37", Accent: "#8250df",
	Success: "#1a7f37", Warning: "#9a6700", Error: "#d1242f", Info: "#0969da",
	Surface: "#f6f8fa", SurfaceAlt: "#eff2f5", SurfaceHover: "#e9eef3",
	Border: "#d1d9e0", Muted: "#59636e", AccentHover: "#0550ae", AccentGlow: "rgba(9, 105, 218, 0.2)",
}

// ForMode returns the light palette only for an explicit light preference.
func ForMode(mode string) ThemePalette {
	if mode == "light" {
		return ThemePaletteLight
	}
	return ThemePaletteDark
}
