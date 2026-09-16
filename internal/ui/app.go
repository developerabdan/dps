// Package ui is the interactive view, shown only when stdout is a terminal.
// It reuses the same column catalog and width-fitting as the plain table, so
// both modes agree about what a container looks like.
package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/developerabdan/dps/internal/dockerapi"
	"github.com/developerabdan/dps/internal/model"
	"github.com/developerabdan/dps/internal/table"
	"github.com/developerabdan/dps/internal/term"
)

const (
	ansiReset   = "\x1b[0m"
	ansiBold    = "\x1b[1m"
	ansiDim     = "\x1b[2m"
	ansiReverse = "\x1b[7m"
	ansiGreen   = "\x1b[32m"
	ansiRed     = "\x1b[31m"
	ansiYellow  = "\x1b[33m"
)

// refreshInterval is the fallback poll. The event feed is the better source
// and replaces this once wired.
const refreshInterval = 2 * time.Second

// chromeLines is how many rows the frame spends on parts that do not scroll:
// the header, the blank line, the status and the help. It is a constant so
// that Update can work out the viewport height without rendering a frame.
const chromeLines = 4

// scrollJump is how far one wheel notch moves the window.
const scrollJump = 3

type (
	rowsMsg []model.Container
	errMsg  struct{ err error }
	tickMsg struct{}
)

// Model is the Bubble Tea model backing the interactive table.
type Model struct {
	ctx    context.Context
	client *dockerapi.Client
	opt    dockerapi.ListOptions
	cols   []table.Column

	rows   []model.Container
	cursor int
	width  int
	height int
	err    error

	// offset is the first body line on screen. The list is taller than the
	// window far more often than not, so the view draws a slice of it and this
	// says where that slice starts. It counts printed lines, not containers,
	// because group headings take a line too.
	offset int

	// group draws project headings. When it is set, rows are stored in
	// grouped order so the cursor index and the printed order stay the same
	// thing — otherwise the highlight lands on a different container than
	// the one the arrow keys moved to.
	group bool
}

// Run opens the interactive view and blocks until the user quits.
func Run(ctx context.Context, client *dockerapi.Client, cols []table.Column, opt dockerapi.ListOptions, group bool) error {
	m := Model{
		ctx:    ctx,
		client: client,
		opt:    opt,
		cols:   cols,
		group:  group,
		width:  term.FallbackWidth,
		height: 24,
	}
	_, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.fetch(), tick())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// A shorter window can leave the cursor below the fold, so the window
		// follows it rather than the other way round.
		m.ensureVisible()
		return m, nil

	case rowsMsg:
		// Store rows in the order they will be drawn. The cursor is a single
		// index into this slice, so if the display order differed the
		// highlight would fall on the wrong container.
		m.rows = msg
		if m.group {
			ordered := make([]model.Container, 0, len(msg))
			for _, g := range table.GroupByProject(msg) {
				ordered = append(ordered, g.Rows...)
			}
			m.rows = ordered
		}
		m.err = nil
		m.clampCursor()
		m.ensureVisible()
		return m, nil

	case errMsg:
		m.err = msg.err
		return m, nil

	case tickMsg:
		return m, tea.Batch(m.fetch(), tick())

	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			m.scroll(-scrollJump)
		case tea.MouseWheelDown:
			m.scroll(scrollJump)
		}
		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "up", "k":
			m.moveCursor(-1)
		case "down", "j":
			m.moveCursor(1)
		case "pgup", "ctrl+b":
			m.moveCursor(-m.bodyHeight())
		case "pgdown", "ctrl+f", " ":
			m.moveCursor(m.bodyHeight())
		case "ctrl+u":
			m.moveCursor(-m.bodyHeight() / 2)
		case "ctrl+d":
			m.moveCursor(m.bodyHeight() / 2)
		case "g", "home":
			m.cursor = 0
			m.ensureVisible()
		case "G", "end":
			m.cursor = len(m.rows) - 1
			m.clampCursor()
			m.ensureVisible()
		case "a":
			m.opt.All = !m.opt.All
			m.cursor = 0
			m.offset = 0
			return m, m.fetch()
		case "r":
			return m, m.fetch()
		}
	}
	return m, nil
}

// altView builds a frame that owns the alternate screen. In v2 the alt screen
// is a property of the view rather than a program option, so every return
// path in View must set it — a frame that forgets drops the program back to
// the main screen and the display flickers between the two.
func altView(s string) tea.View {
	v := tea.NewView(s)
	v.AltScreen = true
	// Wheel events only arrive while the view asks for them, and a list that
	// scrolls is expected to answer the wheel.
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m Model) View() tea.View {
	if m.err != nil {
		return altView(fmt.Sprintf("dps: %v\n\n%spress q to quit%s\n", m.err, ansiDim, ansiReset))
	}
	if len(m.rows) == 0 {
		return altView(fmt.Sprintf("%sno containers%s\n\n%s", ansiDim, ansiReset, m.help()))
	}

	// Indent inside the first column rather than shifting the whole line, so
	// the other columns stay on their axis. This is the same rule the plain
	// table uses.
	cols := m.cols
	if m.group {
		cols = table.IndentFirst(cols, table.GroupIndent)
	}
	active, widths, dropped := table.Fit(cols, m.rows, m.width)

	// Only the body scrolls. m is a copy here, so clamping the offset against
	// the current line count costs nothing and keeps a stale offset — rows
	// removed since the last frame — from cutting past the end.
	slots := m.slots()
	m.clampOffset(len(slots))
	height := m.bodyHeight()
	end := m.offset + height
	if end > len(slots) {
		end = len(slots)
	}

	var b strings.Builder
	b.WriteString(ansiDim)
	b.WriteString(m.line(active, widths, headerCells(active)))
	b.WriteString(ansiReset)
	b.WriteByte('\n')

	for _, s := range slots[m.offset:end] {
		switch {
		case s.row >= 0:
			r := m.rows[s.row]
			cells := make([]string, len(active))
			for j, c := range active {
				v := c.Value(r)
				if v == "" {
					v = table.Empty
				}
				cells[j] = v
			}
			if s.row == m.cursor {
				b.WriteString(ansiReverse + m.line(active, widths, cells) + ansiReset)
			} else {
				b.WriteString(colorize(active, r, cells, widths, m.line))
			}
		case s.heading != "":
			head := table.Truncate(s.heading, m.width, table.TruncTail)
			b.WriteString(ansiBold + head + ansiReset)
			if table.Width(head)+table.Width(s.count) <= m.width {
				b.WriteString(ansiDim + s.count + ansiReset)
			}
		}
		// A spacer slot writes nothing, so this closes every line.
		b.WriteByte('\n')
	}

	b.WriteByte('\n')
	b.WriteString(m.status(dropped, len(slots) > height))
	b.WriteByte('\n')
	b.WriteString(m.help())
	return altView(b.String())
}

// slot is one printed line of the scrolling body. A container row carries its
// index into m.rows; a group heading and the blank line between groups carry
// -1. Update reads the same mapping the view draws from, so the two never
// disagree about which line the cursor is on.
type slot struct {
	row     int
	heading string
	count   string
}

func (m Model) slots() []slot {
	if !m.group {
		out := make([]slot, len(m.rows))
		for i := range m.rows {
			out[i] = slot{row: i}
		}
		return out
	}

	// m.rows is already stored in grouped order, so this counter walks the
	// same indices the cursor uses.
	out := make([]slot, 0, len(m.rows)+8)
	flat := 0
	for gi, g := range table.GroupByProject(m.rows) {
		if gi > 0 {
			out = append(out, slot{row: -1})
		}
		var up int
		for _, r := range g.Rows {
			if r.State == "running" {
				up++
			}
		}
		out = append(out, slot{
			row:     -1,
			heading: g.Name,
			count:   fmt.Sprintf("  %d/%d up", up, len(g.Rows)),
		})
		for range g.Rows {
			out = append(out, slot{row: flat})
			flat++
		}
	}
	return out
}

// bodyHeight is how many lines the scrolling region gets.
func (m Model) bodyHeight() int {
	if h := m.height - chromeLines; h > 0 {
		return h
	}
	return 1
}

func (m Model) cursorLine(slots []slot) int {
	for i, s := range slots {
		if s.row == m.cursor {
			return i
		}
	}
	return 0
}

// ensureVisible moves the window the shortest distance that brings the cursor
// back on screen. When the cursor sits on the first row of a group the heading
// above it comes along, otherwise the row appears with no project to belong to.
func (m *Model) ensureVisible() {
	slots := m.slots()
	line := m.cursorLine(slots)
	top := line
	if m.group && top > 0 && slots[top-1].heading != "" {
		top--
	}
	if top < m.offset {
		m.offset = top
	}
	if h := m.bodyHeight(); line >= m.offset+h {
		m.offset = line - h + 1
	}
	m.clampOffset(len(slots))
}

func (m *Model) clampOffset(total int) {
	if max := total - m.bodyHeight(); m.offset > max {
		m.offset = max
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *Model) moveCursor(delta int) {
	m.cursor += delta
	m.clampCursor()
	m.ensureVisible()
}

// scroll moves the window and leaves the cursor where it is, which is what a
// wheel is expected to do. The cursor is dragged along only once it would go
// off screen, so the highlight is never on a row the reader cannot see.
func (m *Model) scroll(delta int) {
	slots := m.slots()
	m.offset += delta
	m.clampOffset(len(slots))
	m.pullCursorIntoView(slots)
}

func (m *Model) pullCursorIntoView(slots []slot) {
	height := m.bodyHeight()
	line := m.cursorLine(slots)
	if line >= m.offset && line < m.offset+height {
		return
	}
	end := m.offset + height
	if end > len(slots) {
		end = len(slots)
	}
	if line < m.offset {
		for i := m.offset; i < end; i++ {
			if slots[i].row >= 0 {
				m.cursor = slots[i].row
				return
			}
		}
		return
	}
	for i := end - 1; i >= m.offset; i-- {
		if slots[i].row >= 0 {
			m.cursor = slots[i].row
			return
		}
	}
}

// line assembles one row of already-chosen cell values.
func (m Model) line(cols []table.Column, widths []int, cells []string) string {
	var b strings.Builder
	for i, c := range cols {
		cell := table.Truncate(cells[i], widths[i], c.Trunc)
		if i < len(cols)-1 {
			cell = table.Pad(cell, widths[i])
			b.WriteString(cell)
			b.WriteString(strings.Repeat(" ", table.Gap))
			continue
		}
		b.WriteString(cell)
	}
	return b.String()
}

// status is one line, always. The hidden-column notice rides on it rather than
// taking a second line, because chromeLines has to be a constant for the
// viewport arithmetic to hold.
func (m Model) status(dropped []table.Column, clipped bool) string {
	var up, warn, down int
	for _, r := range m.rows {
		switch r.State {
		case "running":
			up++
		case "restarting", "paused", "created", "removing":
			warn++
		default:
			down++
		}
	}
	parts := []string{fmt.Sprintf("%d containers", len(m.rows))}
	if up > 0 {
		parts = append(parts, fmt.Sprintf("%d up", up))
	}
	if warn > 0 {
		parts = append(parts, fmt.Sprintf("%d restarting", warn))
	}
	if down > 0 {
		parts = append(parts, fmt.Sprintf("%d exited", down))
	}
	if m.opt.All {
		parts = append(parts, "showing all")
	}
	if clipped {
		// The list runs past the window, so say where in it the cursor is.
		parts = append(parts, fmt.Sprintf("row %d/%d", m.cursor+1, len(m.rows)))
	}
	text := strings.Join(parts, " · ")

	hidden := ""
	if len(dropped) > 0 {
		keys := make([]string, 0, len(dropped))
		for _, c := range dropped {
			keys = append(keys, c.Key)
		}
		hidden = " · hidden: " + strings.Join(keys, ", ")
	}

	// Colour is added after the length test because ANSI codes print no cells
	// but do count as runes, and a wrapped status line would push the help off
	// the screen.
	if table.Width(text+hidden) > m.width {
		return ansiDim + table.Truncate(text+hidden, m.width, table.TruncTail) + ansiReset
	}
	if hidden == "" {
		return ansiDim + text + ansiReset
	}
	return ansiDim + text + ansiReset + ansiYellow + hidden + ansiReset
}

func (m Model) help() string {
	keys := "↑↓ move · PgUp/PgDn page · a toggle stopped · r refresh · q quit"
	return ansiDim + table.Truncate(keys, m.width, table.TruncTail) + ansiReset
}

func (m *Model) clampCursor() {
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m Model) fetch() tea.Cmd {
	return func() tea.Msg {
		rows, err := m.client.ListContainers(m.ctx, m.opt)
		if err != nil {
			return errMsg{err}
		}
		return rowsMsg(rows)
	}
}

func tick() tea.Cmd {
	return tea.Tick(refreshInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

func headerCells(cols []table.Column) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Header
	}
	return out
}

// colorize renders a non-selected row, tinting the state and ports cells.
// The selected row is skipped because reverse video already carries it and
// nested colour codes fight the highlight.
func colorize(cols []table.Column, r model.Container, cells []string, widths []int,
	line func([]table.Column, []int, []string) string) string {
	out := line(cols, widths, cells)
	for i, c := range cols {
		if c.Key != "state" {
			continue
		}
		code := ansiRed
		switch r.State {
		case "running":
			code = ansiGreen
		case "restarting", "paused", "created", "removing":
			code = ansiYellow
		}
		painted := table.Truncate(cells[i], widths[i], c.Trunc)
		if i < len(cols)-1 {
			painted = table.Pad(painted, widths[i])
		}
		out = strings.Replace(out, painted, code+painted+ansiReset, 1)
		break
	}
	return out
}
