package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"terex/main-be/internal/domain"
	"terex/proto/workflow"
)

func TestManualMediaVerificationEnrichesOnlyPinnedAvailableRevision(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, _, clip := fixture(t, s, nil)
	r, err := s.LatestRevision(ctx, clip)
	if err != nil {
		t.Fatal(err)
	}
	q := domain.VerifyPublicationMediaRequest{RevisionID: r.ID, Version: r.Version, MediaKind: "rendered"}
	ref, err := s.PublicationMediaForVerification(ctx, clip, q)
	if err != nil {
		t.Fatal(err)
	}
	proof := workflow.MediaDigest{SHA256: strings.Repeat("a", 64), SizeBytes: 123}
	if err = s.RecordPublicationMediaVerification(ctx, clip, q, ref, proof); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordPublicationMediaVerification(ctx, clip, q, ref, proof); err != nil {
		t.Fatal("verification is not idempotent", err)
	}
	opts, err := s.ClipPublicationOptions(ctx, clip)
	if err != nil || opts.Variants[0].NeedsVerification || len(opts.Variants[0].Platforms) != 2 {
		t.Fatal(opts, err)
	}
	after, err := s.LatestRevision(ctx, clip)
	if err != nil || after.ID != r.ID || after.Version != r.Version || after.ApprovedAt != nil || after.Object != r.Object {
		t.Fatal("revision/editor changed", after, err)
	}
	var publications, attempts, outbox int
	if err = s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM publications),(SELECT count(*) FROM publication_attempts),(SELECT count(*) FROM publication_outbox)`).Scan(&publications, &attempts, &outbox); err != nil {
		t.Fatal(err)
	}
	if publications != 0 || attempts != 0 || outbox != 0 {
		t.Fatal("verification created publication work", publications, attempts, outbox)
	}
	proof.SHA256 = strings.Repeat("b", 64)
	if err = s.RecordPublicationMediaVerification(ctx, clip, q, ref, proof); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("existing hash silently replaced", err)
	}
	proof.SHA256 = strings.Repeat("a", 64)
	wrong := ref
	wrong.Object = "another.mp4"
	if err = s.RecordPublicationMediaVerification(ctx, clip, q, wrong, proof); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("another object accepted", err)
	}
	q.Version++
	if err = s.RecordPublicationMediaVerification(ctx, clip, q, ref, proof); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("stale version accepted", err)
	}
	q.Version--
	if _, err = s.pool.Exec(ctx, `UPDATE clip_revisions SET storage_state='deleted' WHERE id=$1`, r.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordPublicationMediaVerification(ctx, clip, q, ref, proof); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("deleted media revived", err)
	}
}
