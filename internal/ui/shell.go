package ui

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync/atomic"
	"time"

	xterm "github.com/charmbracelet/x/term"
	"github.com/muesli/cancelreader"

	"github.com/developerabdan/dps/internal/dockerapi"
	"github.com/developerabdan/dps/internal/term"
)

// shell opens an interactive shell inside a container. It is a tea.ExecCommand:
// Bubble Tea gives up the terminal for as long as Run blocks and takes it back
// afterwards, so the table is gone while the shell is open and comes back, as
// it was, when the shell exits.
//
// There is no shell picker. dockerapi.ShellCmd takes bash when the image has
// it and sh otherwise, which is the right answer nearly every time, and a
// question asked on every exec to cover the rest is a cost paid on every exec.
type shell struct {
	ctx    context.Context
	client *dockerapi.Client
	id     string
	name   string

	stdin  io.Reader
	stdout io.Writer

	// Read by the callback once Run has returned.
	code  int
	typed atomic.Bool
}

func (s *shell) SetStdin(r io.Reader)  { s.stdin = r }
func (s *shell) SetStdout(w io.Writer) { s.stdout = w }

// SetStderr is unused: an exec with a TTY has one output stream, and the
// daemon writes both of the process's streams into it.
func (s *shell) SetStderr(io.Writer) {}

func (s *shell) Run() error {
	s.code = -1
	execID, err := s.client.CreateExec(s.ctx, s.id, dockerapi.ShellCmd)
	if err != nil {
		return err
	}

	outFd := fdOf(s.stdout, os.Stdout)
	inFd := fdOf(s.stdin, os.Stdin)
	width, height, _ := xterm.GetSize(outFd)

	stream, err := s.client.StartExec(s.ctx, execID, width, height)
	if err != nil {
		return err
	}
	defer stream.Close()
	stop := context.AfterFunc(s.ctx, func() { stream.Close() })
	defer stop()

	// Keys go to the shell exactly as typed — ctrl+c, arrows, tab — so the
	// terminal is put in raw mode, and the TTY inside the container does the
	// line editing instead.
	if xterm.IsTerminal(inFd) {
		if state, err := xterm.MakeRaw(inFd); err == nil {
			defer xterm.Restore(inFd, state)
		}
	}

	// Said once, before the prompt, because the table vanishing with no word
	// about how to get it back reads as a crash.
	fmt.Fprintf(s.stdout, "\r\n%sdps: shell in %s · exit or ctrl+d returns to the list%s\r\n\r\n",
		ansiDim, s.name, ansiReset)

	// Size is sent once now, for a daemon too old to take it at start, and
	// again on every window change while the shell is open.
	_ = s.client.ResizeExec(s.ctx, execID, width, height)
	resized := make(chan os.Signal, 1)
	term.NotifyResize(resized)
	defer signal.Stop(resized)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-done:
				return
			case <-resized:
				if w, h, err := xterm.GetSize(outFd); err == nil {
					_ = s.client.ResizeExec(s.ctx, execID, w, h)
				}
			}
		}
	}()

	// The reader has to be cancellable. A plain read of stdin would still be
	// blocked when the shell exits, and it would swallow the first key meant
	// for the list.
	in, err := cancelreader.NewReader(s.stdin)
	if err != nil {
		return err
	}
	defer in.Close()
	inDone := make(chan struct{})
	go func() {
		defer close(inDone)
		buf := make([]byte, 1024)
		for {
			n, err := in.Read(buf)
			if n > 0 {
				s.typed.Store(true)
				if _, werr := stream.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// The daemon closes the connection when the shell exits, which ends this
	// copy.
	_, _ = io.Copy(s.stdout, stream)
	in.Cancel()
	select {
	case <-inDone:
	case <-time.After(500 * time.Millisecond):
	}

	if code, err := s.client.ExecExitCode(s.ctx, execID); err == nil {
		s.code = code
	}
	return nil
}

// fdOf returns the descriptor behind v, or the fallback's when v is not a
// file.
func fdOf(v any, fallback *os.File) uintptr {
	if f, ok := v.(interface{ Fd() uintptr }); ok {
		return f.Fd()
	}
	return fallback.Fd()
}

// noShell reports whether a shell session ended because the container has no
// shell to run. 126 and 127 are what the runtime returns when /bin/sh cannot
// be found or started. A shell the user typed into can exit with the same
// codes — the last command was not found, then ctrl+d — so a session that
// received keys never counts.
func noShell(code int, typed bool) bool {
	return !typed && (code == 126 || code == 127)
}
