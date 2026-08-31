package flatfile

// This file owns the reusable-delete transaction format. It is deliberately
// separate from batch_update.go: reusable deletion has a catalog endpoint,
// byte-exact task snapshots, and different recovery semantics.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/reusablejson"
)

const (
	reusableUpdateJournalVersion   = 1
	reusableUpdateJournalName      = "reusable-update.json"
	reusableUpdateJournalPrepared  = "prepared"
	reusableUpdateJournalCommitted = "committed"

	reusableUpdateCatalogTarget = reusableFilename
)

// reusableUpdateJournal records the two complete, byte-exact endpoints of a
// reusable definition deletion. Paths are canonical slash-separated paths
// relative to the .wtp directory, never host-specific absolute paths.
type reusableUpdateJournal struct {
	Version        int                           `json:"version"`
	State          string                        `json:"state"`
	ReusableTaskID string                        `json:"reusableTaskId"`
	Catalog        reusableUpdateJournalChange   `json:"catalog"`
	AffectedTasks  []reusableUpdateJournalChange `json:"affectedTasks"`
}

// reusableUpdateJournalChange keeps the exact bytes at one explicit target
// before and after the transaction. Exists makes an absent endpoint distinct
// from an empty file.
type reusableUpdateJournalChange struct {
	Before reusableUpdateJournalSnapshot `json:"before"`
	After  reusableUpdateJournalSnapshot `json:"after"`
}

type reusableUpdateJournalSnapshot struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Data   []byte `json:"data"`
}

// encodeReusableUpdateJournal is deterministic. Semantic validation that
// depends on the provider's status catalog belongs to validate... below.
func encodeReusableUpdateJournal(journal reusableUpdateJournal) ([]byte, error) {
	if journal.Version != reusableUpdateJournalVersion {
		return nil, fmt.Errorf("unsupported reusable update journal version %d", journal.Version)
	}
	if !validReusableUpdateJournalState(journal.State) {
		return nil, fmt.Errorf("invalid reusable update journal state %q", journal.State)
	}
	return json.Marshal(journal)
}

// decodeReusableUpdateJournal strictly decodes the wire envelope. It rejects
// duplicate and unknown properties before semantic validation touches it.
func decodeReusableUpdateJournal(data []byte) (reusableUpdateJournal, error) {
	if !utf8.Valid(data) {
		return reusableUpdateJournal{}, errors.New("reusable update journal is not valid UTF-8")
	}
	object, err := decodeReusableUpdateJournalObject(data, "reusable update journal")
	if err != nil {
		return reusableUpdateJournal{}, err
	}
	if err := requireReusableUpdateJournalProperties(object, "version", "state", "reusableTaskId", "catalog", "affectedTasks"); err != nil {
		return reusableUpdateJournal{}, err
	}

	journal := reusableUpdateJournal{}
	if journal.Version, err = decodeReusableUpdateJournalInt(object, "version"); err != nil {
		return reusableUpdateJournal{}, err
	}
	if journal.State, err = decodeReusableUpdateJournalString(object, "state"); err != nil {
		return reusableUpdateJournal{}, err
	}
	if journal.ReusableTaskID, err = decodeReusableUpdateJournalString(object, "reusableTaskId"); err != nil {
		return reusableUpdateJournal{}, err
	}
	if journal.Catalog, err = decodeReusableUpdateJournalChange(object["catalog"], "catalog"); err != nil {
		return reusableUpdateJournal{}, err
	}

	rawTasks := object["affectedTasks"]
	if bytes.Equal(bytes.TrimSpace(rawTasks), []byte("null")) {
		return reusableUpdateJournal{}, errors.New("affectedTasks must be an array")
	}
	var rawEntries []json.RawMessage
	if err := json.Unmarshal(rawTasks, &rawEntries); err != nil || rawEntries == nil {
		if err == nil {
			err = errors.New("null")
		}
		return reusableUpdateJournal{}, fmt.Errorf("affectedTasks must be an array: %w", err)
	}
	journal.AffectedTasks = make([]reusableUpdateJournalChange, len(rawEntries))
	for index, rawEntry := range rawEntries {
		entry, entryErr := decodeReusableUpdateJournalChange(rawEntry, fmt.Sprintf("affectedTasks[%d]", index))
		if entryErr != nil {
			return reusableUpdateJournal{}, entryErr
		}
		journal.AffectedTasks[index] = entry
	}
	return journal, nil
}

func decodeReusableUpdateJournalChange(data json.RawMessage, label string) (reusableUpdateJournalChange, error) {
	object, err := decodeReusableUpdateJournalObject(data, label)
	if err != nil {
		return reusableUpdateJournalChange{}, err
	}
	if err := requireReusableUpdateJournalProperties(object, "before", "after"); err != nil {
		return reusableUpdateJournalChange{}, fmt.Errorf("%s: %w", label, err)
	}
	before, err := decodeReusableUpdateJournalSnapshot(object["before"], label+".before")
	if err != nil {
		return reusableUpdateJournalChange{}, err
	}
	after, err := decodeReusableUpdateJournalSnapshot(object["after"], label+".after")
	if err != nil {
		return reusableUpdateJournalChange{}, err
	}
	return reusableUpdateJournalChange{Before: before, After: after}, nil
}

func decodeReusableUpdateJournalSnapshot(data json.RawMessage, label string) (reusableUpdateJournalSnapshot, error) {
	object, err := decodeReusableUpdateJournalObject(data, label)
	if err != nil {
		return reusableUpdateJournalSnapshot{}, err
	}
	if err := requireReusableUpdateJournalProperties(object, "path", "exists", "data"); err != nil {
		return reusableUpdateJournalSnapshot{}, fmt.Errorf("%s: %w", label, err)
	}
	path, err := decodeReusableUpdateJournalString(object, "path")
	if err != nil {
		return reusableUpdateJournalSnapshot{}, fmt.Errorf("%s: %w", label, err)
	}
	var exists bool
	if err := json.Unmarshal(object["exists"], &exists); err != nil {
		return reusableUpdateJournalSnapshot{}, fmt.Errorf("%s.exists must be a boolean", label)
	}
	var bytesValue []byte
	if err := json.Unmarshal(object["data"], &bytesValue); err != nil {
		return reusableUpdateJournalSnapshot{}, fmt.Errorf("%s.data must be a base64 string or null", label)
	}
	return reusableUpdateJournalSnapshot{Path: path, Exists: exists, Data: bytesValue}, nil
}

func decodeReusableUpdateJournalString(object map[string]json.RawMessage, property string) (string, error) {
	raw, ok := object[property]
	if !ok {
		return "", fmt.Errorf("missing required property %q", property)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || !utf8.ValidString(value) {
		return "", fmt.Errorf("%s must be a string", property)
	}
	return value, nil
}

func decodeReusableUpdateJournalInt(object map[string]json.RawMessage, property string) (int, error) {
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

func decodeReusableUpdateJournalObject(data []byte, label string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", label, err)
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
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
		if err := rejectReusableUpdateDuplicateProperties(value); err != nil {
			return nil, fmt.Errorf("decode %s property %q: %w", label, key, err)
		}
		object[key] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", label, err)
		}
		return nil, fmt.Errorf("decode %s: expected closing object", label)
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", label, err)
		}
		return nil, fmt.Errorf("decode %s: trailing JSON token %v", label, token)
	}
	return object, nil
}

func rejectReusableUpdateDuplicateProperties(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := walkReusableUpdateJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON data")
		}
		return err
	}
	return nil
}

func walkReusableUpdateJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
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
			if err := walkReusableUpdateJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := walkReusableUpdateJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return errors.New("invalid JSON delimiter")
	}
}

func requireReusableUpdateJournalProperties(object map[string]json.RawMessage, properties ...string) error {
	for _, property := range properties {
		if _, ok := object[property]; !ok {
			return fmt.Errorf("missing required property %q", property)
		}
	}
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

func validReusableUpdateJournalState(state string) bool {
	return state == reusableUpdateJournalPrepared || state == reusableUpdateJournalCommitted
}

// transitionReusableUpdateJournal performs the only legal state transition.
// A committed journal is immutable; recovery consumes it by removing the file.
func transitionReusableUpdateJournal(journal *reusableUpdateJournal, state string) error {
	if journal == nil {
		return errors.New("reusable update journal is required")
	}
	if journal.Version != reusableUpdateJournalVersion {
		return fmt.Errorf("unsupported reusable update journal version %d", journal.Version)
	}
	if journal.State != reusableUpdateJournalPrepared || state != reusableUpdateJournalCommitted {
		return fmt.Errorf("invalid reusable update journal transition from %q to %q", journal.State, state)
	}
	journal.State = state
	return nil
}

func (p *Provider) reusableUpdateJournalPath() string {
	return filepath.Join(p.root, "meta", reusableUpdateJournalName)
}

func (p *Provider) writeReusableUpdateJournal(journal reusableUpdateJournal) error {
	if err := p.validateReusableUpdateJournal(journal); err != nil {
		return fmt.Errorf("invalid reusable update journal: %w", err)
	}
	data, err := encodeReusableUpdateJournal(journal)
	if err != nil {
		return err
	}
	return writeBytesAtomicWithFileSystem(p.reusableUpdateJournalPath(), data, p.fs)
}

func (p *Provider) readReusableUpdateJournal() (reusableUpdateJournal, error) {
	data, err := os.ReadFile(p.reusableUpdateJournalPath())
	if err != nil {
		return reusableUpdateJournal{}, err
	}
	journal, err := decodeReusableUpdateJournal(data)
	if err != nil {
		return reusableUpdateJournal{}, err
	}
	if err := p.validateReusableUpdateJournal(journal); err != nil {
		return reusableUpdateJournal{}, fmt.Errorf("invalid reusable update journal: %w", err)
	}
	return journal, nil
}

func (p *Provider) removeReusableUpdateJournal() error {
	err := p.removeFile(p.reusableUpdateJournalPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// validateReusableUpdateJournal verifies the full static contract before a
// future recovery path may replace a single file. It has no filesystem side
// effects and does not inspect current on-disk endpoints.
func (p *Provider) validateReusableUpdateJournal(journal reusableUpdateJournal) error {
	if journal.Version != reusableUpdateJournalVersion {
		return fmt.Errorf("unsupported version %d", journal.Version)
	}
	if !validReusableUpdateJournalState(journal.State) {
		return fmt.Errorf("invalid state %q", journal.State)
	}
	beforeCatalog, afterCatalog, err := p.validateReusableUpdateCatalogChange(journal.Catalog, journal.ReusableTaskID)
	if err != nil {
		return err
	}
	if journal.AffectedTasks == nil {
		return errors.New("affectedTasks must be an array")
	}

	seenTaskIDs := make(map[string]struct{}, len(journal.AffectedTasks))
	seenTargets := make(map[string]struct{}, len(journal.AffectedTasks))
	for index, change := range journal.AffectedTasks {
		before, _, err := p.validateReusableUpdateTaskChange(change, journal.ReusableTaskID, beforeCatalog, afterCatalog)
		if err != nil {
			return fmt.Errorf("affectedTasks[%d]: %w", index, err)
		}
		if _, duplicate := seenTaskIDs[before.ID]; duplicate {
			return fmt.Errorf("affectedTasks[%d] duplicates task %s", index, before.ID)
		}
		seenTaskIDs[before.ID] = struct{}{}
		if _, duplicate := seenTargets[change.Before.Path]; duplicate {
			return fmt.Errorf("affectedTasks[%d] duplicates target %s", index, change.Before.Path)
		}
		seenTargets[change.Before.Path] = struct{}{}
	}
	return nil
}

func (p *Provider) validateReusableUpdateCatalogChange(change reusableUpdateJournalChange, reusableTaskID string) (core.ReusableTaskCatalog, core.ReusableTaskCatalog, error) {
	if err := validateReusableUpdateSnapshot(change.Before); err != nil {
		return core.ReusableTaskCatalog{}, core.ReusableTaskCatalog{}, fmt.Errorf("catalog.before: %w", err)
	}
	if err := validateReusableUpdateSnapshot(change.After); err != nil {
		return core.ReusableTaskCatalog{}, core.ReusableTaskCatalog{}, fmt.Errorf("catalog.after: %w", err)
	}
	if err := p.validateReusableUpdateTarget(change.Before.Path, reusableUpdateCatalogTarget); err != nil {
		return core.ReusableTaskCatalog{}, core.ReusableTaskCatalog{}, fmt.Errorf("catalog.before: %w", err)
	}
	if err := p.validateReusableUpdateTarget(change.After.Path, reusableUpdateCatalogTarget); err != nil {
		return core.ReusableTaskCatalog{}, core.ReusableTaskCatalog{}, fmt.Errorf("catalog.after: %w", err)
	}
	if !change.Before.Exists || !change.After.Exists {
		return core.ReusableTaskCatalog{}, core.ReusableTaskCatalog{}, errors.New("catalog snapshots must exist")
	}
	before, err := reusablejson.Decode(change.Before.Data)
	if err != nil {
		return core.ReusableTaskCatalog{}, core.ReusableTaskCatalog{}, fmt.Errorf("decode catalog.before: %w", err)
	}
	after, err := reusablejson.Decode(change.After.Data)
	if err != nil {
		return core.ReusableTaskCatalog{}, core.ReusableTaskCatalog{}, fmt.Errorf("decode catalog.after: %w", err)
	}
	deletedIndex := -1
	for index, definition := range before.Definitions {
		if definition.ID == reusableTaskID {
			deletedIndex = index
			break
		}
	}
	if deletedIndex == -1 {
		return core.ReusableTaskCatalog{}, core.ReusableTaskCatalog{}, fmt.Errorf("reusableTaskId %q is not present in catalog.before", reusableTaskID)
	}
	for _, definition := range after.Definitions {
		if definition.ID == reusableTaskID {
			return core.ReusableTaskCatalog{}, core.ReusableTaskCatalog{}, fmt.Errorf("reusableTaskId %q remains in catalog.after", reusableTaskID)
		}
	}
	expected := append([]core.ReusableTaskDefinition{}, before.Definitions[:deletedIndex]...)
	expected = append(expected, before.Definitions[deletedIndex+1:]...)
	if !slices.Equal(expected, after.Definitions) {
		return core.ReusableTaskCatalog{}, core.ReusableTaskCatalog{}, errors.New("catalog.after must equal catalog.before with exactly reusableTaskId removed")
	}
	return before, after, nil
}

func (p *Provider) validateReusableUpdateTaskChange(change reusableUpdateJournalChange, reusableTaskID string, beforeCatalog, afterCatalog core.ReusableTaskCatalog) (core.Task, core.Task, error) {
	if err := validateReusableUpdateSnapshot(change.Before); err != nil {
		return core.Task{}, core.Task{}, fmt.Errorf("before: %w", err)
	}
	if err := validateReusableUpdateSnapshot(change.After); err != nil {
		return core.Task{}, core.Task{}, fmt.Errorf("after: %w", err)
	}
	if !change.Before.Exists || !change.After.Exists {
		return core.Task{}, core.Task{}, errors.New("task snapshots must exist")
	}
	before, err := p.decodeReusableUpdateTaskSnapshot(change.Before.Data, "before")
	if err != nil {
		return core.Task{}, core.Task{}, err
	}
	after, err := p.decodeReusableUpdateTaskSnapshot(change.After.Data, "after")
	if err != nil {
		return core.Task{}, core.Task{}, err
	}
	if before.ID != after.ID || before.ShortID != after.ShortID {
		return core.Task{}, core.Task{}, errors.New("before and after task identities do not match")
	}
	expectedTarget := pathpkg.Join(string(before.Status), before.ShortID+".json")
	if err := p.validateReusableUpdateTarget(change.Before.Path, expectedTarget); err != nil {
		return core.Task{}, core.Task{}, fmt.Errorf("before: %w", err)
	}
	if err := p.validateReusableUpdateTarget(change.After.Path, expectedTarget); err != nil {
		return core.Task{}, core.Task{}, fmt.Errorf("after: %w", err)
	}
	if !before.UpdatedAt.Before(after.UpdatedAt) {
		return core.Task{}, core.Task{}, errors.New("after updatedAt must advance")
	}
	if !slices.Contains(before.ReusableTaskIDs, reusableTaskID) {
		return core.Task{}, core.Task{}, fmt.Errorf("before task does not reference reusableTaskId %q", reusableTaskID)
	}
	expectedIDs := detachReusableTaskID(before.ReusableTaskIDs, reusableTaskID)
	if !slices.Equal(expectedIDs, after.ReusableTaskIDs) {
		return core.Task{}, core.Task{}, errors.New("after reusableTaskIds must remove exactly reusableTaskId while preserving order")
	}
	comparison := before
	comparison.ReusableTaskIDs = after.ReusableTaskIDs
	comparison.UpdatedAt = after.UpdatedAt
	if !reflect.DeepEqual(comparison, after) {
		return core.Task{}, core.Task{}, errors.New("task snapshots may differ only in reusableTaskIds and updatedAt")
	}
	if _, err := core.ResolveReusableTasks(before.ReusableTaskIDs, beforeCatalog); err != nil {
		return core.Task{}, core.Task{}, fmt.Errorf("before task reusable references: %w", err)
	}
	if _, err := core.ResolveReusableTasks(after.ReusableTaskIDs, afterCatalog); err != nil {
		return core.Task{}, core.Task{}, fmt.Errorf("after task reusable references: %w", err)
	}
	return before, after, nil
}

func (p *Provider) decodeReusableUpdateTaskSnapshot(data []byte, label string) (core.Task, error) {
	if !utf8.Valid(data) {
		return core.Task{}, fmt.Errorf("%s task snapshot is not valid UTF-8", label)
	}
	if err := rejectReusableUpdateDuplicateProperties(data); err != nil {
		return core.Task{}, fmt.Errorf("%s task snapshot: %w", label, err)
	}
	var task core.Task
	if err := json.Unmarshal(data, &task); err != nil {
		return core.Task{}, fmt.Errorf("decode %s task snapshot: %w", label, err)
	}
	if err := task.ValidateWithCatalog(p.catalog); err != nil {
		return core.Task{}, fmt.Errorf("invalid %s task snapshot: %w", label, err)
	}
	return task, nil
}

func detachReusableTaskID(ids []string, deleted string) []string {
	updated := make([]string, 0, len(ids)-1)
	for _, id := range ids {
		if id != deleted {
			updated = append(updated, id)
		}
	}
	if len(updated) == 0 {
		return nil
	}
	return updated
}

func validateReusableUpdateSnapshot(snapshot reusableUpdateJournalSnapshot) error {
	if snapshot.Path == "" {
		return errors.New("path is required")
	}
	if !snapshot.Exists {
		if len(snapshot.Data) != 0 {
			return errors.New("absent snapshot must not contain data")
		}
		return nil
	}
	if len(snapshot.Data) == 0 {
		return errors.New("present snapshot data is required")
	}
	if !utf8.Valid(snapshot.Data) {
		return errors.New("snapshot data is not valid UTF-8")
	}
	return nil
}

func (p *Provider) validateReusableUpdateTarget(target, expected string) error {
	if target != expected {
		return fmt.Errorf("target path %q must be %q", target, expected)
	}
	if _, err := p.resolveReusableUpdateTarget(target); err != nil {
		return err
	}
	return nil
}

// validateReusableUpdateJournalForRecovery is intentionally read-only. Future
// recovery code calls it before any replacement so a stale endpoint cannot be
// mistaken for a partial transaction.
func (p *Provider) validateReusableUpdateJournalForRecovery(journal reusableUpdateJournal) error {
	if err := p.validateReusableUpdateJournal(journal); err != nil {
		return err
	}
	changes := append([]reusableUpdateJournalChange{journal.Catalog}, journal.AffectedTasks...)
	for index, change := range changes {
		if err := p.validateReusableUpdateJournalLiveChange(change); err != nil {
			return fmt.Errorf("target %d: %w", index, err)
		}
	}
	if err := p.validateReusableUpdateJournalAffectedTaskSet(journal); err != nil {
		return err
	}
	return nil
}

// validateReusableUpdateJournalAffectedTaskSet makes stale journals fail when
// a still-referencing task was omitted from the transaction. It is a strict
// read-only scan: unlike loadTasks it never repairs files or cleans residue.
func (p *Provider) validateReusableUpdateJournalAffectedTaskSet(journal reusableUpdateJournal) error {
	journalTasks := make(map[string]struct{}, len(journal.AffectedTasks))
	for _, change := range journal.AffectedTasks {
		before, err := p.decodeReusableUpdateTaskSnapshot(change.Before.Data, "journal")
		if err != nil {
			return err
		}
		journalTasks[before.ID] = struct{}{}
	}
	for _, status := range p.statuses() {
		dir := p.statusDir(status)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("read task directory %s: %w", status, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read task file %s: %w", path, err)
			}
			task, err := p.decodeReusableUpdateTaskSnapshot(data, path)
			if err != nil {
				return err
			}
			if task.Status != status {
				return fmt.Errorf("task file %s status %s does not match directory %s", path, task.Status, status)
			}
			if slices.Contains(task.ReusableTaskIDs, journal.ReusableTaskID) {
				if _, listed := journalTasks[task.ID]; !listed {
					return fmt.Errorf("task %s still references reusableTaskId %q but is not represented in journal", task.ShortID, journal.ReusableTaskID)
				}
			}
		}
	}
	return nil
}

func (p *Provider) validateReusableUpdateJournalLiveChange(change reusableUpdateJournalChange) error {
	path, err := p.resolveReusableUpdateTarget(change.Before.Path)
	if err != nil {
		return err
	}
	data, readErr := os.ReadFile(path)
	exists := readErr == nil
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", change.Before.Path, readErr)
	}
	if reusableUpdateSnapshotMatches(change.Before, exists, data) || reusableUpdateSnapshotMatches(change.After, exists, data) {
		return nil
	}
	return fmt.Errorf("target %s is stale or does not match either journal endpoint", change.Before.Path)
}

func (p *Provider) resolveReusableUpdateTarget(target string) (string, error) {
	if strings.Contains(target, "\\") || filepath.IsAbs(target) || pathpkg.IsAbs(target) || pathpkg.Clean(target) != target || strings.HasPrefix(target, "../") || target == ".." {
		return "", fmt.Errorf("target path %q is not a canonical relative path", target)
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

func reusableUpdateSnapshotMatches(snapshot reusableUpdateJournalSnapshot, exists bool, data []byte) bool {
	return snapshot.Exists == exists && (!exists || bytes.Equal(snapshot.Data, data))
}
