package postgres

import (
	"context"
	"encoding/json"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
	"time"
)

func (s *Store) SyncFlows(ctx context.Context, flows []twitch.Flow) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "UPDATE twitch_collector.flows SET enabled=false"); err != nil {
		return err
	}
	for _, f := range flows {
		raw, e := json.Marshal(f)
		if e != nil {
			return e
		}
		if _, err = tx.Exec(ctx, `INSERT INTO twitch_collector.flows(id,enabled,config) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET enabled=excluded.enabled,config=excluded.config`, f.ID, f.Enabled, raw); err != nil {
			return err
		}
		ids := f.BroadcasterIDs
		if ids == nil {
			ids = []string{}
		}
		// A download keeps its immutable owner and generation, while current
		// filters decide whether that owner remains an eligible consumer.
		if _, err = tx.Exec(ctx, `UPDATE twitch_collector.consumers co SET eligible=(c.view_count >= $2 AND c.created_at<=now()-$3::double precision*interval '1 minute'
 AND c.created_at>=now()-$4::double precision*interval '1 hour' AND c.duration >= $5 AND c.duration <= $6 AND($7='' OR (c.language=$7 AND c.language_source='clip'))
 AND CASE WHEN $8='category' THEN c.game_id=$9 ELSE(cardinality($10::text[])=0 OR c.broadcaster_id=ANY($10::text[])) END)
 FROM twitch_collector.clips c WHERE co.clip_id=c.clip_id AND co.flow_id=$1`, f.ID, f.MinViews, f.MinAgeMinutes, f.WindowHours, f.MinDurationSec, f.MaxDurationSec, f.Language, f.SourceMode, f.CategoryID, ids); err != nil {
			return err
		}
		if f.ID == "default" {
			if _, err = tx.Exec(ctx, `WITH pinned AS (UPDATE twitch_collector.download_tasks SET flow_id=$1,flow_version=$2 WHERE flow_id='' RETURNING clip_id) INSERT INTO twitch_collector.consumers(clip_id,flow_id,flow_version) SELECT clip_id,$1,$2 FROM pinned ON CONFLICT DO NOTHING`, f.ID, f.Version); err != nil {
				return err
			}
		}
		maxAge := f.MaxSourceAgeHours
		if maxAge <= 0 {
			maxAge = 72
		}
		// A pinned discovery snapshot cannot keep an expired candidate eligible.
		if _, err = tx.Exec(ctx, `UPDATE twitch_collector.consumers co SET eligible=FALSE FROM twitch_collector.clips c
WHERE co.flow_id=$1 AND co.clip_id=c.clip_id AND c.created_at < now()-$2::double precision*interval '1 hour'`, f.ID, maxAge); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) RoundRobinStart(ctx context.Context, count int) (int, error) {
	var result int
	err := s.pool.QueryRow(ctx, `UPDATE twitch_collector.state SET next_flow_index=(next_flow_index+1)%$1 WHERE id=1 RETURNING (next_flow_index+$1-1)%$1`, count).Scan(&result)
	return result, err
}

// Freeze the pre-flow catalogue on migration. Category discovery must not add
// new streamers to the legacy default flow through the shared channel table.
func (s *Store) LegacyBroadcasterIDs(ctx context.Context) ([]string, error) {
	var ids []string
	err := s.pool.QueryRow(ctx, `SELECT legacy_broadcaster_ids FROM twitch_collector.state WHERE id=1`).Scan(&ids)
	return ids, err
}

func (s *Store) KnownClipIDs(ctx context.Context) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx, `SELECT clip_id FROM twitch_collector.clips WHERE state<>'discovered' OR reserved_at IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
func (s *Store) ObserveFlow(ctx context.Context, f twitch.Flow, clips []domain.Clip) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, c := range clips {
		age := time.Since(c.CreatedAt)
		maxAge := f.MaxSourceAgeHours
		if maxAge <= 0 {
			maxAge = 72
		}
		eligible := c.ViewCount >= f.MinViews && age >= time.Duration(f.MinAgeMinutes)*time.Minute && age <= time.Duration(f.WindowHours)*time.Hour && age <= time.Duration(maxAge)*time.Hour && c.Duration >= f.MinDurationSec && c.Duration <= f.MaxDurationSec && (f.Language == "" || c.Language == f.Language && c.LanguageSource == "clip")
		if _, err = tx.Exec(ctx, `INSERT INTO twitch_collector.consumers(clip_id,flow_id,flow_version,eligible) VALUES($1,$2,$3,$4)
 ON CONFLICT(clip_id,flow_id) DO UPDATE SET eligible=excluded.eligible,
 flow_version=CASE WHEN EXISTS(SELECT 1 FROM twitch_collector.download_tasks t WHERE t.clip_id=excluded.clip_id) THEN twitch_collector.consumers.flow_version ELSE excluded.flow_version END`, c.ID, f.ID, f.Version, eligible); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s *Store) SharedReady(ctx context.Context, imports []twitch.Import) error {
	for _, i := range imports {
		if i.MediaState != "ready" && i.State != "ready" {
			continue
		}
		if _, err := s.pool.Exec(ctx, `UPDATE twitch_collector.clips SET state='imported',import_id=$2,sha256=$3,last_error='' WHERE clip_id=$1`, i.Source.ClipID, i.ImportID, i.SHA256); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) SaveReservation(ctx context.Context, t domain.Task, imp twitch.Import) error {
	data, err := json.Marshal(imp)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	flowID, flowVersion := t.FlowID, t.FlowVersion
	if imp.FlowID != "" {
		flowID = imp.FlowID
	}
	if imp.FlowVersion > 0 {
		flowVersion = imp.FlowVersion
	} else if flowID != t.FlowID {
		// Compatibility with an older backend during a rolling upgrade.
		if err = tx.QueryRow(ctx, `SELECT (config->>'version')::integer FROM twitch_collector.flows WHERE id=$1`, flowID).Scan(&flowVersion); err != nil {
			return err
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE twitch_collector.download_tasks t SET reservation=$3,flow_id=$4,flow_version=$5 WHERE `+validLease, t.Clip.ID, t.Token, data, flowID, flowVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrLeaseLost
	}
	// Waiting for capacity is not a physical download. Charge its calendar-day
	// budget only after main-be admits it; shared ready media consumes no download.
	if _, err = tx.Exec(ctx, `UPDATE twitch_collector.clips SET reserved_at=CASE WHEN $2 THEN NULL WHEN error_kind='capacity' THEN now() ELSE reserved_at END,error_kind='',last_error='' WHERE clip_id=$1`, t.Clip.ID, imp.MediaState == "ready" || imp.State == "ready"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) HasActiveConsumer(ctx context.Context, id string) (bool, error) {
	var out bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_collector.consumers c JOIN twitch_collector.flows f ON f.id=c.flow_id WHERE c.clip_id=$1 AND c.eligible AND NOT c.excluded AND f.enabled)`, id).Scan(&out)
	return out, err
}

// Before the first import reservation, pick a saved consumer that is still active.
// A reservation already made retains its immutable flow and generation.
func (s *Store) PinActiveConsumer(ctx context.Context, t domain.Task) (domain.Task, error) {
	if t.Reservation.ImportID != "" {
		return t, nil
	}
	err := s.pool.QueryRow(ctx, `SELECT c.flow_id,(f.config->>'version')::integer FROM twitch_collector.consumers c JOIN twitch_collector.flows f ON f.id=c.flow_id WHERE c.clip_id=$1 AND c.eligible AND NOT c.excluded AND f.enabled ORDER BY CASE WHEN c.flow_id=$2 THEN 0 ELSE 1 END,c.flow_id LIMIT 1`, t.Clip.ID, t.FlowID).Scan(&t.FlowID, &t.FlowVersion)
	return t, translate(err)
}

// An unreserved shared clip may serve another flow when its first consumer's
// calendar is full. No import generation is changed after it has been issued.
func (s *Store) ReservationConsumers(ctx context.Context, id string) ([]twitch.Flow, error) {
	rows, err := s.pool.Query(ctx, `SELECT f.config FROM twitch_collector.consumers co JOIN twitch_collector.flows f ON f.id=co.flow_id
WHERE co.clip_id=$1 AND co.eligible AND NOT co.excluded AND f.enabled ORDER BY co.flow_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []twitch.Flow{}
	for rows.Next() {
		var raw []byte
		var f twitch.Flow
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) SyncExclusions(ctx context.Context, cfg twitch.CollectorConfig) error {
	blocked := cfg.BlockedSourceIDs
	if blocked == nil {
		blocked = []string{}
	}
	for _, f := range cfg.Flows {
		excluded := f.ExcludedSourceIDs
		if excluded == nil {
			excluded = []string{}
		}
		if _, err := s.pool.Exec(ctx, `UPDATE twitch_collector.consumers SET excluded=(clip_id=ANY($2::text[]) OR clip_id=ANY($3::text[])) WHERE flow_id=$1`, f.ID, excluded, blocked); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) RetryClip(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE twitch_collector.download_tasks SET state='queued',attempts=0,run_at=now(),last_error='',lease_token=NULL,lease_expires_at=NULL WHERE clip_id=$1 AND state='failed'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_collector.clips SET state='queued',last_error='',error_kind='' WHERE clip_id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
