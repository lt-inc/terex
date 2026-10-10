package postgres

import (
	"context"
	"testing"
	"time"
)

func TestMonitoringDoesNotWaitForAdmissionAndRejectsOldEpoch(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	var epoch int64
	if err := s.pool.QueryRow(ctx, `SELECT epoch FROM twitch_reset_control WHERE id=1`).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	busy, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Rollback(ctx)
	if err := lockTwitch(ctx, busy); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := s.SaveTwitchMonitoring(bounded, "system", "current", epoch, time.Now(), map[string]bool{"available": true}); err != nil {
		t.Fatalf("monitoring must not queue behind admission: %v", err)
	}
	if err := s.CleanupTwitchMonitoring(bounded, epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := busy.Exec(ctx, `UPDATE twitch_reset_control SET epoch=epoch+1,active=true WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := busy.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTwitchMonitoring(ctx, "system", "current", epoch, time.Now(), map[string]bool{"available": false}); err == nil {
		t.Fatal("old epoch must not overwrite the snapshot after reset begins")
	}
	if err := s.CleanupTwitchMonitoring(ctx, epoch+1); err == nil {
		t.Fatal("snapshot cleanup must be fenced while reset is active")
	}
}
