// Package updater implements the network and filesystem portions of wtp's
// checksum-verified self-update command.
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
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mattrandles/wtproj/internal/releaseasset"
)

const (
	maximumReleaseResponseSize = 2 << 20
	maximumChecksumSize        = 1 << 20
	maximumExecutableSize      = 128 << 20
)

// latestReleaseURL and allowInsecureHTTP are linker-overridable only for the
// release QA snapshot harness. Production GoReleaser builds leave both at
// their safe defaults: the canonical GitHub HTTPS endpoint and no HTTP.
//
// The harness embeds a loopback HTTP fixture URL into throwaway snapshot
// binaries; accepting that URL at runtime keeps the test fully external to
// the updater and exercises the same CLI command users run.
var (
	latestReleaseURL  = releaseasset.LatestReleaseURL
	allowInsecureHTTP = "false"
)

// Result describes the outcome of one update check.
type Result struct {
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	Path           string `json:"path,omitempty"`
	Updated        bool   `json:"updated"`
	Scheduled      bool   `json:"scheduled,omitempty"`
}

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type dependencies struct {
	httpClient       httpDoer
	latestReleaseURL string
	goos             string
	goarch           string
	executable       func() (string, error)
	evalSymlinks     func(string) (string, error)
	replace          func(string, string) (bool, error)
	allowHTTP        bool
}

// Run queries the canonical latest-release endpoint and updates the currently
// running executable when a newer release is available.
func Run(ctx context.Context, currentVersion string) (Result, error) {
	allowHTTP := allowInsecureHTTP == "true" && loopbackHTTPURL(latestReleaseURL)
	return run(ctx, currentVersion, dependencies{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(request *http.Request, _ []*http.Request) error {
				return validateDownloadURL(request.URL.String(), allowHTTP)
			},
		},
		latestReleaseURL: latestReleaseURL,
		goos:             runtime.GOOS,
		goarch:           runtime.GOARCH,
		executable:       os.Executable,
		evalSymlinks:     filepath.EvalSymlinks,
		replace:          replaceExecutable,
		allowHTTP:        allowHTTP,
	})
}

func loopbackHTTPURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "http" {
		return false
	}
	host := parsed.Hostname()
	return host == "127.0.0.1" || host == "::1"
}

func run(ctx context.Context, currentVersion string, deps dependencies) (Result, error) {
	result := Result{CurrentVersion: currentVersion}
	assetName, err := releaseasset.AssetName(deps.goos, deps.goarch)
	if err != nil {
		return result, err
	}
	current, err := parseSemanticVersion(currentVersion)
	if err != nil {
		if currentVersion == "dev" {
			return result, errors.New("development builds cannot be updated automatically; install a released wtp binary first")
		}
		return result, fmt.Errorf("current binary version: %w", err)
	}

	release, err := fetchLatestRelease(ctx, deps, currentVersion)
	if err != nil {
		return result, err
	}
	if !strings.HasPrefix(release.TagName, "v") {
		return result, fmt.Errorf("latest release tag %q must start with v", release.TagName)
	}
	result.LatestVersion = strings.TrimPrefix(release.TagName, "v")
	latest, err := parseSemanticVersion(result.LatestVersion)
	if err != nil {
		return result, fmt.Errorf("latest release tag %q: %w", release.TagName, err)
	}
	if compareSemanticVersions(latest, current) <= 0 {
		return result, nil
	}

	binaryAsset, err := findUniqueAsset(release.Assets, assetName)
	if err != nil {
		return result, err
	}
	checksumAsset, err := findUniqueAsset(release.Assets, releaseasset.ChecksumAssetName)
	if err != nil {
		return result, err
	}

	executablePath, err := resolveExecutable(deps)
	if err != nil {
		return result, err
	}
	result.Path = executablePath
	targetInfo, err := os.Stat(executablePath)
	if err != nil {
		return result, fmt.Errorf("inspect installed executable %s: %w", executablePath, err)
	}
	if !targetInfo.Mode().IsRegular() {
		return result, fmt.Errorf("installed executable %s is not a regular file", executablePath)
	}

	checksumData, err := downloadBytes(ctx, deps, checksumAsset.BrowserDownloadURL, maximumChecksumSize, "checksum metadata")
	if err != nil {
		return result, err
	}
	checksums, err := releaseasset.ParseChecksums(strings.NewReader(string(checksumData)))
	if err != nil {
		return result, fmt.Errorf("parse published checksums: %w", err)
	}
	expectedDigest, ok := checksums[assetName]
	if !ok {
		return result, fmt.Errorf("published checksums do not contain %q", assetName)
	}

	stage, err := os.CreateTemp(filepath.Dir(executablePath), ".wtp-update-*")
	if err != nil {
		return result, permissionError("create update staging file", executablePath, err)
	}
	stagePath := stage.Name()
	keepStage := false
	defer func() {
		if !keepStage {
			_ = os.Remove(stagePath)
		}
	}()

	actualDigest, err := downloadExecutable(ctx, deps, binaryAsset.BrowserDownloadURL, stage)
	if err != nil {
		_ = stage.Close()
		return result, err
	}
	if actualDigest != expectedDigest {
		_ = stage.Close()
		return result, fmt.Errorf("checksum verification failed for %s: got %s, want %s", assetName, actualDigest, expectedDigest)
	}
	if err := stage.Chmod(targetInfo.Mode().Perm()); err != nil {
		_ = stage.Close()
		return result, fmt.Errorf("preserve permissions on staged update: %w", err)
	}
	if err := stage.Sync(); err != nil {
		_ = stage.Close()
		return result, fmt.Errorf("sync staged update: %w", err)
	}
	if err := stage.Close(); err != nil {
		return result, fmt.Errorf("close staged update: %w", err)
	}

	scheduled, err := deps.replace(stagePath, executablePath)
	if err != nil {
		return result, permissionError("replace installed executable", executablePath, err)
	}
	faultPoint("update-replacement")
	keepStage = scheduled
	result.Updated = !scheduled
	result.Scheduled = scheduled
	return result, nil
}

func fetchLatestRelease(ctx context.Context, deps dependencies, currentVersion string) (releaseasset.GitHubRelease, error) {
	if err := validateDownloadURL(deps.latestReleaseURL, deps.allowHTTP); err != nil {
		return releaseasset.GitHubRelease{}, fmt.Errorf("latest-release URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, deps.latestReleaseURL, nil)
	if err != nil {
		return releaseasset.GitHubRelease{}, fmt.Errorf("create latest-release request: %w", err)
	}
	req.Header.Set("Accept", releaseasset.GitHubAccept)
	req.Header.Set("X-GitHub-Api-Version", releaseasset.GitHubAPIVersion)
	req.Header.Set("User-Agent", "wtp/"+currentVersion)

	response, err := deps.httpClient.Do(req)
	if err != nil {
		return releaseasset.GitHubRelease{}, fmt.Errorf("query latest GitHub release: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return releaseasset.GitHubRelease{}, fmt.Errorf("query latest GitHub release: unexpected HTTP status %s", response.Status)
	}

	limited := io.LimitReader(response.Body, maximumReleaseResponseSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return releaseasset.GitHubRelease{}, fmt.Errorf("read latest GitHub release: %w", err)
	}
	if len(data) > maximumReleaseResponseSize {
		return releaseasset.GitHubRelease{}, errors.New("latest GitHub release response is too large")
	}
	var release releaseasset.GitHubRelease
	if err := json.Unmarshal(data, &release); err != nil {
		return releaseasset.GitHubRelease{}, fmt.Errorf("decode latest GitHub release: %w", err)
	}
	if strings.TrimSpace(release.TagName) == "" {
		return releaseasset.GitHubRelease{}, errors.New("latest GitHub release response has no tag_name")
	}
	return release, nil
}

func findUniqueAsset(assets []releaseasset.GitHubAsset, name string) (releaseasset.GitHubAsset, error) {
	var found releaseasset.GitHubAsset
	count := 0
	for _, asset := range assets {
		if asset.Name == name {
			found = asset
			count++
		}
	}
	if count == 0 {
		return releaseasset.GitHubAsset{}, fmt.Errorf("latest release is missing required asset %q", name)
	}
	if count != 1 {
		return releaseasset.GitHubAsset{}, fmt.Errorf("latest release contains duplicate asset %q", name)
	}
	if strings.TrimSpace(found.BrowserDownloadURL) == "" {
		return releaseasset.GitHubAsset{}, fmt.Errorf("latest release asset %q has no download URL", name)
	}
	return found, nil
}

func resolveExecutable(deps dependencies) (string, error) {
	path, err := deps.executable()
	if err != nil {
		return "", fmt.Errorf("locate running executable: %w", err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve running executable path: %w", err)
	}
	resolved, err := deps.evalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve installed executable %s: %w", path, err)
	}
	return resolved, nil
}

func downloadBytes(ctx context.Context, deps dependencies, sourceURL string, maximumSize int64, description string) ([]byte, error) {
	response, err := getDownload(ctx, deps, sourceURL, description)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maximumSize+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", description, err)
	}
	if int64(len(data)) > maximumSize {
		return nil, fmt.Errorf("%s exceeds the %d-byte safety limit", description, maximumSize)
	}
	return data, nil
}

func downloadExecutable(ctx context.Context, deps dependencies, sourceURL string, destination io.Writer) (string, error) {
	response, err := getDownload(ctx, deps, sourceURL, "release executable")
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(destination, hash), io.LimitReader(response.Body, maximumExecutableSize+1))
	if err != nil {
		return "", fmt.Errorf("download release executable: %w", err)
	}
	if written > maximumExecutableSize {
		return "", fmt.Errorf("release executable exceeds the %d-byte safety limit", maximumExecutableSize)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func getDownload(ctx context.Context, deps dependencies, sourceURL, description string) (*http.Response, error) {
	if err := validateDownloadURL(sourceURL, deps.allowHTTP); err != nil {
		return nil, fmt.Errorf("%s URL: %w", description, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create %s request: %w", description, err)
	}
	req.Header.Set("Accept", "application/octet-stream")
	response, err := deps.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", description, err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("download %s: unexpected HTTP status %s", description, response.Status)
	}
	return response, nil
}

func validateDownloadURL(rawURL string, allowHTTP bool) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if parsed.User != nil || parsed.Host == "" {
		return errors.New("must be an absolute URL without user information")
	}
	if parsed.Scheme != "https" && !(allowHTTP && parsed.Scheme == "http") {
		return errors.New("must use HTTPS")
	}
	return nil
}

func permissionError(action, path string, err error) error {
	return fmt.Errorf("%s %s: %w; ensure you have write permission for the executable and its directory", action, path, err)
}
