package releaseasset

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestGoReleaserConfigMatchesPlatformContract(t *testing.T) {
	configPath := filepath.Join(repositoryRoot(t), ".goreleaser.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read %s: %v", configPath, err)
	}
	config := string(data)

	gooses := configList(t, config, "goos")
	goarches := configList(t, config, "goarch")
	var gotTargets []string
	for _, goos := range gooses {
		for _, goarch := range goarches {
			gotTargets = append(gotTargets, goos+"/"+goarch)
		}
	}
	var wantTargets []string
	for _, platform := range Platforms() {
		wantTargets = append(wantTargets, platform.GOOS+"/"+platform.GOARCH)
	}
	sort.Strings(gotTargets)
	sort.Strings(wantTargets)
	if !reflect.DeepEqual(gotTargets, wantTargets) {
		t.Fatalf("GoReleaser targets = %v, contract targets = %v", gotTargets, wantTargets)
	}

	required := []string{
		"version: 2",
		"project_name: wtp",
		"CGO_ENABLED=0",
		"      - -trimpath",
		"formats:\n      - binary",
		`name_template: "wtp_{{ .Os }}_{{ .Arch }}"`,
		"name_template: checksums.txt",
		"algorithm: sha256",
		"github.com/mattrandles/wtproj/internal/buildinfo.Version={{ .Version }}",
		"github.com/mattrandles/wtproj/internal/buildinfo.Commit={{ .Commit }}",
		"github.com/mattrandles/wtproj/internal/buildinfo.BuildDate={{ .Date }}",
		"changelog:\n  use: github-native",
		"release:\n  draft: false\n  prerelease: auto",
	}
	for _, snippet := range required {
		if !strings.Contains(config, snippet) {
			t.Errorf("GoReleaser config missing contract snippet %q", snippet)
		}
	}
}

func TestReleaseWorkflowIsTagOnlyAndLeastPrivilege(t *testing.T) {
	workflowPath := filepath.Join(repositoryRoot(t), ".github", "workflows", "release.yml")
	data, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read %s: %v", workflowPath, err)
	}
	workflow := string(data)

	required := []string{
		"push:\n    tags:\n      - \"v*.*.*\"",
		"permissions:\n  contents: read",
		"RELEASE_TAG: ${{ github.ref_name }}",
		"args: release --snapshot --clean",
		"WTP_RELEASE_DIST: dist",
		"needs: validate",
		"    permissions:\n      contents: write",
		"args: release --clean",
		"GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}",
		"persist-credentials: false",
	}
	for _, snippet := range required {
		if !strings.Contains(workflow, snippet) {
			t.Errorf("release workflow missing required snippet %q", snippet)
		}
	}

	if got := strings.Count(workflow, "contents: write"); got != 1 {
		t.Errorf("release workflow contents: write count = %d, want 1", got)
	}
	for _, forbidden := range []string{"pull_request:", "workflow_dispatch:", "branches:", "id-token:", "packages:", "actions: write"} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("release workflow contains forbidden trigger or permission %q", forbidden)
		}
	}

	validate := workflowJob(t, workflow, "validate")
	if strings.Contains(validate, "contents: write") || strings.Contains(validate, "GITHUB_TOKEN") {
		t.Error("release validation job must not receive release write credentials")
	}
	publish := workflowJob(t, workflow, "publish")
	if !strings.Contains(publish, "needs: validate") || !strings.Contains(publish, "contents: write") {
		t.Error("publish job must wait for validation and hold the sole contents: write grant")
	}
}

func TestGoReleaserSnapshotMatchesPlatformContract(t *testing.T) {
	dist := os.Getenv("WTP_RELEASE_DIST")
	if dist == "" {
		t.Skip("set WTP_RELEASE_DIST to validate a GoReleaser snapshot")
	}
	if !filepath.IsAbs(dist) {
		dist = filepath.Join(repositoryRoot(t), dist)
	}

	data, err := os.ReadFile(filepath.Join(dist, "artifacts.json"))
	if err != nil {
		t.Fatalf("read GoReleaser artifacts: %v", err)
	}
	var artifacts []struct {
		Name   string `json:"name"`
		Path   string `json:"path"`
		GOOS   string `json:"goos"`
		GOARCH string `json:"goarch"`
		Type   string `json:"type"`
		Extra  struct {
			Checksum string `json:"Checksum"`
			Format   string `json:"Format"`
		} `json:"extra"`
	}
	if err := json.Unmarshal(data, &artifacts); err != nil {
		t.Fatalf("parse GoReleaser artifacts: %v", err)
	}

	gotTargets := make(map[string]string)
	artifactPaths := make(map[string]string)
	artifactChecksums := make(map[string]string)
	for _, artifact := range artifacts {
		if artifact.Type != "Binary" || artifact.Extra.Format != "binary" {
			continue
		}
		key := artifact.GOOS + "/" + artifact.GOARCH
		if previous, exists := gotTargets[key]; exists {
			t.Fatalf("duplicate binary target %s (%q and %q)", key, previous, artifact.Name)
		}
		gotTargets[key] = artifact.Name
		artifactPath := artifact.Path
		if !filepath.IsAbs(artifactPath) {
			artifactPath = filepath.Join(repositoryRoot(t), artifactPath)
		}
		if _, err := os.Stat(artifactPath); err != nil {
			t.Errorf("stat binary artifact %q: %v", artifactPath, err)
		}
		artifactPaths[key] = artifactPath
		artifactChecksums[artifact.Name] = strings.TrimPrefix(artifact.Extra.Checksum, ChecksumAlgorithm+":")
	}

	wantTargets := make(map[string]string)
	for _, platform := range Platforms() {
		wantTargets[platform.GOOS+"/"+platform.GOARCH] = platform.AssetName
	}
	if !reflect.DeepEqual(gotTargets, wantTargets) {
		t.Errorf("snapshot binaries = %v, want %v", gotTargets, wantTargets)
	}

	checksumData, err := os.ReadFile(filepath.Join(dist, ChecksumAssetName))
	if err != nil {
		t.Fatalf("read snapshot checksums: %v", err)
	}
	checksums, err := ParseChecksums(strings.NewReader(string(checksumData)))
	if err != nil {
		t.Fatalf("parse snapshot checksums: %v", err)
	}
	if len(checksums) != len(wantTargets) {
		t.Fatalf("snapshot checksum count = %d, want %d", len(checksums), len(wantTargets))
	}
	for _, assetName := range wantTargets {
		digest, ok := checksums[assetName]
		if !ok {
			t.Errorf("snapshot checksums missing %q", assetName)
			continue
		}
		if artifactChecksums[assetName] != digest {
			t.Errorf("artifact checksum for %q = %q, checksums file = %q", assetName, artifactChecksums[assetName], digest)
		}
		artifactPath := artifactPaths[targetForAsset(t, wantTargets, assetName)]
		file, err := os.Open(artifactPath)
		if err != nil {
			t.Errorf("open %q: %v", artifactPath, err)
			continue
		}
		hasher := sha256.New()
		_, copyErr := io.Copy(hasher, file)
		closeErr := file.Close()
		if copyErr != nil {
			t.Errorf("hash %q: %v", artifactPath, copyErr)
			continue
		}
		if closeErr != nil {
			t.Errorf("close %q: %v", artifactPath, closeErr)
			continue
		}
		if actual := fmt.Sprintf("%x", hasher.Sum(nil)); actual != digest {
			t.Errorf("sha256(%q) = %s, want %s", assetName, actual, digest)
		}
	}

	currentTarget := runtime.GOOS + "/" + runtime.GOARCH
	currentBinary, ok := artifactPaths[currentTarget]
	if !ok {
		t.Fatalf("snapshot does not contain a runnable binary for test host %s", currentTarget)
	}
	output, err := exec.Command(currentBinary, "--json", "version").CombinedOutput()
	if err != nil {
		t.Fatalf("run snapshot version command: %v\n%s", err, output)
	}
	var version struct {
		Version   string `json:"version"`
		Commit    string `json:"commit"`
		BuildDate string `json:"buildDate"`
	}
	if err := json.Unmarshal(output, &version); err != nil {
		t.Fatalf("parse snapshot version metadata: %v\n%s", err, output)
	}
	if version.Version == "" || version.Version == "dev" || version.Commit == "" || version.Commit == "none" || version.BuildDate == "" || version.BuildDate == "unknown" {
		t.Errorf("snapshot has development metadata: %+v", version)
	}
}

func targetForAsset(t *testing.T, targets map[string]string, assetName string) string {
	t.Helper()
	for target, candidate := range targets {
		if candidate == assetName {
			return target
		}
	}
	t.Fatalf("release target missing asset %q", assetName)
	return ""
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate test file")
	}
	return filepath.Join(filepath.Dir(filename), "..", "..")
}

func workflowJob(t *testing.T, workflow, name string) string {
	t.Helper()
	startMarker := fmt.Sprintf("  %s:\n", name)
	start := strings.Index(workflow, startMarker)
	if start < 0 {
		t.Fatalf("release workflow missing %q job", name)
	}
	rest := workflow[start+len(startMarker):]
	lines := strings.Split(rest, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") && strings.HasSuffix(line, ":") {
			return strings.Join(lines[:i], "\n")
		}
	}
	return rest
}

func configList(t *testing.T, config, key string) []string {
	t.Helper()
	lines := strings.Split(config, "\n")
	header := "    " + key + ":"
	for i, line := range lines {
		if line != header {
			continue
		}
		var values []string
		for _, item := range lines[i+1:] {
			if !strings.HasPrefix(item, "      - ") {
				break
			}
			values = append(values, strings.TrimPrefix(item, "      - "))
		}
		return values
	}
	t.Fatalf("GoReleaser config missing %q list", key)
	return nil
}
