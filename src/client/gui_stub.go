//go:build !gui || freebsd || netbsd || openbsd

package main

import "errors"

func clientGUIAvailable() bool { return false }

func launchClientGUI(server string, cfg *cliConfig, configPath string) error {
	return errors.New(clientText(selectLanguage("", ""), "display.gui_unavailable"))
}

func promptClientUpdateGUI(string, string) (bool, error) {
	return false, errors.New(clientText(selectLanguage("", ""), "display.gui_unavailable"))
}
