package postgres

import (
	"context"
	"terex/proto/twitch"
	"testing"
	"time"
)

func TestCollectorPressureChecksExactAssignmentWithoutAdmissionLock(t *testing.T) {
	s, account, settings := twitchFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Hour)
	settings.AdmissionMode = "enforce"
	if err := s.SaveTwitchSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	f := flowFixture(t, s, account, true, now)
	observed, err := s.ObserveTwitchSources(ctx, twitch.ObservationRequest{FlowID: f.ID, FlowVersion: f.Version, Sources: []twitch.Source{flowSource("pressure-source", "123", 20, now)}}, now)
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.AllocateTwitchDownloads(ctx, twitch.AdmissionRequest{Limit: 1}, now)
	if err != nil || len(g.Grants) != 1 {
		t.Fatal(g, err)
	}
	ref := twitch.PressureTaskRef{ClipID: g.Grants[0].ClipID, ImportID: observed[0].ImportID, FlowID: f.ID, AdmissionSequence: g.Grants[0].AdmissionSequence}
	busy, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Rollback(ctx)
	if err = lockTwitch(ctx, busy); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := s.TwitchCollectorPressure(bounded, twitch.PressureRequest{Tasks: []twitch.PressureTaskRef{ref}}, now)
	if err != nil || len(out.Tasks) != 1 || out.Tasks[0].GrantState != "valid" || out.Tasks[0].ExpiresAt == nil {
		t.Fatalf("pressure blocked or dropped exact grant: %+v %v", out, err)
	}
	ref.AdmissionSequence++
	out, err = s.TwitchCollectorPressure(bounded, twitch.PressureRequest{Tasks: []twitch.PressureTaskRef{ref}}, now)
	if err != nil || out.Tasks[0].GrantState != "mismatch" {
		t.Fatal(out, err)
	}
	if err = busy.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE twitch_reset_control SET epoch=epoch+1,active=true WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	out, err = s.TwitchCollectorPressure(ctx, twitch.PressureRequest{Tasks: []twitch.PressureTaskRef{ref}}, now)
	if err != nil || !out.Maintenance || out.Tasks[0].ReasonCode != "twitch_reset" {
		t.Fatal(out, err)
	}
}
