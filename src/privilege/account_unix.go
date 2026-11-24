//go:build linux || darwin || freebsd || openbsd || netbsd

package privilege

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"syscall"
)

var accountNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// IsElevated reports whether the current process has Unix root privileges.
func IsElevated() bool {
	return os.Geteuid() == 0
}

// EnsureSystemAccount creates the dedicated service account when absent and
// returns the existing account when its UID and primary GID match.
func EnsureSystemAccount(name, homeDir string) (Identity, error) {
	if !accountNamePattern.MatchString(name) {
		return Identity{}, errors.New("invalid service account name")
	}
	if err := requireElevated(os.Geteuid()); err != nil {
		return Identity{}, err
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "freebsd" {
		return Identity{}, ErrUnsupported
	}
	if homeDir == "" || !filepath.IsAbs(homeDir) {
		return Identity{}, errors.New("service account home must be an absolute path")
	}
	return ensureSystemAccount(name, homeDir)
}

func ensureSystemAccount(name, homeDir string) (Identity, error) {
	account, accountErr := user.Lookup(name)
	if accountErr != nil && !isUnknownUser(accountErr) {
		return Identity{}, fmt.Errorf("look up service account: %w", accountErr)
	}
	group, groupErr := user.LookupGroup(name)
	if groupErr != nil && !isUnknownGroup(groupErr) {
		return Identity{}, fmt.Errorf("look up service group: %w", groupErr)
	}

	var id int
	if account != nil {
		uid, err := strconv.Atoi(account.Uid)
		if err != nil {
			return Identity{}, errors.New("service account has an invalid UID")
		}
		gid, err := strconv.Atoi(account.Gid)
		if err != nil || gid != uid {
			return Identity{}, errors.New("service account UID and primary GID must match")
		}
		id = uid
		if !validSystemID(id, runtime.GOOS) {
			return Identity{}, errors.New("service account UID is outside the safe system range")
		}
		if group != nil {
			groupID, err := strconv.Atoi(group.Gid)
			if err != nil || groupID != id {
				return Identity{}, errors.New("service account group GID must match its UID")
			}
		} else {
			if err := createGroup(name, id); err != nil {
				return Identity{}, err
			}
			return Identity{Name: name, UID: id, GID: id, GroupCreated: true}, nil
		}
		return Identity{Name: name, UID: id, GID: id}, nil
	}

	if group != nil {
		groupID, err := strconv.Atoi(group.Gid)
		if err != nil || groupID < 200 || groupID > safeMaximum(runtime.GOOS) || reservedIDs[groupID] {
			return Identity{}, errors.New("service group GID is outside the safe system range")
		}
		if _, err := user.LookupId(strconv.Itoa(groupID)); err == nil {
			return Identity{}, errors.New("service group GID is already assigned to another user")
		} else if !isUnknownUID(err) {
			return Identity{}, fmt.Errorf("check service UID availability: %w", err)
		}
		id = groupID
		if err := createUser(name, id, homeDir); err != nil {
			return Identity{}, err
		}
		return Identity{Name: name, UID: id, GID: id, Created: true}, nil
	} else {
		var err error
		id, err = findAvailableSystemID()
		if err != nil {
			return Identity{}, err
		}
		if err := createGroup(name, id); err != nil {
			return Identity{}, err
		}
	}
	if err := createUser(name, id, homeDir); err != nil {
		rollbackErr := RemoveSystemAccount(Identity{Name: name, UID: id, GID: id, GroupCreated: group == nil})
		if rollbackErr != nil {
			return Identity{}, errors.Join(err, fmt.Errorf("roll back service group creation: %w", rollbackErr))
		}
		return Identity{}, err
	}
	return Identity{Name: name, UID: id, GID: id, Created: true, GroupCreated: group == nil}, nil
}

// RemoveSystemAccount deletes an account only when its current IDs still match
// the identity recorded when the application created it.
func RemoveSystemAccount(identity Identity) error {
	if (!identity.Created && !identity.GroupCreated) || !accountNamePattern.MatchString(identity.Name) || identity.UID < 200 || identity.GID != identity.UID {
		return errors.New("invalid application-created service identity")
	}
	if err := requireElevated(os.Geteuid()); err != nil {
		return err
	}
	accountExists := false
	if identity.Created {
		account, err := user.Lookup(identity.Name)
		if err != nil {
			if !isUnknownUser(err) {
				return fmt.Errorf("look up service account: %w", err)
			}
		} else {
			accountExists = true
			uid, uidErr := strconv.Atoi(account.Uid)
			gid, gidErr := strconv.Atoi(account.Gid)
			if uidErr != nil || gidErr != nil || uid != identity.UID || gid != identity.GID {
				return errors.New("service account identity changed; refusing removal")
			}
		}
	}
	if identity.GroupCreated && accountExists && !identity.Created {
		return nil
	}
	if identity.GroupCreated {
		group, err := user.LookupGroup(identity.Name)
		if err != nil {
			if !isUnknownGroup(err) {
				return fmt.Errorf("look up service group: %w", err)
			}
		} else {
			groupID, parseErr := strconv.Atoi(group.Gid)
			if parseErr != nil || groupID != identity.GID {
				return errors.New("service group identity changed; refusing removal")
			}
		}
	}
	if identity.Created && accountExists {
		var command *exec.Cmd
		switch runtime.GOOS {
		case "linux":
			command = exec.Command("userdel", identity.Name)
		case "darwin":
			if err := exec.Command("dscl", ".", "-delete", "/Users/"+identity.Name).Run(); err != nil {
				return fmt.Errorf("remove service account: %w", err)
			}
		case "freebsd":
			command = exec.Command("pw", "userdel", "-n", identity.Name)
		default:
			return ErrUnsupported
		}
		if command != nil {
			if err := command.Run(); err != nil {
				return fmt.Errorf("remove service account: %w", err)
			}
		}
	}
	if identity.GroupCreated {
		if _, err := user.LookupGroup(identity.Name); err != nil {
			if isUnknownGroup(err) {
				return nil
			}
			return fmt.Errorf("look up service group after account removal: %w", err)
		}
		var command *exec.Cmd
		switch runtime.GOOS {
		case "linux":
			command = exec.Command("groupdel", identity.Name)
		case "darwin":
			command = exec.Command("dscl", ".", "-delete", "/Groups/"+identity.Name)
		case "freebsd":
			command = exec.Command("pw", "groupdel", "-n", identity.Name)
		default:
			return ErrUnsupported
		}
		if err := command.Run(); err != nil {
			return fmt.Errorf("remove service group: %w", err)
		}
	}
	return nil
}

func findAvailableSystemID() (int, error) {
	maximum := safeMaximum(runtime.GOOS)
	for id := maximum; id >= 200; id-- {
		if reservedIDs[id] {
			continue
		}
		_, uidErr := user.LookupId(strconv.Itoa(id))
		if uidErr == nil {
			continue
		}
		if !isUnknownUID(uidErr) {
			continue
		}
		_, gidErr := user.LookupGroupId(strconv.Itoa(id))
		if gidErr == nil {
			continue
		}
		if !isUnknownGID(gidErr) {
			continue
		}
		return id, nil
	}
	return 0, fmt.Errorf("no available UID/GID in safe range 200-%d", maximum)
}

func safeMaximum(goos string) int {
	if goos == "darwin" {
		return 399
	}
	return 899
}

func createGroup(name string, id int) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		command = exec.Command("groupadd", "--system", "--gid", strconv.Itoa(id), name)
	case "darwin":
		for _, args := range [][]string{
			{".", "-create", "/Groups/" + name},
			{".", "-create", "/Groups/" + name, "PrimaryGroupID", strconv.Itoa(id)},
			{".", "-create", "/Groups/" + name, "Password", "*"},
		} {
			if err := exec.Command("dscl", args...).Run(); err != nil {
				return fmt.Errorf("create service group: %w", err)
			}
		}
		return nil
	case "freebsd":
		command = exec.Command("pw", "groupadd", "-n", name, "-g", strconv.Itoa(id))
	default:
		return ErrUnsupported
	}
	if err := command.Run(); err != nil {
		return fmt.Errorf("create service group: %w", err)
	}
	return nil
}

func createUser(name string, id int, homeDir string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		shell := "/sbin/nologin"
		if _, err := os.Stat(shell); err != nil {
			shell = "/usr/sbin/nologin"
		}
		command = exec.Command("useradd", "--system", "--uid", strconv.Itoa(id), "--gid", strconv.Itoa(id), "--home-dir", homeDir, "--no-create-home", "--shell", shell, "--comment", name+" service account", name)
	case "darwin":
		for _, args := range [][]string{
			{".", "-create", "/Users/" + name},
			{".", "-create", "/Users/" + name, "UniqueID", strconv.Itoa(id)},
			{".", "-create", "/Users/" + name, "PrimaryGroupID", strconv.Itoa(id)},
			{".", "-create", "/Users/" + name, "UserShell", "/usr/bin/false"},
			{".", "-create", "/Users/" + name, "RealName", name + " service account"},
			{".", "-create", "/Users/" + name, "NFSHomeDirectory", homeDir},
			{".", "-create", "/Users/" + name, "Password", "*"},
			{".", "-create", "/Users/" + name, "IsHidden", "1"},
		} {
			if err := exec.Command("dscl", args...).Run(); err != nil {
				return fmt.Errorf("create service account: %w", err)
			}
		}
		return nil
	case "freebsd":
		command = exec.Command("pw", "useradd", "-n", name, "-u", strconv.Itoa(id), "-g", strconv.Itoa(id), "-d", homeDir, "-s", "/usr/sbin/nologin", "-c", name+" service account")
	default:
		return ErrUnsupported
	}
	if err := command.Run(); err != nil {
		return fmt.Errorf("create service account: %w", err)
	}
	return nil
}

func isUnknownUser(err error) bool {
	var target user.UnknownUserError
	return errors.As(err, &target)
}

func isUnknownGroup(err error) bool {
	var target user.UnknownGroupError
	return errors.As(err, &target)
}

func isUnknownUID(err error) bool {
	var target user.UnknownUserIdError
	return errors.As(err, &target)
}

func isUnknownGID(err error) bool {
	var target user.UnknownGroupIdError
	return errors.As(err, &target)
}

// DropPrivileges changes the process's real and effective group and user IDs.
func DropPrivileges(identity Identity) error {
	if !accountNamePattern.MatchString(identity.Name) || identity.UID != identity.GID || !validSystemID(identity.UID, runtime.GOOS) {
		return errors.New("invalid service identity")
	}
	if os.Geteuid() != 0 {
		return nil
	}
	if err := syscall.Setgroups([]int{}); err != nil {
		return fmt.Errorf("clear supplementary groups: %w", err)
	}
	if err := syscall.Setgid(identity.GID); err != nil {
		return fmt.Errorf("drop service group privileges: %w", err)
	}
	if err := syscall.Setuid(identity.UID); err != nil {
		return fmt.Errorf("drop service user privileges: %w", err)
	}
	if os.Getuid() == 0 || os.Geteuid() == 0 || os.Getgid() != identity.GID || os.Getegid() != identity.GID {
		return errors.New("service privilege drop could not be verified")
	}
	return nil
}
