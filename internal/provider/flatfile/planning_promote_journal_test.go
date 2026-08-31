package flatfile

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/planningjson"
)

func TestPlanningPromoteJournalCodecIsDeterministicAndByteExact(t *testing.T) {
	journal := planningPromoteJournalFixture(t, "世界")
	first, err := encodePlanningPromoteJournal(journal)
	if err != nil {
		t.Fatalf("encodePlanningPromoteJournal() error = %v", err)
	}
	second, err := encodePlanningPromoteJournal(journal)
	if err != nil {
		t.Fatalf("second encodePlanningPromoteJournal() error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("deterministic encoding changed:\nfirst=%s\nsecond=%s", first, second)
	}
	decoded, err := decodePlanningPromoteJournal(first)
	if err != nil {
		t.Fatalf("decodePlanningPromoteJournal() error = %v", err)
	}
	if !bytes.Equal(decoded.Entries[0].Before.Data, journal.Entries[0].Before.Data) || !bytes.Equal(decoded.Entries[0].After.Data, journal.Entries[0].After.Data) {
		t.Fatal("codec did not preserve exact snapshot bytes")
	}
	if !bytes.Contains(first, []byte(base64.StdEncoding.EncodeToString(journal.Entries[0].Before.Data))) {
		t.Fatal("journal data is not encoded as base64")
	}
}

func TestPlanningPromoteJournalRejectsDuplicateUnknownAndTrailingJSON(t *testing.T) {
	journal := planningPromoteJournalFixture(t, "fixture")
	encoded, err := encodePlanningPromoteJournal(journal)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{name: "duplicate envelope property", data: bytes.Replace(encoded, []byte(`"state":"prepared"`), []byte(`"state":"prepared","state":"prepared"`), 1), want: "duplicate property"},
		{name: "unknown envelope property", data: bytes.Replace(encoded, []byte(`"entries":`), []byte(`"unknown":true,"entries":`), 1), want: "unknown property"},
		{name: "trailing JSON", data: append(append([]byte{}, encoded...), []byte(`{}`)...), want: "trailing JSON"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodePlanningPromoteJournal(test.data); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("decode error = %v, want %q", err, test.want)
			}
		})
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatal(err)
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(envelope["entries"], &entries); err != nil {
		t.Fatal(err)
	}
	var before map[string]json.RawMessage
	if err := json.Unmarshal(entries[0]["before"], &before); err != nil {
		t.Fatal(err)
	}
	beforeJSON := string(entries[0]["before"])
	beforeJSON = strings.Replace(beforeJSON, `"path":`, `"path":"planning/planned/wtp-0d6e4079-0083.json","path":`, 1)
	entries[0]["before"] = json.RawMessage(beforeJSON)
	modifiedEntries, _ := json.Marshal(entries)
	envelope["entries"] = modifiedEntries
	modified, _ := json.Marshal(envelope)
	if _, err := decodePlanningPromoteJournal(modified); err == nil || !strings.Contains(err.Error(), "duplicate property") {
		t.Fatalf("nested duplicate decode error = %v, want duplicate property", err)
	}
}

func TestPlanningPromoteJournalRejectsEmptyCorruptAndNonCanonicalPaths(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*planningPromoteJournal)
		want   string
	}{
		{name: "nil selected IDs", mutate: func(j *planningPromoteJournal) { j.SelectedIDs = nil }, want: "selectedIds"},
		{name: "empty entries", mutate: func(j *planningPromoteJournal) { j.Entries = nil }, want: "entries"},
		{name: "path traversal", mutate: func(j *planningPromoteJournal) {
			j.Entries[0].Before.Path = "planning/planned/../wtp-0d6e4079-0082.json"
		}, want: "canonical"},
		{name: "Windows drive path", mutate: func(j *planningPromoteJournal) {
			j.Entries[0].Before.Path = "C:/store/planning/planned/wtp-0d6e4079-0082.json"
		}, want: "relative"},
		{name: "Windows separator", mutate: func(j *planningPromoteJournal) { j.Entries[0].After.Path = "todo\\wtp-0d6e4079-0082.json" }, want: "canonical"},
		{name: "absolute path", mutate: func(j *planningPromoteJournal) { j.Entries[0].After.Path = "/tmp/todo/wtp-0d6e4079-0082.json" }, want: "relative"},
		{name: "empty snapshot", mutate: func(j *planningPromoteJournal) { j.Entries[0].After.Data = nil }, want: "decode after task snapshot"},
	} {
		t.Run(test.name, func(t *testing.T) {
			journal := planningPromoteJournalFixture(t, "fixture")
			test.mutate(&journal)
			if _, err := encodePlanningPromoteJournal(journal); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("encode error = %v, want %q", err, test.want)
			}
		})
	}

	corrupt := planningPromoteJournalFixture(t, "fixture")
	corrupt.Entries[0].Before.Data = []byte{0xff}
	if _, err := encodePlanningPromoteJournal(corrupt); err == nil || !strings.Contains(err.Error(), "valid UTF-8") {
		t.Fatalf("invalid UTF-8 encode error = %v", err)
	}
}

func TestPlanningPromoteJournalRejectsIdentityFieldAndTimestampDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*planningPromoteJournal)
	}{
		{name: "selected ID drift", mutate: func(j *planningPromoteJournal) { j.SelectedIDs[0] = "25c3806a-bd1b-424d-889b-29e5b06679b8" }},
		{name: "after short ID drift", mutate: func(j *planningPromoteJournal) {
			var task core.Task
			if err := json.Unmarshal(j.Entries[0].After.Data, &task); err != nil {
				panic(err)
			}
			task.ShortID = "wtp-0d6e4079-0083"
			j.Entries[0].After.Data, _ = json.Marshal(task)
		}},
		{name: "after metadata drift", mutate: func(j *planningPromoteJournal) {
			var task core.Task
			if err := json.Unmarshal(j.Entries[0].After.Data, &task); err != nil {
				panic(err)
			}
			task.Title = "changed"
			j.Entries[0].After.Data, _ = json.Marshal(task)
		}},
		{name: "after timestamp does not advance", mutate: func(j *planningPromoteJournal) {
			var task core.Task
			if err := json.Unmarshal(j.Entries[0].After.Data, &task); err != nil {
				panic(err)
			}
			task.UpdatedAt = task.CreatedAt
			j.Entries[0].After.Data, _ = json.Marshal(task)
		}},
		{name: "duplicate identity", mutate: func(j *planningPromoteJournal) {
			j.SelectedIDs = append(j.SelectedIDs, j.SelectedIDs[0])
			j.Entries = append(j.Entries, j.Entries[0])
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			journal := planningPromoteJournalFixture(t, "fixture")
			test.mutate(&journal)
			if _, err := encodePlanningPromoteJournal(journal); err == nil {
				t.Fatal("invalid journal unexpectedly encoded")
			}
		})
	}
}

func TestPlanningPromoteJournalRecoveryPreflightRejectsStaleBytesBeforeReplacement(t *testing.T) {
	root := t.TempDir()
	p, err := New(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	journal := planningPromoteJournalFixture(t, "fixture")
	beforePath := filepath.Join(root, filepath.FromSlash(journal.Entries[0].Before.Path))
	if err := os.MkdirAll(filepath.Dir(beforePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(beforePath, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.validatePlanningPromoteJournalForRecovery(journal); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale preflight error = %v, want stale rejection", err)
	}
	got, err := os.ReadFile(beforePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "stale" {
		t.Fatalf("preflight changed stale endpoint to %q", got)
	}
}

func TestPlanningPromoteJournalStateTransition(t *testing.T) {
	journal := planningPromoteJournalFixture(t, "fixture")
	if err := transitionPlanningPromoteJournal(&journal, planningPromoteCommitted); err != nil {
		t.Fatalf("prepared -> committed error = %v", err)
	}
	if journal.State != planningPromoteCommitted {
		t.Fatalf("state = %q, want committed", journal.State)
	}
	if err := transitionPlanningPromoteJournal(&journal, planningPromoteCommitted); err == nil {
		t.Fatal("committed -> committed transition unexpectedly succeeded")
	}
}

func planningPromoteJournalFixture(t *testing.T, title string) planningPromoteJournal {
	t.Helper()
	created := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	updated := created.Add(time.Minute)
	item := core.PlanningItem{
		ID: "15c3806a-bd1b-424d-889b-29e5b06679b8", ShortID: "wtp-0d6e4079-0082", Title: title,
		Description: "description", Status: core.PlanningStatusPlanned, Dependencies: []string{}, Comments: []core.Comment{},
		CreatedAt: created, UpdatedAt: updated, StartedAt: nil, CompletedAt: nil,
	}
	before, err := planningjson.Encode(item)
	if err != nil {
		t.Fatal(err)
	}
	afterTask := planningPromoteTaskFromPlanning(item, updated.Add(time.Minute))
	after, err := json.Marshal(afterTask)
	if err != nil {
		t.Fatal(err)
	}
	return planningPromoteJournal{
		Version: planningPromoteJournalVersion, State: planningPromotePrepared,
		SelectedIDs: []string{item.ID},
		Entries: []planningPromoteJournalEntry{{
			Before: planningPromoteSnapshot{Path: "planning/planned/" + item.ShortID + ".json", Data: before},
			After:  planningPromoteSnapshot{Path: "todo/" + item.ShortID + ".json", Data: after},
		}},
	}
}
