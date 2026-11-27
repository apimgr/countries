//go:build gui && windows

package main

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const clientRunRegistryPath = `Software\Microsoft\Windows\CurrentVersion\Run`

func autostartSupported() bool { return true }

func autostartEnabled() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, clientRunRegistryPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	_, _, err = key.GetStringValue(projectName + "-cli")
	return err == nil
}

func setAutostart(enabled bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, clientRunRegistryPath, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	name := projectName + "-cli"
	if !enabled {
		if err := key.DeleteValue(name); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	quoted := `"` + strings.ReplaceAll(binary, `"`, `\"`) + `"`
	return key.SetStringValue(name, fmt.Sprintf("%s", quoted))
}
