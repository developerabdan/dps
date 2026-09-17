package ui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/developerabdan/dps/internal/model"
	"github.com/developerabdan/dps/internal/stats"
	"github.com/developerabdan/dps/internal/table"
)

// liveCols gives the cpu column its value. The catalog cannot draw a graph —
// it sees one container at a time and keeps no history — so the view puts in
// a function that reads the history it collected.
func (m Model) liveCols(cols []table.Column) []table.Column {
	out := append([]table.Column(nil), cols...)
	for i := range out {
		if out[i].Key == "cpu" {
			out[i].Value = func(c model.Container) string {
				var values []float64
				if s := m.series[rowKey(c)]; s != nil {
					values = s.CPU.Values()
				}
				return cpuCell(values, c.State == "running")
			}
		}
	}
	return out
}

// cpuCell draws the graph and the latest percentage. A running container is
// always the full width, even before its first value, so the table does not
// change shape two seconds after it opens. A stopped one has no value.
func cpuCell(values []float64, running bool) string {
	if !running {
		return ""
	}
	window := stats.Tail(values, table.CPUBars)
	pct := fmt.Sprintf("%6s", "…")
	if len(values) > 0 {
		pct = stats.Percent(values[len(values)-1])
	}
	return stats.Spark(window, table.CPUBars, stats.Top(cpuFloor, 1, window)) + " " + pct
}

// graphPart returns the bars inside a cpu cell, so they can be coloured
// without the padding and the number around them.
func graphPart(cell string) string {
	first, last := -1, -1
	for i, r := range cell {
		if r >= '▁' && r <= '█' {
			if first < 0 {
				first = i
			}
			last = i + len(string(r))
		}
	}
	if first < 0 {
		return ""
	}
	return cell[first:last]
}

// selectedLine draws the cursor row in reverse video, except a graph. In
// reverse video the empty part of each cell turns solid and the bar turns
// into a gap, so the graph reads upside down.
func (m Model) selectedLine(cols []table.Column, widths []int, cells []string) string {
	out := ansiReverse + m.line(cols, widths, cells) + ansiReset
	for i, c := range cols {
		if c.Key != "cpu" || graphPart(cells[i]) == "" {
			continue
		}
		cell := table.Truncate(cells[i], widths[i], c.Trunc)
		out = strings.Replace(out, cell, ansiReset+ansiCyan+cell+ansiReset+ansiReverse, 1)
	}
	return out
}

func hasCol(cols []table.Column, key string) bool {
	for _, c := range cols {
		if c.Key == key {
			return true
		}
	}
	return false
}

func (m Model) indexOf(key string) int {
	for i, r := range m.rows {
		if rowKey(r) == key {
			return i
		}
	}
	return -1
}

// sampleKeys is every container something on screen draws a graph for.
func (m Model) sampleKeys() []string {
	var keys []string
	if hasCol(m.cols, "cpu") {
		for _, r := range m.rows {
			if r.State == "running" {
				keys = append(keys, rowKey(r))
			}
		}
	}
	if m.mode == modeStats {
		if i := m.indexOf(m.statsKey); i >= 0 && m.rows[i].State == "running" {
			found := false
			for _, k := range keys {
				found = found || k == m.statsKey
			}
			if !found {
				keys = append(keys, m.statsKey)
			}
		}
	}
	return keys
}

// sample starts one round of stats requests. Nothing is asked for when no
// graph is on screen, so a table without the cpu column costs the daemon
// nothing more than it did before.
func (m *Model) sample() tea.Cmd {
	keys := m.sampleKeys()
	if len(keys) == 0 || m.sampling || m.client == nil {
		return nil
	}
	m.sampling = true
	client, parent := m.client, m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, statsTimeout)
		defer cancel()
		return statsMsg{samples: client.StatsFor(ctx, keys)}
	}
}

// sampleMissing samples at once when a container on screen has no history
// yet, instead of waiting for the next poll.
func (m *Model) sampleMissing() tea.Cmd {
	for _, k := range m.sampleKeys() {
		if m.series[k] == nil {
			return m.sample()
		}
	}
	return nil
}

// pruneSeries forgets containers that are no longer on the list.
func (m *Model) pruneSeries() {
	for k := range m.series {
		if m.indexOf(k) < 0 {
			delete(m.series, k)
		}
	}
}
