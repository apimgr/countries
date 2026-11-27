package service

import (
	"fmt"
	"strings"
)

// EnforcePrivateACL secures the private configuration and data trees for a
// system-mode Windows service. It is a no-op in user mode and on other OSes.
func EnforcePrivateACL(systemMode bool, configDir, dataDir string) error {
	if !systemMode {
		return nil
	}
	return enforcePrivateACL(configDir, dataDir)
}

func privateACLDescriptor(serviceSID string) (string, error) {
	if !strings.HasPrefix(serviceSID, "S-1-5-80-") {
		return "", fmt.Errorf("invalid Windows service SID %q", serviceSID)
	}
	return "D:P(A;OICI;FA;;;" + serviceSID + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)", nil
}
