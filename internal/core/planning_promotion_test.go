package core

import (
	"strings"
	"testing"
	"time"
)

func TestSelectPlanningPromotionFiltersStatusAndOrder(t *testing.T) {
	created := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	items := []PlanningItem{
		{ID: "planned-late", ShortID: "wtp-0003", Status: PlanningStatusPlanned, Project: "Apollo", Feature: "Search", CreatedAt: created.Add(time.Minute)},
		{ID: "researched", ShortID: "wtp-0001", Status: PlanningStatusResearched, Project: "Apollo", Feature: "Search", CreatedAt: created},
		{ID: "planned-first", ShortID: "wtp-0002", Status: PlanningStatusPlanned, Project: "Apollo", Feature: "Search", CreatedAt: created},
		{ID: "other", ShortID: "wtp-0004", Status: PlanningStatusPlanned, Project: "Apollo", Feature: "Other", CreatedAt: created},
	}

	selected, err := SelectPlanningPromotion(items, nil, GroupingFilter{Project: " apOLLo ", Feature: "SEARCH"})
	if err != nil {
		t.Fatalf("SelectPlanningPromotion() error = %v", err)
	}
	if len(selected) != 2 || selected[0].ShortID != "wtp-0002" || selected[1].ShortID != "wtp-0003" {
		t.Fatalf("selected = %#v, want planned records in createdAt/shortId order", selected)
	}
	if items[0].Status != PlanningStatusPlanned || items[0].ShortID != "wtp-0003" {
		t.Fatal("selection mutated input records")
	}
}

func TestSelectPlanningPromotionRequiresFilterAndMatch(t *testing.T) {
	item := PlanningItem{ID: "one", ShortID: "wtp-0001", Status: PlanningStatusPlanned, Project: "Apollo"}
	for _, test := range []struct {
		name    string
		filter  GroupingFilter
		wantErr string
	}{
		{name: "missing selector", wantErr: "requires at least one grouping selector"},
		{name: "no match", filter: GroupingFilter{Project: "missing"}, wantErr: "no planned planning items match promotion filters"},
	} {
		t.Run(test.name, func(t *testing.T) {
			items := []PlanningItem{item}
			_, err := SelectPlanningPromotion(items, nil, test.filter)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestSelectPlanningPromotionRequiresTransitivePlanningClosure(t *testing.T) {
	root := PlanningItem{ID: "planning-root", ShortID: "wtp-0001", Status: PlanningStatusPlanned, Project: "Apollo", Dependencies: []string{"executable"}}
	missing := PlanningItem{ID: "planning-missing", ShortID: "wtp-0003", Status: PlanningStatusPlanned, Project: "Other"}
	executable := Task{ID: "executable", ShortID: "wtp-0002", Dependencies: []string{"planning-missing"}}
	_, err := SelectPlanningPromotion([]PlanningItem{root, missing}, []Task{executable}, GroupingFilter{Project: "Apollo"})
	if err == nil {
		t.Fatal("SelectPlanningPromotion() accepted a non-closed transitive selection")
	}
	want := "planning promotion selection is not dependency-closed: missing planning dependency chain: wtp-0001 (planning planned) -> wtp-0002 -> wtp-0003 (planning planned)"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

func TestSelectPlanningPromotionReportsNonPlannedDependencyDeterministically(t *testing.T) {
	root := PlanningItem{ID: "root", ShortID: "wtp-0001", Status: PlanningStatusPlanned, Project: "Apollo", Dependencies: []string{"z-executable"}}
	first := PlanningItem{ID: "first", ShortID: "wtp-0002", Status: PlanningStatusResearched, Project: "Apollo"}
	second := PlanningItem{ID: "second", ShortID: "wtp-0003", Status: PlanningStatusRejected, Project: "Apollo"}
	executable := Task{ID: "z-executable", ShortID: "wtp-0004", Dependencies: []string{"second", "first"}}
	_, err := SelectPlanningPromotion([]PlanningItem{root, first, second}, []Task{executable}, GroupingFilter{Project: "Apollo"})
	if err == nil {
		t.Fatal("SelectPlanningPromotion() accepted a non-planned dependency")
	}
	want := "planning promotion selection is not dependency-closed: missing planning dependency chain: wtp-0001 (planning planned) -> wtp-0004 -> wtp-0002 (planning researched)"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}
