package domain

import "terex/proto/twitch"

// The state fallback is only for backends predating media_state. An expired or
// quarantined media row never becomes ready because its logical source is ready.
func MediaReady(i twitch.Import) bool {
	return i.MediaState == "ready" || i.MediaState == "" && i.State == "ready"
}

// UploadedMedia is durable even when the collector's local temporary file is
// gone. SHA/size identify the exact quarantined upload verified by gpu-be.
type UploadedMedia struct {
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
	Phase     string `json:"phase"` // uploading (PUT uncertain) or uploaded
}
