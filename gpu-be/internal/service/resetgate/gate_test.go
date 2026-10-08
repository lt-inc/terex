package resetgate

import (
	"context"
	"strings"
	"testing"

	"terex/proto/workflow"
)

func TestResetWaitsForWriterExitAndFencesOldEpoch(t *testing.T) {
	g := New()
	ctx := context.Background()
	if _, _, err := g.Begin(ctx, 0); err == nil || !strings.Contains(err.Error(), "control_plane_not_synchronized") {
		t.Fatal("writer admitted before control-plane synchronization or missing reason", err)
	}
	if _, err := g.SetTwitchMaintenance(ctx, &workflow.TwitchMaintenanceRequest{Epoch: 0}); err != nil {
		t.Fatal(err)
	}
	writer, release, err := g.Begin(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	r, err := g.SetTwitchMaintenance(ctx, &workflow.TwitchMaintenanceRequest{Epoch: 1, Active: true})
	if err != nil || r.Drained || r.ActiveWriters != 1 {
		t.Fatalf("cancel mistaken for drained: %+v %v", r, err)
	}
	if writer.Err() == nil {
		t.Fatal("old GPU writer was not cancelled")
	}
	if _, err = g.SetTwitchMaintenance(ctx, &workflow.TwitchMaintenanceRequest{Epoch: 1}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = g.Begin(ctx, 1); err == nil || !strings.Contains(err.Error(), "writers_draining") {
		t.Fatal("gate reopened before cancelled writer exited or missing reason", err)
	}
	release()
	r, err = g.SetTwitchMaintenance(ctx, &workflow.TwitchMaintenanceRequest{Epoch: 1, Active: true})
	if err != nil || !r.Drained {
		t.Fatal(r, err)
	}
	if _, _, err = g.Begin(ctx, 1); err == nil {
		t.Fatal("maintenance writer admitted")
	}
	if _, err = g.SetTwitchMaintenance(ctx, &workflow.TwitchMaintenanceRequest{Epoch: 1}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = g.Begin(ctx, 0); err == nil {
		t.Fatal("old epoch resurrected after reset")
	}
	_, release, err = g.Begin(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestInactiveHeartbeatKeepsHealthyWritersOpen(t *testing.T) {
	g := New()
	ctx := context.Background()
	if _, err := g.SetTwitchMaintenance(ctx, &workflow.TwitchMaintenanceRequest{Epoch: 1}); err != nil {
		t.Fatal(err)
	}
	writer, release, err := g.Begin(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	r, err := g.SetTwitchMaintenance(ctx, &workflow.TwitchMaintenanceRequest{Epoch: 1})
	if err != nil || r.Drained || r.ActiveWriters != 1 {
		t.Fatalf("incorrect active writer count: %+v %v", r, err)
	}
	if writer.Err() != nil {
		t.Fatal("inactive heartbeat cancelled a healthy writer")
	}
	_, releaseNext, err := g.Begin(ctx, 1)
	if err != nil {
		t.Fatal("inactive heartbeat fenced normal work", err)
	}
	releaseNext()
}

func TestInactiveEpochChangeStillWaitsForCancelledWriters(t *testing.T) {
	g := New()
	ctx := context.Background()
	if _, err := g.SetTwitchMaintenance(ctx, &workflow.TwitchMaintenanceRequest{Epoch: 1}); err != nil {
		t.Fatal(err)
	}
	writer, release, err := g.Begin(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err = g.SetTwitchMaintenance(ctx, &workflow.TwitchMaintenanceRequest{Epoch: 2}); err != nil {
		t.Fatal(err)
	}
	if writer.Err() == nil {
		t.Fatal("epoch change did not cancel old writer")
	}
	if _, _, err = g.Begin(ctx, 2); err == nil {
		t.Fatal("epoch change reopened before old writer exited")
	}
	release()
	if _, err = g.SetTwitchMaintenance(ctx, &workflow.TwitchMaintenanceRequest{Epoch: 2}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = g.Begin(ctx, 1); err == nil || !strings.Contains(err.Error(), "epoch_mismatch") {
		t.Fatal("old epoch admitted or missing reason", err)
	}
	_, releaseNext, err := g.Begin(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	releaseNext()
}
