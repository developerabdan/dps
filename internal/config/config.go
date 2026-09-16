// Package config persists the column choice. The file is plain JSON and
// hand-editable on purpose: --set-cols and --save-preset only rewrite what a
// person could have typed themselves.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config is the on-disk shape. A missing file is not an error, so every field
// has a usable zero value.
type Config struct {
	Cols []string `json:"cols"`
	Sort string   `json:"sort,omitempty"`
	// No omitempty. Grouping defaults to on, so false is a real answer and
	// dropping it from the file would read back as the default — anyone who
	// chose the flat view, in the wizard or by hand, would silently get groups
	// again on the next run.
	GroupByProject bool                `json:"group_by_project"`
	Presets        map[string][]string `json:"presets,omitempty"`
}

// DefaultCols is the column set used when nothing has been configured.
var DefaultCols = []string{"name", "state", "image", "ports"}

// Default returns the configuration used before anyone has saved one.
func Default() Config {
	return Config{
		Cols: append([]string(nil), DefaultCols...),
		Sort: "name",
		// Grouping is on by default: a project heading tells you which stack
		// a container belongs to, which is the first thing you want to know
		// on a host that runs several. Set this to false to get a flat table.
		GroupByProject: true,
		Presets: map[string][]string{
			"ports": {"name", "ports", "ip", "state"},
			// No "restarts" here: the count needs a per-container inspect and
			// has no column yet, so shipping it made `--preset debug` fail on
			// a machine that had never configured anything.
			"debug": {"name", "state", "health", "created"},
			"disk":  {"name", "image", "size"},
		},
	}
}

// Path reports where the config lives, honouring XDG_CONFIG_HOME.
func Path() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "dps", "config.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "dps", "config.json"), nil
}

// Exists reports whether a config file has been written. A missing file is
// what makes a run the first one, so this is the whole test the wizard hangs
// off — dps never writes a file it was not asked to write, which keeps that
// test honest.
func Exists() bool {
	path, err := Path()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// Load reads the config. A missing file yields the defaults and no error:
// dps must work on a machine it has never been configured on.
func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Default(), err
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Default(), nil
	}
	if err != nil {
		return Default(), err
	}

	cfg := Default()
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Default(), fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	if len(cfg.Cols) == 0 {
		cfg.Cols = append([]string(nil), DefaultCols...)
	}
	return cfg, nil
}

// Save writes the config, creating the directory if needed. The write goes to
// a temporary file first and is then renamed, so an interrupted save cannot
// leave a half-written config behind.
func (c Config) Save() error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Resolve turns a --cols value into a final column list.
//
// A plain list replaces the saved set. A list where any entry carries + or -
// adjusts the saved set instead, so `--cols +health` means "what I normally
// have, plus health" without retyping it.
func Resolve(base []string, spec string) ([]string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return append([]string(nil), base...), nil
	}

	tokens := strings.Split(spec, ",")
	delta := false
	for _, t := range tokens {
		t = strings.TrimSpace(t)
		if strings.HasPrefix(t, "+") || strings.HasPrefix(t, "-") {
			delta = true
			break
		}
	}

	if !delta {
		out := make([]string, 0, len(tokens))
		for _, t := range tokens {
			if t = strings.TrimSpace(t); t != "" {
				out = append(out, t)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("no columns given")
		}
		return out, nil
	}

	out := append([]string(nil), base...)
	for _, t := range tokens {
		t = strings.TrimSpace(t)
		switch {
		case t == "":
		case strings.HasPrefix(t, "+"):
			key := strings.TrimPrefix(t, "+")
			if key == "" {
				return nil, fmt.Errorf("`+` needs a column name")
			}
			if !contains(out, key) {
				out = append(out, key)
			}
		case strings.HasPrefix(t, "-"):
			key := strings.TrimPrefix(t, "-")
			if key == "" {
				return nil, fmt.Errorf("`-` needs a column name")
			}
			out = remove(out, key)
		default:
			return nil, fmt.Errorf("mixed plain and +/- entries in %q; use one or the other", spec)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("that would remove every column")
	}
	return out, nil
}

func contains(list []string, key string) bool {
	for _, v := range list {
		if v == key {
			return true
		}
	}
	return false
}

func remove(list []string, key string) []string {
	out := list[:0]
	for _, v := range list {
		if v != key {
			out = append(out, v)
		}
	}
	return append([]string(nil), out...)
}
