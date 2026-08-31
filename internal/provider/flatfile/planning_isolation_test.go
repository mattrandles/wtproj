package flatfile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/batchexport"
	"github.com/mattrandles/wtproj/internal/batchimport"
	"github.com/mattrandles/wtproj/internal/batchjson"
	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
	"github.com/mattrandles/wtproj/internal/stats"
)

func TestExecutionQueriesIgnorePlanningOnlyStores(t *testing.T) {
	root := t.TempDir()
	p, err := New(root, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	planning := planningStorageTask(t, "15c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.PlanningStatusPlanned)
	writePlanningStorageItem(t, filepath.Join(root, planningDirectory, string(planning.Status), planning.ShortID+".json"), planning)
	writePlanningIsolationHandoffs(t, root, []core.Handoff{
		planningIsolationHandoff(t, "00000000-0000-4000-8000-000000000001", "", "global"),
		planningIsolationHandoff(t, "00000000-0000-4000-8000-000000000002", planning.ID, "planning"),
	})

	listed, err := p.ListTasks(provider.TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if listed == nil || len(listed) != 0 {
		t.Fatalf("ListTasks() = %#v, want non-nil empty executable result", listed)
	}
	for _, identifier := range []string{planning.ID, planning.ShortID} {
		if _, err := p.GetTask(identifier, ""); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("GetTask(%q) error = %v, want execution lookup miss", identifier, err)
		}
	}
	if _, err := p.PeekNextTask(""); !errors.Is(err, provider.ErrNoEligibleTask) {
		t.Fatalf("PeekNextTask() error = %v, want no eligible task", err)
	}
	if _, err := p.GetNextTask(""); !errors.Is(err, provider.ErrNoEligibleTask) {
		t.Fatalf("GetNextTask() error = %v, want no eligible task", err)
	}
	if _, err := p.UpdateTaskStatus(planning.ShortID, core.StatusInProgress, "worker"); err == nil {
		t.Fatal("UpdateTaskStatus() resolved planning short ID")
	}

	overview, err := stats.Aggregate(p, stats.Options{})
	if err != nil {
		t.Fatalf("stats.Aggregate() error = %v", err)
	}
	if overview.TotalTasks != 0 || overview.Handoffs != (stats.HandoffMetrics{Total: 1, AllStatusTotal: 1, Global: 1}) {
		t.Fatalf("overview = %#v, want zero tasks and global handoff only", overview)
	}
	focused, err := stats.AggregateFocused(p, stats.Options{}, stats.AttributeStatus)
	if err != nil {
		t.Fatalf("stats.AggregateFocused() error = %v", err)
	}
	if focused.TotalTasks != 0 || focused.Buckets == nil {
		t.Fatalf("focused status report = %#v, want zero executable tasks", focused)
	}
	series, err := stats.AggregateSeries(p, stats.SeriesOptions{
		Metric: stats.SeriesMetricCreated,
		Range:  stats.RollingRange{StartDays: 2, EndDays: 0},
		AsOf:   planning.CreatedAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("stats.AggregateSeries() error = %v", err)
	}
	if series.TotalTasks != 0 || len(series.Buckets) != 2 || series.Buckets[1].Count != 0 {
		t.Fatalf("series report = %#v, want zero executable tasks", series)
	}

	handoffs, err := p.ListHandoffs(provider.HandoffFilter{AllScopes: true})
	if err != nil {
		t.Fatalf("ListHandoffs(all scopes) error = %v", err)
	}
	if len(handoffs.Handoffs) != 1 || handoffs.Handoffs[0].TaskID != "" || handoffs.TotalMatching != 1 {
		t.Fatalf("all-scope handoffs = %#v, want global only", handoffs)
	}
	if _, err := p.WriteHandoff(provider.HandoffWriteRequest{Task: planning.ShortID, Message: "must not write planning scope"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("WriteHandoff(planning task) error = %v, want execution lookup miss", err)
	}
	purged, err := p.PurgeHandoffs(provider.HandoffPurgeRequest{AllScopes: true})
	if err != nil {
		t.Fatalf("PurgeHandoffs(all scopes) error = %v", err)
	}
	if purged.Purged != 1 {
		t.Fatalf("PurgeHandoffs(all scopes) purged = %d, want global only", purged.Purged)
	}
	storedHandoffs := readPlanningIsolationHandoffs(t, root)
	if len(storedHandoffs) != 1 || storedHandoffs[0].TaskID != planning.ID {
		t.Fatalf("stored handoffs after purge = %#v, want planning handoff preserved", storedHandoffs)
	}
	if _, err := p.PurgeHandoffs(provider.HandoffPurgeRequest{ID: storedHandoffs[0].ID}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("PurgeHandoffs(planning ID) error = %v, want not found", err)
	}

	canonicalDir := filepath.Join(t.TempDir(), "canonical")
	if err := p.ExportCanonical(canonicalDir); err != nil {
		t.Fatalf("ExportCanonical() error = %v", err)
	}
	canonicalHandoffs := readPlanningIsolationHandoffs(t, canonicalDir)
	if len(canonicalHandoffs) != 0 {
		t.Fatalf("canonical export handoffs = %#v, want planning scope omitted", canonicalHandoffs)
	}

	output := filepath.Join(t.TempDir(), "tasks.json")
	if _, err := batchexport.Export(p, batchexport.Options{Destination: output}); err == nil || !strings.Contains(err.Error(), "batch JSON requires at least one task") {
		t.Fatalf("batch export(all) error = %v, want empty executable batch error", err)
	}
	patch, err := batchjson.Encode([]core.BatchTaskUpdateInput{{
		ID: planning.ID, ShortID: planning.ShortID, ExpectedUpdatedAt: planning.UpdatedAt,
		Title: core.OptionalString{Set: true, Value: "must not mutate planning"},
	}})
	if err != nil {
		t.Fatalf("batchjson.Encode(planning row) error = %v", err)
	}
	if _, err := batchimport.Import(p, batchimport.Options{Source: "-", Format: batchimport.FormatJSON}, strings.NewReader(string(patch))); err == nil {
		t.Fatal("batch import accepted a planning record")
	}
	if got, err := p.GetPlanningItem(planning.ID); err != nil || got.Title != planning.Title {
		t.Fatalf("planning record after rejected batch import = %#v, error = %v", got, err)
	}
}

func TestExecutionQueriesSeparatePlanningAndExecutionStatusNames(t *testing.T) {
	catalog, err := core.NewStatusCatalog([]core.StatusDefinition{{Name: "planned", Category: core.StatusCategoryWaiting}})
	if err != nil {
		t.Fatalf("NewStatusCatalog() error = %v", err)
	}
	root := t.TempDir()
	p, err := NewWithCatalog(root, nil, catalog)
	if err != nil {
		t.Fatalf("NewWithCatalog() error = %v", err)
	}
	planning := planningStorageTask(t, "25c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.PlanningStatusPlanned)
	writePlanningStorageItem(t, filepath.Join(root, planningDirectory, string(planning.Status), planning.ShortID+".json"), planning)
	executable, err := p.CreateTask(core.CreateTaskInput{Title: "executable planned status", Status: core.Status("planned")})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if executable.ShortID == planning.ShortID {
		t.Fatalf("execution task reused planning short ID %q", executable.ShortID)
	}

	filtered, err := p.ListTasks(provider.TaskFilter{Status: statusValue("planned")})
	if err != nil {
		t.Fatalf("ListTasks(status planned) error = %v", err)
	}
	if len(filtered) != 1 || filtered[0].ID != executable.ID || filtered[0].Status != core.Status("planned") {
		t.Fatalf("status-filtered execution tasks = %#v, want executable task only", filtered)
	}
	if _, err := p.GetTask(planning.ShortID, ""); err == nil {
		t.Fatal("execution show resolved planning short ID")
	}
	overview, err := stats.Aggregate(p, stats.Options{Status: statusValue("planned")})
	if err != nil {
		t.Fatalf("stats.Aggregate(planned) error = %v", err)
	}
	if overview.TotalTasks != 1 || overview.StatusCounts[len(overview.StatusCounts)-1].Count != 1 {
		t.Fatalf("planned stats report = %#v, want one executable planned-status task", overview)
	}
	batchPath := filepath.Join(t.TempDir(), "planned.json")
	batchResult, err := batchexport.Export(p, batchexport.Options{Destination: batchPath, Status: "planned"})
	if err != nil {
		t.Fatalf("batch export(status planned) error = %v", err)
	}
	if batchResult.Count != 1 {
		t.Fatalf("batch export(status planned) count = %d, want one executable task", batchResult.Count)
	}
	batchData, err := os.ReadFile(batchPath)
	if err != nil {
		t.Fatalf("read planned batch export = %v", err)
	}
	if strings.Contains(string(batchData), planning.Title) || !strings.Contains(string(batchData), executable.ShortID) {
		t.Fatalf("status-filtered batch export = %s, want executable task only", batchData)
	}
}

func TestCrossLifecyclePlanningBlockerDoesNotExpandExecutionGraph(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	planning, err := p.CreatePlanningItem(core.CreatePlanningItemInput{Title: "planning prerequisite", Status: core.PlanningStatusPlanned})
	if err != nil {
		t.Fatalf("CreatePlanningItem() error = %v", err)
	}
	executable, err := p.CreateTask(core.CreateTaskInput{Title: "blocked executable", Dependencies: []string{planning.ShortID}})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	view, err := p.GetTask(executable.ShortID, "")
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	wantReason := "unresolved dependencies: " + planning.ShortID + " (planning prerequisite; planning planned)"
	if !view.Readiness.Blocked || view.Readiness.Claimable || view.Readiness.BlockedReason != wantReason {
		t.Fatalf("blocked execution view = %#v, want planning blocker %q and no claimability", view.Readiness, wantReason)
	}
	if view.Readiness.ReverseDependencyCount != 0 {
		t.Fatalf("planning blocker changed execution reverse graph: %#v", view.Readiness)
	}
	if _, err := p.GetNextTask("worker"); !errors.Is(err, provider.ErrNoEligibleTask) {
		t.Fatalf("GetNextTask() error = %v, want blocked execution task excluded", err)
	}
}

func TestConcurrentNextClaimsRemainExecutionOnlyWithPlanningRecords(t *testing.T) {
	root := t.TempDir()
	first, err := New(root, nil)
	if err != nil {
		t.Fatalf("New(first) error = %v", err)
	}
	second, err := New(root, nil)
	if err != nil {
		t.Fatalf("New(second) error = %v", err)
	}
	planning := planningStorageTask(t, "35c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.PlanningStatusToplan)
	writePlanningStorageItem(t, filepath.Join(root, planningDirectory, string(planning.Status), planning.ShortID+".json"), planning)
	executable, err := first.CreateTask(core.CreateTaskInput{Title: "claimable executable"})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	results := make(chan core.TaskView, 2)
	errs := make(chan error, 2)
	var group sync.WaitGroup
	for _, p := range []*Provider{first, second} {
		group.Add(1)
		go func(p *Provider) {
			defer group.Done()
			view, err := p.GetNextTask("worker")
			results <- view
			errs <- err
		}(p)
	}
	group.Wait()
	close(results)
	close(errs)

	successes := 0
	failures := 0
	for err := range errs {
		if err == nil {
			successes++
		} else if errors.Is(err, provider.ErrNoEligibleTask) {
			failures++
		} else {
			t.Fatalf("concurrent GetNextTask() error = %v", err)
		}
	}
	for view := range results {
		if view.ID != "" && view.ID != executable.ID {
			t.Fatalf("concurrent claim returned unexpected task %s", view.ShortID)
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("concurrent claims successes=%d failures=%d, want one each", successes, failures)
	}
}

func statusValue(value string) *core.Status {
	status := core.Status(value)
	return &status
}

func planningIsolationHandoff(t *testing.T, id, taskID, message string) core.Handoff {
	t.Helper()
	createdAt, err := time.Parse(time.RFC3339, "2026-08-31T09:00:00Z")
	if err != nil {
		t.Fatalf("time.Parse() error = %v", err)
	}
	return core.Handoff{ID: id, TaskID: taskID, Message: message, CreatedAt: createdAt}
}

func writePlanningIsolationHandoffs(t *testing.T, root string, handoffs []core.Handoff) {
	t.Helper()
	data, err := json.Marshal(struct {
		Handoffs []core.Handoff `json:"handoffs"`
	}{Handoffs: handoffs})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, handoffsFilename), data, 0o644); err != nil {
		t.Fatalf("WriteFile(handoffs) error = %v", err)
	}
}

func readPlanningIsolationHandoffs(t *testing.T, root string) []core.Handoff {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, handoffsFilename))
	if err != nil {
		t.Fatalf("ReadFile(handoffs) error = %v", err)
	}
	var stored struct {
		Handoffs []core.Handoff `json:"handoffs"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("json.Unmarshal(handoffs) error = %v", err)
	}
	return stored.Handoffs
}
