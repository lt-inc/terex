package resetgate

import (
	"context"
	"terex/proto/workflow"
	"testing"
)

func TestResetWaitsForWriterExitAndFencesOldEpoch(t *testing.T) {
	g := New()
	ctx := context.Background()
	if _, _, err := g.Begin(ctx, 0); err == nil {
		t.Fatal("writer admitted before control-plane synchronization")
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
