package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/developerabdan/dps/internal/model"
	"github.com/developerabdan/dps/internal/table"
)

func fleet(n int) []model.Container {
	out := rows(n, false)
	for i := range out {
		out[i].ID = fmt.Sprintf("id-%02d", i)
	}
	return out
}

func feed(m Model, rs []model.Container, manual bool) Model {
	next, _ := m.Update(rowsMsg{rows: rs, manual: manual})
	return next.(Model)
}

func pressKey(m Model, key string) Model {
	next, _ := m.Update(tea.KeyPressMsg{Code: rune(key[0]), Text: key})
	return next.(Model)
}

func statusLine(m Model) string {
	ls := lines(m)
	return ls[len(ls)-2]
}

func TestFirstFetchDoesNotFlash(t *testing.T) {
	m := Model{cols: table.Catalog[:4], width: 120, height: 24}
	m = feed(m, fleet(5), false)
	if len(m.flash) != 0 {
		t.Errorf("first fetch flashed %d rows", len(m.flash))
	}
}

func TestChangedRowsFlash(t *testing.T) {
	m := Model{cols: table.Catalog[:4], width: 120, height: 24}
	m = feed(m, fleet(5), false)

	next := fleet(5)
	next[2].State = "exited"
	next[2].Status = "Exited (1) 1 second ago"
	next = append(next[:4], model.Container{ID: "id-new", Name: "fresh", State: "running"})
	m = feed(m, next, false)

	if !m.flash["id-02"] || !m.flash["id-new"] || len(m.flash) != 2 {
		t.Errorf("flash %v, want id-02 and id-new", m.flash)
	}
	// The poll is not the r key, so it says nothing on the status line.
	if m.notice != "" {
		t.Errorf("background poll set notice %q", m.notice)
	}

	// The flash is bold on screen, and the timer for this flash ends it.
	if !strings.Contains(strings.Join(lines(m), "\n"), ansiBold) {
		t.Errorf("no bold row on screen while flashing")
	}
	done, _ := m.Update(flashDoneMsg{gen: m.flashGen})
	if got := done.(Model).flash; got != nil {
		t.Errorf("flash %v after its timer", got)
	}
}

func TestUptimeTickIsNotAChange(t *testing.T) {
	m := Model{cols: table.Catalog[:4], width: 120, height: 24}
	m = feed(m, fleet(3), false)
	next := fleet(3)
	next[0].Status = "Up 4 minutes"
	m = feed(m, next, false)
	if len(m.flash) != 0 {
		t.Errorf("status text alone flashed %v", m.flash)
	}
}

func TestRefreshKeySaysWhatItFound(t *testing.T) {
	m := Model{cols: table.Catalog[:4], width: 120, height: 24}
	m = feed(m, fleet(4), false)

	m = pressKey(m, "r")
	if !m.refreshing || !strings.Contains(statusLine(m), "refreshing") {
		t.Fatalf("status %q while the fetch is out", statusLine(m))
	}

	m = feed(m, fleet(4), true)
	if m.refreshing || !strings.Contains(statusLine(m), "refreshed · no changes") {
		t.Errorf("status %q after a refresh with no change", statusLine(m))
	}

	m = pressKey(m, "r")
	changed := fleet(3)
	changed[0].Health = "unhealthy"
	m = feed(m, changed, true)
	if !strings.Contains(m.notice, "1 changed, 1 gone") {
		t.Errorf("notice %q, want 1 changed, 1 gone", m.notice)
	}
}

func TestOldNoticeTimerKeepsNewerNotice(t *testing.T) {
	m := Model{cols: table.Catalog[:4], width: 120, height: 24}
	m = feed(m, fleet(2), false)
	m.say(ansiGreen, "first")
	old := m.noticeGen
	m.say(ansiGreen, "second")

	next, _ := m.Update(noticeDoneMsg{gen: old})
	if got := next.(Model).notice; got != "second" {
		t.Errorf("notice %q, want the newer one kept", got)
	}
}

func TestToggleAllDoesNotFlashNewRows(t *testing.T) {
	m := Model{cols: table.Catalog[:4], width: 120, height: 24}
	m = feed(m, fleet(3), false)
	m = pressKey(m, "a")
	m = feed(m, fleet(6), false)
	if len(m.flash) != 0 {
		t.Errorf("toggling stopped containers flashed %v", m.flash)
	}
}

func TestShellRefusesStoppedContainer(t *testing.T) {
	m := Model{cols: table.Catalog[:4], width: 120, height: 24}
	rs := fleet(2)
	rs[0].State = "exited"
	m = feed(m, rs, false)

	m = pressKey(m, "e")
	if !strings.Contains(statusLine(m), "container-00 is not running") {
		t.Errorf("status %q, want the refusal", statusLine(m))
	}
}

func TestNoShellNeedsNoKeys(t *testing.T) {
	cases := []struct {
		code  int
		typed bool
		want  bool
	}{
		{127, false, true},
		{126, false, true},
		{127, true, false}, // last command not found, then ctrl+d
		{0, false, false},
		{1, false, false},
	}
	for _, c := range cases {
		if got := noShell(c.code, c.typed); got != c.want {
			t.Errorf("noShell(%d, %v) = %v, want %v", c.code, c.typed, got, c.want)
		}
	}
}

func TestStatusWithNoticeFitsWindow(t *testing.T) {
	for _, width := range []int{8, 20, 40, 80} {
		m := Model{cols: table.Catalog[:4], width: width, height: 24}
		m = feed(m, fleet(30), false)
		m.say(ansiYellow, "plane-app-plane-minio-1 is not running")
		if got := table.Width(stripANSI(statusLine(m))); got > width {
			t.Errorf("width %d: status is %d cells: %q", width, got, statusLine(m))
		}
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
