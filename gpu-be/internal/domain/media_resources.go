package domain

import (
	"context"
	"errors"
	"fmt"
	"syscall"
)

// ErrMediaResourceWait is temporary pressure, never evidence of damaged media.
var ErrMediaResourceWait = errors.New("media resources unavailable")

type MediaResourceError struct{ Code, Detail string }

func (e *MediaResourceError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Detail) }
func (e *MediaResourceError) Unwrap() error { return ErrMediaResourceWait }

type MediaResources interface {
	Begin(context.Context) (context.Context, func(), error)
	MkdirTemp(context.Context, string, string) (string, func(), error)
}

type ShortRenderSpec struct {
	Source, Banner   string
	StartSec, EndSec float64
	Rectangle        bool
}

// ShortRenderer is optional: unsupported templates retain the existing pipeline.
type ShortRenderer interface {
	RenderShort(context.Context, ShortRenderSpec, string) error
}

// BrandSpec is the branded 1080x1920 frame: the video shrunk to VideoScale of the width and pinned to the
// top centre over BgPath (empty = blurred copy of the video), ASSPath burnt in, audio pitch shifted.
type BrandSpec struct {
	VideoScale     float64
	PitchSemitones float64
	BgPath         string
	ASSPath        string
}

// BrandRenderer is optional, like ShortRenderer: media clients without it cannot render branded jobs.
type BrandRenderer interface {
	RenderBrand(ctx context.Context, in, out string, spec BrandSpec) error
}

// BrandSubsObject is the branded subtitles file in the transcripts bucket (under the job id).
const BrandSubsObject = "brand_subs.ass"

// BrandSubtitleTop is the y of the first subtitle line: just under the video shrunk to scale and pinned to the top.
func BrandSubtitleTop(scale float64) int { return int(1920*BrandScale(scale)) + 12 }

// BrandScale clamps the video share of the canvas width to a sane range (default 0.75).
func BrandScale(scale float64) float64 {
	if scale <= 0.2 || scale > 1 {
		return 0.75
	}
	return scale
}

type mediaIdentityKey struct{}
type mediaIdentity struct {
	job, clip, format string
	importID          string
	attemptID         string
	expectedVersion   int
	revision          int64
}

func WithMediaIdentity(ctx context.Context, job, clip, format string, revision int64) context.Context {
	v, _ := ctx.Value(mediaIdentityKey{}).(mediaIdentity)
	v.job, v.clip, v.format, v.revision = job, clip, format, revision
	return context.WithValue(ctx, mediaIdentityKey{}, v)
}
func MediaLogAttrs(ctx context.Context) []any {
	v, ok := ctx.Value(mediaIdentityKey{}).(mediaIdentity)
	if !ok {
		return nil
	}
	attrs := []any{"job_id", v.job, "clip_id", v.clip, "revision_id", v.revision, "format", v.format}
	if v.importID != "" {
		attrs = append(attrs, "import_id", v.importID)
	}
	if v.attemptID != "" {
		attrs = append(attrs, "attempt_id", v.attemptID, "expected_version", v.expectedVersion)
	}
	return attrs
}

func WithMediaAttempt(ctx context.Context, id string, version int) context.Context {
	v, _ := ctx.Value(mediaIdentityKey{}).(mediaIdentity)
	v.attemptID, v.expectedVersion = id, version
	return context.WithValue(ctx, mediaIdentityKey{}, v)
}

func MediaCause(ctx context.Context, err error) error {
	if cause := context.Cause(ctx); errors.Is(cause, ErrMediaResourceWait) {
		return cause
	}
	if errors.Is(err, syscall.ENOSPC) {
		return &MediaResourceError{Code: "scratch_full", Detail: err.Error()}
	}
	return err
}

func MediaResultCode(err error) string {
	if err == nil {
		return "success"
	}
	if errors.Is(err, ErrMediaResourceWait) {
		return "resource_wait"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "canceled"
	}
	return "error"
}

func WithMediaImport(ctx context.Context, id string) context.Context {
	v, _ := ctx.Value(mediaIdentityKey{}).(mediaIdentity)
	v.importID, v.format = id, "verification"
	return context.WithValue(ctx, mediaIdentityKey{}, v)
}
