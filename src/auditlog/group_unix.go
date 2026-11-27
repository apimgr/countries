//go:build !windows

package auditlog

import (
	"os"
	"os/user"
	"strconv"
)

func lookupAuditGroup(name string) (int, error) {
	group, err := user.LookupGroup(name)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(group.Gid)
}

func defaultAuditGroupID() int { return os.Getgid() }

func setAuditGroup(path string, gid int) error { return os.Chown(path, -1, gid) }
