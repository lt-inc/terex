package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"strings"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
	"time"
)

func flowDestinationKey(f twitch.Flow) string {
	if f.DestinationKey != "" {
		return f.DestinationKey
	}
	return f.YouTubeChannelID
}
func destinationConnected(ctx context.Context, tx pgx.Tx, d twitch.Destination) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE id=$1 AND platform=$2 AND connection_state='connected' AND remote_account_id=$3)`, d.AccountID, d.Provider, d.RemoteAccountID).Scan(&ok)
	return ok, err
}

func destinationFlow(f twitch.Flow, d twitch.Destination) twitch.Flow {
	f.AccountID = d.AccountID
	f.DestinationKey = d.CapacityKey()
	f.YouTubeChannelID = ""
	if d.Provider == "youtube" {
		f.YouTubeChannelID = d.RemoteAccountID
	}
	f.AutoPublish = d.AutoPublish
	f.Timezone = d.Timezone
	f.ShortsSlots = d.ShortsSlots
	f.DailyShorts = d.DailyShorts
	f.ScheduleHorizonHours = d.ScheduleHorizonHours
	if d.Provider == "tiktok" {
		f.CompilationsEnabled = false
	}
	return f
}

func normalizeDestinationsTx(ctx context.Context, tx pgx.Tx, f *twitch.Flow) error {
	ds := f.PublicationDestinations()
	seen := map[string]bool{}
	if len(ds) > 2 {
		return fmt.Errorf("%w: maximum one YouTube and one TikTok destination", domain.ErrInvalid)
	}
	for i := range ds {
		d := &ds[i]
		if (d.Provider != "youtube" && d.Provider != "tiktok") || seen[d.Provider] || d.AccountID <= 0 {
			return fmt.Errorf("%w: select at most one account per platform", domain.ErrInvalid)
		}
		seen[d.Provider] = true
		var provider, remote, state string
		if err := tx.QueryRow(ctx, `SELECT platform,COALESCE(remote_account_id,''),connection_state FROM accounts WHERE id=$1`, d.AccountID).Scan(&provider, &remote, &state); err != nil {
			return mapErr(err)
		}
		if provider != d.Provider || remote == "" || (f.Enabled && d.Enabled && state != "connected") || (d.RemoteAccountID != "" && d.RemoteAccountID != remote) {
			return fmt.Errorf("%w: connect and confirm the destination account", domain.ErrConflict)
		}
		d.RemoteAccountID = remote
		d.ID = f.ID + ":" + provider
		if d.Provider == "tiktok" && d.Visibility == "" {
			return fmt.Errorf("%w: explicitly choose TikTok public or private visibility", domain.ErrInvalid)
		}
		if d.Timezone == "" {
			d.Timezone = f.Timezone
		}
		if d.DailyShorts == 0 {
			d.DailyShorts = 3
		}
		if d.ScheduleHorizonHours == 0 {
			d.ScheduleHorizonHours = 48
		}
		if d.Visibility == "" {
			d.Visibility = "public"
		}
		if len(d.ShortsSlots) == 0 {
			d.ShortsSlots = f.ShortsSlots
		}
		if _, err := time.LoadLocation(d.Timezone); err != nil {
			return domain.ErrInvalid
		}
		if d.DailyShorts < 1 || d.DailyShorts > 100 || d.ScheduleHorizonHours < 1 || d.ScheduleHorizonHours > 168 || (d.Visibility != "public" && d.Visibility != "private") {
			return domain.ErrInvalid
		}
		for _, slot := range d.ShortsSlots {
			if _, err := time.Parse("15:04", slot); err != nil {
				return domain.ErrInvalid
			}
		}
		if d.AutoPublish && len(d.ShortsSlots) == 0 {
			return fmt.Errorf("%w: destination requires publication slots", domain.ErrInvalid)
		}
	}
	if len(ds) == 0 && (f.Enabled || f.AutoPublish) {
		return fmt.Errorf("%w: choose a YouTube or TikTok account", domain.ErrInvalid)
	}
	f.Destinations = ds
	if len(ds) > 0 {
		primary := ds[0]
		for _, d := range ds {
			if d.Provider == "youtube" {
				primary = d
				break
			}
		}
		f.AccountID = primary.AccountID
		f.DestinationKey = primary.CapacityKey()
		f.YouTubeChannelID = ""
		if primary.Provider == "youtube" {
			f.YouTubeChannelID = primary.RemoteAccountID
		}
		f.Timezone = primary.Timezone
		f.DailyShorts = primary.DailyShorts
		f.ScheduleHorizonHours = primary.ScheduleHorizonHours
		f.ShortsSlots = primary.ShortsSlots
		f.AutoPublish = false
		for _, d := range ds {
			f.AutoPublish = f.AutoPublish || (d.Enabled && d.AutoPublish)
		}
		if !seen["youtube"] {
			f.CompilationsEnabled = false
		}
	}
	return nil
}

func saveDestinationsTx(ctx context.Context, tx pgx.Tx, f twitch.Flow) error {
	if _, err := tx.Exec(ctx, `UPDATE twitch_flow_destinations SET enabled=FALSE WHERE flow_id=$1`, f.ID); err != nil {
		return err
	}
	for _, d := range f.Destinations {
		if _, err := tx.Exec(ctx, `INSERT INTO twitch_flow_destinations(id,flow_id,provider,account_id,remote_account_id,destination_key,config,enabled)VALUES($1,$2,$3,$4,$5,$6,$7,$8)ON CONFLICT(flow_id,provider)DO UPDATE SET account_id=EXCLUDED.account_id,remote_account_id=EXCLUDED.remote_account_id,destination_key=EXCLUDED.destination_key,config=EXCLUDED.config,enabled=EXCLUDED.enabled`, f.ID+":"+d.Provider, f.ID, d.Provider, d.AccountID, d.RemoteAccountID, d.CapacityKey(), mustJSON(d), d.Enabled); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO twitch_channel_limits(youtube_channel_id,timezone,daily_shorts,daily_compilations,schedule_horizon_hours)VALUES($1,$2,$3,$4,$5)ON CONFLICT(youtube_channel_id)DO UPDATE SET timezone=EXCLUDED.timezone,daily_shorts=EXCLUDED.daily_shorts,daily_compilations=EXCLUDED.daily_compilations,schedule_horizon_hours=EXCLUDED.schedule_horizon_hours`, d.CapacityKey(), d.Timezone, d.DailyShorts, f.DailyCompilations, d.ScheduleHorizonHours); err != nil {
			return err
		}
		if !d.Enabled || !d.AutoPublish {
			if _, err := tx.Exec(ctx, `UPDATE publications p SET status='on_hold',auto_hold=p.auto_flow_id IS NOT NULL,policy_hold_reason='destination_paused',version=version+1,error='Назначение потока выключено' WHERE p.destination_key=$1 AND p.started_at IS NULL AND p.status NOT IN('published','cancelled','needs_attention') AND ($3=FALSE OR p.auto_flow_id IS NOT NULL) AND EXISTS(SELECT 1 FROM twitch_outputs o WHERE o.clip_id=p.clip_id AND o.flow_id=$2)`, d.CapacityKey(), f.ID, d.Enabled); err != nil {
				return err
			}
		}
	}
	_, err := tx.Exec(ctx, `UPDATE twitch_flows SET destination_key=$2,config=$3 WHERE id=$1`, f.ID, flowDestinationKey(f), mustJSON(f))
	return err
}

// Only snapshots present when the output is created receive this media. Adding
// a destination later never backfills a historical calendar.
func snapshotOutputDestinations(ctx context.Context, tx pgx.Tx, clip string, f twitch.Flow, format string, auto bool, now time.Time) error {
	for _, d := range f.PublicationDestinations() {
		if !d.Enabled || (d.Provider == "tiktok" && format != "short") {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO twitch_output_destinations(clip_id,destination_key,account_id,provider,snapshot,auto_eligible,created_at)VALUES($1,$2,$3,$4,$5,$6,$7)ON CONFLICT DO NOTHING`, clip, d.CapacityKey(), d.AccountID, d.Provider, mustJSON(d), auto, now); err != nil {
			return err
		}
		// Revisions target records are shared; per-target snapshots choose schedule.
		if _, err := tx.Exec(ctx, `INSERT INTO sub_job_targets(sub_job_id,account_id,timezone)SELECT s.sub_job_id,$2,$3 FROM clips c JOIN sub_jobs s ON s.job_id=c.job_id WHERE c.clip_id=$1 ON CONFLICT(sub_job_id,account_id)DO NOTHING`, clip, d.AccountID, d.Timezone); err != nil {
			return err
		}
	}
	return nil
}

func tikTokCaption(title string, tags []string) string {
	parts := []string{strings.TrimSpace(title)}
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.TrimSpace(strings.TrimPrefix(tag, "#"))
		if tag == "" || strings.EqualFold(tag, "shorts") || seen[strings.ToLower(tag)] {
			continue
		}
		seen[strings.ToLower(tag)] = true
		parts = append(parts, "#"+tag)
	}
	return strings.Join(parts, " ")
}

func decodeDestination(data []byte) (twitch.Destination, error) {
	var d twitch.Destination
	err := json.Unmarshal(data, &d)
	return d, err
}
