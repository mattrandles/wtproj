// Package batchcsv implements the CSV representation for editable task
// batches. A row is a patch, so an empty editable cell preserves the stored
// value; the _clear column makes supported empty values explicit.
package batchcsv

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattrandles/wtproj/internal/core"
)

var headerNames = [...]string{
	"id", "shortId", "updatedAt", "title", "description", "status", "priority", "estimate",
	"lane", "model", "issueId", "project", "milestone", "version", "featureId", "feature",
	"gitRepo", "gitBranch", "worktreeName", "worktreeDir", "assignee", "dependencies", "reusableTasks", "_clear",
}

var clearableFields = [...]string{
	"description", "priority", "estimate", "lane", "model", "issueId", "project", "milestone", "version",
	"featureId", "feature", "gitRepo", "gitBranch", "worktreeName", "worktreeDir", "assignee", "dependencies", "reusableTasks",
}

var requiredFields = map[string]struct{}{
	"id": {}, "shortId": {}, "updatedAt": {}, "title": {}, "status": {},
}

// Encode serializes task patches with the deterministic CSV header and LF
// line endings. The encoding is UTF-8 without a byte-order mark.
func Encode(tasks []core.BatchTaskUpdateInput) ([]byte, error) {
	if len(tasks) == 0 {
		return nil, errors.New("batch CSV requires at least one task")
	}

	records := make([][]string, len(tasks)+1)
	records[0] = append([]string(nil), headerNames[:]...)
	seen := make(map[string]int, len(tasks)*2)
	for index, task := range tasks {
		record, err := encodeTask(index, task, seen)
		if err != nil {
			return nil, err
		}
		records[index+1] = record
	}

	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	for _, record := range records {
		if err := writer.Write(record); err != nil {
			return nil, fmt.Errorf("encode batch CSV: %w", err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, fmt.Errorf("encode batch CSV: %w", err)
	}
	return output.Bytes(), nil
}

// Marshal is an alias for Encode for callers using encoding terminology.
func Marshal(tasks []core.BatchTaskUpdateInput) ([]byte, error) { return Encode(tasks) }

// Decode strictly parses an editable task batch. The header may contain a
// subset of editable columns, but it must contain updatedAt and at least one
// identifier column.
func Decode(data []byte) ([]core.BatchTaskUpdateInput, error) {
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		data = data[3:]
	}
	if !utf8.Valid(data) {
		return nil, errors.New("batch CSV is not valid UTF-8")
	}

	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if errors.Is(err, io.EOF) {
		return nil, errors.New("batch CSV is missing its header")
	}
	if err != nil {
		return nil, fmt.Errorf("decode batch CSV header: %w", err)
	}
	positions, err := validateHeader(header)
	if err != nil {
		return nil, err
	}

	var tasks []core.BatchTaskUpdateInput
	seen := make(map[string]int)
	rowErrors := make([]error, 0)
	rowNumber := 0
	for {
		record, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		rowNumber++
		if readErr != nil {
			rowErrors = append(rowErrors, rowError(rowNumber, fmt.Sprintf("malformed CSV: %v", readErr)))
			continue
		}
		if len(record) != len(header) {
			rowErrors = append(rowErrors, rowError(rowNumber, fmt.Sprintf("ragged row has %d fields, want %d", len(record), len(header))))
			continue
		}
		task, err := decodeTask(rowNumber, record, positions, seen)
		if err != nil {
			rowErrors = append(rowErrors, err)
			continue
		}
		tasks = append(tasks, task)
	}
	if len(rowErrors) > 0 {
		return nil, errors.Join(rowErrors...)
	}
	if len(tasks) == 0 {
		return nil, errors.New("batch CSV must contain at least one task row")
	}
	return tasks, nil
}

// Unmarshal is an alias for Decode for callers using encoding terminology.
func Unmarshal(data []byte) ([]core.BatchTaskUpdateInput, error) { return Decode(data) }

// Codec provides method-oriented access to the package codec.
type Codec struct{}

func (Codec) Encode(tasks []core.BatchTaskUpdateInput) ([]byte, error) { return Encode(tasks) }
func (Codec) Decode(data []byte) ([]core.BatchTaskUpdateInput, error)  { return Decode(data) }

func encodeTask(index int, input core.BatchTaskUpdateInput, seen map[string]int) ([]string, error) {
	if input.ExpectedUpdatedAt.IsZero() {
		return nil, rowError(index+1, "updatedAt is required")
	}
	if strings.TrimSpace(input.ID) == "" && strings.TrimSpace(input.ShortID) == "" {
		return nil, rowError(index+1, "id or shortId is required")
	}
	if err := recordIdentifiers(index+1, input.ID, input.ShortID, seen); err != nil {
		return nil, err
	}

	record := make([]string, len(headerNames))
	record[0] = input.ID
	record[1] = input.ShortID
	record[2] = formatTimestamp(input.ExpectedUpdatedAt)
	clears := make([]string, 0, len(clearableFields))

	if input.Title.Set {
		if strings.TrimSpace(input.Title.Value) == "" {
			return nil, rowError(index+1, "title must not be empty when supplied")
		}
		record[3] = input.Title.Value
	}
	if err := encodeOptionalString(record, 4, "description", input.Description, &clears); err != nil {
		return nil, rowError(index+1, err.Error())
	}
	if input.Status.Set {
		if strings.TrimSpace(string(input.Status.Value)) == "" {
			return nil, rowError(index+1, "status must not be empty when supplied")
		}
		record[5] = string(input.Status.Value)
	}
	if err := encodeOptionalEnum(record, 6, "priority", string(input.Priority.Value), input.Priority.Set, &clears); err != nil {
		return nil, rowError(index+1, err.Error())
	}
	if err := encodeOptionalEnum(record, 7, "estimate", string(input.Estimate.Value), input.Estimate.Set, &clears); err != nil {
		return nil, rowError(index+1, err.Error())
	}
	optionalStrings := []struct {
		position int
		name     string
		value    core.OptionalString
	}{
		{8, "lane", input.Lane}, {9, "model", input.Model}, {10, "issueId", input.IssueID},
		{11, "project", input.Project}, {12, "milestone", input.Milestone}, {13, "version", input.Version},
		{14, "featureId", input.FeatureID}, {15, "feature", input.Feature}, {16, "gitRepo", input.GitRepo},
		{17, "gitBranch", input.GitBranch}, {18, "worktreeName", input.WorktreeName},
		{19, "worktreeDir", input.WorktreeDir}, {20, "assignee", input.Assignee},
	}
	for _, field := range optionalStrings {
		if err := encodeOptionalString(record, field.position, field.name, field.value, &clears); err != nil {
			return nil, rowError(index+1, err.Error())
		}
	}
	if input.Dependencies.Set {
		if len(input.Dependencies.Value) == 0 {
			clears = append(clears, "dependencies")
		} else {
			dependencies := make([]string, len(input.Dependencies.Value))
			for depIndex, dependency := range input.Dependencies.Value {
				if strings.TrimSpace(dependency) == "" {
					return nil, rowError(index+1, fmt.Sprintf("dependencies[%d] must not be empty", depIndex))
				}
				dependencies[depIndex] = strings.TrimSpace(dependency)
			}
			record[21] = strings.Join(dependencies, ",")
		}
	}
	if input.ReusableTasks.Set {
		if len(input.ReusableTasks.Value) == 0 {
			clears = append(clears, "reusableTasks")
		} else {
			reusableTasks := make([]string, len(input.ReusableTasks.Value))
			seenReusableTasks := make(map[string]struct{}, len(input.ReusableTasks.Value))
			for reusableIndex, reusableTask := range input.ReusableTasks.Value {
				reusableTask = strings.TrimSpace(reusableTask)
				if reusableTask == "" {
					return nil, rowError(index+1, fmt.Sprintf("reusableTasks[%d] must not be empty", reusableIndex))
				}
				if _, duplicate := seenReusableTasks[reusableTask]; duplicate {
					return nil, rowError(index+1, fmt.Sprintf("reusableTasks[%d] duplicates identifier %q", reusableIndex, reusableTask))
				}
				seenReusableTasks[reusableTask] = struct{}{}
				reusableTasks[reusableIndex] = reusableTask
			}
			record[22] = strings.Join(reusableTasks, ",")
		}
	}
	if len(clears) > 0 {
		record[23] = strings.Join(clears, ",")
	}
	if !hasPatch(input) {
		return nil, rowError(index+1, "row has no mutable patch fields")
	}
	for fieldIndex, field := range record {
		if !utf8.ValidString(field) {
			return nil, rowError(index+1, fmt.Sprintf("field %q is not valid UTF-8", headerNames[fieldIndex]))
		}
	}
	return record, nil
}

func encodeOptionalString(record []string, position int, name string, value core.OptionalString, clears *[]string) error {
	if !value.Set {
		return nil
	}
	record[position] = value.Value
	if value.Value == "" {
		*clears = append(*clears, name)
	}
	return nil
}

func encodeOptionalEnum(record []string, position int, name, value string, set bool, clears *[]string) error {
	if !set {
		return nil
	}
	record[position] = value
	if value == "" {
		*clears = append(*clears, name)
	}
	return nil
}

func decodeTask(rowNumber int, record []string, positions map[string]int, seen map[string]int) (core.BatchTaskUpdateInput, error) {
	value := func(name string) string {
		if position, ok := positions[name]; ok {
			return record[position]
		}
		return ""
	}

	task := core.BatchTaskUpdateInput{ID: value("id"), ShortID: value("shortId")}
	if task.ID != "" && strings.TrimSpace(task.ID) == "" {
		return core.BatchTaskUpdateInput{}, rowError(rowNumber, "id must not be blank when supplied")
	}
	if task.ShortID != "" && strings.TrimSpace(task.ShortID) == "" {
		return core.BatchTaskUpdateInput{}, rowError(rowNumber, "shortId must not be blank when supplied")
	}
	if strings.TrimSpace(task.ID) == "" && strings.TrimSpace(task.ShortID) == "" {
		return core.BatchTaskUpdateInput{}, rowError(rowNumber, "id or shortId is required")
	}
	if err := recordIdentifiers(rowNumber, task.ID, task.ShortID, seen); err != nil {
		return core.BatchTaskUpdateInput{}, err
	}
	updatedAt := value("updatedAt")
	if strings.TrimSpace(updatedAt) == "" {
		return core.BatchTaskUpdateInput{}, rowError(rowNumber, "updatedAt is required")
	}
	parsed, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return core.BatchTaskUpdateInput{}, rowError(rowNumber, fmt.Sprintf("updatedAt is malformed: %v", err))
	}
	task.ExpectedUpdatedAt = parsed.UTC()

	if title := value("title"); title != "" {
		if strings.TrimSpace(title) == "" {
			return core.BatchTaskUpdateInput{}, rowError(rowNumber, "title must not be empty when supplied")
		}
		task.Title = core.OptionalString{Set: true, Value: title}
	}
	if description := value("description"); description != "" {
		task.Description = core.OptionalString{Set: true, Value: description}
	}
	if status := value("status"); status != "" {
		if strings.TrimSpace(status) == "" {
			return core.BatchTaskUpdateInput{}, rowError(rowNumber, "status must not be empty when supplied")
		}
		task.Status = core.OptionalStatus{Set: true, Value: core.Status(status)}
	}
	if priority := value("priority"); priority != "" {
		task.Priority = core.OptionalPriority{Set: true, Value: core.Priority(priority)}
	}
	if estimate := value("estimate"); estimate != "" {
		task.Estimate = core.OptionalEstimate{Set: true, Value: core.Estimate(estimate)}
	}
	for _, field := range []struct {
		name   string
		target *core.OptionalString
	}{
		{"lane", &task.Lane}, {"model", &task.Model}, {"issueId", &task.IssueID}, {"project", &task.Project},
		{"milestone", &task.Milestone}, {"version", &task.Version}, {"featureId", &task.FeatureID}, {"feature", &task.Feature},
		{"gitRepo", &task.GitRepo}, {"gitBranch", &task.GitBranch}, {"worktreeName", &task.WorktreeName},
		{"worktreeDir", &task.WorktreeDir}, {"assignee", &task.Assignee},
	} {
		if fieldValue := value(field.name); fieldValue != "" {
			field.target.Set, field.target.Value = true, fieldValue
		}
	}
	if dependencies := value("dependencies"); dependencies != "" {
		parts := strings.Split(dependencies, ",")
		task.Dependencies.Set = true
		task.Dependencies.Value = make([]string, len(parts))
		for depIndex, dependency := range parts {
			dependency = strings.TrimSpace(dependency)
			if dependency == "" {
				return core.BatchTaskUpdateInput{}, rowError(rowNumber, fmt.Sprintf("dependencies[%d] must not be empty", depIndex))
			}
			task.Dependencies.Value[depIndex] = dependency
		}
	}
	if reusableTasks := value("reusableTasks"); reusableTasks != "" {
		parts := strings.Split(reusableTasks, ",")
		task.ReusableTasks.Set = true
		task.ReusableTasks.Value = make([]string, len(parts))
		seenReusableTasks := make(map[string]struct{}, len(parts))
		for reusableIndex, reusableTask := range parts {
			reusableTask = strings.TrimSpace(reusableTask)
			if reusableTask == "" {
				return core.BatchTaskUpdateInput{}, rowError(rowNumber, fmt.Sprintf("reusableTasks[%d] must not be empty", reusableIndex))
			}
			if _, duplicate := seenReusableTasks[reusableTask]; duplicate {
				return core.BatchTaskUpdateInput{}, rowError(rowNumber, fmt.Sprintf("reusableTasks[%d] duplicates identifier %q", reusableIndex, reusableTask))
			}
			seenReusableTasks[reusableTask] = struct{}{}
			task.ReusableTasks.Value[reusableIndex] = reusableTask
		}
	}

	clearNames, err := parseClearList(value("_clear"))
	if err != nil {
		return core.BatchTaskUpdateInput{}, rowError(rowNumber, err.Error())
	}
	for _, name := range clearNames {
		if value(name) != "" {
			return core.BatchTaskUpdateInput{}, rowError(rowNumber, fmt.Sprintf("field %q is both nonblank and listed for clearing", name))
		}
		setClear(&task, name)
	}
	if !hasPatch(task) {
		return core.BatchTaskUpdateInput{}, rowError(rowNumber, "row has no mutable patch fields")
	}
	return task, nil
}

func validateHeader(header []string) (map[string]int, error) {
	allowed := make(map[string]struct{}, len(headerNames))
	for _, name := range headerNames {
		allowed[name] = struct{}{}
	}
	positions := make(map[string]int, len(header))
	for position, name := range header {
		if _, ok := allowed[name]; !ok {
			return nil, fmt.Errorf("unknown CSV header %q", name)
		}
		if _, exists := positions[name]; exists {
			return nil, fmt.Errorf("duplicate CSV header %q", name)
		}
		positions[name] = position
	}
	if _, ok := positions["updatedAt"]; !ok {
		return nil, errors.New("missing required CSV header \"updatedAt\"")
	}
	if _, hasID := positions["id"]; !hasID {
		if _, hasShortID := positions["shortId"]; !hasShortID {
			return nil, errors.New("missing required CSV header \"id\" or \"shortId\"")
		}
	}
	return positions, nil
}

func parseClearList(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	clearable := make(map[string]struct{}, len(clearableFields))
	for _, name := range clearableFields {
		clearable[name] = struct{}{}
	}
	seen := make(map[string]struct{})
	parts := strings.Split(value, ",")
	clears := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			return nil, errors.New("_clear contains an empty field name")
		}
		if _, ok := clearable[name]; !ok {
			if _, required := requiredFields[name]; required {
				return nil, fmt.Errorf("_clear cannot clear required field %q", name)
			}
			return nil, fmt.Errorf("_clear contains unknown field %q", name)
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("_clear contains duplicate field %q", name)
		}
		seen[name] = struct{}{}
		clears = append(clears, name)
	}
	return clears, nil
}

func setClear(task *core.BatchTaskUpdateInput, name string) {
	switch name {
	case "description":
		task.Description = core.OptionalString{Set: true}
	case "priority":
		task.Priority = core.OptionalPriority{Set: true}
	case "estimate":
		task.Estimate = core.OptionalEstimate{Set: true}
	case "lane":
		task.Lane = core.OptionalString{Set: true}
	case "model":
		task.Model = core.OptionalString{Set: true}
	case "issueId":
		task.IssueID = core.OptionalString{Set: true}
	case "project":
		task.Project = core.OptionalString{Set: true}
	case "milestone":
		task.Milestone = core.OptionalString{Set: true}
	case "version":
		task.Version = core.OptionalString{Set: true}
	case "featureId":
		task.FeatureID = core.OptionalString{Set: true}
	case "feature":
		task.Feature = core.OptionalString{Set: true}
	case "gitRepo":
		task.GitRepo = core.OptionalString{Set: true}
	case "gitBranch":
		task.GitBranch = core.OptionalString{Set: true}
	case "worktreeName":
		task.WorktreeName = core.OptionalString{Set: true}
	case "worktreeDir":
		task.WorktreeDir = core.OptionalString{Set: true}
	case "assignee":
		task.Assignee = core.OptionalString{Set: true}
	case "dependencies":
		task.Dependencies = core.OptionalStrings{Set: true}
	case "reusableTasks":
		task.ReusableTasks = core.OptionalStrings{Set: true}
	}
}

func recordIdentifiers(rowNumber int, id, shortID string, seen map[string]int) error {
	for _, identifier := range []struct {
		kind  string
		value string
	}{{"id", id}, {"shortId", shortID}} {
		if strings.TrimSpace(identifier.value) == "" {
			continue
		}
		key := identifier.kind + "\x00" + identifier.value
		if previous, exists := seen[key]; exists {
			return rowError(rowNumber, fmt.Sprintf("duplicate %s %q from row %d", identifier.kind, identifier.value, previous))
		}
		seen[key] = rowNumber
	}
	return nil
}

func hasPatch(input core.BatchTaskUpdateInput) bool {
	return input.Title.Set || input.Description.Set || input.Status.Set || input.Priority.Set || input.Estimate.Set ||
		input.Lane.Set || input.Model.Set || input.IssueID.Set || input.Project.Set || input.Milestone.Set || input.Version.Set ||
		input.FeatureID.Set || input.Feature.Set || input.GitRepo.Set || input.GitBranch.Set || input.WorktreeName.Set ||
		input.WorktreeDir.Set || input.Assignee.Set || input.Dependencies.Set || input.ReusableTasks.Set
}

func formatTimestamp(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func rowError(rowNumber int, message string) error {
	return fmt.Errorf("task row %d: %s", rowNumber, message)
}
