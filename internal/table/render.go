package table

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/developerabdan/dps/internal/model"
)

// ANSI codes are written directly. The alternative is a styling dependency,
// and the plain path is meant to stay free of those.
const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiGreen  = "\x1b[32m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
)

// NoProject is the heading for containers started outside compose.
const NoProject = "(no project)"

// GroupIndent is how far a row sits under its project heading. The TUI uses
// the same value, so both views indent by the same amount.
const GroupIndent = "  "

// IndentFirst returns cols with the leading column's values prefixed. The
// column's own bounds grow by the same amount, so width fitting still sees
// the true printed length and the indent is never silently truncated away.
//
// The TUI calls this too, so both views indent by the same rule.
func IndentFirst(cols []Column, prefix string) []Column {
	if len(cols) == 0 {
		return cols
	}
	out := append([]Column(nil), cols...)
	inner := out[0].Value
	pad := Width(prefix)
	out[0].Value = func(c model.Container) string {
		v := inner(c)
		if v == "" {
			v = Empty
		}
		return prefix + v
	}
	out[0].Min += pad
	out[0].Max += pad
	return out
}

// RenderOptions controls one table. Color is false whenever stdout is not a
// terminal, which is what makes `dps | grep` behave.
type RenderOptions struct {
	Width  int
	Color  bool
	Wide   bool
	Footer bool
	Group  bool

	// Warn receives the hidden-column notice. It is deliberately separate
	// from the summary footer: the footer is decoration and belongs only on a
	// terminal, but a dropped column is missing data and must be reported
	// even when stdout is a pipe. Sending it here keeps it out of piped
	// output while still saying it out loud.
	Warn io.Writer
}

// Render writes the table. With Wide set, nothing is truncated or dropped and
// the line is allowed to run past the terminal.
func Render(w io.Writer, cols []Column, rows []model.Container, opt RenderOptions) error {
	width := opt.Width
	if opt.Wide {
		width = math.MaxInt32
	}

	// Grouped rows are indented inside their first column rather than by
	// shifting the whole line. Shifting the line would move every column
	// after it too, and `dps -g | awk` would stop lining up.
	if opt.Group {
		cols = IndentFirst(cols, GroupIndent)
	}

	// Fitting runs once over every row, not once per group. Measuring each
	// group separately would give each its own column widths, and the table
	// would stop lining up down the page.
	active, widths, dropped := Fit(cols, rows, width)

	var b strings.Builder
	writeHeader(&b, active, widths, opt.Color)

	if opt.Group {
		for i, g := range GroupByProject(rows) {
			if i > 0 {
				b.WriteByte('\n')
			}
			writeGroupHeading(&b, g, opt.Color)
			writeRows(&b, active, widths, g.Rows, opt.Color)
		}
	} else {
		writeRows(&b, active, widths, rows, opt.Color)
	}

	if opt.Footer {
		writeSummary(&b, rows, opt.Color)
	}

	if _, err := io.WriteString(w, b.String()); err != nil {
		return err
	}

	if len(dropped) > 0 && opt.Warn != nil {
		if _, err := io.WriteString(opt.Warn, droppedWarning(dropped, opt.Color)); err != nil {
			return err
		}
	}
	return nil
}

// Group is one project and the containers that belong to it. The TUI renders
// the same grouping, so this type is shared rather than duplicated there.
type Group struct {
	Name string
	Rows []model.Container
}

// GroupByProject buckets rows by compose project, ordered by name, with
// containers that belong to no project last — they are the exception, so they
// do not lead.
func GroupByProject(rows []model.Container) []Group {
	buckets := map[string][]model.Container{}
	for _, r := range rows {
		key := r.Project
		if key == "" {
			key = NoProject
		}
		buckets[key] = append(buckets[key], r)
	}

	names := make([]string, 0, len(buckets))
	for name := range buckets {
		if name != NoProject {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if _, ok := buckets[NoProject]; ok {
		names = append(names, NoProject)
	}

	out := make([]Group, 0, len(names))
	for _, name := range names {
		out = append(out, Group{Name: name, Rows: buckets[name]})
	}
	return out
}

func writeGroupHeading(b *strings.Builder, g Group, color bool) {
	var up int
	for _, r := range g.Rows {
		if r.State == "running" {
			up++
		}
	}
	// The project name leads at column zero so it reads as the parent of the
	// rows indented beneath it. The count trails, dimmed, because it is
	// context rather than the thing being named.
	name, count := g.Name, fmt.Sprintf("  %d/%d up", up, len(g.Rows))
	if color {
		name = ansiBold + name + ansiReset
		count = ansiDim + count + ansiReset
	}
	b.WriteString(name)
	b.WriteString(count)
	b.WriteByte('\n')
}

func writeHeader(b *strings.Builder, active []Column, widths []int, color bool) {
	if color {
		b.WriteString(ansiDim)
	}
	for i, c := range active {
		cell := Truncate(c.Header, widths[i], TruncTail)
		if i < len(active)-1 {
			cell = Pad(cell, widths[i])
		}
		b.WriteString(cell)
		if i < len(active)-1 {
			b.WriteString(strings.Repeat(" ", Gap))
		}
	}
	if color {
		b.WriteString(ansiReset)
	}
	b.WriteByte('\n')
}

func writeRows(b *strings.Builder, active []Column, widths []int, rows []model.Container, color bool) {
	for _, r := range rows {
		for i, c := range active {
			v := c.Value(r)
			if v == "" {
				v = Empty
			}
			cell := Truncate(v, widths[i], c.Trunc)
			if i < len(active)-1 {
				cell = Pad(cell, widths[i])
			}
			if color {
				if code := colorFor(c, r, v); code != "" {
					cell = code + cell + ansiReset
				}
			}
			b.WriteString(cell)
			if i < len(active)-1 {
				b.WriteString(strings.Repeat(" ", Gap))
			}
		}
		b.WriteByte('\n')
	}
}

func colorFor(c Column, r model.Container, value string) string {
	switch c.Key {
	case "name":
		return ansiBold
	case "state":
		switch r.State {
		case "running":
			return ansiGreen
		case "restarting", "paused", "created", "removing":
			return ansiYellow
		default:
			return ansiRed
		}
	case "ports":
		if value == Empty {
			return ansiDim
		}
		return ansiYellow
	case "health":
		switch value {
		case "healthy":
			return ansiGreen
		case "unhealthy":
			return ansiRed
		case "starting":
			return ansiYellow
		default:
			return ansiDim
		}
	case "id", "created", "cmd", "size":
		return ansiDim
	}
	return ""
}

// droppedWarning names every column the width budget removed.
func droppedWarning(dropped []Column, color bool) string {
	keys := make([]string, 0, len(dropped))
	for _, c := range dropped {
		keys = append(keys, c.Key)
	}
	noun := "column"
	if len(keys) > 1 {
		noun = "columns"
	}
	line := fmt.Sprintf("dps: %d %s hidden (%s) — widen the window or use --wide\n",
		len(keys), noun, strings.Join(keys, ", "))
	if color {
		line = ansiYellow + line + ansiReset
	}
	return line
}

// writeSummary counts the set. This is decoration, so it is printed only when
// stdout is a terminal.
func writeSummary(b *strings.Builder, rows []model.Container, color bool) {
	var up, warn, down int
	project := ""
	mixed := false
	for _, r := range rows {
		switch r.State {
		case "running":
			up++
		case "restarting", "paused", "created", "removing":
			warn++
		default:
			down++
		}
		if r.Project != "" {
			if project == "" {
				project = r.Project
			} else if project != r.Project {
				mixed = true
			}
		}
	}

	parts := []string{fmt.Sprintf("%d containers", len(rows))}
	if project != "" && !mixed {
		parts = append(parts, "project "+project)
	}
	var states []string
	if up > 0 {
		states = append(states, fmt.Sprintf("%d up", up))
	}
	if warn > 0 {
		states = append(states, fmt.Sprintf("%d restarting", warn))
	}
	if down > 0 {
		states = append(states, fmt.Sprintf("%d exited", down))
	}
	if len(states) > 0 {
		parts = append(parts, strings.Join(states, ", "))
	}

	line := strings.Join(parts, " · ")
	if color {
		line = ansiDim + line + ansiReset
	}
	b.WriteString(line)
	b.WriteByte('\n')
}
