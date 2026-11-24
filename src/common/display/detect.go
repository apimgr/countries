// Package display detects which interactive display modes are available.
package display

import (
	"os"
	"strings"

	"golang.org/x/term"
)

type DisplayMode int

const (
	DisplayModeHeadless DisplayMode = iota
	DisplayModeCLI
	DisplayModeTUI
	DisplayModeGUI
)

type DisplayEnv struct {
	Mode         DisplayMode
	HasDisplay   bool
	DisplayType  string
	IsTerminal   bool
	IsSSH        bool
	IsMosh       bool
	IsScreen     bool
	TerminalType string
	Cols         int
	Rows         int
}

func DetectDisplayEnv() DisplayEnv {
	env := DisplayEnv{TerminalType: os.Getenv("TERM")}
	env.IsTerminal = term.IsTerminal(int(os.Stdout.Fd()))
	if env.IsTerminal {
		env.Cols, env.Rows, _ = term.GetSize(int(os.Stdout.Fd()))
	}
	env.IsSSH = os.Getenv("SSH_CLIENT") != "" || os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CONNECTION") != ""
	env.IsMosh = os.Getenv("MOSH") != "" || strings.Contains(strings.ToLower(env.TerminalType), "mosh")
	env.IsScreen = os.Getenv("STY") != "" || os.Getenv("TMUX") != ""
	env.detectPlatformDisplay()
	env.Mode = env.autoDetectDisplayMode()
	return env
}

func (env DisplayEnv) autoDetectDisplayMode() DisplayMode {
	if !env.IsTerminal && !env.HasDisplay {
		return DisplayModeHeadless
	}
	if env.IsDumbTerminal() {
		return DisplayModeCLI
	}
	if env.HasDisplay && !env.IsSSH && !env.IsMosh {
		return DisplayModeGUI
	}
	if env.IsTerminal {
		return DisplayModeTUI
	}
	return DisplayModeCLI
}

func (env DisplayEnv) IsDumbTerminal() bool { return env.TerminalType == "dumb" }

func (env DisplayEnv) IsRemoteSession() bool { return env.IsSSH || env.IsMosh }

func (env DisplayEnv) IsAutoDetectDisplayModeGUI() bool { return env.Mode == DisplayModeGUI }
func (env DisplayEnv) IsAutoDetectDisplayModeTUI() bool { return env.Mode == DisplayModeTUI }
func (env DisplayEnv) IsAutoDetectDisplayModeCLI() bool { return env.Mode == DisplayModeCLI }
func (env DisplayEnv) IsAutoDetectDisplayModeHeadless() bool {
	return env.Mode == DisplayModeHeadless
}

func (env DisplayEnv) IsGUI() bool      { return env.Mode == DisplayModeGUI }
func (env DisplayEnv) IsTUI() bool      { return env.Mode == DisplayModeTUI }
func (env DisplayEnv) IsCLI() bool      { return env.Mode == DisplayModeCLI }
func (env DisplayEnv) IsHeadless() bool { return env.Mode == DisplayModeHeadless }
