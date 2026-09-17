// Command dps prints a readable container table.
//
// Output mode follows stdout: a terminal gets colour and a summary footer, a
// pipe gets a plain greppable table. Nothing about that is flag-driven, so
// `dps | grep exited` behaves the way every other unix tool does.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/developerabdan/dps/internal/config"
	"github.com/developerabdan/dps/internal/dockerapi"
	"github.com/developerabdan/dps/internal/model"
	"github.com/developerabdan/dps/internal/table"
	"github.com/developerabdan/dps/internal/term"
	"github.com/developerabdan/dps/internal/ui"
	"github.com/developerabdan/dps/internal/update"
)

// version is stamped at build time with -ldflags "-X main.version=...".
var version = "dev"

// filterFlag collects repeated -f key=value pairs.
type filterFlag map[string][]string

func (f filterFlag) String() string { return "" }

func (f filterFlag) Set(v string) error {
	key, value, ok := strings.Cut(v, "=")
	if !ok {
		return fmt.Errorf("filter must be key=value, got %q", v)
	}
	f[key] = append(f[key], value)
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "dps: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	var (
		all      = flag.Bool("a", false, "include stopped containers")
		plain    = flag.Bool("plain", false, "force the plain table even on a terminal")
		asJSON   = flag.Bool("json", false, "emit newline-delimited JSON records")
		wide     = flag.Bool("wide", false, "no truncation, no dropped columns")
		colsFlag = flag.String("cols", "", "columns for this run; +x,-y adjusts the saved set")
		setCols  = flag.String("set-cols", "", "save these columns as the default and exit")
		preset   = flag.String("preset", "", "use a saved preset as the column set")
		savePres = flag.String("save-preset", "", "save the resulting columns under this name and exit")
		showConf = flag.Bool("config", false, "print the config path and contents, then exit")
		onboard  = flag.Bool("onboard", false, "run the first-run setup again, then exit")
		pickCols = flag.Bool("pick-cols", false, "tick the default columns from a list, then exit")
		listCols = flag.Bool("list-cols", false, "print the column catalog and exit")
		watch    = flag.Int("w", 0, "redraw every N seconds")
		showVer  = flag.Bool("version", false, "print version and exit")
		filters  = filterFlag{}
	)
	flag.Var(filters, "f", "filter passed to docker, key=value, repeatable")

	// -g and --group are the same switch under two spellings, so they share
	// one variable rather than becoming two flags that can disagree.
	var groupFlag bool
	flag.BoolVar(&groupFlag, "g", false, "group rows by compose project")
	flag.BoolVar(&groupFlag, "group", false, "group rows by compose project")

	flag.Parse()

	if *showVer {
		fmt.Printf("dps %s\n", version)
		return nil
	}
	if *listCols {
		return printCatalog()
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *showConf {
		return printConfig(cfg)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	isTTY := term.IsTTY(os.Stdout)

	if *pickCols {
		if !isTTY || !term.IsTTY(os.Stdin) {
			return fmt.Errorf("--pick-cols needs a terminal on stdin and stdout; use --set-cols in a script")
		}
		keys, err := ui.PickColumns(ctx, cfg)
		if err != nil {
			return err
		}
		if keys == nil {
			fmt.Fprintln(os.Stderr, "dps: nothing saved")
			return nil
		}
		path, _ := config.Path()
		fmt.Printf("saved %s to %s\n", strings.Join(keys, ","), path)
		return nil
	}

	// The wizard writes the config file, so it runs only when nothing else on
	// this command line is already doing that job, and only on a machine that
	// has never saved one. --onboard asks for it again by hand.
	unconfigured := !config.Exists() &&
		*setCols == "" && *savePres == "" && *colsFlag == "" && *preset == "" &&
		!*asJSON && !*plain && *watch <= 0
	if *onboard || unconfigured {
		// It reads keys and draws frames, so it needs a terminal at both ends.
		// Without one a first run carries straight on with the defaults —
		// `dps` inside a script or a pipe must never stop to ask a question —
		// while --onboard, which was typed on purpose, says why it cannot.
		switch {
		case isTTY && term.IsTTY(os.Stdin):
			done, err := ui.Onboard(ctx, cfg)
			if err != nil {
				return err
			}
			if !done {
				fmt.Fprintln(os.Stderr, "dps: setup cancelled — run `dps --onboard` to do it later")
			}
			return nil
		case *onboard:
			return fmt.Errorf("--onboard needs a terminal on stdin and stdout")
		}
	}

	// The config sets the default. A -g or --group actually typed on the
	// command line overrides it in both directions, which is why this checks
	// whether the flag was passed rather than only whether it is true —
	// otherwise `-g=false` could never turn off a config that enabled it.
	groupRows := cfg.GroupByProject
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "g" || f.Name == "group" {
			groupRows = groupFlag
		}
	})

	// The saved set is the starting point. --preset swaps that starting point
	// for a named one; --cols and --set-cols then either replace it outright
	// or adjust it with +/- entries.
	base := cfg.Cols
	if *preset != "" {
		p, ok := cfg.Presets[*preset]
		if !ok {
			return fmt.Errorf("no preset named %q; saved presets: %s", *preset, presetNames(cfg))
		}
		base = p
	}

	spec := *colsFlag
	if *setCols != "" {
		spec = *setCols
	}
	keys, err := config.Resolve(base, spec)
	if err != nil {
		return err
	}

	// Validate against the catalog before writing anything, so a typo cannot
	// save a config that later refuses to load.
	cols, err := table.Resolve(keys)
	if err != nil {
		return err
	}

	if *setCols != "" || *savePres != "" {
		if *setCols != "" {
			cfg.Cols = keys
		}
		if *savePres != "" {
			if cfg.Presets == nil {
				cfg.Presets = map[string][]string{}
			}
			cfg.Presets[*savePres] = keys
		}
		if err := cfg.Save(); err != nil {
			return err
		}
		path, _ := config.Path()
		fmt.Printf("saved %s to %s\n", strings.Join(keys, ","), path)
		return nil
	}

	// A terminal gets the interactive view. --plain and --json are explicit
	// one-shot requests, and -w already owns its own redraw loop, so each of
	// those keeps the plain path.
	interactive := isTTY && !*plain && !*asJSON && *watch <= 0

	// The cpu graph needs a history of samples, and only the interactive view
	// keeps one. The plain table leaves the column out. It says so when the
	// column was asked for on this command line, but not when it comes from
	// the saved default — that would print the same warning on every
	// `dps | grep`.
	if !interactive && !*asJSON {
		asked := *colsFlag != "" || *preset != ""
		if cols, err = dropLiveOnly(cols, asked); err != nil {
			return err
		}
	}

	client, err := dockerapi.New(ctx)
	if err != nil {
		return err
	}

	// A newer release is announced only to a person at a terminal, and only
	// before a view they are about to watch. The notice waits for enter, so a
	// pipe, --plain or --json must never reach it.
	if isTTY && term.IsTTY(os.Stdin) && !*plain && !*asJSON {
		if latest := update.Available(ctx, version); latest != "" {
			if !update.Prompt(ctx, os.Stdin, os.Stdout, version, latest) {
				return nil
			}
		}
	}

	opt := dockerapi.ListOptions{All: *all, Filters: filters}
	for _, c := range cols {
		if c.Key == "size" {
			opt.Size = true
		}
	}

	// The view draws groups itself, so -g does not have to divert away from it.
	if interactive {
		return ui.Run(ctx, client, cols, opt, groupRows)
	}

	draw := func() error {
		containers, err := client.ListContainers(ctx, opt)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(containers)
		}
		return table.Render(os.Stdout, cols, containers, table.RenderOptions{
			Width:  term.Width(os.Stdout),
			Color:  isTTY,
			Wide:   *wide,
			Footer: isTTY,
			// Group headings are structure for a reader, and a script cannot
			// tell one from a container row. Piped output degrades to the
			// flat table so `dps -g | awk` is never quietly wrong.
			Group: groupRows && isTTY,
			Warn:  os.Stderr,
		})
	}

	if *watch <= 0 || !isTTY {
		return draw()
	}

	ticker := time.NewTicker(time.Duration(*watch) * time.Second)
	defer ticker.Stop()
	for {
		fmt.Print("\x1b[H\x1b[2J")
		if err := draw(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// dropLiveOnly removes the columns only the interactive view can fill.
func dropLiveOnly(cols []table.Column, warn bool) ([]table.Column, error) {
	out := make([]table.Column, 0, len(cols))
	var dropped []string
	for _, c := range cols {
		if slices.Contains(table.LiveOnly, c.Key) {
			dropped = append(dropped, c.Key)
			continue
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s needs the interactive view; add another column, or run dps on a terminal",
			strings.Join(dropped, ", "))
	}
	if warn && len(dropped) > 0 {
		fmt.Fprintf(os.Stderr, "dps: %s left out — it needs the interactive view\n", strings.Join(dropped, ", "))
	}
	return out, nil
}

func writeJSON(containers []model.Container) error {
	enc := json.NewEncoder(os.Stdout)
	for _, c := range containers {
		if err := enc.Encode(c); err != nil {
			return err
		}
	}
	return nil
}

func printCatalog() error {
	for _, c := range table.Catalog {
		fmt.Printf("%-9s %-8s prio %-2d  min %-2d  max %d\n",
			c.Key, c.Header, c.Prio, c.Min, c.Max)
	}
	return nil
}

// printConfig shows where the config lives and what is in it. A machine that
// has never saved one still gets an answer: the path it would be written to,
// and the defaults currently in force.
func printConfig(cfg config.Config) error {
	path, err := config.Path()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Printf("%s (not written yet — showing defaults)\n\n", path)
	} else {
		fmt.Printf("%s\n\n", path)
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

// presetNames lists saved presets in a stable order for error messages.
func presetNames(cfg config.Config) string {
	if len(cfg.Presets) == 0 {
		return "none saved"
	}
	names := make([]string, 0, len(cfg.Presets))
	for n := range cfg.Presets {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
