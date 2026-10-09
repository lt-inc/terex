package twitch

import "time"

// Runtime counts represent physical work, not repeated appearances in flows.
type StageCount struct {
	Stage  string `json:"stage"`
	State  string `json:"state"`
	Format string `json:"format"`
	Count  int    `json:"count"`
}

type ActiveTask struct {
	ClipID        string     `json:"clip_id"`
	Title         string     `json:"title"`
	FlowID        string     `json:"flow_id"`
	FlowName      string     `json:"flow_name"`
	Format        string     `json:"format"`
	Stage         string     `json:"stage"`
	State         string     `json:"state"`
	Attempt       int        `json:"attempt"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	ErrorKind     string     `json:"error_kind,omitempty"`
	Error         string     `json:"error,omitempty"`
	SourceURL     string     `json:"source_url,omitempty"`
	TerexURL      string     `json:"terex_url,omitempty"`
}

type RuntimeWork struct {
	Epoch       int64        `json:"epoch"`
	Counts      []StageCount `json:"counts"`
	ActiveTasks []ActiveTask `json:"active_tasks"`
	ActiveTotal int          `json:"active_total"`
}
