//go:build !windows

package service

import (
	"os/exec"
)

func canElevate() bool {
	_, err := exec.LookPath("sudo")
	return err == nil
}

func elevate(args []string) error {
	command := append([]string{"--"}, args...)
	return exec.Command("sudo", command...).Run()
}
