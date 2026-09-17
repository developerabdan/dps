// Package update tells a person that a newer dps has been released. It only
// tells. The upgrade is the install script, run by hand, so dps never replaces
// its own binary and never downloads anything but one small JSON answer.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Repo is the GitHub repository the releases are published in.
const Repo = "developerabdan/dps"

// InstallCommand upgrades dps. It is the install line again: the script
// overwrites the binary in place.
const InstallCommand = "curl -fsSL https://raw.githubusercontent.com/" + Repo + "/main/install.sh | sh"

// checkEvery is how long one answer from GitHub is trusted. Asking on every
// run would put a network round trip in front of every table, and the
// unauthenticated API allows 60 requests an hour per address.
const checkEvery = 24 * time.Hour

// timeout bounds the request. An offline machine or a slow proxy costs this
// much once a day, and then the answer is cached like any other.
const timeout = 2 * time.Second

// latestURL is a variable so tests can point it at a local server.
var latestURL = "https://api.github.com/repos/" + Repo + "/releases/latest"

// state is the cache file: when GitHub was last asked, and the newest tag it
// has ever answered with.
type state struct {
	Checked time.Time `json:"checked"`
	Latest  string    `json:"latest,omitempty"`
}

// Available returns the tag of a release newer than current, or "" when there
// is none. A development build, DPS_NO_UPDATE_CHECK=1 and any failure along the
// way all return "": a version check is never a reason for dps not to start.
func Available(ctx context.Context, current string) string {
	if os.Getenv("DPS_NO_UPDATE_CHECK") == "1" {
		return ""
	}
	if _, ok := parse(current); !ok {
		return ""
	}
	if latest := latest(ctx, cachePath(), time.Now()); Newer(latest, current) {
		return latest
	}
	return ""
}

// cachePath is where the last answer is kept. It is a cache, not config: the
// file can be deleted at any time and costs one request to rebuild.
func cachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "dps", "update.json")
}

// latest returns the newest release tag, from the cache while it is fresh and
// from GitHub otherwise. A failed request still stamps the cache, so a machine
// with no route to GitHub pays the timeout once a day and not on every run.
func latest(ctx context.Context, path string, now time.Time) string {
	var s state
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &s)
		}
	}
	// A check stamped in the future means the clock moved back. Trusting it
	// would silence the check until the clock caught up again.
	if age := now.Sub(s.Checked); age >= 0 && age < checkEvery {
		return s.Latest
	}

	if tag, err := fetch(ctx); err == nil {
		s.Latest = tag
	}
	s.Checked = now
	if path != "" {
		save(path, s)
	}
	return s.Latest
}

func fetch(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestURL, nil)
	if err != nil {
		return "", err
	}
	// GitHub refuses API requests that carry no User-Agent.
	req.Header.Set("User-Agent", "dps")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github answered %s", resp.Status)
	}

	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", err
	}
	if _, ok := parse(body.TagName); !ok {
		return "", fmt.Errorf("github answered an unreadable tag %q", body.TagName)
	}
	return body.TagName, nil
}

// save writes the cache through a temporary file and a rename, the same way
// the config is saved. A failed write is ignored: the next run asks again.
func save(path string, s state) {
	b, err := json.Marshal(s)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// Newer reports whether tag latest is a later release than tag current. A tag
// that is not vMAJOR.MINOR.PATCH is never newer, so a bad answer cannot start
// a notice.
func Newer(latest, current string) bool {
	l, ok := parse(latest)
	if !ok {
		return false
	}
	c, ok := parse(current)
	if !ok {
		return false
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// parse reads v1.2.3 into its three numbers. A pre-release or build suffix is
// ignored: releases/latest never returns a pre-release, and the release
// workflow stamps plain tags.
func parse(tag string) ([3]int, bool) {
	var out [3]int
	core, ok := strings.CutPrefix(tag, "v")
	if !ok {
		return out, false
	}
	if i := strings.IndexAny(core, "-+"); i >= 0 {
		core = core[:i]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

const (
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
	ansiDim   = "\x1b[2m"
	ansiCyan  = "\x1b[36m"
)

// Prompt shows the notice and waits for enter. It returns false when ctx ends
// first — ctrl-c while the notice is up — so the caller can stop rather than
// open the view the person just walked away from.
//
// The read runs in its own goroutine because a read on a terminal cannot be
// cancelled. When ctx wins, that goroutine is left blocked, and the process
// exits under it.
func Prompt(ctx context.Context, in io.Reader, out io.Writer, current, latest string) bool {
	fmt.Fprintf(out, "\n  %sUpdate available:%s %s → %s%s%s\n\n", ansiBold, ansiReset, current, ansiBold, latest, ansiReset)
	fmt.Fprintf(out, "  Run this to update:\n")
	fmt.Fprintf(out, "    %s%s%s\n\n", ansiCyan, InstallCommand, ansiReset)
	fmt.Fprintf(out, "  %s[ enter ] continue%s ", ansiDim, ansiReset)

	done := make(chan struct{})
	go func() {
		defer close(done)
		// One byte at a time, so nothing typed after the newline is taken
		// from the view that reads stdin next.
		b := make([]byte, 1)
		for {
			if n, err := in.Read(b); err != nil || (n == 1 && b[0] == '\n') {
				return
			}
		}
	}()

	select {
	case <-done:
		return true
	case <-ctx.Done():
		fmt.Fprintln(out)
		return false
	}
}
