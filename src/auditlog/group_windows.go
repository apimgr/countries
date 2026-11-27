//go:build windows

package auditlog

import "errors"

func lookupAuditGroup(string) (int, error) {
	return -1, errors.New("Unix audit groups are unavailable on Windows")
}

func defaultAuditGroupID() int { return -1 }

func setAuditGroup(string, int) error { return nil }
