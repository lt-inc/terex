package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"terex/proto/twitch"
)

func TestUnlimitedDownloadsContinueAfterSpentCapAndKeepBroadcasterQuota(t *testing.T) {
	s, account, settings := twitchFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	settings.AdmissionMode = "enforce"
	settings.DailyDownloads, settings.PerChannelDailyDownloads = 1, 1
	if err := s.SaveTwitchSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	f := flowFixture(t, s, account, true, now)
	f.CompilationsEnabled = false
	var err error
	f, err = s.SaveTwitchFlow(ctx, f, false, now)
	if err != nil {
		t.Fatal(err)
	}
	sources := []twitch.Source{}
	for i := 0; i < 4; i++ {
		sources = append(sources, flowSource(fmt.Sprintf("unlimited-%d", i), "123", 30, now))
	}
	imports, err := s.ObserveTwitchSources(ctx, twitch.ObservationRequest{FlowID: f.ID, FlowVersion: f.Version, Sources: sources}, now)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.AllocateTwitchDownloads(ctx, twitch.AdmissionRequest{Limit: 10}, now)
	if err != nil || len(first.Grants) != 1 || first.DailyUnlimited || first.DailyRemaining != 0 {
		t.Fatal("capped admission", first, err)
	}
	g := first.Grants[0]
	if _, err = s.ReserveFlowImport(ctx, twitch.ImportRequest{SchemaVersion: 2, IdempotencyKey: "unlimited:" + g.ClipID, FlowID: g.FlowID, FlowVersion: g.FlowVersion, Source: g.Source}, now); err != nil {
		t.Fatal("capped granted import", err)
	}
	settings.DailyDownloads, settings.PerChannelDailyDownloads = 0, 3
	if err = s.SaveTwitchSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	next, err := s.AllocateTwitchDownloads(ctx, twitch.AdmissionRequest{Limit: 10}, now.Add(time.Second))
	if err != nil || len(next.Grants) != 2 || !next.DailyUnlimited {
		t.Fatal("disabled daily cap still blocks candidates or lost broadcaster quota", next, err)
	}
	for _, g = range next.Grants {
		if _, err = s.ReserveFlowImport(ctx, twitch.ImportRequest{SchemaVersion: 2, IdempotencyKey: "unlimited:" + g.ClipID, FlowID: g.FlowID, FlowVersion: g.FlowVersion, Source: g.Source}, now.Add(time.Second)); err != nil {
			t.Fatal("unlimited granted import", err)
		}
	}
	diagnostic, err := s.TwitchFlowDiagnostics(ctx, f.ID, now.Add(time.Second))
	if err != nil || !diagnostic.Collector.DailyUnlimited || diagnostic.Collector.DailyLimit != 0 || diagnostic.Collector.Spent != 3 {
		t.Fatal("unlimited diagnostics lost accounting", diagnostic.Collector, err)
	}
	refs := []twitch.PressureTaskRef{}
	for _, imported := range imports {
		refs = append(refs, twitch.PressureTaskRef{ClipID: imported.Source.ClipID, ImportID: imported.ImportID, FlowID: f.ID})
	}
	pressure, err := s.TwitchCollectorPressure(ctx, twitch.PressureRequest{Tasks: refs}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	broadcasterBlocked := false
	for _, task := range pressure.Tasks {
		if task.ReasonCode == "daily_download_limit" {
			t.Fatal("disabled daily cap reported as exhausted")
		}
		broadcasterBlocked = broadcasterBlocked || task.ReasonCode == "broadcaster_daily_limit"
	}
	if !broadcasterBlocked {
		t.Fatal("unlimited global cap disabled broadcaster protection")
	}
}
