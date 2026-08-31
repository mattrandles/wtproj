package batchjson

import (
	"strings"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	updatedAt := time.Date(2026, time.January, 2, 3, 4, 5, 123456789, time.FixedZone("input", 3600))
	want := []core.BatchTaskUpdateInput{
		{
			ID:                "00000000-0000-4000-8000-000000000001",
			ShortID:           "wtp-0001",
			ExpectedUpdatedAt: updatedAt.UTC(),
			Title:             core.OptionalString{Set: true, Value: "Updated title"},
			Description:       core.OptionalString{Set: true, Value: "Updated description"},
			Status:            core.OptionalStatus{Set: true, Value: core.StatusInProgress},
			Priority:          core.OptionalPriority{Set: true, Value: core.PriorityHigh},
			Estimate:          core.OptionalEstimate{Set: true, Value: core.EstimateM},
			Lane:              core.OptionalString{Set: true, Value: "batch-json"},
			Model:             core.OptionalString{Set: true, Value: "gpt-5"},
			IssueID:           core.OptionalString{Set: true, Value: "ISSUE-42"},
			Project:           core.OptionalString{Set: true, Value: "Apollo"},
			Milestone:         core.OptionalString{Set: true, Value: "MVP"},
			Version:           core.OptionalString{Set: true, Value: "v1.0"},
			FeatureID:         core.OptionalString{Set: true, Value: "FEAT-7"},
			Feature:           core.OptionalString{Set: true, Value: "Search"},
			GitRepo:           core.OptionalString{Set: true, Value: "/workspace/repo"},
			GitBranch:         core.OptionalString{Set: true, Value: "feature/json"},
			WorktreeName:      core.OptionalString{Set: true, Value: "json-worktree"},
			WorktreeDir:       core.OptionalString{Set: true, Value: "/workspace/worktree"},
			Assignee:          core.OptionalString{Set: true, Value: "Ada"},
			Dependencies:      core.OptionalStrings{Set: true, Value: []string{"wtp-0002", "00000000-0000-4000-8000-000000000003"}},
			ReusableTasks:     core.OptionalStrings{Set: true, Value: []string{"Final review", "7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6"}},
		},
		{
			ShortID:           "wtp-0002",
			ExpectedUpdatedAt: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
			Description:       core.OptionalString{Set: true, Value: ""},
			Priority:          core.OptionalPriority{Set: true, Value: ""},
			Dependencies:      core.OptionalStrings{Set: true, Value: nil},
		},
	}

	encoded, err := Encode(want)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	const expected = `{"version":1,"tasks":[{"id":"00000000-0000-4000-8000-000000000001","shortId":"wtp-0001","updatedAt":"2026-01-02T02:04:05.123456789Z","title":"Updated title","description":"Updated description","status":"inProgress","priority":"high","estimate":"m","lane":"batch-json","model":"gpt-5","issueId":"ISSUE-42","project":"Apollo","milestone":"MVP","version":"v1.0","featureId":"FEAT-7","feature":"Search","gitRepo":"/workspace/repo","gitBranch":"feature/json","worktreeName":"json-worktree","worktreeDir":"/workspace/worktree","assignee":"Ada","dependencies":["wtp-0002","00000000-0000-4000-8000-000000000003"],"reusableTasks":["Final review","7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6"]},{"shortId":"wtp-0002","updatedAt":"2026-01-02T03:04:05Z","description":"","priority":"","dependencies":null}]}`
	if string(encoded) != expected {
		t.Fatalf("Encode() = %s, want %s", encoded, expected)
	}

	got, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("Decode() length = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if !equalBatchInput(got[index], want[index]) {
			t.Errorf("Decode() row %d = %#v, want %#v", index+1, got[index], want[index])
		}
	}
}

func TestDecodeJSONNullClearsOnlyClearableFields(t *testing.T) {
	data := []byte(`{"version":1,"tasks":[{"shortId":"wtp-0001","updatedAt":"2026-01-02T03:04:05Z","description":null,"priority":null,"estimate":null,"lane":null,"model":null,"issueId":null,"project":null,"milestone":null,"version":null,"featureId":null,"feature":null,"gitRepo":null,"gitBranch":null,"worktreeName":null,"worktreeDir":null,"assignee":null,"dependencies":null}]}`)
	got, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	row := got[0]
	if !row.Description.Set || row.Description.Value != "" || !row.Priority.Set || row.Priority.Value != "" || !row.Dependencies.Set || row.Dependencies.Value != nil {
		t.Fatalf("null clear state = %#v", row)
	}
	for name, field := range map[string]core.OptionalString{
		"issueId": row.IssueID, "project": row.Project, "milestone": row.Milestone, "version": row.Version,
		"featureId": row.FeatureID, "feature": row.Feature,
	} {
		if !field.Set || field.Value != "" {
			t.Errorf("%s null clear state = %#v", name, field)
		}
	}
	if row.Title.Set || row.Status.Set {
		t.Fatalf("required fields unexpectedly set = %#v", row)
	}
}

func TestReusableTasksPatchSemantics(t *testing.T) {
	const timestamp = `"2026-01-02T03:04:05Z"`
	const firstID = "7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6"
	tests := []struct {
		name    string
		field   string
		set     bool
		want    []string
		wantNil bool
	}{
		{name: "omitted preserves", field: "", set: false, wantNil: true},
		{name: "replace and reorder", field: `,"reusableTasks":["Verify","` + firstID + `"]`, set: true, want: []string{"Verify", firstID}},
		{name: "empty array clears", field: `,"reusableTasks":[]`, set: true, want: []string{}},
		{name: "null clears", field: `,"reusableTasks":null`, set: true, wantNil: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := []byte(`{"version":1,"tasks":[{"shortId":"wtp-0001","updatedAt":` + timestamp + `,"title":"Patch"` + test.field + `}]}`)
			got, err := Decode(data)
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			assignment := got[0].ReusableTasks
			if assignment.Set != test.set || assignment.Value == nil != test.wantNil || !equalStrings(assignment.Value, test.want) {
				t.Fatalf("ReusableTasks = %#v, want set=%v nil=%v values=%v", assignment, test.set, test.wantNil, test.want)
			}
			encoded, err := Encode(got)
			if err != nil {
				t.Fatalf("Encode() error = %v", err)
			}
			if strings.Contains(test.field, `"reusableTasks"`) && !strings.Contains(string(encoded), test.field[1:]) {
				t.Fatalf("Encode() = %s, want reusableTasks field %s", encoded, test.field)
			}
			if test.field == "" && strings.Contains(string(encoded), `"reusableTasks"`) {
				t.Fatalf("Encode() unexpectedly included omitted reusableTasks: %s", encoded)
			}
		})
	}

}

func TestReusableTasksDecodeDefersUnknownNamesAndRejectsInvalidElements(t *testing.T) {
	valid := func(field string) string {
		return `{"version":1,"tasks":[{"shortId":"wtp-0001","updatedAt":"2026-01-02T03:04:05Z","title":"Patch"` + field + `}]}`
	}
	unknown, err := Decode([]byte(valid(`,"reusableTasks":["not-in-the-catalog"]`)))
	if err != nil {
		t.Fatalf("Decode(unknown name) error = %v", err)
	}
	if !unknown[0].ReusableTasks.Set || !equalStrings(unknown[0].ReusableTasks.Value, []string{"not-in-the-catalog"}) {
		t.Fatalf("unknown reusable name = %#v", unknown[0].ReusableTasks)
	}

	for _, test := range []struct {
		name  string
		field string
		want  string
	}{
		{name: "scalar", field: `,"reusableTasks":"Tests"`, want: "reusableTasks must be an array"},
		{name: "number element", field: `,"reusableTasks":["Tests",7]`, want: "reusableTasks[1] must be a non-empty string"},
		{name: "null element", field: `,"reusableTasks":[null]`, want: "reusableTasks[0] must be a non-empty string"},
		{name: "empty element", field: `,"reusableTasks":[" "]`, want: "reusableTasks[0] must be a non-empty string"},
		{name: "duplicate property", field: `,"reusableTasks":["Tests"],"reusableTasks":["Verify"]`, want: "duplicate property"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Decode([]byte(valid(test.field)))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Decode() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestReusableTasksEncodeRejectsEmptySelector(t *testing.T) {
	_, err := Encode([]core.BatchTaskUpdateInput{{
		ShortID:           "wtp-0001",
		ExpectedUpdatedAt: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
		ReusableTasks:     core.OptionalStrings{Set: true, Value: []string{"Tests", " "}},
	}})
	if err == nil || !strings.Contains(err.Error(), "reusableTasks[1] must not be empty") {
		t.Fatalf("Encode() error = %v", err)
	}
}

func TestReusableTasksUTF8RoundTrip(t *testing.T) {
	want := []core.BatchTaskUpdateInput{{
		ShortID:           "wtp-0001",
		ExpectedUpdatedAt: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
		Title:             core.OptionalString{Set: true, Value: "Patch ✓"},
		ReusableTasks:     core.OptionalStrings{Set: true, Value: []string{"レビュー ✓", "café"}},
	}}
	encoded, err := Encode(want)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	got, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(got) != 1 || !equalStrings(got[0].ReusableTasks.Value, want[0].ReusableTasks.Value) || got[0].Title.Value != want[0].Title.Value {
		t.Fatalf("UTF-8 round trip = %#v, want %#v", got, want)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestDecodeRejectsInvalidBatches(t *testing.T) {
	validRow := `{"shortId":"wtp-0001","updatedAt":"2026-01-02T03:04:05.000000001Z","title":"New title"}`
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "unsupported version", data: `{"version":2,"tasks":[` + validRow + `]}`, want: "unsupported batch JSON version"},
		{name: "unknown root property", data: `{"version":1,"tasks":[` + validRow + `],"extra":true}`, want: `unknown property "extra"`},
		{name: "unknown task property", data: `{"version":1,"tasks":[{"shortId":"wtp-0001","updatedAt":"2026-01-02T03:04:05Z","title":"New","extra":true}]}`, want: `unknown property "extra"`},
		{name: "empty tasks", data: `{"version":1,"tasks":[]}`, want: "must not be empty"},
		{name: "missing identifier", data: `{"version":1,"tasks":[{"updatedAt":"2026-01-02T03:04:05Z","title":"New"}]}`, want: "id or shortId is required"},
		{name: "malformed timestamp", data: `{"version":1,"tasks":[{"shortId":"wtp-0001","updatedAt":"yesterday","title":"New"}]}`, want: "updatedAt is malformed"},
		{name: "missing timestamp", data: `{"version":1,"tasks":[{"shortId":"wtp-0001","title":"New"}]}`, want: "updatedAt is required"},
		{name: "null title", data: `{"version":1,"tasks":[{"shortId":"wtp-0001","updatedAt":"2026-01-02T03:04:05Z","title":null}]}`, want: "title must be a non-empty string"},
		{name: "empty status", data: `{"version":1,"tasks":[{"shortId":"wtp-0001","updatedAt":"2026-01-02T03:04:05Z","status":" "}]}`, want: "status must be a non-empty string"},
		{name: "no patch fields", data: `{"version":1,"tasks":[{"shortId":"wtp-0001","updatedAt":"2026-01-02T03:04:05Z"}]}`, want: "no mutable patch fields"},
		{name: "duplicate short id", data: `{"version":1,"tasks":[` + validRow + `,{"shortId":"wtp-0001","updatedAt":"2026-01-02T03:04:06Z","description":"Other"}]}`, want: "duplicate shortId"},
		{name: "dependency not array", data: `{"version":1,"tasks":[{"shortId":"wtp-0001","updatedAt":"2026-01-02T03:04:05Z","dependencies":"wtp-0002"}]}`, want: "dependencies must be an array"},
		{name: "trailing data", data: `{"version":1,"tasks":[` + validRow + `]} {}`, want: "trailing JSON data"},
		{name: "duplicate property", data: `{"version":1,"version":1,"tasks":[` + validRow + `]}`, want: "duplicate property"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Decode([]byte(test.data))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Decode() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestEncodeRejectsInvalidBatches(t *testing.T) {
	base := core.BatchTaskUpdateInput{
		ShortID:           "wtp-0001",
		ExpectedUpdatedAt: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
		Title:             core.OptionalString{Set: true, Value: "New"},
	}
	tests := []struct {
		name  string
		input []core.BatchTaskUpdateInput
		want  string
	}{
		{name: "empty batch", input: nil, want: "at least one task"},
		{name: "missing timestamp", input: []core.BatchTaskUpdateInput{{ShortID: "wtp-0001", Title: core.OptionalString{Set: true, Value: "New"}}}, want: "updatedAt is required"},
		{name: "missing identifier", input: []core.BatchTaskUpdateInput{{ExpectedUpdatedAt: base.ExpectedUpdatedAt, Title: base.Title}}, want: "id or shortId is required"},
		{name: "empty title", input: []core.BatchTaskUpdateInput{{ShortID: "wtp-0001", ExpectedUpdatedAt: base.ExpectedUpdatedAt, Title: core.OptionalString{Set: true, Value: " "}}}, want: "title must not be empty"},
		{name: "empty status", input: []core.BatchTaskUpdateInput{{ShortID: "wtp-0001", ExpectedUpdatedAt: base.ExpectedUpdatedAt, Status: core.OptionalStatus{Set: true}}}, want: "status must not be empty"},
		{name: "no patch", input: []core.BatchTaskUpdateInput{{ShortID: "wtp-0001", ExpectedUpdatedAt: base.ExpectedUpdatedAt}}, want: "no mutable patch fields"},
		{name: "duplicate rows", input: []core.BatchTaskUpdateInput{base, base}, want: "duplicate shortId"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Encode(test.input)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Encode() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func equalBatchInput(left, right core.BatchTaskUpdateInput) bool {
	if left.ID != right.ID || left.ShortID != right.ShortID || !left.ExpectedUpdatedAt.Equal(right.ExpectedUpdatedAt) {
		return false
	}
	if left.Title != right.Title || left.Description != right.Description || left.Status != right.Status || left.Priority != right.Priority || left.Estimate != right.Estimate || left.Lane != right.Lane || left.Model != right.Model || left.IssueID != right.IssueID || left.Project != right.Project || left.Milestone != right.Milestone || left.Version != right.Version || left.FeatureID != right.FeatureID || left.Feature != right.Feature || left.GitRepo != right.GitRepo || left.GitBranch != right.GitBranch || left.WorktreeName != right.WorktreeName || left.WorktreeDir != right.WorktreeDir || left.Assignee != right.Assignee {
		return false
	}
	if left.Dependencies.Set != right.Dependencies.Set || len(left.Dependencies.Value) != len(right.Dependencies.Value) {
		return false
	}
	for index := range left.Dependencies.Value {
		if left.Dependencies.Value[index] != right.Dependencies.Value[index] {
			return false
		}
	}
	if left.ReusableTasks.Set != right.ReusableTasks.Set || len(left.ReusableTasks.Value) != len(right.ReusableTasks.Value) {
		return false
	}
	for index := range left.ReusableTasks.Value {
		if left.ReusableTasks.Value[index] != right.ReusableTasks.Value[index] {
			return false
		}
	}
	return true
}
