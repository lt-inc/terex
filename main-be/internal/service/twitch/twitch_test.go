package twitch

import (
	"context"
	"errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"math"
	"strings"
	"terex/main-be/internal/domain"
	tw "terex/proto/twitch"
	"terex/proto/workflow"
	"testing"
	"time"
)

func sourceRequest(now time.Time) tw.ImportRequest {
	return tw.ImportRequest{SchemaVersion: 1, IdempotencyKey: "twitch:slug", Source: tw.Source{Platform: "twitch", ClipID: "slug", BroadcasterID: "123", BroadcasterLogin: "streamer", URL: "https://clips.twitch.tv/slug", Title: "Moment", CreatedAt: now.Add(-time.Hour), DurationSec: 20}}
}

func TestImportRejectsInvalidProvenanceMetadata(t *testing.T) {
	now := time.Now()
	for _, mutate := range []func(*tw.ImportRequest){
		func(r *tw.ImportRequest) { r.Source.DurationSec = math.NaN() },
		func(r *tw.ImportRequest) { r.Source.DurationSec = math.Inf(1) },
		func(r *tw.ImportRequest) { r.Source.DurationSec = 0 },
		func(r *tw.ImportRequest) { r.Source.DurationSec = 61 },
		func(r *tw.ImportRequest) { r.Source.BroadcasterLogin = "" },
		func(r *tw.ImportRequest) { r.Source.BroadcasterLogin = "bad/name" },
		func(r *tw.ImportRequest) { r.Source.BroadcasterID = "not-numeric" },
	} {
		r := sourceRequest(now)
		mutate(&r)
		if !errors.Is(ValidateImport(r, now), domain.ErrInvalid) {
			t.Fatal("accepted invalid metadata", r.Source)
		}
	}
}
func TestImportRejectsMismatchedURLAndUntrustedHosts(t *testing.T) {
	now := time.Now()
	for _, u := range []string{"https://clips.twitch.tv/other", "https://example.invalid/slug", "http://clips.twitch.tv/slug", "https://name:pass@clips.twitch.tv/slug", "https://clips.twitch.tv:444/slug"} {
		r := sourceRequest(now)
		r.Source.URL = u
		if !errors.Is(ValidateImport(r, now), domain.ErrInvalid) {
			t.Errorf("accepted %s", u)
		}
	}
	if err := ValidateImport(sourceRequest(now), now); err != nil {
		t.Fatal(err)
	}
}

type completeRepo struct {
	domain.TwitchRepo
	value    tw.Import
	finishes int
}

func (r *completeRepo) GetTwitchImport(context.Context, string) (tw.Import, error) {
	return r.value, nil
}
func (r *completeRepo) FinishTwitchImport(context.Context, string, *workflow.VerifiedMedia, time.Time) (tw.Import, error) {
	r.finishes++
	r.value.ClipID, r.value.State = "created-once", "rendering"
	return r.value, nil
}
func (r *completeRepo) FailTwitchImport(_ context.Context, _ string, message string) error {
	r.value.State, r.value.Error = "failed", message
	return nil
}

type verificationGateway struct {
	workflow.IngestGateway
	err   error
	calls int
}

func (g *verificationGateway) VerifyUpload(context.Context, *workflow.VerifyUploadRequest) (*workflow.VerifiedMedia, error) {
	g.calls++
	if g.err != nil {
		return nil, g.err
	}
	return &workflow.VerifiedMedia{Original: workflow.ObjectRef{Bucket: "imports", Object: "verified/id/media.mp4"}, SHA256: strings.Repeat("a", 64), DurationSec: 20, SizeBytes: 100}, nil
}

func TestCompleteTransientVerificationCanRetrySameImportWithoutDuplicateDraft(t *testing.T) {
	repo := &completeRepo{value: tw.Import{ImportID: "id", State: "upload_pending", Original: workflow.ObjectRef{Bucket: "imports", Object: "staging/id/upload.mp4"}}}
	gateway := &verificationGateway{err: status.Error(codes.Unavailable, "storage temporarily unavailable")}
	svc := &Service{Repo: repo, Gateway: gateway}
	req := tw.ImportCompleteRequest{SHA256: strings.Repeat("a", 64), SizeBytes: 100}
	_, err := svc.Complete(context.Background(), "id", req)
	if err == nil || errors.Is(err, domain.ErrInvalid) || repo.finishes != 0 || repo.value.State != "failed" {
		t.Fatal("transient verification became permanent", err, repo.finishes, repo.value)
	}
	gateway.err = nil
	out, err := svc.Complete(context.Background(), "id", req)
	if err != nil || out.ImportID != "id" || out.ClipID != "created-once" || repo.finishes != 1 || gateway.calls != 2 {
		t.Fatal(out, err, repo.finishes, gateway.calls)
	}
	_, err = svc.Complete(context.Background(), "id", req)
	if err != nil || repo.finishes != 1 || gateway.calls != 2 {
		t.Fatal("completed import was verified/created twice", err, repo.finishes, gateway.calls)
	}
}

func TestCompleteValidationFailuresRemainInvalid(t *testing.T) {
	for _, code := range []codes.Code{codes.InvalidArgument, codes.DataLoss} {
		repo := &completeRepo{value: tw.Import{ImportID: "id", State: "upload_pending", Original: workflow.ObjectRef{Bucket: "imports", Object: "staging/id/upload.mp4"}}}
		svc := &Service{Repo: repo, Gateway: &verificationGateway{err: status.Error(code, "invalid media")}}
		_, err := svc.Complete(context.Background(), "id", tw.ImportCompleteRequest{SHA256: strings.Repeat("a", 64), SizeBytes: 100})
		if !errors.Is(err, domain.ErrInvalid) || repo.finishes != 0 {
			t.Fatal(code, err)
		}
	}
}
func TestCompleteIsIdempotentWithoutCallingGatewayAgain(t *testing.T) {
	repo := &completeRepo{value: tw.Import{ImportID: "id", ClipID: "already-created", State: "rendering"}}
	svc := &Service{Repo: repo}
	out, err := svc.Complete(context.Background(), "id", tw.ImportCompleteRequest{})
	if err != nil || out.ClipID != "already-created" || repo.finishes != 0 {
		t.Fatal(out, err)
	}
}
func TestCompleteRejectsInvalidHashBeforeFileGateway(t *testing.T) {
	repo := &completeRepo{value: tw.Import{ImportID: "id", State: "upload_pending", Original: workflow.ObjectRef{Bucket: "imports", Object: "staging/id/upload.mp4"}}}
	svc := &Service{Repo: repo}
	_, err := svc.Complete(context.Background(), "id", tw.ImportCompleteRequest{SHA256: "not-a-hash", SizeBytes: 2})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatal(err)
	}
}
