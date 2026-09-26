package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/developerabdan/dps/internal/dockerapi"
	"github.com/developerabdan/dps/internal/model"
	"github.com/developerabdan/dps/internal/table"
)

// logTail is how many old lines the logs view asks for when it opens. It is
// enough to see what led up to now, and small enough to open at once on a
// container that has logged for months.
const logTail = 200

// maxLogLines is how many lines the view keeps. A busy container can write
// thousands of lines a minute, and a view left open must not grow for ever,
// so the oldest lines go first.
const maxLogLines = 10000

// logBatch is the most lines one message carries. A burst is drawn in a few
// frames instead of one frame for each line.
const logBatch = 500

// logsFixed is how many lines of the logs view are not log: the title and the
// blank under it, and the blank, status and help at the bottom.
const logsFixed = 5

// logsMinBody is the fewest lines of log worth a title above them.
const logsMinBody = 3

type (
	logItem struct {
		line dockerapi.LogLine
		err  error
	}
	// logsMsg carries the lines that arrived since the last one. gen is the
	// view it belongs to, so lines from a stream that was closed are dropped.
	logsMsg struct {
		gen   int
		lines []dockerapi.LogLine
		done  bool
		err   error
	}
)

// logView is the state of the logs view.
type logView struct {
	key, name string
	gen       int

	lines []dockerapi.LogLine

	// top is the first line on screen. While follow is set the view ignores
	// it and shows the last lines, so it is written again only when the
	// reader scrolls.
	top    int
	follow bool
	// unseen counts the lines that arrived while follow was off, so the
	// status line can say the log did not stop.
	unseen int

	feed   <-chan logItem
	cancel context.CancelFunc
	ended  bool
	err    error

	// typing is set while the search prompt is open, and input is what was
	// typed into it. query is the search in force, hits the lines it
	// matches in order, and hit the one the view jumped to, or -1.
	typing bool
	input  string
	query  string
	re     *regexp.Regexp
	hits   []int
	hit    int
}

// openLogs opens the logs view on the selected container. A stopped container
// still has its log, so it opens too: the stream sends what there is and ends.
func (m Model) openLogs() (tea.Model, tea.Cmd) {
	if len(m.rows) == 0 {
		return m, nil
	}
	r := m.rows[m.cursor]
	m.closeLogs()
	m.mode = modeLogs
	m.logs = logView{key: rowKey(r), name: r.Name, gen: m.logs.gen + 1, follow: true, hit: -1}
	if m.client == nil {
		return m, nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	ch := make(chan logItem, logBatch)
	go pumpLogs(ctx, m.client, r.ID, ch)
	m.logs.feed, m.logs.cancel = ch, cancel
	return m, waitLogs(ch, m.logs.gen)
}

// closeLogs stops the stream and goes back to the list. The lines are let go:
// the next open reads the log again from the daemon.
func (m *Model) closeLogs() {
	if m.logs.cancel != nil {
		m.logs.cancel()
	}
	m.logs = logView{gen: m.logs.gen}
	m.mode = modeList
	m.ensureVisible()
}

// pumpLogs reads the stream into ch until it ends or ctx is cancelled. It is
// the only goroutine that touches the stream.
func pumpLogs(ctx context.Context, client *dockerapi.Client, id string, ch chan<- logItem) {
	defer close(ch)
	send := func(it logItem) bool {
		select {
		case ch <- it:
			return true
		case <-ctx.Done():
			return false
		}
	}
	s, err := client.Logs(ctx, id, logTail)
	if err != nil {
		if ctx.Err() == nil {
			send(logItem{err: err})
		}
		return
	}
	defer s.Close()
	for {
		l, err := s.Next()
		if err != nil {
			if !errors.Is(err, io.EOF) && ctx.Err() == nil {
				send(logItem{err: err})
			}
			return
		}
		if !send(logItem{line: l}) {
			return
		}
	}
}

// waitLogs waits for the next line, then takes whatever else is already
// waiting, up to logBatch.
func waitLogs(ch <-chan logItem, gen int) tea.Cmd {
	return func() tea.Msg {
		msg := logsMsg{gen: gen}
		it, ok := <-ch
		for {
			switch {
			case !ok:
				msg.done = true
				return msg
			case it.err != nil:
				msg.done, msg.err = true, it.err
				return msg
			}
			msg.lines = append(msg.lines, it.line)
			if len(msg.lines) >= logBatch {
				return msg
			}
			select {
			case it, ok = <-ch:
			default:
				return msg
			}
		}
	}
}

func (m Model) takeLogs(msg logsMsg) (tea.Model, tea.Cmd) {
	if m.mode != modeLogs || msg.gen != m.logs.gen {
		return m, nil
	}
	m.addLogs(msg.lines)
	if msg.done {
		m.logs.ended, m.logs.err = true, msg.err
		return m, nil
	}
	if m.logs.feed == nil {
		return m, nil
	}
	return m, waitLogs(m.logs.feed, msg.gen)
}

func (m *Model) addLogs(lines []dockerapi.LogLine) {
	v := &m.logs
	for _, l := range lines {
		l.Text = cleanLog(l.Text)
		if v.re != nil && v.re.MatchString(l.Text) {
			v.hits = append(v.hits, len(v.lines))
		}
		v.lines = append(v.lines, l)
	}
	if !v.follow {
		v.unseen += len(lines)
	}
	drop := len(v.lines) - maxLogLines
	if drop <= 0 {
		return
	}
	v.lines = v.lines[drop:]
	v.top = max(0, v.top-drop)
	keep := v.hits[:0]
	for _, h := range v.hits {
		if h >= drop {
			keep = append(keep, h-drop)
		}
	}
	v.hits = keep
	if v.hit >= 0 {
		v.hit -= drop
		if v.hit < 0 {
			v.hit = -1
		}
	}
}

func (m Model) updateLogs(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.logs.typing {
		return m.typeSearch(msg)
	}
	h := m.logsHeight()
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		// The first esc takes the search away, the second leaves.
		if m.logs.query != "" {
			m.setSearch("")
			return m, nil
		}
		m.closeLogs()
	case "q", "l", "backspace", "left", "h":
		m.closeLogs()
	case "up", "k":
		m.scrollLogs(-1)
	case "down", "j":
		m.scrollLogs(1)
	case "pgup", "ctrl+b":
		m.scrollLogs(-h)
	case "pgdown", "ctrl+f", " ", "space":
		m.scrollLogs(h)
	case "ctrl+u":
		m.scrollLogs(-h / 2)
	case "ctrl+d":
		m.scrollLogs(h / 2)
	case "g", "home":
		m.scrollLogs(-len(m.logs.lines))
	case "G", "end":
		m.scrollLogs(len(m.logs.lines))
	case "f":
		if m.logs.follow {
			m.logs.top = m.logTop()
			m.logs.follow = false
		} else {
			m.logs.follow, m.logs.unseen = true, 0
		}
	case "/":
		m.logs.typing, m.logs.input = true, ""
	case "n":
		return m, m.nextHit(1)
	case "N":
		return m, m.nextHit(-1)
	}
	return m, nil
}

// typeSearch takes the keys typed into the search prompt.
func (m Model) typeSearch(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	v := &m.logs
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		v.typing = false
	case "enter":
		v.typing = false
		m.setSearch(v.input)
		if v.query == "" {
			return m, nil
		}
		if len(v.hits) == 0 {
			return m, m.say(ansiYellow, "no match for "+v.query)
		}
		m.jumpToHit(m.nearestHit())
	case "backspace":
		if r := []rune(v.input); len(r) > 0 {
			v.input = string(r[:len(r)-1])
		}
	default:
		v.input += msg.Text
	}
	return m, nil
}

// setSearch puts q in force and finds every line it matches. The search
// ignores case: a log is read by eye, and the eye does not see case first.
func (m *Model) setSearch(q string) {
	v := &m.logs
	v.query, v.re, v.hits, v.hit = q, nil, nil, -1
	if q == "" {
		return
	}
	v.re = regexp.MustCompile("(?i)" + regexp.QuoteMeta(q))
	for i, l := range v.lines {
		if v.re.MatchString(l.Text) {
			v.hits = append(v.hits, i)
		}
	}
}

// nearestHit is the match a new search starts on: the last one on screen or
// above it, because the newest lines are at the bottom and the reader is
// most likely looking for something that just happened. With nothing above,
// it is the first one below.
func (m Model) nearestHit() int {
	v := m.logs
	bottom := m.logTop() + m.logsHeight() - 1
	i := sort.SearchInts(v.hits, bottom+1) - 1
	if i < 0 {
		i = 0
	}
	return i
}

// nextHit moves to the next match down the log, or up it when dir is -1. At
// either end it goes round to the other end and says so.
func (m *Model) nextHit(dir int) tea.Cmd {
	v := &m.logs
	if v.query == "" {
		return nil
	}
	if len(v.hits) == 0 {
		return m.say(ansiYellow, "no match for "+v.query)
	}
	var i int
	if dir > 0 {
		i = sort.SearchInts(v.hits, v.hit+1)
	} else {
		i = sort.SearchInts(v.hits, v.hit) - 1
	}
	var cmd tea.Cmd
	switch {
	case v.hit < 0:
		i = m.nearestHit()
	case i >= len(v.hits):
		i = 0
		cmd = m.say(ansiDim, "search went round to the top")
	case i < 0:
		i = len(v.hits) - 1
		cmd = m.say(ansiDim, "search went round to the bottom")
	}
	m.jumpToHit(i)
	return cmd
}

// jumpToHit shows the i-th match. A match already on screen does not move the
// view, and one that is not is put in the middle, with its context around it.
func (m *Model) jumpToHit(i int) {
	v := &m.logs
	v.hit = v.hits[i]
	top, h := m.logTop(), m.logsHeight()
	if v.hit < top || v.hit >= top+h {
		top = v.hit - h/2
	}
	m.setLogTop(top)
}

// scrollLogs moves the view by delta lines.
func (m *Model) scrollLogs(delta int) {
	m.setLogTop(m.logTop() + delta)
}

// setLogTop moves the view and decides follow from where it lands: at the
// bottom the view follows the log, anywhere above it the view stays put.
func (m *Model) setLogTop(top int) {
	v := &m.logs
	bottom := m.logBottom()
	v.top = min(max(top, 0), bottom)
	v.follow = v.top >= bottom
	if v.follow {
		v.unseen = 0
	}
}

// logTop is the first line on screen.
func (m Model) logTop() int {
	if m.logs.follow {
		return m.logBottom()
	}
	return min(m.logs.top, m.logBottom())
}

// logBottom is the top line of the view when it shows the last line.
func (m Model) logBottom() int {
	return max(0, len(m.logs.lines)-m.logsHeight())
}

func (m Model) logsHeight() int {
	return max(1, m.height-m.logsChrome())
}

// logsChrome is logsFixed, less the title in a window so short that the
// title would leave less than logsMinBody lines of log.
func (m Model) logsChrome() int {
	if m.height-logsFixed < logsMinBody {
		return logsFixed - 2
	}
	return logsFixed
}

func (m Model) viewLogs() string {
	var r model.Container
	if i := m.indexOf(m.logs.key); i >= 0 {
		r = m.rows[i]
	}
	v := m.logs
	width := max(m.width-2*len(statsIndent), 8)

	title := []seg{{m.logs.name, ansiBold}}
	if r.State != "" {
		title = append(title, seg{"  " + model.ShortState(r.State, r.Status), stateColor(r.State)})
	}
	title = append(title, seg{"  logs", ansiDim})

	var b strings.Builder
	if m.logsChrome() == logsFixed {
		b.WriteString(statsIndent + fit(width, title...) + "\n\n")
	}

	h := m.logsHeight()
	top := m.logTop()
	end := min(top+h, len(v.lines))
	for i := top; i < end; i++ {
		b.WriteString(statsIndent + m.logLine(v.lines[i].Text, width, i == v.hit) + "\n")
	}
	for i := end - top; i < h; i++ {
		if i == 0 && len(v.lines) == 0 {
			empty := "waiting for output…"
			if v.ended {
				empty = "no output"
			}
			b.WriteString(statsIndent + ansiDim + empty + ansiReset)
		}
		b.WriteByte('\n')
	}

	b.WriteByte('\n')
	b.WriteString(m.logsStatus(top, end))
	b.WriteByte('\n')
	b.WriteString(m.help())
	return b.String()
}

// logLine cuts a line to the window and marks what the search matches. The
// line the search jumped to is marked in yellow, so it stands out from the
// other matches around it.
func (m Model) logLine(text string, width int, current bool) string {
	text = table.Truncate(text, width, table.TruncTail)
	re := m.logs.re
	if re == nil {
		return text
	}
	mark := ansiReverse
	if current {
		mark = ansiReverse + ansiYellow
	}
	var b strings.Builder
	last := 0
	for _, loc := range re.FindAllStringIndex(text, -1) {
		b.WriteString(text[last:loc[0]])
		b.WriteString(mark + text[loc[0]:loc[1]] + ansiReset)
		last = loc[1]
	}
	b.WriteString(text[last:])
	return b.String()
}

// logsStatus says where the view is in the log and whether it follows. The
// search prompt takes the whole line while it is open.
func (m Model) logsStatus(top, end int) string {
	v := m.logs
	if v.typing {
		return fit(m.width, seg{"/" + v.input, ansiBold}, seg{"█", ansiDim})
	}

	var segs []seg
	add := func(text, color string) {
		if len(segs) > 0 {
			segs = append(segs, seg{" · ", ansiDim})
		}
		segs = append(segs, seg{text, color})
	}
	if m.notice != "" {
		add(m.notice, m.noticeColor)
	}
	switch {
	case v.err != nil:
		add("stream failed: "+v.err.Error(), ansiRed)
	case v.ended:
		add("end of log", ansiDim)
	case v.follow:
		add("following", ansiGreen)
	case v.unseen > 0:
		add(fmt.Sprintf("paused · %d new · f follows", v.unseen), ansiYellow)
	default:
		add("paused · f follows", ansiYellow)
	}
	if len(v.lines) > 0 {
		add(fmt.Sprintf("lines %d–%d of %d", top+1, end, len(v.lines)), ansiDim)
	}
	if v.query != "" {
		switch i := sort.SearchInts(v.hits, v.hit); {
		case len(v.hits) == 0:
			add("no match for "+v.query, ansiYellow)
		case v.hit >= 0 && i < len(v.hits) && v.hits[i] == v.hit:
			add(fmt.Sprintf("match %d/%d", i+1, len(v.hits)), ansiDim)
		default:
			add(fmt.Sprintf("%d matches", len(v.hits)), ansiDim)
		}
	}
	return fit(m.width, segs...)
}

// ansiSeq matches the escape sequences a program writes to colour or move
// its output: CSI, OSC, and the two-byte forms.
var ansiSeq = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]`)

// cleanLog makes a log line safe to draw inside the view. Escape codes would
// move the cursor or colour the rest of the screen, and a carriage return
// would draw over the view, so the codes go and only the text after the last
// carriage return stays — which is what a terminal would have shown. Tabs
// become spaces, because the width of a tab depends on where it lands.
func cleanLog(s string) string {
	s = ansiSeq.ReplaceAllString(s, "")
	if i := strings.LastIndexByte(s, '\r'); i >= 0 {
		s = s[i+1:]
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		switch {
		case r == '\t':
			n := 8 - col%8
			b.WriteString(strings.Repeat(" ", n))
			col += n
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
		default:
			b.WriteRune(r)
			col++
		}
	}
	return b.String()
}
