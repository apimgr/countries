//go:build !windows

package service

func enforcePrivateACL(_, _ string) error { return nil }
