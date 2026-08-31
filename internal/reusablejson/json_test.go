package reusablejson

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
)

func TestEncodeDecodeRoundTripPreservesOrderAndUnicode(t *testing.T) {
	first := reusableDefinition(t, "7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6", "Tests", "Run focused tests", "Check Unicode: café ✓\nKeep the second line.", time.Nanosecond)
	second := reusableDefinition(t, "e5c3806a-bd1b-424d-889b-29e5b06679b8", "Verify", "Verify output", "Ensure 日本語 and emoji 🧪 survive unchanged.", 2*time.Nanosecond)
	want := core.ReusableTaskCatalog{Version: Version, Definitions: []core.ReusableTaskDefinition{first, second}}

	data, err := Encode(want)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	const expected = `{"version":1,"definitions":[{"id":"7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6","name":"Tests","title":"Run focused tests","instructions":"Check Unicode: café ✓\nKeep the second line.","createdAt":"2026-08-31T09:00:00.000000001Z","updatedAt":"2026-08-31T09:01:00.000000001Z"},{"id":"e5c3806a-bd1b-424d-889b-29e5b06679b8","name":"Verify","title":"Verify output","instructions":"Ensure 日本語 and emoji 🧪 survive unchanged.","createdAt":"2026-08-31T09:00:00.000000002Z","updatedAt":"2026-08-31T09:01:00.000000002Z"}]}`
	if string(data) != expected {
		t.Fatalf("Encode() = %s, want %s", data, expected)
	}

	got, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Decode() = %#v, want %#v", got, want)
	}
	if got.Definitions[0].Instructions != first.Instructions || got.Definitions[1].Instructions != second.Instructions {
		t.Fatalf("decoded instructions lost Unicode or order: %#v", got.Definitions)
	}
}

func TestEncodeIsDeterministic(t *testing.T) {
	catalog := core.ReusableTaskCatalog{
		Version: Version,
		Definitions: []core.ReusableTaskDefinition{
			reusableDefinition(t, "7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6", "First", "First title", "First instructions", 0),
			reusableDefinition(t, "e5c3806a-bd1b-424d-889b-29e5b06679b8", "Second", "Second title", "Second instructions", 0),
		},
	}
	one, err := Encode(catalog)
	if err != nil {
		t.Fatalf("first Encode() error = %v", err)
	}
	two, err := Encode(catalog)
	if err != nil {
		t.Fatalf("second Encode() error = %v", err)
	}
	if !bytes.Equal(one, two) {
		t.Fatalf("Encode() changed bytes:\nfirst:  %s\nsecond: %s", one, two)
	}
	if bytes.Contains(one, []byte("\n")) {
		t.Fatalf("Encode() added non-canonical whitespace: %s", one)
	}
}

func TestDecodeRejectsInvalidCatalogDocuments(t *testing.T) {
	validDefinition := `{"id":"7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6","name":"Tests","title":"Run tests","instructions":"Keep it deterministic","createdAt":"2026-08-31T09:00:00.123456789Z","updatedAt":"2026-08-31T09:01:00Z"}`
	validDocument := `{"version":1,"definitions":[` + validDefinition + `]}`
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "unsupported zero version", data: `{"version":0,"definitions":[]}`, want: "unsupported reusable catalog version 0"},
		{name: "unsupported future version", data: `{"version":2,"definitions":[]}`, want: "unsupported reusable catalog version 2"},
		{name: "unknown root field", data: `{"version":1,"definitions":[],"extra":true}`, want: `unknown property "extra"`},
		{name: "unknown definition field", data: `{"version":1,"definitions":[` + strings.TrimSuffix(validDefinition, "}") + `,"extra":true}]}`, want: `unknown property "extra"`},
		{name: "duplicate root field", data: `{"version":1,"version":1,"definitions":[]}`, want: `contains duplicate property "version"`},
		{name: "duplicate definition field", data: `{"version":1,"definitions":[{"id":"7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6","id":"7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6","name":"Tests","title":"Run tests","instructions":"Keep it deterministic","createdAt":"2026-08-31T09:00:00Z","updatedAt":"2026-08-31T09:01:00Z"}]}`, want: `contains duplicate property "id"`},
		{name: "mixed case duplicate names", data: `{"version":1,"definitions":[` + validDefinition + `,{"id":"e5c3806a-bd1b-424d-889b-29e5b06679b8","name":"tEsTs","title":"Another","instructions":"Another instruction","createdAt":"2026-08-31T09:02:00Z","updatedAt":"2026-08-31T09:03:00Z"}]}`, want: `name "tEsTs" is duplicated`},
		{name: "malformed ID", data: strings.Replace(validDocument, "7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6", "7a6e05a5b5db4d36a1cf4928cc5fd3e6", 1), want: "canonical lowercase UUID"},
		{name: "malformed timestamp", data: strings.Replace(validDocument, "2026-08-31T09:00:00.123456789Z", "yesterday", 1), want: "createdAt is malformed"},
		{name: "non-UTC timestamp", data: strings.Replace(validDocument, "2026-08-31T09:00:00.123456789Z", "2026-08-31T10:00:00.123456789+01:00", 1), want: "createdAt must use UTC"},
		{name: "empty existing document", data: "", want: "decode reusable catalog"},
		{name: "missing definitions", data: `{"version":1}`, want: `missing required property "definitions"`},
		{name: "null definitions", data: `{"version":1,"definitions":null}`, want: "definitions must be an array"},
		{name: "trailing data", data: validDocument + " {}", want: "trailing JSON data"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Decode([]byte(test.data))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Decode() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestEncodeRejectsSemanticallyInvalidCatalogs(t *testing.T) {
	valid := core.ReusableTaskCatalog{
		Version:     Version,
		Definitions: []core.ReusableTaskDefinition{reusableDefinition(t, "7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6", "Tests", "Run tests", "Keep it deterministic", 0)},
	}
	tests := []struct {
		name   string
		mutate func(*core.ReusableTaskCatalog)
		want   string
	}{
		{name: "unsupported version", mutate: func(catalog *core.ReusableTaskCatalog) { catalog.Version = 2 }, want: "unsupported reusable catalog version 2"},
		{name: "nil definitions", mutate: func(catalog *core.ReusableTaskCatalog) { catalog.Definitions = nil }, want: "definitions must be an array"},
		{name: "duplicate mixed case name", mutate: func(catalog *core.ReusableTaskCatalog) {
			second := catalog.Definitions[0]
			second.ID = "e5c3806a-bd1b-424d-889b-29e5b06679b8"
			second.Name = "tEsTs"
			catalog.Definitions = append(catalog.Definitions, second)
		}, want: "name \"tEsTs\" is duplicated"},
		{name: "invalid UTF-8", mutate: func(catalog *core.ReusableTaskCatalog) { catalog.Definitions[0].Instructions = string([]byte{0xff}) }, want: "invalid UTF-8"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := valid
			catalog.Definitions = append([]core.ReusableTaskDefinition(nil), valid.Definitions...)
			test.mutate(&catalog)
			if _, err := Encode(catalog); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Encode() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestReadFileMissingIsEmptyWithoutCreatingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "reusable.json")
	got, err := ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !reflect.DeepEqual(got, core.EmptyReusableTaskCatalog()) {
		t.Fatalf("ReadFile() = %#v, want empty catalog", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("ReadFile() created %s, stat error = %v", path, err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("ReadFile() created parent directory, stat error = %v", err)
	}
}

func TestReadFileRejectsExistingEmptyOrMalformedContent(t *testing.T) {
	dir := t.TempDir()
	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "empty", data: nil},
		{name: "malformed", data: []byte(`{"version":1,"definitions":[`)},
		{name: "semantically invalid", data: []byte(`{"version":1,"definitions":[{"id":"bad","name":"Tests","title":"Title","instructions":"Instructions","createdAt":"2026-08-31T09:00:00Z","updatedAt":"2026-08-31T09:00:00Z"}]}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(dir, test.name+".json")
			if err := os.WriteFile(path, test.data, 0o644); err != nil {
				t.Fatalf("os.WriteFile() error = %v", err)
			}
			if _, err := ReadFile(path); err == nil {
				t.Fatal("ReadFile() succeeded for invalid existing content")
			}
		})
	}
}

func TestWriteFileUsesCodecBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reusable.json")
	catalog := core.ReusableTaskCatalog{
		Version: Version,
		Definitions: []core.ReusableTaskDefinition{
			reusableDefinition(t, "7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6", "Tests", "Run tests", "Keep it deterministic", 123456789),
		},
	}
	if err := WriteFile(path, catalog); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	want, err := Encode(catalog)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("file bytes = %s, want %s", got, want)
	}
}

func reusableDefinition(t *testing.T, id, name, title, instructions string, offset time.Duration) core.ReusableTaskDefinition {
	t.Helper()
	createdAt := time.Date(2026, time.August, 31, 9, 0, 0, int(offset), time.UTC)
	updatedAt := time.Date(2026, time.August, 31, 9, 1, 0, int(offset), time.UTC)
	return core.ReusableTaskDefinition{ID: id, Name: name, Title: title, Instructions: instructions, CreatedAt: createdAt, UpdatedAt: updatedAt}
}
