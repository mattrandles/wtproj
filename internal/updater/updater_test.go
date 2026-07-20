package updater

import (
	"bytes"
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
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/releaseasset"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) Do(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestLoopbackHTTPURL(t *testing.T) {
	for rawURL, want := range map[string]bool{
		"http://127.0.0.1:8080/releases/latest":  true,
		"http://[::1]:8080/releases/latest":      true,
		"https://127.0.0.1/releases/latest":      false,
		"http://localhost/releases/latest":       false,
		"http://updates.example/releases/latest": false,
		"not a URL":                              false,
	} {
		if got := loopbackHTTPURL(rawURL); got != want {
			t.Errorf("loopbackHTTPURL(%q) = %v, want %v", rawURL, got, want)
		}
	}
}

func TestRunNoOpDoesNotDownloadAssets(t *testing.T) {
	target := writeTestExecutable(t, "current", 0o751)
	requests := []string{}
	deps := baseTestDependencies(target, func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request.URL.Path)
		if got := request.Header.Get("Accept"); got != releaseasset.GitHubAccept {
			t.Fatalf("API Accept header = %q", got)
		}
		if got := request.Header.Get("X-GitHub-Api-Version"); got != releaseasset.GitHubAPIVersion {
			t.Fatalf("API version header = %q", got)
		}
		return jsonResponse(http.StatusOK, releaseasset.GitHubRelease{TagName: "v1.2.3"}), nil
	})

	result, err := run(context.Background(), "1.2.3", deps)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if result.Updated || result.Scheduled || result.LatestVersion != "1.2.3" {
		t.Fatalf("result = %#v", result)
	}
	if len(requests) != 1 || requests[0] != "/latest" {
		t.Fatalf("requests = %v, want only latest-release request", requests)
	}
	assertFile(t, target, "current", 0o751)
}

func TestRunUpgradeSelectsExactAssetVerifiesChecksumAndPreservesPermissions(t *testing.T) {
	target := writeTestExecutable(t, "old executable", 0o751)
	replacement := []byte("new executable")
	digest := sha256Hex(replacement)
	checksums := strings.Repeat("a", 64) + "  wtp_linux_arm64\n" + digest + "  wtp_linux_amd64\n"
	requested := []string{}
	deps := baseTestDependencies(target, func(request *http.Request) (*http.Response, error) {
		requested = append(requested, request.URL.Path)
		switch request.URL.Path {
		case "/latest":
			return jsonResponse(http.StatusOK, testRelease("v1.3.0")), nil
		case "/checksums.txt":
			return byteResponse(http.StatusOK, []byte(checksums)), nil
		case "/wtp_linux_amd64":
			return byteResponse(http.StatusOK, replacement), nil
		default:
			return nil, fmt.Errorf("unexpected request %s", request.URL)
		}
	})

	result, err := run(context.Background(), "1.2.3", deps)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !result.Updated || result.Scheduled || result.Path != target || result.LatestVersion != "1.3.0" {
		t.Fatalf("result = %#v", result)
	}
	assertFile(t, target, string(replacement), 0o751)
	wantRequests := []string{"/latest", "/checksums.txt", "/wtp_linux_amd64"}
	if strings.Join(requested, ",") != strings.Join(wantRequests, ",") {
		t.Fatalf("requests = %v, want %v", requested, wantRequests)
	}
	assertNoUpdateStages(t, filepath.Dir(target))
}

func TestRunChecksumFailureLeavesInstalledExecutableUnchanged(t *testing.T) {
	target := writeTestExecutable(t, "known good", 0o755)
	replacement := []byte("tampered download")
	checksums := strings.Repeat("0", 64) + "  wtp_linux_amd64\n"
	deps := baseTestDependencies(target, releaseServer(testRelease("v2.0.0"), []byte(checksums), replacement))

	_, err := run(context.Background(), "1.0.0", deps)
	if err == nil || !strings.Contains(err.Error(), "checksum verification failed") {
		t.Fatalf("run() error = %v", err)
	}
	assertFile(t, target, "known good", 0o755)
	assertNoUpdateStages(t, filepath.Dir(target))
}

func TestRunUnsupportedPlatformDoesNotUseNetwork(t *testing.T) {
	target := writeTestExecutable(t, "known good", 0o755)
	deps := baseTestDependencies(target, func(request *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected network request: %s", request.URL)
		return nil, errors.New("unreachable")
	})
	deps.goos = "plan9"

	_, err := run(context.Background(), "1.0.0", deps)
	if err == nil || !strings.Contains(err.Error(), "unsupported release platform plan9/amd64") {
		t.Fatalf("run() error = %v", err)
	}
}

func TestRunReportsLatestReleaseAPIFailures(t *testing.T) {
	tests := []struct {
		name    string
		do      roundTripFunc
		wantErr string
	}{
		{
			name: "transport",
			do: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("network disabled")
			},
			wantErr: "query latest GitHub release: network disabled",
		},
		{
			name: "status",
			do: func(*http.Request) (*http.Response, error) {
				return byteResponse(http.StatusServiceUnavailable, []byte("unavailable")), nil
			},
			wantErr: "unexpected HTTP status 503 Service Unavailable",
		},
		{
			name: "invalid JSON",
			do: func(*http.Request) (*http.Response, error) {
				return byteResponse(http.StatusOK, []byte("{")), nil
			},
			wantErr: "decode latest GitHub release",
		},
		{
			name: "invalid tag",
			do: func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, releaseasset.GitHubRelease{TagName: "v01.2.3"}), nil
			},
			wantErr: "not a strict semantic version",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := writeTestExecutable(t, "known good", 0o755)
			deps := baseTestDependencies(target, test.do)
			_, err := run(context.Background(), "1.0.0", deps)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("run() error = %v, want containing %q", err, test.wantErr)
			}
			assertFile(t, target, "known good", 0o755)
		})
	}
}

func TestRunRejectsMissingAndDuplicateRequiredAssets(t *testing.T) {
	tests := []struct {
		name    string
		assets  []releaseasset.GitHubAsset
		wantErr string
	}{
		{
			name: "missing platform executable",
			assets: []releaseasset.GitHubAsset{
				{Name: releaseasset.ChecksumAssetName, BrowserDownloadURL: "https://updates.test/checksums.txt"},
			},
			wantErr: `missing required asset "wtp_linux_amd64"`,
		},
		{
			name: "duplicate checksums",
			assets: []releaseasset.GitHubAsset{
				{Name: "wtp_linux_amd64", BrowserDownloadURL: "https://updates.test/wtp_linux_amd64"},
				{Name: releaseasset.ChecksumAssetName, BrowserDownloadURL: "https://updates.test/checksums-1.txt"},
				{Name: releaseasset.ChecksumAssetName, BrowserDownloadURL: "https://updates.test/checksums-2.txt"},
			},
			wantErr: `duplicate asset "checksums.txt"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := writeTestExecutable(t, "known good", 0o755)
			deps := baseTestDependencies(target, func(request *http.Request) (*http.Response, error) {
				if request.URL.Path != "/latest" {
					t.Fatalf("asset validation unexpectedly downloaded %s", request.URL)
				}
				return jsonResponse(http.StatusOK, releaseasset.GitHubRelease{TagName: "v2.0.0", Assets: test.assets}), nil
			})
			_, err := run(context.Background(), "1.0.0", deps)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("run() error = %v, want containing %q", err, test.wantErr)
			}
			assertFile(t, target, "known good", 0o755)
		})
	}
}

func TestRunReplacementFailureRollsBackSafely(t *testing.T) {
	target := writeTestExecutable(t, "known good", 0o700)
	replacement := []byte("verified update")
	checksums := sha256Hex(replacement) + "  wtp_linux_amd64\n"
	deps := baseTestDependencies(target, releaseServer(testRelease("v1.1.0"), []byte(checksums), replacement))
	deps.replace = func(source, destination string) (bool, error) {
		if destination != target {
			t.Fatalf("replacement target = %q, want %q", destination, target)
		}
		assertFile(t, target, "known good", 0o700)
		if _, err := os.Stat(source); err != nil {
			t.Fatalf("staged update missing before replacement: %v", err)
		}
		return false, errors.New("injected atomic replacement failure")
	}

	_, err := run(context.Background(), "1.0.0", deps)
	if err == nil || !strings.Contains(err.Error(), "injected atomic replacement failure") || !strings.Contains(err.Error(), "write permission") {
		t.Fatalf("run() error = %v", err)
	}
	assertFile(t, target, "known good", 0o700)
	assertNoUpdateStages(t, filepath.Dir(target))
}

func TestRunScheduledReplacementKeepsVerifiedStageForWindowsHelper(t *testing.T) {
	target := writeTestExecutable(t, "running executable", 0o755)
	replacement := []byte("verified update")
	checksums := sha256Hex(replacement) + "  wtp_linux_amd64\n"
	deps := baseTestDependencies(target, releaseServer(testRelease("v1.1.0"), []byte(checksums), replacement))
	var stagedPath string
	deps.replace = func(source, destination string) (bool, error) {
		stagedPath = source
		return true, nil
	}

	result, err := run(context.Background(), "1.0.0", deps)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if result.Updated || !result.Scheduled {
		t.Fatalf("result = %#v", result)
	}
	assertFile(t, target, "running executable", 0o755)
	assertFile(t, stagedPath, string(replacement), 0o755)
	if err := os.Remove(stagedPath); err != nil {
		t.Fatalf("remove staged test update: %v", err)
	}
}

func TestRunResolvesExecutableSymlinkBeforeReplacing(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink privileges vary on Windows")
	}
	target := writeTestExecutable(t, "old", 0o755)
	link := filepath.Join(t.TempDir(), "wtp")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("create executable symlink: %v", err)
	}
	replacement := []byte("new")
	checksums := sha256Hex(replacement) + "  wtp_linux_amd64\n"
	deps := baseTestDependencies(link, releaseServer(testRelease("v1.1.0"), []byte(checksums), replacement))

	result, err := run(context.Background(), "1.0.0", deps)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if result.Path != target {
		t.Fatalf("result path = %q, want symlink target %q", result.Path, target)
	}
	linkInfo, err := os.Lstat(link)
	if err != nil || linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("install symlink was replaced: info=%v error=%v", linkInfo, err)
	}
	assertFile(t, target, "new", 0o755)
}

func TestRunRejectsDevelopmentBuildWithoutNetwork(t *testing.T) {
	target := writeTestExecutable(t, "development", 0o755)
	deps := baseTestDependencies(target, func(request *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected network request: %s", request.URL)
		return nil, nil
	})
	_, err := run(context.Background(), "dev", deps)
	if err == nil || !strings.Contains(err.Error(), "development builds cannot be updated") {
		t.Fatalf("run() error = %v", err)
	}
}

func baseTestDependencies(target string, do roundTripFunc) dependencies {
	return dependencies{
		httpClient:       do,
		latestReleaseURL: "https://updates.test/latest",
		goos:             "linux",
		goarch:           "amd64",
		executable: func() (string, error) {
			return target, nil
		},
		evalSymlinks: filepath.EvalSymlinks,
		replace: func(source, destination string) (bool, error) {
			// Test targets are not running. Remove plus rename gives the Windows
			// test runner deterministic replacement semantics as well.
			if err := os.Remove(destination); err != nil {
				return false, err
			}
			return false, os.Rename(source, destination)
		},
	}
}

func testRelease(tag string) releaseasset.GitHubRelease {
	return releaseasset.GitHubRelease{
		TagName: tag,
		Assets: []releaseasset.GitHubAsset{
			{Name: "wtp_linux_arm64", BrowserDownloadURL: "https://updates.test/wtp_linux_arm64"},
			{Name: releaseasset.ChecksumAssetName, BrowserDownloadURL: "https://updates.test/checksums.txt"},
			{Name: "wtp_linux_amd64", BrowserDownloadURL: "https://updates.test/wtp_linux_amd64"},
		},
	}
}

func releaseServer(release releaseasset.GitHubRelease, checksums, executable []byte) roundTripFunc {
	return func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/latest":
			return jsonResponse(http.StatusOK, release), nil
		case "/checksums.txt":
			return byteResponse(http.StatusOK, checksums), nil
		case "/wtp_linux_amd64":
			return byteResponse(http.StatusOK, executable), nil
		default:
			return nil, fmt.Errorf("unexpected request %s", request.URL)
		}
	}
}

func jsonResponse(status int, value any) *http.Response {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return byteResponse(status, data)
}

func byteResponse(status int, data []byte) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(data)),
	}
}

func writeTestExecutable(t *testing.T, contents string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wtp")
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatalf("write test executable: %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("set test executable mode: %v", err)
	}
	return path
}

func assertFile(t *testing.T, path, wantContents string, wantMode os.FileMode) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(contents) != wantContents {
		t.Fatalf("contents of %s = %q, want %q", path, contents, wantContents)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != wantMode.Perm() {
		t.Fatalf("mode of %s = %o, want %o", path, got, wantMode.Perm())
	}
}

func assertNoUpdateStages(t *testing.T, directory string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(directory, ".wtp-update-*"))
	if err != nil {
		t.Fatalf("glob update stages: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("update staging files remain: %v", matches)
	}
}

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
