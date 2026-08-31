package batchcsv

import (
	"bytes"
	"encoding/csv"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	updatedAt := time.Date(2026, time.January, 2, 2, 4, 5, 123456789, time.UTC)
	want := []core.BatchTaskUpdateInput{
		{
			ID:                "00000000-0000-4000-8000-000000000001",
			ShortID:           "wtp-0001",
			ExpectedUpdatedAt: updatedAt,
			Title:             core.OptionalString{Set: true, Value: "New, quoted \"title\""},
			Description:       core.OptionalString{Set: true, Value: "line one\nline two"},
			Status:            core.OptionalStatus{Set: true, Value: core.StatusInProgress},
			Priority:          core.OptionalPriority{Set: true, Value: core.PriorityHigh},
			Estimate:          core.OptionalEstimate{Set: true, Value: core.EstimateM},
			Lane:              core.OptionalString{Set: true, Value: "backend"},
			Model:             core.OptionalString{Set: true, Value: "gpt-5"},
			GitRepo:           core.OptionalString{Set: true, Value: "/workspace/repo"},
			GitBranch:         core.OptionalString{Set: true, Value: "feature/csv"},
			WorktreeName:      core.OptionalString{Set: true, Value: "csv-worktree"},
			WorktreeDir:       core.OptionalString{Set: true, Value: "/workspace/csv"},
			Assignee:          core.OptionalString{Set: true, Value: "Ada"},
			Dependencies:      core.OptionalStrings{Set: true, Value: []string{"wtp-0002", "00000000-0000-4000-8000-000000000003"}},
		},
		{
			ShortID:           "wtp-0002",
			ExpectedUpdatedAt: updatedAt,
			Description:       core.OptionalString{Set: true},
			Priority:          core.OptionalPriority{Set: true},
			Dependencies:      core.OptionalStrings{Set: true},
		},
	}

	encoded, err := Encode(want)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if bytes.HasPrefix(encoded, []byte{0xef, 0xbb, 0xbf}) {
		t.Fatal("Encode() emitted a UTF-8 BOM")
	}
	wantHeader := "id,shortId,updatedAt,title,description,status,priority,estimate,lane,model,gitRepo,gitBranch,worktreeName,worktreeDir,assignee,dependencies,_clear\n"
	if !bytes.HasPrefix(encoded, []byte(wantHeader)) {
		t.Fatalf("Encode() header = %q, want prefix %q", encoded, wantHeader)
	}
	if !strings.Contains(string(encoded), `"wtp-0002,00000000-0000-4000-8000-000000000003"`) {
		t.Fatalf("Encode() did not CSV-quote dependencies: %q", encoded)
	}
	if !strings.Contains(string(encoded), `"description,priority,dependencies"`) {
		t.Fatalf("Encode() clear list = %q", encoded)
	}

	got, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if !reflect.DeepEqual(got, []core.BatchTaskUpdateInput{
		want[0],
		{
			ShortID:           "wtp-0002",
			ExpectedUpdatedAt: updatedAt.UTC(),
			Description:       core.OptionalString{Set: true},
			Priority:          core.OptionalPriority{Set: true},
			Dependencies:      core.OptionalStrings{Set: true},
		},
	}) {
		t.Fatalf("Decode() = %#v, want %#v", got, want)
	}
}

func TestDecodeAcceptsBOMWindowsLineEndingsAndMultilineText(t *testing.T) {
	data := "\ufeffid,updatedAt,title,description\r\n" +
		"00000000-0000-4000-8000-000000000001,2026-01-02T03:04:05.123456789Z,Title,\"first line\r\nsecond line\"\r\n"
	got, err := Decode([]byte(data))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got[0].ID != "00000000-0000-4000-8000-000000000001" || got[0].Description.Value != "first line\nsecond line" {
		t.Fatalf("Decode() = %#v", got[0])
	}
	if !got[0].Description.Set || got[0].Title.Value != "Title" {
		t.Fatalf("Decode() patch state = %#v", got[0])
	}
}

func TestDecodeBlankCellsPreserveAndClearListClears(t *testing.T) {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if err := writer.Write(headerNames[:]); err != nil {
		t.Fatalf("write header: %v", err)
	}
	record := make([]string, len(headerNames))
	record[1] = "wtp-0001"
	record[2] = "2026-01-02T03:04:05Z"
	record[3] = "New title"
	record[16] = "description,priority,dependencies"
	if err := writer.Write(record); err != nil {
		t.Fatalf("write row: %v", err)
	}
	writer.Flush()
	data := buffer.String()
	got, err := Decode([]byte(data))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	row := got[0]
	if row.Title.Value != "New title" || !row.Title.Set {
		t.Fatalf("title patch = %#v", row.Title)
	}
	if row.Description != (core.OptionalString{Set: true}) || row.Priority != (core.OptionalPriority{Set: true}) || !row.Dependencies.Set || row.Dependencies.Value != nil {
		t.Fatalf("clear state = %#v", row)
	}
	if row.Lane.Set || row.Model.Set || row.Status.Set || row.Estimate.Set {
		t.Fatalf("blank cells unexpectedly changed fields = %#v", row)
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
		{name: "missing timestamp", input: []core.BatchTaskUpdateInput{{ShortID: "wtp-0001", Title: base.Title}}, want: "updatedAt is required"},
		{name: "missing identifier", input: []core.BatchTaskUpdateInput{{ExpectedUpdatedAt: base.ExpectedUpdatedAt, Title: base.Title}}, want: "id or shortId is required"},
		{name: "empty title", input: []core.BatchTaskUpdateInput{{ShortID: "wtp-0001", ExpectedUpdatedAt: base.ExpectedUpdatedAt, Title: core.OptionalString{Set: true, Value: " "}}}, want: "title must not be empty"},
		{name: "empty status", input: []core.BatchTaskUpdateInput{{ShortID: "wtp-0001", ExpectedUpdatedAt: base.ExpectedUpdatedAt, Status: core.OptionalStatus{Set: true}}}, want: "status must not be empty"},
		{name: "empty dependency", input: []core.BatchTaskUpdateInput{{ShortID: "wtp-0001", ExpectedUpdatedAt: base.ExpectedUpdatedAt, Dependencies: core.OptionalStrings{Set: true, Value: []string{""}}}}, want: "dependencies[0] must not be empty"},
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

func TestDecodeRejectsMalformedInput(t *testing.T) {
	validHeader := "id,updatedAt,title\n"
	validRow := "00000000-0000-4000-8000-000000000001,2026-01-02T03:04:05Z,New\n"
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "unknown header", data: "id,updatedAt,title,extra\n" + validRow, want: "unknown CSV header"},
		{name: "duplicate header", data: "id,updatedAt,title,title\n" + "00000000-0000-4000-8000-000000000001,2026-01-02T03:04:05Z,New,Other\n", want: "duplicate CSV header"},
		{name: "missing updatedAt header", data: "id,title\n00000000-0000-4000-8000-000000000001,New\n", want: "updatedAt"},
		{name: "missing identifier header", data: "updatedAt,title\n2026-01-02T03:04:05Z,New\n", want: "id"},
		{name: "ragged row", data: validHeader + "00000000-0000-4000-8000-000000000001,2026-01-02T03:04:05Z\n", want: "ragged row"},
		{name: "malformed timestamp", data: validHeader + "00000000-0000-4000-8000-000000000001,yesterday,New\n", want: "updatedAt is malformed"},
		{name: "missing identifier", data: validHeader + ",2026-01-02T03:04:05Z,New\n", want: "id or shortId is required"},
		{name: "no patch", data: "id,updatedAt\n00000000-0000-4000-8000-000000000001,2026-01-02T03:04:05Z\n", want: "no mutable patch fields"},
		{name: "duplicate row", data: validHeader + validRow + validRow, want: "duplicate id"},
		{name: "unknown clear field", data: "id,updatedAt,title,_clear\n00000000-0000-4000-8000-000000000001,2026-01-02T03:04:05Z,New,unknown\n", want: "unknown field"},
		{name: "required clear field", data: "id,updatedAt,title,_clear\n00000000-0000-4000-8000-000000000001,2026-01-02T03:04:05Z,New,title\n", want: "required field"},
		{name: "duplicate clear field", data: "id,updatedAt,title,_clear\n00000000-0000-4000-8000-000000000001,2026-01-02T03:04:05Z,New,\"description,description\"\n", want: "duplicate field"},
		{name: "clear conflicts with value", data: "id,updatedAt,title,description,_clear\n00000000-0000-4000-8000-000000000001,2026-01-02T03:04:05Z,New,Description,description\n", want: "both nonblank"},
		{name: "empty dependency", data: "id,updatedAt,title,dependencies\n00000000-0000-4000-8000-000000000001,2026-01-02T03:04:05Z,New,\"wtp-0002,,wtp-0003\"\n", want: "dependencies[1] must not be empty"},
		{name: "malformed CSV", data: "id,updatedAt,title\n00000000-0000-4000-8000-000000000001,2026-01-02T03:04:05Z,\"unterminated\n", want: "malformed CSV"},
		{name: "empty batch", data: "id,updatedAt,title\n", want: "at least one task row"},
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

func TestDecodeRejectsInvalidUTF8(t *testing.T) {
	_, err := Decode([]byte{'i', 'd', ',', 'u', 'p', 'd', 'a', 't', 'e', 'd', 'A', 't', ',', 't', 'i', 't', 'l', 'e', '\n', 0xff})
	if err == nil || !strings.Contains(err.Error(), "valid UTF-8") {
		t.Fatalf("Decode() error = %v, want invalid UTF-8", err)
	}
}
