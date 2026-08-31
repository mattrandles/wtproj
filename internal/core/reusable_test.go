package core

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

const reusableDefinitionJSON = `{"id":"7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6","name":"Tests","title":"Run focused tests","instructions":"Check Unicode: café ✓\nKeep the second line.","createdAt":"2026-08-31T09:00:00.123456789Z","updatedAt":"2026-08-31T09:01:00Z"}`

func reusableDefinitionFixture(t *testing.T) ReusableTaskDefinition {
	t.Helper()
	var definition ReusableTaskDefinition
	if err := json.Unmarshal([]byte(reusableDefinitionJSON), &definition); err != nil {
		t.Fatal(err)
	}
	return definition
}

func TestReusableTaskCatalogJSONContract(t *testing.T) {
	for _, input := range []string{
		`{"version":1,"definitions":[]}`,
		`{"version":1,"definitions":[` + reusableDefinitionJSON + `]}`,
	} {
		var catalog ReusableTaskCatalog
		if err := json.Unmarshal([]byte(input), &catalog); err != nil {
			t.Fatal(err)
		}
		if err := catalog.Validate(); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(catalog)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != input {
			t.Fatalf("catalog JSON = %s, want %s", data, input)
		}
	}
}

func TestReusableTaskMissingCatalogContract(t *testing.T) {
	// The storage layer maps only a missing file to this value; no I/O is
	// needed to validate or resolve an unassigned legacy task at this boundary.
	catalog := EmptyReusableTaskCatalog()
	if err := catalog.Validate(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(catalog)
	if err != nil || string(data) != `{"version":1,"definitions":[]}` {
		t.Fatalf("empty catalog = %s, error = %v", data, err)
	}
	definitions, err := ResolveReusableTasks(nil, catalog)
	if err != nil || definitions == nil || len(definitions) != 0 {
		t.Fatalf("resolve unassigned = %#v, error = %v", definitions, err)
	}
	if err := (ReusableTaskCatalog{}).Validate(); err == nil {
		t.Fatal("an invalid existing wrapper must not silently become an empty catalog")
	}
}

func TestReusableTaskAssignmentOrderAndRenameContract(t *testing.T) {
	first := reusableDefinitionFixture(t)
	second := first
	second.ID = "e5c3806a-bd1b-424d-889b-29e5b06679b8"
	second.Name = "Verify"
	catalog := ReusableTaskCatalog{Version: ReusableTaskCatalogVersion, Definitions: []ReusableTaskDefinition{first, second}}
	task := validValidationTask(t)
	// Deliberately oppose catalog order and UUID/name sort order.
	task.ReusableTaskIDs = []string{second.ID, first.ID}
	if err := task.Validate(); err != nil {
		t.Fatal(err)
	}
	beforeTask, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	for _, renamed := range []bool{false, true} {
		if renamed {
			catalog.Definitions[1].Name = "Final review"
			catalog.Definitions[1].Instructions = "Review the latest changes."
			catalog.Definitions[1].UpdatedAt = second.UpdatedAt.Add(time.Second)
		}
		resolved, err := ResolveReusableTasks(task.ReusableTaskIDs, catalog)
		if err != nil {
			t.Fatal(err)
		}
		want := []ReusableTaskDefinition{catalog.Definitions[1], catalog.Definitions[0]}
		if !reflect.DeepEqual(resolved, want) {
			t.Fatalf("resolved = %#v, want assignment order %#v", resolved, want)
		}
		view := TaskView{Task: task, ReusableTasks: resolved, Handoffs: []Handoff{{
			ID: first.ID, Message: "Retain this handoff", CreatedAt: first.CreatedAt,
		}}}
		data, err := json.Marshal(view)
		if err != nil {
			t.Fatal(err)
		}
		var got TaskView
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, view) {
			t.Fatalf("view round trip changed ordered references, definitions, or handoffs: %s", data)
		}
		afterTask, err := json.Marshal(got.Task)
		if err != nil || string(afterTask) != string(beforeTask) {
			t.Fatalf("definition edits changed stored task JSON: %s, error = %v", afterTask, err)
		}
		if strings.Contains(string(afterTask), `"reusableTasks"`) {
			t.Fatalf("stored task includes resolved definitions: %s", afterTask)
		}
		// Resolution must not expose the catalog's backing slice for mutation.
		resolved[0].Name = "Mutated view"
		if catalog.Definitions[1].Name == "Mutated view" {
			t.Fatal("resolved slice aliases the catalog")
		}
	}
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	var got ReusableTaskCatalog
	if err := json.Unmarshal(data, &got); err != nil || !reflect.DeepEqual(got, catalog) {
		t.Fatalf("catalog order changed on round trip: %s, error = %v", data, err)
	}
}

func TestReusableTaskLegacyJSONContract(t *testing.T) {
	const legacyJSON = `{"id":"25c3806a-bd1b-424d-889b-29e5b06679b8","shortId":"wtp-0001","title":"Legacy task","description":"","status":"todo","dependencies":[],"comments":[],"createdAt":"2026-08-31T09:00:00Z","updatedAt":"2026-08-31T09:00:00Z","startedAt":null,"completedAt":null}`
	for _, suffix := range []string{"", `,"reusableTaskIds":[]`, `,"reusableTaskIds":null`} {
		var task Task
		if err := json.Unmarshal([]byte(strings.TrimSuffix(legacyJSON, "}")+suffix+"}"), &task); err != nil {
			t.Fatal(err)
		}
		if err := task.Validate(); err != nil {
			t.Fatal(err)
		}
		resolved, err := ResolveReusableTasks(task.ReusableTaskIDs, EmptyReusableTaskCatalog())
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(task)
		if err != nil || string(data) != legacyJSON {
			t.Fatalf("legacy fields or omission changed: %s, error = %v", data, err)
		}
		for _, definitions := range [][]ReusableTaskDefinition{nil, resolved} {
			viewData, err := json.Marshal(TaskView{Task: task, ReusableTasks: definitions})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(viewData), `"reusableTaskIds"`) || strings.Contains(string(viewData), `"reusableTasks"`) {
				t.Fatalf("empty assignments must be omitted from views: %s", viewData)
			}
		}
	}
}

func TestReusableTaskValidationBaseline(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*ReusableTaskCatalog)
		want   string
	}{
		{"version", func(c *ReusableTaskCatalog) { c.Version = 2 }, "unsupported"},
		{"missing array", func(c *ReusableTaskCatalog) { c.Definitions = nil }, "array"},
		{"invalid UUID", func(c *ReusableTaskCatalog) { c.Definitions[0].ID = "Tests" }, "canonical lowercase UUID"},
		{"untrimmed name", func(c *ReusableTaskCatalog) { c.Definitions[0].Name = " Tests " }, "name must be trimmed"},
		{"required instructions", func(c *ReusableTaskCatalog) { c.Definitions[0].Instructions = " " }, "instructions is required"},
		{"non-UTC", func(c *ReusableTaskCatalog) {
			c.Definitions[0].UpdatedAt = c.Definitions[0].UpdatedAt.In(time.FixedZone("BST", 3600))
		}, "UTC"},
		{"duplicate name", func(c *ReusableTaskCatalog) {
			other := c.Definitions[0]
			other.ID = "25c3806a-bd1b-424d-889b-29e5b06679b8"
			other.Name = "tEsTs"
			c.Definitions = append(c.Definitions, other)
		}, "name \"tEsTs\" is duplicated"},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog := ReusableTaskCatalog{Version: ReusableTaskCatalogVersion, Definitions: []ReusableTaskDefinition{reusableDefinitionFixture(t)}}
			test.mutate(&catalog)
			if err := catalog.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() = %v, want %q", err, test.want)
			}
		})
	}
	definition := reusableDefinitionFixture(t)
	catalog := ReusableTaskCatalog{Version: ReusableTaskCatalogVersion, Definitions: []ReusableTaskDefinition{definition}}
	for _, ids := range [][]string{{definition.Name}, {definition.ID, definition.ID}, {"25c3806a-bd1b-424d-889b-29e5b06679b8"}} {
		if _, err := ResolveReusableTasks(ids, catalog); err == nil {
			t.Fatalf("invalid references %v resolved", ids)
		}
	}
	for _, ids := range [][]string{{definition.Name}, {definition.ID, definition.ID}} {
		task := validValidationTask(t)
		task.ReusableTaskIDs = ids
		if err := task.Validate(); err == nil {
			t.Fatalf("Task.Validate() accepted invalid stored references %v", ids)
		}
	}
}

// These assignments check the input field types at compile time. Request
// codecs and provider implementations own normalization and patch application.
var (
	_                 = CreateReusableTaskInput{Name: "Tests", Title: "Run tests", Instructions: "Run focused tests."}
	_                 = UpdateReusableTaskInput{Name: OptionalString{Set: true, Value: "Checks"}, Title: OptionalString{}, Instructions: OptionalString{}}
	_ []string        = CreateTaskInput{ReusableTasks: []string{"Tests"}}.ReusableTasks
	_ OptionalStrings = UpdateTaskInput{ReusableTasks: OptionalStrings{Set: true, Value: []string{}}}.ReusableTasks
	_ OptionalStrings = BatchTaskUpdateInput{ReusableTasks: OptionalStrings{}}.ReusableTasks
)
