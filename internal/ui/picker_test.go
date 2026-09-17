package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/developerabdan/dps/internal/config"
	"github.com/developerabdan/dps/internal/table"
)

func pickTo(t *testing.T, m Model, name string) Model {
	t.Helper()
	for i, k := range table.Keys() {
		if k == name {
			m.pick.cursor = i
			return m
		}
	}
	t.Fatalf("no column named %q", name)
	return m
}

func space(m Model) Model {
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	return next.(Model)
}

func TestPickerAppliesAtOnceAndCancelPutsItBack(t *testing.T) {
	m := Model{cols: mustCols(t, "name", "state"), width: 120, height: 40}
	m = feed(m, fleet(3), false)

	m = pressKey(m, "c")
	m = space(pickTo(t, m, "cpu"))
	m = space(pickTo(t, m, "size"))
	if got := strings.Join(colKeys(m.cols), ","); got != "name,state,cpu,size" {
		t.Errorf("cols %q while picking", got)
	}
	if !m.opt.Size {
		t.Error("size ticked, but the list is not asked for with size")
	}
	if header := stripANSI(lines(m)[m.pickerLines()]); !strings.Contains(header, "CPU") {
		t.Errorf("the table under the picker does not show the new column: %q", header)
	}

	m = escape(m)
	if got := strings.Join(colKeys(m.cols), ","); m.mode != modeList || got != "name,state" || m.opt.Size {
		t.Errorf("after esc: mode %v, cols %q, size %v; want everything as before", m.mode, got, m.opt.Size)
	}
}

func TestPickerSavesTheDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Default()
	cfg.GroupByProject = false
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	m := Model{cols: mustCols(t, "name", "state"), width: 120, height: 40}
	m = feed(m, fleet(3), false)
	m = space(pickTo(t, pressKey(m, "c"), "health"))
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)

	saved, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(saved.Cols, ","); got != "name,state,health" {
		t.Errorf("saved cols %q", got)
	}
	if saved.GroupByProject {
		t.Error("saving columns changed the saved view")
	}
	if m.mode != modeList || !strings.Contains(statusLine(m), "columns saved") {
		t.Errorf("mode %v, status %q", m.mode, statusLine(m))
	}
}

func TestPickerKeepsOneColumn(t *testing.T) {
	m := Model{cols: mustCols(t, "name"), width: 120, height: 40}
	m = feed(m, fleet(2), false)
	m = space(pickTo(t, pressKey(m, "c"), "name"))
	if len(m.cols) != 1 || !strings.Contains(statusLine(m), "keep at least one column") {
		t.Errorf("cols %v, status %q", colKeys(m.cols), statusLine(m))
	}
}

func TestPickerFrameFitsTheWindow(t *testing.T) {
	for _, height := range []int{8, 10, 16, 24, 40} {
		m := Model{cols: mustCols(t, "name", "state"), width: 100, height: height}
		m = feed(m, fleet(30), false)
		m = pressKey(m, "c")
		// Walk the cursor to the last column, so the list has to scroll.
		for range table.Keys() {
			next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			m = next.(Model)
			ls := lines(m)
			if len(ls) > height {
				t.Fatalf("height %d: frame is %d lines", height, len(ls))
			}
			if !strings.Contains(strings.Join(ls, "\n"), "› [") {
				t.Fatalf("height %d: cursor %d is not on screen", height, m.pick.cursor)
			}
		}
	}
}

func TestOrderedKeepsAnOrderSetByHand(t *testing.T) {
	picked := map[string]bool{"ports": true, "name": true, "state": true}
	if got := strings.Join(ordered([]string{"ports", "name"}, picked), ","); got != "ports,name,state" {
		t.Errorf("hand order: %q", got)
	}

	picked = map[string]bool{"name": true, "state": true, "image": true, "cpu": true, "health": true}
	if got := strings.Join(ordered([]string{"name", "image", "health"}, picked), ","); got != "name,state,image,cpu,health" {
		t.Errorf("catalog order: %q", got)
	}
}

func TestPickColsSavesOnlyTheColumns(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg := config.Default()
	cfg.Cols = []string{"ports", "name"}
	cfg.GroupByProject = false
	w := newWizard(cfg)
	w.colsOnly = true
	w = moveTo(t, w, "cpu")
	w = press(t, w, key(tea.KeySpace), key(tea.KeyEnter))
	if !w.saved {
		t.Fatal("enter did not save")
	}

	saved, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(saved.Cols, ","); got != "ports,name,cpu" {
		t.Errorf("saved cols %q, want the hand order kept", got)
	}
	if saved.GroupByProject {
		t.Error("--pick-cols changed the saved view")
	}
}

func TestPreviewDrawsASampleGraph(t *testing.T) {
	cols := mustCols(t, "name", "cpu")
	if out := preview(cols, false, 80, 2); graphPart(out) == "" {
		t.Errorf("the example has no cpu graph:\n%s", out)
	}
}
