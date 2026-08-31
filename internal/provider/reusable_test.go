package provider_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
	"github.com/mattrandles/wtproj/internal/provider/flatfile"
)

// Lock down method signatures without adding nonfunctional CRUD stubs to a
// concrete provider. The existing flat-file provider still satisfies Provider.
var (
	_ provider.Provider             = (*flatfile.Provider)(nil)
	_ provider.ReusableTaskProvider = (*flatfile.Provider)(nil)
	_ interface {
		ListReusableTasks() ([]core.ReusableTaskDefinition, error)
		GetReusableTask(string) (core.ReusableTaskDefinition, error)
		CreateReusableTask(core.CreateReusableTaskInput) (core.ReusableTaskDefinition, error)
	} = provider.ReusableTaskProvider(nil)
	_ interface {
		UpdateReusableTask(string, core.UpdateReusableTaskInput) (core.ReusableTaskDefinition, error)
		DeleteReusableTask(string) (provider.ReusableTaskDeleteResult, error)
	} = provider.ReusableTaskMutationProvider(nil)
)

func TestReusableTaskDeleteResultJSONContract(t *testing.T) {
	const input = `{"deleted":{"id":"7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6","name":"Tests","title":"Run tests","instructions":"Run focused tests.","createdAt":"2026-08-31T09:00:00Z","updatedAt":"2026-08-31T09:01:00Z"},"detachedTaskCount":0}`
	var result provider.ReusableTaskDeleteResult
	if err := json.Unmarshal([]byte(input), &result); err != nil {
		t.Fatal(err)
	}
	if err := result.Deleted.Validate(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result)
	if err != nil || string(data) != input {
		t.Fatalf("delete result JSON = %s, error = %v", data, err)
	}
	result.DetachedTaskCount = 2
	data, err = json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var got provider.ReusableTaskDeleteResult
	if err := json.Unmarshal(data, &got); err != nil || !reflect.DeepEqual(got, result) {
		t.Fatalf("delete result round trip = %#v, error = %v", got, err)
	}
}
