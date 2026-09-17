package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/developerabdan/dps/internal/model"
	"github.com/developerabdan/dps/internal/table"
)

func mustCols(t *testing.T, keys ...string) []table.Column {
	t.Helper()
	cols, err := table.Resolve(keys)
	if err != nil {
		t.Fatal(err)
	}
	return cols
}

// busy is a sample taken sec seconds in, from a one-CPU host whose container
// has used cpu units of the 1000 the host spends each second.
func busy(sec int, cpu uint64) model.Sample {
	return model.Sample{
		Read:       time.Unix(1_800_000_000+int64(sec), 0),
		CPUTotal:   cpu,
		SystemCPU:  uint64(sec) * 1000,
		OnlineCPUs: 1,
		MemUsage:   50e6,
		MemLimit:   1e9,
		DiskRead:   uint64(sec) * 200e3,
		DiskWrite:  uint64(sec) * 50e3,
		NetRx:      uint64(sec) * 30e3,
		NetTx:      uint64(sec) * 5e3,
		PIDs:       7,
	}
}

func feedStats(m Model, samples map[string]model.Sample) Model {
	next, _ := m.Update(statsMsg{samples: samples})
	return next.(Model)
}

// withHistory gives every running container in m two samples, which is one
// CPU value of 10%.
func withHistory(m Model) Model {
	first, second := map[string]model.Sample{}, map[string]model.Sample{}
	for _, r := range m.rows {
		if r.State == "running" {
			first[rowKey(r)] = busy(0, 0)
			second[rowKey(r)] = busy(2, 200)
		}
	}
	return feedStats(feedStats(m, first), second)
}

// rowLine is the printed line of row i in a flat list: the header is line 0.
func rowLine(m Model, i int) string { return lines(m)[1+i] }

func TestCPUColumnKeepsItsWidthBeforeTheFirstSample(t *testing.T) {
	m := Model{cols: mustCols(t, "name", "cpu", "state"), width: 120, height: 24}
	m = feed(m, fleet(3), false)

	plain := stripANSI(rowLine(m, 1))
	want := strings.Repeat(" ", table.CPUBars) + " " + "     …"
	if !strings.Contains(plain, want) {
		t.Errorf("row %q has no full-width placeholder %q", plain, want)
	}
}

func TestCPUColumnDrawsTheHistory(t *testing.T) {
	m := Model{cols: mustCols(t, "name", "cpu", "state"), width: 120, height: 24}
	m = withHistory(feed(m, fleet(3), false))

	plain := stripANSI(rowLine(m, 1))
	if !strings.Contains(plain, " 10.0%") || graphPart(plain) == "" {
		t.Errorf("row %q has no graph or no 10.0%%", plain)
	}
	if strings.ContainsRune(plain, '█') {
		t.Errorf("row %q uses the full block, so rows would touch", plain)
	}
}

func TestCPUGraphHasSpaceOnBothSides(t *testing.T) {
	m := Model{cols: mustCols(t, "name", "cpu", "state"), width: 120, height: 24}
	m = withHistory(feed(m, fleet(3), false))

	for i, name := range []string{"cursor row", "plain row"} {
		plain := stripANSI(rowLine(m, i))
		graph := graphPart(plain)
		before, after, _ := strings.Cut(plain, graph)
		if !strings.HasSuffix(before, strings.Repeat(" ", table.Gap)) {
			t.Errorf("%s: graph touches the column before it: %q", name, plain)
		}
		pct, _, _ := strings.Cut(after, "%")
		if !strings.HasPrefix(pct, " ") {
			t.Errorf("%s: graph touches its number: %q", name, plain)
		}
		if rest := after[len(pct)+1:]; !strings.HasPrefix(rest, strings.Repeat(" ", table.Gap)) {
			t.Errorf("%s: number touches the column after it: %q", name, plain)
		}
	}
}

func TestSelectedGraphIsNotReversed(t *testing.T) {
	m := Model{cols: mustCols(t, "name", "cpu", "state"), width: 120, height: 24}
	m = withHistory(feed(m, fleet(3), false))

	line := lines(m)[1]
	if !strings.HasPrefix(line, ansiReverse) {
		t.Fatalf("line 1 is not the cursor row: %q", line)
	}
	if !strings.Contains(line, ansiReset+ansiCyan+" ") && !strings.Contains(line, ansiReset+ansiCyan+"▁") {
		t.Errorf("the graph on the cursor row is inside the reverse video: %q", line)
	}
}

func TestStoppedContainerHasNoGraph(t *testing.T) {
	m := Model{cols: mustCols(t, "name", "cpu"), width: 120, height: 24}
	rs := fleet(3)
	rs[2].State = "exited"
	m = withHistory(feed(m, rs, false))

	if plain := stripANSI(rowLine(m, 2)); !strings.HasSuffix(plain, table.Empty) {
		t.Errorf("stopped row %q, want a dash", plain)
	}
}

func TestSamplesOnlyWhatIsDrawn(t *testing.T) {
	m := Model{cols: mustCols(t, "name", "state"), width: 120, height: 24}
	rs := fleet(3)
	rs[1].State = "exited"
	m = feed(m, rs, false)
	if keys := m.sampleKeys(); len(keys) != 0 {
		t.Errorf("no graph on screen, but sampling %v", keys)
	}

	m.cols = mustCols(t, "name", "cpu")
	if got := strings.Join(m.sampleKeys(), ","); got != "id-00,id-02" {
		t.Errorf("sampling %q, want the running containers", got)
	}
}

func TestHistoryOfARemovedContainerIsForgotten(t *testing.T) {
	m := Model{cols: mustCols(t, "name", "cpu"), width: 120, height: 24}
	m = withHistory(feed(m, fleet(3), false))
	m = feed(m, fleet(2), false)
	m = feedStats(m, map[string]model.Sample{"id-00": busy(4, 400)})
	if _, ok := m.series["id-02"]; ok {
		t.Error("the history of a container that is gone is still kept")
	}
}

func TestShellAsksFirst(t *testing.T) {
	m := Model{cols: table.Catalog[:4], width: 120, height: 24}
	m = feed(m, fleet(3), false)

	m = pressKey(m, "e")
	if m.confirm == nil || !strings.Contains(statusLine(m), "open a shell in container-00? y/n") {
		t.Fatalf("status %q, want the question", statusLine(m))
	}

	// Any other key cancels, and does nothing else.
	m = pressKey(m, "j")
	if m.confirm != nil || m.cursor != 0 {
		t.Errorf("confirm %v, cursor %d after j; want cancelled and not moved", m.confirm, m.cursor)
	}
	if !strings.Contains(statusLine(m), "shell cancelled") {
		t.Errorf("status %q, want the cancel said", statusLine(m))
	}
}

func TestShellAnswerChecksTheContainerAgain(t *testing.T) {
	m := Model{cols: table.Catalog[:4], width: 120, height: 24}
	m = feed(m, fleet(3), false)
	m = pressKey(m, "e")

	// The container stopped while the question was on screen.
	rs := fleet(3)
	rs[0].State = "exited"
	m = feed(m, rs, false)

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.confirm != nil || !strings.Contains(statusLine(m), "container-00 is not running") {
		t.Errorf("status %q, want the refusal", statusLine(m))
	}
}
