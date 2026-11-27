//go:build gui && darwin

package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
)

func autostartSupported() bool { return true }

func autostartPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", "apimgr."+projectName+"-cli.plist"), nil
}

func autostartEnabled() bool {
	path, err := autostartPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

func setAutostart(enabled bool) error {
	path, err := autostartPath()
	if err != nil {
		return err
	}
	if !enabled {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data := fmt.Sprintf("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\"><dict><key>Label</key><string>apimgr.%s-cli</string><key>ProgramArguments</key><array><string>%s</string></array><key>RunAtLoad</key><true/></dict></plist>\n", projectName, xmlEscape(binary))
	return os.WriteFile(path, []byte(data), 0o600)
}

func xmlEscape(value string) string {
	encoded, _ := xml.Marshal(value)
	return string(encoded[8 : len(encoded)-9])
}
