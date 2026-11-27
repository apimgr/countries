//go:build windows

package service

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func canElevate() bool { return true }

func elevate(args []string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	parameters := make([]string, 0, len(args)-1)
	for _, arg := range args[1:] {
		parameters = append(parameters, windows.EscapeArg(arg))
	}
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return err
	}
	params, err := windows.UTF16PtrFromString(strings.Join(parameters, " "))
	if err != nil {
		return err
	}
	workdir, err := windows.UTF16PtrFromString("")
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, params, workdir, windows.SW_SHOWNORMAL)
}
