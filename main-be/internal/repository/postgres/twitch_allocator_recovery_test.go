package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"terex/proto/twitch"
)

// Retained preparation slots must not hold sources forever after their unclaimed
// download grant expires, even if the calendar/expected compilation is full.
func TestAllocatorRenewsExistingPreparationAfterGrantExpiry(t *testing.T) {
	for _, format := range []string{"short", "compilation"} {
		t.Run(format, func(t *testing.T) {
			s, account, settings := twitchFixture(t)
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Hour)
			settings.AdmissionMode = "enforce"
			if err := s.SaveTwitchSettings(ctx, settings); err != nil {
				t.Fatal(err)
			}
			f := flowFixture(t, s, account, true, now)
			f.ShortsEnabled, f.CompilationsEnabled = format == "short", format == "compilation"
			f.ScheduleHorizonHours = 1
			f.ShortsSlots = []string{now.Add(time.Hour).Format("15:04")}
			f.CompilationSlots = f.ShortsSlots
			f.CompilationMinDurationSec, f.CompilationMaxDurationSec = 60, 70
			f.CompilationMinClips, f.CompilationMaxClips = 3, 3
			var err error
			f, err = s.SaveTwitchFlow(ctx, f, false, now)
			if err != nil {
				t.Fatal(err)
			}
			count := 1
			if format == "compilation" {
				count = 3
			}
			sources := []twitch.Source{}
			for i := 0; i < count; i++ {
				sources = append(sources, flowSource(fmt.Sprintf("recover-%s-%d", format, i), "123", 20, now))
			}
			if _, err = s.ObserveTwitchSources(ctx, twitch.ObservationRequest{FlowID: f.ID, FlowVersion: f.Version, Sources: sources}, now); err != nil {
				t.Fatal(err)
			}
			first, err := s.AllocateTwitchDownloads(ctx, twitch.AdmissionRequest{Limit: count}, now)
			if err != nil || len(first.Grants) != count {
				t.Fatal(first, err)
			}
			var originalReservations int
			if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM twitch_preparation_reservations WHERE state='active'`).Scan(&originalReservations); err != nil {
				t.Fatal(err)
			}
			// No file was accepted. The durable grant expires before its calendar
			// slot; the next admission must reuse that preparation reservation.
			again, err := s.AllocateTwitchDownloads(ctx, twitch.AdmissionRequest{Limit: count}, now.Add(16*time.Minute))
			if err != nil || len(again.Grants) != count {
				t.Fatalf("retained preparation stranded its sources: %+v %v", again, err)
			}
			for i, grant := range again.Grants {
				if grant.ClipID != first.Grants[i].ClipID || grant.Format != format || grant.AdmissionSequence <= first.Grants[i].AdmissionSequence {
					t.Fatal("recovery changed the source/format or reused stale fencing", first, again)
				}
			}
			var reservations int
			if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM twitch_preparation_reservations WHERE state='active'`).Scan(&reservations); err != nil || reservations != originalReservations {
				t.Fatal("recovery reserved more calendar space", reservations, originalReservations, err)
			}
		})
	}
}
