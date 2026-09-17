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
	"github.com/developerabdan/dps/internal/stats"
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
	ansiCyan    = "\x1b[36m"
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

// noticeFor is how long a one-off message stays on the status line: long
// enough to read four words, short enough to be gone before the next key.
const noticeFor = 2 * time.Second

// flashFor is how long a changed row stays bold. It is shorter than
// refreshInterval, so a row that changed on one poll is plain again before the
// next poll can mark it.
const flashFor = 1500 * time.Millisecond

// statsTimeout bounds one round of stats requests. A daemon that stops
// answering must not leave the graphs waiting for ever: the round ends, and
// the next poll starts a new one.
const statsTimeout = 5 * time.Second

// cpuFloor is the least the top of a CPU graph stands for, in percent. Below
// it an idle container draws a flat line instead of blowing its own noise up
// to full height.
const cpuFloor = 10

// mode is which screen the interactive view is showing.
type mode int

const (
	modeList mode = iota
	modeStats
	modePick
)

type (
	// rowsMsg carries one fetch. manual marks the fetch that r asked for,
	// because only that one reports its result on the status line — the
	// background poll runs every two seconds and would never be quiet.
	rowsMsg struct {
		rows   []model.Container
		manual bool
	}
	errMsg        struct{ err error }
	statsMsg      struct{ samples map[string]model.Sample }
	tickMsg       struct{}
	noticeDoneMsg struct{ gen int }
	flashDoneMsg  struct{ gen int }
	shellDoneMsg  struct {
		name  string
		code  int
		typed bool
		err   error
	}
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

	// notice is a one-off message at the front of the status line, drawn in
	// noticeColor. noticeGen numbers each message, so a timer set for an old
	// one cannot clear a newer one that replaced it.
	notice      string
	noticeColor string
	noticeGen   int
	refreshing  bool

	// seen is what each container looked like at the last fetch, and flash
	// holds the rows that differ from it, drawn bold for flashFor. A nil seen
	// means there is nothing to compare with yet — the first fetch, or the
	// first after `a` changed which containers are listed — so nothing flashes.
	seen     map[string]string
	flash    map[string]bool
	flashGen int

	mode mode

	// series is the resource history of each container, by rowKey. Samples are
	// taken only for what is on screen: every running container while the cpu
	// column is shown, and the one container the stats view is open on.
	// sampling is set while a round of requests is out, so a slow daemon does
	// not collect a queue of them.
	series   map[string]*stats.Series
	sampling bool

	// statsKey and statsName are the container the stats view shows. They are
	// kept apart from the cursor because a refresh can move rows under it.
	statsKey  string
	statsName string

	// confirm is the container that waits for a yes before a shell opens in
	// it. One stray e must not take the screen away.
	confirm *shellAsk

	pick picker
}

type shellAsk struct{ key, name string }

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
	return tea.Batch(m.fetch(false), tick())
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
		m.rows = msg.rows
		if m.group {
			ordered := make([]model.Container, 0, len(msg.rows))
			for _, g := range table.GroupByProject(msg.rows) {
				ordered = append(ordered, g.Rows...)
			}
			m.rows = ordered
		}
		m.err = nil
		m.clampCursor()
		m.ensureVisible()

		var cmds []tea.Cmd
		d := m.diff()
		if len(d.marked) > 0 {
			cmds = append(cmds, m.startFlash(d.marked))
		}
		if msg.manual {
			m.refreshing = false
			cmds = append(cmds, m.say(ansiGreen, "↻ refreshed · "+d.summary()))
		}
		if m.mode == modeStats {
			if i := m.indexOf(m.statsKey); i >= 0 {
				m.cursor = i
				m.ensureVisible()
			} else {
				m.mode = modeList
				m.ensureVisible()
				cmds = append(cmds, m.say(ansiYellow, m.statsName+" is gone"))
			}
		}
		// A container that has no history yet — the first fetch, or one that
		// just started — gets its first sample now instead of at the next
		// poll, so its graph begins two seconds sooner.
		cmds = append(cmds, m.sampleMissing())
		return m, tea.Batch(cmds...)

	case errMsg:
		m.err = msg.err
		if m.refreshing {
			m.refreshing = false
			m.notice = ""
		}
		return m, nil

	case tickMsg:
		sample := m.sample()
		return m, tea.Batch(m.fetch(false), tick(), sample)

	case statsMsg:
		m.sampling = false
		if m.series == nil {
			m.series = map[string]*stats.Series{}
		}
		for key, smp := range msg.samples {
			s := m.series[key]
			if s == nil {
				s = &stats.Series{}
				m.series[key] = s
			}
			s.Add(smp)
		}
		m.pruneSeries()
		return m, nil

	case noticeDoneMsg:
		if msg.gen == m.noticeGen {
			m.notice = ""
		}
		return m, nil

	case flashDoneMsg:
		if msg.gen == m.flashGen {
			m.flash = nil
		}
		return m, nil

	case shellDoneMsg:
		// Whatever happened in the shell may have changed the list, so it is
		// read again straight away rather than on the next poll.
		cmds := []tea.Cmd{m.fetch(false)}
		switch {
		case msg.err != nil:
			cmds = append(cmds, m.say(ansiRed, "shell failed: "+msg.err.Error()))
		case noShell(msg.code, msg.typed):
			cmds = append(cmds, m.say(ansiYellow, msg.name+" has no shell to open"))
		}
		return m, tea.Batch(cmds...)

	case tea.MouseWheelMsg:
		if m.mode == modeStats {
			return m, nil
		}
		switch msg.Button {
		case tea.MouseWheelUp:
			m.scroll(-scrollJump)
		case tea.MouseWheelDown:
			m.scroll(scrollJump)
		}
		return m, nil

	case tea.KeyPressMsg:
		switch {
		case m.confirm != nil:
			return m.answerShell(msg)
		case m.mode == modeStats:
			return m.updateStats(msg)
		case m.mode == modePick:
			return m.updatePick(msg)
		}
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
			// The next list is a different set of containers, not a newer
			// version of this one, so none of it should flash as changed.
			m.seen = nil
			return m, m.fetch(false)
		case "r":
			// Most refreshes change nothing, and a list that looks the same
			// after the key looks like the key did nothing. The status line
			// says it is working, then says what it found.
			m.refreshing = true
			m.noticeGen++
			m.notice, m.noticeColor = "↻ refreshing…", ansiDim
			return m, m.fetch(true)
		case "e":
			return m.askShell()
		case "s":
			return m.openStats()
		case "c":
			return m.openPicker()
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
	if m.mode == modeStats {
		return altView(m.viewStats())
	}

	var b strings.Builder
	if m.mode == modePick {
		b.WriteString(m.viewPicker())
	}
	if len(m.rows) == 0 {
		b.WriteString(fmt.Sprintf("%sno containers%s\n\n%s", ansiDim, ansiReset, m.help()))
		return altView(b.String())
	}

	// Indent inside the first column rather than shifting the whole line, so
	// the other columns stay on their axis. This is the same rule the plain
	// table uses.
	cols := m.liveCols(m.cols)
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
			switch {
			case s.row == m.cursor:
				b.WriteString(m.selectedLine(active, widths, cells))
			case m.flash[rowKey(r)]:
				b.WriteString(bold(colorize(active, r, cells, widths, m.line)))
			default:
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

// bodyHeight is how many lines the scrolling region gets. The column picker
// takes its lines from the top, and the table below it shrinks to fit.
func (m Model) bodyHeight() int {
	chrome := chromeLines
	if m.mode == modePick {
		chrome += m.pickerLines()
	}
	if h := m.height - chrome; h > 0 {
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

	// The notice goes first, so a narrow window cuts the counts rather than the
	// one thing that just changed.
	notice, color := m.notice, m.noticeColor
	if m.confirm != nil {
		notice, color = "open a shell in "+m.confirm.name+"? y/n", ansiBold+ansiYellow
	}
	lead, room := "", m.width
	if notice != "" {
		n := table.Truncate(notice, m.width, table.TruncTail)
		lead = color + n + ansiReset
		room -= table.Width(n)
		const sep = " · "
		if room <= table.Width(sep) {
			return lead
		}
		lead += ansiDim + sep + ansiReset
		room -= table.Width(sep)
	}

	// Colour is added after the length test because ANSI codes print no cells
	// but do count as runes, and a wrapped status line would push the help off
	// the screen.
	if table.Width(text+hidden) > room {
		return lead + ansiDim + table.Truncate(text+hidden, room, table.TruncTail) + ansiReset
	}
	if hidden == "" {
		return lead + ansiDim + text + ansiReset
	}
	return lead + ansiDim + text + ansiReset + ansiYellow + hidden + ansiReset
}

func (m Model) help() string {
	keys := "↑↓ move · s stats · e shell · c columns · a toggle stopped · r refresh · q quit"
	switch {
	case m.confirm != nil:
		keys = "y or enter opens the shell · any other key cancels"
	case m.mode == modePick:
		keys = "↑↓ move · space toggle · enter save as default · esc cancel"
	case m.mode == modeStats:
		keys = "↑↓ other container · esc back · ctrl+c quit"
	}
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

func (m Model) fetch(manual bool) tea.Cmd {
	return func() tea.Msg {
		rows, err := m.client.ListContainers(m.ctx, m.opt)
		if err != nil {
			return errMsg{err}
		}
		return rowsMsg{rows: rows, manual: manual}
	}
}

// askShell puts the question on the status line. A container that is not
// running has no process to exec into, and the daemon would only answer 409,
// so that is said at once instead of asking first.
func (m Model) askShell() (tea.Model, tea.Cmd) {
	if len(m.rows) == 0 {
		return m, nil
	}
	r := m.rows[m.cursor]
	if r.State != "running" {
		return m, m.say(ansiYellow, r.Name+" is not running")
	}
	m.confirm = &shellAsk{key: rowKey(r), name: r.Name}
	return m, nil
}

// answerShell takes the key pressed after the question. Only y or enter opens
// the shell. Any other key cancels and does nothing else, so a key typed
// without reading the question cannot also move the cursor or quit.
func (m Model) answerShell(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	ask := m.confirm
	m.confirm = nil
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "y", "Y", "enter":
		// The list can change while the question waits, so the container is
		// found again by key rather than taken from under the cursor.
		i := m.indexOf(ask.key)
		if i < 0 {
			return m, m.say(ansiYellow, ask.name+" is gone")
		}
		if m.rows[i].State != "running" {
			return m, m.say(ansiYellow, ask.name+" is not running")
		}
		return m.openShell(m.rows[i])
	}
	return m, m.say(ansiDim, "shell cancelled")
}

// openShell hands the terminal to a shell in the container.
func (m Model) openShell(r model.Container) (tea.Model, tea.Cmd) {
	if m.client == nil {
		return m, nil
	}
	sh := &shell{ctx: m.ctx, client: m.client, id: r.ID, name: r.Name}
	return m, tea.Exec(sh, func(err error) tea.Msg {
		return shellDoneMsg{name: sh.name, code: sh.code, typed: sh.typed.Load(), err: err}
	})
}

// say puts text at the front of the status line and returns the timer that
// takes it away again.
func (m *Model) say(color, text string) tea.Cmd {
	m.noticeGen++
	m.notice, m.noticeColor = text, color
	gen := m.noticeGen
	return tea.Tick(noticeFor, func(time.Time) tea.Msg { return noticeDoneMsg{gen} })
}

func (m *Model) startFlash(keys map[string]bool) tea.Cmd {
	m.flashGen++
	m.flash = keys
	gen := m.flashGen
	return tea.Tick(flashFor, func(time.Time) tea.Msg { return flashDoneMsg{gen} })
}

// rowDiff is what changed between two fetches.
type rowDiff struct {
	added, changed, gone int
	// marked is every row still on the list that is new or different.
	marked map[string]bool
}

// diff compares m.rows with the previous fetch and records m.rows as the new
// baseline.
func (m *Model) diff() rowDiff {
	next := make(map[string]string, len(m.rows))
	for _, r := range m.rows {
		next[rowKey(r)] = rowSig(r)
	}
	prev := m.seen
	m.seen = next

	d := rowDiff{marked: map[string]bool{}}
	if prev == nil {
		return d
	}
	for k, sig := range next {
		old, ok := prev[k]
		switch {
		case !ok:
			d.added++
			d.marked[k] = true
		case old != sig:
			d.changed++
			d.marked[k] = true
		}
	}
	for k := range prev {
		if _, ok := next[k]; !ok {
			d.gone++
		}
	}
	return d
}

func (d rowDiff) summary() string {
	var parts []string
	if d.added > 0 {
		parts = append(parts, fmt.Sprintf("%d new", d.added))
	}
	if d.changed > 0 {
		parts = append(parts, fmt.Sprintf("%d changed", d.changed))
	}
	if d.gone > 0 {
		parts = append(parts, fmt.Sprintf("%d gone", d.gone))
	}
	if len(parts) == 0 {
		return "no changes"
	}
	return strings.Join(parts, ", ")
}

func rowKey(r model.Container) string {
	if r.ID != "" {
		return r.ID
	}
	return r.Name
}

// rowSig is the part of a container that counts as a change. Status is left
// out on purpose: its text is "Up 3 minutes", which changes every minute on
// its own, and a row that flashes for the clock teaches people to ignore the
// flash.
func rowSig(r model.Container) string {
	return fmt.Sprint(r.State, "|", r.Health, "|", r.Image, "|", r.Ports)
}

// bold draws an already coloured line in bold. Each reset inside the line
// would end the bold early, so bold is opened again after every one of them.
func bold(line string) string {
	return ansiBold + strings.ReplaceAll(line, ansiReset, ansiReset+ansiBold) + ansiReset
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
		if c.Key == "cpu" {
			if g := graphPart(cells[i]); g != "" {
				out = strings.Replace(out, g, ansiCyan+g+ansiReset, 1)
			}
			continue
		}
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
	}
	return out
}
