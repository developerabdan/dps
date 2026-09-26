package dockerapi

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// LogLine is one line of a container's output. The line ending is removed.
type LogLine struct {
	Text   string
	Stderr bool
}

// LogStream reads a container's log one line at a time until the container
// stops or the context given to Logs is cancelled.
type LogStream struct {
	body io.ReadCloser
	r    *bufio.Reader
	mux  bool

	// pending is the start of a line whose end has not arrived yet, one for
	// stdout and one for stderr. A multiplexed frame can end in the middle of
	// a line, and the two streams interleave, so each keeps its own.
	pending [2]string
	queue   []LogLine
}

// Logs opens the log of a container: the last tail lines, then every new line
// as it is written. A stopped container sends what it has and then ends.
//
// The request cannot go through the shared http.Client, because that client
// has a timeout and a log stream stays open for as long as the view does. It
// uses the same transport, so it dials the same socket.
func (c *Client) Logs(ctx context.Context, id string, tail int) (*LogStream, error) {
	q := url.Values{}
	q.Set("follow", "1")
	q.Set("stdout", "1")
	q.Set("stderr", "1")
	q.Set("tail", strconv.Itoa(tail))
	path := "/v" + c.api + "/containers/" + url.PathEscape(id) + "/logs"
	req, err := c.request(ctx, http.MethodGet, path, q, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Transport: c.hc.Transport}).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, statusError(http.MethodGet, path, resp)
	}
	return newLogStream(resp.Body, resp.Header.Get("Content-Type")), nil
}

// newLogStream decides how to read the body. A container without a TTY sends
// stdout and stderr in frames, each with an eight-byte header; a container
// with a TTY sends the raw terminal output. Daemons from API 1.42 name the
// format in the content type. Older ones do not, so there the first bytes
// decide: a frame header starts with a stream number from 0 to 2 and three
// zero bytes, which log text does not.
func newLogStream(body io.ReadCloser, contentType string) *LogStream {
	s := &LogStream{body: body, r: bufio.NewReader(body)}
	switch {
	case strings.Contains(contentType, "multiplexed-stream"):
		s.mux = true
	case strings.Contains(contentType, "raw-stream"):
		s.mux = false
	default:
		head, _ := s.r.Peek(8)
		s.mux = len(head) == 8 && head[0] <= 2 && head[1] == 0 && head[2] == 0 && head[3] == 0
	}
	return s
}

// Next returns the next whole line. At the end of the stream it returns the
// last line even if it has no line ending, and then io.EOF.
func (s *LogStream) Next() (LogLine, error) {
	if !s.mux {
		text, err := s.r.ReadString('\n')
		if err != nil && text == "" {
			return LogLine{}, err
		}
		return LogLine{Text: trimEOL(text)}, nil
	}
	for len(s.queue) == 0 {
		if err := s.readFrame(); err != nil {
			s.flush()
			if len(s.queue) == 0 {
				return LogLine{}, err
			}
		}
	}
	l := s.queue[0]
	s.queue = s.queue[1:]
	return l, nil
}

// Close ends the stream.
func (s *LogStream) Close() error { return s.body.Close() }

// readFrame reads one frame and queues every line it completes.
func (s *LogStream) readFrame() error {
	var head [8]byte
	if _, err := io.ReadFull(s.r, head[:]); err != nil {
		return err
	}
	payload := make([]byte, binary.BigEndian.Uint32(head[4:]))
	if _, err := io.ReadFull(s.r, payload); err != nil {
		return err
	}
	// Stream 2 is stderr. Stream 0 is stdin, which a log never carries, and
	// is read as stdout rather than dropped.
	stream := 0
	if head[0] == 2 {
		stream = 1
	}
	text := s.pending[stream] + string(payload)
	for {
		i := strings.IndexByte(text, '\n')
		if i < 0 {
			break
		}
		s.queue = append(s.queue, LogLine{Text: trimEOL(text[:i]), Stderr: stream == 1})
		text = text[i+1:]
	}
	s.pending[stream] = text
	return nil
}

// flush queues the lines that the stream ended before finishing.
func (s *LogStream) flush() {
	for i, text := range s.pending {
		if text != "" {
			s.queue = append(s.queue, LogLine{Text: trimEOL(text), Stderr: i == 1})
			s.pending[i] = ""
		}
	}
}

func trimEOL(s string) string {
	return strings.TrimRight(s, "\r\n")
}
