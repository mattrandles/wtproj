// Package batchexport selects tasks and writes an editable batch through the
// batch JSON and CSV codecs. It deliberately does not parse CLI arguments or
// print human-facing output.
package batchexport

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattrandles/wtproj/internal/batchcsv"
	"github.com/mattrandles/wtproj/internal/batchjson"
	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
)

// Format is an editable batch wire format.
type Format string

const (
	FormatJSON Format = "json"
	FormatCSV  Format = "csv"
	JSON       Format = FormatJSON
	CSV        Format = FormatCSV
)

// Options describes one export. Status is optional; when it is present it
// must name a status in the provider's configured catalog. TaskIDs are
// resolved against one complete task graph and retain their caller order.
// Omitting both selectors exports every task.
type Options struct {
	// Destination is a file path or "-" for stdout. Output and Out are
	// compatibility aliases for callers at the command boundary; only one
	// distinct destination may be supplied.
	Destination string
	Output      string
	Out         string

	Format  Format
	Status  string
	TaskIDs []string
	// Stdout is used when Destination is "-". Export also accepts a writer
	// argument so a command layer need not mutate Options.
	Stdout io.Writer
}

// Request is an alias for callers that prefer request-oriented naming.
type Request = Options

// Result is metadata for the CLI layer. The service never prints this value.
type Result struct {
	Count       int    `json:"count"`
	Format      Format `json:"format"`
	Destination string `json:"destination"`
}

// Service binds export operations to one provider.
type Service struct {
	Provider provider.Provider
}

var (
	atomicReplace       = replaceFile
	atomicSyncDirectory = syncDirectory
)

// New returns an export service for p.
func New(p provider.Provider) *Service { return &Service{Provider: p} }

// Export selects, encodes, and writes one editable batch. For stdout output,
// the optional writer takes precedence over Options.Stdout. File output is
// published with a same-directory temporary file and replacement.
func Export(p provider.Provider, options Options, stdout ...io.Writer) (Result, error) {
	return New(p).Export(options, stdout...)
}

// Run is a descriptive alias for Export.
func Run(p provider.Provider, options Options, stdout ...io.Writer) (Result, error) {
	return Export(p, options, stdout...)
}

// Export performs one export using the service's provider.
func (s *Service) Export(options Options, stdout ...io.Writer) (Result, error) {
	if s == nil || s.Provider == nil {
		return Result{}, errors.New("batch export provider is nil")
	}
	destination, err := options.destination()
	if err != nil {
		return Result{}, err
	}
	format, err := ResolveFormat(destination, options.Format)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(options.Status) != "" && len(options.TaskIDs) > 0 {
		return Result{}, errors.New("batch export status and task selectors cannot be combined")
	}

	tasks, graph, err := selectTasks(s.Provider, options)
	if err != nil {
		return Result{}, err
	}
	inputs, err := editableInputs(tasks, graph)
	if err != nil {
		return Result{}, err
	}
	data, err := encode(format, inputs)
	if err != nil {
		return Result{}, err
	}

	result := Result{Count: len(tasks), Format: format, Destination: destination}
	if destination == "-" {
		writer := options.Stdout
		if len(stdout) > 0 {
			writer = stdout[0]
		}
		if writer == nil {
			return Result{}, errors.New("batch export stdout writer is required")
		}
		if err := writeAll(writer, data); err != nil {
			return Result{}, fmt.Errorf("write batch export stdout: %w", err)
		}
		return result, nil
	}
	if err := writeAtomic(destination, data); err != nil {
		return Result{}, err
	}
	return result, nil
}

// ResolveFormat validates an explicit format or infers one from .csv/.json.
// Stdout and unknown extensions require an explicit format.
func ResolveFormat(destination string, explicit Format) (Format, error) {
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return "", errors.New("batch export destination is required")
	}
	if explicit != "" {
		format := Format(strings.ToLower(strings.TrimSpace(string(explicit))))
		if format != FormatCSV && format != FormatJSON {
			return "", fmt.Errorf("unsupported batch export format %q", explicit)
		}
		return format, nil
	}
	if destination == "-" {
		return "", errors.New("batch export format is required for stdout")
	}
	switch strings.ToLower(filepath.Ext(destination)) {
	case ".csv":
		return FormatCSV, nil
	case ".json":
		return FormatJSON, nil
	default:
		return "", errors.New("batch export format is required for an unknown file extension")
	}
}

func (o Options) destination() (string, error) {
	values := []struct {
		name  string
		value string
	}{
		{"destination", o.Destination}, {"output", o.Output}, {"out", o.Out},
	}
	chosen := ""
	chosenName := ""
	for _, item := range values {
		value := strings.TrimSpace(item.value)
		if value == "" {
			continue
		}
		if chosen == "" {
			chosen, chosenName = value, item.name
			continue
		}
		if chosen != value {
			return "", fmt.Errorf("batch export %s conflicts with %s", item.name, chosenName)
		}
	}
	if chosen == "" {
		return "", errors.New("batch export destination is required")
	}
	return chosen, nil
}

func selectTasks(p provider.Provider, options Options) ([]core.TaskView, []core.TaskView, error) {
	catalog := p.StatusCatalog()
	if len(catalog.Statuses()) == 0 {
		catalog = core.DefaultStatusCatalog()
	}
	if statusText := strings.TrimSpace(options.Status); statusText != "" {
		status, err := catalog.ParseStatus(statusText)
		if err != nil {
			return nil, nil, err
		}
		tasks, err := p.ListTasks(provider.TaskFilter{Status: &status})
		if err != nil {
			return nil, nil, fmt.Errorf("list tasks for batch export: %w", err)
		}
		graph, err := p.ListTasks(provider.TaskFilter{})
		if err != nil {
			return nil, nil, fmt.Errorf("load task graph for batch export: %w", err)
		}
		return tasks, graph, nil
	}

	allTasks, err := p.ListTasks(provider.TaskFilter{})
	if err != nil {
		return nil, nil, fmt.Errorf("list tasks for batch export: %w", err)
	}
	if len(options.TaskIDs) == 0 {
		return allTasks, allTasks, nil
	}

	byID, byShortID, err := graphIndexes(allTasks)
	if err != nil {
		return nil, nil, err
	}

	selected := make([]core.TaskView, 0, len(options.TaskIDs))
	seen := make(map[string]string, len(options.TaskIDs))
	for index, rawID := range options.TaskIDs {
		identifier := strings.TrimSpace(rawID)
		if identifier == "" {
			return nil, nil, fmt.Errorf("batch export task selector %d is blank", index+1)
		}
		task, ok := byID[identifier]
		if !ok {
			task, ok = byShortID[identifier]
		}
		if !ok {
			return nil, nil, fmt.Errorf("batch export task %q not found", identifier)
		}
		if previous, duplicate := seen[task.ID]; duplicate {
			return nil, nil, fmt.Errorf("batch export task selector %q duplicates %q", identifier, previous)
		}
		seen[task.ID] = identifier
		selected = append(selected, task)
	}
	return selected, allTasks, nil
}

func graphIndexes(tasks []core.TaskView) (map[string]core.TaskView, map[string]core.TaskView, error) {
	byID := make(map[string]core.TaskView, len(tasks))
	byShortID := make(map[string]core.TaskView, len(tasks))
	for _, view := range tasks {
		if view.ID == "" || view.ShortID == "" {
			return nil, nil, fmt.Errorf("loaded task graph contains a task with a missing id or shortId")
		}
		if _, exists := byID[view.ID]; exists {
			return nil, nil, fmt.Errorf("loaded task graph contains duplicate id %q", view.ID)
		}
		if _, exists := byShortID[view.ShortID]; exists {
			return nil, nil, fmt.Errorf("loaded task graph contains duplicate shortId %q", view.ShortID)
		}
		byID[view.ID] = view
		byShortID[view.ShortID] = view
	}
	return byID, byShortID, nil
}

func editableInputs(tasks, graph []core.TaskView) ([]core.BatchTaskUpdateInput, error) {
	byID, byShortID, err := graphIndexes(graph)
	if err != nil {
		return nil, err
	}

	inputs := make([]core.BatchTaskUpdateInput, len(tasks))
	for index, view := range tasks {
		dependencies := make([]string, len(view.Dependencies))
		for dependencyIndex, dependency := range view.Dependencies {
			dependencyTask, ok := byID[dependency]
			if !ok {
				dependencyTask, ok = byShortID[dependency]
			}
			if !ok {
				return nil, fmt.Errorf("task %s dependency %q cannot be resolved in loaded task graph", view.ShortID, dependency)
			}
			dependencies[dependencyIndex] = dependencyTask.ShortID
		}
		inputs[index] = core.BatchTaskUpdateInput{
			ID:                view.ID,
			ShortID:           view.ShortID,
			ExpectedUpdatedAt: view.UpdatedAt,
			Title:             core.OptionalString{Set: true, Value: view.Title},
			Description:       core.OptionalString{Set: true, Value: view.Description},
			Status:            core.OptionalStatus{Set: true, Value: view.Status},
			Priority:          core.OptionalPriority{Set: true, Value: view.Priority},
			Estimate:          core.OptionalEstimate{Set: true, Value: view.Estimate},
			Lane:              core.OptionalString{Set: true, Value: view.Lane},
			Model:             core.OptionalString{Set: true, Value: view.Model},
			GitRepo:           core.OptionalString{Set: true, Value: view.GitRepo},
			GitBranch:         core.OptionalString{Set: true, Value: view.GitBranch},
			WorktreeName:      core.OptionalString{Set: true, Value: view.WorktreeName},
			WorktreeDir:       core.OptionalString{Set: true, Value: view.WorktreeDir},
			Assignee:          core.OptionalString{Set: true, Value: view.Assignee},
			Dependencies:      core.OptionalStrings{Set: true, Value: dependencies},
		}
	}
	return inputs, nil
}

func encode(format Format, tasks []core.BatchTaskUpdateInput) ([]byte, error) {
	switch format {
	case FormatJSON:
		return batchjson.Encode(tasks)
	case FormatCSV:
		return batchcsv.Encode(tasks)
	default:
		return nil, fmt.Errorf("unsupported batch export format %q", format)
	}
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written <= 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create batch export directory %s: %w", directory, err)
	}
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create batch export temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := writeAll(temporary, data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write batch export temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync batch export temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close batch export temporary file: %w", err)
	}
	if err := atomicReplace(temporaryPath, path); err != nil {
		return fmt.Errorf("replace batch export %s: %w", path, err)
	}
	if err := atomicSyncDirectory(directory); err != nil {
		return fmt.Errorf("sync batch export directory %s: %w", directory, err)
	}
	return nil
}
