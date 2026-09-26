package dockerapi

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func frame(stream byte, text string) []byte {
	head := make([]byte, 8)
	head[0] = stream
	binary.BigEndian.PutUint32(head[4:], uint32(len(text)))
	return append(head, text...)
}

func readAll(t *testing.T, s *LogStream) []LogLine {
	t.Helper()
	var out []LogLine
	for {
		l, err := s.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, l)
	}
}

func TestLogStreamDemuxesFrames(t *testing.T) {
	var body []byte
	body = append(body, frame(1, "one\ntw")...)
	body = append(body, frame(2, "err\n")...)
	body = append(body, frame(1, "o\r\nthree")...)

	for _, ct := range []string{"application/vnd.docker.multiplexed-stream", ""} {
		s := newLogStream(io.NopCloser(bytes.NewReader(body)), ct)
		got := readAll(t, s)
		want := []LogLine{{"one", false}, {"err", true}, {"two", false}, {"three", false}}
		if len(got) != len(want) {
			t.Fatalf("content type %q: got %v, want %v", ct, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("content type %q: line %d is %v, want %v", ct, i, got[i], want[i])
			}
		}
	}
}

func TestLogStreamReadsRawTTY(t *testing.T) {
	body := "hello\r\nworld\nlast"
	for _, ct := range []string{"application/vnd.docker.raw-stream", ""} {
		s := newLogStream(io.NopCloser(bytes.NewReader([]byte(body))), ct)
		got := readAll(t, s)
		if len(got) != 3 || got[0].Text != "hello" || got[1].Text != "world" || got[2].Text != "last" {
			t.Errorf("content type %q: got %v", ct, got)
		}
	}
}
