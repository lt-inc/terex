package twitch

import "time"

// ClipGrowth is measured metadata, never a permission to publish stale media.
type ClipGrowth struct {
	State        string    `json:"state"`
	DeltaViews   int       `json:"delta_views"`
	IntervalSec  float64   `json:"interval_sec"`
	ViewsPerHour float64   `json:"views_per_hour"`
	ObservedAt   time.Time `json:"observed_at"`
	Anomalous    bool      `json:"anomalous"`
}

func GrowthStaleAfter(pollMinutes int) time.Duration {
	d := time.Duration(pollMinutes) * 2 * time.Minute
	if d < 30*time.Minute {
		d = 30 * time.Minute
	}
	if d > 6*time.Hour {
		d = 6 * time.Hour
	}
	return d
}

func MeasuredGrowth(g *ClipGrowth, pollMinutes int, now time.Time) bool {
	return g != nil && g.State == "measured" && !g.ObservedAt.After(now.Add(time.Minute)) && now.Sub(g.ObservedAt) <= GrowthStaleAfter(pollMinutes)
}

type DiscoveryWindow struct {
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Cursor      string    `json:"cursor,omitempty"`
	Pages       int       `json:"pages"`
	Restarts    int       `json:"restarts"`
	SeenCursors []string  `json:"seen_cursors,omitempty"`
}

// DiscoveryProgress contains only bounded metadata work. It never reserves a download.
type DiscoveryProgress struct {
	Epoch       int64             `json:"epoch"`
	FlowVersion int               `json:"flow_version"`
	AsOf        time.Time         `json:"as_of"`
	Pending     []DiscoveryWindow `json:"pending"`
	Completed   int               `json:"completed"`
	Total       int               `json:"total"`
	Truncated   bool              `json:"truncated"`
}

type DiscoveryCoverage struct {
	AsOf             time.Time `json:"as_of"`
	WindowsTotal     int       `json:"windows_total"`
	WindowsCompleted int       `json:"windows_completed"`
	Requests         int       `json:"requests"`
	Complete         bool      `json:"complete"`
	Reason           string    `json:"reason,omitempty"`
	RefreshClips     int       `json:"refresh_clips"`
}
