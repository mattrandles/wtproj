package stats

import (
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
)

// SeriesMetric identifies the task timestamp used by a rolling time-series
// query. Created counts CreatedAt; progressed counts the latest UpdatedAt.
type SeriesMetric string

const (
	SeriesMetricCreated    SeriesMetric = "created"
	SeriesMetricProgressed SeriesMetric = "progressed"
)

// Validate rejects values outside the supported typed series metrics.
func (m SeriesMetric) Validate() error {
	switch m {
	case SeriesMetricCreated, SeriesMetricProgressed:
		return nil
	default:
		return fmt.Errorf("unknown stats series metric %q", m)
	}
}

const day = 24 * time.Hour

const maxDurationDays = (1<<63 - 1) / int64(day)

var rollingRangePattern = regexp.MustCompile(`^([0-9]+)d-([0-9]+)d$`)

// RollingRange describes a rolling window using whole-day offsets from an
// explicit as-of instant. StartDays must be greater than EndDays: 7d-0d, for
// example, resolves to [asOf-168h, asOf).
type RollingRange struct {
	StartDays int
	EndDays   int
}

// String returns the canonical day-offset form of the range.
func (r RollingRange) String() string {
	return fmt.Sprintf("%dd-%dd", r.StartDays, r.EndDays)
}

// ParseRollingRange parses the strict day-offset form accepted by series
// queries, such as 7d-0d. The bounds are whole, non-negative day counts.
func ParseRollingRange(value string) (RollingRange, error) {
	matches := rollingRangePattern.FindStringSubmatch(value)
	if matches == nil {
		return RollingRange{}, fmt.Errorf("stats rolling range %q must match STARTd-ENDd", value)
	}
	start, err := parseRollingDayOffset(matches[1])
	if err != nil {
		return RollingRange{}, fmt.Errorf("invalid stats rolling range %q: %w", value, err)
	}
	end, err := parseRollingDayOffset(matches[2])
	if err != nil {
		return RollingRange{}, fmt.Errorf("invalid stats rolling range %q: %w", value, err)
	}
	rangeValue := RollingRange{StartDays: start, EndDays: end}
	if err := rangeValue.validate(); err != nil {
		return RollingRange{}, err
	}
	return rangeValue, nil
}

func parseRollingDayOffset(value string) (int, error) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("day offset must be a non-negative integer")
	}
	maxInt := uint64(^uint(0) >> 1)
	if parsed > maxInt || parsed > uint64(maxDurationDays) {
		return 0, fmt.Errorf("day offset overflows time duration")
	}
	return int(parsed), nil
}

// TimeRange is an exact half-open UTC range. Its JSON field order is part of
// the series report contract.
type TimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Resolve converts a rolling day range into exact UTC boundaries relative to
// asOf. Requiring asOf from the caller keeps query tests independent of the
// wall clock.
func (r RollingRange) Resolve(asOf time.Time) (TimeRange, error) {
	if asOf.IsZero() {
		return TimeRange{}, fmt.Errorf("stats as-of instant is required")
	}
	if err := r.validate(); err != nil {
		return TimeRange{}, err
	}

	asOf = asOf.UTC()
	start := asOf.Add(-time.Duration(r.StartDays) * day)
	end := asOf.Add(-time.Duration(r.EndDays) * day)
	if !isRFC3339Time(start) || !isRFC3339Time(end) {
		return TimeRange{}, fmt.Errorf("stats rolling range is outside RFC3339 time bounds")
	}
	return TimeRange{Start: start, End: end}, nil
}

func (r RollingRange) validate() error {
	if r.StartDays < 0 || r.EndDays < 0 {
		return fmt.Errorf("stats rolling range day offsets must be non-negative")
	}
	if r.StartDays <= r.EndDays {
		return fmt.Errorf("stats rolling range start must be greater than end")
	}
	if int64(r.StartDays) > maxDurationDays || int64(r.EndDays) > maxDurationDays {
		return fmt.Errorf("stats rolling range day offset overflows time duration")
	}
	return nil
}

func isRFC3339Time(value time.Time) bool {
	return value.Year() >= 0 && value.Year() <= 9999
}

// TimeBucket is one exact half-open interval in a rolling series. Label keeps
// the requested day-offset form (for example, 7d-6d).
type TimeBucket struct {
	Label string    `json:"label"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Count int       `json:"count"`
}

// SeriesReport is the stable JSON shape for a rolling time-series query.
// TotalTasks is the sum of bucket counts, not the size of the whole store.
type SeriesReport struct {
	Attribute  SeriesMetric `json:"attribute"`
	TotalTasks int          `json:"totalTasks"`
	Range      TimeRange    `json:"range"`
	Buckets    []TimeBucket `json:"buckets"`
}

// SeriesOptions is the renderer-independent input to rolling aggregation.
// AsOf is mandatory so callers capture the clock once and tests can inject a
// deterministic instant.
type SeriesOptions struct {
	Metric   SeriesMetric
	Range    RollingRange
	AsOf     time.Time
	Grouping core.GroupingFilter
}

// ResolveRange validates the metric and resolves this query's exact window.
func (o SeriesOptions) ResolveRange() (TimeRange, error) {
	if err := o.Metric.Validate(); err != nil {
		return TimeRange{}, err
	}
	return o.Range.Resolve(o.AsOf)
}

// AggregateSeries loads each task once and counts its selected timestamp in
// the resolved half-open buckets. It never loads retained handoffs.
func AggregateSeries(p provider.Provider, options SeriesOptions) (SeriesReport, error) {
	if p == nil {
		return SeriesReport{}, fmt.Errorf("stats provider is nil")
	}
	rangeValue, err := options.ResolveRange()
	if err != nil {
		return SeriesReport{}, err
	}
	buckets, err := seriesBuckets(options.Range, rangeValue)
	if err != nil {
		return SeriesReport{}, err
	}
	tasks, err := p.ListTasks(provider.TaskFilter{Grouping: core.NormalizeGroupingFilter(options.Grouping)})
	if err != nil {
		return SeriesReport{}, fmt.Errorf("list tasks for stats series: %w", err)
	}

	counts := make([]int, len(buckets))
	for _, timestamp := range seriesTaskTimestamps(tasks, options.Metric) {
		if timestamp.Before(rangeValue.Start) || !timestamp.Before(rangeValue.End) {
			continue
		}
		index := int(timestamp.Sub(rangeValue.Start) / day)
		if index >= 0 && index < len(counts) {
			counts[index]++
		}
	}
	for index := range buckets {
		buckets[index].Count = counts[index]
	}

	total := 0
	for _, count := range counts {
		total += count
	}
	return SeriesReport{
		Attribute:  options.Metric,
		TotalTasks: total,
		Range:      rangeValue,
		Buckets:    buckets,
	}, nil
}

// BuildSeries is a descriptive alias for AggregateSeries.
func BuildSeries(p provider.Provider, options SeriesOptions) (SeriesReport, error) {
	return AggregateSeries(p, options)
}

func seriesBuckets(r RollingRange, rangeValue TimeRange) ([]TimeBucket, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	bucketCount := r.StartDays - r.EndDays
	buckets := make([]TimeBucket, bucketCount)
	for index := range buckets {
		start := rangeValue.Start.Add(time.Duration(index) * day)
		end := start.Add(day)
		startOffset := r.StartDays - index
		buckets[index] = TimeBucket{
			Label: fmt.Sprintf("%dd-%dd", startOffset, startOffset-1),
			Start: start,
			End:   end,
		}
	}
	return buckets, nil
}

func seriesTaskTimestamps(tasks []core.TaskView, metric SeriesMetric) []time.Time {
	// Providers return current task views, but retaining one timestamp per ID
	// keeps the task-counting invariant true if a provider ever duplicates one.
	seen := make(map[string]int, len(tasks))
	timestamps := make([]time.Time, 0, len(tasks))
	for _, task := range tasks {
		timestamp := task.CreatedAt
		if metric == SeriesMetricProgressed {
			timestamp = task.UpdatedAt
		}
		if task.ID == "" {
			timestamps = append(timestamps, timestamp)
			continue
		}
		if index, ok := seen[task.ID]; ok {
			if metric == SeriesMetricProgressed {
				if timestamp.After(timestamps[index]) {
					timestamps[index] = timestamp
				}
			} else if timestamps[index].IsZero() || (!timestamp.IsZero() && timestamp.Before(timestamps[index])) {
				timestamps[index] = timestamp
			}
			continue
		}
		seen[task.ID] = len(timestamps)
		timestamps = append(timestamps, timestamp)
	}
	return timestamps
}
