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

func Check(ctx context.Context, client *http.Client, releasesURL, branch, currentVersion string, buildEpoch int64) (*Release, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
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
	defer response.Body.Close()
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
	candidate := selectRelease(releases, normalizeBranch(branch))
	if candidate == nil || !newer(candidate, currentVersion, buildEpoch, normalizeBranch(branch)) {
		return nil, nil
	}
	return candidate, nil
}

func selectRelease(releases []Release, branch string) *Release {
	var selected *Release
	for index := range releases {
		release := &releases[index]
		if release.Draft || !eligible(release, branch) {
			continue
		}
		if selected == nil || release.PublishedAt.After(selected.PublishedAt) {
			selected = release
		}
	}
	return selected
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

func Install(ctx context.Context, client *http.Client, release *Release, binaryName, currentPath string) error {
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
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
		return fmt.Errorf("release %s is missing %s or sha256.txt", release.TagName, binaryName)
	}
	checksum, err := downloadChecksum(ctx, client, checksumAsset.URL, binaryName)
	if err != nil {
		return err
	}
	root := filepath.Join(os.TempDir(), "apimgr")
	if err := os.MkdirAll(root, 0700); err != nil {
		return fmt.Errorf("create updater temp directory: %w", err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		return fmt.Errorf("secure updater temp directory: %w", err)
	}
	tempDir, err := os.MkdirTemp(root, "countries-*")
	if err != nil {
		return fmt.Errorf("create updater workspace: %w", err)
	}
	defer os.RemoveAll(tempDir)
	if err := os.Chmod(tempDir, 0700); err != nil {
		return fmt.Errorf("secure updater workspace: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, binaryAsset.URL, nil)
	if err != nil {
		return fmt.Errorf("create binary download request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download update binary: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("binary download returned HTTP %d", response.StatusCode)
	}
	newBinary := filepath.Join(tempDir, "update.tmp")
	file, err := os.OpenFile(newBinary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return fmt.Errorf("create update file: %w", err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, maxUpdateBinary+1))
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("save update binary: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close update binary: %w", closeErr)
	}
	if written == 0 || written > maxUpdateBinary {
		return fmt.Errorf("update binary has invalid size %d", written)
	}
	if actual := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(actual, checksum) {
		return fmt.Errorf("update checksum mismatch: expected %s, got %s", checksum, actual)
	}
	return replaceBinary(currentPath, newBinary)
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
	defer response.Body.Close()
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
