package table

import (
	"sort"

	"github.com/developerabdan/dps/internal/model"
)

// Gap is the blank space between two columns.
const Gap = 2

// Fit decides what actually gets printed at the given terminal width.
//
// Columns first take their natural width, then the widest column still above
// its floor gives back one cell at a time. When nothing can shrink further the
// lowest-priority column is dropped and the measurement restarts. Dropped
// columns are returned so the caller can name them: a column that vanished
// without explanation reads as missing data.
func Fit(cols []Column, rows []model.Container, width int) (active []Column, widths []int, dropped []Column) {
	active = append([]Column(nil), cols...)
	dropped = []Column{}

	for {
		widths = natural(active, rows)
		floors := floorsFor(active, widths)

		for total(widths) > width {
			i := widestAboveFloor(widths, floors)
			if i < 0 {
				break
			}
			widths[i]--
		}

		if total(widths) <= width || len(active) <= 1 {
			return active, widths, dropped
		}

		victim := lowestPriority(active)
		if victim < 0 {
			return active, widths, dropped
		}
		dropped = append(dropped, active[victim])
		active = append(active[:victim:victim], active[victim+1:]...)
	}
}

// natural is the longest cell including the header, capped at the column's
// own maximum.
func natural(cols []Column, rows []model.Container) []int {
	w := make([]int, len(cols))
	for i, c := range cols {
		n := Width(c.Header)
		for _, r := range rows {
			v := c.Value(r)
			if v == "" {
				v = Empty
			}
			if l := Width(v); l > n {
				n = l
			}
		}
		if n > c.Max {
			n = c.Max
		}
		w[i] = n
	}
	return w
}

// floorsFor clamps each column's configured minimum to its natural width, so
// a column whose content is already short is never padded back out.
func floorsFor(cols []Column, widths []int) []int {
	f := make([]int, len(cols))
	for i, c := range cols {
		f[i] = c.Min
		if widths[i] < f[i] {
			f[i] = widths[i]
		}
	}
	return f
}

func total(widths []int) int {
	if len(widths) == 0 {
		return 0
	}
	sum := Gap * (len(widths) - 1)
	for _, w := range widths {
		sum += w
	}
	return sum
}

func widestAboveFloor(widths, floors []int) int {
	best, bestW := -1, 0
	for i := range widths {
		if widths[i] > floors[i] && widths[i] > bestW {
			best, bestW = i, widths[i]
		}
	}
	return best
}

// lowestPriority picks the highest Prio number, breaking ties toward the
// rightmost column. Priority 1 is never a candidate.
func lowestPriority(cols []Column) int {
	idx := make([]int, 0, len(cols))
	for i, c := range cols {
		if c.Prio > 1 {
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		return -1
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ca, cb := cols[idx[a]], cols[idx[b]]
		if ca.Prio != cb.Prio {
			return ca.Prio > cb.Prio
		}
		return idx[a] > idx[b]
	})
	return idx[0]
}
