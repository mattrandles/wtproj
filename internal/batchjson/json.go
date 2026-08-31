// Package batchjson implements the versioned JSON representation for editable
// task batches. Its wire DTOs intentionally do not reuse core.Task: a batch
// row is a patch, not a complete task snapshot.
package batchjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
)

const Version = 1

// Encode serializes task patches as a deterministic, compact JSON document.
func Encode(tasks []core.BatchTaskUpdateInput) ([]byte, error) {
	if len(tasks) == 0 {
		return nil, errors.New("batch JSON requires at least one task")
	}

	document := batchDTO{
		Version: Version,
		Tasks:   make([]taskDTO, len(tasks)),
	}
	seen := make(map[string]int, len(tasks)*2)
	for index, task := range tasks {
		row, err := encodeTask(index, task, seen)
		if err != nil {
			return nil, err
		}
		document.Tasks[index] = row
	}

	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode batch JSON: %w", err)
	}
	return encoded, nil
}

// Marshal is an alias for Encode for callers using encoding/json terminology.
func Marshal(tasks []core.BatchTaskUpdateInput) ([]byte, error) {
	return Encode(tasks)
}

// Decode strictly parses a version 1 editable task batch.
func Decode(data []byte) ([]core.BatchTaskUpdateInput, error) {
	root, err := decodeObject(data, "batch")
	if err != nil {
		return nil, err
	}
	if err := requireOnly(root, "version", "tasks"); err != nil {
		return nil, err
	}

	version, err := decodeInt(root["version"], "version")
	if err != nil {
		return nil, err
	}
	if version != Version {
		return nil, fmt.Errorf("unsupported batch JSON version %d", version)
	}

	var rawTasks []json.RawMessage
	if err := json.Unmarshal(root["tasks"], &rawTasks); err != nil {
		return nil, fmt.Errorf("tasks must be an array: %w", err)
	}
	if len(rawTasks) == 0 {
		return nil, errors.New("batch JSON tasks must not be empty")
	}

	tasks := make([]core.BatchTaskUpdateInput, len(rawTasks))
	seen := make(map[string]int, len(rawTasks)*2)
	rowErrors := make([]error, 0)
	for index, rawTask := range rawTasks {
		task, err := decodeTask(index, rawTask, seen)
		if err != nil {
			rowErrors = append(rowErrors, err)
			continue
		}
		tasks[index] = task
	}
	if len(rowErrors) > 0 {
		return nil, errors.Join(rowErrors...)
	}
	return tasks, nil
}

// Unmarshal is an alias for Decode for callers using encoding/json terminology.
func Unmarshal(data []byte) ([]core.BatchTaskUpdateInput, error) {
	return Decode(data)
}

// Codec provides method-oriented access to the package codec.
type Codec struct{}

func (Codec) Encode(tasks []core.BatchTaskUpdateInput) ([]byte, error) {
	return Encode(tasks)
}

func (Codec) Decode(data []byte) ([]core.BatchTaskUpdateInput, error) {
	return Decode(data)
}

// The DTO field order is the public wire order. RawMessage fields are nil for
// omitted properties and contain either a JSON value or "null" when supplied.
type batchDTO struct {
	Version int       `json:"version"`
	Tasks   []taskDTO `json:"tasks"`
}

type taskDTO struct {
	ID           *string         `json:"id,omitempty"`
	ShortID      *string         `json:"shortId,omitempty"`
	UpdatedAt    string          `json:"updatedAt"`
	Title        json.RawMessage `json:"title,omitempty"`
	Description  json.RawMessage `json:"description,omitempty"`
	Status       json.RawMessage `json:"status,omitempty"`
	Priority     json.RawMessage `json:"priority,omitempty"`
	Estimate     json.RawMessage `json:"estimate,omitempty"`
	Lane         json.RawMessage `json:"lane,omitempty"`
	Model        json.RawMessage `json:"model,omitempty"`
	GitRepo      json.RawMessage `json:"gitRepo,omitempty"`
	GitBranch    json.RawMessage `json:"gitBranch,omitempty"`
	WorktreeName json.RawMessage `json:"worktreeName,omitempty"`
	WorktreeDir  json.RawMessage `json:"worktreeDir,omitempty"`
	Assignee     json.RawMessage `json:"assignee,omitempty"`
	Dependencies json.RawMessage `json:"dependencies,omitempty"`
}

func encodeTask(index int, input core.BatchTaskUpdateInput, seen map[string]int) (taskDTO, error) {
	row := taskDTO{UpdatedAt: formatTimestamp(input.ExpectedUpdatedAt)}
	if input.ExpectedUpdatedAt.IsZero() {
		return taskDTO{}, rowError(index, "updatedAt is required")
	}
	if strings.TrimSpace(input.ID) == "" && strings.TrimSpace(input.ShortID) == "" {
		return taskDTO{}, rowError(index, "id or shortId is required")
	}
	if err := recordIdentifiers(index, input.ID, input.ShortID, seen); err != nil {
		return taskDTO{}, err
	}
	if strings.TrimSpace(input.ID) != "" {
		value := input.ID
		row.ID = &value
	}
	if strings.TrimSpace(input.ShortID) != "" {
		value := input.ShortID
		row.ShortID = &value
	}

	if input.Title.Set {
		if strings.TrimSpace(input.Title.Value) == "" {
			return taskDTO{}, rowError(index, "title must not be empty when supplied")
		}
		row.Title = stringValue(input.Title.Value)
	}
	if input.Description.Set {
		row.Description = stringValue(input.Description.Value)
	}
	if input.Status.Set {
		if strings.TrimSpace(string(input.Status.Value)) == "" {
			return taskDTO{}, rowError(index, "status must not be empty when supplied")
		}
		row.Status = stringValue(string(input.Status.Value))
	}
	if input.Priority.Set {
		row.Priority = stringValue(string(input.Priority.Value))
	}
	if input.Estimate.Set {
		row.Estimate = stringValue(string(input.Estimate.Value))
	}
	if input.Lane.Set {
		row.Lane = stringValue(input.Lane.Value)
	}
	if input.Model.Set {
		row.Model = stringValue(input.Model.Value)
	}
	if input.GitRepo.Set {
		row.GitRepo = stringValue(input.GitRepo.Value)
	}
	if input.GitBranch.Set {
		row.GitBranch = stringValue(input.GitBranch.Value)
	}
	if input.WorktreeName.Set {
		row.WorktreeName = stringValue(input.WorktreeName.Value)
	}
	if input.WorktreeDir.Set {
		row.WorktreeDir = stringValue(input.WorktreeDir.Value)
	}
	if input.Assignee.Set {
		row.Assignee = stringValue(input.Assignee.Value)
	}
	if input.Dependencies.Set {
		if input.Dependencies.Value == nil {
			row.Dependencies = json.RawMessage("null")
		} else {
			row.Dependencies, _ = json.Marshal(input.Dependencies.Value)
		}
	}
	if !hasPatch(input) {
		return taskDTO{}, rowError(index, "row has no mutable patch fields")
	}
	return row, nil
}

func decodeTask(index int, raw json.RawMessage, seen map[string]int) (core.BatchTaskUpdateInput, error) {
	object, err := decodeObject(raw, fmt.Sprintf("task row %d", index+1))
	if err != nil {
		return core.BatchTaskUpdateInput{}, rowError(index, err.Error())
	}
	if err := requireOnly(object,
		"id", "shortId", "updatedAt", "title", "description", "status", "priority", "estimate",
		"lane", "model", "gitRepo", "gitBranch", "worktreeName", "worktreeDir", "assignee", "dependencies",
	); err != nil {
		return core.BatchTaskUpdateInput{}, rowError(index, err.Error())
	}

	input := core.BatchTaskUpdateInput{}
	var errField error
	if input.ID, errField = decodeIdentifier(object, "id"); errField != nil {
		return core.BatchTaskUpdateInput{}, rowError(index, errField.Error())
	}
	if input.ShortID, errField = decodeIdentifier(object, "shortId"); errField != nil {
		return core.BatchTaskUpdateInput{}, rowError(index, errField.Error())
	}
	if strings.TrimSpace(input.ID) == "" && strings.TrimSpace(input.ShortID) == "" {
		return core.BatchTaskUpdateInput{}, rowError(index, "id or shortId is required")
	}
	if err := recordIdentifiers(index, input.ID, input.ShortID, seen); err != nil {
		return core.BatchTaskUpdateInput{}, err
	}

	updatedAt, ok := object["updatedAt"]
	if !ok {
		return core.BatchTaskUpdateInput{}, rowError(index, "updatedAt is required")
	}
	input.ExpectedUpdatedAt, err = decodeTimestamp(updatedAt)
	if err != nil {
		return core.BatchTaskUpdateInput{}, rowError(index, err.Error())
	}

	if err := decodeRequiredStringPatch(object, "title", &input.Title); err != nil {
		return core.BatchTaskUpdateInput{}, rowError(index, err.Error())
	}
	if err := decodeOptionalStringPatch(object, "description", &input.Description, true); err != nil {
		return core.BatchTaskUpdateInput{}, rowError(index, err.Error())
	}
	if err := decodeStatusPatch(object, &input.Status); err != nil {
		return core.BatchTaskUpdateInput{}, rowError(index, err.Error())
	}
	if err := decodeOptionalEnumPatch(object, "priority", &input.Priority); err != nil {
		return core.BatchTaskUpdateInput{}, rowError(index, err.Error())
	}
	if err := decodeOptionalEnumPatch(object, "estimate", &input.Estimate); err != nil {
		return core.BatchTaskUpdateInput{}, rowError(index, err.Error())
	}
	for name, target := range map[string]*core.OptionalString{
		"lane": &input.Lane, "model": &input.Model, "gitRepo": &input.GitRepo, "gitBranch": &input.GitBranch,
		"worktreeName": &input.WorktreeName, "worktreeDir": &input.WorktreeDir, "assignee": &input.Assignee,
	} {
		if err := decodeOptionalStringPatch(object, name, target, true); err != nil {
			return core.BatchTaskUpdateInput{}, rowError(index, err.Error())
		}
	}
	if rawDependencies, ok := object["dependencies"]; ok {
		input.Dependencies.Set = true
		if bytes.Equal(bytes.TrimSpace(rawDependencies), []byte("null")) {
			input.Dependencies.Value = nil
		} else {
			if err := json.Unmarshal(rawDependencies, &input.Dependencies.Value); err != nil || input.Dependencies.Value == nil {
				return core.BatchTaskUpdateInput{}, rowError(index, "dependencies must be an array of identifier strings or null")
			}
			for dependencyIndex, dependency := range input.Dependencies.Value {
				if strings.TrimSpace(dependency) == "" {
					return core.BatchTaskUpdateInput{}, rowError(index, fmt.Sprintf("dependencies[%d] must not be empty", dependencyIndex))
				}
			}
		}
	}
	if !hasPatch(input) {
		return core.BatchTaskUpdateInput{}, rowError(index, "row has no mutable patch fields")
	}
	return input, nil
}

func decodeIdentifier(object map[string]json.RawMessage, name string) (string, error) {
	raw, ok := object[name]
	if !ok {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("%s must be a non-empty string", name)
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s must be a non-empty string", name)
	}
	return value, nil
}

func decodeTimestamp(raw json.RawMessage) (time.Time, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
		return time.Time{}, errors.New("updatedAt must be an RFC3339 timestamp string")
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("updatedAt is malformed: %w", err)
	}
	return parsed.UTC(), nil
}

func decodeRequiredStringPatch(object map[string]json.RawMessage, name string, target *core.OptionalString) error {
	raw, ok := object[name]
	if !ok {
		return nil
	}
	var value string
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s must be a non-empty string when supplied", name)
	}
	target.Set, target.Value = true, value
	return nil
}

func decodeStatusPatch(object map[string]json.RawMessage, target *core.OptionalStatus) error {
	raw, ok := object["status"]
	if !ok {
		return nil
	}
	var value string
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) == "" {
		return errors.New("status must be a non-empty string when supplied")
	}
	target.Set, target.Value = true, core.Status(value)
	return nil
}

func decodeOptionalStringPatch(object map[string]json.RawMessage, name string, target *core.OptionalString, allowNull bool) error {
	raw, ok := object[name]
	if !ok {
		return nil
	}
	target.Set = true
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if !allowNull {
			return fmt.Errorf("%s must not be null", name)
		}
		target.Value = ""
		return nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("%s must be a string or null", name)
	}
	target.Value = value
	return nil
}

func decodeOptionalEnumPatch(object map[string]json.RawMessage, name string, target any) error {
	raw, ok := object[name]
	if !ok {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		switch value := target.(type) {
		case *core.OptionalPriority:
			value.Set, value.Value = true, ""
		case *core.OptionalEstimate:
			value.Set, value.Value = true, ""
		}
		return nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("%s must be a string or null", name)
	}
	switch target := target.(type) {
	case *core.OptionalPriority:
		target.Set, target.Value = true, core.Priority(value)
	case *core.OptionalEstimate:
		target.Set, target.Value = true, core.Estimate(value)
	default:
		return fmt.Errorf("unsupported enum field %s", name)
	}
	return nil
}

func decodeInt(raw json.RawMessage, name string) (int, error) {
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return value, nil
}

func decodeObject(data []byte, name string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", name, err)
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return nil, fmt.Errorf("%s must be an object", name)
	}

	object := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("decode %s property: %w", name, err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("decode %s property name", name)
		}
		if _, exists := object[key]; exists {
			return nil, fmt.Errorf("%s contains duplicate property %q", name, key)
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
	for name := range object {
		if _, ok := set[name]; !ok {
			return fmt.Errorf("unknown property %q", name)
		}
	}
	for _, name := range allowed {
		if _, ok := object[name]; !ok && (name == "version" || name == "tasks") {
			return fmt.Errorf("missing required property %q", name)
		}
	}
	return nil
}

func recordIdentifiers(index int, id, shortID string, seen map[string]int) error {
	identifiers := []struct{ kind, value string }{{"id", id}, {"shortId", shortID}}
	for _, identifier := range identifiers {
		if strings.TrimSpace(identifier.value) == "" {
			continue
		}
		key := identifier.kind + "\x00" + identifier.value
		if previous, exists := seen[key]; exists {
			return rowError(index, fmt.Sprintf("duplicate %s %q from row %d", identifier.kind, identifier.value, previous+1))
		}
		seen[key] = index
	}
	return nil
}

func hasPatch(input core.BatchTaskUpdateInput) bool {
	return input.Title.Set || input.Description.Set || input.Status.Set || input.Priority.Set || input.Estimate.Set ||
		input.Lane.Set || input.Model.Set || input.GitRepo.Set || input.GitBranch.Set || input.WorktreeName.Set ||
		input.WorktreeDir.Set || input.Assignee.Set || input.Dependencies.Set
}

func stringValue(value string) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

func formatTimestamp(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func rowError(index int, message string) error {
	return fmt.Errorf("task row %d: %s", index+1, message)
}
