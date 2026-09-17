package dockerapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ShellCmd opens bash when the image has it and sh when it does not. The
// choice is made inside the container by the one process that runs, so no
// round trip is spent asking which shells exist, and nobody is asked to pick
// one they cannot see. Images with no /bin/sh at all — distroless, scratch —
// cannot run this, and the exit code says so.
var ShellCmd = []string{"/bin/sh", "-c", "command -v bash >/dev/null 2>&1 && exec bash || exec sh"}

// ExecStream is the connection of a started exec, taken over from HTTP. The
// exec has a TTY, so reads are the raw terminal output — not the multiplexed
// frames a log stream carries — and writes go to the process's stdin.
type ExecStream struct {
	conn net.Conn
	r    *bufio.Reader
}

func (s *ExecStream) Read(p []byte) (int, error)  { return s.r.Read(p) }
func (s *ExecStream) Write(p []byte) (int, error) { return s.conn.Write(p) }
func (s *ExecStream) Close() error                { return s.conn.Close() }

// CreateExec prepares cmd inside a running container with a TTY and all three
// streams attached, and returns the exec ID. Nothing runs until StartExec.
func (c *Client) CreateExec(ctx context.Context, container string, cmd []string) (string, error) {
	body, err := json.Marshal(struct {
		AttachStdin  bool     `json:"AttachStdin"`
		AttachStdout bool     `json:"AttachStdout"`
		AttachStderr bool     `json:"AttachStderr"`
		Tty          bool     `json:"Tty"`
		Cmd          []string `json:"Cmd"`
	}{true, true, true, true, cmd})
	if err != nil {
		return "", err
	}
	rc, err := c.post(ctx, "/containers/"+url.PathEscape(container)+"/exec", nil, body)
	if err != nil {
		return "", err
	}
	defer rc.Close()

	var out struct {
		ID string `json:"Id"`
	}
	if err := json.NewDecoder(rc).Decode(&out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", errors.New("docker created an exec with no ID")
	}
	return out.ID, nil
}

// StartExec starts the exec and hands back its connection. It cannot go
// through the shared http.Client: that client has a timeout, and it would
// also keep the connection for itself, while an interactive stream has to own
// the socket for as long as the shell is open. So this dials its own
// connection, asks for an upgrade, and keeps the socket once the daemon
// agrees.
//
// width and height set the terminal size from the first byte on. Zero leaves
// it to ResizeExec.
func (c *Client) StartExec(ctx context.Context, id string, width, height int) (*ExecStream, error) {
	start := struct {
		Detach      bool    `json:"Detach"`
		Tty         bool    `json:"Tty"`
		ConsoleSize *[2]int `json:"ConsoleSize,omitempty"`
	}{Tty: true}
	// ConsoleSize arrived in API 1.42. An older daemon rejects the field, so
	// there the size comes from the first ResizeExec instead.
	if width > 0 && height > 0 && !olderThan(c.api, "1.42") {
		start.ConsoleSize = &[2]int{height, width}
	}
	body, err := json.Marshal(start)
	if err != nil {
		return nil, err
	}

	path := "/v" + c.api + "/exec/" + url.PathEscape(id) + "/start"
	req, err := c.request(ctx, http.MethodPost, path, nil, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "tcp")

	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.sock)
	if err != nil {
		return nil, err
	}
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, err
	}

	// The reader outlives the response: whatever it buffered past the headers
	// is already shell output, so the stream reads through it rather than
	// from the bare connection.
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusSwitchingProtocols, http.StatusOK:
		// 101 is the upgrade. Daemons from before it existed answer 200 and
		// stream on the same connection, which reads the same way.
		return &ExecStream{conn: conn, r: br}, nil
	}
	err = statusError(http.MethodPost, path, resp)
	conn.Close()
	return nil, err
}

// ResizeExec sets the exec's terminal size.
func (c *Client) ResizeExec(ctx context.Context, id string, width, height int) error {
	if width <= 0 || height <= 0 {
		return nil
	}
	q := url.Values{}
	q.Set("w", strconv.Itoa(width))
	q.Set("h", strconv.Itoa(height))
	rc, err := c.post(ctx, "/exec/"+url.PathEscape(id)+"/resize", q, nil)
	if err != nil {
		return err
	}
	return rc.Close()
}

// ExecExitCode waits briefly for the exec to be marked finished and returns
// its exit code. The stream closes a moment before the daemon records the
// exit, so the first answer can still say it is running.
func (c *Client) ExecExitCode(ctx context.Context, id string) (int, error) {
	for range 20 {
		rc, err := c.get(ctx, "/exec/"+url.PathEscape(id)+"/json", nil)
		if err != nil {
			return -1, err
		}
		var v struct {
			Running  bool `json:"Running"`
			ExitCode *int `json:"ExitCode"`
		}
		err = json.NewDecoder(rc).Decode(&v)
		rc.Close()
		if err != nil {
			return -1, err
		}
		if !v.Running && v.ExitCode != nil {
			return *v.ExitCode, nil
		}
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return -1, errors.New("exec did not finish")
}
