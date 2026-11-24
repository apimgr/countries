package display

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type SystemTheme string

const (
	ThemeDark  SystemTheme = "dark"
	ThemeLight SystemTheme = "light"
)

func DetectSystemTheme() SystemTheme {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var command *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		command = exec.CommandContext(ctx, "gsettings", "get", "org.gnome.desktop.interface", "color-scheme")
	case "darwin":
		command = exec.CommandContext(ctx, "defaults", "read", "-g", "AppleInterfaceStyle")
	case "windows":
		command = exec.CommandContext(ctx, "reg", "query", `HKCU\Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, "/v", "AppsUseLightTheme")
	default:
		return ThemeDark
	}
	output, err := command.Output()
	if err != nil {
		if runtime.GOOS == "darwin" && ctx.Err() == nil {
			if _, lookErr := exec.LookPath("defaults"); lookErr == nil {
				return ThemeLight
			}
		}
		return ThemeDark
	}
	value := strings.ToLower(strings.TrimSpace(string(output)))
	if strings.Contains(value, "dark") || strings.Contains(value, "0x0") {
		return ThemeDark
	}
	if strings.Contains(value, "light") || strings.Contains(value, "0x1") {
		return ThemeLight
	}
	if runtime.GOOS == "linux" && value != "" {
		return ThemeLight
	}
	return ThemeDark
}
