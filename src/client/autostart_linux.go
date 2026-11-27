//go:build gui && linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func autostartSupported() bool { return true }

func autostartPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "autostart", projectName+"-cli.desktop"), nil
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
	data := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=Countries CLI\nExec=%s\nTerminal=true\n", strconv.Quote(binary))
	return os.WriteFile(path, []byte(data), 0o644)
}
