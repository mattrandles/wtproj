package releaseasset

import (
	"reflect"
	"strings"
	"testing"
)

func TestPlatforms(t *testing.T) {
	want := []Platform{
		{GOOS: "darwin", GOARCH: "amd64", AssetName: "wtp_darwin_amd64"},
		{GOOS: "darwin", GOARCH: "arm64", AssetName: "wtp_darwin_arm64"},
		{GOOS: "linux", GOARCH: "amd64", AssetName: "wtp_linux_amd64"},
		{GOOS: "linux", GOARCH: "arm64", AssetName: "wtp_linux_arm64"},
		{GOOS: "windows", GOARCH: "amd64", AssetName: "wtp_windows_amd64.exe"},
		{GOOS: "windows", GOARCH: "arm64", AssetName: "wtp_windows_arm64.exe"},
	}

	if got := Platforms(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Platforms() = %#v, want %#v", got, want)
	}

	got := Platforms()
	got[0].AssetName = "changed"
	if gotAgain := Platforms()[0].AssetName; gotAgain != want[0].AssetName {
		t.Fatalf("Platforms returned mutable contract storage: got %q", gotAgain)
	}
}

func TestParseChecksums(t *testing.T) {
	digestA := strings.Repeat("a", ChecksumDigestHexLength)
	digestB := strings.Repeat("1", ChecksumDigestHexLength)
	input := digestA + "  wtp_linux_amd64\n" + digestB + "  wtp_windows_arm64.exe\n"

	got, err := ParseChecksums(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseChecksums() error = %v", err)
	}
	want := map[string]string{
		"wtp_linux_amd64":       digestA,
		"wtp_windows_arm64.exe": digestB,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseChecksums() = %#v, want %#v", got, want)
	}
}

func TestParseChecksumsRejectsInvalidMetadata(t *testing.T) {
	digest := strings.Repeat("a", ChecksumDigestHexLength)
	tests := map[string]string{
		"empty":              "",
		"single separator":   digest + " wtp_linux_amd64\n",
		"short digest":       "abcd  wtp_linux_amd64\n",
		"uppercase digest":   strings.ToUpper(digest) + "  wtp_linux_amd64\n",
		"non-hex digest":     strings.Repeat("z", ChecksumDigestHexLength) + "  wtp_linux_amd64\n",
		"missing asset":      digest + "  \n",
		"duplicate filename": digest + "  wtp_linux_amd64\n" + digest + "  wtp_linux_amd64\n",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseChecksums(strings.NewReader(input)); err == nil {
				t.Fatal("ParseChecksums() error = nil")
			}
		})
	}
}

func TestAssetName(t *testing.T) {
	for _, platform := range Platforms() {
		got, err := AssetName(platform.GOOS, platform.GOARCH)
		if err != nil {
			t.Fatalf("AssetName(%q, %q) error = %v", platform.GOOS, platform.GOARCH, err)
		}
		if got != platform.AssetName {
			t.Errorf("AssetName(%q, %q) = %q, want %q", platform.GOOS, platform.GOARCH, got, platform.AssetName)
		}
	}

	if _, err := AssetName("plan9", "amd64"); err == nil {
		t.Fatal("AssetName(plan9, amd64) error = nil, want unsupported-platform error")
	}
}

func TestGitHubContractConstants(t *testing.T) {
	if LatestReleaseURL != "https://api.github.com/repos/mattrandles/wtproj/releases/latest" {
		t.Fatalf("LatestReleaseURL = %q", LatestReleaseURL)
	}
	if GitHubAccept != "application/vnd.github+json" || GitHubAPIVersion != "2022-11-28" {
		t.Fatalf("GitHub request contract = (%q, %q)", GitHubAccept, GitHubAPIVersion)
	}
	if ChecksumAssetName != "checksums.txt" || ChecksumAlgorithm != "sha256" {
		t.Fatalf("checksum contract = (%q, %q)", ChecksumAssetName, ChecksumAlgorithm)
	}
}
