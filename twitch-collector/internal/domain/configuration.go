package domain

import (
	"terex/proto/twitch"
	"time"
)

// Operational counters change without a settings revision. They must not turn
// a five-second control observation into another full consumer reconciliation.
func ConfigurationFlow(f twitch.Flow) twitch.Flow {
	f.CreatedAt = time.Time{}
	f.UpdatedAt = time.Time{}
	f.AutoEnabledAt = nil
	f.WorkStartedAt = nil
	f.LastCheckedAt = nil
	f.BlockReason = ""
	f.FoundClips = 0
	f.ReadyOutputs = 0
	f.NextPublicationAt = nil
	return f
}
