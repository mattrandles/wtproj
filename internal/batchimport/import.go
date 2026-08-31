// Package batchimport implements the provider-facing, non-CLI batch import
// service. It deliberately leaves argument parsing and output formatting to
// the command layer.
package batchimport

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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

// Options describes one import. Source, Input, In, and Path are compatibility
// aliases for the command boundary; at most one distinct value may be used.
// A source of "-" reads from Stdin (or Reader).
type Options struct {
	Source string
	Input  string
	In     string
	Path   string

	Format Format
	Stdin  io.Reader
	Reader io.Reader
}

func (o Options) source() (string, error) {
	values := []struct {
		name  string
		value string
	}{
		{"source", o.Source}, {"input", o.Input}, {"in", o.In}, {"path", o.Path},
	}
	chosen, chosenName := "", ""
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
			return "", fmt.Errorf("batch import %s conflicts with %s", item.name, chosenName)
		}
	}
	if chosen == "" {
		return "", errors.New("batch import input is required")
	}
	return chosen, nil
}

// Diagnostic identifies one independent decode or mapping problem. Row is
// one-based. Task is the row's supplied shortId or id, when available.
type Diagnostic struct {
	Row     int    `json:"row"`
	Task    string `json:"task,omitempty"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

// DiagnosticsError reports every independent importer validation problem in
// deterministic input-row and field order.
type DiagnosticsError struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
}

func (e *DiagnosticsError) Error() string {
	if e == nil || len(e.Diagnostics) == 0 {
		return "batch import validation failed"
	}
	var builder strings.Builder
	builder.WriteString("batch import validation failed:")
	for _, diagnostic := range e.Diagnostics {
		builder.WriteString("\nrow ")
		builder.WriteString(fmt.Sprint(diagnostic.Row))
		if diagnostic.Task != "" {
			builder.WriteString(" task ")
			builder.WriteString(diagnostic.Task)
		}
		if diagnostic.Field != "" {
			builder.WriteString(" field ")
			builder.WriteString(diagnostic.Field)
		}
		builder.WriteString(": ")
		builder.WriteString(diagnostic.Message)
	}
	return builder.String()
}

// Errors returns a defensive copy of the diagnostics.
func (e *DiagnosticsError) Errors() []Diagnostic {
	if e == nil {
		return nil
	}
	return append([]Diagnostic(nil), e.Diagnostics...)
}

// Result is the provider's updated/unchanged result. Keeping the provider
// type here makes the service a transparent boundary for the CLI layer.
type Result = provider.BatchUpdateResult

// Service binds import operations to one provider.
type Service struct {
	Provider provider.Provider
}

// New returns an import service for p.
func New(p provider.Provider) *Service { return &Service{Provider: p} }

// Import reads, decodes, validates, and atomically submits one batch.
func Import(p provider.Provider, options Options, stdin ...io.Reader) (provider.BatchUpdateResult, error) {
	return New(p).Import(options, stdin...)
}

// Run is a descriptive alias for Import.
func Run(p provider.Provider, options Options, stdin ...io.Reader) (provider.BatchUpdateResult, error) {
	return Import(p, options, stdin...)
}

// Import performs one import using the service's provider. The optional
// reader takes precedence over Options.Reader and Options.Stdin for stdin.
func (s *Service) Import(options Options, stdin ...io.Reader) (provider.BatchUpdateResult, error) {
	if s == nil || s.Provider == nil {
		return provider.BatchUpdateResult{}, errors.New("batch import provider is nil")
	}
	source, err := options.source()
	if err != nil {
		return provider.BatchUpdateResult{}, err
	}
	format, err := ResolveFormat(source, options.Format)
	if err != nil {
		return provider.BatchUpdateResult{}, err
	}
	data, err := readInput(source, options, stdin...)
	if err != nil {
		return provider.BatchUpdateResult{}, err
	}

	inputs, err := decode(format, data)
	if err != nil {
		return provider.BatchUpdateResult{}, err
	}
	inputs, err = mapInputs(inputs, s.Provider.StatusCatalog())
	if err != nil {
		return provider.BatchUpdateResult{}, err
	}

	result, err := s.Provider.BatchUpdate(provider.BatchUpdateRequest{Tasks: inputs})
	if err != nil {
		return provider.BatchUpdateResult{}, err
	}
	return orderResult(result, inputs), nil
}

// ResolveFormat validates an explicit format or infers one from .csv/.json.
// Stdin and unknown extensions require an explicit format.
func ResolveFormat(source string, explicit Format) (Format, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", errors.New("batch import input is required")
	}
	if explicit != "" {
		format := Format(strings.ToLower(strings.TrimSpace(string(explicit))))
		if format != FormatCSV && format != FormatJSON {
			return "", fmt.Errorf("unsupported batch import format %q", explicit)
		}
		return format, nil
	}
	if source == "-" {
		return "", errors.New("batch import format is required for stdin")
	}
	switch strings.ToLower(filepath.Ext(source)) {
	case ".csv":
		return FormatCSV, nil
	case ".json":
		return FormatJSON, nil
	default:
		return "", errors.New("batch import format is required for an unknown file extension")
	}
}

func readInput(source string, options Options, stdin ...io.Reader) ([]byte, error) {
	if source != "-" {
		data, err := os.ReadFile(source)
		if err != nil {
			return nil, fmt.Errorf("read batch import input %s: %w", source, err)
		}
		return data, nil
	}
	reader := options.Stdin
	if options.Reader != nil {
		reader = options.Reader
	}
	if len(stdin) > 0 && stdin[0] != nil {
		reader = stdin[0]
	}
	if reader == nil {
		reader = os.Stdin
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read batch import stdin: %w", err)
	}
	return data, nil
}

func decode(format Format, data []byte) ([]core.BatchTaskUpdateInput, error) {
	switch format {
	case FormatJSON:
		return batchjson.Decode(data)
	case FormatCSV:
		return batchcsv.Decode(data)
	default:
		return nil, fmt.Errorf("unsupported batch import format %q", format)
	}
}

func mapInputs(inputs []core.BatchTaskUpdateInput, catalog core.StatusCatalog) ([]core.BatchTaskUpdateInput, error) {
	if len(catalog.Statuses()) == 0 {
		catalog = core.DefaultStatusCatalog()
	}
	mapped := append([]core.BatchTaskUpdateInput(nil), inputs...)
	diagnostics := make([]Diagnostic, 0)
	for index := range mapped {
		input := &mapped[index]
		task := input.ShortID
		if strings.TrimSpace(task) == "" {
			task = input.ID
		}
		add := func(field string, mappingErr error) {
			if mappingErr != nil {
				diagnostics = append(diagnostics, Diagnostic{Row: index + 1, Task: task, Field: field, Message: mappingErr.Error()})
			}
		}
		if input.ExpectedUpdatedAt.IsZero() {
			add("updatedAt", errors.New("updatedAt is required"))
		}
		if input.Status.Set {
			status, mappingErr := catalog.ParseStatus(strings.TrimSpace(string(input.Status.Value)))
			add("status", mappingErr)
			if mappingErr == nil {
				input.Status.Value = status
			}
		}
		if input.Priority.Set {
			priority, mappingErr := core.ParsePriority(string(input.Priority.Value))
			add("priority", mappingErr)
			if mappingErr == nil {
				input.Priority.Value = priority
			}
		}
		if input.Estimate.Set {
			estimate, mappingErr := core.ParseEstimate(string(input.Estimate.Value))
			add("estimate", mappingErr)
			if mappingErr == nil {
				input.Estimate.Value = estimate
			}
		}
	}
	if len(diagnostics) > 0 {
		return nil, &DiagnosticsError{Diagnostics: diagnostics}
	}
	return mapped, nil
}

func orderResult(result provider.BatchUpdateResult, inputs []core.BatchTaskUpdateInput) provider.BatchUpdateResult {
	order := make(map[string]int, len(inputs))
	for index, input := range inputs {
		if strings.TrimSpace(input.ID) != "" {
			order[input.ID] = index
		}
		if strings.TrimSpace(input.ShortID) != "" {
			order[input.ShortID] = index
		}
	}
	less := func(left, right core.TaskView) bool {
		leftIndex, leftOK := order[left.ID]
		if !leftOK {
			leftIndex, leftOK = order[left.ShortID]
		}
		rightIndex, rightOK := order[right.ID]
		if !rightOK {
			rightIndex, rightOK = order[right.ShortID]
		}
		if leftOK && rightOK {
			return leftIndex < rightIndex
		}
		if leftOK != rightOK {
			return leftOK
		}
		return left.ShortID < right.ShortID
	}
	sort.SliceStable(result.Updated, func(i, j int) bool { return less(result.Updated[i], result.Updated[j]) })
	sort.SliceStable(result.Unchanged, func(i, j int) bool { return less(result.Unchanged[i], result.Unchanged[j]) })
	return result
}
