package releaseasset

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseDocumentationMatchesContract(t *testing.T) {
	root := repositoryRoot(t)
	assetDocument := readDocumentationFile(t, filepath.Join(root, "docs", "release-assets.md"))
	readme := readDocumentationFile(t, filepath.Join(root, "README.md"))
	documentation := strings.Join(strings.Fields(assetDocument+"\n"+readme), " ")

	for _, platform := range Platforms() {
		want := fmt.Sprintf("| `%s` | `%s` | `%s` |", platform.GOOS, platform.GOARCH, platform.AssetName)
		if !strings.Contains(assetDocument, want) {
			t.Errorf("release asset documentation missing contract row %q", want)
		}
	}

	for _, text := range []string{
		LatestReleaseURL,
		ChecksumAssetName,
		ChecksumAlgorithm,
		"https://github.com/mattrandles/wtproj/releases",
		"sole supported distribution channel",
		"latest/download",
		"wtp update",
	} {
		if !strings.Contains(documentation, strings.Join(strings.Fields(text), " ")) {
			t.Errorf("release documentation missing %q", text)
		}
	}

	for _, text := range []string{
		"GitHub Release asset contract",
		"direct-download release QA guide",
		"implemented, supported backend is local flat-file storage",
	} {
		if !strings.Contains(readme, text) {
			t.Errorf("README missing %q", text)
		}
	}

	if strings.Contains(strings.ToLower(readme), "trello") {
		t.Error("README must not document the unimplemented Trello provider")
	}
}

func TestRetainedHandoffDocumentationMatchesContract(t *testing.T) {
	root := repositoryRoot(t)
	readme := readDocumentationFile(t, filepath.Join(root, "README.md"))
	contributing := readDocumentationFile(t, filepath.Join(root, "CONTRIBUTING.md"))
	changelog := readDocumentationFile(t, filepath.Join(root, "CHANGELOG.md"))

	for _, text := range []string{
		"### Retained handoffs",
		"wtp handoff write --message",
		"wtp handoff get [--task <task-id> | --all-scopes] [--limit N | --all]",
		"wtp handoff purge (--id <handoff-id> | --global | --task <task-id> | --all-scopes)",
		"Writes append by default",
		"Handoff reads and claim attachment are non-consuming",
		".wtp/handoffs.json",
		"missing file is compatible",
		"exact retained collection",
		"--export-tasks=<directory>",
	} {
		if !strings.Contains(readme, text) {
			t.Errorf("README missing retained handoff contract %q", text)
		}
	}

	for _, text := range []string{
		"### Retained handoff context",
		"Handoff reads and claim attachment are non-consuming",
		"Purge uses exactly one of",
		"`--export-tasks=<directory>` form remains an export alias",
	} {
		if !strings.Contains(contributing, text) {
			t.Errorf("CONTRIBUTING missing retained handoff guidance %q", text)
		}
	}

	if !strings.Contains(changelog, "Documented the retained handoff workflow") {
		t.Error("CHANGELOG missing retained handoff entry")
	}
}

func readDocumentationFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
