package postgres

import (
	"context"
	"errors"
	"fmt"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
	"testing"
	"time"
)

func TestPreparationBudgetBorrowsOnlyWhenOtherFormatHasNoDemand(t *testing.T) {
	comp := 0
	for used := 0; used < 10; used++ {
		if allocationFormat(comp, used, 70, true, true) == "compilation" {
			comp++
		}
	}
	if comp != 7 {
		t.Fatalf("balanced budget: %d compilation grants", comp)
	}
	if allocationFormat(7, 10, 70, true, false) != "compilation" || allocationFormat(0, 0, 100, false, true) != "short" {
		t.Fatal("unused share was not borrowed")
	}
}

func TestCompilationReadsAllReadySourcesAndCurrentLanguage(t *testing.T) {
	s, account, _ := twitchFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Hour)
	f := flowFixture(t, s, account, true, now)
	f.ShortsEnabled = false
	f.Language = "ru"
	f.CompilationHour = 23
	f.CompilationMinDurationSec = 60
	f.CompilationMaxDurationSec = 70
	f.CompilationMaxClips = 3
	var err error
	f, err = s.SaveTwitchFlow(ctx, f, false, now)
	if err != nil {
		t.Fatal(err)
	}
	ready := []twitch.Import{}
	for i := 0; i < 3; i++ {
		src := flowSource(fmt.Sprintf("outside-batch-%d", i), "123", 20, now)
		ready = append(ready, finishFlow(t, s, reserveFlow(t, s, f, src, now), 20, "", now))
	}
	if _, err = s.ApplyTwitchPolicy(ctx, now); err != nil {
		t.Fatal(err)
	}
	// One pending batch was the former search boundary. Ready media outside it
	// must still satisfy the compilation without another physical download.
	pending := reserveFlow(t, s, f, flowSource("pending-oldest-batch", "456", 20, now), now)
	_ = pending
	inspected, err := s.InspectFlowCompilation(ctx, f.ID, now)
	if err != nil || inspected.SelectedClips != 3 || inspected.ReadyDurationSec != 62 {
		t.Fatal(inspected, err)
	}
	made, err := s.AutoFlowCompilation(ctx, f.ID, now)
	if err != nil || !made.Created {
		t.Fatal(made, err)
	}
	completeRender(t, s, made.Compilation.RevisionID, now, nil)
	if err = s.TickTwitchFlows(ctx, now); err != nil {
		t.Fatal(err)
	}
	rev, err := scanRevision(s.pool.QueryRow(ctx, `SELECT `+revisionCols+` FROM clip_revisions WHERE id=$1`, made.Compilation.RevisionID))
	if err != nil || rev.ApprovedAt == nil {
		t.Fatal("ready version was not auto-approved", rev, err)
	}
	foreign := flowSource("wrong-language-manual", "456", 20, now)
	foreign.Language = "en"
	observed, err := s.ObserveTwitchSources(ctx, twitch.ObservationRequest{FlowID: f.ID, FlowVersion: f.Version, Sources: []twitch.Source{foreign}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(observed) != 1 {
		t.Fatal(observed)
	}
	// Import the file through another matching flow; shared media remains usable
	// there, but cannot be smuggled into this ru assembly through a manual edit.
	other := flowFixture(t, s, flowAccount(t, s, "UC-en"), false, now)
	other.ShortsEnabled = false
	other.Language = "en"
	other, err = s.SaveTwitchFlow(ctx, other, false, now)
	if err != nil {
		t.Fatal(err)
	}
	imported := finishFlow(t, s, reserveFlow(t, s, other, foreign, now), 20, "", now)
	req := twitch.CompilationRequest{FlowID: f.ID, Title: "Wrong language", Items: []twitch.CompilationItemRequest{{SourceID: ready[0].ImportID}, {SourceID: ready[1].ImportID}, {SourceID: imported.ImportID}}}
	if _, err = s.UpdateCompilation(ctx, made.Compilation.ClipID, domain.CompilationUpdate{ExpectedVersion: made.Compilation.Version, CompilationRequest: req}, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("manual language bypass", err)
	}
	// No approve action is needed in manual publication mode either.
	manual := flowFixture(t, s, flowAccount(t, s, "UC-manual-calendar"), false, now)
	manual.CompilationsEnabled = false
	manual, err = s.SaveTwitchFlow(ctx, manual, false, now)
	if err != nil {
		t.Fatal(err)
	}
	v := finishFlow(t, s, reserveFlow(t, s, manual, flowSource("manual-autoapproved", "123", 20, now), now), 20, "", now)
	completeRender(t, s, v.RevisionID, now, nil)
	if err = s.TickTwitchFlows(ctx, now); err != nil {
		t.Fatal(err)
	}
	var pid int64
	var version int
	var status string
	if err = s.pool.QueryRow(ctx, `SELECT id,version,status FROM publications WHERE clip_id=$1`, v.ClipID).Scan(&pid, &version, &status); err != nil || status != "on_hold" {
		t.Fatal("manual calendar missing", status, err)
	}
	at := time.Now().UTC().Add(time.Hour)
	if err = s.PatchPublication(ctx, pid, domain.PublicationPatch{Version: version, PublishAt: &at}); err != nil {
		t.Fatal("manual date selection failed", err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT status FROM publications WHERE id=$1`, pid).Scan(&status); err != nil || status != "scheduled" {
		t.Fatal("date selection still requires approval", status, err)
	}
	// Diagnostic drilldowns retain discovered metadata, but separate a channel
	// language hint from the clip language and use the upcoming release's age.
	hint := flowSource("language-channel-hint", "123", 20, now)
	hint.Language, hint.LanguageSource = "en", "channel"
	staleAtTarget := flowSource("stale-at-target", "123", 20, now)
	staleAtTarget.CreatedAt = now.Add(-71 * time.Hour)
	if _, err = s.ObserveTwitchSources(ctx, twitch.ObservationRequest{FlowID: f.ID, FlowVersion: f.Version, Sources: []twitch.Source{hint, staleAtTarget}}, now); err != nil {
		t.Fatal(err)
	}
	f.BroadcasterIDs = []string{"123"}
	f, err = s.SaveTwitchFlow(ctx, f, false, now)
	if err != nil {
		t.Fatal(err)
	}
	for state, clip := range map[string]string{"language_mismatch": foreign.ClipID, "language_unknown": hint.ClipID, "source_mismatch": pending.Source.ClipID, "filtered": pending.Source.ClipID} {
		page, err := s.ListFlowClips(ctx, f.ID, domain.FlowClipQuery{State: state, ShowFiltered: true, Format: "compilation"})
		if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Source.ClipID != clip {
			t.Fatal("incorrect diagnostic drilldown", state, page, err)
		}
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO twitch_preparation_reservations(id,flow_id,youtube_channel_id,format,slot_at,expires_at)VALUES('diagnostic-age-target',$1,$2,'compilation',$3,$4)`, f.ID, f.YouTubeChannelID, now.Add(2*time.Hour+7*time.Minute), now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListFlowClips(ctx, f.ID, domain.FlowClipQuery{State: "age_filtered", ShowFiltered: true, Format: "compilation"})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Source.ClipID != staleAtTarget.ClipID || page.Items[0].SourceState != "filtered" {
		t.Fatal("publication target age was ignored", page, err)
	}
}

func TestAllocatorPersistsDistinctWeightedGrantsAndAcceptsImports(t *testing.T) {
	s, account, settings := twitchFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Hour)
	settings.DailyDownloads = 10
	settings.PerChannelDailyDownloads = 10
	settings.AdmissionMode = "enforce"
	settings.CompilationBudgetPercent = 70
	if err := s.SaveTwitchSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	comp := flowFixture(t, s, account, true, now)
	comp.ShortsEnabled = false
	comp.CompilationMinDurationSec = 300
	comp.CompilationMaxDurationSec = 600
	comp.CompilationMaxClips = 30
	var err error
	comp, err = s.SaveTwitchFlow(ctx, comp, false, now)
	if err != nil {
		t.Fatal(err)
	}
	short := flowFixture(t, s, flowAccount(t, s, "UC-shorts"), true, now)
	short.CompilationsEnabled = false
	short.DailyShorts = 10
	short, err = s.SaveTwitchFlow(ctx, short, false, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, flow := range []twitch.Flow{comp, short} {
		sources := []twitch.Source{}
		for i := 0; i < 12; i++ {
			sources = append(sources, flowSource(fmt.Sprintf("%s-candidate-%d", flow.ID, i), "123", 30, now))
		}
		if _, err = s.ObserveTwitchSources(ctx, twitch.ObservationRequest{FlowID: flow.ID, FlowVersion: flow.Version, Sources: sources}, now); err != nil {
			t.Fatal(err)
		}
	}
	grants, err := s.AllocateTwitchDownloads(ctx, twitch.AdmissionRequest{Limit: 10}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants.Grants) != 10 || grants.DailyRemaining != 0 {
		t.Fatal(grants)
	}
	n := 0
	seen := map[string]bool{}
	for _, g := range grants.Grants {
		if seen[g.ClipID] {
			t.Fatal("duplicate grant")
		}
		seen[g.ClipID] = true
		if g.Format == "compilation" {
			n++
		}
	}
	if n != 7 {
		t.Fatal("weighted distribution", n, grants)
	}
	diagnostics, err := s.TwitchFlowDiagnostics(ctx, comp.ID, now)
	if err != nil || diagnostics.Compilation.ReadyDurationSec != 0 {
		t.Fatal("diagnostics included pending as ready", diagnostics, err)
	}
	var afterDiagnostics int
	if err = s.pool.QueryRow(ctx, `SELECT count(*)FROM twitch_download_grants`).Scan(&afterDiagnostics); err != nil || afterDiagnostics != 10 {
		t.Fatal("diagnostics mutated grants", afterDiagnostics, err)
	}
	repeat, err := s.AllocateTwitchDownloads(ctx, twitch.AdmissionRequest{Limit: 10}, now)
	if err != nil || len(repeat.Grants) != 10 {
		t.Fatal("durable redelivery", repeat, err)
	}
	first := grants.Grants[0]
	if _, err = s.ReserveFlowImport(ctx, twitch.ImportRequest{SchemaVersion: 2, IdempotencyKey: "admitted:" + first.ClipID, FlowID: first.FlowID, FlowVersion: first.FlowVersion, Source: first.Source}, now); err != nil {
		t.Fatal("issued grant rejected by import", err)
	}
	var count int
	if err = s.pool.QueryRow(ctx, `SELECT count(*)FROM twitch_download_grants`).Scan(&count); err != nil || count != 10 {
		t.Fatal("repeat charged quota", count, err)
	}
	// A full Shorts calendar makes its unused share available to compilations,
	// even when Shorts remain enabled and their target share is 100%.
	settings.DailyDownloads, settings.PerChannelDailyDownloads = 20, 20
	settings.CompilationBudgetPercent = 0
	if err = s.SaveTwitchSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	slots, err := calendarSlotsTx(ctx, tx, short, "short", 0, "", now)
	if err != nil {
		t.Fatal(err)
	}
	var shortImport string
	for _, grant := range grants.Grants {
		if grant.Format == "short" {
			shortImport = grant.ImportID
			break
		}
	}
	for n, slot := range slots {
		if slot.Occupied || slot.Preparing {
			continue
		}
		id := fmt.Sprintf("calendar-full-%d", n)
		if _, err = tx.Exec(ctx, `INSERT INTO twitch_preparation_reservations(id,flow_id,youtube_channel_id,format,slot_at,expires_at)VALUES($1,$2,$3,'short',$4,$5)`, id, short.ID, short.YouTubeChannelID, slot.At, slot.At.Add(15*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO twitch_preparation_sources(reservation_id,import_id,flow_id,duration_sec)VALUES($1,$2,$3,30)`, id, shortImport, short.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	borrowed, err := s.AllocateTwitchDownloads(ctx, twitch.AdmissionRequest{Limit: 100}, now)
	if err != nil {
		t.Fatal(err)
	}
	newGrants := 0
	for _, grant := range borrowed.Grants {
		if seen[grant.ClipID] {
			continue
		}
		newGrants++
		if grant.Format != "compilation" {
			t.Fatal("full calendar still received new Shorts", grant)
		}
	}
	if newGrants == 0 {
		t.Fatal("full Shorts calendar prevented unused quota borrowing")
	}
}
