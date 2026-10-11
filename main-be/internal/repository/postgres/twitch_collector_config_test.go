package postgres

import (
	"context"
	"testing"
	"time"

	twitchservice "terex/main-be/internal/service/twitch"
	"terex/proto/twitch"
)

func TestCollectorConfigKeepsLiveAccessExclusionsAndResetFence(t *testing.T) {
	s, account, _ := twitchFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	f := flowFixture(t, s, account, false, now)
	sources, err := s.ObserveTwitchSources(ctx, twitch.ObservationRequest{FlowID: f.ID, FlowVersion: f.Version, Sources: []twitch.Source{flowSource("config-excluded", "123", 20, now)}}, now)
	if err != nil || len(sources) != 1 {
		t.Fatal(sources, err)
	}
	if err = s.ActFlowSource(ctx, f.ID, sources[0].ImportID, "exclude", true, now); err != nil {
		t.Fatal(err)
	}
	service := twitchservice.Service{Repo: s}
	readFlow := func() twitch.Flow {
		t.Helper()
		cfg, e := service.CollectorConfig(ctx)
		if e != nil {
			t.Fatal(e)
		}
		for _, item := range cfg.Flows {
			if item.ID == f.ID {
				return item
			}
		}
		t.Fatal("flow missing from collector config")
		return twitch.Flow{}
	}
	control := readFlow()
	if !control.Enabled || control.Version != f.Version || control.AccountID != f.AccountID || control.YouTubeChannelID != f.YouTubeChannelID || len(control.ExcludedSourceIDs) != 1 || control.ExcludedSourceIDs[0] != "config-excluded" {
		t.Fatal("collector lost control settings", control)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE accounts SET connection_state='disconnected' WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	control = readFlow()
	if control.Enabled || control.BlockReason == "" {
		t.Fatal("disconnected account allowed collector work", control)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO twitch_reset_runs(id,epoch,state,counts,created_at,updated_at) VALUES('config-reset',1,'failed','{}',$1,$1)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE twitch_reset_control SET epoch=1,active=true,reset_id='config-reset' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	cfg, err := service.CollectorConfig(ctx)
	if err != nil || cfg.Epoch != 1 || cfg.Maintenance == nil || cfg.Maintenance.ID != "config-reset" || cfg.Maintenance.State != "failed" {
		t.Fatal("failed reset fence lost", cfg, err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE twitch_reset_runs SET state='completed'; UPDATE twitch_reset_control SET active=false`); err != nil {
		t.Fatal(err)
	}
	cfg, err = service.CollectorConfig(ctx)
	if err != nil || cfg.Epoch != 1 || cfg.Maintenance != nil {
		t.Fatal("completed reset did not release maintenance", cfg, err)
	}
}
