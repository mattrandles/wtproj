package planningjson

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
)

const planningFixtureTemplate = `{"id":"11111111-1111-4111-8111-111111111111","shortId":"wtp-0d6e4079-0091","title":"Plan 世界","description":"first\nsecond","priority":"high","estimate":"l","lane":"architecture","model":"chosen model","issueId":"ISSUE-1","project":"Apollo","milestone":"Preview","version":"v2","featureId":"F-1","feature":"Search","gitRepo":"/repo","gitBranch":"main","worktreeName":"repo","worktreeDir":"/repo","status":"planned","assignee":"worker","dependencies":["22222222-2222-4222-8222-222222222222"],"comments":[{"id":"33333333-3333-4333-8333-333333333333","author":"author","message":"Retained note\n第二行 ✓","createdAt":"2026-08-31T09:00:00Z"}],"createdAt":"2026-08-31T08:00:00Z","updatedAt":"2026-08-31T10:00:00Z","startedAt":null,"completedAt":null,"reusableTaskIds":["55555555-5555-4555-8555-555555555555","44444444-4444-4444-8444-444444444444"]}`

var planningFixture = platformPlanningFixture()
var planningFixtureRepo = absoluteTestPath("repo")

func platformPlanningFixture() string {
	path, err := filepath.Abs("repo")
	if err != nil {
		panic(err)
	}
	encoded, err := json.Marshal(filepath.Clean(path))
	if err != nil {
		panic(err)
	}
	return strings.ReplaceAll(planningFixtureTemplate, `"/repo"`, string(encoded))
}

func absoluteTestPath(name string) string {
	path, err := filepath.Abs(name)
	if err != nil {
		panic(err)
	}
	return filepath.Clean(path)
}

func TestEncodeDecodeRoundTripPreservesCompletePayload(t *testing.T) {
	want := planningFixtureItem()
	data, err := Encode(want)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if string(data) != planningFixture {
		t.Fatalf("Encode() = %s, want %s", data, planningFixture)
	}

	got, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Decode() = %#v, want %#v", got, want)
	}
	second, err := Encode(got)
	if err != nil {
		t.Fatalf("second Encode() error = %v", err)
	}
	if !bytes.Equal(second, data) {
		t.Fatalf("round trip changed bytes:\nfirst:  %s\nsecond: %s", data, second)
	}
}

func TestEncodeOmitsEmptyOptionalFieldsAndKeepsRequiredEmptyArrays(t *testing.T) {
	item := core.PlanningItem{
		ID:           "11111111-1111-4111-8111-111111111111",
		ShortID:      "wtp-0091",
		Title:        "Minimal",
		Status:       core.PlanningStatusToplan,
		Dependencies: []string{},
		Comments:     []core.Comment{},
		CreatedAt:    planningTime("2026-08-31T08:00:00Z"),
		UpdatedAt:    planningTime("2026-08-31T08:00:00Z"),
	}
	want := `{"id":"11111111-1111-4111-8111-111111111111","shortId":"wtp-0091","title":"Minimal","description":"","status":"toplan","dependencies":[],"comments":[],"createdAt":"2026-08-31T08:00:00Z","updatedAt":"2026-08-31T08:00:00Z","startedAt":null,"completedAt":null}`
	data, err := Encode(item)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if string(data) != want {
		t.Fatalf("Encode() = %s, want %s", data, want)
	}
	for _, name := range []string{"priority", "estimate", "lane", "model", "issueId", "project", "milestone", "version", "featureId", "feature", "gitRepo", "gitBranch", "worktreeName", "worktreeDir", "assignee", "reusableTaskIds"} {
		if bytes.Contains(data, []byte(`"`+name+`"`)) {
			t.Errorf("empty optional field %q was not omitted", name)
		}
	}
}

func TestDecodeRejectsUnknownMalformedAndLifecycleFields(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "unknown root field", data: planningFixture[:len(planningFixture)-1] + `,"extra":true}`, want: `unknown property "extra"`},
		{name: "duplicate root field", data: strings.Replace(planningFixture, `"status":"planned"`, `"status":"planned","status":"planned"`, 1), want: `contains duplicate property "status"`},
		{name: "unknown comment field", data: strings.Replace(planningFixture, `"author":"author"`, `"author":"author","extra":true`, 1), want: `unknown property "extra"`},
		{name: "duplicate comment field", data: strings.Replace(planningFixture, `"author":"author"`, `"author":"author","author":"author"`, 1), want: `contains duplicate property "author"`},
		{name: "unknown status", data: strings.Replace(planningFixture, `"status":"planned"`, `"status":"approved"`, 1), want: `invalid planning status`},
		{name: "execution status", data: strings.Replace(planningFixture, `"status":"planned"`, `"status":"inProgress"`, 1), want: `invalid planning status`},
		{name: "malformed status type", data: strings.Replace(planningFixture, `"status":"planned"`, `"status":true`, 1), want: `status must be a string`},
		{name: "started timestamp", data: strings.Replace(planningFixture, `"startedAt":null`, `"startedAt":"2026-08-31T09:00:00Z"`, 1), want: `startedAt must be null`},
		{name: "completed timestamp", data: strings.Replace(planningFixture, `"completedAt":null`, `"completedAt":"2026-08-31T10:00:00Z"`, 1), want: `completedAt must be null`},
		{name: "missing required comments", data: strings.Replace(planningFixture, `,"comments":[{"id":"33333333-3333-4333-8333-333333333333","author":"author","message":"Retained note\n第二行 ✓","createdAt":"2026-08-31T09:00:00Z"}]`, ``, 1), want: `missing required property "comments"`},
		{name: "trailing data", data: planningFixture + ` {}`, want: `trailing JSON data`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Decode([]byte(test.data)); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Decode() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestPlanningValidationRejectsNonCanonicalIDsAndInvalidMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*core.PlanningItem)
		want   string
	}{
		{name: "record ID", mutate: func(item *core.PlanningItem) { item.ID = "not-a-uuid" }, want: "canonical lowercase UUID"},
		{name: "dependency ID", mutate: func(item *core.PlanningItem) { item.Dependencies[0] = "not-a-uuid" }, want: "dependency 0"},
		{name: "comment ID", mutate: func(item *core.PlanningItem) { item.Comments[0].ID = "comment-1" }, want: "comment 0 id"},
		{name: "reusable assignment ID", mutate: func(item *core.PlanningItem) { item.ReusableTaskIDs[0] = "reusable-1" }, want: "reusableTaskIds 0"},
		{name: "priority", mutate: func(item *core.PlanningItem) { item.Priority = "critical" }, want: "invalid priority"},
		{name: "estimate", mutate: func(item *core.PlanningItem) { item.Estimate = "xxl" }, want: "invalid estimate"},
		{name: "blank project", mutate: func(item *core.PlanningItem) { item.Project = "   " }, want: "project cannot be blank"},
		{name: "relative origin", mutate: func(item *core.PlanningItem) { item.GitRepo = "repo" }, want: "gitRepo"},
		{name: "started timestamp", mutate: func(item *core.PlanningItem) { now := planningTime("2026-08-31T09:00:00Z"); item.StartedAt = &now }, want: "startedAt"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item := planningFixtureItem()
			test.mutate(&item)
			if _, err := Encode(item); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Encode() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestExecutableTaskJSONByteContractRemainsUnchanged(t *testing.T) {
	task := core.Task{
		ID:              "11111111-1111-4111-8111-111111111111",
		ShortID:         "wtp-0d6e4079-0091",
		Title:           "Plan 世界",
		Description:     "first\nsecond",
		Priority:        core.PriorityHigh,
		Estimate:        core.EstimateL,
		Lane:            "architecture",
		Model:           "chosen model",
		IssueID:         "ISSUE-1",
		Project:         "Apollo",
		Milestone:       "Preview",
		Version:         "v2",
		FeatureID:       "F-1",
		Feature:         "Search",
		GitRepo:         planningFixtureRepo,
		GitBranch:       "main",
		WorktreeName:    "repo",
		WorktreeDir:     planningFixtureRepo,
		Status:          core.StatusTodo,
		Assignee:        "worker",
		Dependencies:    []string{"22222222-2222-4222-8222-222222222222"},
		Comments:        []core.Comment{{ID: "33333333-3333-4333-8333-333333333333", Author: "author", Message: "Retained note\n第二行 ✓", CreatedAt: planningTime("2026-08-31T09:00:00Z")}},
		CreatedAt:       planningTime("2026-08-31T08:00:00Z"),
		UpdatedAt:       planningTime("2026-08-31T10:00:00Z"),
		StartedAt:       nil,
		CompletedAt:     nil,
		ReusableTaskIDs: []string{"55555555-5555-4555-8555-555555555555", "44444444-4444-4444-8444-444444444444"},
	}
	want := strings.Replace(planningFixture, `"status":"planned"`, `"status":"todo"`, 1)
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("json.Marshal(Task) error = %v", err)
	}
	if string(data) != want {
		t.Fatalf("executable task JSON = %s, want unchanged contract %s", data, want)
	}
}

func planningFixtureItem() core.PlanningItem {
	return core.PlanningItem{
		ID:           "11111111-1111-4111-8111-111111111111",
		ShortID:      "wtp-0d6e4079-0091",
		Title:        "Plan 世界",
		Description:  "first\nsecond",
		Priority:     core.PriorityHigh,
		Estimate:     core.EstimateL,
		Lane:         "architecture",
		Model:        "chosen model",
		IssueID:      "ISSUE-1",
		Project:      "Apollo",
		Milestone:    "Preview",
		Version:      "v2",
		FeatureID:    "F-1",
		Feature:      "Search",
		GitRepo:      planningFixtureRepo,
		GitBranch:    "main",
		WorktreeName: "repo",
		WorktreeDir:  planningFixtureRepo,
		Status:       core.PlanningStatusPlanned,
		Assignee:     "worker",
		Dependencies: []string{"22222222-2222-4222-8222-222222222222"},
		Comments: []core.Comment{{
			ID:        "33333333-3333-4333-8333-333333333333",
			Author:    "author",
			Message:   "Retained note\n第二行 ✓",
			CreatedAt: planningTime("2026-08-31T09:00:00Z"),
		}},
		CreatedAt:       planningTime("2026-08-31T08:00:00Z"),
		UpdatedAt:       planningTime("2026-08-31T10:00:00Z"),
		StartedAt:       nil,
		CompletedAt:     nil,
		ReusableTaskIDs: []string{"55555555-5555-4555-8555-555555555555", "44444444-4444-4444-8444-444444444444"},
	}
}

func planningTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		panic(err)
	}
	return parsed
}
