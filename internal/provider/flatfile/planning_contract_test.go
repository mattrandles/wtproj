package flatfile

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestPlanningStorageAndRecoveryContract(t *testing.T) {
	if planningDirectory != "planning" || planningPromoteJournalName != "planning-promote.json" || planningPromoteJournalVersion != 1 || planningPromotePrepared != "prepared" || planningPromoteCommitted != "committed" {
		t.Fatal("planning storage/journal v1 names changed")
	}
	want := []string{"batch-update.json", "reusable-update.json", "planning-promote.json"}
	if got := planningRecoveryJournalOrder(); !reflect.DeepEqual(got, want) {
		t.Fatalf("recovery order = %v, want %v", got, want)
	}
	order := planningRecoveryJournalOrder()
	order[0] = "mutated"
	if !reflect.DeepEqual(planningRecoveryJournalOrder(), want) {
		t.Fatal("recovery order leaked mutable state")
	}
}

func TestPlanningPromotionJournalEnvelopeContract(t *testing.T) {
	// This tests declarations, not the later strict codec. In particular the
	// byte snapshots must remain base64 fields so rollback can restore exact
	// whitespace and Unicode, rather than re-encoding decoded task objects.
	before := []byte("{\n  \"status\": \"planned\", \"title\": \"世界\"\n}\n")
	after := []byte("{\n  \"status\": \"todo\", \"title\": \"世界\"\n}\n")
	journal := planningPromoteJournal{
		Version: 1, State: planningPromotePrepared,
		SelectedIDs: []string{"11111111-1111-4111-8111-111111111111"},
		Entries: []planningPromoteJournalEntry{{
			Before: planningPromoteSnapshot{Path: "planning/planned/wtp-0d6e4079-0091.json", Data: before},
			After:  planningPromoteSnapshot{Path: "todo/wtp-0d6e4079-0091.json", Data: after},
		}},
	}
	data, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	var decoded planningPromoteJournal
	if err := json.Unmarshal(data, &decoded); err != nil || !reflect.DeepEqual(decoded, journal) {
		t.Fatalf("journal lost endpoint bytes: %#v, %v", decoded, err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	assertPlanningJSONKeys(t, envelope, "version", "state", "selectedIds", "entries")
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(envelope["entries"], &entries); err != nil || len(entries) != 1 {
		t.Fatalf("journal entries = %s, error %v", envelope["entries"], err)
	}
	assertPlanningJSONKeys(t, entries[0], "before", "after")
	for _, name := range []string{"before", "after"} {
		var snapshot map[string]json.RawMessage
		if err := json.Unmarshal(entries[0][name], &snapshot); err != nil {
			t.Fatal(err)
		}
		assertPlanningJSONKeys(t, snapshot, "path", "data")
		if len(snapshot["data"]) == 0 || snapshot["data"][0] != '"' {
			t.Fatalf("snapshot data must encode as base64 string: %s", snapshot["data"])
		}
	}
}

func assertPlanningJSONKeys(t *testing.T, object map[string]json.RawMessage, keys ...string) {
	t.Helper()
	if len(object) != len(keys) {
		t.Fatalf("object keys changed: %v, want %v", object, keys)
	}
	for _, key := range keys {
		if _, exists := object[key]; !exists {
			t.Errorf("missing required key %s", key)
		}
	}
}
