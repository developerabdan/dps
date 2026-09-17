package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/developerabdan/dps/internal/config"
	"github.com/developerabdan/dps/internal/table"
)

// picker is the column chooser inside the live view. Each tick is applied to
// the table under it at once, so the result is seen on real containers before
// it is saved. before is what to put back when it is cancelled.
type picker struct {
	cursor     int
	offset     int
	picked     map[string]bool
	before     []table.Column
	beforeSize bool
}

// minTableRows is how much of the table stays on screen under the picker. The
// table is how the choice is judged, so in a short window the list scrolls
// before the table goes.
const minTableRows = 3

// pickerRows is how many columns the picker lists at a time.
func (m Model) pickerRows() int {
	return max(1, min(len(table.Catalog), m.height-chromeLines-2-minTableRows))
}

// pickerLines is how many lines the picker takes above the table: a title,
// the list, and a blank.
func (m Model) pickerLines() int { return m.pickerRows() + 2 }

// first is the first column the list shows: it moves only as far as it must
// to keep the cursor in view.
func (p picker) first(rows int) int {
	start := p.offset
	if p.cursor < start {
		start = p.cursor
	}
	if p.cursor >= start+rows {
		start = p.cursor - rows + 1
	}
	return max(0, min(start, len(table.Catalog)-rows))
}

func (m Model) openPicker() (tea.Model, tea.Cmd) {
	m.pick = picker{picked: map[string]bool{}, before: m.cols, beforeSize: m.opt.Size}
	for _, c := range m.cols {
		m.pick.picked[c.Key] = true
	}
	m.mode = modePick
	m.ensureVisible()
	return m, nil
}

func (m Model) updatePick(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	keys := table.Keys()
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "escape", "q":
		m.mode = modeList
		m.cols = m.pick.before
		var cmds []tea.Cmd
		if m.opt.Size != m.pick.beforeSize {
			m.opt.Size = m.pick.beforeSize
			cmds = append(cmds, m.fetch(false))
		}
		m.ensureVisible()
		cmds = append(cmds, m.say(ansiDim, "columns unchanged"))
		return m, tea.Batch(cmds...)
	case "up", "k":
		m.pick.cursor = clamp(m.pick.cursor-1, len(keys))
	case "down", "j":
		m.pick.cursor = clamp(m.pick.cursor+1, len(keys))
	case "home", "g":
		m.pick.cursor = 0
	case "end", "G":
		m.pick.cursor = len(keys) - 1
	case " ", "space", "x":
		return m.togglePick(keys[m.pick.cursor])
	case "enter":
		return m.savePick()
	}
	m.pick.offset = m.pick.first(m.pickerRows())
	return m, nil
}

// togglePick ticks or unticks one column and applies the result at once. The
// last column cannot be unticked: a table with no columns has nothing to show.
func (m Model) togglePick(key string) (tea.Model, tea.Cmd) {
	if m.pick.picked[key] {
		if len(m.pick.picked) == 1 {
			return m, m.say(ansiYellow, "keep at least one column")
		}
		delete(m.pick.picked, key)
	} else {
		m.pick.picked[key] = true
	}

	cols, err := table.Resolve(ordered(colKeys(m.cols), m.pick.picked))
	if err != nil {
		return m, m.say(ansiRed, err.Error())
	}
	m.cols = cols
	m.ensureVisible()

	// The size column needs the list asked for with size=1, and the cpu
	// column needs samples. Both are started now rather than at the next poll.
	var cmds []tea.Cmd
	if size := m.pick.picked["size"]; size != m.opt.Size {
		m.opt.Size = size
		cmds = append(cmds, m.fetch(false))
	}
	cmds = append(cmds, m.sampleMissing())
	return m, tea.Batch(cmds...)
}

// savePick writes the columns as the saved default. Everything else in the
// file is kept as it was.
func (m Model) savePick() (tea.Model, tea.Cmd) {
	m.mode = modeList
	m.ensureVisible()
	cfg, err := config.Load()
	if err != nil {
		return m, m.say(ansiRed, "not saved: "+err.Error())
	}
	cfg.Cols = colKeys(m.cols)
	if err := cfg.Save(); err != nil {
		return m, m.say(ansiRed, "not saved: "+err.Error())
	}
	return m, m.say(ansiGreen, "columns saved as default")
}

func (m Model) viewPicker() string {
	var b strings.Builder
	title := fit(m.width, seg{"Columns", ansiBold}, seg{" · the table below shows the result", ansiDim})
	b.WriteString(title + "\n")
	keys, rows := table.Keys(), m.pickerRows()
	start := m.pick.first(rows)
	for i := start; i < start+rows; i++ {
		b.WriteString(pickLine(keys[i], m.pick.picked[keys[i]], i == m.pick.cursor, m.width) + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

// pickLine is one row of a column list, shared by the first-run wizard and the
// picker in the live view so the two read the same.
func pickLine(key string, picked, current bool, width int) string {
	mark, point := " ", "  "
	if picked {
		mark = "x"
	}
	if current {
		point = "› "
	}
	line := table.Truncate(fmt.Sprintf("  %s[%s] %-8s %s", point, mark, key, colDesc[key]), width, table.TruncTail)
	if current {
		line = ansiBold + line + ansiReset
	}
	return line
}

// ordered returns the picked keys in the order the table should print them.
// Keys already in current keep their order, because that order may have been
// set by hand with --set-cols. A newly ticked key goes in after the last key
// that comes before it in the catalog, so a set that was in catalog order
// stays in catalog order.
func ordered(current []string, picked map[string]bool) []string {
	rank := map[string]int{}
	for i, k := range table.Keys() {
		rank[k] = i
	}
	out := make([]string, 0, len(picked))
	have := map[string]bool{}
	for _, k := range current {
		if picked[k] && !have[k] {
			out = append(out, k)
			have[k] = true
		}
	}
	for _, k := range table.Keys() {
		if !picked[k] || have[k] {
			continue
		}
		at := 0
		for i, o := range out {
			if rank[o] < rank[k] {
				at = i + 1
			}
		}
		out = append(out[:at], append([]string{k}, out[at:]...)...)
		have[k] = true
	}
	return out
}

func colKeys(cols []table.Column) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Key
	}
	return out
}
