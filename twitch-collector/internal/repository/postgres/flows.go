package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
	"time"

	"github.com/jackc/pgx/v5"
)

// lockFlowConsumers serialises writers of one flow's consumers rows for the rest
// of the transaction. SyncFlows updates them in bulk while ObserveFlow upserts
// them one by one; without a shared lock they take row locks in different
// orders and deadlock.
//
// NOTE (do not "simplify" this away): every writer that touches
// twitch_collector.consumers for a flow (SyncFlows, ObserveFlow,
// SyncExclusions) MUST take this lock first, inside a transaction. A bare
// pool.Exec UPDATE on consumers bypasses it and brings back PostgreSQL 40P01
// "deadlock detected" in production (seen as stage=verification collector
// task failures, retried 5-30 min later). Hold only one flow's lock per
// transaction unless you always acquire several in one fixed order.
// Regression test: flows_lock_test.go.
func lockFlowConsumers(ctx context.Context, tx pgx.Tx, flowID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('consumers:'||$1::text))`, flowID)
	return err
}

func (s *Store) SyncFlows(ctx context.Context, flows []twitch.Flow) error {
	flows = append([]twitch.Flow(nil), flows...)
	sort.Slice(flows, func(i, j int) bool { return flows[i].ID < flows[j].ID })
	ids := make([]string, 0, len(flows))
	for _, f := range flows {
		ids = append(ids, f.ID)
		if err := s.syncOneFlow(ctx, f); err != nil {
			return err
		}
	}
	rows, err := s.pool.Query(ctx, `SELECT id FROM twitch_collector.flows WHERE enabled AND NOT(id=ANY($1::text[])) ORDER BY id`, ids)
	if err != nil {
		return err
	}
	var removed []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		removed = append(removed, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range removed {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		if err = lockFlowConsumers(ctx, tx, id); err == nil {
			_, err = tx.Exec(ctx, `UPDATE twitch_collector.flows SET enabled=false WHERE id=$1 AND enabled`, id)
		}
		if err != nil {
			tx.Rollback(ctx)
			return err
		}
		if err = tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) syncOneFlow(ctx context.Context, f twitch.Flow) error {
	f = domain.ConfigurationFlow(f)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// All writers use advisory lock -> flow row -> consumer rows. Never acquire
	// the flow row first: ObserveFlow checks the current version under this lock.
	if err = lockFlowConsumers(ctx, tx, f.ID); err != nil {
		return err
	}
	raw, e := json.Marshal(f)
	if e != nil {
		return e
	}
	tag, e := tx.Exec(ctx, `INSERT INTO twitch_collector.flows(id,enabled,config) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET enabled=excluded.enabled,config=excluded.config WHERE twitch_collector.flows.enabled IS DISTINCT FROM excluded.enabled OR twitch_collector.flows.config IS DISTINCT FROM excluded.config`, f.ID, f.Enabled, raw)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	if err = refreshFlowEligibility(ctx, tx, f); err != nil {
		return err
	}
	if f.ID == "default" {
		if _, err = tx.Exec(ctx, `WITH pinned AS (UPDATE twitch_collector.download_tasks SET flow_id=$1,flow_version=$2 WHERE flow_id='' RETURNING clip_id) INSERT INTO twitch_collector.consumers(clip_id,flow_id,flow_version) SELECT clip_id,$1,$2 FROM pinned ON CONFLICT DO NOTHING`, f.ID, f.Version); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func refreshFlowEligibility(ctx context.Context, tx pgx.Tx, f twitch.Flow) error {
	ids := f.BroadcasterIDs
	if ids == nil {
		ids = []string{}
	}
	maxAge := f.MaxSourceAgeHours
	if maxAge <= 0 {
		maxAge = 72
	}
	_, err := tx.Exec(ctx, `WITH evaluated AS(SELECT co.clip_id,(c.view_count >= $2 AND c.created_at<=now()-$3::double precision*interval '1 minute'
 AND c.created_at>=now()-LEAST($4::double precision,$11::double precision)*interval '1 hour' AND c.duration >= $5 AND c.duration <= $6 AND($7='' OR(c.language=$7 AND c.language_source='clip'))
 AND CASE WHEN $8='category' THEN c.game_id=$9 ELSE(cardinality($10::text[])=0 OR c.broadcaster_id=ANY($10::text[])) END)AS eligible
 FROM twitch_collector.consumers co JOIN twitch_collector.clips c ON co.clip_id=c.clip_id WHERE co.flow_id=$1)
 UPDATE twitch_collector.consumers co SET eligible=e.eligible FROM evaluated e WHERE co.flow_id=$1 AND co.clip_id=e.clip_id AND co.eligible IS DISTINCT FROM e.eligible`, f.ID, f.MinViews, f.MinAgeMinutes, f.WindowHours, f.MinDurationSec, f.MaxDurationSec, f.Language, f.SourceMode, f.CategoryID, ids, maxAge)
	return err
}

func (s *Store) RefreshEligibility(ctx context.Context, flows []twitch.Flow) error {
	for _, f := range flows {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		if err = lockFlowConsumers(ctx, tx, f.ID); err == nil {
			var raw []byte
			err = tx.QueryRow(ctx, `SELECT config FROM twitch_collector.flows WHERE id=$1 FOR SHARE`, f.ID).Scan(&raw)
			if err == nil {
				var current twitch.Flow
				err = json.Unmarshal(raw, &current)
				if err == nil {
					err = refreshFlowEligibility(ctx, tx, current)
				}
			}
		}
		if err != nil {
			tx.Rollback(ctx)
			return err
		}
		if err = tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
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
	if err = lockFlowConsumers(ctx, tx, f.ID); err != nil {
		return err
	}
	var version int
	var enabled bool
	if err = tx.QueryRow(ctx, `SELECT (config->>'version')::int,enabled FROM twitch_collector.flows WHERE id=$1 FOR SHARE`, f.ID).Scan(&version, &enabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrConflict
		}
		return err
	}
	if version != f.Version || !enabled {
		return &domain.Failure{Kind: "conflict", Code: "flow_version_changed", Message: "Настройки потока изменились во время поиска; старые наблюдения не применены"}
	}
	for _, c := range clips {
		excluded := false
		for _, id := range f.ExcludedSourceIDs {
			if id == c.ID {
				excluded = true
				break
			}
		}
		age := time.Since(c.CreatedAt)
		maxAge := f.MaxSourceAgeHours
		if maxAge <= 0 {
			maxAge = 72
		}
		eligible := c.ViewCount >= f.MinViews && age >= time.Duration(f.MinAgeMinutes)*time.Minute && age <= time.Duration(f.WindowHours)*time.Hour && age <= time.Duration(maxAge)*time.Hour && c.Duration >= f.MinDurationSec && c.Duration <= f.MaxDurationSec && (f.Language == "" || c.Language == f.Language && c.LanguageSource == "clip")
		if f.SourceMode == "category" {
			eligible = eligible && c.GameID == f.CategoryID
		} else if len(f.BroadcasterIDs) > 0 {
			matched := false
			for _, id := range f.BroadcasterIDs {
				if id == c.BroadcasterID {
					matched = true
					break
				}
			}
			eligible = eligible && matched
		}
		if _, err = tx.Exec(ctx, `INSERT INTO twitch_collector.consumers(clip_id,flow_id,flow_version,eligible,excluded) VALUES($1,$2,$3,$4,$5)
 ON CONFLICT(clip_id,flow_id) DO UPDATE SET eligible=excluded.eligible,excluded=excluded.excluded,
 flow_version=CASE WHEN EXISTS(SELECT 1 FROM twitch_collector.download_tasks t WHERE t.clip_id=excluded.clip_id) THEN twitch_collector.consumers.flow_version ELSE excluded.flow_version END
 WHERE twitch_collector.consumers.eligible IS DISTINCT FROM excluded.eligible OR twitch_collector.consumers.excluded IS DISTINCT FROM excluded.excluded OR(NOT EXISTS(SELECT 1 FROM twitch_collector.download_tasks t WHERE t.clip_id=excluded.clip_id) AND twitch_collector.consumers.flow_version IS DISTINCT FROM excluded.flow_version)`, c.ID, f.ID, f.Version, eligible, excluded); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s *Store) SharedReady(ctx context.Context, imports []twitch.Import) error {
	for _, i := range imports {
		if _, err := s.pool.Exec(ctx, `UPDATE twitch_collector.clips SET import_id=$2 WHERE clip_id=$1 AND(import_id='' OR import_id=$2)`, i.Source.ClipID, i.ImportID); err != nil {
			return err
		}
		// Older collectors did not retain an admission's import ID. Observations
		// supply it without changing an already issued upload generation.
		raw, err := json.Marshal(i)
		if err != nil {
			return err
		}
		if _, err = s.pool.Exec(ctx, `UPDATE twitch_collector.download_tasks SET reservation=$2 WHERE clip_id=$1 AND state='failed' AND COALESCE(reservation->>'import_id','')='' AND epoch=$3`, i.Source.ClipID, raw, i.Epoch); err != nil {
			return err
		}
		if !domain.MediaReady(i) {
			continue
		}
		if _, err := s.pool.Exec(ctx, `UPDATE twitch_collector.clips SET state='imported',import_id=$2,sha256=$3,last_error='' WHERE clip_id=$1`, i.Source.ClipID, i.ImportID, i.SHA256); err != nil {
			return err
		}
		if _, err = s.pool.Exec(ctx, `UPDATE twitch_collector.download_tasks SET state='done',uploaded=NULL,last_error='',lease_token=NULL,lease_expires_at=NULL,updated_at=now()
 WHERE clip_id=$1 AND state='queued' AND epoch=$2 AND COALESCE(reservation->>'import_id','')=$3
 AND(COALESCE((reservation->>'upload_generation')::bigint,0)=0 OR COALESCE((reservation->>'upload_generation')::bigint,0)=$4)`, i.Source.ClipID, i.Epoch, i.ImportID, i.UploadGeneration); err != nil {
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
	tag, err := tx.Exec(ctx, `UPDATE twitch_collector.download_tasks t SET reservation=$3,flow_id=$4,flow_version=$5,transfer_phase=CASE WHEN $6 THEN 'reserved' ELSE transfer_phase END WHERE `+validLease, t.Clip.ID, t.Token, data, flowID, flowVersion, imp.UploadGeneration > 0 && !domain.MediaReady(imp))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrLeaseLost
	}
	// Waiting for capacity is not a physical download. Charge its calendar-day
	// budget only after main-be admits it; shared ready media consumes no download.
	if _, err = tx.Exec(ctx, `UPDATE twitch_collector.clips SET reserved_at=CASE WHEN $2 THEN NULL WHEN error_kind='capacity' THEN now() ELSE reserved_at END,error_kind='',last_error='' WHERE clip_id=$1`, t.Clip.ID, domain.MediaReady(imp)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) HasActiveConsumer(ctx context.Context, id string) (bool, error) {
	var out bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_collector.consumers c JOIN twitch_collector.flows f ON f.id=c.flow_id WHERE c.clip_id=$1 AND c.eligible AND NOT c.excluded AND f.enabled)`, id).Scan(&out)
	return out, err
}

// A coordinator admission keeps its selected flow/version before any upload is
// issued. An existing upload generation also retains its immutable owner.
// Only the legacy local admission path may select another active consumer.
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
		if err := s.syncFlowExclusions(ctx, f.ID, f.Version, excluded, blocked); err != nil {
			return err
		}
	}
	return nil
}

// syncFlowExclusions takes the per-flow consumers lock, like SyncFlows and
// ObserveFlow, and holds only that one lock per transaction so concurrent
// syncs cannot form a lock cycle across flows. It used to be a plain
// pool.Exec without the lock, which deadlocked against SyncFlows; keep the
// transaction and the lock (see lockFlowConsumers).
func (s *Store) syncFlowExclusions(ctx context.Context, flowID string, version int, excluded, blocked []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockFlowConsumers(ctx, tx, flowID); err != nil {
		return err
	}
	var currentVersion int
	if err = tx.QueryRow(ctx, `SELECT (config->>'version')::integer FROM twitch_collector.flows WHERE id=$1 FOR SHARE`, flowID).Scan(&currentVersion); err != nil {
		return err
	}
	if currentVersion != version {
		return &domain.Failure{Kind: "conflict", Code: "flow_version_changed", Message: "Настройки потока изменились; прежние исключения не применены"}
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_collector.consumers SET excluded=(clip_id=ANY($2::text[]) OR clip_id=ANY($3::text[])) WHERE flow_id=$1 AND excluded IS DISTINCT FROM(clip_id=ANY($2::text[]) OR clip_id=ANY($3::text[]))`, flowID, excluded, blocked); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RetryClip(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE twitch_collector.download_tasks SET state='queued',attempts=0,run_at=now(),last_error='',failure_synced_at=NULL,lease_token=NULL,lease_expires_at=NULL WHERE clip_id=$1 AND state='failed'`, id)
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
