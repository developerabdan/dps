package dockerapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// PreferredAPI is the highest version dps asks for. Everything it needs exists
// at 1.41, so degrading below this costs no functionality.
const PreferredAPI = "1.44"

// Client is a Docker Engine client over a unix socket.
type Client struct {
	hc   *http.Client
	api  string
	sock string
}

// New dials the resolved socket and negotiates an API version before
// returning. Callers get a client that is already known to work.
func New(ctx context.Context) (*Client, error) {
	sock, err := ResolveSocket()
	if err != nil {
		return nil, err
	}
	c := &Client{
		sock: sock,
		api:  PreferredAPI,
		hc: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", sock)
				},
				// The cpu column reads stats for several containers at once.
				// The default of two idle connections would close and dial
				// most of them again on every poll.
				MaxIdleConnsPerHost: StatsWorkers,
			},
		},
	}
	if err := c.negotiate(ctx); err != nil {
		return nil, fmt.Errorf("cannot reach docker at %s: %w", sock, err)
	}
	return c, nil
}

// APIVersion reports the negotiated version, for --version output.
func (c *Client) APIVersion() string { return c.api }

// Socket reports the resolved socket path, so errors can name it.
func (c *Client) Socket() string { return c.sock }

// negotiate asks the daemon what it supports rather than pinning a version.
// A pinned /v1.41/ prefix is rejected outright by Docker 29.0 through 29.2,
// which raised their floor to 1.44 before 29.3 lowered it again.
func (c *Client) negotiate(ctx context.Context) error {
	rc, err := c.raw(ctx, http.MethodGet, "/version", nil, nil)
	if err != nil {
		return err
	}
	defer rc.Close()

	var v struct {
		APIVersion    string `json:"ApiVersion"`
		MinAPIVersion string `json:"MinAPIVersion"`
	}
	if err := json.NewDecoder(rc).Decode(&v); err != nil {
		return fmt.Errorf("unreadable /version response: %w", err)
	}
	c.api = pickVersion(PreferredAPI, v.APIVersion, v.MinAPIVersion)
	return nil
}

// pickVersion clamps the preferred version into the daemon's supported range:
// never above what it offers, never below what it accepts.
func pickVersion(want, daemonMax, daemonMin string) string {
	use := want
	if daemonMax != "" && olderThan(daemonMax, use) {
		use = daemonMax
	}
	if daemonMin != "" && olderThan(use, daemonMin) {
		use = daemonMin
	}
	return use
}

func olderThan(a, b string) bool {
	aMaj, aMin := splitVersion(a)
	bMaj, bMin := splitVersion(b)
	if aMaj != bMaj {
		return aMaj < bMaj
	}
	return aMin < bMin
}

func splitVersion(v string) (int, int) {
	parts := strings.SplitN(strings.TrimPrefix(v, "v"), ".", 2)
	major, _ := strconv.Atoi(parts[0])
	minor := 0
	if len(parts) > 1 {
		minor, _ = strconv.Atoi(parts[1])
	}
	return major, minor
}

// get issues a version-prefixed request. Unversioned calls are deprecated by
// the Engine API, so every endpoint except /version goes through here.
func (c *Client) get(ctx context.Context, path string, q url.Values) (io.ReadCloser, error) {
	return c.raw(ctx, http.MethodGet, "/v"+c.api+path, q, nil)
}

// post sends body as JSON when it is not nil.
func (c *Client) post(ctx context.Context, path string, q url.Values, body []byte) (io.ReadCloser, error) {
	return c.raw(ctx, http.MethodPost, "/v"+c.api+path, q, body)
}

// raw performs the request. The host in the URL is a placeholder; the dialer
// ignores it and connects to the socket.
func (c *Client) raw(ctx context.Context, method, path string, q url.Values, body []byte) (io.ReadCloser, error) {
	req, err := c.request(ctx, method, path, q, body)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, statusError(method, path, resp)
	}
	return resp.Body, nil
}

func (c *Client) request(ctx context.Context, method, path string, q url.Values, body []byte) (*http.Request, error) {
	u := "http://docker" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// statusError reads the start of a failed response into the error, because
// the daemon puts the reason in the body and the status alone rarely says it.
func statusError(method, path string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	resp.Body.Close()
	return fmt.Errorf("docker %s %s: %s: %s",
		method, path, resp.Status, strings.TrimSpace(string(body)))
}
