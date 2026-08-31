// Package reusablejson implements the versioned JSON representation of the
// store-global reusable task catalog. The codec owns wire strictness; the
// core package owns the catalog's semantic invariants.
package reusablejson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/mattrandles/wtproj/internal/core"
)

const Version = core.ReusableTaskCatalogVersion

// Encode serializes a valid catalog as compact, deterministic UTF-8 JSON.
// Definition order is part of the representation and is retained exactly.
func Encode(catalog core.ReusableTaskCatalog) ([]byte, error) {
	if err := catalog.Validate(); err != nil {
		return nil, err
	}
	for index, definition := range catalog.Definitions {
		if !utf8.ValidString(definition.Name) || !utf8.ValidString(definition.Title) || !utf8.ValidString(definition.Instructions) {
			return nil, fmt.Errorf("reusable catalog definition %d contains invalid UTF-8", index)
		}
	}

	document := catalogDTO{
		Version:     Version,
		Definitions: make([]definitionDTO, len(catalog.Definitions)),
	}
	for index, definition := range catalog.Definitions {
		document.Definitions[index] = definitionDTO{
			ID:           definition.ID,
			Name:         definition.Name,
			Title:        definition.Title,
			Instructions: definition.Instructions,
			CreatedAt:    formatTimestamp(definition.CreatedAt),
			UpdatedAt:    formatTimestamp(definition.UpdatedAt),
		}
	}

	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode reusable catalog: %w", err)
	}
	return encoded, nil
}

// Marshal is an alias for Encode for callers using encoding/json terminology.
func Marshal(catalog core.ReusableTaskCatalog) ([]byte, error) { return Encode(catalog) }

// Decode strictly parses a version-1 reusable task catalog. It rejects
// unknown and duplicate properties, malformed values, and catalogs that fail
// the core semantic contract.
func Decode(data []byte) (core.ReusableTaskCatalog, error) {
	if !utf8.Valid(data) {
		return core.ReusableTaskCatalog{}, errors.New("reusable catalog is not valid UTF-8")
	}
	root, err := decodeObject(data, "reusable catalog")
	if err != nil {
		return core.ReusableTaskCatalog{}, err
	}
	if err := requireOnly(root, "version", "definitions"); err != nil {
		return core.ReusableTaskCatalog{}, err
	}

	rawVersion, ok := root["version"]
	if !ok {
		return core.ReusableTaskCatalog{}, errors.New("reusable catalog is missing required property \"version\"")
	}
	version, err := decodeInt(rawVersion, "version")
	if err != nil {
		return core.ReusableTaskCatalog{}, err
	}
	if version != Version {
		return core.ReusableTaskCatalog{}, fmt.Errorf("unsupported reusable catalog version %d", version)
	}

	rawDefinitions, ok := root["definitions"]
	if !ok {
		return core.ReusableTaskCatalog{}, errors.New("reusable catalog is missing required property \"definitions\"")
	}
	if bytes.Equal(bytes.TrimSpace(rawDefinitions), []byte("null")) {
		return core.ReusableTaskCatalog{}, errors.New("reusable catalog definitions must be an array")
	}
	var rawItems []json.RawMessage
	if err := json.Unmarshal(rawDefinitions, &rawItems); err != nil || rawItems == nil {
		if err == nil {
			err = errors.New("null")
		}
		return core.ReusableTaskCatalog{}, fmt.Errorf("reusable catalog definitions must be an array: %w", err)
	}

	catalog := core.ReusableTaskCatalog{
		Version:     version,
		Definitions: make([]core.ReusableTaskDefinition, len(rawItems)),
	}
	for index, rawItem := range rawItems {
		definition, err := decodeDefinition(index, rawItem)
		if err != nil {
			return core.ReusableTaskCatalog{}, err
		}
		catalog.Definitions[index] = definition
	}
	if err := catalog.Validate(); err != nil {
		return core.ReusableTaskCatalog{}, err
	}
	return catalog, nil
}

// Unmarshal is an alias for Decode for callers using encoding/json terminology.
func Unmarshal(data []byte) (core.ReusableTaskCatalog, error) { return Decode(data) }

// ReadFile reads a catalog from path. A missing path is the one absent-storage
// case and returns a valid empty catalog; no file or parent directory is
// created. Every existing file, including an empty one, must decode strictly.
func ReadFile(path string) (core.ReusableTaskCatalog, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return core.EmptyReusableTaskCatalog(), nil
	}
	if err != nil {
		return core.ReusableTaskCatalog{}, fmt.Errorf("read reusable catalog %s: %w", path, err)
	}
	catalog, err := Decode(data)
	if err != nil {
		return core.ReusableTaskCatalog{}, fmt.Errorf("decode reusable catalog %s: %w", path, err)
	}
	return catalog, nil
}

// WriteFile writes the deterministic catalog bytes to path. Atomic replacement
// is deliberately left to the flat-file provider's existing storage layer;
// this helper is only the codec's file boundary.
func WriteFile(path string, catalog core.ReusableTaskCatalog) error {
	data, err := Encode(catalog)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent for reusable catalog %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write reusable catalog %s: %w", path, err)
	}
	return nil
}

// Codec provides method-oriented access to the package codec.
type Codec struct{}

func (Codec) Encode(catalog core.ReusableTaskCatalog) ([]byte, error) {
	return Encode(catalog)
}

func (Codec) Decode(data []byte) (core.ReusableTaskCatalog, error) {
	return Decode(data)
}

func (Codec) ReadFile(path string) (core.ReusableTaskCatalog, error) { return ReadFile(path) }
func (Codec) WriteFile(path string, catalog core.ReusableTaskCatalog) error {
	return WriteFile(path, catalog)
}

// The DTO field order is the public wire order. Keeping timestamps as strings
// prevents encoding/json's time layout and location rules from changing the
// catalog's canonical representation.
type catalogDTO struct {
	Version     int             `json:"version"`
	Definitions []definitionDTO `json:"definitions"`
}

type definitionDTO struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Title        string `json:"title"`
	Instructions string `json:"instructions"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

func decodeDefinition(index int, data json.RawMessage) (core.ReusableTaskDefinition, error) {
	label := fmt.Sprintf("reusable catalog definition %d", index+1)
	object, err := decodeObject(data, label)
	if err != nil {
		return core.ReusableTaskDefinition{}, err
	}
	if err := requireOnly(object, "id", "name", "title", "instructions", "createdAt", "updatedAt"); err != nil {
		return core.ReusableTaskDefinition{}, fmt.Errorf("%s: %w", label, err)
	}

	definition := core.ReusableTaskDefinition{}
	for _, field := range []struct {
		name   string
		target *string
	}{
		{name: "id", target: &definition.ID},
		{name: "name", target: &definition.Name},
		{name: "title", target: &definition.Title},
		{name: "instructions", target: &definition.Instructions},
	} {
		name, target := field.name, field.target
		raw, ok := object[name]
		if !ok {
			return core.ReusableTaskDefinition{}, fmt.Errorf("%s is missing required property %q", label, name)
		}
		value, err := decodeString(raw, name)
		if err != nil {
			return core.ReusableTaskDefinition{}, fmt.Errorf("%s: %w", label, err)
		}
		*target = value
	}

	for _, field := range []struct {
		name   string
		target *time.Time
	}{
		{name: "createdAt", target: &definition.CreatedAt},
		{name: "updatedAt", target: &definition.UpdatedAt},
	} {
		name, target := field.name, field.target
		raw, ok := object[name]
		if !ok {
			return core.ReusableTaskDefinition{}, fmt.Errorf("%s is missing required property %q", label, name)
		}
		value, err := decodeTimestamp(raw, name)
		if err != nil {
			return core.ReusableTaskDefinition{}, fmt.Errorf("%s: %w", label, err)
		}
		*target = value
	}
	return definition, nil
}

func decodeString(raw json.RawMessage, name string) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("%s must be a string", name)
	}
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("%s is not valid UTF-8", name)
	}
	return value, nil
}

func decodeTimestamp(raw json.RawMessage, name string) (time.Time, error) {
	value, err := decodeString(raw, name)
	if err != nil || value == "" {
		return time.Time{}, fmt.Errorf("%s must be an RFC3339Nano timestamp string", name)
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s is malformed: %w", name, err)
	}
	_, offset := parsed.Zone()
	if offset != 0 {
		return time.Time{}, fmt.Errorf("%s must use UTC (Z)", name)
	}
	return parsed.UTC(), nil
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

func formatTimestamp(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
