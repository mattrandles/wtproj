package core

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestTaskGroupingJSON(t *testing.T) {
	for _, test := range []struct {
		name  string
		key   string
		value string
		field func(*Task) *string
	}{
		{"issue", "issueId", "Owner/Repo#42", func(task *Task) *string { return &task.IssueID }},
		{"project", "project", "WTP Project", func(task *Task) *string { return &task.Project }},
		{"milestone", "milestone", "Release Candidate", func(task *Task) *string { return &task.Milestone }},
		{"version", "version", "v1.0-RC1+Build", func(task *Task) *string { return &task.Version }},
		{"feature key without name", "featureId", "Feature-7", func(task *Task) *string { return &task.FeatureID }},
		{"feature name without key", "feature", "Task Grouping", func(task *Task) *string { return &task.Feature }},
	} {
		t.Run(test.name, func(t *testing.T) {
			task := validValidationTask(t)
			*test.field(&task) = test.value
			if err := task.Validate(); err != nil {
				t.Fatalf("Task.Validate() = %v", err)
			}
			for _, value := range []any{task, TaskView{Task: task}} {
				data, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(data, &fields); err != nil {
					t.Fatal(err)
				}
				for _, key := range groupingJSONKeys() {
					if key == test.key {
						var got string
						if err := json.Unmarshal(fields[key], &got); err != nil || got != test.value {
							t.Fatalf("%s = %s, want %q (error: %v)", key, fields[key], test.value, err)
						}
					} else if _, present := fields[key]; present {
						t.Fatalf("empty %s must be omitted: %s", key, data)
					}
				}
				var got Task
				if err := json.Unmarshal(data, &got); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, task) {
					t.Fatalf("round trip changed task: got %#v, want %#v", got, task)
				}
			}
			*test.field(&task) = " \t\n"
			if err := task.Validate(); err == nil || !strings.Contains(err.Error(), test.key+" cannot be blank") {
				t.Fatalf("Task.Validate() = %v, want blank %s error", err, test.key)
			}
		})
	}
}

func TestTaskGroupingLegacyJSONCompatibility(t *testing.T) {
	const legacyJSON = `{
		"id": "25c3806a-bd1b-424d-889b-29e5b06679b8",
		"shortId": "wtp-0001",
		"title": "Legacy task",
		"description": "Preserve existing data",
		"status": "todo",
		"dependencies": [],
		"comments": [],
		"createdAt": "2026-03-24T14:10:04Z",
		"updatedAt": "2026-03-24T14:10:04Z",
		"startedAt": null,
		"completedAt": null
	}`
	for _, name := range []string{"absent", "empty", "null"} {
		t.Run(name, func(t *testing.T) {
			var original map[string]json.RawMessage
			if err := json.Unmarshal([]byte(legacyJSON), &original); err != nil {
				t.Fatal(err)
			}
			if name != "absent" {
				value := json.RawMessage(`""`)
				if name == "null" {
					value = json.RawMessage(`null`)
				}
				for _, key := range groupingJSONKeys() {
					original[key] = value
				}
			}
			input, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			var task Task
			if err := json.Unmarshal(input, &task); err != nil {
				t.Fatal(err)
			}
			if err := task.Validate(); err != nil {
				t.Fatalf("legacy task invalid: %v", err)
			}
			data, err := json.Marshal(task)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			for _, key := range groupingJSONKeys() {
				if _, present := got[key]; present {
					t.Fatalf("unset %s must be omitted: %s", key, data)
				}
				delete(original, key)
			}
			if !reflect.DeepEqual(got, original) {
				t.Fatalf("legacy fields changed: got %s, original %s", data, legacyJSON)
			}
		})
	}
}

func groupingJSONKeys() []string {
	return []string{"issueId", "project", "milestone", "version", "featureId", "feature"}
}
