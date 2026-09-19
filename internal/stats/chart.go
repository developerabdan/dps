package stats

import (
	"fmt"
	"math"
	"strings"

	"github.com/developerabdan/dps/internal/model"
)

// sparkLevels are the heights of a one-line graph. The full block is left out
// on purpose: it fills the whole cell, so the graphs on two rows next to each
// other would touch and read as one shape.
var sparkLevels = []rune("▁▂▃▄▅▆▇")

// Spark draws the newest width values on one line, oldest on the left. A
// history shorter than width is padded with spaces on the left, so the graph
// grows in from the right the way a new value arrives. Every value, zero
// included, draws at least the lowest bar, so an idle container shows a flat
// line rather than a gap.
func Spark(values []float64, width int, top float64) string {
	values = Tail(values, width)
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", width-len(values)))
	for _, v := range values {
		i := int(math.Round(ratio(v, top) * float64(len(sparkLevels)-1)))
		b.WriteRune(sparkLevels[i])
	}
	return b.String()
}

// Top is the value the top of a graph stands for: the largest value times
// headroom, but never less than floor. The floor keeps noise flat — without
// it, a container that goes from 0.1% to 0.3% CPU would draw a full-height
// spike.
func Top(floor, headroom float64, series ...[]float64) float64 {
	top := 0.0
	for _, s := range series {
		for _, v := range s {
			top = max(top, v)
		}
	}
	return max(floor, top*headroom)
}

// Percent prints a CPU share in six cells, right-aligned, so the numbers in
// a column line up: "  0.4%", " 38.1%", "  812%".
func Percent(v float64) string {
	if v < 99.95 {
		return fmt.Sprintf("%5.1f%%", v)
	}
	return fmt.Sprintf("%5.0f%%", v)
}

// Bytes prints a size, with 0B for zero rather than nothing: in the stats
// view zero is a real reading, not a missing one.
func Bytes(v float64) string {
	if s := model.HumanBytes(int64(math.Round(v))); s != "" {
		return s
	}
	return "0B"
}

// Rate prints bytes per second.
func Rate(v float64) string { return Bytes(v) + "/s" }

// Tail returns the newest n values.
func Tail(values []float64, n int) []float64 {
	if n < 0 {
		n = 0
	}
	if len(values) > n {
		return values[len(values)-n:]
	}
	return values
}

func ratio(v, top float64) float64 {
	if top <= 0 || v <= 0 {
		return 0
	}
	return min(1, v/top)
}
