package paths

import (
	"errors"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var (
	ErrPathTraversal = errors.New("path traversal attempt detected")
	ErrInvalidPath   = errors.New("invalid path characters")
	ErrPathTooLong   = errors.New("path exceeds maximum length")
	validPathSegment = regexp.MustCompile(`^[a-z0-9_-]+$`)
	validFileSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// SafePath normalizes and validates a URL prefix or configured path identifier.
func SafePath(input string) (string, error) {
	if len(input) > 2048 {
		return "", ErrPathTooLong
	}
	if strings.Contains(input, "..") {
		return "", ErrPathTraversal
	}
	cleaned := strings.Trim(path.Clean(input), "/")
	if cleaned == "." || cleaned == "" {
		if input == "" || strings.Trim(input, "/") == "" {
			return "", nil
		}
	}
	for _, segment := range strings.Split(cleaned, "/") {
		if segment == "" {
			continue
		}
		if len(segment) > 64 {
			return "", ErrPathTooLong
		}
		if !validPathSegment.MatchString(segment) {
			return "", ErrInvalidPath
		}
	}
	return cleaned, nil
}

// SafeResourcePath validates a relative file or resource path without rewriting names.
func SafeResourcePath(input string) (string, error) {
	if len(input) > 2048 || strings.Contains(input, `\`) {
		return "", ErrPathTraversal
	}
	var kept []string
	for _, segment := range strings.Split(strings.Trim(input, "/"), "/") {
		if segment == "" {
			continue
		}
		if len(segment) > 255 || !validFileSegment.MatchString(segment) {
			return "", ErrInvalidPath
		}
		kept = append(kept, segment)
	}
	if len(kept) == 0 {
		return "", ErrInvalidPath
	}
	return strings.Join(kept, "/"), nil
}

// SafeDirPath validates a filesystem directory override and preserves absolute paths.
func SafeDirPath(input string) (string, error) {
	if input == "" {
		return "", errors.New("empty path")
	}
	for _, r := range input {
		if unicode.IsControl(r) {
			return "", ErrInvalidPath
		}
	}
	for _, segment := range strings.Split(filepath.ToSlash(input), "/") {
		if segment == ".." {
			return "", ErrPathTraversal
		}
	}
	return filepath.Clean(input), nil
}

// SafeFilePath validates a resource name and ensures its absolute path stays under baseDir.
func SafeFilePath(baseDir, userPath string) (string, error) {
	safe, err := SafeResourcePath(userPath)
	if err != nil {
		return "", err
	}
	fullPath, err := filepath.Abs(filepath.Join(baseDir, filepath.FromSlash(safe)))
	if err != nil {
		return "", err
	}
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(fullPath, absBase+string(filepath.Separator)) && fullPath != absBase {
		return "", ErrPathTraversal
	}
	return fullPath, nil
}
