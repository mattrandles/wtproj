package prerelease

// Package prerelease contains the black-box, hermetic workflow gate. It is
// deliberately independent of the CLI implementation: all task operations
// below cross an OS process boundary and use only the candidate executable.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mattrandles/wtproj/internal/config"
	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/stats"
)

const ReportVersion = "wtp-prerelease/v1"

type Options struct {
	Candidate      string
	CandidateAsset string
	Seed           int64
	Repeat         int
	Report         string
	KeepWorkdir    bool
	Timeout        time.Duration
	SuiteTimeout   time.Duration
	SourceRoot     string
}

type Report struct {
	SchemaVersion string           `json:"schemaVersion"`
	Status        string           `json:"status"`
	Verdict       string           `json:"verdict"`
	Seed          int64            `json:"seed"`
	Repeat        int              `json:"repeat"`
	StartedAt     string           `json:"startedAt"`
	EndedAt       string           `json:"endedAt"`
	DurationMS    int64            `json:"durationMs"`
	Platform      PlatformInfo     `json:"platform"`
	Candidate     CandidateInfo    `json:"candidate"`
	Source        SourceInfo       `json:"source"`
	Scenarios     []ScenarioReport `json:"scenarios"`
	Artifacts     []string         `json:"artifacts"`
	PlatformSkips []PlatformSkip   `json:"platformSkips"`
	Race          RaceReport       `json:"race"`
	Normalized    NormalizedReport `json:"normalized"`
	Reproduction  string           `json:"reproduction,omitempty"`
	Error         string           `json:"error,omitempty"`
}

type RaceReport struct {
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
	ExitCode   int    `json:"exitCode"`
	DurationMS int64  `json:"durationMs"`
	Output     string `json:"output,omitempty"`
}

type PlatformInfo struct {
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	GoVersion  string `json:"goVersion"`
	GitVersion string `json:"gitVersion"`
}

type CandidateInfo struct {
	Path    string `json:"path"`
	Asset   string `json:"asset,omitempty"`
	SHA256  string `json:"sha256"`
	Version any    `json:"version,omitempty"`
}

type SourceInfo struct {
	Root              string         `json:"root"`
	Commit            string         `json:"commit"`
	Dirty             bool           `json:"dirty"`
	StatusBefore      string         `json:"statusBefore"`
	StatusAfter       string         `json:"statusAfter"`
	ManifestBefore    []ManifestFile `json:"manifestBefore"`
	ManifestAfter     []ManifestFile `json:"manifestAfter"`
	ManifestUnchanged bool           `json:"manifestUnchanged"`
}

type ManifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type ScenarioReport struct {
	Name               string                 `json:"name"`
	Status             string                 `json:"status"`
	Iteration          int                    `json:"iteration,omitempty"`
	ScheduleSeed       int64                  `json:"scheduleSeed,omitempty"`
	DurationMS         int64                  `json:"durationMs"`
	ProcessCount       int                    `json:"processCount,omitempty"`
	ProcessExitCodes   []int                  `json:"processExitCodes,omitempty"`
	ProcessDurationsMS []int64                `json:"processDurationsMs,omitempty"`
	Assertions         []string               `json:"assertions"`
	InvariantFailures  []string               `json:"invariantFailures,omitempty"`
	Commands           []CommandEvidence      `json:"commands"`
	Artifacts          []string               `json:"artifacts"`
	Preservation       []PreservationEvidence `json:"preservation,omitempty"`
	Error              string                 `json:"error,omitempty"`
}

// PreservationEvidence records the byte-level boundary checked by a
// rejected public command. Transient lock and temp files are intentionally
// excluded; they are process machinery rather than published state.
type PreservationEvidence struct {
	Label     string         `json:"label"`
	Before    []ManifestFile `json:"before"`
	After     []ManifestFile `json:"after"`
	Unchanged bool           `json:"unchanged"`
	Expected  string         `json:"expected"`
}

type CommandEvidence struct {
	Argv        []string          `json:"argv"`
	Environment map[string]string `json:"environment"`
	Stdout      string            `json:"stdout"`
	Stderr      string            `json:"stderr"`
	ExitCode    int               `json:"exitCode"`
	DurationMS  int64             `json:"durationMs"`
	Assertions  []string          `json:"assertions"`
}

type PlatformSkip struct {
	Scenario string `json:"scenario"`
	Reason   string `json:"reason"`
}

type NormalizedReport struct {
	SchemaVersion        string               `json:"schemaVersion"`
	Seed                 int64                `json:"seed"`
	Repeat               int                  `json:"repeat"`
	CandidateSHA256      string               `json:"candidateSha256"`
	Status               string               `json:"status"`
	SourceManifestSHA256 string               `json:"sourceManifestSha256"`
	Scenarios            []NormalizedScenario `json:"scenarios"`
	RaceStatus           string               `json:"raceStatus"`
}

type NormalizedScenario struct {
	Name       string   `json:"name"`
	Status     string   `json:"status"`
	Assertions []string `json:"assertions"`
}

type RunResult struct {
	Report Report
	Err    error
}

var (
	uuidPattern      = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	timestampPattern = regexp.MustCompile(`\b20[0-9]{2}-[0-9]{2}-[0-9]{2}T[0-9:.+-]+Z\b`)
	workRootPattern  = regexp.MustCompile(`wtp-prerelease-[0-9a-f-]+`)
)

func Run(options Options) (Report, error) {
	started := time.Now().UTC()
	if options.Repeat < 1 {
		options.Repeat = 1
	}
	if options.Timeout <= 0 {
		options.Timeout = 30 * time.Second
	}
	if options.SuiteTimeout <= 0 {
		options.SuiteTimeout = 2 * time.Minute
	}
	if strings.TrimSpace(options.Candidate) == "" {
		return Report{}, errors.New("--candidate is required; the harness never resolves wtp from PATH")
	}
	candidate, err := filepath.Abs(options.Candidate)
	if err != nil {
		return Report{}, fmt.Errorf("resolve candidate: %w", err)
	}
	if info, statErr := os.Stat(candidate); statErr != nil || info.IsDir() {
		if statErr == nil {
			statErr = errors.New("candidate is a directory")
		}
		return Report{}, fmt.Errorf("candidate %s: %w", candidate, statErr)
	}
	if options.SourceRoot == "" {
		options.SourceRoot, _ = os.Getwd()
	}
	sourceRoot, err := filepath.Abs(options.SourceRoot)
	if err != nil {
		return Report{}, fmt.Errorf("resolve source root: %w", err)
	}
	beforeManifest, err := manifest(sourceRoot)
	if err != nil {
		return Report{}, fmt.Errorf("source manifest before: %w", err)
	}
	statusBefore := gitOutput(sourceRoot, "status", "--porcelain=v1", "--untracked-files=all")
	commit := strings.TrimSpace(gitOutput(sourceRoot, "rev-parse", "HEAD"))
	candidateDigest, err := fileSHA256(candidate)
	if err != nil {
		return Report{}, fmt.Errorf("candidate sha256: %w", err)
	}
	root, err := os.MkdirTemp("", "wtp-prerelease-")
	if err != nil {
		return Report{}, fmt.Errorf("create disposable root: %w", err)
	}
	keep := options.KeepWorkdir
	cleanup := func() {
		if !keep {
			_ = os.RemoveAll(root)
		}
	}
	defer cleanup()

	baseEnv := disposableEnv(root)
	report := Report{
		SchemaVersion: ReportVersion,
		Status:        "passed",
		Verdict:       "GO",
		Seed:          options.Seed,
		Repeat:        options.Repeat,
		StartedAt:     started.Format(time.RFC3339Nano),
		Platform:      PlatformInfo{OS: runtime.GOOS, Arch: runtime.GOARCH, GoVersion: toolVersion("go", "version"), GitVersion: toolVersion("git", "--version")},
		Candidate:     CandidateInfo{Path: normalize(candidate, root), Asset: options.CandidateAsset, SHA256: candidateDigest},
		Source:        SourceInfo{Root: normalize(sourceRoot, root), Commit: commit, Dirty: strings.TrimSpace(statusBefore) != "", StatusBefore: normalize(statusBefore, root), ManifestBefore: beforeManifest},
		Artifacts:     []string{},
		Scenarios:     []ScenarioReport{},
		PlatformSkips: []PlatformSkip{},
		Race:          RaceReport{Status: "not_run", Reason: "not run because the matrix stopped before race coverage"},
	}
	report.Reproduction = reproductionCommand(candidate, sourceRoot, options)
	if version, versionErr := candidateJSON(candidate, baseEnv, options.Timeout); versionErr == nil {
		report.Candidate.Version = version
	}

	scenarios := []struct {
		name string
		fn   func(*scenarioRunner) error
	}{
		{"lifecycle", scenarioLifecycle},
		{"stats-and-custom-statuses", scenarioStatsAndCustomStatuses},
		{"bounded-shared-dependency-graph", scenarioBoundedSharedDependencyGraph},
		{"dependencies-and-ownership", scenarioDependenciesAndOwnership},
		{"handoffs-and-export", scenarioHandoffsAndExport},
		{"git-and-storage-topology", scenarioGitAndStorageTopology},
		{"configuration-failures", scenarioConfigurationFailures},
		{"nested-invocation-and-hermeticity", scenarioNestedInvocation},
		{"contention-creates", scenarioContentionCreates},
		{"contention-next", scenarioContentionNext},
		{"contention-handoffs", scenarioContentionHandoffs},
		{"contention-readers-and-writers", scenarioContentionReadersAndWriters},
		{"failure-recovery", scenarioFailureRecovery},
	}
	failed := false
	for repeat := 0; repeat < options.Repeat; repeat++ {
		iterationRoot := filepath.Join(root, fmt.Sprintf("iteration-%d", repeat+1))
		if err := os.MkdirAll(iterationRoot, 0o755); err != nil {
			return report, fmt.Errorf("create iteration root: %w", err)
		}
		iterationEnv := disposableEnv(iterationRoot)
		for _, item := range scenarios {
			name := item.name
			if options.Repeat > 1 {
				name = fmt.Sprintf("%s#%d", item.name, repeat+1)
			}
			entry := ScenarioReport{Name: name, Status: "passed", Assertions: []string{}, Commands: []CommandEvidence{}, Artifacts: []string{}}
			entry.Iteration = repeat + 1
			entry.ScheduleSeed = scheduleSeed(options.Seed, repeat+1, name)
			runner := &scenarioRunner{candidate: candidate, root: iterationRoot, env: iterationEnv, timeout: options.Timeout, suiteTimeout: options.SuiteTimeout, scheduleSeed: entry.ScheduleSeed, report: &entry, platformSkips: &report.PlatformSkips}
			startedScenario := time.Now()
			err := item.fn(runner)
			entry.DurationMS = time.Since(startedScenario).Milliseconds()
			if err != nil {
				runner.invariantFailure(err.Error())
				entry.Status = "failed"
				entry.Error = err.Error()
				report.Status = "failed"
				report.Verdict = "NO_GO"
				if report.Error == "" {
					report.Error = fmt.Sprintf("scenario %s: %v", name, err)
				}
			}
			report.Scenarios = append(report.Scenarios, entry)
			if err != nil {
				failed = true
				break
			}
		}
		if failed {
			break
		}
	}
	if !failed {
		report.Race = runRaceCoverage(sourceRoot, options.SuiteTimeout, root)
		if report.Race.Status == "failed" {
			report.Status, report.Verdict = "failed", "NO_GO"
			report.Error = "go test -race ./...: " + report.Race.Reason
		} else if report.Race.Status == "not_applicable" {
			report.PlatformSkips = append(report.PlatformSkips, PlatformSkip{Scenario: "race", Reason: report.Race.Reason})
		}
	}
	afterManifest, manifestErr := manifest(sourceRoot)
	statusAfter := gitOutput(sourceRoot, "status", "--porcelain=v1", "--untracked-files=all")
	report.Source.ManifestAfter = afterManifest
	report.Source.StatusAfter = normalize(statusAfter, root)
	report.Source.ManifestUnchanged = manifestErr == nil && equalManifests(beforeManifest, afterManifest)
	if manifestErr != nil {
		report.Status, report.Verdict = "failed", "NO_GO"
		report.Error = "source manifest after: " + manifestErr.Error()
	}
	if !report.Source.ManifestUnchanged {
		report.Status, report.Verdict = "failed", "NO_GO"
		if report.Error == "" {
			report.Error = "source checkout content manifest changed during fixture execution"
		}
	}
	report.Normalized = normalizedReport(report, candidateDigest, beforeManifest)
	report.EndedAt = time.Now().UTC().Format(time.RFC3339Nano)
	report.DurationMS = time.Since(started).Milliseconds()
	if options.KeepWorkdir || report.Status == "failed" {
		report.Artifacts = append(report.Artifacts, root)
	}
	if options.Report != "" {
		if err := validateReport(report); err != nil {
			return report, err
		}
		if err := writeReport(options.Report, report); err != nil {
			return report, err
		}
	}
	if report.Status == "failed" {
		return report, errors.New(report.Error)
	}
	return report, nil
}

type scenarioRunner struct {
	candidate     string
	root          string
	env           []string
	timeout       time.Duration
	suiteTimeout  time.Duration
	scheduleSeed  int64
	report        *ScenarioReport
	platformSkips *[]PlatformSkip
}

type commandResult struct {
	stdout string
	stderr string
	exit   int
}

func (r *scenarioRunner) command(args ...string) (commandResult, error) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	command := exec.Command(r.candidate, args...)
	prepareCommand(command)
	command.Dir = r.actualCWD()
	command.Env = append([]string(nil), r.env...)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Start()
	if err == nil {
		waitResult := make(chan error, 1)
		go func() { waitResult <- command.Wait() }()
		select {
		case err = <-waitResult:
		case <-ctx.Done():
			terminateCommand(command)
			err = <-waitResult
		}
	}
	exit := 0
	if err != nil {
		exit = 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exit = exitErr.ExitCode()
		}
		if ctx.Err() != nil {
			exit = -1
		}
	}
	result := commandResult{stdout: stdout.String(), stderr: stderr.String(), exit: exit}
	evidence := CommandEvidence{Argv: append([]string{"$CANDIDATE"}, normalizeArgs(args, r.root)...), Environment: sanitizedEnvironment(r.env, r.root), Stdout: normalize(result.stdout, r.root), Stderr: normalize(result.stderr, r.root), ExitCode: exit, DurationMS: time.Since(started).Milliseconds(), Assertions: []string{}}
	r.report.Commands = append(r.report.Commands, evidence)
	if err != nil {
		return result, fmt.Errorf("%s: %w", strings.Join(args, " "), err)
	}
	return result, nil
}

func (r *scenarioRunner) expectFailure(args ...string) error {
	result, err := r.command(args...)
	if err == nil || result.exit == 0 {
		return fmt.Errorf("expected command failure: %s", strings.Join(args, " "))
	}
	if result.exit != 1 {
		return fmt.Errorf("expected command %s to exit 1, got %d", strings.Join(args, " "), result.exit)
	}
	return nil
}

func (r *scenarioRunner) expectFailureContaining(want string, args ...string) error {
	result, err := r.command(args...)
	if err == nil || result.exit == 0 {
		return fmt.Errorf("expected command failure: %s", strings.Join(args, " "))
	}
	if result.exit != 1 {
		return fmt.Errorf("expected command %s to exit 1, got %d", strings.Join(args, " "), result.exit)
	}
	if !strings.Contains(strings.ToLower(result.stderr), strings.ToLower(want)) {
		return fmt.Errorf("command %s error %q does not contain actionable %q", strings.Join(args, " "), strings.TrimSpace(result.stderr), want)
	}
	r.assert("rejected command exits 1 with actionable error: " + want)
	return nil
}

func (r *scenarioRunner) expectTimedOut(args ...string) error {
	original := r.timeout
	r.timeout = 350 * time.Millisecond
	result, err := r.command(args...)
	r.timeout = original
	if err == nil || result.exit != -1 {
		return fmt.Errorf("expected bounded timeout for %s, exit=%d error=%v", strings.Join(args, " "), result.exit, err)
	}
	r.assert("fresh/malformed lock is bounded by the runner deadline")
	return nil
}

func (r *scenarioRunner) json(out any, args ...string) error {
	result, err := r.command(append([]string{"--json"}, args...)...)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(result.stdout))
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("decode JSON from %s: %w", strings.Join(args, " "), err)
	}
	r.assert("valid JSON: " + strings.Join(args, " "))
	return nil
}

func (r *scenarioRunner) assert(label string) {
	r.report.Assertions = append(r.report.Assertions, label)
	if len(r.report.Commands) > 0 {
		r.report.Commands[len(r.report.Commands)-1].Assertions = append(r.report.Commands[len(r.report.Commands)-1].Assertions, label)
	}
}

func (r *scenarioRunner) skip(scenario, reason string) {
	if r.platformSkips == nil {
		return
	}
	for _, existing := range *r.platformSkips {
		if existing.Scenario == scenario && existing.Reason == reason {
			return
		}
	}
	*r.platformSkips = append(*r.platformSkips, PlatformSkip{Scenario: scenario, Reason: reason})
}

func (r *scenarioRunner) cwd() string {
	return filepath.Join(r.root, "repo with spaces", "nested")
}

func scenarioLifecycle(r *scenarioRunner) error {
	project, err := r.newGitProject("lifecycle project ☃")
	if err != nil {
		return err
	}
	r.setCWD(project)
	if _, err = r.command("help"); err != nil {
		return err
	}
	if _, err = r.command("schema"); err != nil {
		return err
	}
	if _, err = r.command("version"); err != nil {
		return err
	}
	if _, err = r.command("task", "list"); err != nil {
		return err
	}
	var first core.TaskView
	if err = r.json(&first, "task", "create", "--title", "Full lifecycle π", "--description", "spaces and unicode ☃", "--priority", "urgent", "--estimate", "xl", "--lane", "release", "--model", "gpt-5.6", "--worktree-name", "lifecycle", "--worktree-dir", filepath.Join(project, "linked worktree"), "--agent", "Alice"); err != nil {
		return err
	}
	if first.Title != "Full lifecycle π" || first.Priority != core.PriorityUrgent || first.Estimate != core.EstimateXL || first.Assignee != "Alice" {
		return fmt.Errorf("create metadata mismatch: %#v", first.Task)
	}
	r.assert("create preserves all scheduling/worktree metadata")
	for _, args := range [][]string{{"task", "show", first.ShortID}, {"task", "list"}} {
		var value any
		if err := r.json(&value, args...); err != nil {
			return err
		}
	}
	var updated core.TaskView
	if err = r.json(&updated, "task", "update", first.ShortID, "--description", "edited description"); err != nil {
		return err
	}
	if updated.Description != "edited description" {
		return errors.New("task update did not change description")
	}
	if err = r.json(&updated, "task", "edit", first.ShortID, "--lane", "qa"); err != nil {
		return err
	}
	if err = r.json(&updated, "task", "comment", first.ShortID, "--agent", "Alice", "--message", "comment with π"); err != nil {
		return err
	}
	var graph any
	if err = r.json(&graph, "graph", "--status", "all"); err != nil {
		return err
	}
	var ready core.TaskView
	if err = r.json(&ready, "task", "ready", "--agent", "Alice"); err != nil {
		return err
	}
	if ready.ID != first.ID {
		return errors.New("ready did not return lifecycle task")
	}
	if err = r.json(&updated, "task", "start", first.ShortID, "--agent", "Alice"); err != nil {
		return err
	}
	if err = r.json(&updated, "task", "pause", first.ShortID, "--agent", "Alice"); err != nil {
		return err
	}
	if err = r.json(&updated, "task", "start", first.ShortID, "--agent", "Alice"); err != nil {
		return err
	}
	var second core.TaskView
	if err = r.json(&second, "task", "create", "--title", "next task", "--depends-on", first.ShortID, "--agent", "Bob"); err != nil {
		return err
	}
	if err = r.json(&updated, "task", "done", first.ShortID, "--agent", "Alice"); err != nil {
		return err
	}
	if err = r.json(&updated, "task", "next", "--agent", "Bob"); err != nil {
		return err
	}
	if updated.ID != second.ID {
		return fmt.Errorf("next returned %s, want %s", updated.ShortID, second.ShortID)
	}
	if err = r.json(&updated, "task", "done", second.ShortID, "--agent", "Bob"); err != nil {
		return err
	}
	export := filepath.Join(r.root, "lifecycle export with spaces")
	if _, err = r.command("export", "--out", export); err != nil {
		return err
	}
	if err = validateStore(filepath.Join(project, ".wtp")); err != nil {
		return err
	}
	if err = validateExport(export, 2); err != nil {
		return err
	}
	r.assert("lifecycle persisted store and canonical export validate")
	return nil
}

func scenarioStatsAndCustomStatuses(r *scenarioRunner) error {
	project, err := r.newGitProject("stats and custom statuses project")
	if err != nil {
		return err
	}
	configuration := `{"additionalStatuses":[{"name":"waitingForReview","category":"waiting"},{"name":"vendorBlocked","category":"blocked"},{"name":"verificationFailed","category":"failed"}]}`
	if err = writeConfig(project, configuration); err != nil {
		return err
	}
	r.setCWD(project)

	var waiting, blocked, failed, todo core.TaskView
	if err = r.json(&waiting, "task", "create", "--title", "Review π", "--status", "waitingForReview", "--priority", "urgent", "--estimate", "xl", "--lane", "qa", "--model", "gpt-5.6", "--agent", "Reviewer"); err != nil {
		return err
	}
	if waiting.Status != "waitingForReview" || waiting.StartedAt == nil || waiting.CompletedAt != nil {
		return fmt.Errorf("custom waiting lifecycle mismatch: %#v", waiting.Task)
	}
	if err = r.json(&waiting, "task", "set-status", waiting.ShortID, "inProgress", "--agent", "Reviewer"); err != nil {
		return err
	}
	if err = r.json(&waiting, "task", "set-status", waiting.ShortID, "waitingForReview", "--agent", "Reviewer"); err != nil {
		return err
	}
	if err = r.json(&waiting, "task", "comment", waiting.ShortID, "--agent", "Reviewer", "--message", "ready for review"); err != nil {
		return err
	}
	if err = r.json(&blocked, "task", "create", "--title", "Vendor dependency", "--status", "vendorBlocked", "--depends-on", waiting.ShortID); err != nil {
		return err
	}
	if blocked.StartedAt != nil || blocked.CompletedAt != nil {
		return fmt.Errorf("custom blocked lifecycle mismatch: %#v", blocked.Task)
	}
	if err = r.json(&failed, "task", "create", "--title", "Failed verification", "--status", "verificationFailed"); err != nil {
		return err
	}
	if failed.StartedAt == nil || failed.CompletedAt == nil {
		return fmt.Errorf("custom failed lifecycle mismatch: %#v", failed.Task)
	}
	if err = r.json(&todo, "task", "create", "--title", "Ordinary todo"); err != nil {
		return err
	}
	if err = r.expectFailureContaining("invalid status", "task", "set-status", todo.ShortID, "notConfigured"); err != nil {
		return err
	}

	var handoff map[string]any
	if err = r.json(&handoff, "handoff", "write", "--agent", "Release", "--message", "global context"); err != nil {
		return err
	}
	if err = r.json(&handoff, "handoff", "write", "--agent", "Reviewer", "--message", "review context", "--task", waiting.ShortID); err != nil {
		return err
	}
	if err = r.json(&handoff, "handoff", "write", "--agent", "Vendor", "--message", "vendor context", "--task", blocked.ShortID); err != nil {
		return err
	}

	var overview stats.Report
	if err = r.json(&overview, "stats"); err != nil {
		return err
	}
	wantCounts := []stats.Bucket{
		{Value: "todo", Count: 1}, {Value: "inProgress", Count: 0},
		{Value: "paused", Count: 0}, {Value: "done", Count: 0},
		{Value: "waitingForReview", Count: 1}, {Value: "vendorBlocked", Count: 1},
		{Value: "verificationFailed", Count: 1},
	}
	if overview.TotalTasks != 4 || !equalBuckets(overview.StatusCounts, wantCounts) {
		return fmt.Errorf("stats overview task/status counts mismatch: %#v", overview)
	}
	if overview.Comments.TasksWithComments != 1 || overview.Comments.TotalRecords != 1 || overview.Dependencies.TasksWithDependencies != 1 || overview.Dependencies.DirectDependencyTotal != 1 {
		return fmt.Errorf("stats overview comment/dependency metrics mismatch: %#v", overview)
	}
	if overview.Handoffs != (stats.HandoffMetrics{Total: 3, AllStatusTotal: 3, Global: 1, TaskScoped: 2}) {
		return fmt.Errorf("stats overview handoff metrics mismatch: %#v", overview.Handoffs)
	}

	var focused stats.FocusedReport
	if err = r.json(&focused, "stats", "waitingForReview", "model"); err != nil {
		return err
	}
	if focused.Status != "waitingForReview" || focused.TotalTasks != 1 || focused.Attribute != stats.AttributeModel || focused.Buckets == nil || !equalBuckets(*focused.Buckets, []stats.Bucket{{Value: "gpt-5.6", Count: 1}}) {
		return fmt.Errorf("focused custom-status stats mismatch: %#v", focused)
	}
	var filtered stats.Report
	if err = r.json(&filtered, "stats", "waitingForReview"); err != nil {
		return err
	}
	if filtered.Handoffs != (stats.HandoffMetrics{Total: 2, AllStatusTotal: 3, Global: 1, TaskScoped: 1}) {
		return fmt.Errorf("filtered stats handoff metrics mismatch: %#v", filtered.Handoffs)
	}
	text, err := r.command("stats", "waitingForReview", "model")
	if err != nil {
		return err
	}
	for _, want := range []string{"status: waitingForReview", "totalTasks: 1", "gpt-5.6: 1"} {
		if !strings.Contains(text.stdout, want) {
			return fmt.Errorf("human stats output missing %q: %q", want, text.stdout)
		}
	}
	var listed []core.TaskView
	if err = r.json(&listed, "task", "list", "--status", "waitingForReview"); err != nil {
		return err
	}
	if len(listed) != 1 || listed[0].ID != waiting.ID {
		return fmt.Errorf("custom status list filter mismatch: %#v", listed)
	}
	var ready []core.TaskView
	if err = r.json(&ready, "task", "ready", "--limit", "10"); err != nil {
		return err
	}
	if len(ready) != 1 || ready[0].ID != todo.ID {
		return fmt.Errorf("custom statuses affected claim eligibility: %#v", ready)
	}

	store := filepath.Join(project, ".wtp")
	before, err := manifest(store)
	if err != nil {
		return err
	}
	if err = writeConfig(project, `{}`); err != nil {
		return err
	}
	if err = r.expectFailureContaining("absent from active configuration", "task", "list"); err != nil {
		return err
	}
	after, err := manifest(store)
	if err != nil {
		return err
	}
	unchanged := manifestJSON(before) == manifestJSON(after)
	r.report.Preservation = append(r.report.Preservation, PreservationEvidence{Label: "removed-custom-status", Before: before, After: after, Unchanged: unchanged, Expected: "rejected custom status removal preserves storage"})
	if !unchanged {
		return errors.New("removing an in-use custom status changed persistent task bytes")
	}
	if err = writeConfig(project, configuration); err != nil {
		return err
	}
	if err = validateStore(store); err != nil {
		return err
	}
	r.assert("stats overview/focus and custom status lifecycle/filter/scheduling contracts")
	return nil
}

func equalBuckets(got, want []stats.Bucket) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

type graphEvidenceNode struct {
	Task         *core.TaskView      `json:"task,omitempty"`
	Ref          string              `json:"ref,omitempty"`
	Dependencies []graphEvidenceNode `json:"dependencies,omitempty"`
}

func scenarioBoundedSharedDependencyGraph(r *scenarioRunner) error {
	project, err := r.newGitProject("bounded shared dependency graph project")
	if err != nil {
		return err
	}
	r.setCWD(project)

	const finalLayer = 12
	expectedIDs := make(map[string]struct{}, (finalLayer+1)*2)
	previous := []core.TaskView(nil)
	for layer := 0; layer <= finalLayer; layer++ {
		current := make([]core.TaskView, 0, 2)
		for _, suffix := range []string{"a", "b"} {
			args := []string{"task", "create", "--title", fmt.Sprintf("shared graph layer %02d %s", layer, suffix)}
			if len(previous) != 0 {
				args = append(args, "--depends-on", previous[0].ShortID+","+previous[1].ShortID)
			}
			var task core.TaskView
			if err = r.json(&task, args...); err != nil {
				return err
			}
			expectedIDs[task.ID] = struct{}{}
			current = append(current, task)
		}
		previous = current
	}

	var graph []graphEvidenceNode
	if err = r.json(&graph, "graph", "--status", "all"); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(expectedIDs))
	fullTasks, references, records := 0, 0, 0
	var visit func([]graphEvidenceNode) error
	visit = func(nodes []graphEvidenceNode) error {
		for _, node := range nodes {
			records++
			switch {
			case node.Task != nil && node.Ref == "":
				if _, expected := expectedIDs[node.Task.ID]; !expected {
					return fmt.Errorf("graph expanded unexpected task %s", node.Task.ID)
				}
				if _, duplicate := seen[node.Task.ID]; duplicate {
					return fmt.Errorf("graph expanded shared task %s more than once", node.Task.ID)
				}
				seen[node.Task.ID] = struct{}{}
				fullTasks++
			case node.Task == nil && node.Ref != "":
				if _, alreadyExpanded := seen[node.Ref]; !alreadyExpanded {
					return fmt.Errorf("graph reference %s appeared before its expanded task", node.Ref)
				}
				if len(node.Dependencies) != 0 {
					return fmt.Errorf("graph reference %s recursively expanded dependencies", node.Ref)
				}
				references++
			default:
				return errors.New("graph node must contain exactly one of task or ref")
			}
			if err := visit(node.Dependencies); err != nil {
				return err
			}
		}
		return nil
	}
	if err = visit(graph); err != nil {
		return err
	}
	if fullTasks != 26 || len(seen) != len(expectedIDs) || references != 24 || records != 50 {
		return fmt.Errorf("bounded shared graph counts = full %d unique %d refs %d records %d, want 26/26/24/50", fullTasks, len(seen), references, records)
	}

	textGraph, err := r.command("graph", "--status", "all")
	if err != nil {
		return err
	}
	if markers := strings.Count(textGraph.stdout, "(already shown)"); markers != references {
		return fmt.Errorf("shared graph text reference markers = %d, want %d", markers, references)
	}
	if err = validateStore(filepath.Join(project, ".wtp")); err != nil {
		return err
	}
	r.assert("shared dependency graph expands each task once and emits bounded explicit references")
	return nil
}

func scenarioDependenciesAndOwnership(r *scenarioRunner) error {
	project, err := r.newGitProject("dependency project")
	if err != nil {
		return err
	}
	r.setCWD(project)
	var root, left, right, sink core.TaskView
	if err = r.json(&root, "task", "create", "--title", "root", "--priority", "high", "--agent", "owner"); err != nil {
		return err
	}
	if err = r.json(&left, "task", "create", "--title", "fan out left", "--depends-on", root.ShortID); err != nil {
		return err
	}
	if err = r.json(&right, "task", "create", "--title", "fan out right", "--depends-on", root.ShortID); err != nil {
		return err
	}
	if err = r.json(&sink, "task", "create", "--title", "fan in", "--depends-on", left.ShortID+","+right.ShortID); err != nil {
		return err
	}
	if err = r.expectFailure("task", "start", sink.ShortID, "--agent", "owner"); err != nil {
		return err
	}
	var ready []core.TaskView
	if err = r.json(&ready, "task", "ready", "--limit", "4", "--agent", "owner"); err != nil {
		return err
	}
	if len(ready) != 1 || ready[0].ID != root.ID {
		return fmt.Errorf("deterministic initial ready set = %#v", ready)
	}
	if err = r.json(&root, "task", "next", "--agent", "owner"); err != nil {
		return err
	}
	if err = r.json(&root, "task", "done", root.ShortID, "--agent", "owner"); err != nil {
		return err
	}
	if err = r.json(&ready, "task", "ready", "--limit", "4", "--agent", "owner"); err != nil {
		return err
	}
	if len(ready) != 2 || ready[0].ShortID >= ready[1].ShortID {
		return fmt.Errorf("fan-out ready ordering = %#v", ready)
	}
	if err = r.json(&left, "task", "start", left.ShortID, "--agent", "owner"); err != nil {
		return err
	}
	if err = r.json(&left, "task", "done", left.ShortID, "--agent", "owner"); err != nil {
		return err
	}
	if err = r.json(&right, "task", "start", right.ShortID, "--agent", "owner"); err != nil {
		return err
	}
	if err = r.json(&right, "task", "done", right.ShortID, "--agent", "owner"); err != nil {
		return err
	}
	var claimed core.TaskView
	if err = r.json(&claimed, "task", "next", "--agent", "foreign"); err != nil {
		return err
	}
	if claimed.ID != sink.ID || claimed.Assignee != "foreign" {
		return errors.New("fan-in task was not claimed after prerequisites")
	}
	var listed []core.TaskView
	if err = r.json(&listed, "task", "list", "--agent", "unassigned"); err != nil {
		return err
	}
	for _, task := range listed {
		if task.ID == sink.ID && task.Readiness.Claimable {
			return errors.New("foreign agent unexpectedly claimable")
		}
	}
	if err = r.json(&claimed, "task", "done", sink.ShortID, "--agent", "foreign"); err != nil {
		return err
	}
	r.assert("fan-out/fan-in blocking, ordering, and ownership safety")
	return validateStore(filepath.Join(project, ".wtp"))
}

func scenarioHandoffsAndExport(r *scenarioRunner) error {
	emptyProject, err := r.newGitProject("empty export project")
	if err != nil {
		return err
	}
	r.setCWD(emptyProject)
	emptyExport := filepath.Join(r.root, "empty export")
	if _, err = r.command("export", "--out", emptyExport); err != nil {
		return err
	}
	if err = validateExport(emptyExport, 0); err != nil {
		return err
	}
	project, err := r.newGitProject("handoff project")
	if err != nil {
		return err
	}
	r.setCWD(project)
	var task core.TaskView
	if err = r.json(&task, "task", "create", "--title", "handoff target"); err != nil {
		return err
	}
	var write map[string]any
	if err = r.json(&write, "handoff", "write", "--agent", "A", "--message", "global one"); err != nil {
		return err
	}
	if err = r.json(&write, "handoff", "write", "--agent", "B", "--message", "task one", "--task", task.ShortID); err != nil {
		return err
	}
	if err = r.json(&write, "handoff", "write", "--agent", "C", "--message", "task replacement", "--task", task.ShortID, "--replace"); err != nil {
		return err
	}
	if err = r.json(&write, "handoff", "write", "--agent", "D", "--message", "global to purge by id"); err != nil {
		return err
	}
	globalID := nestedString(write, "handoff", "id")
	if globalID == "" {
		return errors.New("handoff write did not return an ID")
	}
	var got map[string]any
	if err = r.json(&got, "handoff", "get", "--all-scopes", "--all"); err != nil {
		return err
	}
	if countArray(got["handoffs"]) != 3 {
		return fmt.Errorf("handoff append/replace count = %v", got["handoffs"])
	}
	var claimed core.TaskView
	if err = r.json(&claimed, "task", "start", task.ShortID, "--agent", "A"); err != nil {
		return err
	}
	if len(claimed.Handoffs) != 1 || claimed.Handoffs[0].Message != "task replacement" {
		return errors.New("task handoff was not attached on claim")
	}
	if err = r.json(&got, "handoff", "get", "--all-scopes", "--limit", "1"); err != nil {
		return err
	}
	if !boolValue(got["hasMore"]) {
		return errors.New("handoff pagination did not report more records")
	}
	if err = r.json(&got, "handoff", "get", "--task", task.ShortID, "--all"); err != nil {
		return err
	}
	if countArray(got["handoffs"]) != 1 {
		return errors.New("task-scoped handoff get did not return replacement")
	}
	if err = r.json(&got, "handoff", "purge", "--id", globalID); err != nil {
		return err
	}
	if err = r.json(&got, "handoff", "purge", "--global"); err != nil {
		return err
	}
	if err = r.json(&got, "handoff", "purge", "--task", task.ShortID); err != nil {
		return err
	}
	export := filepath.Join(r.root, "handoff export")
	if _, err = r.command("export", "--out", export); err != nil {
		return err
	}
	if _, err = r.command("export", "--out", export); err != nil {
		return err
	}
	if err = validateExport(export, 1); err != nil {
		return err
	}
	stale := task
	stale.ID = "00000000-0000-4000-8000-000000000001"
	stale.ShortID = "wtp-9999"
	staleData, marshalErr := json.Marshal(stale)
	if marshalErr != nil {
		return marshalErr
	}
	if err = os.WriteFile(filepath.Join(export, stale.ID+".json"), staleData, 0o644); err != nil {
		return err
	}
	if _, err = r.command("export", "--out", export); err != nil {
		return err
	}
	if _, statErr := os.Stat(filepath.Join(export, stale.ID+".json")); !errors.Is(statErr, os.ErrNotExist) {
		return errors.New("stale managed export file was not cleaned")
	}
	unmanaged := filepath.Join(r.root, "unmanaged export")
	if err = os.MkdirAll(unmanaged, 0o755); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(unmanaged, "keep.txt"), []byte("unmanaged"), 0o644); err != nil {
		return err
	}
	if err = r.expectFailure("export", "--out", unmanaged); err != nil {
		return err
	}
	if _, statErr := os.Stat(filepath.Join(unmanaged, "keep.txt")); statErr != nil {
		return errors.New("unmanaged export destination changed")
	}
	if err = r.expectFailure("export", "--out", filepath.Join(project, ".wtp")); err != nil {
		return err
	}
	if err = validateStore(filepath.Join(project, ".wtp")); err != nil {
		return err
	}
	r.assert("handoff append/replace/get/pagination/claim/purge and safe idempotent export")
	return nil
}

func scenarioGitAndStorageTopology(r *scenarioRunner) error {
	project, err := r.newGitProject("topology project")
	if err != nil {
		return err
	}
	r.setCWD(project)
	var first core.TaskView
	if err = r.json(&first, "task", "create", "--title", "main task"); err != nil {
		return err
	}
	linkedOne := filepath.Join(r.root, "linked one")
	linkedTwo := filepath.Join(r.root, "linked two")
	if err = runGit(project, "worktree", "add", "-b", "CaseBranch", linkedOne); err != nil {
		return err
	}
	secondBranch := "casebranch"
	if !caseSensitiveDirectory(r.root) {
		secondBranch = "lowerbranch"
		r.skip("git-and-storage-topology/case-sensitive-branches", "filesystem is case-insensitive; case-only branch scope coverage is not applicable")
	}
	if err = runGit(project, "worktree", "add", "-b", secondBranch, linkedTwo); err != nil {
		return err
	}
	r.setCWD(linkedOne)
	var one core.TaskView
	if err = r.json(&one, "task", "create", "--title", "upper-case branch"); err != nil {
		return err
	}
	r.setCWD(linkedTwo)
	var two core.TaskView
	if err = r.json(&two, "task", "create", "--title", "lower-case branch"); err != nil {
		return err
	}
	if one.ShortID == two.ShortID {
		return errors.New("case-sensitive branches shared a task scope")
	}
	if err = runGit(linkedTwo, "branch", "-m", "renamed"); err != nil {
		return err
	}
	var renamed core.TaskView
	if err = r.json(&renamed, "task", "create", "--title", "renamed branch task"); err != nil {
		return err
	}
	if renamed.ShortID == two.ShortID {
		return errors.New("branch rename reused old branch scope")
	}
	detached := filepath.Join(r.root, "detached")
	if err = runGit(project, "worktree", "add", "--detach", detached); err != nil {
		return err
	}
	r.setCWD(detached)
	var detachedTask core.TaskView
	if err = r.json(&detachedTask, "task", "create", "--title", "detached task"); err != nil {
		return err
	}
	if !strings.HasPrefix(detachedTask.ShortID, "wtp-") || strings.Count(detachedTask.ShortID, "-") != 1 {
		return errors.New("detached task was not legacy scoped")
	}
	nonGit := filepath.Join(r.root, "non-Git")
	if err = os.MkdirAll(nonGit, 0o755); err != nil {
		return err
	}
	r.setCWD(nonGit)
	var nonGitTask core.TaskView
	if err = r.json(&nonGitTask, "task", "create", "--title", "non-Git task"); err != nil {
		return err
	}
	if nonGitTask.GitBranch != "" {
		return errors.New("non-Git task unexpectedly has branch metadata")
	}
	relativeProject, err := r.newGitProject("relative config project")
	if err != nil {
		return err
	}
	if err = writeConfig(relativeProject, `{"wtpDir":"relative store"}`); err != nil {
		return err
	}
	r.setCWD(relativeProject)
	var relativeTask core.TaskView
	if err = r.json(&relativeTask, "task", "create", "--title", "relative configured store"); err != nil {
		return err
	}
	if _, err = os.Stat(filepath.Join(relativeProject, "relative store", "todo", relativeTask.ShortID+".json")); err != nil {
		return fmt.Errorf("relative configured store: %w", err)
	}
	absoluteStore := filepath.Join(r.root, "absolute external store")
	absoluteProject, err := r.newGitProject("absolute config project")
	if err != nil {
		return err
	}
	if err = writeConfig(absoluteProject, fmt.Sprintf(`{"wtpDir":%q}`, absoluteStore)); err != nil {
		return err
	}
	r.setCWD(absoluteProject)
	if _, err = r.command("task", "create", "--title", "absolute configured store"); err != nil {
		return err
	}
	sharedStore := filepath.Join(r.root, "shared external store")
	sharedA, err := r.newGitProject("shared external A")
	if err != nil {
		return err
	}
	sharedB, err := r.newGitProject("shared external B")
	if err != nil {
		return err
	}
	config := fmt.Sprintf(`{"wtpDir":%q}`, sharedStore)
	if err = writeConfig(sharedA, config); err != nil {
		return err
	}
	if err = writeConfig(sharedB, config); err != nil {
		return err
	}
	r.setCWD(sharedA)
	if _, err = r.command("task", "create", "--title", "shared A"); err != nil {
		return err
	}
	r.setCWD(sharedB)
	if _, err = r.command("task", "create", "--title", "shared B"); err != nil {
		return err
	}
	if err = validateStore(sharedStore); err != nil {
		return err
	}
	r.assert("linked worktrees, case-sensitive branch scopes, rename, detached HEAD, and non-Git")
	return validateStore(filepath.Join(project, ".wtp"))
}

func scenarioConfigurationFailures(r *scenarioRunner) error {
	root := filepath.Join(r.root, "config fixture")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	r.setCWD(root)
	if err := os.WriteFile(filepath.Join(root, ".wtp.json"), []byte(`{"wtpDir":`), 0o644); err != nil {
		return err
	}
	if err := r.expectFailure("task", "list"); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, ".wtp.json"), []byte(`{"wtpDir":"storage file"}`), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "storage file"), []byte("do not mutate"), 0o644); err != nil {
		return err
	}
	if err := r.expectFailure("task", "list"); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(root, "storage file"))
	if err != nil || string(data) != "do not mutate" {
		return errors.New("inaccessible storage fixture was mutated")
	}
	if err := os.WriteFile(filepath.Join(root, ".wtp.json"), []byte(`{"wtpDir":"new storage"}`), 0o644); err != nil {
		return err
	}
	if err = r.json(new([]core.TaskView), "task", "list"); err != nil {
		return err
	}
	if _, statErr := os.Stat(filepath.Join(root, "storage file", "todo")); statErr == nil {
		return errors.New("config change initialized unintended old storage")
	}
	r.assert("invalid config and storage failures preserve existing targets")
	return nil
}

func scenarioNestedInvocation(r *scenarioRunner) error {
	project, err := r.newGitProject("nested invocation project")
	if err != nil {
		return err
	}
	nested := filepath.Join(project, "deep", "space path", "子")
	if err = os.MkdirAll(nested, 0o755); err != nil {
		return err
	}
	r.setCWD(nested)
	var task core.TaskView
	if err = r.json(&task, "task", "create", "--title", "nested task"); err != nil {
		return err
	}
	if !strings.HasSuffix(filepath.Clean(task.WorktreeDir), filepath.Base(project)) {
		return fmt.Errorf("nested worktree root = %q", task.WorktreeDir)
	}
	if _, err = r.command("--json", "--get-tasks", "--status", "todo"); err != nil {
		return err
	}
	if err = validateStore(filepath.Join(project, ".wtp")); err != nil {
		return err
	}
	if _, err = r.command("--json", "version"); err != nil {
		return err
	}
	r.assert("nested invocation and legacy flag parity")
	return nil
}

func (r *scenarioRunner) newGitProject(name string) (string, error) {
	project := filepath.Join(r.root, name)
	if err := os.MkdirAll(project, 0o755); err != nil {
		return "", err
	}
	if err := runGit(project, "init", "-q"); err != nil {
		return "", err
	}
	if err := runGit(project, "config", "user.email", "qa@example.invalid"); err != nil {
		return "", err
	}
	if err := runGit(project, "config", "user.name", "Hermetic QA"); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(project, "README ☃.md"), []byte("fixture\n"), 0o644); err != nil {
		return "", err
	}
	if err := runGit(project, "add", "."); err != nil {
		return "", err
	}
	if err := runGit(project, "commit", "-qm", "fixture"); err != nil {
		return "", err
	}
	r.setCWD(project)
	return project, nil
}

func (r *scenarioRunner) setCWD(path string) {
	// All normal commands use the runner's repository root. The directory is
	// changed by placing a marker consumed by command; this avoids mutating the
	// harness process's cwd and remains safe when scenarios run in parallel in
	// future revisions.
	r.env = replaceEnv(r.env, "WTP_QA_CWD", path)
	_ = os.MkdirAll(filepath.Join(r.root, "repo with spaces", "nested"), 0o755)
}

func runGit(dir string, args ...string) error {
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	globalConfig := "/dev/null"
	if runtime.GOOS == "windows" {
		globalConfig = "NUL"
	}
	qaHome := filepath.Join(dir, ".qa-home")
	if err := os.MkdirAll(qaHome, 0o755); err != nil {
		return err
	}
	command.Env = append(os.Environ(), "HOME="+qaHome, "USERPROFILE="+qaHome, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+globalConfig)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func caseSensitiveDirectory(root string) bool {
	probe := filepath.Join(root, ".wtp-case-probe")
	if err := os.WriteFile(probe, []byte("probe"), 0o644); err != nil {
		return true
	}
	defer os.Remove(probe)
	_, err := os.Stat(filepath.Join(root, ".WTP-CASE-PROBE"))
	return errors.Is(err, os.ErrNotExist)
}

func writeConfig(project, contents string) error {
	return os.WriteFile(filepath.Join(project, ".wtp.json"), []byte(contents), 0o644)
}

func (r *scenarioRunner) actualCWD() string {
	for _, value := range r.env {
		if strings.HasPrefix(value, "WTP_QA_CWD=") {
			return strings.TrimPrefix(value, "WTP_QA_CWD=")
		}
	}
	return r.cwd()
}

func disposableEnv(root string) []string {
	pathValue := os.Getenv("PATH")
	values := []string{"HOME=" + filepath.Join(root, "home"), "USERPROFILE=" + filepath.Join(root, "userprofile"), "XDG_CONFIG_HOME=" + filepath.Join(root, "xdg"), "GIT_CONFIG_GLOBAL=" + filepath.Join(root, "gitconfig"), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "WTP_QA_HERMETIC=true", "WTP_QA_CWD=" + filepath.Join(root, "repo with spaces", "nested"), "PATH=" + pathValue}
	for _, dir := range []string{"home", "userprofile", "xdg", "repo with spaces", filepath.Join("repo with spaces", "nested")} {
		_ = os.MkdirAll(filepath.Join(root, dir), 0o755)
	}
	return values
}

func replaceEnv(env []string, name, value string) []string {
	prefix := name + "="
	for i := range env {
		if strings.HasPrefix(env[i], prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func sanitizedEnvironment(env []string, root string) map[string]string {
	result := make(map[string]string)
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if key == "PATH" {
			result[key] = "$PATH"
		} else {
			result[key] = normalize(value, root)
		}
	}
	return result
}

func normalize(value, root string) string {
	value = strings.ReplaceAll(value, root, "$WORKDIR")
	value = uuidPattern.ReplaceAllString(value, "$$UUID")
	value = timestampPattern.ReplaceAllString(value, "$$TIME")
	return value
}

func normalizeArgs(args []string, root string) []string {
	out := make([]string, len(args))
	for i, value := range args {
		out[i] = normalize(value, root)
	}
	return out
}

func candidateJSON(candidate string, env []string, timeout time.Duration) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, candidate, "--json", "version")
	prepareCommand(command)
	command.Env = env
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &bytes.Buffer{}
	if err := command.Run(); err != nil {
		return nil, err
	}
	var value any
	err := json.Unmarshal(output.Bytes(), &value)
	return value, err
}

func gitOutput(dir string, args ...string) string {
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, _ := command.Output()
	return string(output)
}
func toolVersion(name string, args ...string) string {
	output, err := exec.Command(name, args...).Output()
	if err != nil {
		return "unavailable"
	}
	return strings.TrimSpace(string(output))
}

func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func manifest(root string) ([]ManifestFile, error) {
	var files []ManifestFile
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && filepath.Base(path) == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		digest, digestErr := fileSHA256(path)
		if digestErr != nil {
			return digestErr
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		files = append(files, ManifestFile{Path: filepath.ToSlash(rel), SHA256: digest, Size: info.Size()})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, err
}

func equalManifests(left, right []ManifestFile) bool {
	return manifestJSON(left) == manifestJSON(right)
}
func manifestJSON(value []ManifestFile) string { data, _ := json.Marshal(value); return string(data) }

func normalizedReport(report Report, digest string, before []ManifestFile) NormalizedReport {
	result := NormalizedReport{SchemaVersion: ReportVersion, Seed: report.Seed, Repeat: report.Repeat, CandidateSHA256: digest, Status: report.Status, SourceManifestSHA256: manifestDigest(before), RaceStatus: report.Race.Status}
	for _, scenario := range report.Scenarios {
		assertions := make([]string, 0, len(scenario.Assertions))
		for _, assertion := range scenario.Assertions {
			assertions = append(assertions, stableNormalize(assertion))
		}
		result.Scenarios = append(result.Scenarios, NormalizedScenario{Name: scenario.Name, Status: scenario.Status, Assertions: assertions})
	}
	return result
}

func validateReport(report Report) error {
	if report.SchemaVersion != ReportVersion {
		return fmt.Errorf("report schema %q, want %q", report.SchemaVersion, ReportVersion)
	}
	if report.Repeat < 1 {
		return errors.New("report repeat must be positive")
	}
	if report.Status != "passed" && report.Status != "failed" {
		return fmt.Errorf("report status %q is invalid", report.Status)
	}
	if report.Status == "passed" && report.Verdict != "GO" {
		return fmt.Errorf("passed report verdict %q is not GO", report.Verdict)
	}
	if report.Status == "failed" && report.Verdict != "NO_GO" {
		return fmt.Errorf("failed report verdict %q is not NO_GO", report.Verdict)
	}
	if report.Race.Status != "passed" && report.Race.Status != "failed" && report.Race.Status != "not_applicable" && report.Race.Status != "not_run" {
		return fmt.Errorf("race result %q is not explicit", report.Race.Status)
	}
	required := []string{
		"lifecycle", "stats-and-custom-statuses", "bounded-shared-dependency-graph", "dependencies-and-ownership", "handoffs-and-export",
		"git-and-storage-topology", "configuration-failures",
		"nested-invocation-and-hermeticity", "contention-creates", "contention-next",
		"contention-handoffs", "contention-readers-and-writers", "failure-recovery",
	}
	minimumProcesses := map[string]int{
		"contention-creates":             64,
		"contention-next":                16,
		"contention-handoffs":            32,
		"contention-readers-and-writers": 16,
	}
	seen := map[string]bool{}
	for _, scenario := range report.Scenarios {
		baseName := strings.TrimSuffix(scenario.Name, "#"+strconv.Itoa(scenario.Iteration))
		seen[baseName] = true
		if scenario.Status == "passed" && len(scenario.InvariantFailures) != 0 {
			return fmt.Errorf("passed scenario %s has invariant failures", scenario.Name)
		}
		if scenario.ProcessCount != len(scenario.ProcessExitCodes) || scenario.ProcessCount != len(scenario.ProcessDurationsMS) {
			return fmt.Errorf("scenario %s process evidence is incomplete", scenario.Name)
		}
		if minimum, ok := minimumProcesses[baseName]; ok && scenario.Status == "passed" && scenario.ProcessCount < minimum {
			return fmt.Errorf("passed scenario %s has %d processes, want at least %d", scenario.Name, scenario.ProcessCount, minimum)
		}
		if scenario.Status == "passed" {
			for _, evidence := range scenario.Preservation {
				if strings.Contains(evidence.Expected, "rejected") && !evidence.Unchanged {
					return fmt.Errorf("passed scenario %s has changed preservation evidence %s", scenario.Name, evidence.Label)
				}
			}
		}
	}
	if report.Status == "passed" {
		for _, name := range required {
			if !seen[name] {
				return fmt.Errorf("passed report is missing scenario %s", name)
			}
		}
		for _, scenario := range report.Scenarios {
			if scenario.Status != "passed" {
				return fmt.Errorf("passed report contains %s scenario %s", scenario.Status, scenario.Name)
			}
		}
		if report.Race.Status == "not_run" || report.Race.Status == "failed" {
			return fmt.Errorf("passed report has race result %s", report.Race.Status)
		}
	}
	if report.Normalized.SchemaVersion != ReportVersion || report.Normalized.Status != report.Status || report.Normalized.RaceStatus != report.Race.Status {
		return errors.New("normalized report does not match report status")
	}
	return nil
}

func reproductionCommand(candidate, sourceRoot string, options Options) string {
	asset := ""
	if options.CandidateAsset != "" {
		asset = fmt.Sprintf(" --candidate-asset %q", options.CandidateAsset)
	}
	return fmt.Sprintf("go run ./cmd/wtp-prerelease-qa --candidate %q%s --source-root %q --seed %d --repeat %d --timeout %s --suite-timeout %s --keep-workdir", candidate, asset, sourceRoot, options.Seed, options.Repeat, options.Timeout, options.SuiteTimeout)
}

func stableNormalize(value string) string {
	value = workRootPattern.ReplaceAllString(value, "$$WORKDIR")
	value = uuidPattern.ReplaceAllString(value, "$$UUID")
	value = timestampPattern.ReplaceAllString(value, "$$TIME")
	return value
}
func manifestDigest(files []ManifestFile) string {
	sum := sha256.Sum256([]byte(manifestJSON(files)))
	return hex.EncodeToString(sum[:])
}

func writeReport(path string, report Report) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(absolute, data, 0o644)
}

type handoffCollection struct {
	Handoffs []core.Handoff `json:"handoffs"`
}
type allocationIndex struct {
	Branch string `json:"branch,omitempty"`
	Next   int    `json:"next"`
}

func validateStore(path string) error {
	statuses, err := statusDirectories(path)
	if err != nil {
		return err
	}
	catalog, err := statusCatalogForStore(path, statuses)
	if err != nil {
		return err
	}
	ids := map[string]bool{}
	for _, status := range statuses {
		dir := string(status)
		entries, err := os.ReadDir(filepath.Join(path, dir))
		if err != nil {
			return fmt.Errorf("read store %s: %w", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			data, readErr := os.ReadFile(filepath.Join(path, dir, entry.Name()))
			if readErr != nil {
				return readErr
			}
			var task core.Task
			if err = json.Unmarshal(data, &task); err != nil {
				return fmt.Errorf("decode %s: %w", entry.Name(), err)
			}
			if err = task.ValidateWithCatalog(catalog); err != nil {
				return fmt.Errorf("validate %s: %w", entry.Name(), err)
			}
			if task.Status != status {
				return fmt.Errorf("task %s is in %s but status is %s", task.ShortID, dir, task.Status)
			}
			if ids[task.ID] {
				return fmt.Errorf("duplicate task id %s", task.ID)
			}
			ids[task.ID] = true
		}
	}
	entries, err := os.ReadDir(filepath.Join(path, "meta"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "index") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(path, "meta", entry.Name()))
		if readErr != nil {
			return readErr
		}
		var index allocationIndex
		if err = json.Unmarshal(data, &index); err != nil || index.Next < 1 {
			return fmt.Errorf("invalid allocation index %s", entry.Name())
		}
	}
	handoffData, err := os.ReadFile(filepath.Join(path, "handoffs.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		var collection handoffCollection
		if json.Unmarshal(handoffData, &collection) != nil {
			return errors.New("invalid handoff collection")
		}
		seen := map[string]bool{}
		for _, handoff := range collection.Handoffs {
			if err = handoff.Validate(); err != nil {
				return err
			}
			if seen[handoff.ID] {
				return errors.New("duplicate handoff id")
			}
			seen[handoff.ID] = true
		}
	}
	return nil
}

func statusDirectories(path string) ([]core.Status, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	statuses := make([]core.Status, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != "meta" {
			statuses = append(statuses, core.Status(entry.Name()))
		}
	}
	sort.Slice(statuses, func(i, j int) bool {
		return statusDirectoryOrder(statuses[i]) < statusDirectoryOrder(statuses[j])
	})
	return statuses, nil
}

func statusDirectoryOrder(status core.Status) int {
	switch status {
	case core.StatusTodo:
		return 0
	case core.StatusInProgress:
		return 1
	case core.StatusPaused:
		return 2
	case core.StatusDone:
		return 3
	default:
		return 4
	}
}

func statusCatalogForStore(path string, statuses []core.Status) (core.StatusCatalog, error) {
	if discovered, err := config.Discover(filepath.Dir(path)); err == nil && len(discovered.AdditionalStatuses) > 0 {
		return discovered.StatusCatalog()
	}
	additional := make([]core.StatusDefinition, 0)
	for _, status := range statuses {
		switch status {
		case core.StatusTodo, core.StatusInProgress, core.StatusPaused, core.StatusDone:
			continue
		}
		category := core.StatusCategoryBlocked
		entries, err := os.ReadDir(filepath.Join(path, string(status)))
		if err != nil {
			return core.StatusCatalog{}, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(path, string(status), entry.Name()))
			if err != nil {
				return core.StatusCatalog{}, err
			}
			var task struct {
				StartedAt   *time.Time `json:"startedAt"`
				CompletedAt *time.Time `json:"completedAt"`
			}
			if err := json.Unmarshal(data, &task); err != nil {
				return core.StatusCatalog{}, err
			}
			if task.CompletedAt != nil {
				category = core.StatusCategoryFailed
			} else if task.StartedAt != nil {
				category = core.StatusCategoryWaiting
			}
			break
		}
		additional = append(additional, core.StatusDefinition{Name: status, Category: category})
	}
	return core.NewStatusCatalog(additional)
}

func validateExport(path string, taskCount int) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	got := 0
	for _, entry := range entries {
		if entry.IsDir() {
			return fmt.Errorf("export contains directory %s", entry.Name())
		}
		data, readErr := os.ReadFile(filepath.Join(path, entry.Name()))
		if readErr != nil {
			return readErr
		}
		if entry.Name() == "handoffs.json" {
			var collection handoffCollection
			if err = json.Unmarshal(data, &collection); err != nil {
				return err
			}
			for _, handoff := range collection.Handoffs {
				if err = handoff.Validate(); err != nil {
					return err
				}
			}
			continue
		}
		var task core.Task
		if err = json.Unmarshal(data, &task); err != nil {
			return err
		}
		if err = task.Validate(); err != nil {
			return err
		}
		got++
	}
	if got != taskCount {
		return fmt.Errorf("export contains %d tasks, want %d", got, taskCount)
	}
	return nil
}

func countArray(value any) int {
	values, ok := value.([]any)
	if !ok {
		return 0
	}
	return len(values)
}
func boolValue(value any) bool { result, _ := value.(bool); return result }

func nestedString(value map[string]any, keys ...string) string {
	var current any = value
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	result, _ := current.(string)
	return result
}

// Keep strconv linked in old Go toolchains where generated build metadata can
// omit it from otherwise identical command paths.
var _ = strconv.IntSize
