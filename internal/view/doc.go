// Package view is what ps, top and the Paseo plugin read: the ps --view document (everything as
// one JSON value) and the readable lines of a worker log. These types are the contract with the
// Paseo plugin (extensions/paseo/shared/view.ts): change both together, and bump the version
// unless the change only adds fields.
package view

// Version is the version of the ps --view document.
const Version = 1

// View is everything squad runs: ps, top and the Paseo plugin show it.
type View struct {
	Version int      `json:"version"`
	Daemon  Daemon   `json:"daemon"`
	Summary Summary  `json:"summary"`
	Teams   []Team   `json:"teams"`
	Solos   []Member `json:"solos"`
	Events  []Event  `json:"events"`
	// Notices are the latest notices about the teams' mail, newest first.
	Notices []Notice `json:"notices"`
}

type Daemon struct {
	Version   string `json:"version"`
	Pid       int    `json:"pid"`
	StartedAt string `json:"started_at"`
}

type Summary struct {
	Teams    int `json:"teams"`
	Sessions int `json:"sessions"`
	Workers  int `json:"workers"`
	Working  int `json:"working"`
}

type Team struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Template  string   `json:"template"`
	Root      string   `json:"root"`
	Status    string   `json:"status"`
	Gate      string   `json:"gate"`
	Parent    string   `json:"parent"`
	CreatedAt string   `json:"created_at"`
	Members   []Member `json:"members"`
}

type Member struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	Model      string `json:"model"`
	ReportsTo  string `json:"reports_to"`
	Task       string `json:"task"`
	Unread     int    `json:"unread"`
	Cwd        string `json:"cwd"`
	LastActive string `json:"last_active"`
}

// Notice is one of squad's notices (reply, settled, failed, gate_lost).
type Notice struct {
	At    string `json:"at"`
	Age   string `json:"age"`   // 12s, 5m, 3h, 4d
	Where string `json:"where"` // the team, else -
	Kind  string `json:"kind"`
	Body  string `json:"body"`
}

type Event struct {
	At   string `json:"at"`
	Kind string `json:"kind"`
	Team string `json:"team"`
	Text string `json:"text"`
}
