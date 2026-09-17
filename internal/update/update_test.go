package update

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v1.0.1", "v1.0.0", true},
		{"v1.1.0", "v1.0.9", true},
		{"v2.0.0", "v1.9.9", true},
		{"v1.10.0", "v1.9.0", true},
		{"v1.0.0", "v1.0.0", false},
		{"v1.0.0", "v1.0.1", false},
		{"v0.9.0", "v1.0.0", false},
		{"v1.0.0", "dev", false},
		{"", "v1.0.0", false},
		{"1.2.0", "v1.0.0", false},
		{"v1.2", "v1.0.0", false},
		{"v1.0.1", "v1.0.0-3-gabc123", true},
		{"v1.0.0", "v1.0.0-3-gabc123", false},
	}
	for _, c := range cases {
		if got := Newer(c.latest, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

// serve points the check at a local server for one test and counts the
// requests it gets.
func serve(t *testing.T, status int, tag string) *int {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("User-Agent") == "" {
			t.Error("request carries no User-Agent; GitHub refuses those")
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"tag_name":"`+tag+`","name":"release"}`)
	}))
	t.Cleanup(srv.Close)

	old := latestURL
	latestURL = srv.URL
	t.Cleanup(func() { latestURL = old })
	return &hits
}

func writeState(t *testing.T, path string, s state) {
	t.Helper()
	b, _ := json.Marshal(s)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readState(t *testing.T, path string) state {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s state
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFreshCacheSkipsTheRequest(t *testing.T) {
	hits := serve(t, http.StatusOK, "v9.9.9")
	path := filepath.Join(t.TempDir(), "update.json")
	now := time.Now()
	writeState(t, path, state{Checked: now.Add(-time.Hour), Latest: "v1.2.0"})

	if got := latest(context.Background(), path, now); got != "v1.2.0" {
		t.Errorf("latest = %q, want the cached v1.2.0", got)
	}
	if *hits != 0 {
		t.Errorf("GitHub asked %d times with a fresh cache", *hits)
	}
}

func TestStaleCacheAsksAgain(t *testing.T) {
	hits := serve(t, http.StatusOK, "v1.3.0")
	path := filepath.Join(t.TempDir(), "sub", "update.json")
	now := time.Now()

	if got := latest(context.Background(), path, now); got != "v1.3.0" {
		t.Errorf("latest with no cache = %q, want v1.3.0", got)
	}
	writeState(t, path, state{Checked: now.Add(-25 * time.Hour), Latest: "v1.2.0"})
	if got := latest(context.Background(), path, now); got != "v1.3.0" {
		t.Errorf("latest with a stale cache = %q, want v1.3.0", got)
	}
	if *hits != 2 {
		t.Errorf("GitHub asked %d times, want 2", *hits)
	}
	if s := readState(t, path); s.Latest != "v1.3.0" || !s.Checked.Equal(now) {
		t.Errorf("cache = %+v, want v1.3.0 checked now", s)
	}
}

func TestFailedRequestKeepsTheLastAnswer(t *testing.T) {
	hits := serve(t, http.StatusForbidden, "")
	path := filepath.Join(t.TempDir(), "update.json")
	now := time.Now()
	writeState(t, path, state{Checked: now.Add(-48 * time.Hour), Latest: "v1.2.0"})

	if got := latest(context.Background(), path, now); got != "v1.2.0" {
		t.Errorf("latest = %q, want the cached v1.2.0", got)
	}
	// The stamp is what stops an offline machine from waiting on every run.
	if s := readState(t, path); !s.Checked.Equal(now) {
		t.Errorf("failed check not stamped: %+v", s)
	}
	latest(context.Background(), path, now.Add(time.Minute))
	if *hits != 1 {
		t.Errorf("GitHub asked %d times after a failure, want 1", *hits)
	}
}

func TestCheckFromTheFutureIsNotTrusted(t *testing.T) {
	hits := serve(t, http.StatusOK, "v1.3.0")
	path := filepath.Join(t.TempDir(), "update.json")
	now := time.Now()
	writeState(t, path, state{Checked: now.Add(72 * time.Hour), Latest: "v1.2.0"})

	if got := latest(context.Background(), path, now); got != "v1.3.0" || *hits != 1 {
		t.Errorf("latest = %q after %d requests, want v1.3.0 after 1", got, *hits)
	}
}

func TestAvailableSkipsDevBuildsAndOptOut(t *testing.T) {
	hits := serve(t, http.StatusOK, "v9.9.9")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	if got := Available(context.Background(), "dev"); got != "" {
		t.Errorf("dev build told about %q", got)
	}
	t.Setenv("DPS_NO_UPDATE_CHECK", "1")
	if got := Available(context.Background(), "v1.0.0"); got != "" {
		t.Errorf("DPS_NO_UPDATE_CHECK=1 still told about %q", got)
	}
	if *hits != 0 {
		t.Errorf("GitHub asked %d times, want 0", *hits)
	}
}

func TestPromptContinuesOnEnter(t *testing.T) {
	var out strings.Builder
	if !Prompt(context.Background(), strings.NewReader("\n"), &out, "v1.0.0", "v1.1.0") {
		t.Fatal("enter did not continue")
	}
	for _, want := range []string{"Update available", "v1.0.0", "v1.1.0", InstallCommand, "enter"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("notice has no %q:\n%s", want, out.String())
		}
	}
}

func TestPromptLeavesTypeAheadUnread(t *testing.T) {
	in := strings.NewReader("\nq")
	if !Prompt(context.Background(), in, io.Discard, "v1.0.0", "v1.1.0") {
		t.Fatal("enter did not continue")
	}
	if rest, _ := io.ReadAll(in); string(rest) != "q" {
		t.Errorf("left %q for the view, want \"q\"", rest)
	}
}

func TestPromptStopsWhenCancelled(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if Prompt(ctx, r, io.Discard, "v1.0.0", "v1.1.0") {
		t.Error("a cancelled prompt continued")
	}
}
