// Package releaseasset defines the stable GitHub Release contract used by
// release automation and self-update clients.
package releaseasset

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

const (
	// LatestReleaseURL is GitHub's endpoint for the latest non-draft,
	// non-prerelease release in the canonical repository.
	LatestReleaseURL = "https://api.github.com/repos/mattrandles/wtproj/releases/latest"

	// GitHubAccept and GitHubAPIVersion make requests to LatestReleaseURL
	// explicit and reproducible.
	GitHubAccept     = "application/vnd.github+json"
	GitHubAPIVersion = "2022-11-28"

	// ChecksumAssetName is the release asset containing SHA-256 metadata.
	ChecksumAssetName       = "checksums.txt"
	ChecksumAlgorithm       = "sha256"
	ChecksumDigestHexLength = 64
	checksumSeparator       = "  "
)

// Platform identifies one supported Go build target and its release asset.
type Platform struct {
	GOOS      string
	GOARCH    string
	AssetName string
}

var platforms = []Platform{
	{GOOS: "darwin", GOARCH: "amd64", AssetName: "wtp_darwin_amd64"},
	{GOOS: "darwin", GOARCH: "arm64", AssetName: "wtp_darwin_arm64"},
	{GOOS: "linux", GOARCH: "amd64", AssetName: "wtp_linux_amd64"},
	{GOOS: "linux", GOARCH: "arm64", AssetName: "wtp_linux_arm64"},
	{GOOS: "windows", GOARCH: "amd64", AssetName: "wtp_windows_amd64.exe"},
	{GOOS: "windows", GOARCH: "arm64", AssetName: "wtp_windows_arm64.exe"},
}

// Platforms returns a copy of the complete supported release matrix.
func Platforms() []Platform {
	result := make([]Platform, len(platforms))
	copy(result, platforms)
	return result
}

// AssetName returns the exact GitHub Release asset for a Go platform.
func AssetName(goos, goarch string) (string, error) {
	for _, platform := range platforms {
		if platform.GOOS == goos && platform.GOARCH == goarch {
			return platform.AssetName, nil
		}
	}
	return "", fmt.Errorf("unsupported release platform %s/%s", goos, goarch)
}

// ParseChecksums parses the checksums.txt sha256sum format. Keys are exact
// release asset names and values are lowercase hexadecimal SHA-256 digests.
func ParseChecksums(r io.Reader) (map[string]string, error) {
	checksums := make(map[string]string)
	scanner := bufio.NewScanner(r)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := scanner.Text()
		digest, assetName, ok := strings.Cut(line, checksumSeparator)
		if !ok || assetName == "" || strings.Contains(assetName, checksumSeparator) {
			return nil, fmt.Errorf("invalid checksum line %d", lineNumber)
		}
		if len(digest) != ChecksumDigestHexLength || strings.ToLower(digest) != digest {
			return nil, fmt.Errorf("invalid sha256 digest on checksum line %d", lineNumber)
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return nil, fmt.Errorf("invalid sha256 digest on checksum line %d: %w", lineNumber, err)
		}
		if _, exists := checksums[assetName]; exists {
			return nil, fmt.Errorf("duplicate checksum for %q", assetName)
		}
		checksums[assetName] = digest
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read checksums: %w", err)
	}
	if len(checksums) == 0 {
		return nil, fmt.Errorf("checksum file is empty")
	}
	return checksums, nil
}

// GitHubRelease is the part of GitHub's latest-release response consumed by an
// update client.
type GitHubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []GitHubAsset `json:"assets"`
}

// GitHubAsset is a downloadable asset in a GitHub Release response.
type GitHubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}
