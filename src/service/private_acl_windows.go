//go:build windows

package service

import (
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

func enforcePrivateACL(configDir, dataDir string) error {
	serviceSID := serviceAccountSID(appName)
	descriptorString, err := privateACLDescriptor(serviceSID)
	if err != nil {
		return err
	}
	descriptor, err := windows.SecurityDescriptorFromString(descriptorString)
	if err != nil {
		return fmt.Errorf("build protected Windows service ACL: %w", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("build protected Windows service ACL: %w", err)
	}
	privateRoots := []string{
		configDir,
		filepath.Join(dataDir, "security"),
		filepath.Join(dataDir, "tor"),
	}
	for _, root := range privateRoots {
		if strings.TrimSpace(root) == "" {
			return errors.New("private Windows path is empty")
		}
		if err := ensureProtectedDirectory(root, descriptor, dacl); err != nil {
			return err
		}
		if err := applyProtectedTree(root, dacl); err != nil {
			return err
		}
	}
	return nil
}

func ensureProtectedDirectory(path string, descriptor *windows.SECURITY_DESCRIPTOR, dacl *windows.ACL) error {
	path = filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent for private path %s: %w", path, err)
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		pathPtr, convertErr := windows.UTF16PtrFromString(path)
		if convertErr != nil {
			return fmt.Errorf("prepare private path %s: %w", path, convertErr)
		}
		attributes := &windows.SecurityAttributes{
			Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
			SecurityDescriptor: descriptor,
		}
		if createErr := windows.CreateDirectory(pathPtr, attributes); createErr != nil && !errors.Is(createErr, windows.ERROR_ALREADY_EXISTS) {
			return fmt.Errorf("create private path %s with protected ACL: %w", path, createErr)
		}
	} else if err != nil {
		return fmt.Errorf("inspect private path %s: %w", path, err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect private path %s: %w", path, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private path %s is not a directory (mode %s)", path, info.Mode())
	}
	return setProtectedACL(path, dacl)
}

func applyProtectedTree(root string, dacl *windows.ACL) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("inspect private path %s: %w", path, walkErr)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("inspect private path %s: %w", path, err)
		}
		mode := info.Mode()
		if mode&os.ModeSymlink != 0 || !mode.IsDir() && !mode.IsRegular() {
			return fmt.Errorf("private path %s has unsupported mode %s", path, mode)
		}
		return setProtectedACL(path, dacl)
	})
}

func setProtectedACL(path string, dacl *windows.ACL) error {
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		return fmt.Errorf("cannot enforce private ACL on %s: %w", path, err)
	}
	return nil
}

func serviceAccountSID(serviceName string) string {
	wide := utf16.Encode([]rune(strings.ToUpper(serviceName)))
	encoded := make([]byte, len(wide)*2)
	for index, unit := range wide {
		binary.LittleEndian.PutUint16(encoded[index*2:], unit)
	}
	digest := sha1.Sum(encoded)
	parts := make([]string, 5)
	for index := range parts {
		parts[index] = fmt.Sprint(binary.LittleEndian.Uint32(digest[index*4 : index*4+4]))
	}
	return "S-1-5-80-" + strings.Join(parts, "-")
}
