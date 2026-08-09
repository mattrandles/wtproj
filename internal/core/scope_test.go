package core

import "testing"

func TestBranchIDFixedVectors(t *testing.T) {
	tests := []struct {
		branch string
		want   string
	}{
		{branch: "main", want: "0d6e4079"},
		{branch: "Feature/ABC", want: "f718f729"},
		{branch: "feature/task-metadata", want: "18394566"},
		{branch: "release v1", want: "1ed51341"},
	}

	for _, test := range tests {
		t.Run(test.branch, func(t *testing.T) {
			if got := BranchID(test.branch); got != test.want {
				t.Fatalf("BranchID(%q) = %q, want %q", test.branch, got, test.want)
			}
		})
	}
}

func TestNewBranchScopePreservesExactBranchName(t *testing.T) {
	scope := NewBranchScope("Feature/ABC")
	if scope == nil {
		t.Fatal("NewBranchScope() returned nil for named branch")
	}
	if scope.Branch != "Feature/ABC" {
		t.Fatalf("Branch = %q, want Feature/ABC", scope.Branch)
	}
	if scope.BranchID != "f718f729" {
		t.Fatalf("BranchID = %q, want f718f729", scope.BranchID)
	}
}

func TestNewBranchScopeReturnsNoScopeForEmptyBranch(t *testing.T) {
	if scope := NewBranchScope(""); scope != nil {
		t.Fatalf("NewBranchScope(\"\") = %#v, want nil", scope)
	}
}
