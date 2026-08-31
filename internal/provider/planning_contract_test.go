package provider_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
	"github.com/mattrandles/wtproj/internal/provider/flatfile"
)

// Check both directions so the architecture rejects not only missing methods
// but also scope expansion. No concrete provider claims unfinished support.
type planningContract interface {
	ListPlanningItems(provider.PlanningFilter) ([]core.PlanningItemView, error)
	GetPlanningItem(string) (core.PlanningItemView, error)
	CreatePlanningItem(core.CreatePlanningItemInput) (core.PlanningItemView, error)
	UpdatePlanningItem(string, core.UpdatePlanningItemInput) (core.PlanningItemView, error)
	SetPlanningStatus(string, core.PlanningStatus) (core.PlanningItemView, error)
	PreviewPlanningPromotion(core.GroupingFilter) (provider.PlanningPromotionResult[core.PlanningItemView], error)
	PromotePlanningItems(core.GroupingFilter) (provider.PlanningPromotionResult[core.TaskView], error)
}

var (
	_ planningContract          = provider.PlanningProvider(nil)
	_ provider.PlanningProvider = planningContract(nil)
	_ provider.PlanningReader   = provider.PlanningProvider(nil)
	_ provider.PlanningCreator  = provider.PlanningProvider(nil)
	_ provider.PlanningEditor   = provider.PlanningProvider(nil)
	_ provider.PlanningPromoter = provider.PlanningProvider(nil)
	_ provider.Provider         = (*flatfile.Provider)(nil)
)

func TestPlanningCapabilityIsolationContract(t *testing.T) {
	planning := reflect.TypeOf((*provider.PlanningProvider)(nil)).Elem()
	execution := reflect.TypeOf((*provider.Provider)(nil)).Elem()
	if planning.Implements(execution) || execution.Implements(planning) {
		t.Fatal("execution and planning capabilities must be separate")
	}
	for i := 0; i < planning.NumMethod(); i++ {
		if _, exists := execution.MethodByName(planning.Method(i).Name); exists {
			t.Errorf("execution Provider exposes %s", planning.Method(i).Name)
		}
	}
	for _, capability := range []struct {
		typeOf reflect.Type
		count  int
	}{
		{reflect.TypeOf((*provider.PlanningReader)(nil)).Elem(), 2},
		{reflect.TypeOf((*provider.PlanningCreator)(nil)).Elem(), 1},
		{reflect.TypeOf((*provider.PlanningEditor)(nil)).Elem(), 2},
		{reflect.TypeOf((*provider.PlanningPromoter)(nil)).Elem(), 2},
	} {
		if capability.typeOf.NumMethod() != capability.count {
			t.Errorf("%s scope expanded", capability.typeOf)
		}
	}
	filter := reflect.TypeOf(provider.PlanningFilter{})
	if filter.NumField() != 2 || filter.Field(0).Name != "Status" || filter.Field(0).Type != reflect.TypeOf((*core.PlanningStatus)(nil)) ||
		filter.Field(1).Name != "Grouping" || filter.Field(1).Type != reflect.TypeOf(core.GroupingFilter{}) {
		t.Fatal("planning filters must contain only planning status and shared six-field grouping")
	}
	grouping := reflect.TypeOf(core.GroupingFilter{})
	want := []string{"IssueID", "Project", "Milestone", "Version", "FeatureID", "Feature"}
	if grouping.NumField() != len(want) {
		t.Fatal("shared grouping contract changed")
	}
	for i, name := range want {
		if grouping.Field(i).Name != name {
			t.Errorf("grouping field %d = %s, want %s", i, grouping.Field(i).Name, name)
		}
	}
}

func TestPlanningPromotionEnvelopeContract(t *testing.T) {
	preview := provider.PlanningPromotionResult[core.PlanningItemView]{
		DryRun: true, Count: 1, Items: []core.PlanningItemView{{PlanningItem: core.PlanningItem{Status: core.PlanningStatusPlanned}}},
	}
	result := provider.PlanningPromotionResult[core.TaskView]{
		DryRun: false, Count: 1, Items: []core.TaskView{{Task: core.Task{Status: core.StatusTodo}}},
	}
	for _, tc := range []struct {
		name      string
		value     any
		status    string
		dryRun    string
		readiness bool
	}{
		{"preview", preview, `"planned"`, "true", false},
		{"publish", result, `"todo"`, "false", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			var envelope map[string]json.RawMessage
			if err := json.Unmarshal(data, &envelope); err != nil {
				t.Fatal(err)
			}
			if len(envelope) != 3 || string(envelope["dryRun"]) != tc.dryRun || string(envelope["count"]) != "1" {
				t.Fatalf("promotion envelope changed: %s", data)
			}
			var items []map[string]json.RawMessage
			if err := json.Unmarshal(envelope["items"], &items); err != nil || len(items) != 1 {
				t.Fatalf("items = %s, error %v", envelope["items"], err)
			}
			_, readiness := items[0]["readiness"]
			if string(items[0]["status"]) != tc.status || readiness != tc.readiness {
				t.Fatalf("promotion leaked the other lifecycle's view: %s", envelope["items"])
			}
		})
	}
}
