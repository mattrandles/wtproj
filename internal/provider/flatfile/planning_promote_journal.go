package flatfile

// This file owns the planning-promotion transaction format. Planning records
// and execution tasks deliberately retain their exact encoded bytes here so
// recovery never has to guess at formatting or re-encode a user's data.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/planningjson"
)

var planningPromoteTaskRecordFields = []string{
	"id", "shortId", "title", "description", "priority", "estimate", "lane", "model",
	"issueId", "project", "milestone", "version", "featureId", "feature", "gitRepo",
	"gitBranch", "worktreeName", "worktreeDir", "status", "assignee", "dependencies",
	"comments", "createdAt", "updatedAt", "startedAt", "completedAt", "reusableTaskIds",
}

var planningPromoteRequiredTaskRecordFields = []string{
	"id", "shortId", "title", "description", "status", "dependencies", "comments",
	"createdAt", "updatedAt", "startedAt", "completedAt",
}

// encodePlanningPromoteJournal emits the stable field order supplied by the
// journal structs. []byte fields are encoded by encoding/json as base64, which
// is part of this journal's wire contract.
func encodePlanningPromoteJournal(journal planningPromoteJournal) ([]byte, error) {
	if err := validatePlanningPromoteJournalContract(journal); err != nil {
		return nil, fmt.Errorf("invalid planning promotion journal: %w", err)
	}
	data, err := json.Marshal(journal)
	if err != nil {
		return nil, fmt.Errorf("encode planning promotion journal: %w", err)
	}
	return data, nil
}

// decodePlanningPromoteJournal strictly decodes the v1 envelope, including
// every nested object. Unknown and duplicate properties are rejected before
// snapshot semantics are inspected.
func decodePlanningPromoteJournal(data []byte) (planningPromoteJournal, error) {
	if !utf8.Valid(data) {
		return planningPromoteJournal{}, errors.New("planning promotion journal is not valid UTF-8")
	}
	object, err := decodePlanningPromoteObject(data, "planning promotion journal")
	if err != nil {
		return planningPromoteJournal{}, err
	}
	if err := requirePlanningPromoteProperties(object, "version", "state", "selectedIds", "entries"); err != nil {
		return planningPromoteJournal{}, err
	}

	journal := planningPromoteJournal{}
	if journal.Version, err = decodePlanningPromoteInt(object, "version"); err != nil {
		return planningPromoteJournal{}, err
	}
	if journal.State, err = decodePlanningPromoteString(object, "state"); err != nil {
		return planningPromoteJournal{}, err
	}
	if journal.Version != planningPromoteJournalVersion {
		return planningPromoteJournal{}, fmt.Errorf("unsupported planning promotion journal version %d", journal.Version)
	}
	if journal.State != planningPromotePrepared && journal.State != planningPromoteCommitted {
		return planningPromoteJournal{}, fmt.Errorf("invalid planning promotion journal state %q", journal.State)
	}

	journal.SelectedIDs, err = decodePlanningPromoteStringArray(object["selectedIds"], "selectedIds")
	if err != nil {
		return planningPromoteJournal{}, err
	}
	rawEntries, err := decodePlanningPromoteRawArray(object["entries"], "entries")
	if err != nil {
		return planningPromoteJournal{}, err
	}
	journal.Entries = make([]planningPromoteJournalEntry, len(rawEntries))
	for index, rawEntry := range rawEntries {
		entry, err := decodePlanningPromoteEntry(rawEntry, fmt.Sprintf("entries[%d]", index))
		if err != nil {
			return planningPromoteJournal{}, err
		}
		journal.Entries[index] = entry
	}
	if err := validatePlanningPromoteJournalContract(journal); err != nil {
		return planningPromoteJournal{}, fmt.Errorf("invalid planning promotion journal: %w", err)
	}
	return journal, nil
}

func decodePlanningPromoteEntry(data json.RawMessage, label string) (planningPromoteJournalEntry, error) {
	object, err := decodePlanningPromoteObject(data, label)
	if err != nil {
		return planningPromoteJournalEntry{}, err
	}
	if err := requirePlanningPromoteProperties(object, "before", "after"); err != nil {
		return planningPromoteJournalEntry{}, fmt.Errorf("%s: %w", label, err)
	}
	before, err := decodePlanningPromoteSnapshot(object["before"], label+".before")
	if err != nil {
		return planningPromoteJournalEntry{}, err
	}
	after, err := decodePlanningPromoteSnapshot(object["after"], label+".after")
	if err != nil {
		return planningPromoteJournalEntry{}, err
	}
	return planningPromoteJournalEntry{Before: before, After: after}, nil
}

func decodePlanningPromoteSnapshot(data json.RawMessage, label string) (planningPromoteSnapshot, error) {
	object, err := decodePlanningPromoteObject(data, label)
	if err != nil {
		return planningPromoteSnapshot{}, err
	}
	if err := requirePlanningPromoteProperties(object, "path", "data"); err != nil {
		return planningPromoteSnapshot{}, fmt.Errorf("%s: %w", label, err)
	}
	path, err := decodePlanningPromoteString(object, "path")
	if err != nil {
		return planningPromoteSnapshot{}, fmt.Errorf("%s: %w", label, err)
	}
	encoded, err := decodePlanningPromoteString(object, "data")
	if err != nil {
		return planningPromoteSnapshot{}, fmt.Errorf("%s.data: %w", label, err)
	}
	if encoded == "" {
		return planningPromoteSnapshot{}, fmt.Errorf("%s.data must not be empty", label)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(decoded) == 0 {
		if err == nil {
			err = errors.New("decoded data is empty")
		}
		return planningPromoteSnapshot{}, fmt.Errorf("%s.data must be non-empty base64: %w", label, err)
	}
	if base64.StdEncoding.EncodeToString(decoded) != encoded {
		return planningPromoteSnapshot{}, fmt.Errorf("%s.data must use canonical base64", label)
	}
	return planningPromoteSnapshot{Path: path, Data: decoded}, nil
}

func decodePlanningPromoteStringArray(raw json.RawMessage, name string) ([]string, error) {
	items, err := decodePlanningPromoteRawArray(raw, name)
	if err != nil {
		return nil, err
	}
	values := make([]string, len(items))
	for index, item := range items {
		value, err := decodePlanningPromoteStringValue(item, fmt.Sprintf("%s[%d]", name, index))
		if err != nil {
			return nil, err
		}
		if value == "" {
			return nil, fmt.Errorf("%s[%d] must not be empty", name, index)
		}
		values[index] = value
	}
	return values, nil
}

func decodePlanningPromoteRawArray(raw json.RawMessage, name string) ([]json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, fmt.Errorf("%s must be an array", name)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || items == nil {
		if err == nil {
			err = errors.New("null")
		}
		return nil, fmt.Errorf("%s must be an array: %w", name, err)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("%s must not be empty", name)
	}
	return items, nil
}

func decodePlanningPromoteObject(data []byte, label string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", label, err)
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return nil, fmt.Errorf("%s must be an object", label)
	}
	object := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("decode %s property: %w", label, err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("decode %s property name", label)
		}
		if _, exists := object[key]; exists {
			return nil, fmt.Errorf("%s contains duplicate property %q", label, key)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("decode %s property %q: %w", label, key, err)
		}
		if err := rejectPlanningPromoteDuplicateProperties(value); err != nil {
			return nil, fmt.Errorf("decode %s property %q: %w", label, key, err)
		}
		object[key] = value
	}
	closing, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", label, err)
	}
	if delimiter, ok := closing.(json.Delim); !ok || delimiter != '}' {
		return nil, fmt.Errorf("decode %s: expected closing object", label)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("decode %s: trailing JSON data", label)
		}
		return nil, fmt.Errorf("decode %s: %w", label, err)
	}
	return object, nil
}

func rejectPlanningPromoteDuplicateProperties(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := walkPlanningPromoteJSONValue(decoder); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON data")
		}
		return err
	}
	return nil
}

func walkPlanningPromoteJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object property name is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate property %q", key)
			}
			seen[key] = struct{}{}
			if err := walkPlanningPromoteJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return errors.New("object is not closed")
		}
	case '[':
		for decoder.More() {
			if err := walkPlanningPromoteJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return errors.New("array is not closed")
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	return nil
}

func requirePlanningPromoteProperties(object map[string]json.RawMessage, properties ...string) error {
	allowed := make(map[string]struct{}, len(properties))
	for _, property := range properties {
		allowed[property] = struct{}{}
		if _, ok := object[property]; !ok {
			return fmt.Errorf("missing required property %q", property)
		}
	}
	for property := range object {
		if _, ok := allowed[property]; !ok {
			return fmt.Errorf("unknown property %q", property)
		}
	}
	return nil
}

func requirePlanningPromoteOnlyProperties(object map[string]json.RawMessage, properties ...string) error {
	allowed := make(map[string]struct{}, len(properties))
	for _, property := range properties {
		allowed[property] = struct{}{}
	}
	for property := range object {
		if _, ok := allowed[property]; !ok {
			return fmt.Errorf("unknown property %q", property)
		}
	}
	return nil
}

func decodePlanningPromoteString(object map[string]json.RawMessage, property string) (string, error) {
	raw, ok := object[property]
	if !ok {
		return "", fmt.Errorf("missing required property %q", property)
	}
	return decodePlanningPromoteStringValue(raw, property)
}

func decodePlanningPromoteStringValue(raw json.RawMessage, property string) (string, error) {
	var value string
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil || !utf8.ValidString(value) {
		return "", fmt.Errorf("%s must be a string", property)
	}
	return value, nil
}

func decodePlanningPromoteInt(object map[string]json.RawMessage, property string) (int, error) {
	raw, ok := object[property]
	if !ok {
		return 0, fmt.Errorf("missing required property %q", property)
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", property, err)
	}
	return value, nil
}

// validatePlanningPromoteJournalContract validates all facts that are
// independent of the live store. It is deliberately called by both encode
// and decode, so a journal cannot be written that recovery would reject.
func validatePlanningPromoteJournalContract(journal planningPromoteJournal) error {
	if journal.Version != planningPromoteJournalVersion {
		return fmt.Errorf("unsupported version %d", journal.Version)
	}
	if journal.State != planningPromotePrepared && journal.State != planningPromoteCommitted {
		return fmt.Errorf("invalid state %q", journal.State)
	}
	if journal.SelectedIDs == nil || len(journal.SelectedIDs) == 0 {
		return errors.New("selectedIds must be a non-empty array")
	}
	if journal.Entries == nil || len(journal.Entries) == 0 {
		return errors.New("entries must be a non-empty array")
	}
	if len(journal.SelectedIDs) != len(journal.Entries) {
		return errors.New("selectedIds and entries must have matching lengths")
	}

	seenIDs := make(map[string]struct{}, len(journal.SelectedIDs))
	seenPaths := make(map[string]struct{}, len(journal.Entries)*2)
	var commonAfterTimeSet bool
	var commonAfterTime time.Time
	for index, entry := range journal.Entries {
		before, after, err := decodePlanningPromoteEntrySnapshots(entry)
		if err != nil {
			return fmt.Errorf("entry %d: %w", index, err)
		}
		if journal.SelectedIDs[index] != before.ID {
			return fmt.Errorf("entry %d selectedIds[%d] %q does not match before identity %q", index, index, journal.SelectedIDs[index], before.ID)
		}
		if _, duplicate := seenIDs[before.ID]; duplicate {
			return fmt.Errorf("entry %d duplicates selected identity %s", index, before.ID)
		}
		seenIDs[before.ID] = struct{}{}

		for snapshotIndex, snapshot := range []planningPromoteSnapshot{entry.Before, entry.After} {
			if err := validatePlanningPromoteCanonicalPath(snapshot.Path); err != nil {
				return fmt.Errorf("entry %d %s path: %w", index, planningPromoteSnapshotLabel(snapshotIndex), err)
			}
			if _, duplicate := seenPaths[snapshot.Path]; duplicate {
				return fmt.Errorf("entry %d duplicates target %s", index, snapshot.Path)
			}
			seenPaths[snapshot.Path] = struct{}{}
			if len(snapshot.Data) == 0 || !utf8.Valid(snapshot.Data) {
				return fmt.Errorf("entry %d %s data must be non-empty valid UTF-8", index, planningPromoteSnapshotLabel(snapshotIndex))
			}
		}

		if before.Status != core.PlanningStatusPlanned {
			return fmt.Errorf("entry %d before status must be %q", index, core.PlanningStatusPlanned)
		}
		if after.Status != core.StatusTodo {
			return fmt.Errorf("entry %d after status must be %q", index, core.StatusTodo)
		}
		if before.ID != after.ID || before.ShortID != after.ShortID {
			return fmt.Errorf("entry %d before and after identities do not match", index)
		}
		if !after.UpdatedAt.After(before.UpdatedAt) {
			return fmt.Errorf("entry %d after updatedAt must advance", index)
		}
		if after.StartedAt != nil || after.CompletedAt != nil {
			return fmt.Errorf("entry %d after lifecycle timestamps must be null", index)
		}
		if index > 0 && !after.UpdatedAt.Equal(commonAfterTime) {
			return fmt.Errorf("entry %d after updatedAt must use the common promotion timestamp", index)
		}
		if !commonAfterTimeSet {
			commonAfterTime = after.UpdatedAt
			commonAfterTimeSet = true
		}

		if err := validatePlanningPromoteEntryPaths(entry, before, after); err != nil {
			return fmt.Errorf("entry %d: %w", index, err)
		}
		if !reflect.DeepEqual(planningPromoteTaskFromPlanning(before, after.UpdatedAt), after) {
			return fmt.Errorf("entry %d after snapshot may differ only in status, updatedAt, and lifecycle normalization", index)
		}
	}
	return nil
}

func decodePlanningPromoteEntrySnapshots(entry planningPromoteJournalEntry) (core.PlanningItem, core.Task, error) {
	before, err := planningjson.Decode(entry.Before.Data)
	if err != nil {
		return core.PlanningItem{}, core.Task{}, fmt.Errorf("decode before planning snapshot: %w", err)
	}
	if err := rejectPlanningPromoteDuplicateProperties(entry.After.Data); err != nil {
		return core.PlanningItem{}, core.Task{}, fmt.Errorf("decode after task snapshot: %w", err)
	}
	afterObject, err := decodePlanningPromoteObject(entry.After.Data, "after task snapshot")
	if err != nil {
		return core.PlanningItem{}, core.Task{}, err
	}
	if err := requirePlanningPromoteOnlyProperties(afterObject, planningPromoteTaskRecordFields...); err != nil {
		return core.PlanningItem{}, core.Task{}, fmt.Errorf("after task snapshot: %w", err)
	}
	for _, field := range planningPromoteRequiredTaskRecordFields {
		if _, ok := afterObject[field]; !ok {
			return core.PlanningItem{}, core.Task{}, fmt.Errorf("after task snapshot is missing required property %q", field)
		}
	}
	var after core.Task
	if err := json.Unmarshal(entry.After.Data, &after); err != nil {
		return core.PlanningItem{}, core.Task{}, fmt.Errorf("decode after task snapshot: %w", err)
	}
	if err := after.Validate(); err != nil {
		return core.PlanningItem{}, core.Task{}, fmt.Errorf("invalid after task snapshot: %w", err)
	}
	return before, after, nil
}

func validatePlanningPromoteEntryPaths(entry planningPromoteJournalEntry, before core.PlanningItem, after core.Task) error {
	beforeShort := pathpkg.Join(planningDirectory, string(core.PlanningStatusPlanned), before.ShortID+".json")
	beforeUUID := pathpkg.Join(planningDirectory, string(core.PlanningStatusPlanned), before.ID+".json")
	if entry.Before.Path != beforeShort && entry.Before.Path != beforeUUID {
		return fmt.Errorf("before path %q must be %q or %q", entry.Before.Path, beforeShort, beforeUUID)
	}
	afterPath := pathpkg.Join(string(core.StatusTodo), after.ShortID+".json")
	if entry.After.Path != afterPath {
		return fmt.Errorf("after path %q must be %q", entry.After.Path, afterPath)
	}
	return nil
}

func validatePlanningPromoteCanonicalPath(value string) error {
	if value == "" || strings.ContainsRune(value, '\x00') || strings.Contains(value, "\\") {
		return fmt.Errorf("path %q is not canonical slash-separated storage path", value)
	}
	if pathpkg.IsAbs(value) || filepath.IsAbs(value) || strings.HasPrefix(value, "//") || isWindowsAbsolutePath(value) {
		return fmt.Errorf("path %q must be relative", value)
	}
	if pathpkg.Clean(value) != value || strings.Contains(value, "//") {
		return fmt.Errorf("path %q is not canonical", value)
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("path %q contains a non-canonical segment", value)
		}
	}
	return nil
}

func isWindowsAbsolutePath(value string) bool {
	return len(value) >= 3 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) && value[1] == ':' && (value[2] == '/' || value[2] == '\\')
}

func planningPromoteSnapshotLabel(index int) string {
	if index == 0 {
		return "before"
	}
	return "after"
}

func planningPromoteTaskFromPlanning(before core.PlanningItem, updatedAt time.Time) core.Task {
	return core.Task{
		ID: before.ID, ShortID: before.ShortID, Title: before.Title, Description: before.Description,
		Priority: before.Priority, Estimate: before.Estimate, Lane: before.Lane, Model: before.Model,
		IssueID: before.IssueID, Project: before.Project, Milestone: before.Milestone, Version: before.Version,
		FeatureID: before.FeatureID, Feature: before.Feature, GitRepo: before.GitRepo, GitBranch: before.GitBranch,
		WorktreeName: before.WorktreeName, WorktreeDir: before.WorktreeDir, Status: core.StatusTodo,
		Assignee: before.Assignee, Dependencies: clonePlanningPromoteStrings(before.Dependencies),
		Comments: clonePlanningPromoteComments(before.Comments), CreatedAt: before.CreatedAt, UpdatedAt: updatedAt,
		StartedAt: nil, CompletedAt: nil, ReusableTaskIDs: clonePlanningPromoteStrings(before.ReusableTaskIDs),
	}
}

func clonePlanningPromoteStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}

func clonePlanningPromoteComments(values []core.Comment) []core.Comment {
	if values == nil {
		return nil
	}
	return append([]core.Comment{}, values...)
}

// validatePlanningPromoteJournal is the provider-aware validation boundary.
// The static contract has already checked the fixed planning lifecycle and
// todo destination; the provider catalog check keeps this safe if the task
// status catalog gains additional configured states.
func (p *Provider) validatePlanningPromoteJournal(journal planningPromoteJournal) error {
	if err := validatePlanningPromoteJournalContract(journal); err != nil {
		return err
	}
	for index, entry := range journal.Entries {
		_, after, err := decodePlanningPromoteEntrySnapshots(entry)
		if err != nil {
			return fmt.Errorf("entry %d: %w", index, err)
		}
		if err := after.ValidateWithCatalog(p.catalog); err != nil {
			return fmt.Errorf("entry %d after task snapshot: %w", index, err)
		}
	}
	return nil
}

func (p *Provider) planningPromoteJournalPath() string {
	return filepath.Join(p.root, "meta", planningPromoteJournalName)
}

func (p *Provider) writePlanningPromoteJournal(journal planningPromoteJournal) error {
	if err := p.validatePlanningPromoteJournal(journal); err != nil {
		return fmt.Errorf("invalid planning promotion journal: %w", err)
	}
	data, err := encodePlanningPromoteJournal(journal)
	if err != nil {
		return err
	}
	return writeBytesAtomicWithFileSystem(p.planningPromoteJournalPath(), data, p.fs)
}

func (p *Provider) readPlanningPromoteJournal() (planningPromoteJournal, error) {
	data, err := os.ReadFile(p.planningPromoteJournalPath())
	if err != nil {
		return planningPromoteJournal{}, err
	}
	journal, err := decodePlanningPromoteJournal(data)
	if err != nil {
		return planningPromoteJournal{}, err
	}
	if err := p.validatePlanningPromoteJournal(journal); err != nil {
		return planningPromoteJournal{}, fmt.Errorf("invalid planning promotion journal: %w", err)
	}
	return journal, nil
}

func (p *Provider) removePlanningPromoteJournal() error {
	err := p.removeFile(p.planningPromoteJournalPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// transitionPlanningPromoteJournal is the only legal marker transition.
// Once committed, the journal is immutable and recovery consumes it.
func transitionPlanningPromoteJournal(journal *planningPromoteJournal, state string) error {
	if journal == nil {
		return errors.New("planning promotion journal is required")
	}
	if journal.Version != planningPromoteJournalVersion {
		return fmt.Errorf("unsupported version %d", journal.Version)
	}
	if journal.State != planningPromotePrepared || state != planningPromoteCommitted {
		return fmt.Errorf("invalid transition from %q to %q", journal.State, state)
	}
	journal.State = state
	return nil
}

// validatePlanningPromoteJournalForRecovery performs the complete read-only
// recovery preflight. Every live endpoint must still be absent or contain its
// own exact journal snapshot; an unrelated byte sequence is stale and must
// never be overwritten.
func (p *Provider) validatePlanningPromoteJournalForRecovery(journal planningPromoteJournal) error {
	if err := p.validatePlanningPromoteJournal(journal); err != nil {
		return err
	}
	for index, entry := range journal.Entries {
		if err := p.validatePlanningPromoteLiveSnapshot(entry.Before); err != nil {
			return fmt.Errorf("entry %d before target: %w", index, err)
		}
		if err := p.validatePlanningPromoteLiveSnapshot(entry.After); err != nil {
			return fmt.Errorf("entry %d after target: %w", index, err)
		}
	}
	return nil
}

func (p *Provider) validatePlanningPromoteLiveSnapshot(snapshot planningPromoteSnapshot) error {
	path, err := p.resolvePlanningPromoteTarget(snapshot.Path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", snapshot.Path, err)
	}
	if !bytes.Equal(data, snapshot.Data) {
		return fmt.Errorf("target %s is stale or contains unrelated bytes", snapshot.Path)
	}
	return nil
}

// resolvePlanningPromoteTarget converts a canonical journal path into a host
// path only after checking the path syntax, namespace, and symlink-resolved
// containment. It intentionally accepts both endpoints because recovery
// checks their individual snapshot semantics above.
func (p *Provider) resolvePlanningPromoteTarget(target string) (string, error) {
	if err := validatePlanningPromoteCanonicalPath(target); err != nil {
		return "", err
	}
	if !strings.HasPrefix(target, planningDirectory+"/"+string(core.PlanningStatusPlanned)+"/") && !strings.HasPrefix(target, string(core.StatusTodo)+"/") {
		return "", fmt.Errorf("target path %q is outside planning/planned and todo", target)
	}
	root := filepath.Clean(p.root)
	resolved := filepath.Clean(filepath.Join(root, filepath.FromSlash(target)))
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve storage root %s: %w", root, err)
	}
	canonicalParent, err := filepath.EvalSymlinks(filepath.Dir(resolved))
	if err != nil {
		return "", fmt.Errorf("resolve target parent %s: %w", filepath.Dir(resolved), err)
	}
	canonicalTarget := filepath.Join(canonicalParent, filepath.Base(resolved))
	relative, err := filepath.Rel(canonicalRoot, canonicalTarget)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("target path %q escapes storage root", target)
	}
	if info, err := os.Lstat(resolved); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("target path %q is a symbolic link", target)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect target %s: %w", target, err)
	}
	return resolved, nil
}
