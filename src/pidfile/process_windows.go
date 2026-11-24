//go:build windows

package pidfile

func processIsThisExecutable(int) bool { return false }
