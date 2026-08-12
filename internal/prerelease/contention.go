package prerelease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
)

const (
	contentionCreateCount  = 32
	contentionNextCount    = 16
	contentionHandoffCount = 32
)

type batchSpec struct {
	args []string
	cwd  string
	kind string
}

type batchProcess struct {
	spec     batchSpec
	result   commandResult
	duration time.Duration
}

// batch starts every command only after all command goroutines are waiting on
// the same barrier. It intentionally invokes the candidate directly; no shell
// job-control behavior is involved, which keeps the runner portable to Windows.
func (r *scenarioRunner) batch(specs []batchSpec) ([]commandResult, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	suiteContext, cancelSuite := context.WithTimeout(context.Background(), r.suiteTimeout)
	defer cancelSuite()

	processes := make([]batchProcess, len(specs))
	startBarrier := make(chan struct{})
	var waitGroup sync.WaitGroup
	for index, spec := range specs {
		processes[index].spec = spec
		waitGroup.Add(1)
		go func(index int, spec batchSpec) {
			defer waitGroup.Done()
			<-startBarrier
			started := time.Now()
			processContext, cancel := context.WithTimeout(suiteContext, r.timeout)
			defer cancel()
			command := exec.Command(r.candidate, spec.args...)
			prepareCommand(command)
			if spec.cwd == "" {
				command.Dir = r.actualCWD()
			} else {
				command.Dir = spec.cwd
			}
			command.Env = append([]string(nil), r.env...)
			var stdout, stderr strings.Builder
			command.Stdout, command.Stderr = &stdout, &stderr
			err := command.Start()
			if err == nil {
				waitResult := make(chan error, 1)
				go func() { waitResult <- command.Wait() }()
				select {
				case err = <-waitResult:
				case <-processContext.Done():
					terminateCommand(command)
					err = <-waitResult
				}
			}
			exitCode := 0
			if err != nil {
				exitCode = 1
				var exitError *exec.ExitError
				if errors.As(err, &exitError) {
					exitCode = exitError.ExitCode()
				}
				if processContext.Err() != nil {
					exitCode = -1
				}
			}
			processes[index].result = commandResult{stdout: stdout.String(), stderr: stderr.String(), exit: exitCode}
			processes[index].duration = time.Since(started)
		}(index, spec)
	}
	close(startBarrier)
	waitGroup.Wait()

	results := make([]commandResult, len(processes))
	for index, process := range processes {
		results[index] = process.result
		evidence := CommandEvidence{
			Argv:        append([]string{"$CANDIDATE"}, normalizeArgs(process.spec.args, r.root)...),
			Environment: sanitizedEnvironment(r.env, r.root),
			Stdout:      normalize(process.result.stdout, r.root),
			Stderr:      normalize(process.result.stderr, r.root),
			ExitCode:    process.result.exit,
			DurationMS:  process.duration.Milliseconds(),
			Assertions:  []string{},
		}
		r.report.Commands = append(r.report.Commands, evidence)
		r.report.ProcessCount++
		r.report.ProcessExitCodes = append(r.report.ProcessExitCodes, process.result.exit)
		r.report.ProcessDurationsMS = append(r.report.ProcessDurationsMS, process.duration.Milliseconds())
	}
	if suiteContext.Err() != nil {
		return results, fmt.Errorf("contention process suite deadline exceeded: %w", suiteContext.Err())
	}
	return results, nil
}

func scheduleSeed(seed int64, iteration int, scenario string) int64 {
	var hash uint64 = 1469598103934665603
	for index := range scenario {
		hash ^= uint64(scenario[index])
		hash *= 1099511628211
	}
	return seed + int64(hash&0x7fffffffffffffff) + int64(iteration)*7919
}

func shuffledSpecs(specs []batchSpec, seed int64) []batchSpec {
	result := append([]batchSpec(nil), specs...)
	rand.New(rand.NewSource(seed)).Shuffle(len(result), func(i, j int) {
		result[i], result[j] = result[j], result[i]
	})
	return result
}

func (r *scenarioRunner) invariantFailure(message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	for _, existing := range r.report.InvariantFailures {
		if existing == message {
			return
		}
	}
	r.report.InvariantFailures = append(r.report.InvariantFailures, message)
}

func decodeJSONOutput(result commandResult, target any) error {
	decoder := json.NewDecoder(strings.NewReader(result.stdout))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode command JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return errors.New("command returned more than one JSON value")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode trailing command JSON: %w", err)
	}
	return nil
}

func requireBatchSuccess(results []commandResult, operation string) error {
	for index, result := range results {
		if result.exit != 0 {
			return fmt.Errorf("%s process %d exited with code %d: %s", operation, index+1, result.exit, strings.TrimSpace(result.stderr))
		}
	}
	return nil
}

func scenarioContentionCreates(r *scenarioRunner) error {
	legacy := filepath.Join(r.root, "contention legacy")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		return err
	}
	r.setCWD(legacy)
	if err := contentionCreateScope(r, legacy, "legacy", ""); err != nil {
		return err
	}

	named, err := r.newGitProject("contention named branch")
	if err != nil {
		return err
	}
	if err := runGit(named, "branch", "-M", "main"); err != nil {
		return err
	}
	r.setCWD(named)
	if err := contentionCreateScope(r, named, "named branch", "main"); err != nil {
		return err
	}
	r.assert("32 separate processes allocate contiguous legacy and named-branch task IDs")
	r.assert("all create UUIDs, task JSON, indexes, and lock cleanup validate")
	return nil
}

func contentionCreateScope(r *scenarioRunner, project, label, branch string) error {
	// Initialize the disposable store before the synchronized batch. This keeps
	// first-use directory publication out of the 32-process allocation
	// invariant; the batch then measures the actual multi-process allocator on
	// every native filesystem, including slower Windows runners.
	if err := initializeContentionStore(r, project, branch); err != nil {
		return fmt.Errorf("initialize %s contention store: %w", label, err)
	}
	specs := make([]batchSpec, 0, contentionCreateCount)
	for index := 0; index < contentionCreateCount; index++ {
		specs = append(specs, batchSpec{args: []string{"--json", "task", "create", "--title", fmt.Sprintf("%s create %02d", label, index+1)}, cwd: project, kind: "create"})
	}
	results, err := r.batch(shuffledSpecs(specs, r.scheduleSeed))
	if err != nil {
		return err
	}
	if err := requireBatchSuccess(results, label+" create"); err != nil {
		return err
	}
	ids := map[string]bool{}
	shortIDs := map[string]bool{}
	for index, result := range results {
		var task core.TaskView
		if err := decodeJSONOutput(result, &task); err != nil {
			return fmt.Errorf("%s create process %d: %w", label, index+1, err)
		}
		if err := task.Validate(); err != nil {
			return fmt.Errorf("%s create process %d task: %w", label, index+1, err)
		}
		if ids[task.ID] || shortIDs[task.ShortID] {
			return fmt.Errorf("%s create produced duplicate UUID or short ID at process %d", label, index+1)
		}
		ids[task.ID], shortIDs[task.ShortID] = true, true
		parts, parseErr := core.ParseShortID(task.ShortID)
		if parseErr != nil {
			return parseErr
		}
		sequence, parseErr := strconv.Atoi(parts.Sequence)
		if parseErr != nil || sequence != index+1 {
			// The process schedule is deliberately nondeterministic. Contiguous
			// allocation is checked from the complete persisted set below.
			continue
		}
	}
	store := filepath.Join(project, ".wtp")
	if err := validateStore(store); err != nil {
		return fmt.Errorf("%s store: %w", label, err)
	}
	tasks, err := storedTasks(store)
	if err != nil {
		return err
	}
	if err := assertContiguousTasks(tasks, contentionCreateCount, branch); err != nil {
		return fmt.Errorf("%s allocation: %w", label, err)
	}
	if err := assertIndexNext(store, branch, contentionCreateCount+1); err != nil {
		return fmt.Errorf("%s allocation index: %w", label, err)
	}
	if err := assertLockFree(store); err != nil {
		return fmt.Errorf("%s lock cleanup: %w", label, err)
	}
	return nil
}

func initializeContentionStore(r *scenarioRunner, project, branch string) error {
	r.setCWD(project)
	var seed core.TaskView
	if err := r.json(&seed, "task", "create", "--title", "contention store seed"); err != nil {
		return err
	}
	store := filepath.Join(project, ".wtp")
	if err := os.Remove(filepath.Join(store, "todo", seed.ShortID+".json")); err != nil {
		return err
	}
	indexName := "index.json"
	index := allocationIndex{Next: 1}
	if branch != "" {
		indexName = "index-" + core.BranchID(branch) + ".json"
		index.Branch = branch
	}
	return writeJSON(filepath.Join(store, "meta", indexName), index)
}

func scenarioContentionNext(r *scenarioRunner) error {
	project, err := r.newGitProject("contention next")
	if err != nil {
		return err
	}
	if err := runGit(project, "branch", "-M", "main"); err != nil {
		return err
	}
	r.setCWD(project)
	const eligibleCount = 3
	const foreignCount = 2
	for index := 0; index < eligibleCount; index++ {
		var task core.TaskView
		if err := r.jsonAt(&task, project, "task", "create", "--title", fmt.Sprintf("eligible %d", index+1)); err != nil {
			return err
		}
	}
	for index := 0; index < foreignCount; index++ {
		var task core.TaskView
		if err := r.jsonAt(&task, project, "task", "create", "--title", fmt.Sprintf("foreign %d", index+1), "--agent", "foreign-owner"); err != nil {
			return err
		}
	}
	specs := make([]batchSpec, 0, contentionNextCount)
	for index := 0; index < contentionNextCount; index++ {
		specs = append(specs, batchSpec{args: []string{"--json", "task", "next", "--agent", "worker"}, cwd: project, kind: "next"})
	}
	results, err := r.batch(shuffledSpecs(specs, r.scheduleSeed))
	if err != nil {
		return err
	}
	claimed := map[string]bool{}
	successes, noWork := 0, 0
	for index, result := range results {
		switch result.exit {
		case 0:
			var task core.TaskView
			if err := decodeJSONOutput(result, &task); err != nil {
				return fmt.Errorf("next success %d: %w", index+1, err)
			}
			if task.Assignee != "worker" || task.Status != core.StatusInProgress || claimed[task.ID] {
				return fmt.Errorf("next process %d claimed invalid or duplicate task %s", index+1, task.ShortID)
			}
			claimed[task.ID] = true
			successes++
		case 1:
			if !strings.Contains(result.stderr, "no eligible task found") {
				return fmt.Errorf("next process %d failure is not documented no-work behavior: %q", index+1, result.stderr)
			}
			noWork++
		default:
			return fmt.Errorf("next process %d exited with unexpected code %d", index+1, result.exit)
		}
	}
	if successes != eligibleCount || noWork != contentionNextCount-eligibleCount {
		return fmt.Errorf("next claims=%d no-work=%d, want %d and %d", successes, noWork, eligibleCount, contentionNextCount-eligibleCount)
	}
	var listed []core.TaskView
	if err := r.jsonAt(&listed, project, "task", "list"); err != nil {
		return err
	}
	foreignSeen := 0
	for _, task := range listed {
		if task.Assignee == "foreign-owner" {
			foreignSeen++
			if task.Status != core.StatusTodo || task.Assignee == "worker" {
				return fmt.Errorf("foreign task %s was changed by next", task.ShortID)
			}
		}
		if claimed[task.ID] && (task.Status != core.StatusInProgress || task.Assignee != "worker") {
			return fmt.Errorf("persisted claim for %s does not match command result", task.ShortID)
		}
	}
	if foreignSeen != foreignCount || len(claimed) != eligibleCount {
		return fmt.Errorf("foreign tasks=%d claimed=%d, want %d and %d", foreignSeen, len(claimed), foreignCount, eligibleCount)
	}
	store := filepath.Join(project, ".wtp")
	if err := validateStore(store); err != nil {
		return err
	}
	if err := assertLockFree(store); err != nil {
		return err
	}
	r.assert("16 separate task-next processes claim each eligible task exactly once")
	r.assert("extra claimers return documented no-work and foreign assignees remain untouched")
	return nil
}

func scenarioContentionHandoffs(r *scenarioRunner) error {
	project := filepath.Join(r.root, "contention handoffs")
	if err := os.MkdirAll(project, 0o755); err != nil {
		return err
	}
	if err := initializeContentionStore(r, project, ""); err != nil {
		return fmt.Errorf("initialize handoff contention store: %w", err)
	}
	r.setCWD(project)
	specs := make([]batchSpec, 0, contentionHandoffCount)
	for index := 0; index < contentionHandoffCount; index++ {
		specs = append(specs, batchSpec{args: []string{"--json", "handoff", "write", "--agent", fmt.Sprintf("agent-%02d", index+1), "--message", fmt.Sprintf("handoff-%02d", index+1)}, cwd: project, kind: "handoff"})
	}
	results, err := r.batch(shuffledSpecs(specs, r.scheduleSeed))
	if err != nil {
		return err
	}
	if err := requireBatchSuccess(results, "handoff append"); err != nil {
		return err
	}
	ids := map[string]bool{}
	for index, result := range results {
		var write struct {
			Handoff core.Handoff `json:"handoff"`
		}
		if err := decodeJSONOutput(result, &write); err != nil {
			return fmt.Errorf("handoff process %d: %w", index+1, err)
		}
		if err := write.Handoff.Validate(); err != nil {
			return err
		}
		if ids[write.Handoff.ID] {
			return fmt.Errorf("duplicate handoff ID %s", write.Handoff.ID)
		}
		ids[write.Handoff.ID] = true
	}
	store := filepath.Join(project, ".wtp")
	if err := validateStore(store); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(store, "handoffs.json"))
	if err != nil {
		return err
	}
	var persisted handoffCollection
	if err := json.Unmarshal(data, &persisted); err != nil {
		return fmt.Errorf("persisted handoffs: %w", err)
	}
	if len(persisted.Handoffs) != contentionHandoffCount {
		return fmt.Errorf("persisted handoffs=%d, want %d", len(persisted.Handoffs), contentionHandoffCount)
	}
	var listed struct {
		Handoffs []core.Handoff `json:"handoffs"`
	}
	if err := r.jsonAt(&listed, project, "handoff", "get", "--all-scopes", "--all"); err != nil {
		return err
	}
	if len(listed.Handoffs) != contentionHandoffCount {
		return fmt.Errorf("listed handoffs=%d, want %d", len(listed.Handoffs), contentionHandoffCount)
	}
	for index, handoff := range listed.Handoffs {
		if !ids[handoff.ID] || (index > 0 && (handoff.CreatedAt.After(listed.Handoffs[index-1].CreatedAt) || (handoff.CreatedAt.Equal(listed.Handoffs[index-1].CreatedAt) && handoff.ID < listed.Handoffs[index-1].ID))) {
			return errors.New("handoff list is missing a record or violates newest-first ordering")
		}
	}
	if err := assertLockFree(store); err != nil {
		return err
	}
	r.assert("32 separate handoff appends preserve every unique record")
	r.assert("handoff reads preserve the documented newest-first ordering")
	return nil
}

func scenarioContentionReadersAndWriters(r *scenarioRunner) error {
	project, err := r.newGitProject("contention readers and writers")
	if err != nil {
		return err
	}
	if err := runGit(project, "branch", "-M", "main"); err != nil {
		return err
	}
	r.setCWD(project)
	stable := make([]core.TaskView, 0, 8)
	for index := 0; index < 8; index++ {
		var task core.TaskView
		if err := r.jsonAt(&task, project, "task", "create", "--title", fmt.Sprintf("stable task %02d", index+1)); err != nil {
			return err
		}
		stable = append(stable, task)
	}
	if err := r.readerWriterRound(r.scheduleSeed, project, stable, true, ""); err != nil {
		return err
	}
	if err := r.readerWriterRound(r.scheduleSeed+1, project, stable, false, "start"); err != nil {
		return err
	}
	if err := r.readerWriterRound(r.scheduleSeed+2, project, stable, false, "done"); err != nil {
		return err
	}
	store := filepath.Join(project, ".wtp")
	if err := validateStore(store); err != nil {
		return err
	}
	tasks, err := storedTasks(store)
	if err != nil {
		return err
	}
	if len(tasks) != 24 {
		return fmt.Errorf("reader/writer final task count=%d, want 24", len(tasks))
	}
	stableIDs := map[string]bool{}
	for _, task := range stable {
		stableIDs[task.ID] = true
	}
	for _, task := range tasks {
		if stableIDs[task.ID] && task.Status != core.StatusDone {
			return fmt.Errorf("stable task %s did not reach done", task.ShortID)
		}
	}
	if err := assertLockFree(store); err != nil {
		return err
	}
	r.assert("concurrent list/show/graph/export readers decode complete atomic snapshots")
	r.assert("concurrent creates and lifecycle transitions leave one valid logical task per UUID")
	return nil
}

func (r *scenarioRunner) readerWriterRound(seed int64, project string, stable []core.TaskView, create bool, transition string) error {
	specs := make([]batchSpec, 0, 32)
	if create {
		for index := 0; index < 16; index++ {
			specs = append(specs, batchSpec{args: []string{"--json", "task", "create", "--title", fmt.Sprintf("writer task %02d", index+1)}, cwd: project, kind: "create"})
		}
	} else {
		for _, task := range stable {
			target := transition
			specs = append(specs, batchSpec{args: []string{"--json", "task", target, task.ShortID, "--agent", "lifecycle-writer"}, cwd: project, kind: target})
		}
	}
	for index := 0; index < 4; index++ {
		specs = append(specs,
			batchSpec{args: []string{"--json", "task", "list"}, cwd: project, kind: "list"},
			batchSpec{args: []string{"--json", "task", "show", stable[0].ShortID}, cwd: project, kind: "show"},
			batchSpec{args: []string{"--json", "graph", "--status", "all"}, cwd: project, kind: "graph"},
			batchSpec{args: []string{"--json", "export", "--out", filepath.Join(r.root, fmt.Sprintf("reader export %d %d", seed, index))}, cwd: project, kind: "export"},
		)
	}
	results, err := r.batch(shuffledSpecs(specs, seed))
	if err != nil {
		return err
	}
	if err := requireBatchSuccess(results, "reader/writer round"); err != nil {
		return err
	}
	// Reconstruct the same schedule used by batch so each output is paired
	// with its command and every reader is validated independently.
	ordered := shuffledSpecs(specs, seed)
	for index, spec := range ordered {
		result := results[index]
		switch spec.kind {
		case "create":
			var task core.TaskView
			if err := decodeJSONOutput(result, &task); err != nil {
				return err
			}
			if err := task.Validate(); err != nil {
				return err
			}
		case "start", "done":
			var task core.TaskView
			if err := decodeJSONOutput(result, &task); err != nil {
				return err
			}
			if err := task.Validate(); err != nil {
				return err
			}
		case "list":
			var tasks []core.TaskView
			if err := decodeJSONOutput(result, &tasks); err != nil {
				return err
			}
			if err := validateTaskViews(tasks); err != nil {
				return err
			}
		case "show":
			var task core.TaskView
			if err := decodeJSONOutput(result, &task); err != nil {
				return err
			}
			if err := task.Validate(); err != nil {
				return err
			}
		case "graph":
			var graph any
			if err := decodeJSONOutput(result, &graph); err != nil {
				return err
			}
			if err := validateGraphJSON(graph); err != nil {
				return err
			}
		case "export":
			var output map[string]string
			if err := decodeJSONOutput(result, &output); err != nil {
				return err
			}
			if err := validateExportSnapshot(spec.args[len(spec.args)-1]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *scenarioRunner) jsonAt(out any, cwd string, args ...string) error {
	result, err := r.batch([]batchSpec{{args: append([]string{"--json"}, args...), cwd: cwd, kind: "setup"}})
	if err != nil {
		return err
	}
	if len(result) != 1 || result[0].exit != 0 {
		return fmt.Errorf("setup command failed: %s", strings.TrimSpace(result[0].stderr))
	}
	if err := decodeJSONOutput(result[0], out); err != nil {
		return err
	}
	r.assert("valid JSON: " + strings.Join(args, " "))
	return nil
}

func storedTasks(path string) ([]core.Task, error) {
	statuses := []core.Status{core.StatusTodo, core.StatusInProgress, core.StatusPaused, core.StatusDone}
	result := make([]core.Task, 0)
	seen := map[string]bool{}
	for _, status := range statuses {
		entries, err := os.ReadDir(filepath.Join(path, string(status)))
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(path, string(status), entry.Name()))
			if err != nil {
				return nil, err
			}
			var task core.Task
			if err := json.Unmarshal(data, &task); err != nil {
				return nil, err
			}
			if err := task.Validate(); err != nil {
				return nil, err
			}
			if task.Status != status {
				return nil, fmt.Errorf("task %s has status %s in %s", task.ShortID, task.Status, status)
			}
			if seen[task.ID] {
				return nil, fmt.Errorf("duplicate logical task %s", task.ID)
			}
			seen[task.ID] = true
			result = append(result, task)
		}
	}
	return result, nil
}

func assertContiguousTasks(tasks []core.Task, count int, branch string) error {
	if len(tasks) != count {
		return fmt.Errorf("tasks=%d, want %d", len(tasks), count)
	}
	sequences := make([]int, 0, len(tasks))
	branchID := ""
	if branch != "" {
		branchID = core.BranchID(branch)
	}
	for _, task := range tasks {
		parts, err := core.ParseShortID(task.ShortID)
		if err != nil {
			return err
		}
		if (branch == "") != parts.Legacy || parts.BranchID != branchID {
			return fmt.Errorf("task %s is in the wrong scope", task.ShortID)
		}
		sequence, err := strconv.Atoi(parts.Sequence)
		if err != nil {
			return err
		}
		sequences = append(sequences, sequence)
	}
	sort.Ints(sequences)
	for index, sequence := range sequences {
		if sequence != index+1 {
			return fmt.Errorf("allocation sequences=%v, want 1..%d", sequences, count)
		}
	}
	return nil
}

func assertIndexNext(store, branch string, want int) error {
	name := "index.json"
	if branch != "" {
		name = "index-" + core.BranchID(branch) + ".json"
	}
	data, err := os.ReadFile(filepath.Join(store, "meta", name))
	if err != nil {
		return err
	}
	var index allocationIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return err
	}
	if index.Next != want {
		return fmt.Errorf("next=%d, want %d", index.Next, want)
	}
	if branch != "" && index.Branch != branch {
		return fmt.Errorf("index branch=%q, want %q", index.Branch, branch)
	}
	return nil
}

func assertLockFree(store string) error {
	if _, err := os.Stat(filepath.Join(store, "meta", "wtp.lock")); err == nil {
		return errors.New("lock file remains after process batch")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func validateTaskViews(tasks []core.TaskView) error {
	seen := map[string]bool{}
	for _, task := range tasks {
		if err := task.Validate(); err != nil {
			return err
		}
		if seen[task.ID] {
			return fmt.Errorf("duplicate logical task %s in read result", task.ID)
		}
		seen[task.ID] = true
	}
	return nil
}

func validateGraphJSON(value any) error {
	if _, ok := value.([]any); !ok {
		return errors.New("graph JSON is not a complete array")
	}
	seen := map[string]bool{}
	var visit func(any) error
	visit = func(value any) error {
		switch typed := value.(type) {
		case []any:
			for _, child := range typed {
				if err := visit(child); err != nil {
					return err
				}
			}
		case map[string]any:
			if id, ok := typed["id"].(string); ok && id != "" {
				if seen[id] {
					return fmt.Errorf("duplicate logical task %s in graph", id)
				}
				seen[id] = true
			}
			for _, child := range typed {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(value)
}

func validateExportSnapshot(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return errors.New("export snapshot is empty")
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() {
			return fmt.Errorf("export snapshot contains directory %s", entry.Name())
		}
		data, err := os.ReadFile(filepath.Join(path, entry.Name()))
		if err != nil {
			return err
		}
		if entry.Name() == "handoffs.json" {
			var handoffs handoffCollection
			if err := json.Unmarshal(data, &handoffs); err != nil {
				return err
			}
			continue
		}
		var task core.Task
		if err := json.Unmarshal(data, &task); err != nil {
			return err
		}
		if err := task.Validate(); err != nil {
			return err
		}
		if seen[task.ID] {
			return fmt.Errorf("duplicate logical task %s in export", task.ID)
		}
		seen[task.ID] = true
	}
	return nil
}

func runRaceCoverage(sourceRoot string, timeout time.Duration, root string) RaceReport {
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.Command("go", "test", "-race", "./...")
	prepareCommand(command)
	command.Dir = sourceRoot
	raceRoot := filepath.Join(root, "race")
	_ = os.MkdirAll(raceRoot, 0o755)
	command.Env = disposableEnv(raceRoot)
	var stdout, stderr strings.Builder
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
	output := []byte(stdout.String() + stderr.String())
	report := RaceReport{Status: "passed", ExitCode: 0, DurationMS: time.Since(started).Milliseconds(), Output: normalize(string(output), root)}
	if err == nil {
		return report
	}
	report.ExitCode = 1
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		report.ExitCode = exitError.ExitCode()
	}
	if ctx.Err() != nil {
		report.Status = "failed"
		report.Reason = "timeout running go test -race ./..."
		return report
	}
	report.Reason = strings.TrimSpace(string(output))
	if err != nil {
		if report.Reason != "" {
			report.Reason += ": "
		}
		report.Reason += err.Error()
	}
	lower := strings.ToLower(report.Reason)
	if strings.Contains(lower, "race is not supported") || strings.Contains(lower, "-race is not supported") || strings.Contains(lower, "gcc: not found") || strings.Contains(lower, "c compiler cannot create executables") || strings.Contains(lower, "requires cgo") || strings.Contains(lower, "go: command not found") || strings.Contains(lower, "go is not recognized") || strings.Contains(lower, "executable file not found") {
		report.Status = "not_applicable"
		return report
	}
	report.Status = "failed"
	return report
}
