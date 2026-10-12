package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"strings"
	"terex/main-be/internal/domain"
	"terex/proto/msg"
	"terex/proto/twitch"
	"terex/proto/workflow"
	"time"
)

func lockTwitch(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(773219421)`)
	return err
}
func readTwitchSettings(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) (twitch.Settings, error) {
	out := twitch.DefaultSettings()
	var data []byte
	err := q.QueryRow(ctx, `SELECT settings FROM twitch_project_settings WHERE id=1`).Scan(&data)
	if err == nil {
		err = json.Unmarshal(data, &out)
	}
	return out, err
}
func (s *Store) TwitchSettings(ctx context.Context) (twitch.Settings, error) {
	return readTwitchSettings(ctx, s.pool)
}
func (s *Store) SaveTwitchSettings(ctx context.Context, v twitch.Settings) error {
	if err := v.Validate(); err != nil {
		return fmt.Errorf("%w: %s", domain.ErrInvalid, err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	previous, err := readTwitchSettings(ctx, tx)
	if err != nil {
		return err
	}
	if v.TargetAccountID > 0 {
		var platform string
		if err = tx.QueryRow(ctx, `SELECT platform FROM accounts WHERE id=$1`, v.TargetAccountID).Scan(&platform); err != nil {
			return mapErr(err)
		}
		if platform != "youtube" {
			return fmt.Errorf("%w: target must be a YouTube account", domain.ErrInvalid)
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_project_settings SET settings=$1,updated_at=now() WHERE id=1`, mustJSON(v)); err != nil {
		return err
	}
	if err = syncLegacyFlow(ctx, tx, previous, v); err != nil {
		return err
	}
	// Do not silently leave already-approved plans above a newly reduced cap.
	if err = enforceTwitchCaps(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

const importCols = `i.id,i.request,i.state,i.original,i.verified,i.upload_expires_at,COALESCE(i.job_id,''),COALESCE(i.clip_id,''),i.rejected,i.created_at,COALESCE(i.error,''),COALESCE(c.latest_revision_id,0),COALESCE(i.media_id,''),i.media_state,i.upload_generation,i.epoch`

func scanImport(row pgx.Row) (twitch.Import, error) {
	var o twitch.Import
	var req, original, verified []byte
	err := row.Scan(&o.ImportID, &req, &o.State, &original, &verified, &o.UploadExpiresAt, &o.JobID, &o.ClipID, &o.Rejected, &o.CreatedAt, &o.Error, &o.RevisionID, &o.MediaID, &o.MediaState, &o.UploadGeneration, &o.Epoch)
	if err != nil {
		return o, mapErr(err)
	}
	var r twitch.ImportRequest
	var v workflow.VerifiedMedia
	if err = json.Unmarshal(req, &r); err != nil {
		return o, err
	}
	if err = json.Unmarshal(original, &o.Original); err != nil {
		return o, err
	}
	if err = json.Unmarshal(verified, &v); err != nil {
		return o, err
	}
	o.Source, o.AccountID = r.Source, r.AccountID
	o.FlowID, o.ReservationID = r.FlowID, r.ReservationID
	o.FlowVersion = r.FlowVersion
	o.AdmissionSequence = r.AdmissionSequence
	if o.UploadGeneration > 0 {
		o.UploadImportID = fmt.Sprintf("%s-g%d", o.ImportID, o.UploadGeneration)
	}
	o.SHA256, o.DurationSec, o.SizeBytes, o.Width, o.Height = v.SHA256, v.DurationSec, v.SizeBytes, v.Width, v.Height
	return o, nil
}
func importByID(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id string) (twitch.Import, error) {
	return scanImport(q.QueryRow(ctx, `SELECT `+importCols+` FROM twitch_imports i LEFT JOIN clips c ON c.clip_id=i.clip_id WHERE i.id=$1`, id))
}
func (s *Store) GetTwitchImport(ctx context.Context, id string) (twitch.Import, error) {
	return importByID(ctx, s.pool, id)
}

// Queue pressure is per physical source, not per observation or output. Ready
// media waits only while an active flow has an undecided output or still needs
// that media for a compilation. Approved/used sources release their place.
const currentFlowSourceEligible = `
 COALESCE((i.request->'source'->>'view_count')::bigint,0)>=COALESCE((f.config->>'min_views')::bigint,0)
 AND (i.request->'source'->>'duration_sec')::numeric BETWEEN (f.config->>'min_duration_sec')::numeric AND (f.config->>'max_duration_sec')::numeric
 AND (i.request->'source'->>'created_at')::timestamptz BETWEEN now()-make_interval(hours=>(f.config->>'window_hours')::int) AND now()-make_interval(mins=>COALESCE((f.config->>'min_age_minutes')::int,0))
 AND(COALESCE(f.config->>'language','')='' OR (f.config->>'language'=i.request->'source'->>'language' AND i.request->'source'->>'language_source'='clip'))
 AND(CASE WHEN f.config->>'source_mode'='category' THEN f.config->>'category_id'=i.request->'source'->>'game_id'
 ELSE COALESCE(f.config->'broadcaster_ids','[]'::jsonb) ? (i.request->'source'->>'broadcaster_id') OR (COALESCE((f.config->>'legacy_length')::boolean,false) AND COALESCE(jsonb_array_length(f.config->'broadcaster_ids'),0)=0) END)`

const pendingTwitchSource = `NOT i.rejected AND i.state NOT IN('discovered','failed') AND (
 (i.media_state<>'ready' AND (NOT EXISTS(SELECT 1 FROM twitch_flow_sources fs WHERE fs.import_id=i.id)
 OR EXISTS(SELECT 1 FROM twitch_flow_sources fs JOIN twitch_flows f ON f.id=fs.flow_id JOIN accounts a ON a.id=f.account_id
 WHERE fs.import_id=i.id AND NOT fs.excluded AND f.enabled AND NOT f.archived AND a.connection_state='connected' AND a.remote_account_id=f.youtube_channel_id
 AND(fs.reserved_at IS NOT NULL OR (` + currentFlowSourceEligible + `)))))
 OR EXISTS(SELECT 1 FROM twitch_flow_sources fs JOIN twitch_flows f ON f.id=fs.flow_id JOIN accounts a ON a.id=f.account_id
 WHERE fs.import_id=i.id AND NOT fs.excluded AND f.enabled AND NOT f.archived AND a.connection_state='connected' AND a.remote_account_id=f.youtube_channel_id
 AND(EXISTS(SELECT 1 FROM twitch_outputs o JOIN clips c ON c.clip_id=o.clip_id WHERE o.flow_id=f.id AND c.approval='pending'
 AND(o.source_id=i.id OR EXISTS(SELECT 1 FROM twitch_compilation_items ci WHERE ci.compilation_revision_id=c.latest_revision_id AND ci.source_id=i.id)))
 OR(i.media_state='ready' AND COALESCE((f.config->>'compilations_enabled')::boolean,false) AND (` + currentFlowSourceEligible + `)
 AND NOT EXISTS(SELECT 1 FROM twitch_source_uses u WHERE u.youtube_channel_id=f.youtube_channel_id AND u.format='compilation' AND u.media_id=i.media_id))))
 OR(i.clip_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM twitch_outputs o WHERE o.clip_id=i.clip_id) AND EXISTS(SELECT 1 FROM clips c WHERE c.clip_id=i.clip_id AND c.approval='pending')))
 AND((SELECT mode FROM twitch_policy_settings WHERE id=1)<>'enforce'
 OR EXISTS(SELECT 1 FROM twitch_preparation_sources ps JOIN twitch_preparation_reservations pr ON pr.id=ps.reservation_id JOIN twitch_flows pf ON pf.id=pr.flow_id WHERE ps.import_id=i.id AND pr.state='active' AND pr.expires_at>now() AND pf.enabled AND NOT pf.archived)
 OR EXISTS(SELECT 1 FROM twitch_outputs o JOIN clips c ON c.clip_id=o.clip_id JOIN render_attempts ra ON ra.revision_id=c.latest_revision_id WHERE(o.source_id=i.id OR EXISTS(SELECT 1 FROM twitch_compilation_items ci WHERE ci.compilation_revision_id=c.latest_revision_id AND ci.source_id=i.id)) AND ra.status='active' AND ((ra.execution_started_at IS NOT NULL AND ra.lease_until>now()) OR (ra.execution_started_at IS NULL AND ra.dispatch_deadline>now()))))`

func (s *Store) ListTwitchImports(ctx context.Context, pending bool, state string) ([]twitch.Import, error) {
	rows, err := s.pool.Query(ctx, `WITH pending_sources AS(SELECT i.id,row_number()OVER(PARTITION BY COALESCE(i.media_id,i.id)ORDER BY i.created_at DESC,i.id)position FROM twitch_imports i WHERE `+pendingTwitchSource+`)
 SELECT `+importCols+` FROM twitch_imports i LEFT JOIN clips c ON c.clip_id=i.clip_id
 WHERE ($1=FALSE OR i.id IN(SELECT id FROM pending_sources WHERE position=1)) AND ($2='' OR i.state=$2)
 ORDER BY i.created_at DESC LIMIT 1000`, pending, state)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []twitch.Import{}
	for rows.Next() {
		v, e := scanImport(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) ReserveTwitchImport(ctx context.Context, r twitch.ImportRequest, now time.Time) (twitch.Import, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return twitch.Import{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return twitch.Import{}, err
	}
	if err = twitchResetRequestAllowed(ctx, tx, r.Epoch); err != nil {
		return twitch.Import{}, err
	}
	existing, err := scanImport(tx.QueryRow(ctx, `SELECT `+importCols+` FROM twitch_imports i LEFT JOIN clips c ON c.clip_id=i.clip_id WHERE EXISTS(SELECT 1 FROM twitch_import_keys k WHERE k.idempotency_key=$1 AND k.import_id=i.id) OR i.twitch_clip_id=$2 ORDER BY EXISTS(SELECT 1 FROM twitch_import_keys k WHERE k.idempotency_key=$1 AND k.import_id=i.id) DESC LIMIT 1`, r.IdempotencyKey, r.Source.ClipID))
	if err == nil {
		if existing.Source.ClipID != r.Source.ClipID {
			return twitch.Import{}, fmt.Errorf("%w: idempotency key belongs to another clip", domain.ErrConflict)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO twitch_import_keys(idempotency_key,import_id)VALUES($1,$2)ON CONFLICT DO NOTHING`, r.IdempotencyKey, existing.ImportID); err != nil {
			return existing, err
		}
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return twitch.Import{}, err
	}
	mode, err := policyMode(ctx, tx)
	if err != nil {
		return twitch.Import{}, err
	}
	if mode == "enforce" {
		return twitch.Import{}, waitPreparation("legacy_collector_upgrade", "Для новых загрузок требуется collector с резервированием ёмкости потока", now)
	}
	settings, err := readTwitchSettings(ctx, tx)
	if err != nil {
		return twitch.Import{}, err
	}
	if !settings.Enabled {
		return twitch.Import{}, fmt.Errorf("%w: Twitch collection is disabled", domain.ErrConflict)
	}
	if r.AccountID == 0 {
		r.AccountID = settings.TargetAccountID
	}
	if r.AccountID != settings.TargetAccountID || r.AccountID <= 0 {
		return twitch.Import{}, fmt.Errorf("%w: use the configured target account", domain.ErrInvalid)
	}
	var backlog, total, channel int
	if err = tx.QueryRow(ctx, `SELECT count(DISTINCT COALESCE(i.media_id,i.id)) FROM twitch_imports i WHERE `+pendingTwitchSource).Scan(&backlog); err != nil {
		return twitch.Import{}, err
	}
	if backlog >= settings.BacklogLimit {
		return twitch.Import{}, fmt.Errorf("%w: review backlog is full", domain.ErrConflict)
	}
	loc, _ := time.LoadLocation(settings.Timezone)
	date := now.In(loc).Format("2006-01-02")
	if err = tx.QueryRow(ctx, `SELECT count(DISTINCT import_id),count(DISTINCT import_id) FILTER(WHERE broadcaster_id=$3) FROM twitch_download_reservations WHERE (created_at AT TIME ZONE $1)::date=$2::date`, settings.Timezone, date, r.Source.BroadcasterID).Scan(&total, &channel); err != nil {
		return twitch.Import{}, err
	}
	if settings.DailyDownloadLimitReached(total) || channel >= settings.PerChannelDailyDownloads {
		return twitch.Import{}, fmt.Errorf("%w: daily download limit reached", domain.ErrConflict)
	}
	id := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO twitch_imports(id,idempotency_key,twitch_clip_id,request,created_at,updated_at,epoch) VALUES($1,$2,$3,$4,$5,$5,$6)`, id, r.IdempotencyKey, r.Source.ClipID, mustJSON(r), now, r.Epoch); err != nil {
		return twitch.Import{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO twitch_download_reservations(import_id,generation,broadcaster_id,created_at)VALUES($1,0,$2,$3)`, id, r.Source.BroadcasterID, now); err != nil {
		return twitch.Import{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO twitch_import_keys(idempotency_key,import_id)VALUES($1,$2)`, r.IdempotencyKey, id); err != nil {
		return twitch.Import{}, err
	}
	out, err := importByID(ctx, tx, id)
	if err != nil {
		return twitch.Import{}, err
	}
	return out, tx.Commit(ctx)
}
func (s *Store) SetTwitchUpload(ctx context.Context, id string, ref workflow.ObjectRef, expires time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	if err = twitchResetAllowed(ctx, tx); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE twitch_imports SET state='upload_pending',original=$2,upload_expires_at=$3,error=NULL,updated_at=now() WHERE id=$1 AND state IN ('upload_pending','failed') AND clip_id IS NULL`, id, mustJSON(ref), expires)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return tx.Commit(ctx)
}
func (s *Store) FailTwitchImport(ctx context.Context, id, message string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	if err = twitchResetAllowed(ctx, tx); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE twitch_imports SET state='failed',error=$2,updated_at=now() WHERE id=$1 AND clip_id IS NULL`, id, message)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) FinishTwitchImport(ctx context.Context, id string, v *workflow.VerifiedMedia, now time.Time) (twitch.Import, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return twitch.Import{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return twitch.Import{}, err
	}
	if err = twitchResetAllowed(ctx, tx); err != nil {
		return twitch.Import{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM twitch_imports WHERE id=$1 FOR UPDATE`, id); err != nil {
		return twitch.Import{}, err
	}
	out, err := importByID(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if out.ClipID != "" {
		return out, nil
	}
	if out.Rejected {
		return out, fmt.Errorf("%w: import is rejected", domain.ErrConflict)
	}
	if err = storageRefsAvailable(ctx, tx, v.Original); err != nil {
		return out, err
	}
	var duplicate string
	err = tx.QueryRow(ctx, `SELECT id FROM twitch_imports WHERE clip_id IS NOT NULL AND verified->>'sha256'=$1 AND id<>$2`, v.SHA256, id).Scan(&duplicate)
	if err == nil {
		if _, err = tx.Exec(ctx, `UPDATE twitch_imports SET rejected=TRUE,state='duplicate',error=$2,updated_at=now() WHERE id=$1`, id, "Identical media already belongs to import "+duplicate); err != nil {
			return out, err
		}
		if err = tx.Commit(ctx); err != nil {
			return out, err
		}
		return out, fmt.Errorf("%w: identical media already imported as %s", domain.ErrConflict, duplicate)
	}
	if err != pgx.ErrNoRows {
		return out, err
	}
	settings, err := readTwitchSettings(ctx, tx)
	if err != nil {
		return out, err
	}
	if v.DurationSec < settings.MinDurationSec || v.DurationSec > settings.MaxDurationSec+0.05 {
		return out, fmt.Errorf("%w: verified clip duration is outside configured range", domain.ErrInvalid)
	}
	var used bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_source_uses u JOIN accounts a ON a.remote_account_id=u.youtube_channel_id WHERE a.id=$1 AND u.format='short' AND u.media_id=$2)`, out.AccountID, v.SHA256).Scan(&used); err != nil {
		return out, err
	}
	if used {
		return out, fmt.Errorf("%w: исходник уже использован в Short этого канала", domain.ErrConflict)
	}
	jobID, clipID := uuid.NewString(), "twitch-"+id
	manifest := workflow.SourceManifest{Original: v.Original, DurationSec: v.DurationSec, Options: msg.JobOptions{Vertical: true, Shape: "rectangle"}, Cues: []workflow.Cue{}}
	title := out.Source.Title
	if login := strings.TrimSpace(out.Source.BroadcasterLogin); login != "" {
		suffix := " | " + login
		runes := []rune(title)
		limit := 100 - len([]rune(suffix))
		if limit < 1 {
			limit = 1
			suffix = ""
		}
		if len(runes) > limit {
			runes = runes[:limit]
		}
		title = string(runes) + suffix
	}
	title = publicTwitchTitle(title)
	f, err := flowByID(ctx, tx, "default")
	if err != nil {
		return out, err
	}
	rid, err := createTwitchDraft(ctx, tx, jobID, clipID, "twitch", "short", out.Source.URL, out.Source.ClipID, title, out.AccountID, settings.Timezone, manifest, now, flowMetadataInput(f, "short", title, []twitch.Source{out.Source}))
	if err != nil {
		return out, err
	}
	if err = registerAcceptedStagingTx(ctx, tx, out.Original, now); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_imports SET state='rendering',original=$2,verified=$3,job_id=$4,clip_id=$5,updated_at=$6,verified_at=COALESCE(verified_at,$6),error=NULL WHERE id=$1`, id, mustJSON(v.Original), mustJSON(v), jobID, clipID, now); err != nil {
		return out, err
	}
	if err = storeVerifiedMedia(ctx, tx, id, v, now); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO twitch_download_reservations(import_id,generation,broadcaster_id,created_at)VALUES($1,0,$2,$3) ON CONFLICT DO NOTHING`, id, out.Source.BroadcasterID, out.CreatedAt); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO twitch_flow_sources(id,flow_id,import_id,config,flow_version,created_at)SELECT $1,'default',$2,config,version,$3 FROM twitch_flows WHERE id='default' ON CONFLICT(flow_id,import_id)DO NOTHING`, uuid.NewString(), id, now); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO twitch_outputs(clip_id,flow_id,source_id,account_id,youtube_channel_id,format,config,initial_revision_id,created_at)SELECT $1,'default',$2,$3,COALESCE(a.remote_account_id,''),'short',f.config,$4,$5 FROM twitch_flows f JOIN accounts a ON a.id=$3 WHERE f.id='default' ON CONFLICT DO NOTHING`, clipID, id, out.AccountID, rid, now); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO twitch_source_uses(youtube_channel_id,format,media_id,clip_id)SELECT COALESCE(remote_account_id,''),'short',$2,$3 FROM accounts WHERE id=$1 AND COALESCE(remote_account_id,'')<>'' ON CONFLICT DO NOTHING`, out.AccountID, v.SHA256, clipID); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_flows SET work_started_at=COALESCE(work_started_at,$1)WHERE id='default'`, now); err != nil {
		return out, err
	}
	out, err = importByID(ctx, tx, id)
	out.RevisionID = rid
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func createTwitchDraft(ctx context.Context, tx pgx.Tx, jobID, clipID, kind, format, sourceURL, sourceID, title string, accountID int64, zone string, manifest workflow.SourceManifest, now time.Time, metadata ...twitch.MetadataInput) (int64, error) {
	if _, err := tx.Exec(ctx, `INSERT INTO jobs(job_id,youtube_url,source_kind,source_url,source_id,output_format,is_vertical,is_subtitles,slice_mode,approve_mode,status)VALUES($1,NULL,$2,$3,$4,$5,$6,FALSE,'full_clip','by_approve','processing')`, jobID, kind, sourceURL, sourceID, format, manifest.Options.Vertical); err != nil {
		return 0, err
	}
	sj := jobID + "-target"
	if _, err := tx.Exec(ctx, `INSERT INTO sub_jobs(sub_job_id,job_id,account_ids)VALUES($1,$2,$3)`, sj, jobID, []int64{accountID}); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO sub_job_targets(sub_job_id,account_id,timezone)VALUES($1,$2,$3)`, sj, accountID, zone); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clips(clip_id,job_id,bucket,object,start_sec,end_sec,title)VALUES($1,$2,'clips','',0,$3,$4)`, clipID, jobID, manifest.DurationSec, title); err != nil {
		return 0, err
	}
	spec := domain.EditSpec{StartSec: 0, EndSec: manifest.DurationSec, Title: title, Description: sourceURL, Visibility: "public", Hashtags: []string{}, Cues: []domain.Cue{}}
	if len(metadata) > 0 {
		if err := applyTwitchMetadata(&spec, metadata[0]); err != nil {
			return 0, err
		}
	}
	var rid int64
	if err := tx.QueryRow(ctx, `INSERT INTO clip_revisions(clip_id,revision_no,spec,render_state,manifest)VALUES($1,1,$2,'pending',$3) RETURNING id`, clipID, mustJSON(spec), mustJSON(manifest)).Scan(&rid); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE clips SET latest_revision_id=$2 WHERE clip_id=$1`, clipID, rid); err != nil {
		return 0, err
	}
	return rid, queueInitialRender(ctx, tx, rid, 1, now)
}
func queueInitialRender(ctx context.Context, tx pgx.Tx, rid int64, version int, now time.Time) error {
	return queueRenderAttempt(ctx, tx, rid, version, now)
}
func (s *Store) RejectTwitchImport(ctx context.Context, id string, rejected bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	var clip string
	err = tx.QueryRow(ctx, `UPDATE twitch_imports SET rejected=$2,updated_at=now() WHERE id=$1 RETURNING COALESCE(clip_id,'')`, id, rejected).Scan(&clip)
	if err != nil {
		return mapErr(err)
	}
	if rejected && clip != "" {
		if err = rejectTwitchDependencies(ctx, tx, clip); err != nil {
			return err
		}
	} else if !rejected && clip != "" {
		if _, err = tx.Exec(ctx, `UPDATE clips SET approval='pending' WHERE clip_id=$1`, clip); err != nil {
			return err
		}
	}
	if rejected {
		if err = rejectFlowDependencies(ctx, tx, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func rejectTwitchDependencies(ctx context.Context, tx pgx.Tx, clip string) error {
	var imported bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_imports WHERE clip_id=$1)`, clip).Scan(&imported); err != nil {
		return err
	}
	if !imported {
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE clips SET approval='rejected' WHERE clip_id=$1`, clip); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE clip_revisions SET approved_at=NULL WHERE clip_id=$1 AND id=(SELECT latest_revision_id FROM clips WHERE clip_id=$1)`, clip); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE clip_revisions r SET approved_at=NULL FROM twitch_compilation_items i WHERE i.source_clip_id=$1 AND i.compilation_revision_id=r.id AND r.id IN(SELECT latest_revision_id FROM clips)`, clip); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE clips SET approval='pending' WHERE latest_revision_id IN(SELECT compilation_revision_id FROM twitch_compilation_items WHERE source_clip_id=$1)`, clip); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE publications SET status=CASE WHEN clip_id=$1 THEN 'cancelled' ELSE 'on_hold' END,cancel_requested=(clip_id=$1),version=version+1,error='A source was rejected' WHERE started_at IS NULL AND status NOT IN ('published','cancelled') AND (clip_id=$1 OR revision_id IN(SELECT compilation_revision_id FROM twitch_compilation_items WHERE source_clip_id=$1))`, clip)
	return err
}
