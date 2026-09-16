// Package dockerapi is a hand-rolled client for the few Docker Engine
// endpoints dps needs. It imports nothing outside the standard library:
// the daemon socket speaks plain HTTP, so net/http over a unix dialer is the
// whole transport.
package dockerapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultSocket is the last resort, not the first guess. Docker Desktop for
// macOS does not create it.
const DefaultSocket = "/var/run/docker.sock"

// ResolveSocket walks the same order the docker CLI does: DOCKER_HOST, then
// the active context's endpoint, then the default path. Skipping the context
// step is why naive tools fail on Docker Desktop, where the endpoint is
// ~/.docker/run/docker.sock under the context "desktop-linux".
func ResolveSocket() (string, error) {
	if h := os.Getenv("DOCKER_HOST"); h != "" {
		if strings.HasPrefix(h, "unix://") {
			return strings.TrimPrefix(h, "unix://"), nil
		}
		return "", fmt.Errorf("dps speaks to unix sockets only, but DOCKER_HOST is %q", h)
	}
	if home, err := os.UserHomeDir(); err == nil {
		if sock := socketFromContext(home); sock != "" {
			return sock, nil
		}
	}
	return DefaultSocket, nil
}

// socketFromContext reads the active context and returns its docker endpoint.
// Every failure here is a silent fall-through to the default path: a missing
// or malformed context file means "no opinion", not an error.
func socketFromContext(home string) string {
	var cfg struct {
		CurrentContext string `json:"currentContext"`
	}
	b, err := os.ReadFile(filepath.Join(home, ".docker", "config.json"))
	if err != nil || json.Unmarshal(b, &cfg) != nil {
		return ""
	}
	if cfg.CurrentContext == "" || cfg.CurrentContext == "default" {
		return ""
	}

	// Context metadata is stored under the hex sha256 of the context name.
	sum := sha256.Sum256([]byte(cfg.CurrentContext))
	b, err = os.ReadFile(filepath.Join(
		home, ".docker", "contexts", "meta", hex.EncodeToString(sum[:]), "meta.json",
	))
	if err != nil {
		return ""
	}

	var meta struct {
		Endpoints struct {
			Docker struct {
				Host string `json:"Host"`
			} `json:"docker"`
		} `json:"Endpoints"`
	}
	if json.Unmarshal(b, &meta) != nil {
		return ""
	}
	host := meta.Endpoints.Docker.Host
	if !strings.HasPrefix(host, "unix://") {
		return ""
	}
	return strings.TrimPrefix(host, "unix://")
}
