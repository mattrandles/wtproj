package flatfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/planningjson"
)

func TestPromotePlanningItemsPublishesDeterministicallyAndPreservesSourceBytes(t *testing.T) {
	root := t.TempDir()
	p, err := New(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	item, err := p.CreatePlanningItem(core.CreatePlanningItemInput{
		Title: "preserve formatting", Description: "line one\nline two", Status: core.PlanningStatusPlanned,
		Project: "Apollo", Feature: "Search", Priority: core.PriorityHigh,
	})
	if err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(root, planningDirectory, string(core.PlanningStatusPlanned), item.ShortID+".json")
	canonical, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, canonical, "\t", "  "); err != nil {
		t.Fatal(err)
	}
	before := append([]byte("\n"), pretty.Bytes()...)
	before = append(before, '\n')
	if err := os.WriteFile(sourcePath, before, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := p.PromotePlanningItems(core.GroupingFilter{Project: " apOLLo ", Feature: "SEARCH"})
	if err != nil {
		t.Fatalf("PromotePlanningItems() error = %v", err)
	}
	if result.DryRun || result.Count != 1 || len(result.Items) != 1 {
		t.Fatalf("promotion result = %#v", result)
	}
	if result.Items[0].ID != item.ID || result.Items[0].ShortID != item.ShortID || result.Items[0].Status != core.StatusTodo {
		t.Fatalf("promoted identity/status = %s/%s/%s", result.Items[0].ID, result.Items[0].ShortID, result.Items[0].Status)
	}
	if _, err := os.Stat(sourcePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("planning source stat = %v, want absent", err)
	}
	afterPath := filepath.Join(root, string(core.StatusTodo), item.ShortID+".json")
	after, err := os.ReadFile(afterPath)
	if err != nil {
		t.Fatal(err)
	}
	want, err := rewritePlanningPromotionSource(before, result.Items[0].UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, want) {
		t.Fatalf("promotion rewrote preserved bytes:\nwant %q\ngot  %q", want, after)
	}
	if _, err := os.Stat(p.planningPromoteJournalPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("promotion journal stat = %v, want absent", err)
	}
	if got := result.Items[0].Readiness.Blocked; got {
		t.Fatalf("promoted item unexpectedly blocked: %#v", result.Items[0].Readiness)
	}
	decoded, err := planningjson.Decode(before)
	if err != nil || decoded.ID != item.ID {
		t.Fatalf("source was not a valid exact planning record: %v", err)
	}
}

func TestPromotePlanningItemsKeepsExecutableBlockers(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := p.CreateTask(core.CreateTaskInput{Title: "unfinished blocker"})
	if err != nil {
		t.Fatal(err)
	}
	planned, err := p.CreatePlanningItem(core.CreatePlanningItemInput{
		Title: "blocked promotion", Description: "depends on executable", Status: core.PlanningStatusPlanned,
		Project: "Apollo", Dependencies: []string{blocker.ShortID},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.PromotePlanningItems(core.GroupingFilter{Project: "Apollo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].ID != planned.ID {
		t.Fatalf("promoted items = %#v", result.Items)
	}
	if !result.Items[0].Readiness.Blocked || !strings.Contains(result.Items[0].Readiness.BlockedReason, blocker.ShortID) {
		t.Fatalf("readiness = %#v, want executable blocker", result.Items[0].Readiness)
	}
}

func TestPromotePlanningItemsSerializesConcurrentPromotionAndUpdate(t *testing.T) {
	root := t.TempDir()
	p, err := New(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	item, err := p.CreatePlanningItem(core.CreatePlanningItemInput{Title: "race", Status: core.PlanningStatusPlanned, Project: "Apollo"})
	if err != nil {
		t.Fatal(err)
	}
	promoter, err := New(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	updater, err := New(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := promoter.PromotePlanningItems(core.GroupingFilter{Project: "Apollo"})
		results <- err
	}()
	go func() {
		defer wg.Done()
		_, err := updater.UpdatePlanningItem(item.ShortID, core.UpdatePlanningItemInput{Title: core.OptionalString{Set: true, Value: "updated"}})
		results <- err
	}()
	wg.Wait()
	close(results)
	var successes, failures int
	for err := range results {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes < 1 || successes > 2 || failures != 2-successes {
		t.Fatalf("concurrent promotion/update outcomes = successes %d failures %d", successes, failures)
	}
	if _, err := New(root, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := p.GetPlanningItem(item.ID); err == nil {
		t.Fatal("planning item remains after successful promotion")
	}
	if _, err := p.GetTask(item.ID, ""); err != nil {
		t.Fatalf("promoted task lookup failed after concurrent operations: %v", err)
	}
}
