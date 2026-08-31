package cli

import (
	"fmt"
	"io"
	"math/bits"
	"strings"
	"unicode/utf8"
)

const statsChartMaxBarWidth = 32

// chartBucket is the renderer's deliberately small input contract. Callers
// decide which buckets to show and in what order; the renderer only formats
// their labels and counts.
type chartBucket struct {
	Label string
	Count int
}

// renderStatsChart writes one aligned horizontal bar per bucket. Bars are
// scaled against the largest positive count and never exceed 32 cells. The
// integer arithmetic keeps large counts exact without an intermediate
// count*32 overflow.
func renderStatsChart(w io.Writer, buckets []chartBucket) error {
	maxCount := 0
	labelWidth := 0
	labels := make([]string, len(buckets))
	for index, bucket := range buckets {
		label := bucket.Label
		if label == "" {
			label = "(unset)"
		}
		labels[index] = label
		if width := utf8.RuneCountInString(label); width > labelWidth {
			labelWidth = width
		}
		if bucket.Count > maxCount {
			maxCount = bucket.Count
		}
	}

	for index, bucket := range buckets {
		barWidth := scaledBarWidth(bucket.Count, maxCount)
		if barWidth == 0 {
			if _, err := fmt.Fprintf(w, "%*s │ %d\n", labelWidth, labels[index], bucket.Count); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(w, "%*s │ %s %d\n", labelWidth, labels[index], strings.Repeat("█", barWidth), bucket.Count); err != nil {
			return err
		}
	}
	return nil
}

func scaledBarWidth(count, maxCount int) int {
	if count <= 0 || maxCount <= 0 {
		return 0
	}

	// bits.Div64 divides the exact 128-bit product count*32 without
	// overflowing when counts are near the platform's maximum int.
	high, low := bits.Mul64(uint64(count), statsChartMaxBarWidth)
	width, _ := bits.Div64(high, low, uint64(maxCount))
	if width == 0 {
		return 1
	}
	if width > statsChartMaxBarWidth {
		return statsChartMaxBarWidth
	}
	return int(width)
}
