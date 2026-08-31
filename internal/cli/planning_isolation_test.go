package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
	"github.com/mattrandles/wtproj/internal/provider/flatfile"
)

func TestGraphDoesNotExpandPlanningDependencies(t *testing.T) {
	p, err := flatfile.New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	internalDependency, err := p.CreateTask(core.CreateTaskInput{Title: "internal executable dependency"})
	if err != nil {
		t.Fatalf("CreateTask(dependency) error = %v", err)
	}
	planning, err := p.CreatePlanningItem(core.CreatePlanningItemInput{
		Title: "planning-only dependency", Status: core.PlanningStatusPlanned,
	})
	if err != nil {
		t.Fatalf("CreatePlanningItem() error = %v", err)
	}
	target, err := p.CreateTask(core.CreateTaskInput{
		Title:        "executable graph root",
		Dependencies: []string{internalDependency.ShortID, planning.ShortID},
	})
	if err != nil {
		t.Fatalf("CreateTask(target) error = %v", err)
	}

	var output bytes.Buffer
	if err := runGraph(context{
		provider: p, stdout: &output, stderr: &bytes.Buffer{}, jsonOut: true,
	}, []string{"--status", "all"}); err != nil {
		t.Fatalf("runGraph() error = %v", err)
	}
	if !strings.Contains(output.String(), target.ShortID) || !strings.Contains(output.String(), internalDependency.ShortID) {
		t.Fatalf("graph omitted executable records: %s", output.String())
	}
	var nodes []struct {
		Task         *core.TaskView `json:"task,omitempty"`
		Dependencies []struct {
			Task *core.TaskView `json:"task,omitempty"`
		} `json:"dependencies,omitempty"`
	}
	if err := json.Unmarshal(output.Bytes(), &nodes); err != nil {
		t.Fatalf("decode graph JSON: %v\n%s", err, output.String())
	}
	if len(nodes) != 1 || nodes[0].Task == nil || nodes[0].Task.ID != target.ID || len(nodes[0].Dependencies) != 1 || nodes[0].Dependencies[0].Task == nil || nodes[0].Dependencies[0].Task.ID != internalDependency.ID {
		t.Fatalf("graph nodes = %#v, want target with one executable internal edge", nodes)
	}
	if nodes[0].Task.ID == planning.ID || nodes[0].Dependencies[0].Task.ID == planning.ID {
		t.Fatalf("graph expanded planning task node: %#v", nodes)
	}

	listed, err := p.ListTasks(provider.TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks() after graph error = %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("execution list after graph = %d, want two executable tasks", len(listed))
	}
}
