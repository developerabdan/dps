package model

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	durRe         = regexp.MustCompile(`(\d+)\s+(second|minute|hour|day|week|month|year)s?`)
	exitRe        = regexp.MustCompile(`Exited \((\d+)\)`)
	trailingIndex = regexp.MustCompile(`[-_]\d+$`)
)

var unitLetter = map[string]string{
	"second": "s", "minute": "m", "hour": "h",
	"day": "d", "week": "w", "month": "mo", "year": "y",
}

// ShortImage drops the registry host and any digest, keeping repo:tag.
// "artifacts.plane.so/makeplane/plane-backend:v1.3.1" becomes
// "plane-backend:v1.3.1". The tag is what tells two running versions apart,
// so it is never the part that goes.
func ShortImage(image string) string {
	if i := strings.Index(image, "@"); i >= 0 {
		image = image[:i]
	}
	if i := strings.LastIndex(image, "/"); i >= 0 {
		image = image[i+1:]
	}
	return image
}

// ShortName strips the leading slash Docker returns, the compose project
// prefix, and the replica suffix: "/plane-app-api-1" with project "plane-app"
// becomes "api". The project is printed once in the footer instead of on
// every row.
func ShortName(name, project string) string {
	name = strings.TrimPrefix(name, "/")
	if project != "" {
		name = strings.TrimPrefix(name, project+"-")
		name = strings.TrimPrefix(name, project+"_")
	}
	if stripped := trailingIndex.ReplaceAllString(name, ""); stripped != "" {
		name = stripped
	}
	return name
}

// CompactPorts merges the IPv4/IPv6 pair Docker reports separately and drops
// unpublished ports. "0.0.0.0:8090->80/tcp, :::8090->80/tcp" becomes "8090→80";
// a mapping onto the same number collapses to just that number.
func CompactPorts(ports []Port) string {
	seen := make(map[string]bool, len(ports))
	out := make([]string, 0, len(ports))
	for _, p := range ports {
		if p.Public == 0 {
			continue
		}
		key := fmt.Sprintf("%d→%d", p.Public, p.Private)
		if p.Public == p.Private {
			key = strconv.Itoa(p.Public)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return strings.Join(out, " ")
}

// ShortState renders a glyph plus the shortest true description: "● up 6d",
// "✗ exited 0", "◐ restarting". Colour is applied by the renderer, so this
// still reads correctly when piped.
func ShortState(state, status string) string {
	switch state {
	case "running":
		if d := compactDuration(status); d != "" {
			return "● up " + d
		}
		return "● up"
	case "restarting":
		return "◐ restarting"
	case "paused":
		return "‖ paused"
	case "created":
		return "○ created"
	case "removing":
		return "◌ removing"
	case "exited":
		if m := exitRe.FindStringSubmatch(status); m != nil {
			return "✗ exited " + m[1]
		}
		return "✗ exited"
	case "dead":
		return "✗ dead"
	}
	return state
}

// ParseHealth pulls the healthcheck result out of the status string, which is
// where GET /containers/json reports it. Empty means no healthcheck defined.
func ParseHealth(status string) string {
	switch {
	case strings.Contains(status, "(healthy)"):
		return "healthy"
	case strings.Contains(status, "(unhealthy)"):
		return "unhealthy"
	case strings.Contains(status, "health: starting"):
		return "starting"
	}
	return ""
}

// CompactAge turns a unix creation time into one token: 45s, 12m, 3h, 6d.
func CompactAge(created int64) string {
	if created <= 0 {
		return ""
	}
	d := time.Since(time.Unix(created, 0))
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	default:
		return strconv.Itoa(int(d.Hours())/24) + "d"
	}
}

// compactDuration reads Docker's humanized status text. It writes "About an
// hour" and "Less than a second" rather than numbers in those ranges, so
// those forms are matched explicitly.
func compactDuration(s string) string {
	if m := durRe.FindStringSubmatch(s); m != nil {
		return m[1] + unitLetter[m[2]]
	}
	switch {
	case strings.Contains(s, "an hour"):
		return "1h"
	case strings.Contains(s, "a minute"):
		return "1m"
	case strings.Contains(s, "Less than"), strings.Contains(s, "a second"):
		return "1s"
	}
	return ""
}
