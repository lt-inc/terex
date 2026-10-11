package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"terex/proto/twitch"
)

// The pressure snapshot is advisory and read-only. ReserveFlowImport remains
// authoritative under the admission lock; this endpoint cannot reserve quota.
func (s *Store) TwitchCollectorPressure(ctx context.Context, q twitch.PressureRequest, now time.Time) (out twitch.PressureResult, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	out.ObservedAt = now
	out.Tasks = []twitch.PressureTaskResult{}
	if err = tx.QueryRow(ctx, `SELECT epoch,active FROM twitch_reset_control WHERE id=1`).Scan(&out.Epoch, &out.Maintenance); err != nil {
		return
	}
	settings, e := readTwitchSettings(ctx, tx)
	if e != nil {
		err = e
		return
	}
	out.Enabled = settings.Enabled
	out.BacklogLimit = settings.BacklogLimit
	if err = tx.QueryRow(ctx, `SELECT required,state,available_bytes,total_bytes,observed_at,reason FROM twitch_storage_pressure WHERE id=1`).Scan(&out.Storage.Required, &out.Storage.State, &out.Storage.AvailableBytes, &out.Storage.CapacityBytes, &out.Storage.ObservedAt, &out.Storage.Reason); err != nil {
		return
	}
	s.pressureMu.Lock()
	if s.pressureEpoch == out.Epoch && !s.pressureAt.IsZero() && now.Sub(s.pressureAt) < 5*time.Second {
		out.PendingPhysical = s.pressureCount
	} else {
		err = tx.QueryRow(ctx, `SELECT count(DISTINCT COALESCE(i.media_id,i.id)) FROM twitch_imports i WHERE `+pendingTwitchSource).Scan(&out.PendingPhysical)
		if err == nil {
			s.pressureCount = out.PendingPhysical
			s.pressureAt = now
			s.pressureEpoch = out.Epoch
		}
	}
	s.pressureMu.Unlock()
	if err != nil {
		return
	}
	// The grouped quota lookup serves all references instead of running the
	// canonical catalog/flow projection once for each queued clip.
	spent := map[string]int{}
	total := 0
	rows, e := tx.Query(ctx, `WITH spent AS (
 SELECT r.import_id,r.broadcaster_id FROM twitch_download_reservations r WHERE (r.created_at AT TIME ZONE $1)::date=$2::date
 UNION SELECT g.import_id,i.request->'source'->>'broadcaster_id' FROM twitch_download_grants g JOIN twitch_imports i ON i.id=g.import_id WHERE(g.created_at AT TIME ZONE $1)::date=$2::date AND g.state<>'released')
 SELECT COALESCE(broadcaster_id,''),count(DISTINCT import_id)::integer FROM spent GROUP BY broadcaster_id`, settings.Timezone, now.In(mustLocation(settings.Timezone)).Format("2006-01-02"))
	if e != nil {
		err = e
		return
	}
	for rows.Next() {
		var id string
		var n int
		if e = rows.Scan(&id, &n); e != nil {
			break
		}
		spent[id] = n
		total += n
	}
	rows.Close()
	if e == nil {
		e = rows.Err()
	}
	if e != nil {
		err = e
		return
	}
	if q.Tasks == nil {
		q.Tasks = []twitch.PressureTaskRef{}
	}
	refs, _ := json.Marshal(q.Tasks)
	rows, err = tx.Query(ctx, `SELECT r.clip_id,r.import_id,r.flow_id,r.admission_sequence,r.upload_generation,
 COALESCE(i.state,''),COALESCE(i.media_state,''),COALESCE(i.upload_generation,0),COALESCE(i.epoch,0),COALESCE(i.twitch_clip_id,''),
 COALESCE(g.import_id,''),COALESCE(g.flow_id,''),COALESCE(g.sequence,0),COALESCE(g.epoch,0),COALESCE(g.state,''),g.expires_at,
 COALESCE(f.enabled,false),COALESCE(f.archived,true),COALESCE(i.request->'source'->>'broadcaster_id',''),COALESCE(i.rejected,false),COALESCE(i.request,'{}'::jsonb),COALESCE(f.config,'{}'::jsonb)
 FROM jsonb_to_recordset($1::jsonb) AS r(clip_id text,import_id text,flow_id text,admission_sequence bigint,upload_generation bigint)
 LEFT JOIN twitch_imports i ON i.id=r.import_id LEFT JOIN twitch_download_grants g ON g.clip_id=r.clip_id LEFT JOIN twitch_flows f ON f.id=r.flow_id ORDER BY r.import_id`, refs)
	if err != nil {
		return
	}
	for rows.Next() {
		var v twitch.PressureTaskResult
		var actualGeneration, importEpoch, grantSequence, grantEpoch int64
		var importClip, grantImport, grantFlow, grantState, broadcaster string
		var enabled, archived, rejected bool
		var requestRaw, flowRaw []byte
		if err = rows.Scan(&v.ClipID, &v.ImportID, &v.FlowID, &v.AdmissionSequence, &v.UploadGeneration, &v.ImportState, &v.MediaState, &actualGeneration, &importEpoch, &importClip, &grantImport, &grantFlow, &grantSequence, &grantEpoch, &grantState, &v.ExpiresAt, &enabled, &archived, &broadcaster, &rejected, &requestRaw, &flowRaw); err != nil {
			break
		}
		var request twitch.ImportRequest
		var flow twitch.Flow
		if e = json.Unmarshal(requestRaw, &request); e != nil {
			err = e
			break
		}
		if e = json.Unmarshal(flowRaw, &flow); e != nil {
			err = e
			break
		}
		requestedGeneration := v.UploadGeneration
		v.UploadGeneration = actualGeneration
		v.GrantState = "absent"
		v.ReasonCode = "allocation_wait"
		switch {
		case out.Maintenance || q.Epoch != out.Epoch:
			v.GrantState = "mismatch"
			v.ReasonCode = "twitch_reset"
		case importClip == "":
			v.ReasonCode = "import_missing"
		case importClip != v.ClipID || importEpoch != q.Epoch || (requestedGeneration > 0 && requestedGeneration != actualGeneration):
			v.GrantState = "mismatch"
			v.ReasonCode = "upload_generation_changed"
		case rejected || v.MediaState == "expired":
			v.GrantState = "revoked"
			v.ReasonCode = "source_rejected_or_expired"
		case v.MediaState == "ready":
			v.GrantState = "consumed"
			v.ReasonCode = "media_ready"
		case !enabled || archived:
			v.GrantState = "revoked"
			v.ReasonCode = "flow_paused"
		case !sourceEligible(flow, request.Source, now):
			v.GrantState = "revoked"
			v.ReasonCode = "source_filtered"
		case grantImport != "" && (grantImport != v.ImportID || grantFlow != v.FlowID || grantSequence != v.AdmissionSequence || grantEpoch != q.Epoch):
			v.GrantState = "mismatch"
			v.ReasonCode = "assignment_changed"
		case grantState == "consumed":
			v.GrantState = "consumed"
			v.ReasonCode = ""
		case grantState == "active" && v.ExpiresAt != nil && v.ExpiresAt.After(now):
			v.GrantState = "valid"
			v.ReasonCode = ""
		case grantState == "active":
			v.GrantState = "expired"
			v.ReasonCode = "assignment_expired"
		case grantState == "released":
			v.GrantState = "revoked"
			v.ReasonCode = "assignment_released"
		case !settings.Enabled:
			v.ReasonCode = "collector_disabled"
		case total >= settings.DailyDownloads:
			v.ReasonCode = "daily_download_limit"
		case spent[broadcaster] >= settings.PerChannelDailyDownloads:
			v.ReasonCode = "broadcaster_daily_limit"
		}
		// A generation is a reservation identity, never evidence of a PUT. The
		// completion worker uses its saved digest/size and these server states.
		out.Tasks = append(out.Tasks, v)
	}
	rows.Close()
	if err == nil {
		err = rows.Err()
	}
	if err != nil {
		return
	}
	err = tx.Commit(ctx)
	return
}
