//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func launchDaemonChild() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	args := daemonChildArgs(os.Args[1:])
	command := exec.Command(executable, args...)
	command.Env = daemonChildEnvironment(os.Environ())
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	command.Stdin = stdin
	stdout, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		_ = stdin.Close()
		return err
	}
	command.Stdout = stdout
	command.Stderr = stdout
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return err
	}
	_ = stdin.Close()
	_ = stdout.Close()
	return command.Process.Release()
}
