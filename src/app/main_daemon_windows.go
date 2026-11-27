//go:build windows

package app

import (
	"os"
	"os/exec"
	"syscall"
)

const detachedProcess = 0x00000008

func launchDaemonChild() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(executable, daemonChildArgs(os.Args[1:])...)
	command.Env = daemonChildEnvironment(os.Environ())
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
