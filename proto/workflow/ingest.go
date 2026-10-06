package workflow

import (
	"context"
	"google.golang.org/grpc"
	"time"
)

type AssemblyItem struct {
	MediaID          string    `json:"media_id,omitempty"`
	TwitchSourceID   string    `json:"twitch_source_id,omitempty"`
	SourceClipID     string    `json:"source_clip_id"`
	SourceRevisionID int64     `json:"source_revision_id"`
	Original         ObjectRef `json:"original"`
	SHA256           string    `json:"sha256"`
	StartSec         float64   `json:"start_sec"`
	EndSec           float64   `json:"end_sec"`
	BroadcasterLogin string    `json:"broadcaster_login"`
}
type AssemblySpec struct {
	SchemaVersion        int            `json:"schema_version"`
	Profile              string         `json:"profile"`
	BumperDurationFrames int            `json:"bumper_duration_frames"`
	Items                []AssemblyItem `json:"items"`
}
type ReserveUploadRequest struct {
	ImportID string    `json:"import_id"`
	Ref      ObjectRef `json:"ref"`
}
type ReserveUploadResponse struct {
	Ref       ObjectRef `json:"ref"`
	UploadURL string    `json:"upload_url"`
	ExpiresAt time.Time `json:"expires_at"`
}
type VerifyUploadRequest struct {
	ImportID            string    `json:"import_id"`
	Ref                 ObjectRef `json:"ref"`
	ExpectedSHA256      string    `json:"expected_sha256"`
	ExpectedSizeBytes   int64     `json:"expected_size_bytes"`
	ExpectedDurationSec float64   `json:"expected_duration_sec"`
}
type VerifiedMedia struct {
	Original    ObjectRef `json:"original"`
	SHA256      string    `json:"sha256"`
	DurationSec float64   `json:"duration_sec"`
	SizeBytes   int64     `json:"size_bytes"`
	Width       int       `json:"width"`
	Height      int       `json:"height"`
	HasAudio    bool      `json:"has_audio"`
}

// IngestGateway isolates file handling from the control plane and collector.
type IngestGateway interface {
	ReserveUpload(context.Context, *ReserveUploadRequest) (*ReserveUploadResponse, error)
	VerifyUpload(context.Context, *VerifyUploadRequest) (*VerifiedMedia, error)
}
type ingestClient struct{ conn grpc.ClientConnInterface }

func NewIngestGatewayClient(conn grpc.ClientConnInterface) IngestGateway { return &ingestClient{conn} }
func (c *ingestClient) ReserveUpload(ctx context.Context, q *ReserveUploadRequest) (*ReserveUploadResponse, error) {
	return call[ReserveUploadRequest, ReserveUploadResponse](ctx, c.conn, "/terex.workflow.v1.IngestGateway/ReserveUpload", q)
}
func (c *ingestClient) VerifyUpload(ctx context.Context, q *VerifyUploadRequest) (*VerifiedMedia, error) {
	return call[VerifyUploadRequest, VerifiedMedia](ctx, c.conn, "/terex.workflow.v1.IngestGateway/VerifyUpload", q)
}
func RegisterIngestGatewayServer(reg grpc.ServiceRegistrar, srv IngestGateway) {
	const s = "terex.workflow.v1.IngestGateway"
	reg.RegisterService(&grpc.ServiceDesc{ServiceName: s, HandlerType: (*IngestGateway)(nil), Methods: []grpc.MethodDesc{method(s, "ReserveUpload", IngestGateway.ReserveUpload), method(s, "VerifyUpload", IngestGateway.VerifyUpload)}, Metadata: "workflow/workflow.proto"}, srv)
}
