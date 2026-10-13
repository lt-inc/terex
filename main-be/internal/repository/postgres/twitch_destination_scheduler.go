package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
	"time"
)

func (s *Store) scheduleDestinationOutputs(ctx context.Context, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	if err = expirePreparationTx(ctx, tx, now); err != nil {
		return err
	}
	// Legacy import paths also create outputs; register their original target,
	// never current destinations that were added after creation.
	rows, err := tx.Query(ctx, `SELECT o.clip_id,o.config,o.format,o.auto_eligible,o.created_at FROM twitch_outputs o WHERE NOT EXISTS(SELECT 1 FROM twitch_output_destinations d WHERE d.clip_id=o.clip_id) LIMIT 100`)
	if err != nil {
		return err
	}
	type legacy struct {
		clip, format string
		cfg          []byte
		auto         bool
		at           time.Time
	}
	var legacyOutputs []legacy
	for rows.Next() {
		var l legacy
		if err = rows.Scan(&l.clip, &l.cfg, &l.format, &l.auto, &l.at); err != nil {
			rows.Close()
			return err
		}
		legacyOutputs = append(legacyOutputs, l)
	}
	rows.Close()
	for _, l := range legacyOutputs {
		var f twitch.Flow
		if err = json.Unmarshal(l.cfg, &f); err != nil {
			return err
		}
		if err = snapshotOutputDestinations(ctx, tx, l.clip, f, l.format, l.auto, l.at); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM twitch_publication_slots s USING publications p WHERE p.id=s.publication_id AND(p.status IN('cancelled','on_hold','failed','blocked') OR p.auto_flow_id IS NULL)`); err != nil {
		return err
	}
	rows, err = tx.Query(ctx, `SELECT o.clip_id,o.flow_id,o.format,d.destination_key,d.account_id,d.snapshot,r.id,r.spec,COALESCE(p.id,0) FROM twitch_outputs o JOIN twitch_output_destinations d ON d.clip_id=o.clip_id JOIN twitch_flows f ON f.id=o.flow_id JOIN clips c ON c.clip_id=o.clip_id JOIN clip_revisions r ON r.id=c.latest_revision_id JOIN accounts a ON a.id=d.account_id LEFT JOIN publications p ON p.clip_id=o.clip_id AND p.account_id=d.account_id WHERE (o.auto_eligible AND d.auto_eligible OR NOT COALESCE((d.snapshot->>'auto_publish')::bool,FALSE)) AND c.approval<>'rejected' AND r.render_state='ready' AND r.approved_at IS NOT NULL AND COALESCE(r.object,'')<>'' AND f.enabled AND NOT f.archived AND a.connection_state='connected' AND ((a.platform='youtube' AND a.remote_account_id=d.destination_key) OR(a.platform='tiktok' AND 'tiktok:'||a.remote_account_id=d.destination_key)) AND(p.id IS NULL OR(p.auto_flow_id=o.flow_id AND p.auto_hold AND p.policy_hold_reason='calendar_horizon' AND p.started_at IS NULL AND p.status='on_hold')) ORDER BY o.created_at,o.clip_id,d.destination_key LIMIT 100`)
	if err != nil {
		return err
	}
	type candidate struct {
		clip, flow, format, key string
		account, rid, pid       int64
		snapshot, spec          []byte
	}
	var cs []candidate
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.clip, &c.flow, &c.format, &c.key, &c.account, &c.snapshot, &c.rid, &c.spec, &c.pid); err != nil {
			rows.Close()
			return err
		}
		cs = append(cs, c)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, c := range cs {
		d, e := decodeDestination(c.snapshot)
		if e != nil {
			return e
		}
		if !d.Enabled {
			continue
		}
		current, e := currentFlowPolicy(ctx, tx, c.flow)
		if e != nil {
			return e
		}
		f := destinationFlow(current, d)
		f.Destinations = nil
		// Disabling/removing a current target stops unstarted work, but never points
		// an old output at a newly connected account.
		var targetEnabled bool
		var currentAuto bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_flow_destinations WHERE flow_id=$1 AND destination_key=$2 AND enabled),EXISTS(SELECT 1 FROM twitch_flow_destinations WHERE flow_id=$1 AND destination_key=$2 AND enabled AND COALESCE((config->>'auto_publish')::bool,FALSE))`, c.flow, c.key).Scan(&targetEnabled, &currentAuto); e != nil {
			return e
		}
		if !targetEnabled || (d.AutoPublish && !currentAuto) {
			continue
		}
		auto := d.AutoPublish && currentAuto
		if e = storageClipAvailable(ctx, tx, c.clip); e != nil {
			if errors.Is(e, domain.ErrConflict) {
				continue
			}
			return e
		}
		if e = requireFlowFiltersTx(ctx, tx, c.clip, c.rid); e != nil {
			if errors.Is(e, domain.ErrConflict) {
				continue
			}
			return e
		}
		var metadata domain.PublicationMetadata
		if e = json.Unmarshal(c.spec, &metadata); e != nil {
			return e
		}
		metadata.Visibility = d.Visibility
		if d.Provider == "tiktok" {
			metadata.Caption = tikTokCaption(metadata.Title, metadata.Description, metadata.Hashtags)
			if len([]rune(metadata.Caption)) > 2200 {
				continue
			}
		}
		at := now
		slot := ""
		status := "on_hold"
		reason := "manual_schedule"
		message := "Выберите время публикации в календаре"
		var prep string
		var reservedAt time.Time
		e = tx.QueryRow(ctx, `SELECT id,slot_at FROM twitch_preparation_reservations WHERE clip_id=$1 AND youtube_channel_id=$2 AND state='active' AND expires_at>$3 ORDER BY slot_at LIMIT 1`, c.clip, c.key, now).Scan(&prep, &reservedAt)
		if e != nil && e != pgx.ErrNoRows {
			return e
		}
		if auto {
			if prep != "" {
				if _, e = tx.Exec(ctx, `UPDATE twitch_preparation_reservations SET state='consumed' WHERE id=$1`, prep); e != nil {
					return e
				}
			}
			var ok bool
			at, slot, ok, e = findFlowSlot(ctx, tx, f, c.format, c.pid, now)
			if e != nil {
				return e
			}
			if prep != "" && reservedAt.After(now) {
				at = reservedAt
				slot = at.In(mustLocation(f.Timezone)).Format("15:04")
				ok = true
			}
			if !ok {
				continue
			}
			v, e := sourcePolicyViolationForTarget(ctx, tx, c.clip, c.rid, at, now, &f)
			if e != nil {
				return e
			}
			if v != nil {
				if _, e = tx.Exec(ctx, `UPDATE twitch_output_destinations SET auto_eligible=FALSE WHERE clip_id=$1 AND destination_key=$2`, c.clip, c.key); e != nil {
					return e
				}
				continue
			}
			status = "scheduled"
			reason = ""
			message = ""
		}
		if c.pid == 0 {
			if e = tx.QueryRow(ctx, `INSERT INTO publications(clip_id,sub_job_id,account_id,platform,publish_at,revision_id,timezone,metadata,status,auto_flow_id,auto_hold,policy_hold_reason,error,destination_key,target_snapshot)SELECT $1,s.sub_job_id,$2,$3,$4,$5,$6,$7,$8,CASE WHEN $9 THEN $10 ELSE NULL END,FALSE,$11,NULLIF($12,''),$13,$14 FROM clips c JOIN sub_jobs s ON s.job_id=c.job_id WHERE c.clip_id=$1 LIMIT 1 ON CONFLICT(clip_id,account_id)DO UPDATE SET clip_id=EXCLUDED.clip_id RETURNING id`, c.clip, c.account, d.Provider, at, c.rid, d.Timezone, mustJSON(metadata), status, auto, c.flow, reason, message, c.key, c.snapshot).Scan(&c.pid); e != nil {
				return e
			}
		} else {
			if _, e = tx.Exec(ctx, `UPDATE publications SET publish_at=$2,status=$3,auto_hold=FALSE,policy_hold_reason=$4,error=NULL,next_attempt_at=$5,version=version+1 WHERE id=$1 AND started_at IS NULL`, c.pid, at, status, reason, now); e != nil {
				return e
			}
		}
		if auto {
			if _, e = tx.Exec(ctx, `INSERT INTO twitch_publication_slots(flow_id,local_date,format,slot,publication_id,youtube_channel_id)VALUES($1,$2::date,$3,$4,$5,$6)ON CONFLICT(publication_id)DO NOTHING`, c.flow, at.In(mustLocation(d.Timezone)).Format("2006-01-02"), c.format, slot, c.pid, c.key); e != nil {
				return e
			}
			if e = insertEvent(ctx, tx, c.pid, "", "auto_scheduled", "scheduled", map[string]any{"flow_id": c.flow, "provider": d.Provider, "publish_at": at}); e != nil {
				return e
			}
		}
	}
	if err = enforceTwitchCaps(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
