package ingestgateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"terex/gpu-be/internal/domain"
	"terex/gpu-be/internal/service/resetgate"
	"terex/proto/workflow"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const Bucket = "imports"
const MaxBytes int64 = 512 * 1024 * 1024

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var digest = regexp.MustCompile(`^[a-f0-9]{64}$`)

func unavailable(ctx context.Context, action string, err error) error {
	if ctx.Err() != nil {
		return status.FromContextError(ctx.Err()).Err()
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return status.FromContextError(err).Err()
	}
	return status.Errorf(codes.Unavailable, "%s: %v", action, err)
}

func invalidMedia(ctx context.Context, action string, err error) error {
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, exec.ErrNotFound) {
		return unavailable(ctx, action, err)
	}
	return status.Errorf(codes.InvalidArgument, "%s: %v", action, err)
}

type Probe = domain.MediaProbe
type Media interface {
	Probe(context.Context, string) (Probe, error)
	VerifyIntegrity(context.Context, string) error
}
type Store interface {
	PresignInternalPut(context.Context, domain.ObjectRef, time.Duration) (string, error)
	DownloadLimited(context.Context, domain.ObjectRef, string, int64) error
	Upload(context.Context, domain.ObjectRef, string) error
}
type Service struct {
	Reset  *resetgate.Gate
	Store  Store
	Media  Media
	TmpDir string
}

func staging(id string) domain.ObjectRef {
	return domain.ObjectRef{Bucket: Bucket, Object: "staging/" + id + "/upload.mp4"}
}
func (s *Service) ReserveUpload(ctx context.Context, q *workflow.ReserveUploadRequest) (*workflow.ReserveUploadResponse, error) {
	if q == nil || !safeID.MatchString(q.ImportID) {
		return nil, status.Error(codes.InvalidArgument, "invalid import ID")
	}
	ctx, release, err := s.Reset.Begin(ctx, q.Epoch)
	if err != nil {
		return nil, err
	}
	defer release()
	ref := staging(q.ImportID)
	if q.Ref.Bucket != "" && q.Ref != ref {
		return nil, status.Error(codes.InvalidArgument, "invalid staging reference")
	}
	const ttl = 15 * time.Minute
	u, err := s.Store.PresignInternalPut(ctx, ref, ttl)
	if err != nil {
		return nil, unavailable(ctx, "reserve upload", err)
	}
	return &workflow.ReserveUploadResponse{Ref: ref, UploadURL: u, ExpiresAt: time.Now().UTC().Add(ttl)}, nil
}
func (s *Service) VerifyUpload(ctx context.Context, q *workflow.VerifyUploadRequest) (*workflow.VerifiedMedia, error) {
	if q == nil || !safeID.MatchString(q.ImportID) || q.Ref != staging(q.ImportID) {
		return nil, status.Error(codes.InvalidArgument, "invalid staging reference")
	}
	ctx, release, err := s.Reset.Begin(ctx, q.Epoch)
	if err != nil {
		return nil, err
	}
	defer release()
	if q.ExpectedSHA256 != "" && !digest.MatchString(q.ExpectedSHA256) {
		return nil, status.Error(codes.InvalidArgument, "invalid SHA-256")
	}
	if q.ExpectedSizeBytes < 0 || q.ExpectedSizeBytes > MaxBytes {
		return nil, status.Error(codes.InvalidArgument, "invalid file size")
	}
	if err := os.MkdirAll(s.TmpDir, 0o755); err != nil {
		return nil, unavailable(ctx, "prepare verification directory", err)
	}
	dir, err := os.MkdirTemp(s.TmpDir, "verify-")
	if err != nil {
		return nil, unavailable(ctx, "prepare verification file", err)
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "source.mp4")
	if err = s.Store.DownloadLimited(ctx, q.Ref, file, MaxBytes); err != nil {
		return nil, unavailable(ctx, "download verification", err)
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, unavailable(ctx, "open verification file", err)
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	f.Close()
	if err != nil {
		return nil, unavailable(ctx, "read verification file", err)
	}
	hash := hex.EncodeToString(h.Sum(nil))
	if n <= 0 || n > MaxBytes || (q.ExpectedSizeBytes > 0 && n != q.ExpectedSizeBytes) || q.ExpectedSHA256 != "" && hash != q.ExpectedSHA256 {
		return nil, status.Error(codes.InvalidArgument, "file checksum or size mismatch")
	}
	p, err := s.Media.Probe(ctx, file)
	if err != nil {
		return nil, invalidMedia(ctx, "probe media", err)
	}
	if p.Width <= 0 || p.Height <= 0 || p.DurationSec <= 0 || p.DurationSec > 61 || math.IsNaN(p.DurationSec) || math.IsInf(p.DurationSec, 0) {
		return nil, status.Error(codes.InvalidArgument, "file lacks valid video stream or bounded duration")
	}
	if q.ExpectedDurationSec > 0 && (math.IsNaN(q.ExpectedDurationSec) || math.IsInf(q.ExpectedDurationSec, 0) || math.Abs(p.DurationSec-q.ExpectedDurationSec) > 2) {
		return nil, status.Error(codes.InvalidArgument, "duration differs from source metadata")
	}
	if err = s.Media.VerifyIntegrity(ctx, file); err != nil {
		return nil, invalidMedia(ctx, "corrupt video", err)
	}
	// Only fully verified bytes reach this content-addressed object. Concurrent
	// aliases publish identical bytes to the same key; staging URLs cannot write it.
	key := "verified/sha256/" + hash + ".mp4"
	if q.Epoch > 0 {
		key = fmt.Sprintf("verified/epoch-%d/sha256/%s.mp4", q.Epoch, hash)
	}
	ref := domain.ObjectRef{Bucket: Bucket, Object: key}
	if err = s.Store.Upload(ctx, ref, file); err != nil {
		return nil, unavailable(ctx, "store verified media", err)
	}
	return &workflow.VerifiedMedia{Original: ref, SHA256: hash, SizeBytes: n, DurationSec: p.DurationSec, Width: p.Width, Height: p.Height, HasAudio: p.HasAudio}, nil
}
