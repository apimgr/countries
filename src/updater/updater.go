// Package updater checks release metadata and verifies binary update assets.
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/apimgr/countries/src/paths"
)

const maxReleaseResponse = 8 << 20
const maxUpdateBinary = 256 << 20

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type Release struct {
	TagName     string    `json:"tag_name"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []Asset   `json:"assets"`
}

const (
	stagedBinaryName = "countries.new"
	stagedHashName   = "countries.sha256"
	stagedVersion    = "version"
	stagedPublished  = "published_at"
)

func Check(ctx context.Context, client *http.Client, releasesURL, branch, currentVersion string, buildEpoch int64) (*Release, error) {
	return CheckWithDefer(ctx, client, releasesURL, branch, currentVersion, buildEpoch, 0, time.Now())
}

// CheckWithDefer selects the newest eligible release after applying the
// scheduled update deferral window. The devel channel intentionally ignores it.
func CheckWithDefer(ctx context.Context, client *http.Client, releasesURL, branch, currentVersion string, buildEpoch int64, deferDays int, now time.Time) (*Release, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	branch = normalizeBranch(branch)
	if deferDays < 0 {
		deferDays = 0
	}
	if branch == "devel" {
		deferDays = 0
	}
	if now.IsZero() {
		now = time.Now()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create release request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "countries-updater")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("query releases: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release query returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxReleaseResponse+1))
	if err != nil {
		return nil, fmt.Errorf("read release response: %w", err)
	}
	if len(body) > maxReleaseResponse {
		return nil, errors.New("release response exceeds size limit")
	}
	var releases []Release
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, fmt.Errorf("decode release response: %w", err)
	}
	cutoff := now.UTC().Add(-time.Duration(deferDays) * 24 * time.Hour)
	var candidate *Release
	for index := range releases {
		release := &releases[index]
		if release.Draft || !eligible(release, branch) || (deferDays > 0 && (release.PublishedAt.IsZero() || release.PublishedAt.After(cutoff))) {
			continue
		}
		if !newer(release, currentVersion, buildEpoch, branch) {
			continue
		}
		if candidate == nil || release.PublishedAt.After(candidate.PublishedAt) {
			candidate = release
		}
	}
	if candidate == nil {
		return nil, nil
	}
	return candidate, nil
}

func eligible(release *Release, branch string) bool {
	tag := strings.TrimPrefix(strings.ToLower(release.TagName), "v")
	beta := strings.HasSuffix(tag, "-beta") || strings.Contains(tag, "-beta.")
	stable := !release.Prerelease && !beta && tag != "daily" && tag != "devel"
	switch branch {
	case "stable":
		return stable
	case "beta":
		return stable || beta
	default:
		return true
	}
}

func newer(release *Release, currentVersion string, buildEpoch int64, branch string) bool {
	if branch == "devel" && (strings.EqualFold(release.TagName, "daily") || strings.EqualFold(release.TagName, "devel")) {
		return release.PublishedAt.Unix() > buildEpoch
	}
	return compareVersions(release.TagName, currentVersion) > 0
}

func compareVersions(left, right string) int {
	left = strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(left, "v")), "v")
	right = strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(right, "v")), "v")
	leftBase, leftPrerelease, leftIsPrerelease := strings.Cut(left, "-")
	rightBase, rightPrerelease, rightIsPrerelease := strings.Cut(right, "-")
	leftParts, rightParts := versionParts(leftBase), versionParts(rightBase)
	length := len(leftParts)
	if len(rightParts) > length {
		length = len(rightParts)
	}
	for index := 0; index < length; index++ {
		var a, b int
		if index < len(leftParts) {
			a = leftParts[index]
		}
		if index < len(rightParts) {
			b = rightParts[index]
		}
		if a > b {
			return 1
		}
		if a < b {
			return -1
		}
	}
	if leftIsPrerelease != rightIsPrerelease {
		if leftIsPrerelease {
			return -1
		}
		return 1
	}
	return strings.Compare(leftPrerelease, rightPrerelease)
}

func versionParts(version string) []int {
	parts := make([]int, 0)
	for _, part := range strings.FieldsFunc(version, func(r rune) bool { return r < '0' || r > '9' }) {
		var value int
		for _, digit := range part {
			value = value*10 + int(digit-'0')
		}
		parts = append(parts, value)
	}
	return parts
}

func normalizeBranch(branch string) string {
	switch strings.ToLower(strings.TrimSpace(branch)) {
	case "beta":
		return "beta"
	case "devel", "daily", "rolling", "dev", "development":
		return "devel"
	default:
		return "stable"
	}
}

func BinaryName(projectName, suffix string) string {
	name := fmt.Sprintf("%s-%s-%s", projectName, runtime.GOOS, runtime.GOARCH)
	if suffix != "" {
		name = projectName + "-cli-" + runtime.GOOS + "-" + runtime.GOARCH
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func Install(ctx context.Context, client *http.Client, release *Release, binaryName, internalName, currentPath string) error {
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	tempDir, err := paths.CreateTempDir(internalName)
	if err != nil {
		return fmt.Errorf("create updater workspace: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()
	newBinary, _, err := downloadVerifiedBinary(ctx, client, release, binaryName, tempDir)
	if err != nil {
		return err
	}
	return replaceBinary(currentPath, newBinary)
}

// Stage downloads a release into a private data directory without replacing
// the running binary.
func Stage(ctx context.Context, client *http.Client, release *Release, binaryName, internalName, dataDir string) error {
	if release == nil {
		return errors.New("release is unavailable")
	}
	if isRollingVersion(release.TagName) && release.PublishedAt.IsZero() {
		return errors.New("rolling release has no published timestamp")
	}
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	tempDir, err := paths.CreateTempDir(internalName)
	if err != nil {
		return fmt.Errorf("create updater workspace: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()
	newBinary, checksum, err := downloadVerifiedBinary(ctx, client, release, binaryName, tempDir)
	if err != nil {
		return err
	}
	updateDir := filepath.Join(dataDir, "update")
	if err := os.MkdirAll(updateDir, 0700); err != nil {
		return fmt.Errorf("create staged update directory: %w", err)
	}
	if err := os.Chmod(updateDir, 0700); err != nil {
		return fmt.Errorf("secure staged update directory: %w", err)
	}
	stagedBinary := filepath.Join(updateDir, stagedBinaryName)
	hashPath := filepath.Join(updateDir, stagedHashName)
	versionPath := filepath.Join(updateDir, stagedVersion)
	binaryTemp, err := os.CreateTemp(updateDir, ".countries.new-*")
	if err != nil {
		return fmt.Errorf("create staged binary: %w", err)
	}
	binaryTempPath := binaryTemp.Name()
	defer func() { _ = os.Remove(binaryTempPath) }()
	if err := binaryTemp.Chmod(0700); err != nil {
		_ = binaryTemp.Close()
		return fmt.Errorf("set staged binary permissions: %w", err)
	}
	source, err := os.Open(newBinary)
	if err != nil {
		_ = binaryTemp.Close()
		return fmt.Errorf("open verified binary: %w", err)
	}
	_, copyErr := io.Copy(binaryTemp, source)
	sourceCloseErr := source.Close()
	syncErr := binaryTemp.Sync()
	closeErr := binaryTemp.Close()
	if copyErr != nil {
		return fmt.Errorf("write staged binary: %w", copyErr)
	}
	if sourceCloseErr != nil {
		return fmt.Errorf("close verified binary: %w", sourceCloseErr)
	}
	if syncErr != nil {
		return fmt.Errorf("sync staged binary: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close staged binary: %w", closeErr)
	}
	if err := os.Rename(binaryTempPath, stagedBinary); err != nil {
		return fmt.Errorf("install staged binary: %w", err)
	}
	if err := writePrivateAtomic(hashPath, []byte(checksum+"\n")); err != nil {
		_ = os.Remove(stagedBinary)
		return fmt.Errorf("write staged checksum: %w", err)
	}
	if err := writePrivateAtomic(versionPath, []byte(release.TagName+"\n")); err != nil {
		_ = os.Remove(stagedBinary)
		_ = os.Remove(hashPath)
		return fmt.Errorf("write staged version: %w", err)
	}
	if err := writePrivateAtomic(filepath.Join(updateDir, stagedPublished), []byte(release.PublishedAt.UTC().Format(time.RFC3339Nano)+"\n")); err != nil {
		_ = os.Remove(stagedBinary)
		_ = os.Remove(hashPath)
		_ = os.Remove(versionPath)
		return fmt.Errorf("write staged release timestamp: %w", err)
	}
	return nil
}

// ApplyStaged validates and installs a staged update when the process has
// permission to replace the installed binary.
func ApplyStaged(dataDir, currentPath, currentVersion string, currentBuildEpoch int64) (bool, error) {
	if !CanApplyStaged() {
		return false, nil
	}
	updateDir := filepath.Join(dataDir, "update")
	versionBytes, err := os.ReadFile(filepath.Join(updateDir, stagedVersion))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, discardStaged(updateDir, fmt.Errorf("read staged update version: %w", err))
	}
	metadataVersion := strings.TrimSpace(string(versionBytes))
	hashBytes, err := os.ReadFile(filepath.Join(updateDir, stagedHashName))
	if err != nil {
		return false, discardStaged(updateDir, fmt.Errorf("read staged update checksum: %w", err))
	}
	checksum := strings.TrimSpace(string(hashBytes))
	if metadataVersion == "" || len(checksum) != sha256.Size*2 {
		return false, discardStaged(updateDir, errors.New("staged update metadata is invalid"))
	}
	if isRollingVersion(metadataVersion) {
		publishedBytes, publishedErr := os.ReadFile(filepath.Join(updateDir, stagedPublished))
		publishedAt, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(publishedBytes)))
		if publishedErr != nil || parseErr != nil || publishedAt.Unix() <= currentBuildEpoch {
			return false, discardStaged(updateDir, errors.New("staged rolling release is not newer than the running build"))
		}
	} else if compareVersions(metadataVersion, currentVersion) <= 0 {
		return false, discardStaged(updateDir, fmt.Errorf("staged version %q is not newer than %q", metadataVersion, currentVersion))
	}
	stagedBinary := filepath.Join(updateDir, stagedBinaryName)
	if err := verifyChecksum(stagedBinary, checksum); err != nil {
		return false, discardStaged(updateDir, err)
	}
	if err := replaceBinary(currentPath, stagedBinary); err != nil {
		return false, discardStaged(updateDir, err)
	}
	if err := os.RemoveAll(updateDir); err != nil {
		return false, fmt.Errorf("remove applied staged update: %w", err)
	}
	return true, nil
}

func isRollingVersion(version string) bool {
	return strings.EqualFold(strings.TrimSpace(version), "devel") || strings.EqualFold(strings.TrimSpace(version), "daily")
}

// StagedVersion returns the staged release identifier when one is present.
func StagedVersion(dataDir string) (string, error) {
	content, err := os.ReadFile(filepath.Join(dataDir, "update", stagedVersion))
	if err != nil {
		return "", err
	}
	version := strings.TrimSpace(string(content))
	if version == "" || len(version) > 128 || strings.ContainsAny(version, "\\/\x00\r\n") {
		return "", errors.New("staged version metadata is invalid")
	}
	return version, nil
}

func writePrivateAtomic(path string, contents []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".staged-metadata-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func discardStaged(updateDir string, cause error) error {
	if err := os.RemoveAll(updateDir); err != nil {
		return errors.Join(cause, fmt.Errorf("remove invalid staged update: %w", err))
	}
	return cause
}

func downloadVerifiedBinary(ctx context.Context, client *http.Client, release *Release, binaryName, tempDir string) (string, string, error) {
	if release == nil {
		return "", "", errors.New("release is unavailable")
	}
	var binaryAsset, checksumAsset *Asset
	for index := range release.Assets {
		asset := &release.Assets[index]
		if asset.Name == binaryName {
			binaryAsset = asset
		}
		if asset.Name == "sha256.txt" {
			checksumAsset = asset
		}
	}
	if binaryAsset == nil || checksumAsset == nil {
		return "", "", fmt.Errorf("release %s is missing %s or sha256.txt", release.TagName, binaryName)
	}
	checksum, err := downloadChecksum(ctx, client, checksumAsset.URL, binaryName)
	if err != nil {
		return "", "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, binaryAsset.URL, nil)
	if err != nil {
		return "", "", fmt.Errorf("create binary download request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return "", "", fmt.Errorf("download update binary: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("binary download returned HTTP %d", response.StatusCode)
	}
	newBinary := filepath.Join(tempDir, "update.tmp")
	file, err := os.OpenFile(newBinary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return "", "", fmt.Errorf("create update file: %w", err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, maxUpdateBinary+1))
	closeErr := file.Close()
	if copyErr != nil {
		return "", "", fmt.Errorf("save update binary: %w", copyErr)
	}
	if closeErr != nil {
		return "", "", fmt.Errorf("close update binary: %w", closeErr)
	}
	if written == 0 || written > maxUpdateBinary {
		return "", "", fmt.Errorf("update binary has invalid size %d", written)
	}
	if actual := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(actual, checksum) {
		return "", "", fmt.Errorf("update checksum mismatch: expected %s, got %s", checksum, actual)
	}
	return newBinary, checksum, nil
}

func verifyChecksum(filePath, expectedHash string) error {
	if _, err := hex.DecodeString(expectedHash); err != nil {
		return errors.New("staged update checksum is invalid")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open staged update binary: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect staged update binary: %w", err)
	}
	if info.Size() <= 0 || info.Size() > maxUpdateBinary {
		return fmt.Errorf("staged update binary has invalid size %d", info.Size())
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("hash staged update binary: %w", err)
	}
	if actual := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(actual, expectedHash) {
		return fmt.Errorf("staged update checksum mismatch: expected %s, got %s", expectedHash, actual)
	}
	return nil
}

func downloadChecksum(ctx context.Context, client *http.Client, url, binaryName string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create checksum request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("download checksums: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksum download returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read checksums: %w", err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == binaryName && len(fields[0]) == sha256.Size*2 {
			if _, err := hex.DecodeString(fields[0]); err == nil {
				return fields[0], nil
			}
		}
	}
	return "", fmt.Errorf("sha256.txt has no checksum for %s", binaryName)
}
