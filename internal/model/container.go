// Package model holds the normalized container shape dps renders, plus the
// transforms that turn Docker's raw fields into something that fits a
// terminal. Nothing here talks to Docker.
package model

// Port is one published mapping. Public is 0 when the port is exposed inside
// the network but not published to the host.
type Port struct {
	IP      string `json:"ip,omitempty"`
	Public  int    `json:"public,omitempty"`
	Private int    `json:"private"`
	Type    string `json:"type,omitempty"`
}

// Container is one row. Fields are raw; the Short*/Compact* transforms render
// them.
//
// The json tags are a public contract: `dps --json` feeds other people's
// scripts, so these names are snake_case and stable, not whatever Go happens
// to call the fields.
type Container struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Image   string `json:"image"`
	State   string `json:"state"`
	Status  string `json:"status"`
	Health  string `json:"health,omitempty"`
	Ports   []Port `json:"ports,omitempty"`
	Created int64  `json:"created"`

	Project string `json:"project,omitempty"`
	Service string `json:"service,omitempty"`
	IP      string `json:"ip,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Command string `json:"command,omitempty"`

	// Restarts is not carried by GET /containers/json; only a per-container
	// inspect returns RestartCount. It stays 0 until the restarts column is
	// asked for, which triggers that extra round trip.
	Restarts int `json:"restarts"`
}
