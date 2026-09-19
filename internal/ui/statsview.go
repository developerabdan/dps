package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

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

// statsFramedFixed is the same count for the framed view, where each panel
// also has the two sides of its box and a line of times under it.
const statsFramedFixed = 2 + 4*4 + 3 + 3

// minFramedChart is the shortest chart worth a frame. Below it the four lines
// the frame costs are better spent on the chart itself, so the view drops the
// box and puts the scale back on the label line.
const minFramedChart = 4

// minFramedInner is the narrowest chart worth a frame, for the same reason:
// the box and its scale take cells from the side.
const minFramedInner = 20

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
	// The frame is worth its lines only in a window tall enough to keep the
	// charts readable with them gone. A short window gets the bare charts.
	framed := false
	height := min(maxChartHeight, max(1, (m.height-statsFixed)/4))
	if h := min(maxChartHeight, (m.height-statsFramedFixed)/4); h >= minFramedChart {
		framed, height = true, h
	}

	lines := []string{statsIndent + fit(width,
		seg{m.statsName, ansiBold},
		seg{"  " + model.ShortState(r.State, r.Status), stateColor(r.State)},
		seg{"  " + model.ShortImage(r.Image), ansiDim},
	), ""}

	cpu := s.CPU.Values()
	cpuNote := ""
	if n := s.Latest.OnlineCPUs; n > 0 {
		cpuNote = fmt.Sprintf(" · %d CPUs", n)
	}
	cpuTop := stats.Top(cpuFloor, chartHeadroom, cpu)
	panels := []chartPanel{{
		name: "CPU",
		head: []seg{
			{reading(&s.CPU, running, func(v float64) string {
				return strings.TrimSpace(stats.Percent(v))
			}), ""},
			{cpuNote, ansiDim},
		},
		scale: strings.TrimSpace(stats.Percent(cpuTop)),
		top:   cpuTop,
		lines: []chartLine{{cpu, ansiCyan}},
	}}

	mem := s.Mem.Values()
	memNote := ""
	if limit, used := float64(s.Latest.MemLimit), float64(s.Latest.MemUsage); limit > 0 && s.Mem.Len() > 0 {
		memNote = fmt.Sprintf(" / %s · %.1f%%", stats.Bytes(limit), used/limit*100)
	}
	memTop := stats.Top(memFloor, chartHeadroom, mem)
	panels = append(panels, chartPanel{
		name:  "MEMORY",
		head:  []seg{{reading(&s.Mem, running, stats.Bytes), ""}, {memNote, ansiDim}},
		scale: stats.Bytes(memTop),
		top:   memTop,
		lines: []chartLine{{mem, ansiCyan}},
	})

	read, write := s.DiskRead.Values(), s.DiskWrite.Values()
	diskTop := stats.Top(diskFloor, chartHeadroom, read, write)
	panels = append(panels, chartPanel{
		name: "DISK",
		head: pair(
			part{"read", reading(&s.DiskRead, running, stats.Rate), s.Latest.DiskRead},
			part{"write", reading(&s.DiskWrite, running, stats.Rate), s.Latest.DiskWrite}),
		scale: stats.Rate(diskTop),
		top:   diskTop,
		lines: []chartLine{{read, ansiCyan}, {write, ansiYellow}},
	})

	rx, tx := s.NetRx.Values(), s.NetTx.Values()
	netTop := stats.Top(netFloor, chartHeadroom, rx, tx)
	panels = append(panels, chartPanel{
		name: "NETWORK",
		head: pair(
			part{"in", reading(&s.NetRx, running, stats.Rate), s.Latest.NetRx},
			part{"out", reading(&s.NetTx, running, stats.Rate), s.Latest.NetTx}),
		scale: stats.Rate(netTop),
		top:   netTop,
		lines: []chartLine{{rx, ansiCyan}, {tx, ansiYellow}},
	})

	// One gutter for every panel, so the boxes stand in a column rather than
	// each starting where its own scale happens to end.
	gutter := 0
	for _, p := range panels {
		gutter = max(gutter, table.Width(p.scale))
	}
	for i, p := range panels {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, p.render(width, height, gutter, framed)...)
	}

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

// chartPanel is one block of the stats view: a label line and, under it, the
// chart of one or two series.
type chartPanel struct {
	name  string
	head  []seg
	scale string // what the top of the chart stands for
	top   float64
	lines []chartLine
}

// chartLine is one series and the colour it is drawn in. Where two series
// share a cell the first one gives it its colour, so the colour of the name
// on the label line is the key to the chart.
type chartLine struct {
	values []float64
	color  string
}

// part is one of the two series of a panel that draws a pair, such as the
// read and write sides of DISK.
type part struct {
	name  string
	value string
	total uint64
}

// pair writes the label line of a panel that draws two series. The name of
// each one takes the colour of its line, which is the only legend the chart
// needs.
func pair(a, b part) []seg {
	return []seg{
		{a.name + " ", ansiCyan}, {a.value, ""},
		{" · " + stats.Bytes(float64(a.total)) + " total", ansiDim},
		{"   ", ""},
		{b.name + " ", ansiYellow}, {b.value, ""},
		{" · " + stats.Bytes(float64(b.total)) + " total", ansiDim},
	}
}

func (p chartPanel) render(width, height, gutter int, framed bool) []string {
	if framed {
		if out, ok := p.framed(width, height, gutter); ok {
			return out
		}
	}
	return p.bare(width, height)
}

// bare is the panel without a box: the scale sits on the label line, and the
// chart has the whole width.
func (p chartPanel) bare(width, height int) []string {
	out := []string{statsIndent + label(width,
		append([]seg{{table.Pad(p.name, 9), ansiBold}}, p.head...),
		seg{"0–" + p.scale, ansiDim})}
	for _, l := range p.draw(width, height) {
		out = append(out, statsIndent+l)
	}
	return out
}

// framed is the panel with an axis around the chart: the scale at the top
// left corner, the zero at the bottom one, and how far back the chart reaches
// under it. It returns false when the box would leave too little chart.
func (p chartPanel) framed(width, height, gutter int) ([]string, bool) {
	inner := width - gutter - 3 // the gutter, a space, and the two sides
	if inner < minFramedInner {
		return nil, false
	}
	pad := strings.Repeat(" ", gutter+1)
	rule := strings.Repeat("─", inner)

	out := []string{statsIndent + fit(width, append([]seg{{p.name + "  ", ansiBold}}, p.head...)...)}
	out = append(out, statsIndent+ansiDim+rightPad(p.scale, gutter)+" ┌"+rule+"┐"+ansiReset)
	for _, l := range p.draw(inner, height) {
		out = append(out, statsIndent+ansiDim+pad+"│"+ansiReset+l+ansiDim+"│"+ansiReset)
	}
	out = append(out, statsIndent+ansiDim+rightPad("0", gutter)+" └"+rule+"┘"+ansiReset)
	out = append(out, statsIndent+pad+label(inner+2,
		[]seg{{ago(chartWindow(inner)), ansiDim}}, seg{"now", ansiDim}))
	return out, true
}

// rightPad puts s against the right edge of w cells, so the scale and the
// zero under it end where the box begins.
func rightPad(s string, w int) string {
	if n := w - table.Width(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}

// draw renders the series as dots, one line of the chart per string. Runs of
// the same colour share one escape code, because a code for every cell would
// make a wide chart several times the bytes it needs to be.
func (p chartPanel) draw(width, height int) []string {
	grids := make([]stats.Grid, len(p.lines))
	for i, l := range p.lines {
		grids[i] = stats.Dots(l.values, width, height, p.top)
	}
	out := make([]string, height)
	for row := range out {
		var b strings.Builder
		open := ""
		for col := range width {
			var mask uint8
			color := ""
			for i, g := range grids {
				if m := g.At(row, col); m != 0 {
					mask |= m
					if color == "" {
						color = p.lines[i].color
					}
				}
			}
			if color != open {
				if open != "" {
					b.WriteString(ansiReset)
				}
				b.WriteString(color)
				open = color
			}
			b.WriteRune(stats.Dot(mask))
		}
		if open != "" {
			b.WriteString(ansiReset)
		}
		out[row] = b.String()
	}
	return out
}

// chartWindow is how much time a chart width cells wide covers. Two samples
// go in each cell, and the history never holds more than it keeps.
func chartWindow(width int) time.Duration {
	return time.Duration(min(width*2, stats.History)) * refreshInterval
}

// ago writes a duration as one token for the left end of a time axis.
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s ago"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m ago"
	default:
		return strconv.Itoa(int(d.Hours())) + "h ago"
	}
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
