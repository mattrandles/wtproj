package prerelease

// This file contains the small, deterministic evidence protocol used by the
// native workflow.  It intentionally does not call a model: the model-ready
// evaluator consumes the output, while these checks make structural omissions
// impossible to waive in prose.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"

	"github.com/mattrandles/wtproj/internal/releaseasset"
)

const (
	CandidateManifestVersion = "wtp-prerelease-candidate/v1"
	MergedEvidenceVersion    = "wtp-prerelease-merged/v1"
	PreflightVersion         = "wtp-prerelease-preflight/v1"
)

type CandidateManifest struct {
	SchemaVersion string                   `json:"schemaVersion"`
	Commit        string                   `json:"commit"`
	Version       string                   `json:"version"`
	BuildDate     string                   `json:"buildDate"`
	Assets        []CandidateAssetEvidence `json:"assets"`
}

type CandidateAssetEvidence struct {
	Name             string `json:"name"`
	GOOS             string `json:"goos"`
	GOARCH           string `json:"goarch"`
	SHA256           string `json:"sha256"`
	Size             int64  `json:"size"`
	Version          string `json:"version"`
	Commit           string `json:"commit"`
	BuildDate        string `json:"buildDate"`
	MetadataVerified bool   `json:"metadataVerified"`
}

type EvidenceShard struct {
	Platform string `json:"platform"`
	Path     string `json:"path"`
	Report   Report `json:"report"`
}

type MergedEvidence struct {
	SchemaVersion        string             `json:"schemaVersion"`
	Commit               string             `json:"commit"`
	Seed                 int64              `json:"seed"`
	Repeat               int                `json:"repeat"`
	Candidate            CandidateManifest  `json:"candidate"`
	Shards               []EvidenceShard    `json:"shards"`
	UpdaterReport        json.RawMessage    `json:"updaterReport,omitempty"`
	RepeatNormalizations []NormalizedReport `json:"repeatNormalizations,omitempty"`
	Artifacts            []string           `json:"artifacts"`
}

type PreflightResult struct {
	SchemaVersion  string   `json:"schemaVersion"`
	Status         string   `json:"status"` // complete, no_go, inconclusive
	Issues         []string `json:"issues"`
	Warnings       []string `json:"warnings"`
	Platforms      []string `json:"platforms"`
	ScenarioTotal  int      `json:"scenarioTotal"`
	IterationTotal int      `json:"iterationTotal"`
}

var requiredScenarioNames = []string{
	"lifecycle", "stats-and-custom-statuses", "bounded-shared-dependency-graph", "dependencies-and-ownership", "handoffs-and-export",
	"git-and-storage-topology", "configuration-failures",
	"nested-invocation-and-hermeticity", "contention-creates", "contention-next",
	"contention-handoffs", "contention-readers-and-writers", "failure-recovery",
}

var requiredNativePlatforms = []string{"linux/amd64", "windows/amd64", "darwin/arm64"}

var requiredUpdaterCases = []string{
	"equal-version-noop", "older-version-noop", "invalid-tag", "missing-assets",
	"duplicate-assets", "malformed-checksums", "checksum-mismatch",
	"failed-checksum-download", "connection-termination", "truncated-download",
	"timeout-download", "unsafe-url", "unsafe-redirect", "symlink-launch",
	"unwritable-target", "replacement-rollback",
}

func BuildCandidateManifest(directory, output string) (CandidateManifest, error) {
	var manifest CandidateManifest
	manifest.SchemaVersion = CandidateManifestVersion
	assets := releaseasset.Platforms()
	for _, platform := range assets {
		path := filepath.Join(directory, platform.AssetName)
		data, err := os.ReadFile(path)
		if err != nil {
			return manifest, fmt.Errorf("read candidate %s: %w", platform.AssetName, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return manifest, err
		}
		digest := sha256.Sum256(data)
		asset := CandidateAssetEvidence{Name: platform.AssetName, GOOS: platform.GOOS, GOARCH: platform.GOARCH, SHA256: hex.EncodeToString(digest[:]), Size: info.Size()}
		manifest.Assets = append(manifest.Assets, asset)
	}
	checksums, err := os.ReadFile(filepath.Join(directory, releaseasset.ChecksumAssetName))
	if err != nil {
		return manifest, fmt.Errorf("read candidate checksums: %w", err)
	}
	parsed, err := releaseasset.ParseChecksums(bytes.NewReader(checksums))
	if err != nil {
		return manifest, err
	}
	if len(parsed) != len(assets) {
		return manifest, fmt.Errorf("candidate checksum entries = %d, want %d", len(parsed), len(assets))
	}
	for index := range manifest.Assets {
		asset := &manifest.Assets[index]
		if parsed[asset.Name] != asset.SHA256 {
			return manifest, fmt.Errorf("checksum for %s does not match candidate bytes", asset.Name)
		}
	}

	hostName, err := releaseasset.AssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return manifest, fmt.Errorf("candidate manifest host is unsupported: %w", err)
	}
	version, err := candidateVersion(filepath.Join(directory, hostName))
	if err != nil {
		return manifest, fmt.Errorf("read embedded metadata from %s: %w", hostName, err)
	}
	manifest.Version = version.Version
	manifest.Commit = version.Commit
	manifest.BuildDate = version.BuildDate
	if manifest.Version == "" || manifest.Version == "dev" || manifest.Commit == "" || manifest.Commit == "none" || manifest.BuildDate == "" || manifest.BuildDate == "unknown" {
		return manifest, fmt.Errorf("candidate contains development metadata: %+v", version)
	}
	for index := range manifest.Assets {
		asset := &manifest.Assets[index]
		data, readErr := os.ReadFile(filepath.Join(directory, asset.Name))
		if readErr != nil {
			return manifest, readErr
		}
		asset.Version, asset.Commit, asset.BuildDate = manifest.Version, manifest.Commit, manifest.BuildDate
		asset.MetadataVerified = bytes.Contains(data, []byte(manifest.Version)) && bytes.Contains(data, []byte(manifest.Commit)) && bytes.Contains(data, []byte(manifest.BuildDate))
		if !asset.MetadataVerified {
			return manifest, fmt.Errorf("embedded metadata is incomplete in %s", asset.Name)
		}
	}
	return manifest, writeJSONFile(output, manifest)
}

func candidateVersion(path string) (struct{ Version, Commit, BuildDate string }, error) {
	var result struct{ Version, Commit, BuildDate string }
	output, err := exec.Command(path, "--json", "version").Output()
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return result, err
	}
	return result, nil
}

func MergeEvidence(manifestPath, output, updaterPath string, shardPaths, repeatPaths []string) (MergedEvidence, error) {
	var merged MergedEvidence
	if err := readJSONFile(manifestPath, &merged.Candidate); err != nil {
		return merged, fmt.Errorf("candidate manifest: %w", err)
	}
	if merged.Candidate.SchemaVersion != CandidateManifestVersion {
		return merged, fmt.Errorf("candidate manifest schema %q", merged.Candidate.SchemaVersion)
	}
	if len(shardPaths) == 0 {
		return merged, errors.New("at least one native shard is required")
	}
	merged.SchemaVersion = MergedEvidenceVersion
	merged.Commit = merged.Candidate.Commit
	for _, path := range shardPaths {
		var report Report
		if err := readJSONFile(path, &report); err != nil {
			return merged, fmt.Errorf("shard %s: %w", path, err)
		}
		if report.SchemaVersion != ReportVersion {
			return merged, fmt.Errorf("shard %s schema %q", path, report.SchemaVersion)
		}
		platform := report.Platform.OS + "/" + report.Platform.Arch
		assetName := report.Candidate.Asset
		if assetName == "" {
			assetName, _ = releaseasset.AssetName(report.Platform.OS, report.Platform.Arch)
		}
		var expected *CandidateAssetEvidence
		for index := range merged.Candidate.Assets {
			if merged.Candidate.Assets[index].Name == assetName {
				expected = &merged.Candidate.Assets[index]
				break
			}
		}
		if expected == nil || report.Candidate.SHA256 != expected.SHA256 {
			return merged, fmt.Errorf("shard %s candidate digest does not match manifest for %s", path, assetName)
		}
		if report.Source.Commit != "" && report.Source.Commit != merged.Commit {
			return merged, fmt.Errorf("shard %s commit %q does not match %q", path, report.Source.Commit, merged.Commit)
		}
		if len(merged.Shards) > 0 && (report.Seed != merged.Seed || report.Repeat != merged.Repeat) {
			return merged, fmt.Errorf("shard %s seed/repeat differs from existing shards", path)
		}
		for _, existing := range merged.Shards {
			if existing.Platform == platform {
				return merged, fmt.Errorf("duplicate native shard for %s", platform)
			}
		}
		if len(merged.Shards) == 0 {
			merged.Seed, merged.Repeat = report.Seed, report.Repeat
		}
		merged.Shards = append(merged.Shards, EvidenceShard{Platform: platform, Path: path, Report: report})
	}
	for _, path := range repeatPaths {
		var report Report
		if err := readJSONFile(path, &report); err != nil {
			return merged, fmt.Errorf("repeat report %s: %w", path, err)
		}
		if report.SchemaVersion != ReportVersion || report.Seed != merged.Seed || report.Repeat != merged.Repeat || report.Candidate.SHA256 == "" {
			return merged, fmt.Errorf("repeat report %s does not match shard identity", path)
		}
		merged.RepeatNormalizations = append(merged.RepeatNormalizations, report.Normalized)
	}
	if updaterPath != "" {
		data, err := os.ReadFile(updaterPath)
		if err != nil {
			return merged, err
		}
		if !json.Valid(data) {
			return merged, fmt.Errorf("updater report is not JSON")
		}
		merged.UpdaterReport = data
	}
	sort.Slice(merged.Shards, func(i, j int) bool { return merged.Shards[i].Platform < merged.Shards[j].Platform })
	return merged, writeJSONFile(output, merged)
}

func PreflightEvidence(merged MergedEvidence) PreflightResult {
	result := PreflightResult{SchemaVersion: PreflightVersion, Status: "complete", Issues: []string{}, Warnings: []string{}}
	if merged.SchemaVersion != MergedEvidenceVersion {
		result.Issues = append(result.Issues, "merged evidence schema is missing or unsupported")
	}
	if merged.Candidate.SchemaVersion != CandidateManifestVersion || len(merged.Candidate.Assets) != len(releaseasset.Platforms()) {
		result.Issues = append(result.Issues, "candidate manifest is missing or does not contain all six release assets")
	}
	assetNames := map[string]bool{}
	for _, asset := range merged.Candidate.Assets {
		assetNames[asset.Name] = true
		if asset.SHA256 == "" || !asset.MetadataVerified || asset.Version != merged.Candidate.Version || asset.Commit != merged.Candidate.Commit || asset.BuildDate != merged.Candidate.BuildDate {
			result.Issues = append(result.Issues, "candidate metadata or digest evidence is incomplete for "+asset.Name)
		}
	}
	for _, platform := range requiredNativePlatforms {
		found := false
		for _, shard := range merged.Shards {
			if shard.Platform == platform {
				found = true
				result.Platforms = append(result.Platforms, platform)
			}
		}
		if !found {
			result.Issues = append(result.Issues, "required native platform is missing: "+platform)
		}
	}
	if len(merged.Shards) == 0 {
		result.Issues = append(result.Issues, "no native shards were supplied")
	}
	for _, shard := range merged.Shards {
		result.ScenarioTotal += len(shard.Report.Scenarios)
		result.IterationTotal += shard.Report.Repeat
		if shard.Report.Source.Commit != merged.Commit {
			result.Issues = append(result.Issues, "native shard source commit identity is missing or mismatched: "+shard.Platform)
		}
		if shard.Report.Status == "failed" || shard.Report.Verdict == "NO_GO" {
			result.Status = "no_go"
			result.Issues = append(result.Issues, "native shard failed: "+shard.Platform)
		}
		if shard.Report.Repeat < 20 {
			result.Issues = append(result.Issues, "native shard repeat count is below 20: "+shard.Platform)
		}
		if !shard.Report.Source.ManifestUnchanged {
			result.Issues = append(result.Issues, "source checkout preservation failed: "+shard.Platform)
		}
		if len(shard.Report.PlatformSkips) != 0 {
			for _, skip := range shard.Report.PlatformSkips {
				if strings.TrimSpace(skip.Reason) == "" {
					result.Issues = append(result.Issues, "native shard contains an unexplained platform skip: "+shard.Platform+"/"+skip.Scenario)
				} else {
					result.Warnings = append(result.Warnings, shard.Platform+" skip recorded for "+skip.Scenario+": "+skip.Reason)
				}
			}
		}
		seen := map[string]bool{}
		for _, scenario := range shard.Report.Scenarios {
			base := strings.TrimSuffix(scenario.Name, "#"+fmt.Sprint(scenario.Iteration))
			seen[base] = true
			if scenario.Status != "passed" || len(scenario.InvariantFailures) != 0 {
				result.Status = "no_go"
				result.Issues = append(result.Issues, "failed assertion or invariant in "+shard.Platform+"/"+scenario.Name)
			}
			if len(scenario.Preservation) > 0 {
				for _, evidence := range scenario.Preservation {
					if !evidence.Unchanged && strings.Contains(evidence.Expected, "rejected") {
						result.Status = "no_go"
						result.Issues = append(result.Issues, "preservation boundary changed in "+shard.Platform+"/"+scenario.Name)
					}
				}
			}
			if minimum, ok := map[string]int{"contention-creates": 64, "contention-next": 16, "contention-handoffs": 32, "contention-readers-and-writers": 16}[base]; ok && scenario.ProcessCount < minimum {
				result.Issues = append(result.Issues, fmt.Sprintf("%s/%s has %d processes, want at least %d", shard.Platform, scenario.Name, scenario.ProcessCount, minimum))
			}
		}
		for _, required := range requiredScenarioNames {
			if !seen[required] {
				result.Issues = append(result.Issues, "required scenario is missing from "+shard.Platform+": "+required)
			}
		}
		if shard.Report.Race.Status == "failed" || shard.Report.Race.Status == "not_run" {
			result.Status = "no_go"
			result.Issues = append(result.Issues, "race evidence failed or is absent on "+shard.Platform)
		}
	}
	if len(merged.RepeatNormalizations) > 1 {
		for _, repeat := range merged.RepeatNormalizations[1:] {
			if !reflect.DeepEqual(merged.RepeatNormalizations[0], repeat) {
				result.Status = "no_go"
				result.Issues = append(result.Issues, "same-seed normalized repeat comparison differs")
				break
			}
		}
	}
	if len(merged.UpdaterReport) == 0 {
		result.Issues = append(result.Issues, "release updater evidence is missing or unreadable")
	} else {
		var updater struct {
			SchemaVersion string                          `json:"schemaVersion"`
			Status        string                          `json:"status"`
			Scenarios     []struct{ Name, Status string } `json:"scenarios"`
		}
		if err := json.Unmarshal(merged.UpdaterReport, &updater); err != nil || updater.SchemaVersion != "wtp-release-qa/v1" {
			result.Issues = append(result.Issues, "release updater evidence has an invalid schema")
		} else {
			cases := map[string]string{}
			for _, scenario := range updater.Scenarios {
				cases[scenario.Name] = scenario.Status
				if scenario.Status == "failed" {
					result.Status = "no_go"
					result.Issues = append(result.Issues, "updater case failed: "+scenario.Name)
				}
			}
			for _, required := range requiredUpdaterCases {
				if cases[required] == "" {
					result.Issues = append(result.Issues, "updater case is missing: "+required)
				}
			}
		}
	}
	_ = assetNames
	if len(result.Issues) > 0 && result.Status != "no_go" {
		result.Status = "inconclusive"
	}
	return result
}

func EvaluateEvidence(merged MergedEvidence, evidencePath string) (string, PreflightResult) {
	preflight := PreflightEvidence(merged)
	verdict := "GO"
	if preflight.Status == "no_go" {
		verdict = "NO_GO"
	} else if preflight.Status == "inconclusive" {
		verdict = "INCONCLUSIVE"
	}
	var builder strings.Builder
	builder.WriteString("# Pre-release verdict\n\n")
	builder.WriteString("Candidate identity\n\n")
	builder.WriteString(fmt.Sprintf("- Commit: `%s`\n- Version: `%s`\n- Build date: `%s`\n- Evidence: `%s`\n", merged.Commit, merged.Candidate.Version, merged.Candidate.BuildDate, evidencePath))
	builder.WriteString("- Candidate digests:\n")
	for _, asset := range merged.Candidate.Assets {
		builder.WriteString(fmt.Sprintf("  - `%s`: `%s`\n", asset.Name, asset.SHA256))
	}
	builder.WriteString("\nEvidence summary\n\n")
	builder.WriteString(fmt.Sprintf("- Native platforms: %s\n- Scenario entries: %d\n- Iterations: %d\n- Candidate assets: %d\n- Failures/issues: %d\n- Warnings/skips: %d\n", strings.Join(preflight.Platforms, ", "), preflight.ScenarioTotal, preflight.IterationTotal, len(merged.Candidate.Assets), len(preflight.Issues), len(preflight.Warnings)))
	builder.WriteString("\nFailures, skips, and residual risks\n\n")
	if len(preflight.Issues) == 0 {
		builder.WriteString("- None reported by deterministic preflight.\n")
	} else {
		for _, issue := range preflight.Issues {
			builder.WriteString("- " + issue + "\n")
		}
	}
	for _, warning := range preflight.Warnings {
		builder.WriteString("- Warning: " + warning + "\n")
	}
	builder.WriteString("\nVerdict: " + verdict + "\n")
	return builder.String(), preflight
}

func writeJSONFile(path string, value any) error {
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func readJSONFile(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}
