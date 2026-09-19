package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/developerabdan/dps/internal/model"
)

func escape(m Model) Model {
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	return next.(Model)
}

func TestStatsOpensOnRunningContainer(t *testing.T) {
	m := Model{cols: mustCols(t, "name", "state"), width: 120, height: 30}
	m = feed(m, fleet(3), false)
	m.cursor = 1

	m = pressKey(m, "s")
	if m.mode != modeStats || m.statsKey != "id-01" {
		t.Fatalf("mode %v on %q, want the stats view on id-01", m.mode, m.statsKey)
	}
	m = escape(m)
	if m.mode != modeList {
		t.Errorf("esc left mode %v, want the list", m.mode)
	}
}

func TestStatsRefusesStoppedContainer(t *testing.T) {
	m := Model{cols: mustCols(t, "name", "state"), width: 120, height: 30}
	rs := fleet(2)
	rs[0].State = "exited"
	m = feed(m, rs, false)

	m = pressKey(m, "s")
	if m.mode != modeList || !strings.Contains(statusLine(m), "container-00 is not running") {
		t.Errorf("mode %v, status %q; want the refusal", m.mode, statusLine(m))
	}
}

func TestStatsViewShowsEveryPanel(t *testing.T) {
	m := Model{cols: mustCols(t, "name", "state"), width: 120, height: 40}
	m = feed(m, fleet(2), false)
	m = pressKey(m, "s")
	m = feedStats(m, map[string]model.Sample{"id-00": busy(0, 0)})
	m = feedStats(m, map[string]model.Sample{"id-00": busy(2, 200)})

	screen := stripANSI(m.View().Content)
	for _, want := range []string{
		"container-00", "CPU", "10.0%", "1 CPUs",
		"MEMORY", "50.0MB / 1.0GB · 5.0%",
		"DISK", "read 200.0kB/s", "write 50.0kB/s",
		"NETWORK", "in 30.0kB/s", "out 5.0kB/s",
		"7 processes",
	} {
		if !strings.Contains(screen, want) {
			t.Errorf("stats view has no %q:\n%s", want, screen)
		}
	}
}

func TestStatsViewFitsTheWindow(t *testing.T) {
	for _, width := range []int{30, 80, 200} {
		for _, height := range []int{6, 10, 24, 40, 80} {
			m := Model{cols: mustCols(t, "name", "state"), width: width, height: height}
			m = feed(m, fleet(2), false)
			m = pressKey(m, "s")
			for sec := 0; sec <= 20; sec += 2 {
				m = feedStats(m, map[string]model.Sample{"id-00": busy(sec, uint64(sec*100))})
			}

			ls := lines(m)
			if len(ls) > height {
				t.Errorf("%dx%d: frame is %d lines", width, height, len(ls))
			}
			for i, l := range ls {
				if n := visibleWidth(l); n > width {
					t.Errorf("%dx%d: line %d is %d cells: %q", width, height, i, n, stripANSI(l))
				}
			}
			if !strings.Contains(ls[len(ls)-1], "esc back") {
				t.Errorf("%dx%d: help is not the last line", width, height)
			}
		}
	}
}

func TestStatsFollowsItsContainer(t *testing.T) {
	m := Model{cols: mustCols(t, "name", "state"), width: 120, height: 30}
	m = feed(m, fleet(3), false)
	m.cursor = 1
	m = pressKey(m, "s")

	// The refresh puts the rows in a different order.
	rs := fleet(3)
	rs[0], rs[2] = rs[2], rs[0]
	rs[1], rs[2] = rs[2], rs[1]
	m = feed(m, rs, false)
	if rowKey(m.rows[m.cursor]) != "id-01" {
		t.Errorf("cursor on %s, want it to follow id-01", rowKey(m.rows[m.cursor]))
	}

	// The container is removed: back to the list, and say why.
	m = feed(m, fleet(1), false)
	if m.mode != modeList || !strings.Contains(statusLine(m), "container-01 is gone") {
		t.Errorf("mode %v, status %q; want the list and the reason", m.mode, statusLine(m))
	}
}

func TestStatsArrowsPassOverStoppedContainers(t *testing.T) {
	m := Model{cols: mustCols(t, "name", "state"), width: 120, height: 30}
	rs := fleet(4)
	rs[1].State = "exited"
	rs[2].State = "exited"
	m = feed(m, rs, false)
	m = pressKey(m, "s")

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = next.(Model)
	if m.statsKey != "id-03" {
		t.Errorf("down went to %q, want id-03", m.statsKey)
	}
}

func TestStatsFramesTheChartsOnlyWhenTall(t *testing.T) {
	view := func(height int) string {
		m := Model{cols: mustCols(t, "name", "state"), width: 100, height: height}
		m = feed(m, fleet(2), false)
		m = pressKey(m, "s")
		for sec := 0; sec <= 20; sec += 2 {
			m = feedStats(m, map[string]model.Sample{"id-00": busy(sec, uint64(sec*100))})
		}
		return stripANSI(m.View().Content)
	}

	tall := view(52)
	for _, want := range []string{"┌", "└", "│", "now", "ago"} {
		if !strings.Contains(tall, want) {
			t.Errorf("a tall window has no %q:\n%s", want, tall)
		}
	}
	// Every box starts in the same column, whatever its scale reads.
	col := -1
	for _, l := range strings.Split(tall, "\n") {
		i := strings.Index(l, "┌")
		if i < 0 {
			continue
		}
		if col >= 0 && i != col {
			t.Errorf("a box starts at column %d, another at %d", col, i)
		}
		col = i
	}

	if short := view(30); strings.Contains(short, "┌") {
		t.Errorf("a short window drew a frame:\n%s", short)
	} else if !strings.Contains(short, "0–") {
		t.Errorf("a short window has no scale on its labels:\n%s", short)
	}
}
