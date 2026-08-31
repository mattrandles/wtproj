package core_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
)

func TestPlanningFixedLifecycleContract(t *testing.T) {
	want := []core.PlanningStatus{"toplan", "researched", "planned", "rejected"}
	if got := core.PlanningStatuses(); !reflect.DeepEqual(got, want) {
		t.Fatalf("planning status order = %v, want %v", got, want)
	}
	statuses := core.PlanningStatuses()
	statuses[0] = "mutated"
	if !reflect.DeepEqual(core.PlanningStatuses(), want) {
		t.Fatal("planning status catalog leaked mutable state")
	}
	wantTransitions := []core.PlanningTransition{
		{From: "toplan", To: "researched"}, {From: "toplan", To: "rejected"},
		{From: "researched", To: "toplan"}, {From: "researched", To: "planned"}, {From: "researched", To: "rejected"},
		{From: "planned", To: "researched"}, {From: "planned", To: "rejected"},
		{From: "rejected", To: "toplan"},
	}
	if got := core.PlanningTransitions(); !reflect.DeepEqual(got, wantTransitions) {
		t.Fatalf("planning transition contract changed: %v", got)
	}
	transitions := core.PlanningTransitions()
	transitions[0].To = "mutated"
	if !reflect.DeepEqual(core.PlanningTransitions(), wantTransitions) {
		t.Fatal("planning transitions leaked mutable state")
	}

	var additional []core.StatusDefinition
	for _, status := range want {
		if _, err := core.ParseStatus(string(status)); err == nil {
			t.Errorf("planning status %q leaked into default execution catalog", status)
		}
		additional = append(additional, core.StatusDefinition{Name: core.Status(status), Category: core.StatusCategoryWaiting})
	}
	catalog, err := core.NewStatusCatalog(additional)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range want {
		if _, err := catalog.ParseStatus(string(status)); err != nil {
			t.Errorf("same-leaf execution status must remain configurable: %v", err)
		}
		if err := catalog.NormalizeLifecycle(core.Status(status), nil, nil); err == nil {
			t.Errorf("configured execution %s lost its waiting timestamp rules", status)
		}
	}
	if reflect.TypeOf(core.PlanningStatusToplan).AssignableTo(reflect.TypeOf(core.StatusTodo)) {
		t.Fatal("planning and execution status types must be independent")
	}
}

func TestPlanningStatusParserIsFixedAndExact(t *testing.T) {
	catalog := core.DefaultPlanningStatusCatalog()
	tests := []struct {
		name      string
		value     string
		want      core.PlanningStatus
		wantError string
	}{
		{name: "toplan", value: "toplan", want: core.PlanningStatusToplan},
		{name: "researched", value: "researched", want: core.PlanningStatusResearched},
		{name: "planned", value: "planned", want: core.PlanningStatusPlanned},
		{name: "rejected", value: "rejected", want: core.PlanningStatusRejected},
		{name: "empty", value: "", wantError: `invalid planning status ""`},
		{name: "padded", value: " planned ", wantError: "invalid planning status"},
		{name: "different case", value: "Planned", wantError: "invalid planning status"},
		{name: "execution status", value: "todo", wantError: "invalid planning status"},
		{name: "unknown", value: "approved", wantError: "invalid planning status"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := catalog.ParseStatus(test.value)
			if test.wantError == "" {
				if err != nil || got != test.want {
					t.Fatalf("ParseStatus(%q) = %q, %v; want %q", test.value, got, err, test.want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("ParseStatus(%q) error = %v; want %q", test.value, err, test.wantError)
			}
		})
	}

	// The package parser is the same fixed parser and does not accept an
	// execution catalog's similarly named statuses as planning statuses.
	for _, value := range []string{"toplan", "researched", "planned", "rejected"} {
		if _, err := core.ParsePlanningStatus(value); err != nil {
			t.Fatalf("ParsePlanningStatus(%q) error = %v", value, err)
		}
	}
	if _, err := core.ParsePlanningStatus("waiting"); err == nil {
		t.Fatal("ParsePlanningStatus accepted an unknown status")
	}
}

func TestPlanningLifecycleRequiresNilExecutionTimestamps(t *testing.T) {
	catalog := core.DefaultPlanningStatusCatalog()
	now := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name                   string
		status                 core.PlanningStatus
		startedAt, completedAt *time.Time
		wantError              string
	}{
		{name: "toplan without timestamps", status: core.PlanningStatusToplan},
		{name: "researched without timestamps", status: core.PlanningStatusResearched},
		{name: "planned without timestamps", status: core.PlanningStatusPlanned},
		{name: "rejected without timestamps", status: core.PlanningStatusRejected},
		{name: "started timestamp", status: core.PlanningStatusPlanned, startedAt: &now, wantError: "startedAt"},
		{name: "completed timestamp", status: core.PlanningStatusRejected, completedAt: &now, wantError: "completedAt"},
		{name: "both timestamps", status: core.PlanningStatusResearched, startedAt: &now, completedAt: &now, wantError: "startedAt"},
		{name: "unknown status", status: "approved", wantError: "invalid planning status"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := catalog.NormalizeLifecycle(test.status, test.startedAt, test.completedAt)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("NormalizeLifecycle(%q) error = %v", test.status, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("NormalizeLifecycle(%q) error = %v; want %q", test.status, err, test.wantError)
			}
		})
	}

	item := core.PlanningItem{Status: core.PlanningStatusPlanned}
	if err := item.ValidatePlanningLifecycle(); err != nil {
		t.Fatalf("valid planning item lifecycle error = %v", err)
	}
	item.StartedAt = &now
	if err := item.ValidatePlanningLifecycle(); err == nil {
		t.Fatal("planning item accepted startedAt")
	}
}

func TestPlanningTransitionsMatchTheFixedTable(t *testing.T) {
	allowed := map[core.PlanningTransition]bool{}
	for _, transition := range core.PlanningTransitions() {
		allowed[transition] = true
	}
	catalog := core.DefaultPlanningStatusCatalog()
	statuses := core.PlanningStatuses()
	for _, from := range statuses {
		for _, to := range statuses {
			want := allowed[core.PlanningTransition{From: from, To: to}]
			if got := catalog.AllowedTransition(from, to); got != want {
				t.Errorf("AllowedTransition(%s, %s) = %v, want %v", from, to, got, want)
			}
			if got := core.AllowedPlanningTransition(from, to); got != want {
				t.Errorf("AllowedPlanningTransition(%s, %s) = %v, want %v", from, to, got, want)
			}
		}
	}
	for _, test := range []struct {
		from, to core.PlanningStatus
	}{
		{from: "approved", to: core.PlanningStatusToplan},
		{from: core.PlanningStatusToplan, to: "approved"},
		{from: "approved", to: "approved"},
	} {
		if catalog.AllowedTransition(test.from, test.to) {
			t.Errorf("AllowedTransition(%s, %s) accepted an unknown status", test.from, test.to)
		}
	}
}

// These comparisons deliberately include every field and tag, rather than a
// hand-picked metadata subset, to catch future task additions silently lost
// during planning serialization or promotion.
func TestPlanningCompletePayloadContract(t *testing.T) {
	assertPlanningFieldParity(t, reflect.TypeOf(core.Task{}), reflect.TypeOf(core.PlanningItem{}), nil,
		map[string]reflect.Type{"Status": reflect.TypeOf(core.PlanningStatusToplan)})
	assertPlanningFieldParity(t, reflect.TypeOf(core.CreateTaskInput{}), reflect.TypeOf(core.CreatePlanningItemInput{}), nil,
		map[string]reflect.Type{"Status": reflect.TypeOf(core.PlanningStatusToplan)})
	assertPlanningFieldParity(t, reflect.TypeOf(core.UpdateTaskInput{}), reflect.TypeOf(core.UpdatePlanningItemInput{}),
		map[string]bool{"Status": true}, map[string]reflect.Type{"Dependencies": reflect.TypeOf(core.OptionalStrings{})})
	if reflect.TypeOf(core.PlanningItem{}).AssignableTo(reflect.TypeOf(core.Task{})) {
		t.Fatal("planning record must not implicitly become an execution task")
	}
	view := reflect.TypeOf(core.PlanningItemView{})
	if view.NumField() != 2 || !view.Field(0).Anonymous || view.Field(0).Type != reflect.TypeOf(core.PlanningItem{}) ||
		view.Field(1).Name != "ReusableTasks" || view.Field(1).Type != reflect.TypeOf([]core.ReusableTaskDefinition{}) ||
		view.Field(1).Tag.Get("json") != "reusableTasks,omitempty" {
		t.Fatal("planning view must expose only the complete record and live reusable definitions, without execution decorations")
	}
}

func assertPlanningFieldParity(t *testing.T, executable, planning reflect.Type, excluded map[string]bool, replacements map[string]reflect.Type) {
	t.Helper()
	if planning.NumField() != executable.NumField()-len(excluded) {
		t.Fatalf("%s has %d fields; %s parity requires %d", planning, planning.NumField(), executable, executable.NumField()-len(excluded))
	}
	next := 0
	for i := 0; i < executable.NumField(); i++ {
		field := executable.Field(i)
		if excluded[field.Name] {
			if _, exists := planning.FieldByName(field.Name); exists {
				t.Errorf("%s must not expose %s", planning, field.Name)
			}
			continue
		}
		got := planning.Field(next)
		next++
		wantType := field.Type
		if replacement, ok := replacements[field.Name]; ok {
			wantType = replacement
		}
		if got.Name != field.Name || got.Type != wantType || got.Tag != field.Tag || got.Anonymous {
			t.Errorf("%s field %s = %v; want name %s, type %s, tag %q, no embedding", planning, field.Name, got, field.Name, wantType, field.Tag)
		}
	}
}

func TestPlanningRecordAndViewJSONContract(t *testing.T) {
	// This fixture freezes the full payload, including legacy comments that
	// planning cannot append, grouping, origins, and ordered advisory UUIDs.
	const fixture = `{"id":"11111111-1111-4111-8111-111111111111","shortId":"wtp-0d6e4079-0091","title":"Plan 世界","description":"first\nsecond","priority":"high","estimate":"l","lane":"architecture","model":"chosen model","issueId":"ISSUE-1","project":"Apollo","milestone":"Preview","version":"v2","featureId":"F-1","feature":"Search","gitRepo":"/repo","gitBranch":"main","worktreeName":"repo","worktreeDir":"/repo","status":"planned","assignee":"worker","dependencies":["22222222-2222-4222-8222-222222222222"],"comments":[{"id":"33333333-3333-4333-8333-333333333333","author":"author","message":"Retained note","createdAt":"2026-08-31T09:00:00Z"}],"createdAt":"2026-08-31T08:00:00Z","updatedAt":"2026-08-31T10:00:00Z","startedAt":null,"completedAt":null,"reusableTaskIds":["55555555-5555-4555-8555-555555555555","44444444-4444-4444-8444-444444444444"]}`
	var item core.PlanningItem
	if err := json.Unmarshal([]byte(fixture), &item); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(item)
	if err != nil || string(data) != fixture {
		t.Fatalf("planning record round trip = %s, error %v", data, err)
	}
	if item.StartedAt != nil || item.CompletedAt != nil {
		t.Fatal("planning fixture must have nil execution timestamps")
	}
	// The only on-disk type difference is status; even field order stays equal.
	var task core.Task
	if err := json.Unmarshal(data, &task); err != nil {
		t.Fatal(err)
	}
	taskData, err := json.Marshal(task)
	if err != nil || string(taskData) != fixture {
		t.Fatalf("task JSON compatibility lost: %s, %v", taskData, err)
	}
	view := core.PlanningItemView{PlanningItem: item, ReusableTasks: []core.ReusableTaskDefinition{
		{ID: item.ReusableTaskIDs[0], Name: "Review", Title: "Live title", Instructions: "Review changes"},
		{ID: item.ReusableTaskIDs[1], Name: "Tests", Title: "Tests", Instructions: "Run tests"},
	}}
	data, err = json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["reusableTasks"]; !ok {
		t.Fatal("planning view must expose resolved reusable tasks")
	}
	for _, excluded := range []string{"readiness", "claimable", "blocked", "handoffs", "task", "planningItem"} {
		if _, ok := fields[excluded]; ok {
			t.Errorf("planning view must not include %q", excluded)
		}
	}
	var decoded core.PlanningItemView
	if err := json.Unmarshal(data, &decoded); err != nil || !reflect.DeepEqual(decoded, view) {
		t.Fatalf("planning view lost payload/order: %#v, %v", decoded, err)
	}

	minimal := core.PlanningItem{Status: core.PlanningStatusToplan, Dependencies: []string{}, Comments: []core.Comment{}}
	data, err = json.Marshal(minimal)
	if err != nil {
		t.Fatal(err)
	}
	fields = nil
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"priority", "estimate", "lane", "model", "issueId", "project", "milestone", "version", "featureId", "feature", "gitRepo", "gitBranch", "worktreeName", "worktreeDir", "assignee", "reusableTaskIds"} {
		if _, exists := fields[name]; exists {
			t.Errorf("empty optional %s must be omitted", name)
		}
	}
	for key, expected := range map[string]string{"dependencies": "[]", "comments": "[]", "startedAt": "null", "completedAt": "null"} {
		if string(fields[key]) != expected {
			t.Errorf("minimal record %s = %s; want %s", key, fields[key], expected)
		}
	}
}
