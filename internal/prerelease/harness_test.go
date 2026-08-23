package prerelease

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/releaseasset"
)

func TestReportSchemaRoundTrip(t *testing.T) {
	report := Report{
		SchemaVersion: ReportVersion,
		Status:        "passed",
		Verdict:       "GO",
		Seed:          42,
		Repeat:        2,
		Platform:      PlatformInfo{OS: "linux", Arch: "amd64", GoVersion: "go1.25", GitVersion: "git version 2"},
		Candidate:     CandidateInfo{Path: "$WORKDIR/wtp", SHA256: strings.Repeat("a", 64), Version: map[string]any{"version": "test"}},
		Source:        SourceInfo{Commit: "deadbeef", ManifestUnchanged: true},
		Scenarios:     []ScenarioReport{{Name: "lifecycle", Status: "passed", Assertions: []string{"typed task"}, Commands: []CommandEvidence{{Argv: []string{"$CANDIDATE", "--json", "version"}, Environment: map[string]string{"HOME": "$WORKDIR/home"}, ExitCode: 0}}}},
		Normalized:    NormalizedReport{SchemaVersion: ReportVersion, Seed: 42, Repeat: 2, CandidateSHA256: strings.Repeat("a", 64), Status: "passed"},
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	var decoded Report
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if decoded.SchemaVersion != ReportVersion || decoded.Candidate.SHA256 != report.Candidate.SHA256 || len(decoded.Scenarios) != 1 || len(decoded.Scenarios[0].Commands) != 1 {
		t.Fatalf("round-tripped report lost schema fields: %#v", decoded)
	}
}

func TestPreflightMissingNativeShardIsInconclusive(t *testing.T) {
	manifest := CandidateManifest{SchemaVersion: CandidateManifestVersion, Commit: "commit", Version: "1.2.3", BuildDate: "date"}
	for _, platform := range releaseasset.Platforms() {
		manifest.Assets = append(manifest.Assets, CandidateAssetEvidence{
			Name: platform.AssetName, GOOS: platform.GOOS, GOARCH: platform.GOARCH,
			SHA256: strings.Repeat("a", 64), Version: manifest.Version, Commit: manifest.Commit,
			BuildDate: manifest.BuildDate, MetadataVerified: true,
		})
	}
	result := PreflightEvidence(MergedEvidence{SchemaVersion: MergedEvidenceVersion, Commit: manifest.Commit, Candidate: manifest})
	if result.Status != "inconclusive" {
		t.Fatalf("preflight status = %q, want inconclusive: %#v", result.Status, result)
	}
	if !strings.Contains(strings.Join(result.Issues, "\n"), "required native platform") {
		t.Fatalf("preflight issues = %#v, want native coverage issue", result.Issues)
	}
}

func TestPreflightFailedAssertionIsNoGo(t *testing.T) {
	manifest := CandidateManifest{SchemaVersion: CandidateManifestVersion, Commit: "commit", Version: "1.2.3", BuildDate: "date"}
	for _, platform := range releaseasset.Platforms() {
		manifest.Assets = append(manifest.Assets, CandidateAssetEvidence{
			Name: platform.AssetName, GOOS: platform.GOOS, GOARCH: platform.GOARCH,
			SHA256: strings.Repeat("a", 64), Version: manifest.Version, Commit: manifest.Commit,
			BuildDate: manifest.BuildDate, MetadataVerified: true,
		})
	}
	result := PreflightEvidence(MergedEvidence{
		SchemaVersion: MergedEvidenceVersion, Commit: manifest.Commit, Candidate: manifest,
		Shards: []EvidenceShard{{Platform: "linux/amd64", Report: Report{Status: "failed", Verdict: "NO_GO"}}},
	})
	if result.Status != "no_go" {
		t.Fatalf("preflight status = %q, want no_go", result.Status)
	}
}

func TestStableNormalizeRemovesRunSpecificValues(t *testing.T) {
	input := `/tmp/wtp-prerelease-123456789/iteration-1/repo with spaces/00000000-0000-4000-8000-000000000001.json 2026-08-09T12:00:00Z`
	got := stableNormalize(input)
	for _, want := range []string{"$WORKDIR", "$UUID", "$TIME"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stableNormalize(%q) = %q, missing %q", input, got, want)
		}
	}
}

func TestRunRequiresExplicitCandidate(t *testing.T) {
	_, err := Run(Options{SourceRoot: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "--candidate is required") {
		t.Fatalf("Run without candidate error = %v, want explicit-candidate error", err)
	}
}

func TestManifestIsSortedAndHashesContent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := manifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Path != "a.txt" || files[1].Path != "b.txt" || files[0].SHA256 == files[1].SHA256 {
		t.Fatalf("manifest = %#v, want sorted distinct content hashes", files)
	}
}

func TestValidateReportRejectsPassedReportMissingContentionEvidence(t *testing.T) {
	report := validReportForTest()
	report.Scenarios = report.Scenarios[:7]
	if err := validateReport(report); err == nil || !strings.Contains(err.Error(), "missing scenario contention") {
		t.Fatalf("validateReport() error = %v, want missing contention evidence", err)
	}
}

func TestValidateReportRejectsIncompleteProcessEvidence(t *testing.T) {
	report := validReportForTest()
	report.Scenarios[7].ProcessCount = 32
	report.Scenarios[7].ProcessExitCodes = []int{0}
	report.Scenarios[7].ProcessDurationsMS = []int64{1}
	if err := validateReport(report); err == nil || !strings.Contains(err.Error(), "process evidence is incomplete") {
		t.Fatalf("validateReport() error = %v, want incomplete process evidence", err)
	}
}

func TestValidateReportRejectsPassedFailedRace(t *testing.T) {
	report := validReportForTest()
	report.Race = RaceReport{Status: "failed", Reason: "compiler failed"}
	report.Normalized.RaceStatus = "failed"
	if err := validateReport(report); err == nil || !strings.Contains(err.Error(), "race result failed") {
		t.Fatalf("validateReport() error = %v, want failed race evidence", err)
	}
}

func validReportForTest() Report {
	names := []string{
		"lifecycle", "stats-and-custom-statuses", "dependencies-and-ownership", "handoffs-and-export",
		"git-and-storage-topology", "configuration-failures", "nested-invocation-and-hermeticity",
		"contention-creates", "contention-next", "contention-handoffs", "contention-readers-and-writers", "failure-recovery",
	}
	scenarios := make([]ScenarioReport, 0, len(names))
	for index, name := range names {
		scenario := ScenarioReport{Name: name, Status: "passed", Iteration: 1}
		if index >= 7 {
			count := 16
			if name == "contention-creates" {
				count = 64
			} else if name == "contention-handoffs" {
				count = 32
			}
			scenario.ProcessCount = count
			scenario.ProcessExitCodes = make([]int, count)
			scenario.ProcessDurationsMS = make([]int64, count)
		}
		scenarios = append(scenarios, scenario)
	}
	return Report{
		SchemaVersion: ReportVersion,
		Status:        "passed",
		Verdict:       "GO",
		Repeat:        1,
		Race:          RaceReport{Status: "passed"},
		Scenarios:     scenarios,
		Normalized:    NormalizedReport{SchemaVersion: ReportVersion, Status: "passed", RaceStatus: "passed"},
	}
}
