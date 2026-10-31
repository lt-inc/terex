package workflow

// References are resolved by main-be from a pinned revision, never from a user URL.
type VerifyPublicationMediaRequest struct {
	Media ObjectRef `json:"media"`
}

type MediaDigest struct {
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

const MaxPublicationMediaBytes int64 = 512 * 1024 * 1024
