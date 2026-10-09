package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"terex/main-be/internal/domain"
	"terex/proto/twitch"
	"terex/proto/workflow"
)

func TestRevalidationReceiptFencesLostResponsesAndRetriesSpentSource(t *testing.T) {
	s, account, settings := twitchFixture(t)
	ctx, now := context.Background(), time.Now().UTC().Truncate(time.Second)
	settings.AdmissionMode, settings.DailyDownloads, settings.PerChannelDailyDownloads = "enforce", 1, 1
	if err := s.SaveTwitchSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	f := flowFixture(t, s, account, false, now)
	f.ShortsEnabled = false
	f.CompilationMinDurationSec, f.CompilationMaxDurationSec, f.CompilationMaxClips = 300, 600, 30
	var err error
	if f, err = s.SaveTwitchFlow(ctx, f, false, now); err != nil {
		t.Fatal(err)
	}
	src := flowSource("recover-flow-source", "123", 30, now)
	if _, err = s.ObserveTwitchSources(ctx, twitch.ObservationRequest{FlowID: f.ID, FlowVersion: f.Version, Sources: []twitch.Source{src}}, now); err != nil {
		t.Fatal(err)
	}
	allocate := func() twitch.DownloadGrant {
		t.Helper()
		v, e := s.AllocateTwitchDownloads(ctx, twitch.AdmissionRequest{Limit: 1}, now)
		if e != nil || len(v.Grants) != 1 {
			t.Fatal("missing recovery grant", v, e)
		}
		return v.Grants[0]
	}
	first := allocate()
	f.Name += " changed"
	if f, err = s.SaveTwitchFlow(ctx, f, false, now); err != nil {
		t.Fatal(err)
	}
	request := func(g twitch.DownloadGrant) twitch.ImportRequest {
		return twitch.ImportRequest{SchemaVersion: 2, IdempotencyKey: "recover:" + src.ClipID, Source: src, FlowID: g.FlowID, FlowVersion: g.FlowVersion, AdmissionSequence: g.AdmissionSequence}
	}
	_, err = s.ReserveFlowImport(ctx, request(first), now)
	var wait *domain.PreparationWaitError
	if !errors.As(err, &wait) || wait.Code != "flow_version_changed" {
		t.Fatal("version change was not typed", err)
	}
	q := twitch.RevalidateImportRequest{AdmissionSequence: first.AdmissionSequence, FailureToken: "first-failure-token-123", FlowID: f.ID, FlowVersion: f.Version, Reason: "flow_version_changed"}
	receipt, err := s.RevalidateFlowImport(ctx, first.ImportID, q, now)
	if err != nil || receipt.State != "discovered" {
		t.Fatal(receipt, err)
	}
	second := allocate()
	if second.AdmissionSequence <= first.AdmissionSequence || second.RevalidationToken != q.FailureToken {
		t.Fatal("grant lost revalidation identity", second)
	}
	if _, err = s.RevalidateFlowImport(ctx, first.ImportID, q, now); err != nil {
		t.Fatal("lost response was not idempotent", err)
	}
	if err = s.AbortFlowImportFenced(ctx, first.ImportID, twitch.AbortImportRequest{AdmissionSequence: first.AdmissionSequence, FailureToken: q.FailureToken}, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("old abort accepted", err)
	}
	var state string
	if err = s.pool.QueryRow(ctx, `SELECT state FROM twitch_download_grants WHERE import_id=$1`, first.ImportID).Scan(&state); err != nil || state != "active" {
		t.Fatal("repeated recovery destroyed new grant", state, err)
	}
	imported, err := s.ReserveFlowImport(ctx, request(second), now)
	if err != nil || imported.UploadGeneration != 1 {
		t.Fatal(imported, err)
	}
	failedToken := "second-failure-token-456"
	if err = s.AbortFlowImportFenced(ctx, first.ImportID, twitch.AbortImportRequest{AdmissionSequence: second.AdmissionSequence, UploadGeneration: 1, FailureToken: failedToken}, now); err != nil {
		t.Fatal(err)
	}
	manual := twitch.RevalidateImportRequest{AdmissionSequence: second.AdmissionSequence, UploadGeneration: 1, FailureToken: failedToken, FlowID: f.ID, FlowVersion: f.Version, Reason: "manual_retry", Manual: true}
	if _, err = s.RevalidateFlowImport(ctx, first.ImportID, manual, now); err != nil {
		t.Fatal(err)
	}
	third := allocate() // Already charged today; no second distinct-source quota.
	if third.AdmissionSequence <= second.AdmissionSequence {
		t.Fatal(third)
	}
	if _, err = s.RevalidateFlowImport(ctx, first.ImportID, q, now); err != nil {
		t.Fatal("older receipt was lost", err)
	}
	resumed, err := s.ReserveFlowImport(ctx, request(third), now)
	if err != nil || resumed.UploadGeneration != 2 {
		t.Fatal("spent source could not resume", resumed, err)
	}
	active := manual
	active.AdmissionSequence = third.AdmissionSequence
	active.UploadGeneration = 2
	active.FailureToken = "cannot-reset-active-789"
	if _, err = s.RevalidateFlowImport(ctx, first.ImportID, active, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("active upload was reset", err)
	}
	if err = s.SetFlowUpload(ctx, first.ImportID, 2, workflow.ObjectRef{Bucket: "imports", Object: "staging/test-g2/upload.mp4"}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Verification may exhaust its attempts twice. Preserve the uploaded
	// generation, but allow a new failure token to release/retry it again.
	for _, token := range []string{"uploaded-failure-111", "uploaded-failure-222"} {
		abort := twitch.AbortImportRequest{AdmissionSequence: third.AdmissionSequence, UploadGeneration: 2, FailureToken: token}
		if err = s.AbortFlowImportFenced(ctx, first.ImportID, abort, now); err != nil {
			t.Fatal("second verification failure could not abort", err)
		}
		v := twitch.RevalidateImportRequest{AdmissionSequence: third.AdmissionSequence, UploadGeneration: 2, FailureToken: token, FlowID: f.ID, FlowVersion: f.Version, Manual: true, Uploaded: true}
		if got, e := s.RevalidateFlowImport(ctx, first.ImportID, v, now); e != nil || got.State != "verification_retry" {
			t.Fatal(got, e)
		}
		if err = s.AbortFlowImportFenced(ctx, first.ImportID, abort, now); !errors.Is(err, domain.ErrConflict) {
			t.Fatal("old verification abort passed receipt fence", err)
		}
	}
	settings.AdmissionMode, settings.DailyDownloads, settings.PerChannelDailyDownloads = "shadow", 10, 10
	if err = s.SaveTwitchSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	legacy := reserveFlow(t, s, f, flowSource("legacy-no-grant", "123", 30, now), now)
	if err = s.SetFlowUpload(ctx, legacy.ImportID, legacy.UploadGeneration, workflow.ObjectRef{Bucket: "imports", Object: "staging/legacy/upload.mp4"}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"legacy-failure-111", "legacy-failure-222"} {
		q := twitch.AbortImportRequest{UploadGeneration: legacy.UploadGeneration, FailureToken: token}
		if err = s.AbortFlowImportFenced(ctx, legacy.ImportID, q, now); err != nil {
			t.Fatal("legacy repeated verification could not abort", err)
		}
		retry := twitch.RevalidateImportRequest{UploadGeneration: legacy.UploadGeneration, FailureToken: token, FlowID: f.ID, FlowVersion: f.Version, Manual: true, Uploaded: true}
		if _, err = s.RevalidateFlowImport(ctx, legacy.ImportID, retry, now); err != nil {
			t.Fatal("legacy repeated verification could not recover", err)
		}
		if err = s.AbortFlowImportFenced(ctx, legacy.ImportID, q, now); !errors.Is(err, domain.ErrConflict) {
			t.Fatal("legacy old abort passed", err)
		}
	}
	if _, err = s.pool.Exec(ctx, `UPDATE twitch_reset_control SET epoch=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RevalidateFlowImport(ctx, first.ImportID, q, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("old reset epoch was accepted", err)
	}
}
