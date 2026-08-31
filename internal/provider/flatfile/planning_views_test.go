package flatfile

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
)

func TestPlanningViewsListAllStatusesFiltersAndSortsAcrossBranchScopes(t *testing.T) {
	root := t.TempDir()
	current, err := New(root, core.NewBranchScope("feature/current"))
	if err != nil {
		t.Fatalf("New(current) error = %v", err)
	}
	foreign, err := New(root, core.NewBranchScope("feature/foreign"))
	if err != nil {
		t.Fatalf("New(foreign) error = %v", err)
	}

	items := []struct {
		provider *Provider
		title    string
		status   core.PlanningStatus
		project  string
		feature  string
	}{
		{current, "planned", core.PlanningStatusPlanned, "Apollo", "Search"},
		{foreign, "toplan", core.PlanningStatusToplan, "apollo", "Search"},
		{current, "researched", core.PlanningStatusResearched, "Apollo", "Other"},
		{foreign, "rejected", core.PlanningStatusRejected, "Apollo", "Search"},
	}
	created := make([]core.PlanningItemView, 0, len(items))
	for _, input := range items {
		view, err := input.provider.CreatePlanningItem(core.CreatePlanningItemInput{
			Title: input.title, Status: input.status, Project: input.project,
			IssueID: "ISSUE-1", Milestone: "MVP", Version: "v1", FeatureID: "FEAT-1", Feature: input.feature,
		})
		if err != nil {
			t.Fatalf("CreatePlanningItem(%s) error = %v", input.title, err)
		}
		created = append(created, view)
	}

	got, err := current.ListPlanningItems(provider.PlanningFilter{})
	if err != nil {
		t.Fatalf("ListPlanningItems() error = %v", err)
	}
	if len(got) != len(created) {
		t.Fatalf("planning list length = %d, want %d", len(got), len(created))
	}
	for index := range got {
		if got[index].ShortID != created[index].ShortID {
			t.Fatalf("planning list order[%d] = %s, want %s", index, got[index].ShortID, created[index].ShortID)
		}
		if got[index].Status != created[index].Status {
			t.Fatalf("planning list status[%d] = %s, want stored %s", index, got[index].Status, created[index].Status)
		}
	}

	for _, status := range core.PlanningStatuses() {
		filter := provider.PlanningFilter{Status: &status}
		statusItems, err := current.ListPlanningItems(filter)
		if err != nil {
			t.Fatalf("ListPlanningItems(%s) error = %v", status, err)
		}
		if len(statusItems) != 1 || statusItems[0].Status != status {
			t.Fatalf("status filter %s returned %#v", status, statusItems)
		}
	}

	filtered, err := current.ListPlanningItems(provider.PlanningFilter{Grouping: core.GroupingFilter{
		IssueID: " issue-1 ", Project: "APOLLO", Milestone: "mvp", Version: "V1", FeatureID: "feat-1", Feature: "sEaRcH",
	}})
	if err != nil {
		t.Fatalf("combined grouping filter error = %v", err)
	}
	if gotTitles := planningTitles(filtered); !slices.Equal(gotTitles, []string{"planned", "toplan", "rejected"}) {
		t.Fatalf("combined grouping filter titles = %v", gotTitles)
	}

	unset, err := current.ListPlanningItems(provider.PlanningFilter{Grouping: core.GroupingFilter{Project: "apollo", Feature: "search"}})
	if err != nil {
		t.Fatalf("unset grouping filter error = %v", err)
	}
	if len(unset) != 3 {
		t.Fatalf("case-insensitive grouping match count = %d, want 3", len(unset))
	}
	noUnset, err := current.ListPlanningItems(provider.PlanningFilter{Grouping: core.GroupingFilter{Milestone: "missing"}})
	if err != nil {
		t.Fatalf("missing grouping filter error = %v", err)
	}
	if noUnset == nil || len(noUnset) != 0 {
		t.Fatalf("non-matching grouping filter = %#v, want non-nil empty slice", noUnset)
	}
}

func TestPlanningViewsShowIsPlanningScopedAndResolvesLiveReusableDefinitions(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	task, err := p.CreateTask(core.CreateTaskInput{Title: "execution task"})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	first, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "First", Title: "First title", Instructions: "first instructions"})
	if err != nil {
		t.Fatalf("CreateReusableTask(first) error = %v", err)
	}
	second, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "Second", Title: "Second title", Instructions: "second instructions"})
	if err != nil {
		t.Fatalf("CreateReusableTask(second) error = %v", err)
	}
	created, err := p.CreatePlanningItem(core.CreatePlanningItemInput{
		Title: "planning", Description: "full description", Status: core.PlanningStatusPlanned,
		Project: "Apollo", Dependencies: []string{task.ShortID}, ReusableTasks: []string{second.ID, first.ID},
	})
	if err != nil {
		t.Fatalf("CreatePlanningItem() error = %v", err)
	}

	got, err := p.GetPlanningItem("  " + created.ShortID + "  ")
	if err != nil {
		t.Fatalf("GetPlanningItem(short ID) error = %v", err)
	}
	if got.ID != created.ID || !slices.Equal(got.Dependencies, []string{task.ID}) {
		t.Fatalf("planning show identity/dependency = %#v", got.PlanningItem)
	}
	if !slices.EqualFunc(got.ReusableTasks, []core.ReusableTaskDefinition{second, first}, func(a, b core.ReusableTaskDefinition) bool { return a == b }) {
		t.Fatalf("planning show reusable order = %#v", got.ReusableTasks)
	}

	updated, err := p.UpdateReusableTask(first.ID, core.UpdateReusableTaskInput{
		Name:         core.OptionalString{Set: true, Value: "Renamed"},
		Title:        core.OptionalString{Set: true, Value: "Live title"},
		Instructions: core.OptionalString{Set: true, Value: "Live instructions"},
	})
	if err != nil {
		t.Fatalf("UpdateReusableTask() error = %v", err)
	}
	live, err := p.GetPlanningItem(created.ID)
	if err != nil {
		t.Fatalf("GetPlanningItem(UUID) error = %v", err)
	}
	if live.ReusableTasks[1] != updated {
		t.Fatalf("planning show did not resolve live reusable definition = %#v, want %#v", live.ReusableTasks[1], updated)
	}

	if _, err := p.GetPlanningItem(task.ID); err == nil || !strings.Contains(err.Error(), "planning item") || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("execution UUID unexpectedly resolved as planning item: %v", err)
	}
	if _, err := p.GetPlanningItem(task.ShortID); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("execution short ID unexpectedly resolved as planning item: %v", err)
	}
	if _, err := p.GetPlanningItem(""); err == nil || !strings.Contains(err.Error(), "identifier is required") {
		t.Fatalf("empty planning identifier error = %v", err)
	}

	data, err := json.Marshal(live)
	if err != nil {
		t.Fatalf("Marshal(planning view) error = %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal(planning view) error = %v", err)
	}
	for _, forbidden := range []string{"readiness", "claimable", "blocked", "handoffs"} {
		if _, exists := payload[forbidden]; exists {
			t.Errorf("planning view leaked execution field %q: %s", forbidden, data)
		}
	}
	if _, exists := payload["dependencies"]; !exists {
		t.Fatalf("planning view omitted dependencies: %s", data)
	}
}

func TestPlanningViewsEmptyStoreAndMissingReusableAssignmentErrors(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, err := p.ListPlanningItems(provider.PlanningFilter{})
	if err != nil {
		t.Fatalf("ListPlanningItems(empty) error = %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("empty planning list = %#v, want non-nil empty slice", got)
	}

	item := planningStorageTask(t, "15c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.PlanningStatusToplan)
	item.ReusableTaskIDs = []string{"25c3806a-bd1b-424d-889b-29e5b06679b8"}
	writePlanningStorageItem(t, p.planningStatusDir(item.Status)+"/"+item.ShortID+".json", item)
	if _, err := p.GetPlanningItem(item.ID); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing reusable definition error = %v", err)
	}
}

func TestPlanningViewsResolveZeroOneManyAssignmentsInOrderAndReflectUnicodeEdits(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	first, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "First", Title: "First title", Instructions: "第一行 ✓\n第二行"})
	if err != nil {
		t.Fatalf("CreateReusableTask(first) error = %v", err)
	}
	second, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "Second", Title: "Second title", Instructions: "二つ目の指示"})
	if err != nil {
		t.Fatalf("CreateReusableTask(second) error = %v", err)
	}
	third, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "Third", Title: "Third title", Instructions: "第三条"})
	if err != nil {
		t.Fatalf("CreateReusableTask(third) error = %v", err)
	}

	zero, err := p.CreatePlanningItem(core.CreatePlanningItemInput{Title: "zero"})
	if err != nil {
		t.Fatalf("CreatePlanningItem(zero) error = %v", err)
	}
	one, err := p.CreatePlanningItem(core.CreatePlanningItemInput{Title: "one", ReusableTasks: []string{first.ID}})
	if err != nil {
		t.Fatalf("CreatePlanningItem(one) error = %v", err)
	}
	many, err := p.CreatePlanningItem(core.CreatePlanningItemInput{Title: "many", ReusableTasks: []string{third.ID, first.ID, second.ID}})
	if err != nil {
		t.Fatalf("CreatePlanningItem(many) error = %v", err)
	}
	if len(zero.ReusableTasks) != 0 || !slices.Equal(one.ReusableTaskIDs, []string{first.ID}) || !slices.Equal(many.ReusableTaskIDs, []string{third.ID, first.ID, second.ID}) {
		t.Fatalf("created planning assignments = zero=%#v one=%#v many=%#v", zero.ReusableTaskIDs, one.ReusableTaskIDs, many.ReusableTaskIDs)
	}
	if !slices.EqualFunc(many.ReusableTasks, []core.ReusableTaskDefinition{third, first, second}, func(a, b core.ReusableTaskDefinition) bool { return a == b }) {
		t.Fatalf("created planning reusable order = %#v", many.ReusableTasks)
	}

	updatedSecond, err := p.UpdateReusableTask(second.ID, core.UpdateReusableTaskInput{
		Name:         core.OptionalString{Set: true, Value: "二番目"},
		Title:        core.OptionalString{Set: true, Value: "更新された題名"},
		Instructions: core.OptionalString{Set: true, Value: "新しい指示 🌍\n保持される順序"},
	})
	if err != nil {
		t.Fatalf("UpdateReusableTask(second) error = %v", err)
	}
	live, err := p.GetPlanningItem(many.ID)
	if err != nil {
		t.Fatalf("GetPlanningItem(live) error = %v", err)
	}
	if !slices.Equal(live.ReusableTaskIDs, []string{third.ID, first.ID, second.ID}) || !slices.EqualFunc(live.ReusableTasks, []core.ReusableTaskDefinition{third, first, updatedSecond}, func(a, b core.ReusableTaskDefinition) bool { return a == b }) {
		t.Fatalf("live planning assignments = %#v / %#v", live.ReusableTaskIDs, live.ReusableTasks)
	}
}

func TestResolvePlanningItemReportsAmbiguousSelectorsDeterministically(t *testing.T) {
	items := []core.PlanningItem{
		{ID: "11111111-1111-4111-8111-111111111111", ShortID: "wtp-0001"},
		{ID: "22222222-2222-4222-8222-222222222222", ShortID: "wtp-0002"},
	}
	// The valid storage loader rejects this impossible identity collision, but
	// the resolver still keeps its ambiguity contract independently testable.
	items[1].ID = items[0].ID
	got, err := resolvePlanningItem(items[0].ID, items)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous planning selector error = %v", err)
	}
	if !reflect.DeepEqual(got, core.PlanningItem{}) {
		t.Fatalf("ambiguous selector returned %#v", got)
	}
}

func planningTitles(items []core.PlanningItemView) []string {
	titles := make([]string, 0, len(items))
	for _, item := range items {
		titles = append(titles, item.Title)
	}
	return titles
}
