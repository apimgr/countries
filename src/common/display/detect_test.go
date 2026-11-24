package display

import "testing"

func TestAutoDetectDisplayMode(t *testing.T) {
	for _, test := range []struct {
		name string
		env  DisplayEnv
		want DisplayMode
	}{
		{name: "headless", env: DisplayEnv{}, want: DisplayModeHeadless},
		{name: "dumb terminal", env: DisplayEnv{IsTerminal: true, TerminalType: "dumb", HasDisplay: true}, want: DisplayModeCLI},
		{name: "local graphical terminal", env: DisplayEnv{HasDisplay: true, IsTerminal: true}, want: DisplayModeGUI},
		{name: "remote graphical terminal", env: DisplayEnv{HasDisplay: true, IsTerminal: true, IsSSH: true}, want: DisplayModeTUI},
		{name: "mosh terminal", env: DisplayEnv{IsTerminal: true, IsMosh: true}, want: DisplayModeTUI},
		{name: "terminal without display", env: DisplayEnv{IsTerminal: true}, want: DisplayModeTUI},
		{name: "display without terminal", env: DisplayEnv{HasDisplay: true}, want: DisplayModeGUI},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.env.autoDetectDisplayMode(); got != test.want {
				t.Fatalf("autoDetectDisplayMode() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestDisplayModePredicates(t *testing.T) {
	for _, test := range []struct {
		mode DisplayMode
		gui  bool
		tui  bool
		cli  bool
		head bool
	}{
		{mode: DisplayModeGUI, gui: true},
		{mode: DisplayModeTUI, tui: true},
		{mode: DisplayModeCLI, cli: true},
		{mode: DisplayModeHeadless, head: true},
	} {
		env := DisplayEnv{Mode: test.mode}
		if env.IsGUI() != test.gui || env.IsAutoDetectDisplayModeGUI() != test.gui {
			t.Errorf("GUI predicates for mode %v do not match", test.mode)
		}
		if env.IsTUI() != test.tui || env.IsAutoDetectDisplayModeTUI() != test.tui {
			t.Errorf("TUI predicates for mode %v do not match", test.mode)
		}
		if env.IsCLI() != test.cli || env.IsAutoDetectDisplayModeCLI() != test.cli {
			t.Errorf("CLI predicates for mode %v do not match", test.mode)
		}
		if env.IsHeadless() != test.head || env.IsAutoDetectDisplayModeHeadless() != test.head {
			t.Errorf("headless predicates for mode %v do not match", test.mode)
		}
	}
}

func TestRemoteAndDumbTerminalPredicates(t *testing.T) {
	if !(DisplayEnv{IsSSH: true}).IsRemoteSession() || !(DisplayEnv{IsMosh: true}).IsRemoteSession() {
		t.Fatal("remote session was not detected")
	}
	if (DisplayEnv{TerminalType: "xterm"}).IsDumbTerminal() || !(DisplayEnv{TerminalType: "dumb"}).IsDumbTerminal() {
		t.Fatal("TERM=dumb predicate returned an incorrect result")
	}
}

func TestDetectDisplayAndSystemTheme(t *testing.T) {
	for _, name := range []string{"DISPLAY", "WAYLAND_DISPLAY", "SSH_CLIENT", "SSH_TTY", "SSH_CONNECTION", "MOSH", "STY", "TMUX"} {
		t.Setenv(name, "")
	}
	t.Setenv("TERM", "dumb")
	env := DetectDisplayEnv()
	if env.TerminalType != "dumb" || env.Mode < DisplayModeHeadless || env.Mode > DisplayModeGUI {
		t.Fatalf("DetectDisplayEnv() = %+v", env)
	}
	if env.IsDumbTerminal() != (env.TerminalType == "dumb") || env.IsRemoteSession() != (env.IsSSH || env.IsMosh) {
		t.Fatalf("display predicates do not reflect detected environment: %+v", env)
	}
	if theme := DetectSystemTheme(); theme != ThemeDark && theme != ThemeLight {
		t.Fatalf("DetectSystemTheme() = %q, want a supported theme", theme)
	}
}
