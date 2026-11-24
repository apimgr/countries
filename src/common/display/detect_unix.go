//go:build !windows

package display

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func (env *DisplayEnv) detectPlatformDisplay() {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		env.HasDisplay, env.DisplayType = true, "wayland"
		return
	}
	if os.Getenv("DISPLAY") != "" {
		env.HasDisplay, env.DisplayType = true, "x11"
		return
	}
	if runtime.GOOS == "darwin" && !env.IsSSH {
		if os.Getenv("__CFBundleIdentifier") != "" {
			env.HasDisplay, env.DisplayType = true, "macos"
			return
		}
		if output, err := exec.Command("launchctl", "managername").Output(); err == nil && strings.Contains(string(output), "Aqua") {
			env.HasDisplay, env.DisplayType = true, "macos"
			return
		}
	}
	env.DisplayType = "none"
}
