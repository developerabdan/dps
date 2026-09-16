package dockerapi

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strings"

	"github.com/developerabdan/dps/internal/model"
)

// ListOptions mirrors the query parameters of GET /containers/json.
type ListOptions struct {
	All     bool
	Size    bool
	Filters map[string][]string
}

// apiContainer is the wire shape. Only the fields dps renders are declared;
// the daemon sends considerably more.
type apiContainer struct {
	ID      string   `json:"Id"`
	Names   []string `json:"Names"`
	Image   string   `json:"Image"`
	Command string   `json:"Command"`
	Created int64    `json:"Created"`
	State   string   `json:"State"`
	Status  string   `json:"Status"`
	SizeRw  int64    `json:"SizeRw"`
	Ports   []struct {
		IP          string `json:"IP"`
		PrivatePort int    `json:"PrivatePort"`
		PublicPort  int    `json:"PublicPort"`
		Type        string `json:"Type"`
	} `json:"Ports"`
	Labels          map[string]string `json:"Labels"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

// ListContainers returns the container list in daemon order.
func (c *Client) ListContainers(ctx context.Context, opt ListOptions) ([]model.Container, error) {
	q := url.Values{}
	if opt.All {
		q.Set("all", "1")
	}
	if opt.Size {
		q.Set("size", "1")
	}
	if len(opt.Filters) > 0 {
		encoded, err := json.Marshal(opt.Filters)
		if err != nil {
			return nil, err
		}
		q.Set("filters", string(encoded))
	}

	rc, err := c.get(ctx, "/containers/json", q)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	var raw []apiContainer
	if err := json.NewDecoder(rc).Decode(&raw); err != nil {
		return nil, err
	}

	out := make([]model.Container, 0, len(raw))
	for _, r := range raw {
		out = append(out, convert(r))
	}
	return out, nil
}

func convert(r apiContainer) model.Container {
	c := model.Container{
		ID:      r.ID,
		Image:   r.Image,
		State:   r.State,
		Status:  r.Status,
		Health:  model.ParseHealth(r.Status),
		Created: r.Created,
		Size:    r.SizeRw,
		Command: r.Command,
		Project: r.Labels["com.docker.compose.project"],
		Service: r.Labels["com.docker.compose.service"],
	}
	if len(r.Names) > 0 {
		c.Name = strings.TrimPrefix(r.Names[0], "/")
	}
	for _, p := range r.Ports {
		c.Ports = append(c.Ports, model.Port{
			IP:      p.IP,
			Public:  p.PublicPort,
			Private: p.PrivatePort,
			Type:    p.Type,
		})
	}
	// Map order is random in Go, so the network is chosen by sorted name to
	// keep the IP column stable between runs.
	if n := len(r.NetworkSettings.Networks); n > 0 {
		names := make([]string, 0, n)
		for name := range r.NetworkSettings.Networks {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if ip := r.NetworkSettings.Networks[name].IPAddress; ip != "" {
				c.IP = ip
				break
			}
		}
	}
	return c
}
