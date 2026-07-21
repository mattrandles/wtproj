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

func readDocumentationFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
