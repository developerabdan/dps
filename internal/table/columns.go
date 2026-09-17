package table

import (
	"fmt"
	"strings"

	"github.com/developerabdan/dps/internal/model"
)

// Empty is what a column prints when a container has no value for it. A dash
// is unambiguous in a way that a blank cell is not.
const Empty = "—"

// Column describes one printable field. Prio decides who survives a narrow
// terminal: 1 is never dropped, higher numbers go first.
type Column struct {
	Key    string
	Header string
	Prio   int
	Min    int
	Max    int
	Trunc  TruncMode
	Value  func(c model.Container) string
}

// CPUWidth is the printed width of the cpu column: the graph, one space, and
// a percentage right-aligned in six cells.
const CPUWidth = CPUBars + 1 + 6

// CPUBars is how many samples the cpu column draws. At one sample every two
// seconds that is the last twenty seconds.
const CPUBars = 10

// LiveOnly lists the columns that only the interactive view can fill.
var LiveOnly = []string{"cpu"}

// DefaultKeys is the column set used when nothing is configured.
var DefaultKeys = []string{"name", "state", "image", "ports"}

// Catalog is every column dps can print, in the order --list-cols shows them.
var Catalog = []Column{
	{Key: "name", Header: "NAME", Prio: 1, Min: 8, Max: 22, Trunc: TruncMid,
		Value: func(c model.Container) string { return model.ShortName(c.Name, c.Project) }},
	{Key: "state", Header: "STATE", Prio: 2, Min: 10, Max: 16, Trunc: TruncTail,
		Value: func(c model.Container) string { return model.ShortState(c.State, c.Status) }},
	{Key: "image", Header: "IMAGE", Prio: 3, Min: 12, Max: 30, Trunc: TruncHead,
		Value: func(c model.Container) string { return model.ShortImage(c.Image) }},
	{Key: "ports", Header: "PORTS", Prio: 4, Min: 8, Max: 26, Trunc: TruncTail,
		Value: func(c model.Container) string { return model.CompactPorts(c.Ports) }},
	// cpu is a graph of the last samples and the latest percentage. A graph
	// needs history, and only the interactive view keeps any, so here the
	// value is empty and the view supplies its own. Min and Max are the same
	// because a graph that is cut short no longer reads as a graph: the column
	// is dropped whole instead.
	{Key: "cpu", Header: "CPU", Prio: 4, Min: CPUWidth, Max: CPUWidth, Trunc: TruncTail,
		Value: func(model.Container) string { return "" }},
	{Key: "health", Header: "HEALTH", Prio: 5, Min: 8, Max: 12, Trunc: TruncTail,
		Value: func(c model.Container) string { return c.Health }},
	{Key: "created", Header: "CREATED", Prio: 6, Min: 6, Max: 10, Trunc: TruncTail,
		Value: func(c model.Container) string { return model.CompactAge(c.Created) }},
	{Key: "project", Header: "PROJECT", Prio: 7, Min: 8, Max: 16, Trunc: TruncMid,
		Value: func(c model.Container) string { return c.Project }},
	{Key: "service", Header: "SERVICE", Prio: 7, Min: 8, Max: 16, Trunc: TruncMid,
		Value: func(c model.Container) string { return c.Service }},
	{Key: "ip", Header: "IP", Prio: 8, Min: 9, Max: 15, Trunc: TruncTail,
		Value: func(c model.Container) string { return c.IP }},
	{Key: "size", Header: "SIZE", Prio: 9, Min: 6, Max: 10, Trunc: TruncTail,
		Value: func(c model.Container) string { return model.HumanBytes(c.Size) }},
	{Key: "id", Header: "ID", Prio: 9, Min: 4, Max: 12, Trunc: TruncTail,
		Value: func(c model.Container) string {
			if len(c.ID) > 6 {
				return c.ID[:6]
			}
			return c.ID
		}},
	{Key: "cmd", Header: "COMMAND", Prio: 10, Min: 8, Max: 24, Trunc: TruncTail,
		Value: func(c model.Container) string { return c.Command }},
}

// ByKey looks a column up by its key.
func ByKey(key string) (Column, bool) {
	for _, c := range Catalog {
		if c.Key == key {
			return c, true
		}
	}
	return Column{}, false
}

// Keys lists every catalog key, for error messages and --list-cols.
func Keys() []string {
	out := make([]string, 0, len(Catalog))
	for _, c := range Catalog {
		out = append(out, c.Key)
	}
	return out
}

// Resolve turns a comma-separated key list into columns, preserving the order
// given. An unknown key is an error naming the valid set rather than a
// silently missing column.
func Resolve(keys []string) ([]Column, error) {
	out := make([]Column, 0, len(keys))
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		col, ok := ByKey(k)
		if !ok {
			return nil, fmt.Errorf("unknown column %q; available: %s", k, strings.Join(Keys(), ", "))
		}
		out = append(out, col)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no columns selected; available: %s", strings.Join(Keys(), ", "))
	}
	return out, nil
}
