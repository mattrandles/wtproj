// Package planningjson implements the strict JSON representation of one
// planning record. The record deliberately uses the same wire fields and
// order as core.Task; only the lifecycle status has a separate core type.
package planningjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"unicode/utf8"

	"github.com/mattrandles/wtproj/internal/core"
)

var recordFields = []string{
	"id", "shortId", "title", "description", "priority", "estimate", "lane", "model",
	"issueId", "project", "milestone", "version", "featureId", "feature", "gitRepo",
	"gitBranch", "worktreeName", "worktreeDir", "status", "assignee", "dependencies",
	"comments", "createdAt", "updatedAt", "startedAt", "completedAt", "reusableTaskIds",
}

var requiredRecordFields = []string{
	"id", "shortId", "title", "description", "status", "dependencies", "comments",
	"createdAt", "updatedAt", "startedAt", "completedAt",
}

var recordStringFields = []string{
	"id", "shortId", "title", "description", "priority", "estimate", "lane", "model",
	"issueId", "project", "milestone", "version", "featureId", "feature", "gitRepo",
	"gitBranch", "worktreeName", "worktreeDir", "status", "assignee", "createdAt", "updatedAt",
}

var commentFields = []string{"id", "author", "message", "createdAt"}

// Encode validates and serializes a planning record as compact, deterministic
// UTF-8 JSON. Struct declaration order supplies the established field order
// and the core tags supply the established omitempty behavior.
func Encode(item core.PlanningItem) ([]byte, error) {
	if err := item.Validate(); err != nil {
		return nil, err
	}
	if err := validateUTF8(item); err != nil {
		return nil, err
	}
	data, err := json.Marshal(item)
	if err != nil {
		return nil, fmt.Errorf("encode planning record: %w", err)
	}
	return data, nil
}

// Marshal is an alias for Encode for callers using encoding/json terminology.
func Marshal(item core.PlanningItem) ([]byte, error) { return Encode(item) }

// Decode strictly parses one planning record. Unknown and duplicate
// properties are rejected, as are non-null execution timestamps, malformed
// nested comments, invalid status values, and all core metadata violations.
func Decode(data []byte) (core.PlanningItem, error) {
	if !utf8.Valid(data) {
		return core.PlanningItem{}, errors.New("planning record is not valid UTF-8")
	}
	object, err := decodeObject(data, "planning record")
	if err != nil {
		return core.PlanningItem{}, err
	}
	if err := requireOnly(object, recordFields...); err != nil {
		return core.PlanningItem{}, err
	}
	for _, name := range requiredRecordFields {
		if _, ok := object[name]; !ok {
			return core.PlanningItem{}, fmt.Errorf("planning record is missing required property %q", name)
		}
	}
	for _, name := range recordStringFields {
		if raw, ok := object[name]; ok {
			if _, err := decodeString(raw, name); err != nil {
				return core.PlanningItem{}, err
			}
		}
	}
	if err := requireArray(object["dependencies"], "dependencies"); err != nil {
		return core.PlanningItem{}, err
	}
	if err := validateComments(object["comments"]); err != nil {
		return core.PlanningItem{}, err
	}
	if raw, ok := object["reusableTaskIds"]; ok {
		if err := requireArray(raw, "reusableTaskIds"); err != nil {
			return core.PlanningItem{}, err
		}
	}
	for _, name := range []string{"startedAt", "completedAt"} {
		if !bytes.Equal(bytes.TrimSpace(object[name]), []byte("null")) {
			return core.PlanningItem{}, fmt.Errorf("planning record %s must be null", name)
		}
	}

	var item core.PlanningItem
	if err := json.Unmarshal(data, &item); err != nil {
		return core.PlanningItem{}, fmt.Errorf("decode planning record: %w", err)
	}
	if err := item.Validate(); err != nil {
		return core.PlanningItem{}, err
	}
	if err := validateUTF8(item); err != nil {
		return core.PlanningItem{}, err
	}
	return item, nil
}

// Unmarshal is an alias for Decode for callers using encoding/json
// terminology.
func Unmarshal(data []byte) (core.PlanningItem, error) { return Decode(data) }

// ReadFile reads and strictly decodes a planning record. Unlike an absent
// reusable catalog, an absent planning record is an error.
func ReadFile(path string) (core.PlanningItem, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return core.PlanningItem{}, fmt.Errorf("read planning record %s: %w", path, err)
	}
	item, err := Decode(data)
	if err != nil {
		return core.PlanningItem{}, fmt.Errorf("decode planning record %s: %w", path, err)
	}
	return item, nil
}

// WriteFile writes codec bytes to path. Atomic publication is owned by the
// flat-file provider, just as it is for the reusable catalog codec.
func WriteFile(path string, item core.PlanningItem) error {
	data, err := Encode(item)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent for planning record %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write planning record %s: %w", path, err)
	}
	return nil
}

// Codec provides method-oriented access to the package codec.
type Codec struct{}

func (Codec) Encode(item core.PlanningItem) ([]byte, error)   { return Encode(item) }
func (Codec) Decode(data []byte) (core.PlanningItem, error)   { return Decode(data) }
func (Codec) ReadFile(path string) (core.PlanningItem, error) { return ReadFile(path) }
func (Codec) WriteFile(path string, item core.PlanningItem) error {
	return WriteFile(path, item)
}

func validateUTF8(item core.PlanningItem) error {
	values := []struct {
		name  string
		value string
	}{
		{"id", item.ID}, {"shortId", item.ShortID}, {"title", item.Title}, {"description", item.Description},
		{"lane", item.Lane}, {"model", item.Model}, {"issueId", item.IssueID}, {"project", item.Project},
		{"milestone", item.Milestone}, {"version", item.Version}, {"featureId", item.FeatureID}, {"feature", item.Feature},
		{"gitRepo", item.GitRepo}, {"gitBranch", item.GitBranch}, {"worktreeName", item.WorktreeName}, {"worktreeDir", item.WorktreeDir},
		{"status", string(item.Status)}, {"assignee", item.Assignee},
	}
	for _, field := range values {
		if !utf8.ValidString(field.value) {
			return fmt.Errorf("planning record %s contains invalid UTF-8", field.name)
		}
	}
	for index, dependency := range item.Dependencies {
		if !utf8.ValidString(dependency) {
			return fmt.Errorf("planning record dependency %d contains invalid UTF-8", index)
		}
	}
	for index, comment := range item.Comments {
		for _, field := range []struct {
			name  string
			value string
		}{{"id", comment.ID}, {"author", comment.Author}, {"message", comment.Message}} {
			if !utf8.ValidString(field.value) {
				return fmt.Errorf("planning record comment %d %s contains invalid UTF-8", index, field.name)
			}
		}
	}
	for index, id := range item.ReusableTaskIDs {
		if !utf8.ValidString(id) {
			return fmt.Errorf("planning record reusableTaskIds %d contains invalid UTF-8", index)
		}
	}
	return nil
}

func validateComments(raw json.RawMessage) error {
	if err := requireArray(raw, "comments"); err != nil {
		return err
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return fmt.Errorf("comments must be an array: %w", err)
	}
	for index, rawItem := range items {
		label := fmt.Sprintf("planning record comment %d", index)
		object, err := decodeObject(rawItem, label)
		if err != nil {
			return err
		}
		if err := requireOnly(object, commentFields...); err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		for _, name := range commentFields {
			if _, ok := object[name]; !ok {
				return fmt.Errorf("%s is missing required property %q", label, name)
			}
			if _, err := decodeString(object[name], name); err != nil {
				return fmt.Errorf("%s: %w", label, err)
			}
		}
	}
	return nil
}

func requireArray(raw json.RawMessage, name string) error {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("%s must be an array", name)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || items == nil {
		if err == nil {
			err = errors.New("null")
		}
		return fmt.Errorf("%s must be an array: %w", name, err)
	}
	return nil
}

func decodeString(raw json.RawMessage, name string) (string, error) {
	var value string
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil {
		return "", fmt.Errorf("%s must be a string", name)
	}
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("%s is not valid UTF-8", name)
	}
	return value, nil
}

func decodeObject(data []byte, name string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", name, err)
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return nil, fmt.Errorf("decode %s: expected object", name)
	}
	object := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("decode %s: expected string property name", name)
		}
		if _, exists := object[key]; exists {
			return nil, fmt.Errorf("decode %s: contains duplicate property %q", name, key)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("decode %s property %q: %w", name, key, err)
		}
		object[key] = value
	}
	closing, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", name, err)
	}
	if delimiter, ok := closing.(json.Delim); !ok || delimiter != '}' {
		return nil, fmt.Errorf("decode %s: expected closing object", name)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("decode %s: trailing JSON data", name)
		}
		return nil, fmt.Errorf("decode %s: %w", name, err)
	}
	return object, nil
}

func requireOnly(object map[string]json.RawMessage, allowed ...string) error {
	set := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		set[name] = struct{}{}
	}
	unknown := make([]string, 0)
	for name := range object {
		if _, ok := set[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("unknown property %q", unknown[0])
	}
	return nil
}
