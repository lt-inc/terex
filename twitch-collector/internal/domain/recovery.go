package domain

// UploadedMedia is durable even when the collector's local temporary file is
// gone. SHA/size identify the exact quarantined upload verified by gpu-be.
type UploadedMedia struct {
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
	Phase     string `json:"phase"` // uploading (PUT uncertain) or uploaded
}
