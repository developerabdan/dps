package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/developerabdan/dps/internal/model"
	"github.com/developerabdan/dps/internal/table"
)

// rows builds n containers, spread over projects when group is set, so the
// grouped layout gets headings to place.
func rows(n int, grouped bool) []model.Container {
	out := make([]model.Container, n)
	for i := range out {
		out[i] = model.Container{
			Name:  fmt.Sprintf("container-%02d", i),
			State: "running",
			Image: "alpine:3.20",
		}
		if grouped {
			out[i].Project = fmt.Sprintf("proj-%d", i/10)
		}
	}
	return out
}

func newModel(n int, grouped bool, width, height int) Model {
	m := Model{
		cols:   table.Catalog[:4],
		group:  grouped,
		width:  width,
		height: height,
	}
	next, _ := m.Update(rowsMsg(rows(n, grouped)))
	return next.(Model)
}

func lines(m Model) []string {
	return strings.Split(m.View().Content, "\n")
}

// cursorLineOf finds the highlighted line, which is the one the reverse-video
// code opens.
func cursorLineOf(t *testing.T, ls []string) int {
	t.Helper()
	for i, l := range ls {
		if strings.HasPrefix(l, ansiReverse) {
			return i
		}
	}
	t.Fatalf("no highlighted row on screen")
	return -1
}

func TestFrameNeverExceedsWindow(t *testing.T) {
	for _, grouped := range []bool{false, true} {
		for _, height := range []int{6, 10, 24, 40, 80} {
			m := newModel(37, grouped, 120, height)
			if got := len(lines(m)); got > height {
				t.Errorf("grouped=%v height=%d: frame is %d lines", grouped, height, got)
			}
		}
	}
}

func TestCursorStaysOnScreen(t *testing.T) {
	m := newModel(37, false, 120, 24)
	for i := 0; i < 36; i++ {
		m.moveCursor(1)
		if m.cursor != i+1 {
			t.Fatalf("cursor %d after %d moves", m.cursor, i+1)
		}
		cursorLineOf(t, lines(m)) // fails the test when the row is off screen
	}
	if m.offset == 0 {
		t.Errorf("window never scrolled: offset %d", m.offset)
	}

	// Back to the top and the window comes with it.
	m.cursor = 0
	m.ensureVisible()
	if m.offset != 0 {
		t.Errorf("offset %d at the first row, want 0", m.offset)
	}
}

func TestShortListDoesNotScroll(t *testing.T) {
	m := newModel(5, false, 120, 24)
	m.cursor = 4
	m.ensureVisible()
	if m.offset != 0 {
		t.Errorf("offset %d for a list that fits, want 0", m.offset)
	}
}

func TestWheelScrollsAndDragsCursor(t *testing.T) {
	m := newModel(37, false, 120, 24)
	m.scroll(scrollJump)
	if m.offset != scrollJump {
		t.Fatalf("offset %d after one notch, want %d", m.offset, scrollJump)
	}
	// The cursor was on row 0, which the window has left behind, so it comes
	// to the first row still on screen.
	if m.cursor != scrollJump {
		t.Errorf("cursor %d after scrolling past it, want %d", m.cursor, scrollJump)
	}
	cursorLineOf(t, lines(m))

	// Scrolling far past the end stops at the last screenful.
	m.scroll(1000)
	if want := len(m.slots()) - m.bodyHeight(); m.offset != want {
		t.Errorf("offset %d at the end, want %d", m.offset, want)
	}
	m.scroll(-1000)
	if m.offset != 0 {
		t.Errorf("offset %d at the top, want 0", m.offset)
	}
}

func TestGroupHeadingFollowsItsFirstRow(t *testing.T) {
	m := newModel(37, true, 120, 24)
	// proj-2 starts at row 20, well below the first screenful.
	m.cursor = 20
	m.ensureVisible()
	ls := lines(m)
	cursor := cursorLineOf(t, ls)
	if cursor == 0 {
		t.Fatalf("cursor on the header line")
	}
	if !strings.Contains(ls[cursor-1], "proj-2") {
		t.Errorf("heading missing above the first row of its group: %q", ls[cursor-1])
	}
}

func TestWindowResizeKeepsCursorVisible(t *testing.T) {
	m := newModel(37, false, 120, 40)
	m.cursor = 30
	m.ensureVisible()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 12})
	m = next.(Model)
	cursorLineOf(t, lines(m))
}

func TestFewerRowsPullsWindowBack(t *testing.T) {
	m := newModel(37, false, 120, 24)
	m.cursor = 36
	m.ensureVisible()
	next, _ := m.Update(rowsMsg(rows(3, false)))
	m = next.(Model)
	if m.offset != 0 {
		t.Errorf("offset %d after the list shrank, want 0", m.offset)
	}
	cursorLineOf(t, lines(m))
}
