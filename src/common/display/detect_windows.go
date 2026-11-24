//go:build windows

package display

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func (env *DisplayEnv) detectPlatformDisplay() {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	consoleWindow := kernel32.NewProc("GetConsoleWindow")
	hwnd, _, _ := consoleWindow.Call()
	var sessionID uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &sessionID); err != nil || sessionID == 0 {
		env.DisplayType = "none"
		return
	}
	if strings.HasPrefix(os.Getenv("SESSIONNAME"), "RDP-") {
		env.HasDisplay, env.DisplayType = true, "windows-rdp"
		return
	}
	env.HasDisplay = hwnd != 0
	if env.HasDisplay {
		env.DisplayType = "windows"
	} else {
		env.DisplayType = "none"
	}
}
