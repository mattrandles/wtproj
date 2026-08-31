package cli

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestRenderStatsChartExactOutput(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	tests := []struct {
		name    string
		buckets []chartBucket
		want    string
	}{
		{
			name: "empty",
		},
		{
			name: "zero-only",
			buckets: []chartBucket{
				{Label: "todo", Count: 0},
				{Label: "", Count: 0},
			},
			want: "   todo │ 0\n(unset) │ 0\n",
		},
		{
			name: "scaled",
			buckets: []chartBucket{
				{Label: "alpha", Count: 10},
				{Label: "beta", Count: 5},
				{Label: "one", Count: 1},
				{Label: "none", Count: 0},
			},
			want: "alpha │ ████████████████████████████████ 10\n" +
				" beta │ ████████████████ 5\n" +
				"  one │ ███ 1\n" +
				" none │ 0\n",
		},
		{
			name: "tied",
			buckets: []chartBucket{
				{Label: "first", Count: 4},
				{Label: "second", Count: 4},
				{Label: "zero", Count: 0},
			},
			want: " first │ ████████████████████████████████ 4\n" +
				"second │ ████████████████████████████████ 4\n" +
				"  zero │ 0\n",
		},
		{
			name: "long-label",
			buckets: []chartBucket{
				{Label: "a-very-long-label", Count: 2},
				{Label: "x", Count: 1},
			},
			want: "a-very-long-label │ ████████████████████████████████ 2\n" +
				"                x │ ████████████████ 1\n",
		},
		{
			name: "large-count",
			buckets: []chartBucket{
				{Label: "max", Count: maxInt},
				{Label: "half", Count: maxInt / 2},
				{Label: "small", Count: 1},
			},
			want: fmt.Sprintf("  max │ %s %d\n half │ %s %d\nsmall │ %s 1\n",
				strings.Repeat("█", 32), maxInt,
				strings.Repeat("█", scaledBarWidth(maxInt/2, maxInt)), maxInt/2,
				strings.Repeat("█", scaledBarWidth(1, maxInt))),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var got strings.Builder
			if err := renderStatsChart(&got, test.buckets); err != nil {
				t.Fatalf("renderStatsChart() error = %v", err)
			}
			if got.String() != test.want {
				t.Fatalf("renderStatsChart() = %q, want %q", got.String(), test.want)
			}
			if strings.Contains(got.String(), "\x1b[") {
				t.Fatal("renderStatsChart() emitted ANSI escape sequences")
			}
		})
	}
}

func TestScaledBarWidthGuaranteesOneCellForNonzeroValues(t *testing.T) {
	if got := scaledBarWidth(1, math.MaxInt); got != 1 {
		t.Fatalf("scaledBarWidth(1, MaxInt) = %d, want 1", got)
	}
	if got := scaledBarWidth(0, math.MaxInt); got != 0 {
		t.Fatalf("scaledBarWidth(0, MaxInt) = %d, want 0", got)
	}
	if got := scaledBarWidth(1, 0); got != 0 {
		t.Fatalf("scaledBarWidth(1, 0) = %d, want 0", got)
	}
}
