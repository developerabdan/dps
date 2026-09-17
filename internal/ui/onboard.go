// onboard.go is the first-run setup: pick the columns, pick grouped or flat,
// and the config file is written. It runs once, when no config exists yet, and
// again on demand with --onboard.
//
// Nothing it sets is a gate. Every choice here is also a flag, and a machine
// that never sees the wizard still runs dps with the same defaults. The wizard
// exists because a column set nobody knows about is a column set nobody uses.
package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/developerabdan/dps/internal/config"
	"github.com/developerabdan/dps/internal/model"
	"github.com/developerabdan/dps/internal/table"
)

// step is which screen the wizard is showing.
type step int

const (
	stepCols step = iota
	stepView
	stepDone
)

// shortHeight is the window height under which the column screen drops its
// preview. The picker and a preview together need about this many lines, and
// when only one can fit it is the picker — that is the part being operated.
const shortHeight = 24

// colDesc explains each column in the few words that fit beside the picker.
// The catalog carries widths and priorities, not prose, so the prose lives
// here next to the only screen that prints it.
var colDesc = map[string]string{
	"name":    "container name, project prefix removed",
	"state":   "running state and uptime",
	"image":   "image, registry stripped",
	"ports":   "published ports",
	"cpu":     "CPU graph, live view only",
	"health":  "healthcheck result",
	"created": "age",
	"project": "compose project",
	"service": "compose service",
	"ip":      "container IP",
	"size":    "writable layer size (extra query)",
	"id":      "short container ID",
	"cmd":     "entrypoint command",
}

// wizard is the Bubble Tea model behind the setup screens.
type wizard struct {
	cfg config.Config

	step   step
	cursor int // which column the picker is on
	view   int // 0 grouped, 1 flat

	keys   []string
	picked map[string]bool

	width  int
	height int

	// saved reports that the config file was written. Onboard reads it to tell
	// a finished run from one that was abandoned, because an abandoned run must
	// leave no file behind — the next dps has to offer the wizard again.
	saved bool
	err   error
	path  string

	// colsOnly is the wizard started by --pick-cols: the column question
	// alone, saved on enter, with the rest of the config left as it is.
	colsOnly bool
}

// newWizard starts the wizard from the configuration already in force, so
// --onboard on a configured machine opens with that machine's answers ticked
// rather than asking someone to enter them again.
func newWizard(cfg config.Config) wizard {
	w := wizard{
		cfg:    cfg,
		keys:   table.Keys(),
		picked: map[string]bool{},
		width:  80,
		height: 24,
	}
	for _, k := range cfg.Cols {
		w.picked[k] = true
	}
	if !cfg.GroupByProject {
		w.view = 1
	}
	return w
}

// Onboard runs the setup and reports whether it finished. A wizard the user
// backed out of returns false with no error and writes nothing.
func Onboard(ctx context.Context, cfg config.Config) (bool, error) {
	out, err := tea.NewProgram(newWizard(cfg), tea.WithContext(ctx)).Run()
	if err != nil {
		return false, err
	}
	final, ok := out.(wizard)
	if !ok {
		return false, nil
	}
	return final.saved, final.err
}

// PickColumns asks only which columns to show and saves the answer as the
// default. It returns the saved columns, or nothing when the user backed out.
func PickColumns(ctx context.Context, cfg config.Config) ([]string, error) {
	w := newWizard(cfg)
	w.colsOnly = true
	out, err := tea.NewProgram(w, tea.WithContext(ctx)).Run()
	if err != nil {
		return nil, err
	}
	final, ok := out.(wizard)
	if !ok || !final.saved {
		return nil, final.err
	}
	return final.cfg.Cols, nil
}

func (w wizard) Init() tea.Cmd { return nil }

func (w wizard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		w.width, w.height = msg.Width, msg.Height
		return w, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return w, tea.Quit
		}
		switch w.step {
		case stepCols:
			return w.updateCols(msg)
		case stepView:
			return w.updateView(msg)
		default:
			return w.updateDone(msg)
		}
	}
	return w, nil
}

func (w wizard) updateCols(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Both spellings of a key are accepted throughout the wizard. The terminal
	// decides which one arrives, and a toggle that silently does nothing on
	// some terminals is not worth the one extra word here.
	switch msg.String() {
	case "esc", "escape":
		return w, tea.Quit
	case "up", "k":
		w.cursor = clamp(w.cursor-1, len(w.keys))
	case "down", "j":
		w.cursor = clamp(w.cursor+1, len(w.keys))
	case "home", "g":
		w.cursor = 0
	case "end", "G":
		w.cursor = len(w.keys) - 1
	case " ", "space", "x":
		key := w.keys[w.cursor]
		if w.picked[key] {
			delete(w.picked, key)
		} else {
			w.picked[key] = true
		}
	case "enter":
		// A table with no columns has nothing to print, so the step simply does
		// not advance. The hint under the list already says what is missing.
		if len(w.selected()) == 0 {
			break
		}
		if !w.colsOnly {
			w.step = stepView
			break
		}
		w.cfg.Cols = w.result()
		if err := w.cfg.Save(); err != nil {
			w.err = err
			return w, tea.Quit
		}
		w.saved = true
		return w, tea.Quit
	}
	return w, nil
}

func (w wizard) updateView(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "escape":
		w.step = stepCols
	case "up", "k", "down", "j", "left", "h", "right", "l", "tab":
		w.view = 1 - w.view
	case "enter":
		w.cfg.Cols = w.selected()
		w.cfg.GroupByProject = w.view == 0
		if err := w.cfg.Save(); err != nil {
			w.err = err
			return w, tea.Quit
		}
		w.saved = true
		w.path, _ = config.Path()
		w.step = stepDone
	}
	return w, nil
}

func (w wizard) updateDone(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", "esc", "escape":
		return w, tea.Quit
	}
	return w, nil
}

// selected returns the picked keys in catalog order. The order the boxes were
// ticked in is not the order the table reads well in, so the catalog decides.
func (w wizard) selected() []string {
	out := make([]string, 0, len(w.keys))
	for _, k := range w.keys {
		if w.picked[k] {
			out = append(out, k)
		}
	}
	return out
}

// result is the column list the wizard would save. The first run has no order
// to keep, so it uses the catalog's. --pick-cols changes an existing set, and
// an order set by hand is kept.
func (w wizard) result() []string {
	if w.colsOnly {
		return ordered(w.cfg.Cols, w.picked)
	}
	return w.selected()
}

// View draws the wizard inline rather than on the alternate screen. The last
// frame is the congratulations, and the alternate screen would wipe it the
// moment the program exits.
func (w wizard) View() tea.View {
	switch w.step {
	case stepCols:
		return tea.NewView(w.viewCols())
	case stepView:
		return tea.NewView(w.viewArrange())
	default:
		return tea.NewView(w.viewDone())
	}
}

func (w wizard) viewCols() string {
	var b strings.Builder
	b.WriteString(w.title("Which columns should dps show?"))

	for i, key := range w.keys {
		b.WriteString(pickLine(key, w.picked[key], i == w.cursor, w.width) + "\n")
	}

	// The preview is the first thing to go in a short window: the list is what
	// the keys operate, and a wizard that scrolls is worse than one that shows
	// less.
	if w.height >= shortHeight {
		if cols, err := table.Resolve(w.result()); err == nil {
			b.WriteString("\n")
			b.WriteString("  " + ansiDim + "example" + ansiReset + "\n")
			b.WriteString(preview(cols, false, w.previewWidth(), 2))
		}
	}

	b.WriteString("\n")
	if len(w.selected()) == 0 {
		b.WriteString("  " + ansiYellow + "pick at least one column" + ansiReset + "\n")
	}
	if w.colsOnly {
		b.WriteString(w.help("↑↓ move · space toggle · enter save · esc quit"))
	} else {
		b.WriteString(w.help("↑↓ move · space toggle · enter next · esc quit"))
	}
	return b.String()
}

func (w wizard) viewArrange() string {
	var b strings.Builder
	b.WriteString(w.title("How should dps arrange the rows?"))

	options := []struct{ label, note string }{
		{"grouped by project", "one heading per compose project"},
		{"one flat list", "every container in one run"},
	}
	for i, o := range options {
		mark, point := " ", "  "
		if i == w.view {
			mark, point = "•", "› "
		}
		line := fmt.Sprintf("%s(%s) %-20s %s", point, mark, o.label, o.note)
		line = table.Truncate(line, w.width, table.TruncTail)
		if i == w.view {
			line = ansiBold + line + ansiReset
		}
		b.WriteString("  " + line + "\n")
	}

	// The preview follows the highlighted option, so the difference between the
	// two is read rather than described.
	if cols, err := table.Resolve(w.selected()); err == nil {
		b.WriteString("\n")
		b.WriteString("  " + ansiDim + "example" + ansiReset + "\n")
		b.WriteString(preview(cols, w.view == 0, w.previewWidth(), 2))
	}

	b.WriteString("\n")
	b.WriteString(w.help("↑↓ choose · enter save · esc back"))
	return b.String()
}

func (w wizard) viewDone() string {
	view := "grouped by project"
	if !w.cfg.GroupByProject {
		view = "one flat list"
	}

	var b strings.Builder
	b.WriteString("\n  " + ansiBold + "All set." + ansiReset + "\n\n")
	b.WriteString(fmt.Sprintf("  %-8s %s\n", "columns", strings.Join(w.cfg.Cols, ", ")))
	b.WriteString(fmt.Sprintf("  %-8s %s\n", "view", view))
	b.WriteString(fmt.Sprintf("  %-8s %s\n", "saved", shortenHome(w.path)))
	b.WriteString("\n  Run it:  " + ansiBold + "dps" + ansiReset + "\n")
	b.WriteString("  " + ansiDim + "Change it later:  dps --onboard" + ansiReset + "\n\n")
	b.WriteString(w.help("[ enter ] quit"))
	return b.String()
}

func (w wizard) title(question string) string {
	head := ansiDim + "dps — first run" + ansiReset
	if w.colsOnly {
		head = ansiDim + "dps — columns" + ansiReset
	}
	return "\n  " + head + "\n\n  " + ansiBold + question + ansiReset + "\n\n"
}

func (w wizard) help(keys string) string {
	return "  " + ansiDim + table.Truncate(keys, w.width, table.TruncTail) + ansiReset + "\n"
}

// previewWidth is the width the example table is fitted to. It is the window
// minus the indent, so a narrow window drops columns in the preview exactly as
// it would in the real table.
func (w wizard) previewWidth() int {
	if n := w.width - 4; n > 20 {
		return n
	}
	return 20
}

// preview renders the example table with the chosen columns. It uses fixed
// sample containers rather than the live ones: the sample always has two
// projects and one container outside them, so grouped and flat actually look
// different, which is the whole point of showing it.
func preview(cols []table.Column, group bool, width, indent int) string {
	// The sample has no daemon to sample, so the cpu column draws a fixed
	// history. An empty graph would not show what the column is for.
	cols = append([]table.Column(nil), cols...)
	for i := range cols {
		if cols[i].Key == "cpu" {
			cols[i].Value = func(c model.Container) string {
				return cpuCell(sampleCPU[c.ID], c.State == "running")
			}
		}
	}

	var b strings.Builder
	_ = table.Render(&b, cols, sampleRows(), table.RenderOptions{
		Width:  width,
		Color:  true,
		Footer: false,
		Group:  group,
	})

	pad := strings.Repeat(" ", indent)
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n") + "\n"
}

// sampleRows is the example set: two compose projects and one container
// started outside compose, so every screen the wizard draws shows the same
// familiar shape.
func sampleRows() []model.Container {
	now := time.Now().Unix()
	return []model.Container{
		{
			ID: "0dec549e57b9", Name: "/plane-app-proxy-1", Image: "artifacts.plane.so/makeplane/plane-proxy:v1.3.1",
			State: "running", Status: "Up 3 hours", Health: "healthy",
			Ports:   []model.Port{{IP: "127.0.0.1", Public: 8090, Private: 80, Type: "tcp"}},
			Created: now - 3*3600, Project: "plane-app", Service: "proxy", IP: "172.24.0.7",
			Size: 12_300_000, Command: "caddy run",
		},
		{
			ID: "5b1c8ad41f02", Name: "/plane-app-api-1", Image: "artifacts.plane.so/makeplane/plane-backend:v1.3.1",
			State: "running", Status: "Up 3 hours",
			Created: now - 3*3600, Project: "plane-app", Service: "api", IP: "172.24.0.4",
			Size: 48_000_000, Command: "./bin/docker-entrypoint-api.sh",
		},
		{
			ID: "9f77e0b3c145", Name: "/shop-web-1", Image: "nginx:1.27-alpine",
			State: "running", Status: "Up 6 days",
			Ports:   []model.Port{{IP: "0.0.0.0", Public: 8080, Private: 80, Type: "tcp"}},
			Created: now - 6*24*3600, Project: "shop", Service: "web", IP: "172.19.0.2",
			Size: 4_100_000, Command: "nginx -g daemon off;",
		},
		{
			ID: "c204a6e91bd7", Name: "/shop-db-1", Image: "postgres:15.7-alpine",
			State: "exited", Status: "Exited (0) 2 hours ago",
			Created: now - 6*24*3600, Project: "shop", Service: "db",
			Size: 31_000_000, Command: "postgres",
		},
		{
			ID: "76ba0f5d3e88", Name: "/launch-postgres", Image: "postgres:16",
			State: "running", Status: "Up 12 minutes",
			Ports:   []model.Port{{IP: "0.0.0.0", Public: 5432, Private: 5432, Type: "tcp"}},
			Created: now - 12*60, IP: "172.17.0.2",
			Size: 29_000_000, Command: "postgres",
		},
	}
}

// sampleCPU is the CPU history the example rows draw, in percent.
var sampleCPU = map[string][]float64{
	"0dec549e57b9": {0.3, 0.2, 0.4, 0.3, 0.2, 0.5, 0.3, 0.2, 0.3, 0.4},
	"5b1c8ad41f02": {12, 18, 35, 61, 88, 72, 44, 30, 22, 38.1},
	"9f77e0b3c145": {2.1, 2.4, 1.9, 2.2, 6.8, 7.4, 2.3, 2.0, 2.2, 2.1},
	"76ba0f5d3e88": {4, 4.2, 5.1, 9.8, 15.2, 14.9, 8.3, 5.2, 4.4, 4.6},
}

// shortenHome prints ~/… for a path under the home directory. The config path
// is read, not pasted, and the tilde form is the one people recognise.
func shortenHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(path, home+string(os.PathSeparator)) {
		return path
	}
	return "~" + strings.TrimPrefix(path, home)
}

func clamp(i, n int) int {
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}
