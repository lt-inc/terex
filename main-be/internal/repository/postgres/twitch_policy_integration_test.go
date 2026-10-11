package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"terex/main-be/internal/domain"
	"terex/proto/twitch"
	w "terex/proto/workflow"
)

func TestTwitchPreparationSharesChannelCapacityAndRejectsStaleSlots(t *testing.T) {
	s, account, _ := twitchFixture(t)
	ctx := context.Background()
	day := time.Now().UTC()
	now := time.Date(day.Year(), day.Month(), day.Day(), 8, 0, 0, 0, time.UTC)
	f := flowFixture(t, s, account, false, now)
	f.CompilationsEnabled = false
	f.ShortsSlots = []string{"09:00", "10:00"}
	f.DailyShorts = 2
	f.ScheduleHorizonHours = 4
	f.MaxSourceAgeHours = 3
	var err error
	f, err = s.SaveTwitchFlow(ctx, f, false, now)
	if err != nil {
		t.Fatal(err)
	}
	alias := flowFixture(t, s, flowAccount(t, s, "UC-test"), false, now)
	alias.CompilationsEnabled = false
	alias.ShortsSlots = f.ShortsSlots
	alias.DailyShorts = 2
	alias.ScheduleHorizonHours = 4
	alias.MaxSourceAgeHours = 3
	alias, err = s.SaveTwitchFlow(ctx, alias, false, now)
	if err != nil {
		t.Fatal(err)
	}
	f, err = s.GetTwitchFlow(ctx, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyTwitchPolicy(ctx, now); err != nil {
		t.Fatal(err)
	}
	a := reserveFlow(t, s, f, flowSource("capacity-one", "123", 20, now), now)
	request := twitch.ImportRequest{SchemaVersion: 2, IdempotencyKey: "second", FlowID: alias.ID, FlowVersion: alias.Version, Source: flowSource("capacity-two", "123", 20, now)}
	_, err = s.ReserveFlowImport(ctx, request, now)
	var waiting *domain.PreparationWaitError
	if !errors.As(err, &waiting) || waiting.Code != "stale_for_slot" {
		t.Fatal("accepted a clip fresh now but stale at the remaining slot", err)
	}
	request.Source.CreatedAt = now.Add(-time.Hour)
	b, err := s.ReserveFlowImport(ctx, request, now)
	if err != nil {
		t.Fatal(err)
	}
	request.Source = flowSource("capacity-third", "123", 20, now)
	request.Source.CreatedAt = now.Add(-time.Hour)
	request.IdempotencyKey = "third"
	if _, err = s.ReserveFlowImport(ctx, request, now); !errors.As(err, &waiting) || waiting.Code != "calendar_full" {
		t.Fatal("OAuth alias doubled shared capacity", err)
	}
	ready := finishFlow(t, s, a, 20, "", now)
	if ready.ClipID == "" {
		t.Fatal("reserved source failed to create Short", ready)
	}
	cap, err := s.FlowCapacity(ctx, f.ID, now)
	if err != nil || len(cap.Formats) != 1 || cap.Formats[0].PreparingSlots != 2 || cap.Formats[0].FreeSlots != 0 {
		t.Fatal(cap, err)
	}
	// A new observation must not create another speculative output behind a full calendar.
	if _, err = s.ObserveTwitchSources(ctx, twitch.ObservationRequest{FlowID: alias.ID, FlowVersion: alias.Version, Sources: []twitch.Source{ready.Source}}, now); err != nil {
		t.Fatal(err)
	}
	var outputs int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM twitch_outputs WHERE format='short'`).Scan(&outputs); err != nil || outputs != 1 {
		t.Fatal("shared observer created extra render", outputs, err)
	}
	if err = s.AbortFlowImport(ctx, b.ImportID, b.UploadGeneration, "Клип удалён на Twitch", now); err != nil {
		t.Fatal(err)
	}
	cap, err = s.FlowCapacity(ctx, f.ID, now)
	if err != nil || cap.Formats[0].FreeSlots != 1 {
		t.Fatal("permanent failure retained preparation slot", cap, err)
	}
	if _, err = s.FinishFlowImport(ctx, b.ImportID, b.UploadGeneration, &w.VerifiedMedia{Original: w.ObjectRef{Bucket: "imports", Object: "late.mp4"}, SHA256: "late", DurationSec: 20}, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("late upload revived aborted generation", err)
	}

	// One compilation slot owns a bounded batch, rather than an unlimited pool.
	comp := flowFixture(t, s, flowAccount(t, s, "UC-comp-budget"), true, now)
	comp.ShortsEnabled = false
	comp.ScheduleHorizonHours = 4
	comp.MaxSourceAgeHours = 3
	comp.CompilationSlots = []string{"09:30"}
	comp.CompilationMinDurationSec = 50
	comp.CompilationMaxDurationSec = 63
	comp.CompilationMaxClips = 3
	comp, err = s.SaveTwitchFlow(ctx, comp, false, now)
	if err != nil {
		t.Fatal(err)
	}
	compIDs := []string{}
	for n, slug := range []string{"batch-one", "batch-two", "batch-three"} {
		src := flowSource(slug, "123", 20, now)
		src.CreatedAt = now.Add(-time.Hour)
		v := reserveFlow(t, s, comp, src, now)
		compIDs = append(compIDs, v.ImportID)
		finishFlow(t, s, v, 20, "", now.Add(time.Duration(n)*time.Second))
	}
	extra := flowSource("batch-overflow", "123", 20, now)
	extra.CreatedAt = now.Add(-time.Hour)
	if _, err = s.ReserveFlowImport(ctx, twitch.ImportRequest{SchemaVersion: 2, IdempotencyKey: "overflow", FlowID: comp.ID, FlowVersion: comp.Version, Source: extra}, now); !errors.As(err, &waiting) || waiting.Code != "calendar_full" {
		t.Fatal("compilation preparation exceeded count/duration budget", err)
	}
	result, err := s.AutoFlowCompilation(ctx, comp.ID, now)
	if err != nil || !result.Created {
		t.Fatal(result, err)
	}
	completeRender(t, s, result.Compilation.RevisionID, now, nil)
	if _, err = s.pool.Exec(ctx, `UPDATE twitch_imports SET request=jsonb_set(request,'{source,created_at}',to_jsonb($2::timestamptz)) WHERE id=$1`, compIDs[0], now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = s.scheduleFlowOutputs(ctx, now); err != nil {
		t.Fatal(err)
	}
	var planned int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM publications WHERE clip_id=$1`, result.Compilation.ClipID).Scan(&planned); err != nil || planned != 0 {
		t.Fatal("compilation ignored its oldest source deadline", planned, err)
	}
}

func TestTwitchPolicyFencesOldPlansAndFirstUploadRechecksAge(t *testing.T) {
	s, account, _ := twitchFixture(t)
	ctx := context.Background()
	day := time.Now().UTC()
	now := time.Date(day.Year(), day.Month(), day.Day(), 8, 0, 0, 0, time.UTC)
	f := flowFixture(t, s, account, true, now)
	f.CompilationsEnabled = false
	f.DailyShorts = 5
	var err error
	f, err = s.SaveTwitchFlow(ctx, f, false, now)
	if err != nil {
		t.Fatal(err)
	}
	makePlan := func(slug string, at time.Time, automatic bool) (twitch.Import, domain.PublicationRecord) {
		im := finishFlow(t, s, reserveFlow(t, s, f, flowSource(slug, "123", 20, now), now), 20, "", now)
		completeRender(t, s, im.RevisionID, now, nil)
		if err := s.ApproveRevision(ctx, im.ClipID, now); err != nil {
			t.Fatal(err)
		}
		var id int64
		// Match queue readiness to the fixture clock instead of PostgreSQL's
		// DEFAULT now(), which may already be later than the test's 08:00 UTC.
		if err := s.pool.QueryRow(ctx, `UPDATE publications SET publish_at=$2,next_attempt_at=$2,auto_flow_id=CASE WHEN $3 THEN $4 ELSE NULL END WHERE clip_id=$1 RETURNING id`, im.ClipID, at, automatic, f.ID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		p, err := s.GetPublicationRecord(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return im, p
	}
	_, late := makePlan("late-auto", now.Add(14*24*time.Hour), true)
	_, manual := makePlan("late-manual", now.Add(13*24*time.Hour), false)
	_, started := makePlan("started-auto", now.Add(12*24*time.Hour), true)
	if _, err = s.pool.Exec(ctx, `UPDATE publications SET started_at=$2,status='needs_attention' WHERE id=$1`, started.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO publication_attempts(id,publication_id,publication_version,status,lease_until,lease_token)VALUES('late-attempt',$1,$2,'active',$3,'old-token')`, late.ID, late.Version, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	out, err := s.ApplyTwitchPolicy(ctx, now)
	if err != nil || out.Held != 1 {
		t.Fatal(out, err)
	}
	held, err := s.GetPublicationRecord(ctx, late.ID)
	if err != nil || held.Status != "on_hold" || held.Version <= late.Version {
		t.Fatal(held, err)
	}
	unchanged, err := s.GetPublicationRecord(ctx, manual.ID)
	if err != nil || unchanged.Status != "scheduled" {
		t.Fatal("manual plan cancelled automatically", unchanged, err)
	}
	protected, err := s.GetPublicationRecord(ctx, started.ID)
	if err != nil || protected.Status != "needs_attention" {
		t.Fatal("ambiguous upload modified", protected, err)
	}
	accepted, err := s.CompleteWorkflow(ctx, &w.CompleteRequest{LeaseRequest: w.LeaseRequest{AttemptID: "late-attempt", LeaseToken: "old-token", ExpectedVersion: late.Version}, Checkpoint: w.Checkpoint{State: "published", RemoteID: "old"}}, now)
	if err != nil || accepted {
		t.Fatal("late worker unfenced", accepted, err)
	}
	if err = s.PatchPublication(ctx, manual.ID, domain.PublicationPatch{Version: manual.Version, FreshnessOverride: true}); err != nil {
		t.Fatal("explicit exact revision/time exception rejected", err)
	}
	manual, err = s.GetPublicationRecord(ctx, manual.ID)
	if err != nil || !manual.FreshnessOverride {
		t.Fatal(manual, err)
	}
	newAt := manual.PublishAt.Add(time.Hour)
	if err = s.PatchPublication(ctx, manual.ID, domain.PublicationPatch{Version: manual.Version, PublishAt: &newAt}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("changed date retained stale exception", err)
	}

	// A fresh queued upload may become stale while the worker downloads the file.
	im, p := makePlan("delayed-start", now, true)
	if _, err = s.pool.Exec(ctx, `UPDATE twitch_flows SET config=config||'{"max_source_age_hours":3}'::jsonb WHERE id=$1`, f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE twitch_imports SET request=jsonb_set(request,'{source,created_at}',to_jsonb($2::timestamptz)) WHERE id=$1`, im.ImportID, now.Add(-3*time.Hour+30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = s.PreparePublications(ctx, now, 50); err != nil {
		t.Fatal(err)
	}
	var attempt string
	if err = s.pool.QueryRow(ctx, `SELECT id FROM publication_attempts WHERE publication_id=$1 AND status='active'`, p.ID).Scan(&attempt); err != nil {
		t.Fatal(err)
	}
	lease, err := s.ClaimWorkflow(ctx, &w.ClaimRequest{Command: w.Command{Kind: w.KindPublish, PublicationID: p.ID, AttemptID: attempt, ExpectedVersion: p.Version}, WorkerID: "slow"}, now)
	if err != nil || !lease.Granted {
		t.Fatal(lease, err)
	}
	accepted, err = s.CheckpointWorkflow(ctx, &w.CheckpointRequest{LeaseRequest: w.LeaseRequest{AttemptID: attempt, LeaseToken: lease.LeaseToken, ExpectedVersion: p.Version}, Checkpoint: w.Checkpoint{SessionURI: "https://upload.example/session", State: "uploading"}}, now.Add(31*time.Second))
	if err != nil || accepted {
		t.Fatal("first upload checkpoint accepted stale source", im, accepted, err)
	}
	stopped, err := s.GetPublicationRecord(ctx, p.ID)
	if err != nil || stopped.Status != "on_hold" || stopped.StartedAt != nil {
		t.Fatal("expired preupload failed to release capacity", stopped, err)
	}
}

func TestHeldTwitchPublicationResumesOnlyWithExactPolicyException(t *testing.T) {
	s, account, _ := twitchFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	f := flowFixture(t, s, account, false, now)
	im := finishFlow(t, s, reserveFlow(t, s, f, flowSource("expired-manual-release", "123", 20, now), now), 20, "", now)
	completeRender(t, s, im.RevisionID, now, nil)
	if err := s.TickTwitchFlows(ctx, now); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := s.pool.QueryRow(ctx, `SELECT id FROM publications WHERE clip_id=$1`, im.ClipID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	// Keep valid, ready files but reproduce the expiry hold from the live calendar.
	if _, err := s.pool.Exec(ctx, `UPDATE twitch_imports SET request=jsonb_set(request,'{source,created_at}',to_jsonb($2::timestamptz)) WHERE id=$1`, im.ImportID, now.Add(-100*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE twitch_policy_settings SET mode='enforce'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE publications SET policy_hold_reason='source_stale',auto_flow_id=$2,auto_hold=true WHERE id=$1`, id, f.ID); err != nil {
		t.Fatal(err)
	}
	get := func() domain.PublicationRecord {
		t.Helper()
		p, err := s.GetPublicationRecord(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := get()
	at := now.Add(time.Hour)
	if err := s.PatchPublication(ctx, id, domain.PublicationPatch{Version: p.Version, PublishAt: &at}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("expired held publication resumed without confirmation", err)
	}
	if err := s.PatchPublication(ctx, id, domain.PublicationPatch{Version: p.Version, PublishAt: &at, FreshnessOverride: true}); err != nil {
		t.Fatal(err)
	}
	p = get()
	if p.Status != "scheduled" || !p.FreshnessOverride || !p.PublishAt.Equal(at) {
		t.Fatal("confirmed held schedule still requires approval", p)
	}
	events, err := s.PublicationEvents(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind == "freshness_override" && event.State != "scheduled" {
			t.Fatal("confirmation event still reports a hold", event)
		}
	}
	if err := s.ActPublicationWithOverride(ctx, id, "publish-now", p.Version, false, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("saved-time exception silently authorized a new time", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE publications SET status='on_hold',policy_hold_reason='source_stale' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := s.ActPublicationWithOverride(ctx, id, "publish-now", p.Version, true, now); err != nil {
		t.Fatal("held immediate upload failed", err)
	}
	p = get()
	if p.Status != "scheduled" || !p.FreshnessOverride || !p.PublishAt.Equal(now) {
		t.Fatal("immediate upload stayed held", p)
	}
	// Older releases may have a confirmed exception and empty hold reason while
	// remaining on_hold. They must recover through the same explicit action.
	if _, err := s.pool.Exec(ctx, `UPDATE publications SET status='on_hold',policy_hold_reason='' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := s.ActPublicationWithOverride(ctx, id, "publish-now", p.Version, true, now.Add(time.Second)); err != nil {
		t.Fatal("previously confirmed hold failed to recover", err)
	}
	p = get()
	// A freshness exception is not permission to ignore another hold or missing
	// files. Both failures must leave the existing version and hold intact.
	if _, err := s.pool.Exec(ctx, `UPDATE publications SET status='on_hold',policy_hold_reason='source_failed' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := s.ActPublicationWithOverride(ctx, id, "publish-now", p.Version, true, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("unrelated hold was bypassed", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE publications SET policy_hold_reason='source_stale' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE clip_revisions SET storage_state='expired' WHERE id=$1`, im.RevisionID); err != nil {
		t.Fatal(err)
	}
	if err := s.ActPublicationWithOverride(ctx, id, "publish-now", p.Version, true, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("deleted file was accepted with freshness exception", err)
	}
	if held := get(); held.Status != "on_hold" || held.Version != p.Version {
		t.Fatal("rejected action modified the held release", held)
	}
}
