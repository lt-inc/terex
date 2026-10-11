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
