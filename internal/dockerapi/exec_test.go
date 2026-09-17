package dockerapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeDaemon serves handler on a unix socket and returns a client for it. The
// socket lives under a short temp path because macOS caps socket paths at 104
// bytes, and t.TempDir is longer than that there.
func fakeDaemon(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "dps")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	return &Client{
		sock: sock,
		api:  PreferredAPI,
		hc: &http.Client{Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", sock)
			},
		}},
	}
}

func TestCreateExecSendsTTYAndCommand(t *testing.T) {
	var got struct {
		AttachStdin bool
		Tty         bool
		Cmd         []string
	}
	c := fakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v"+PreferredAPI+"/containers/abc/exec" {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"Id":"exec1"}`))
	}))

	id, err := c.CreateExec(context.Background(), "abc", ShellCmd)
	if err != nil {
		t.Fatal(err)
	}
	if id != "exec1" {
		t.Errorf("id %q, want exec1", id)
	}
	if !got.Tty || !got.AttachStdin {
		t.Errorf("exec created without a TTY or stdin: %+v", got)
	}
	if strings.Join(got.Cmd, " ") != strings.Join(ShellCmd, " ") {
		t.Errorf("cmd %q", got.Cmd)
	}
}

func TestCreateExecReportsDaemonReason(t *testing.T) {
	c := fakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"container abc is not running"}`, http.StatusConflict)
	}))
	_, err := c.CreateExec(context.Background(), "abc", ShellCmd)
	if err == nil || !strings.Contains(err.Error(), "is not running") {
		t.Fatalf("err %v, want the daemon's reason", err)
	}
}

// TestStartExecStreamsBothWays upgrades the connection the way the daemon
// does, then echoes stdin back in upper case. Anything the daemon writes in
// the same packet as the headers must still reach the reader.
func TestStartExecStreamsBothWays(t *testing.T) {
	var size []int
	c := fakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "tcp" {
			http.Error(w, "no upgrade asked", http.StatusBadRequest)
			return
		}
		var body struct{ ConsoleSize []int }
		json.NewDecoder(r.Body).Decode(&body)
		size = body.ConsoleSize

		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		buf.WriteString("HTTP/1.1 101 UPGRADED\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n$ ")
		buf.Flush()
		line, err := buf.ReadString('\n')
		if err != nil {
			return
		}
		conn.Write([]byte(strings.ToUpper(line)))
	}))

	s, err := c.StartExec(context.Background(), "exec1", 120, 40)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if len(size) != 2 || size[0] != 40 || size[1] != 120 {
		t.Errorf("console size %v, want [40 120]", size)
	}

	if _, err := s.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	s.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	out, err := io.ReadAll(bufio.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, []byte("$ HELLO\n")) {
		t.Errorf("stream read %q, want %q", out, "$ HELLO\n")
	}
}

func TestStartExecRefusedIsAnError(t *testing.T) {
	c := fakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"No such exec instance: nope"}`, http.StatusNotFound)
	}))
	_, err := c.StartExec(context.Background(), "nope", 80, 24)
	if err == nil || !strings.Contains(err.Error(), "No such exec instance") {
		t.Fatalf("err %v, want the daemon's reason", err)
	}
}

// TestExecExitCodeWaitsForExit covers the gap between the stream closing and
// the daemon recording the exit: the first inspect still says running.
func TestExecExitCodeWaitsForExit(t *testing.T) {
	calls := 0
	c := fakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Write([]byte(`{"Running":true,"ExitCode":null}`))
			return
		}
		w.Write([]byte(`{"Running":false,"ExitCode":127}`))
	}))
	code, err := c.ExecExitCode(context.Background(), "exec1")
	if err != nil {
		t.Fatal(err)
	}
	if code != 127 {
		t.Errorf("code %d, want 127", code)
	}
}
