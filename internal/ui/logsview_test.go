package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/developerabdan/dps/internal/dockerapi"
)

func logLines(from, n int) []dockerapi.LogLine {
	out := make([]dockerapi.LogLine, n)
	for i := range out {
		out[i].Text = fmt.Sprintf("line %03d", from+i)
	}
	return out
}

func feedLogs(m Model, ls []dockerapi.LogLine) Model {
	next, _ := m.Update(logsMsg{gen: m.logs.gen, lines: ls})
	return next.(Model)
}

func typeText(m Model, s string) Model {
	for _, r := range s {
		next, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = next.(Model)
	}
	return m
}

func enter(m Model) Model {
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return next.(Model)
}

func openLogsOn(t *testing.T, height int) Model {
	t.Helper()
	m := Model{cols: mustCols(t, "name", "state"), width: 80, height: height}
	m = feed(m, fleet(3), false)
	m.cursor = 1
	m = pressKey(m, "l")
	if m.mode != modeLogs || m.logs.key != "id-01" {
		t.Fatalf("mode %v on %q, want the logs view on id-01", m.mode, m.logs.key)
	}
	return m
}

func TestLogsOpenAndClose(t *testing.T) {
	m := openLogsOn(t, 20)
	screen := stripANSI(m.View().Content)
	if !strings.Contains(screen, "container-01") || !strings.Contains(screen, "waiting for output") {
		t.Errorf("empty logs view:\n%s", screen)
	}
	m = escape(m)
	if m.mode != modeList || m.cursor != 1 {
		t.Errorf("esc left mode %v, cursor %d; want the list on row 1", m.mode, m.cursor)
	}
}

func TestLogsFollowTheEnd(t *testing.T) {
	m := openLogsOn(t, 20)
	m = feedLogs(m, logLines(0, 100))

	ls := lines(m)
	body := ls[2 : len(ls)-3]
	if last := stripANSI(body[len(body)-1]); !strings.Contains(last, "line 099") {
		t.Errorf("last body line %q, want line 099", last)
	}
	if !strings.Contains(stripANSI(statusLine(m)), "following") {
		t.Errorf("status %q, want following", statusLine(m))
	}
}

func TestLogsScrollUpPausesFollow(t *testing.T) {
	m := openLogsOn(t, 20)
	m = feedLogs(m, logLines(0, 100))
	m = pressKey(m, "k")
	if m.logs.follow {
		t.Fatal("scrolling up kept follow on")
	}
	top := m.logTop()
	m = feedLogs(m, logLines(100, 5))
	if m.logTop() != top {
		t.Errorf("new lines moved a paused view from %d to %d", top, m.logTop())
	}
	if s := stripANSI(statusLine(m)); !strings.Contains(s, "paused · 5 new") {
		t.Errorf("status %q, want the count of new lines", s)
	}

	m = pressKey(m, "f")
	if !m.logs.follow || m.logTop() != m.logBottom() {
		t.Errorf("f did not follow again: top %d, bottom %d", m.logTop(), m.logBottom())
	}
}

func TestLogsSearch(t *testing.T) {
	m := openLogsOn(t, 20)
	ls := logLines(0, 100)
	ls[10].Text = "ERROR first"
	ls[50].Text = "error second"
	m = feedLogs(m, ls)

	m = pressKey(m, "/")
	m = typeText(m, "error")
	if s := stripANSI(statusLine(m)); !strings.Contains(s, "/error") {
		t.Errorf("prompt %q, want /error", s)
	}
	m = enter(m)
	// The newest match at or above the screen comes first.
	if m.logs.hit != 50 || len(m.logs.hits) != 2 {
		t.Fatalf("hit %d of %v, want line 50 of two", m.logs.hit, m.logs.hits)
	}
	if !strings.Contains(stripANSI(m.View().Content), "error second") {
		t.Error("the match is not on screen")
	}
	if m.logs.follow {
		t.Error("jumping to a match kept follow on")
	}

	m = pressKey(m, "N")
	if m.logs.hit != 10 {
		t.Errorf("N went to %d, want 10", m.logs.hit)
	}
	if s := stripANSI(statusLine(m)); !strings.Contains(s, "match 1/2") {
		t.Errorf("status %q, want match 1/2", s)
	}
	m = pressKey(m, "N")
	if m.logs.hit != 50 {
		t.Errorf("N at the top went to %d, want round to 50", m.logs.hit)
	}

	// The first esc clears the search, the second leaves.
	m = escape(m)
	if m.mode != modeLogs || m.logs.query != "" {
		t.Errorf("first esc: mode %v, query %q", m.mode, m.logs.query)
	}
	m = escape(m)
	if m.mode != modeList {
		t.Errorf("second esc left mode %v", m.mode)
	}
}

func TestLogsKeepAtMost(t *testing.T) {
	m := openLogsOn(t, 20)
	for i := 0; i < maxLogLines+1000; i += logBatch {
		m = feedLogs(m, logLines(i, logBatch))
	}
	if n := len(m.logs.lines); n != maxLogLines {
		t.Errorf("kept %d lines, want %d", n, maxLogLines)
	}
	if got := m.logs.lines[0].Text; got != fmt.Sprintf("line %03d", 1000) {
		t.Errorf("first kept line %q", got)
	}
}

func TestLogsIgnoreOldStream(t *testing.T) {
	m := openLogsOn(t, 20)
	next, _ := m.Update(logsMsg{gen: m.logs.gen - 1, lines: logLines(0, 3)})
	if n := len(next.(Model).logs.lines); n != 0 {
		t.Errorf("took %d lines from a closed stream", n)
	}
}

func TestLogsViewFitsTheWindow(t *testing.T) {
	long := strings.Repeat("x", 300)
	for _, width := range []int{20, 80, 200} {
		for _, height := range []int{4, 10, 40} {
			m := Model{cols: mustCols(t, "name", "state"), width: width, height: height}
			m = feed(m, fleet(2), false)
			m = pressKey(m, "l")
			m = feedLogs(m, []dockerapi.LogLine{{Text: long}, {Text: "short"}})
			ls := lines(m)
			if len(ls) > height {
				t.Errorf("%dx%d: frame is %d lines", width, height, len(ls))
			}
			for i, l := range ls {
				if n := visibleWidth(l); n > width {
					t.Errorf("%dx%d: line %d is %d cells", width, height, i, n)
				}
			}
		}
	}
}

func TestCleanLog(t *testing.T) {
	for in, want := range map[string]string{
		"\x1b[31mred\x1b[0m":        "red",
		"a\tb":                      "a       b",
		"50%\r100% done":            "100% done",
		"bell\x07 and \x1b]0;t\x07": "bell and ",
	} {
		if got := cleanLog(in); got != want {
			t.Errorf("cleanLog(%q) = %q, want %q", in, got, want)
		}
	}
}
