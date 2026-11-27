//go:build gui && !linux && !darwin && !windows

package main

import "errors"

func autostartSupported() bool { return false }
func autostartEnabled() bool   { return false }
func setAutostart(bool) error  { return errors.New("login autostart is unsupported on this platform") }
