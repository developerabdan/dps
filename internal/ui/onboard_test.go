package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/developerabdan/dps/internal/config"
	"github.com/developerabdan/dps/internal/table"
)

// press feeds keystrokes to the wizard the way the program would. The tests
// drive real key messages rather than calling the step functions directly, so
// a key whose name changes under it is a failure here rather than a toggle
// that quietly stops working on someone's terminal.
func press(t *testing.T, w wizard, keys ...tea.Key) wizard {
	t.Helper()
	for _, k := range keys {
		next, _ := w.Update(tea.KeyPressMsg(k))
		w = next.(wizard)
	}
	return w
}

func key(code rune) tea.Key { return tea.Key{Code: code} }

// moveTo returns the presses that put the cursor on a named column.
func moveTo(t *testing.T, w wizard, name string) wizard {
	t.Helper()
	for i, k := range w.keys {
		if k == name {
			w = press(t, w, key(tea.KeyHome))
			for j := 0; j < i; j++ {
				w = press(t, w, key(tea.KeyDown))
			}
			return w
		}
	}
	t.Fatalf("no column named %q in the catalog", name)
	return w
}

func TestWizardSavesTheAnswers(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	w := newWizard(config.Default())
	w = moveTo(t, w, "health")
	w = press(t, w, key(tea.KeySpace))
	w = press(t, w, key(tea.KeyEnter))
	if w.step != stepView {
		t.Fatalf("enter did not advance to the arrangement question, step = %v", w.step)
	}

	w = press(t, w, key(tea.KeyDown)) // grouped -> flat
	w = press(t, w, key(tea.KeyEnter))
	if w.err != nil {
		t.Fatalf("saving: %v", w.err)
	}
	if !w.saved || w.step != stepDone {
		t.Fatalf("saved = %v, step = %v; want true and stepDone", w.saved, w.step)
	}

	if !config.Exists() {
		t.Fatal("the wizard finished but wrote no config file")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("loading what the wizard wrote: %v", err)
	}
	want := []string{"name", "state", "image", "ports", "health"}
	if strings.Join(cfg.Cols, ",") != strings.Join(want, ",") {
		t.Errorf("cols = %v, want %v", cfg.Cols, want)
	}
	if cfg.GroupByProject {
		t.Error("group_by_project = true, but the flat view was chosen")
	}

	// The closing screen is the only place the file is named, so it has to
	// carry the path it actually wrote.
	done := w.viewDone()
	if !strings.Contains(done, "All set.") || !strings.Contains(done, "config.json") {
		t.Errorf("the congratulations screen does not name the saved file:\n%s", done)
	}
}

func TestWizardKeepsPresetsItDidNotAskAbout(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	w := newWizard(config.Default())
	w = press(t, w, key(tea.KeyEnter), key(tea.KeyEnter))
	if !w.saved {
		t.Fatal("the wizard did not save")
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if _, ok := cfg.Presets["debug"]; !ok {
		t.Errorf("the saved file lost the default presets: %v", cfg.Presets)
	}
}

func TestWizardQuitBeforeTheEndWritesNothing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	w := newWizard(config.Default())
	w = press(t, w, key(tea.KeyEscape))
	if w.saved {
		t.Error("a wizard that was quit reports itself as finished")
	}
	// Nothing on disk is what makes the setup open again next time.
	if config.Exists() {
		t.Error("quitting the wizard still wrote a config file")
	}
}

func TestWizardRefusesAnEmptyColumnSet(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	w := newWizard(config.Default())
	for _, name := range config.DefaultCols {
		w = moveTo(t, w, name)
		w = press(t, w, key(tea.KeySpace))
	}
	if len(w.selected()) != 0 {
		t.Fatalf("space did not untick the defaults; still picked: %v", w.selected())
	}

	w = press(t, w, key(tea.KeyEnter))
	if w.step != stepCols {
		t.Error("the wizard advanced with no columns selected")
	}
	if !strings.Contains(w.viewCols(), "pick at least one column") {
		t.Error("the screen does not say why enter did nothing")
	}
}

func TestWizardPicksInCatalogOrderNotTickOrder(t *testing.T) {
	w := newWizard(config.Config{Cols: []string{"ports"}})
	w = moveTo(t, w, "name")
	w = press(t, w, key(tea.KeySpace))

	// name was ticked last but leads, because the catalog decides the order a
	// table reads well in.
	if got := strings.Join(w.selected(), ","); got != "name,ports" {
		t.Errorf("selected = %q, want \"name,ports\"", got)
	}
}

func TestPreviewShowsTheDifferenceBetweenTheTwoViews(t *testing.T) {
	cols, err := table.Resolve(config.DefaultCols)
	if err != nil {
		t.Fatalf("resolving the default columns: %v", err)
	}

	grouped := preview(cols, true, 80, 2)
	flat := preview(cols, false, 80, 2)

	// The sample has to carry both a project and a container outside one, or
	// the grouped screen shows nothing the flat screen does not.
	for _, want := range []string{"plane-app", table.NoProject} {
		if !strings.Contains(grouped, want) {
			t.Errorf("the grouped example is missing %q:\n%s", want, grouped)
		}
	}
	if strings.Contains(flat, "plane-app") {
		t.Errorf("the flat example prints a project heading:\n%s", flat)
	}
	if !strings.Contains(flat, "proxy") || !strings.Contains(grouped, "proxy") {
		t.Error("both examples should list the same containers")
	}
}
