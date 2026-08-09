package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestHandoffValidate(t *testing.T) {
	createdAt := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)
	const handoffID = "7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6"
	const taskID = "25c3806a-bd1b-424d-889b-29e5b06679b8"

	tests := []struct {
		name   string
		mutate func(*Handoff)
		want   string
	}{
		{
			name: "valid global handoff without author",
		},
		{
			name: "valid task-scoped handoff with author",
			mutate: func(handoff *Handoff) {
				handoff.TaskID = taskID
				handoff.Author = "Tony"
			},
		},
		{
			name: "id must be lowercase",
			mutate: func(handoff *Handoff) {
				handoff.ID = strings.ToUpper(handoff.ID)
			},
			want: "canonical lowercase UUID",
		},
		{
			name: "id must be canonical",
			mutate: func(handoff *Handoff) {
				handoff.ID = strings.ReplaceAll(handoff.ID, "-", "")
			},
			want: "canonical lowercase UUID",
		},
		{
			name: "task id must be lowercase",
			mutate: func(handoff *Handoff) {
				handoff.TaskID = strings.ToUpper(taskID)
			},
			want: "canonical lowercase UUID",
		},
		{
			name: "task id must be canonical",
			mutate: func(handoff *Handoff) {
				handoff.TaskID = "task-1"
			},
			want: "canonical lowercase UUID",
		},
		{
			name: "author cannot be blank",
			mutate: func(handoff *Handoff) {
				handoff.Author = " \t\n"
			},
			want: "author cannot be blank",
		},
		{
			name: "message is required when empty",
			mutate: func(handoff *Handoff) {
				handoff.Message = ""
			},
			want: "message is required",
		},
		{
			name: "message is required when whitespace only",
			mutate: func(handoff *Handoff) {
				handoff.Message = " \t\n"
			},
			want: "message is required",
		},
		{
			name: "message must be trimmed on the left",
			mutate: func(handoff *Handoff) {
				handoff.Message = " handoff"
			},
			want: "message must be trimmed",
		},
		{
			name: "message must be trimmed on the right",
			mutate: func(handoff *Handoff) {
				handoff.Message = "handoff "
			},
			want: "message must be trimmed",
		},
		{
			name: "createdAt is required",
			mutate: func(handoff *Handoff) {
				handoff.CreatedAt = time.Time{}
			},
			want: "createdAt is required",
		},
		{
			name: "createdAt must be UTC",
			mutate: func(handoff *Handoff) {
				handoff.CreatedAt = time.Date(2026, time.August, 9, 17, 0, 0, 0, time.FixedZone("BST", 3600))
			},
			want: "createdAt must be in UTC",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handoff := Handoff{
				ID:        handoffID,
				Message:   "Leave context for the next worker",
				CreatedAt: createdAt,
			}
			if test.mutate != nil {
				test.mutate(&handoff)
			}

			err := handoff.Validate()
			if test.want == "" {
				if err != nil {
					t.Fatalf("Handoff.Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Handoff.Validate() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestTaskViewJSONHandoffsOmitempty(t *testing.T) {
	handoff := Handoff{
		ID:        "7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6",
		Message:   "Leave context for the next worker",
		CreatedAt: time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC),
	}

	tests := []struct {
		name        string
		handoffs    []Handoff
		wantPresent bool
	}{
		{name: "nil handoffs", handoffs: nil, wantPresent: false},
		{name: "empty handoffs", handoffs: []Handoff{}, wantPresent: false},
		{name: "retained handoff", handoffs: []Handoff{handoff}, wantPresent: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(TaskView{Handoffs: test.handoffs})
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			handoffsJSON, present := fields["handoffs"]
			if present != test.wantPresent {
				t.Fatalf("handoffs field present = %v, want %v; JSON = %s", present, test.wantPresent, data)
			}
			if !test.wantPresent {
				return
			}

			var got []Handoff
			if err := json.Unmarshal(handoffsJSON, &got); err != nil {
				t.Fatalf("json.Unmarshal(handoffs) error = %v", err)
			}
			if len(got) != 1 || got[0].ID != handoff.ID {
				t.Fatalf("handoffs = %#v, want one handoff with ID %q", got, handoff.ID)
			}
		})
	}
}

func TestTaskJSONExcludesViewOnlyHandoffs(t *testing.T) {
	task := Task{
		ID:           "25c3806a-bd1b-424d-889b-29e5b06679b8",
		ShortID:      "wtp-0001",
		Title:        "Persisted task",
		Status:       StatusTodo,
		Dependencies: []string{},
		Comments:     []Comment{},
		CreatedAt:    time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC),
		UpdatedAt:    time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if _, present := fields["handoffs"]; present {
		t.Fatalf("persisted Task JSON unexpectedly contains handoffs: %s", data)
	}
}
