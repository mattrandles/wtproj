package core

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReusableTaskDefinitionValidateRejectsNonCanonicalIDs(t *testing.T) {
	valid := reusableDefinitionFixture(t)
	for _, id := range []string{
		"",
		"7A6E05A5-B5DB-4D36-A1CF-4928CC5FD3E6",
		"7a6e05a5b5db4d36a1cf4928cc5fd3e6",
		"{7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6}",
		"7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e60",
		"7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6\n",
	} {
		t.Run("id="+id, func(t *testing.T) {
			definition := valid
			definition.ID = id
			if err := definition.Validate(); err == nil || !strings.Contains(err.Error(), "canonical lowercase UUID") {
				t.Fatalf("Validate() error = %v, want canonical lowercase UUID error", err)
			}
		})
	}
}

func TestReusableTaskDefinitionValidateRequiresTrimmedText(t *testing.T) {
	valid := reusableDefinitionFixture(t)
	for _, test := range []struct {
		name   string
		mutate func(*ReusableTaskDefinition)
		want   string
	}{
		{name: "name required", mutate: func(definition *ReusableTaskDefinition) { definition.Name = "\t\n" }, want: "name is required"},
		{name: "name trimmed", mutate: func(definition *ReusableTaskDefinition) { definition.Name = " Tests" }, want: "name must be trimmed"},
		{name: "title required", mutate: func(definition *ReusableTaskDefinition) { definition.Title = " " }, want: "title is required"},
		{name: "title trimmed", mutate: func(definition *ReusableTaskDefinition) { definition.Title = "Run focused tests " }, want: "title must be trimmed"},
		{name: "instructions required", mutate: func(definition *ReusableTaskDefinition) { definition.Instructions = "\u2003" }, want: "instructions is required"},
		{name: "instructions trimmed", mutate: func(definition *ReusableTaskDefinition) {
			definition.Instructions = " Check Unicode: café ✓\nKeep the second line."
		}, want: "instructions must be trimmed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			definition := valid
			test.mutate(&definition)
			if err := definition.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestReusableTaskDefinitionValidateRequiresUTCOrderedTimestamps(t *testing.T) {
	valid := reusableDefinitionFixture(t)
	created := valid.CreatedAt
	tests := []struct {
		name   string
		mutate func(*ReusableTaskDefinition)
		want   string
	}{
		{name: "created required", mutate: func(definition *ReusableTaskDefinition) { definition.CreatedAt = time.Time{} }, want: "timestamps are required"},
		{name: "updated required", mutate: func(definition *ReusableTaskDefinition) { definition.UpdatedAt = time.Time{} }, want: "timestamps are required"},
		{name: "created UTC", mutate: func(definition *ReusableTaskDefinition) {
			definition.CreatedAt = created.In(time.FixedZone("BST", 3600))
		}, want: "timestamps must be in UTC"},
		{name: "updated UTC", mutate: func(definition *ReusableTaskDefinition) {
			definition.UpdatedAt = valid.UpdatedAt.In(time.FixedZone("BST", 3600))
		}, want: "timestamps must be in UTC"},
		{name: "updated not before created", mutate: func(definition *ReusableTaskDefinition) { definition.UpdatedAt = created.Add(-time.Nanosecond) }, want: "updatedAt cannot be before createdAt"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition := valid
			test.mutate(&definition)
			if err := definition.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.want)
			}
		})
	}

	valid.UpdatedAt = valid.CreatedAt
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() rejected equal createdAt and updatedAt: %v", err)
	}
}

func TestReusableTaskCatalogValidateRejectsInvalidWrappersAndDuplicateIdentity(t *testing.T) {
	validDefinition := reusableDefinitionFixture(t)
	second := validDefinition
	second.ID = "e5c3806a-bd1b-424d-889b-29e5b06679b8"
	second.Name = "Verify"
	valid := ReusableTaskCatalog{
		Version:     ReusableTaskCatalogVersion,
		Definitions: []ReusableTaskDefinition{validDefinition, second},
	}

	for _, test := range []struct {
		name   string
		mutate func(*ReusableTaskCatalog)
		want   string
	}{
		{name: "zero version", mutate: func(catalog *ReusableTaskCatalog) { catalog.Version = 0 }, want: "unsupported reusable catalog version 0"},
		{name: "future version", mutate: func(catalog *ReusableTaskCatalog) { catalog.Version = 2 }, want: "unsupported reusable catalog version 2"},
		{name: "negative version", mutate: func(catalog *ReusableTaskCatalog) { catalog.Version = -1 }, want: "unsupported reusable catalog version -1"},
		{name: "nil definitions", mutate: func(catalog *ReusableTaskCatalog) { catalog.Definitions = nil }, want: "definitions must be an array"},
		{name: "duplicate IDs", mutate: func(catalog *ReusableTaskCatalog) { catalog.Definitions[1].ID = catalog.Definitions[0].ID }, want: "reusable definition id"},
		{name: "case-insensitive duplicate names", mutate: func(catalog *ReusableTaskCatalog) { catalog.Definitions[1].Name = "tEsTs" }, want: `name "tEsTs" is duplicated`},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog := valid
			catalog.Definitions = append([]ReusableTaskDefinition(nil), valid.Definitions...)
			test.mutate(&catalog)
			if err := catalog.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestReusableTaskCatalogJSONWrapperCompatibility(t *testing.T) {
	validJSON := `{"version":1,"definitions":[]}`
	for _, test := range []struct {
		name       string
		data       string
		decodeWant string
		validWant  string
	}{
		{name: "malformed JSON", data: `{"version":1,"definitions":[`, decodeWant: "unexpected end"},
		{name: "root is not object", data: `[]`, decodeWant: "cannot unmarshal array"},
		{name: "version wrong type", data: `{"version":"1","definitions":[]}`, decodeWant: "cannot unmarshal string"},
		{name: "definitions wrong type", data: `{"version":1,"definitions":{}}`, decodeWant: "cannot unmarshal object"},
		{name: "missing version", data: `{"definitions":[]}`, validWant: "unsupported reusable catalog version 0"},
		{name: "missing definitions", data: `{"version":1}`, validWant: "definitions must be an array"},
		{name: "null definitions", data: `{"version":1,"definitions":null}`, validWant: "definitions must be an array"},
		{name: "unsupported version", data: `{"version":2,"definitions":[]}`, validWant: "unsupported reusable catalog version 2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var catalog ReusableTaskCatalog
			err := json.Unmarshal([]byte(test.data), &catalog)
			if test.decodeWant != "" {
				if err == nil || !strings.Contains(err.Error(), test.decodeWant) {
					t.Fatalf("json.Unmarshal() error = %v, want substring %q", err, test.decodeWant)
				}
				return
			}
			if err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			if err := catalog.Validate(); err == nil || !strings.Contains(err.Error(), test.validWant) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.validWant)
			}
		})
	}

	var catalog ReusableTaskCatalog
	if err := json.Unmarshal([]byte(validJSON), &catalog); err != nil {
		t.Fatalf("json.Unmarshal(valid) error = %v", err)
	}
	if err := catalog.Validate(); err != nil {
		t.Fatalf("Validate(valid) error = %v", err)
	}
	// Strict duplicate-property rejection belongs to a wire codec. Core's
	// catalog type intentionally exposes only unmarshalling plus validation;
	// the strict batch codec has its own duplicate-property contract tests.
}

func TestReusableTaskIDValidationRejectsInvalidReferences(t *testing.T) {
	definition := reusableDefinitionFixture(t)
	catalog := ReusableTaskCatalog{Version: ReusableTaskCatalogVersion, Definitions: []ReusableTaskDefinition{definition}}
	for _, test := range []struct {
		name string
		ids  []string
		want string
	}{
		{name: "name is not stored ID", ids: []string{definition.Name}, want: "canonical lowercase UUID"},
		{name: "uppercase ID", ids: []string{strings.ToUpper(definition.ID)}, want: "canonical lowercase UUID"},
		{name: "unknown canonical ID", ids: []string{"25c3806a-bd1b-424d-889b-29e5b06679b8"}, want: "does not exist"},
		{name: "duplicate assigned ID", ids: []string{definition.ID, definition.ID}, want: "is duplicated"},
	} {
		t.Run(test.name, func(t *testing.T) {
			validationErr := validateReusableTaskIDList(test.ids)
			if test.name == "unknown canonical ID" {
				if validationErr != nil {
					t.Fatalf("validateReusableTaskIDList() error = %v, want unknown IDs to pass syntax validation", validationErr)
				}
			} else if validationErr == nil || !strings.Contains(validationErr.Error(), test.want) {
				t.Fatalf("validateReusableTaskIDList() error = %v, want substring %q", validationErr, test.want)
			}

			if _, err := ResolveReusableTasks(test.ids, catalog); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ResolveReusableTasks() error = %v, want substring %q", err, test.want)
			}

			if test.name != "unknown canonical ID" {
				task := validValidationTask(t)
				task.ReusableTaskIDs = test.ids
				if err := task.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("Task.Validate() error = %v, want substring %q", err, test.want)
				}
			}
		})
	}
}

func TestReusableTaskResolutionPreservesAssignmentOrder(t *testing.T) {
	first := reusableDefinitionFixture(t)
	second := first
	second.ID = "e5c3806a-bd1b-424d-889b-29e5b06679b8"
	second.Name = "Verify"
	catalog := ReusableTaskCatalog{
		Version:     ReusableTaskCatalogVersion,
		Definitions: []ReusableTaskDefinition{first, second},
	}
	assigned := []string{second.ID, first.ID}

	resolved, err := ResolveReusableTasks(assigned, catalog)
	if err != nil {
		t.Fatalf("ResolveReusableTasks() error = %v", err)
	}
	if got, want := []string{resolved[0].ID, resolved[1].ID}, assigned; !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved IDs = %v, want stable assignment order %v", got, want)
	}
	if resolved[0].Name != "Verify" || resolved[1].Name != "Tests" {
		t.Fatalf("resolved names = [%q, %q], want [Verify, Tests]", resolved[0].Name, resolved[1].Name)
	}

	resolved[0].Name = "Mutated view"
	if catalog.Definitions[1].Name == "Mutated view" {
		t.Fatal("ResolveReusableTasks() returned a definition backed by the catalog slice")
	}
}

func TestReusableTaskJSONOmitsEmptyAssignmentsAndRoundTripsPopulatedValues(t *testing.T) {
	task := validValidationTask(t)
	for _, assignments := range [][]string{nil, {}} {
		task.ReusableTaskIDs = assignments
		data, err := json.Marshal(task)
		if err != nil {
			t.Fatalf("json.Marshal(empty assignments) error = %v", err)
		}
		if strings.Contains(string(data), `"reusableTaskIds"`) {
			t.Fatalf("empty assignments were not omitted: %s", data)
		}
	}

	first := reusableDefinitionFixture(t)
	second := first
	second.ID = "e5c3806a-bd1b-424d-889b-29e5b06679b8"
	second.Name = "Verify"
	catalog := ReusableTaskCatalog{
		Version:     ReusableTaskCatalogVersion,
		Definitions: []ReusableTaskDefinition{first, second},
	}
	task.ReusableTaskIDs = []string{second.ID, first.ID}
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("json.Marshal(populated task) error = %v", err)
	}
	if !strings.Contains(string(data), `"reusableTaskIds":["e5c3806a-bd1b-424d-889b-29e5b06679b8","7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6"]`) {
		t.Fatalf("populated assignments were not serialized in order: %s", data)
	}
	var gotTask Task
	if err := json.Unmarshal(data, &gotTask); err != nil {
		t.Fatalf("json.Unmarshal(task) error = %v", err)
	}
	if !reflect.DeepEqual(gotTask, task) {
		t.Fatalf("task round trip = %#v, want %#v", gotTask, task)
	}

	catalogData, err := json.Marshal(catalog)
	if err != nil {
		t.Fatalf("json.Marshal(catalog) error = %v", err)
	}
	var gotCatalog ReusableTaskCatalog
	if err := json.Unmarshal(catalogData, &gotCatalog); err != nil {
		t.Fatalf("json.Unmarshal(catalog) error = %v", err)
	}
	if !reflect.DeepEqual(gotCatalog, catalog) {
		t.Fatalf("catalog round trip = %#v, want %#v", gotCatalog, catalog)
	}
	secondCatalogData, err := json.Marshal(gotCatalog)
	if err != nil || string(secondCatalogData) != string(catalogData) {
		t.Fatalf("catalog JSON is not deterministic: first=%s second=%s error=%v", catalogData, secondCatalogData, err)
	}
}
