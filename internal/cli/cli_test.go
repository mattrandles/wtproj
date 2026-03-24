package cli

import (
	"reflect"
	"testing"
)

func TestRewriteLegacyArgsGetTask(t *testing.T) {
	got, err := rewriteLegacyArgs([]string{"--json", "--get-task", "--task-id", "wtp-0003"})
	if err != nil {
		t.Fatalf("rewriteLegacyArgs error = %v", err)
	}

	want := []string{"--json", "task", "get", "wtp-0003"}
	if !reflect.DeepEqual(got.args, want) {
		t.Fatalf("rewriteLegacyArgs args = %v, want %v", got.args, want)
	}
}

func TestRewriteLegacyArgsRejectsMultipleActions(t *testing.T) {
	_, err := rewriteLegacyArgs([]string{"--get-next-task", "--get-tasks"})
	if err == nil {
		t.Fatal("expected multiple-action error")
	}
}

func TestRewriteLegacyArgsRequiresTaskID(t *testing.T) {
	_, err := rewriteLegacyArgs([]string{"--set-task-done"})
	if err == nil {
		t.Fatal("expected missing task-id error")
	}
}

func TestRewriteLegacyArgsRequiresComment(t *testing.T) {
	_, err := rewriteLegacyArgs([]string{"--add-comment", "--task-id", "wtp-0003"})
	if err == nil {
		t.Fatal("expected missing comment error")
	}
}
