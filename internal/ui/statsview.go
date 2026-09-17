package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/developerabdan/dps/internal/model"
	"github.com/developerabdan/dps/internal/stats"
	"github.com/developerabdan/dps/internal/table"
)

// statsIndent is the margin on both sides of the stats view.
const statsIndent = "  "

// statsFixed is how many lines of the stats view are not chart: the title and
// the blank under it, one label for each of the four panels, the three blanks
// between them, and the blank, status and help at the bottom.
const statsFixed = 2 + 4 + 3 + 3

// maxChartHeight stops the charts at a height where one more line adds no
// detail anyone reads. A tall window keeps the rest empty.
const maxChartHeight = 8

// chartHeadroom keeps the highest value under the top of its chart, so a
// steady level draws a line with space above it rather than a solid wall.
const chartHeadroom = 1.25

// The floors are the least the top of each chart stands for, for the same
// reason as cpuFloor: noise should look like noise.
const (
	memFloor  = 16e6
	diskFloor = 100e3
	netFloor  = 10e3
)

// openStats opens the stats view on the selected container. A stopped
// container has nothing to sample, so that is said instead.
func (m Model) openStats() (tea.Model, tea.Cmd) {
	if len(m.rows) == 0 {
		return m, nil
	}
	r := m.rows[m.cursor]
	if r.State != "running" {
		return m, m.say(ansiYellow, r.Name+" is not running")
	}
	m.mode = modeStats
	m.statsKey, m.statsName = rowKey(r), r.Name
	cmd := m.sampleMissing()
	return m, cmd
}

func (m Model) updateStats(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q", "s", "backspace", "left", "h":
		m.mode = modeList
		m.ensureVisible()
	case "up", "k":
		return m.stepStats(-1)
	case "down", "j":
		return m.stepStats(1)
	}
	return m, nil
}

// stepStats moves the view to the next running container above or below.
// Stopped containers are passed over: they have no numbers to show.
func (m Model) stepStats(dir int) (tea.Model, tea.Cmd) {
	for i := m.cursor + dir; i >= 0 && i < len(m.rows); i += dir {
		if m.rows[i].State != "running" {
			continue
		}
		m.cursor = i
		m.ensureVisible()
		m.statsKey, m.statsName = rowKey(m.rows[i]), m.rows[i].Name
		cmd := m.sampleMissing()
		return m, cmd
	}
	return m, nil
}

func (m Model) viewStats() string {
	var r model.Container
	if i := m.indexOf(m.statsKey); i >= 0 {
		r = m.rows[i]
	}
	s := m.series[m.statsKey]
	if s == nil {
		s = &stats.Series{}
	}
	running := r.State == "running"

	width := max(m.width-2*len(statsIndent), 8)
	height := min(maxChartHeight, max(1, (m.height-statsFixed)/4))

	lines := []string{statsIndent + fit(width,
		seg{m.statsName, ansiBold},
		seg{"  " + model.ShortState(r.State, r.Status), stateColor(r.State)},
		seg{"  " + model.ShortImage(r.Image), ansiDim},
	), ""}

	cpu := s.CPU.Values()
	cpuText, cpuNote := reading(&s.CPU, running, func(v float64) string {
		return strings.TrimSpace(stats.Percent(v))
	}), ""
	if n := s.Latest.OnlineCPUs; n > 0 {
		cpuNote = fmt.Sprintf(" · %d CPUs", n)
	}
	top := stats.Top(cpuFloor, chartHeadroom, cpu)
	lines = append(lines, panel("CPU", cpuText, cpuNote,
		"0–"+strings.TrimSpace(stats.Percent(top)), cpu, top, width, height)...)
	lines = append(lines, "")

	mem := s.Mem.Values()
	memNote := ""
	if limit, used := float64(s.Latest.MemLimit), float64(s.Latest.MemUsage); limit > 0 && s.Mem.Len() > 0 {
		memNote = fmt.Sprintf(" / %s · %.1f%%", stats.Bytes(limit), used/limit*100)
	}
	top = stats.Top(memFloor, chartHeadroom, mem)
	lines = append(lines, panel("MEMORY", reading(&s.Mem, running, stats.Bytes), memNote,
		"0–"+stats.Bytes(top), mem, top, width, height)...)
	lines = append(lines, "")

	read, write := s.DiskRead.Values(), s.DiskWrite.Values()
	top = stats.Top(diskFloor, chartHeadroom, read, write)
	lines = append(lines, dualPanel("DISK",
		half{"read", reading(&s.DiskRead, running, stats.Rate), s.Latest.DiskRead, read},
		half{"write", reading(&s.DiskWrite, running, stats.Rate), s.Latest.DiskWrite, write},
		"0–"+stats.Rate(top), top, width, height)...)
	lines = append(lines, "")

	rx, tx := s.NetRx.Values(), s.NetTx.Values()
	top = stats.Top(netFloor, chartHeadroom, rx, tx)
	lines = append(lines, dualPanel("NETWORK",
		half{"in", reading(&s.NetRx, running, stats.Rate), s.Latest.NetRx, rx},
		half{"out", reading(&s.NetTx, running, stats.Rate), s.Latest.NetTx, tx},
		"0–"+stats.Rate(top), top, width, height)...)

	// The frame must not be taller than the window, so a short window loses
	// the bottom of the charts rather than the status and help lines.
	if n := max(m.height-3, 1); len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n") + "\n\n" + m.statsStatus(r, s) + "\n" + m.help()
}

// statsStatus is the status line of the stats view.
func (m Model) statsStatus(r model.Container, s *stats.Series) string {
	var parts []string
	if n := s.Latest.PIDs; n > 0 {
		parts = append(parts, fmt.Sprintf("%d processes", n))
	}
	if n := s.CPU.Len(); n > 0 {
		parts = append(parts, fmt.Sprintf("%s of history", s.Span(refreshInterval)))
	} else if r.State == "running" {
		parts = append(parts, "collecting samples…")
	}
	parts = append(parts, fmt.Sprintf("a sample every %s", refreshInterval))

	var lead []seg
	switch {
	case m.notice != "":
		lead = append(lead, seg{m.notice, m.noticeColor}, seg{" · ", ansiDim})
	case r.State != "running":
		lead = append(lead, seg{"not running · no new samples", ansiYellow}, seg{" · ", ansiDim})
	}
	return fit(m.width, append(lead, seg{strings.Join(parts, " · "), ansiDim})...)
}

// reading is the newest value of a series as text: … while the first samples
// are still on the way, and a dash when no more will come.
func reading(r *stats.Ring, running bool, format func(float64) string) string {
	if v, ok := r.Last(); ok {
		return format(v)
	}
	if running {
		return "…"
	}
	return table.Empty
}

// panel is one chart the full width of the view, under a label line.
func panel(name, value, note, scale string, values []float64, top float64, width, height int) []string {
	out := []string{statsIndent + label(width,
		[]seg{{table.Pad(name, 9), ansiBold}, {value, ""}, {note, ansiDim}},
		seg{scale, ansiDim})}
	for _, l := range stats.Chart(values, width, height, top) {
		out = append(out, statsIndent+ansiCyan+l+ansiReset)
	}
	return out
}

// half is one side of a panel that draws two series, such as read and write.
type half struct {
	name   string
	value  string
	total  uint64
	values []float64
}

// dualPanel draws two series side by side on one scale, so the larger one can
// be seen to be larger.
func dualPanel(name string, a, b half, scale string, top float64, width, height int) []string {
	const gap = "   "
	left := (width - len(gap)) / 2
	right := width - len(gap) - left

	head := fit(left,
		seg{table.Pad(name, 9), ansiBold},
		seg{a.name + " " + a.value, ""},
		seg{" · " + stats.Bytes(float64(a.total)) + " total", ansiDim})
	head += strings.Repeat(" ", left+len(gap)-visibleWidth(head))
	head += label(right,
		[]seg{{b.name + " " + b.value, ""}, {" · " + stats.Bytes(float64(b.total)) + " total", ansiDim}},
		seg{scale, ansiDim})

	out := []string{statsIndent + head}
	ls, rs := stats.Chart(a.values, left, height, top), stats.Chart(b.values, right, height, top)
	for i := range ls {
		out = append(out, statsIndent+ansiCyan+ls[i]+ansiReset+gap+ansiCyan+rs[i]+ansiReset)
	}
	return out
}

// seg is a run of text with one colour.
type seg struct{ text, color string }

// fit writes the segments one after another in at most width cells. The
// segment that crosses the edge is cut and the rest are dropped. Colour goes
// on after the cut, because escape codes count as runes but print no cells.
func fit(width int, segs ...seg) string {
	var b strings.Builder
	room := width
	for _, s := range segs {
		if room <= 0 {
			break
		}
		t := table.Truncate(s.text, room, table.TruncTail)
		room -= table.Width(t)
		if s.color != "" && t != "" {
			t = s.color + t + ansiReset
		}
		b.WriteString(t)
	}
	return b.String()
}

// label writes left in width cells and puts right against the right edge when
// there is room for it with two spaces to spare. The scale is the first thing
// a narrow window gives up.
func label(width int, left []seg, right seg) string {
	head := fit(width, left...)
	used := visibleWidth(head)
	if w := table.Width(right.text); right.text != "" && used+2+w <= width {
		head += strings.Repeat(" ", width-used-w) + right.color + right.text + ansiReset
	}
	return head
}

// visibleWidth counts the cells a string prints, skipping escape codes.
func visibleWidth(s string) int {
	n, esc := 0, false
	for _, r := range s {
		switch {
		case esc:
			esc = r != 'm'
		case r == 0x1b:
			esc = true
		default:
			n++
		}
	}
	return n
}

func stateColor(state string) string {
	switch state {
	case "running":
		return ansiGreen
	case "restarting", "paused", "created", "removing":
		return ansiYellow
	}
	return ansiRed
}
