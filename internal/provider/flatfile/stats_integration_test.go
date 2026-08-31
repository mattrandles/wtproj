package flatfile_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
	"github.com/mattrandles/wtproj/internal/provider/flatfile"
	"github.com/mattrandles/wtproj/internal/stats"
)

func TestFlatFileStatsSeriesUsesPersistedTimestampsAndIsReadOnly(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	asOf := time.Date(2026, time.August, 30, 12, 34, 56, 789000000, time.UTC)
	tasks := flatFileSeriesTasks(asOf)
	for _, task := range tasks {
		writeFlatFileSeriesJSON(t, filepath.Join(root, string(task.Status), task.ShortID+".json"), task)
	}
	handoffs := struct {
		Handoffs []core.Handoff `json:"handoffs"`
	}{Handoffs: []core.Handoff{{
		ID:        "00000000-0000-4000-8000-000000000199",
		Message:   "flat-file stats handoff",
		CreatedAt: asOf,
	}}}
	writeFlatFileSeriesJSON(t, filepath.Join(root, "handoffs.json"), handoffs)

	before := flatFileStorageManifest(t, root)
	beforeViews, err := p.ListTasks(provider.TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(before) error = %v", err)
	}
	if len(beforeViews) != len(tasks) {
		t.Fatalf("persisted task count = %d, want %d", len(beforeViews), len(tasks))
	}

	queries := []struct {
		name   string
		metric stats.SeriesMetric
		rangeV stats.RollingRange
		counts []int
		total  int
	}{
		{name: "created seven to zero", metric: stats.SeriesMetricCreated, rangeV: stats.RollingRange{StartDays: 7, EndDays: 0}, counts: []int{1, 1, 0, 0, 0, 0, 1}, total: 3},
		{name: "progressed seven to zero", metric: stats.SeriesMetricProgressed, rangeV: stats.RollingRange{StartDays: 7, EndDays: 0}, counts: []int{0, 0, 0, 0, 0, 0, 2}, total: 2},
		{name: "created fourteen to seven", metric: stats.SeriesMetricCreated, rangeV: stats.RollingRange{StartDays: 14, EndDays: 7}, counts: []int{1, 0, 0, 0, 1, 0, 0}, total: 2},
		{name: "progressed fourteen to seven", metric: stats.SeriesMetricProgressed, rangeV: stats.RollingRange{StartDays: 14, EndDays: 7}, counts: []int{1, 0, 0, 0, 1, 0, 0}, total: 2},
		{name: "created one to zero", metric: stats.SeriesMetricCreated, rangeV: stats.RollingRange{StartDays: 1, EndDays: 0}, counts: []int{1}, total: 1},
	}
	for _, query := range queries {
		t.Run(query.name, func(t *testing.T) {
			got, err := stats.AggregateSeries(p, stats.SeriesOptions{Metric: query.metric, Range: query.rangeV, AsOf: asOf})
			if err != nil {
				t.Fatalf("AggregateSeries() error = %v", err)
			}
			want := expectedFlatFileSeriesReport(query.metric, query.rangeV, asOf, query.counts, query.total)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("series report = %#v, want complete report %#v", got, want)
			}
			if after := flatFileStorageManifest(t, root); after != before {
				t.Fatalf("stats read changed storage:\nbefore %s\nafter %s", before, after)
			}
		})
	}

	afterViews, err := p.ListTasks(provider.TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(after) error = %v", err)
	}
	for index, beforeView := range beforeViews {
		if !beforeView.CreatedAt.Equal(afterViews[index].CreatedAt) || !beforeView.UpdatedAt.Equal(afterViews[index].UpdatedAt) {
			t.Fatalf("task timestamps changed for %s: before created=%v updated=%v, after created=%v updated=%v", beforeView.ShortID, beforeView.CreatedAt, beforeView.UpdatedAt, afterViews[index].CreatedAt, afterViews[index].UpdatedAt)
		}
	}
}

func flatFileSeriesTasks(asOf time.Time) []core.Task {
	return []core.Task{
		flatFileSeriesTask(1, "exact seven-day start", asOf.Add(-7*24*time.Hour), asOf.Add(-24*time.Hour)),
		flatFileSeriesTask(2, "exact fourteen-day start", asOf.Add(-14*24*time.Hour), asOf.Add(-14*24*time.Hour)),
		flatFileSeriesTask(3, "fourteen-day middle", asOf.Add(-10*24*time.Hour), asOf.Add(-10*24*time.Hour)),
		flatFileSeriesTask(4, "different created and progressed buckets", asOf.Add(-6*24*time.Hour), asOf.Add(-24*time.Hour)),
		flatFileSeriesTask(5, "one-day start", asOf.Add(-24*time.Hour), asOf),
		flatFileSeriesTask(6, "exact range end", asOf, asOf),
		flatFileSeriesTask(7, "outside range", asOf.Add(-15*24*time.Hour), asOf.Add(-15*24*time.Hour)),
		flatFileSeriesTask(8, "future value", asOf.Add(24*time.Hour), asOf.Add(24*time.Hour)),
	}
}

func flatFileSeriesTask(number int, title string, createdAt, updatedAt time.Time) core.Task {
	return core.Task{
		ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", number), ShortID: fmt.Sprintf("wtp-%04d", number),
		Title: title, Status: core.StatusTodo, Dependencies: []string{}, Comments: []core.Comment{},
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
}

func writeFlatFileSeriesJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent(%s) error = %v", path, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", path, err)
	}
}

func expectedFlatFileSeriesReport(metric stats.SeriesMetric, rangeV stats.RollingRange, asOf time.Time, counts []int, total int) stats.SeriesReport {
	rangeValue := stats.TimeRange{Start: asOf.Add(-time.Duration(rangeV.StartDays) * 24 * time.Hour), End: asOf.Add(-time.Duration(rangeV.EndDays) * 24 * time.Hour)}
	buckets := make([]stats.TimeBucket, len(counts))
	for index, count := range counts {
		start := rangeValue.Start.Add(time.Duration(index) * 24 * time.Hour)
		buckets[index] = stats.TimeBucket{Label: fmt.Sprintf("%dd-%dd", rangeV.StartDays-index, rangeV.StartDays-index-1), Start: start, End: start.Add(24 * time.Hour), Count: count}
	}
	return stats.SeriesReport{Attribute: metric, TotalTasks: total, Range: rangeValue, Buckets: buckets}
}

func flatFileStorageManifest(t *testing.T, root string) string {
	t.Helper()
	var paths []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			hash := sha256.Sum256(data)
			paths = append(paths, relative+"\x00"+hex.EncodeToString(hash[:])+"\x00"+string(data))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk storage: %v", err)
	}
	slices.Sort(paths)
	return fmt.Sprintf("%q", paths)
}
