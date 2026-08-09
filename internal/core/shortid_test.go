package core

import (
	"strings"
	"testing"
)

func TestParseShortIDAcceptsLegacyAndScopedForms(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		legacy   bool
		branchID string
		sequence string
	}{
		{name: "legacy lower boundary", value: "wtp-0000", legacy: true, sequence: "0000"},
		{name: "legacy grows beyond four digits", value: "wtp-00000001", legacy: true, sequence: "00000001"},
		{name: "legacy keeps arbitrarily long sequence text", value: "wtp-999999999999999999999999999999999", legacy: true, sequence: "999999999999999999999999999999999"},
		{name: "scoped zero branch", value: "wtp-00000000-0001", branchID: "00000000", sequence: "0001"},
		{name: "scoped maximum branch", value: "wtp-ffffffff-9999", branchID: "ffffffff", sequence: "9999"},
		{name: "scoped mixed lowercase hex", value: "wtp-a1b2c3d4-00000001", branchID: "a1b2c3d4", sequence: "00000001"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseShortID(test.value)
			if err != nil {
				t.Fatalf("ParseShortID(%q) error = %v", test.value, err)
			}
			if got.Legacy != test.legacy {
				t.Fatalf("Legacy = %t, want %t", got.Legacy, test.legacy)
			}
			if got.IsScoped() == test.legacy {
				t.Fatalf("IsScoped() = %t for Legacy = %t", got.IsScoped(), got.Legacy)
			}
			if got.IsLegacy() != test.legacy {
				t.Fatalf("IsLegacy() = %t, want %t", got.IsLegacy(), test.legacy)
			}
			if got.BranchID != test.branchID {
				t.Fatalf("BranchID = %q, want %q", got.BranchID, test.branchID)
			}
			if got.Sequence != test.sequence {
				t.Fatalf("Sequence = %q, want %q", got.Sequence, test.sequence)
			}
		})
	}
}

func TestParseShortIDRejectsMalformedAndUnsafeValues(t *testing.T) {
	values := []string{
		"",
		"wtp-",
		"wtp-1",
		"wtp-123",
		"wtp-12345-0001",
		"wtp-1234567-0001",
		"wtp-123456789-0001",
		"wtp-0000000-0001",
		"wtp-000000000-0001",
		"wtp-00000000-1",
		"wtp-00000000-123",
		"wtp-00000000-0001-2",
		"wtp--0001",
		"wtp-00000000--0001",
		"wtp_0001",
		"wtp-00000000_0001",
		"wtp-00000000.0001",
		"WTP-0001",
		"wtp-ABCDEF12-0001",
		"wtp-abcdef12-0001.JSON",
		"wtp-abcdef12-0001.json",
		"wtp-abcdef12/0001",
		"./wtp-0001",
		"wtp-0001/../other.json",
	}

	for _, value := range values {
		t.Run(value, func(t *testing.T) {
			if _, err := ParseShortID(value); err == nil {
				t.Fatalf("ParseShortID(%q) unexpectedly succeeded", value)
			} else if !strings.Contains(err.Error(), "wtp-NNNN or wtp-BBBBBBBB-NNNN") {
				t.Fatalf("ParseShortID(%q) error = %v, want both accepted forms", value, err)
			}
		})
	}
}

func TestTaskValidateAcceptsScopedShortID(t *testing.T) {
	task := validValidationTask(t)
	task.ShortID = "wtp-abcdef12-0001"

	if err := task.Validate(); err != nil {
		t.Fatalf("Task.Validate() error = %v", err)
	}
}

func TestTaskValidateShortIDErrorNamesBothAcceptedForms(t *testing.T) {
	task := validValidationTask(t)
	task.ShortID = "wtp-abcdef12-000"

	err := task.Validate()
	if err == nil {
		t.Fatal("Task.Validate() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "wtp-NNNN or wtp-BBBBBBBB-NNNN") {
		t.Fatalf("Task.Validate() error = %v, want both accepted forms", err)
	}
}

func TestTaskValidateRejectsFilenameShapedShortID(t *testing.T) {
	task := validValidationTask(t)
	task.ShortID = "wtp-abcdef12-0001.json"

	if err := task.Validate(); err == nil {
		t.Fatal("Task.Validate() unexpectedly accepted a filename-shaped short ID")
	}
}
