package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"math"
	"strings"
	"terex/main-be/internal/domain"
	"terex/proto/msg"
	"terex/proto/twitch"
	"terex/proto/workflow"
	"time"
)

func sourceMatches(f twitch.Flow, src twitch.Source) bool {
	if f.Language != "" && (f.Language != src.Language || src.LanguageSource != "clip") {
		return false
	}
	if f.SourceMode == "category" {
		return src.GameID == f.CategoryID
	}
	if f.LegacyLength && len(f.BroadcasterIDs) == 0 {
		return true
	}
	for _, id := range f.BroadcasterIDs {
		if id == src.BroadcasterID {
			return true
		}
	}
	return false
}
func sourceEligible(f twitch.Flow, src twitch.Source, now time.Time) bool {
	return sourceMatches(f, src) && src.ViewCount >= f.MinViews && src.DurationSec >= f.MinDurationSec && src.DurationSec <= f.MaxDurationSec && !src.CreatedAt.Before(now.Add(-time.Duration(f.WindowHours)*time.Hour)) && !src.CreatedAt.After(now.Add(-time.Duration(f.MinAgeMinutes)*time.Minute)) && preparationSourceFresh(f, src, now, now)
}
func attachFlowSource(ctx context.Context, tx pgx.Tx, f twitch.Flow, src twitch.Source, now time.Time) (string, error) {
	matchConfig := f
	matchConfig.Language = ""
	if !sourceMatches(matchConfig, src) {
		return "", fmt.Errorf("%w: исходник не соответствует источникам потока", domain.ErrInvalid)
	}
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM twitch_imports WHERE twitch_clip_id=$1`, src.ClipID).Scan(&id)
	if err == pgx.ErrNoRows {
		id = uuid.NewString()
		epoch, _, e := twitchResetEpoch(ctx, tx)
		if e != nil {
			return "", e
		}
		r := twitch.ImportRequest{SchemaVersion: 2, IdempotencyKey: "source:" + src.ClipID, Source: src, FlowID: f.ID, FlowVersion: f.Version, AccountID: f.AccountID, Epoch: epoch}
		if _, err = tx.Exec(ctx, `INSERT INTO twitch_imports(id,idempotency_key,twitch_clip_id,request,state,created_at,updated_at,epoch)VALUES($1,$2,$3,$4,'discovered',$5,$5,$6)`, id, r.IdempotencyKey, src.ClipID, mustJSON(r), now, epoch); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	if !src.ObservedAt.IsZero() {
		if _, err = tx.Exec(ctx, `UPDATE twitch_imports SET request=jsonb_set(request,'{source}',$2::jsonb),updated_at=$3 WHERE id=$1 AND COALESCE((request->'source'->>'observed_at')::timestamptz,'epoch')<=$3`, id, mustJSON(src), src.ObservedAt); err != nil {
			return "", err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO twitch_import_keys(idempotency_key,import_id)VALUES($1,$2)ON CONFLICT DO NOTHING`, "source:"+src.ClipID, id); err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO twitch_flow_sources(id,flow_id,import_id,config,flow_version,created_at)VALUES($1,$2,$3,$4,$5,$6)ON CONFLICT(flow_id,import_id)DO UPDATE SET config=EXCLUDED.config,flow_version=EXCLUDED.flow_version WHERE twitch_flow_sources.reserved_at IS NULL`, uuid.NewString(), f.ID, id, mustJSON(f), f.Version, now)
	return id, err
}
func flowImport(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id, flow string) (twitch.Import, error) {
	out, err := importByID(ctx, q, id)
	if err != nil {
		return out, err
	}
	var config []byte
	var excluded bool
	err = q.QueryRow(ctx, `SELECT fs.id,fs.config,fs.excluded,COALESCE(o.clip_id,''),COALESCE(c.job_id,''),COALESCE(c.latest_revision_id,0),fs.flow_version FROM twitch_flow_sources fs LEFT JOIN twitch_outputs o ON o.flow_id=fs.flow_id AND o.source_id=fs.import_id AND o.format='short' LEFT JOIN clips c ON c.clip_id=o.clip_id WHERE fs.flow_id=$1 AND fs.import_id=$2`, flow, id).Scan(&out.ReservationID, &config, &excluded, &out.ClipID, &out.JobID, &out.RevisionID, &out.FlowVersion)
	if err != nil {
		return out, mapErr(err)
	}
	var f twitch.Flow
	if err = json.Unmarshal(config, &f); err != nil {
		return out, err
	}
	out.FlowID = flow
	out.AccountID = f.AccountID
	out.Excluded = excluded
	if err = q.QueryRow(ctx, `SELECT COALESCE((SELECT primary_format FROM twitch_download_grants WHERE import_id=$1),'')`, id).Scan(&out.PreparationFormat); err != nil {
		return out, err
	}
	return out, nil
}
func (s *Store) GetFlowImport(ctx context.Context, id, flow string) (twitch.Import, error) {
	return flowImport(ctx, s.pool, id, flow)
}
func (s *Store) ObserveTwitchSources(ctx context.Context, r twitch.ObservationRequest, now time.Time) ([]twitch.Import, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return nil, err
	}
	if err = twitchResetRequestAllowed(ctx, tx, r.Epoch); err != nil {
		return nil, err
	}
	f, err := flowByID(ctx, tx, r.FlowID)
	if err != nil {
		return nil, err
	}
	if f.Version != r.FlowVersion || !f.Enabled {
		return nil, fmt.Errorf("%w: поток остановлен или его настройки изменились", domain.ErrConflict)
	}
	if err = confirmedFlow(ctx, tx, f); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_flows SET last_checked_at=$2 WHERE id=$1`, f.ID, now); err != nil {
		return nil, err
	}
	out := []twitch.Import{}
	for _, src := range r.Sources {
		id, e := attachFlowSource(ctx, tx, f, src, now)
		if e != nil {
			return nil, e
		}
		if e = createFlowShort(ctx, tx, id, f.ID, now); e != nil {
			return nil, e
		}
		v, e := flowImport(ctx, tx, id, f.ID)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, tx.Commit(ctx)
}
func (s *Store) ReserveFlowImport(ctx context.Context, r twitch.ImportRequest, now time.Time) (twitch.Import, error) {
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
	f, err := flowByID(ctx, tx, r.FlowID)
	// A shared download keeps its original reservation even when its owner is
	// archived. Only an existing reservation may read that historical owner.
	if errors.Is(err, domain.ErrNotFound) && r.ReservationID != "" && r.UploadGeneration > 0 {
		f, err = scanFlow(tx.QueryRow(ctx, `SELECT `+flowCols+` FROM twitch_flows WHERE id=$1`, r.FlowID))
	}
	if err != nil {
		return twitch.Import{}, err
	}
	currentFilters := f
	var pinned []byte
	var savedVersion int
	var reservedAt *time.Time
	pinErr := tx.QueryRow(ctx, `SELECT fs.config,fs.flow_version,fs.reserved_at FROM twitch_flow_sources fs JOIN twitch_imports i ON i.id=fs.import_id WHERE fs.flow_id=$1 AND i.twitch_clip_id=$2`, f.ID, r.Source.ClipID).Scan(&pinned, &savedVersion, &reservedAt)
	if pinErr != nil && pinErr != pgx.ErrNoRows {
		return twitch.Import{}, pinErr
	}
	continuing := reservedAt != nil && r.ReservationID != "" && r.UploadGeneration > 0
	if continuing {
		var active bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_flow_sources fs JOIN twitch_flows f ON f.id=fs.flow_id JOIN accounts a ON a.id=f.account_id JOIN twitch_imports i ON i.id=fs.import_id WHERE i.twitch_clip_id=$1 AND NOT i.rejected AND NOT fs.excluded AND f.enabled AND NOT f.archived AND a.connection_state='connected' AND a.remote_account_id=f.youtube_channel_id AND(fs.reserved_at IS NOT NULL OR (`+currentFlowSourceEligible+`)))`, r.Source.ClipID).Scan(&active); err != nil {
			return twitch.Import{}, err
		}
		if !active {
			return twitch.Import{}, fmt.Errorf("%w: нет активных потребителей исходника", domain.ErrConflict)
		}
	}
	if reservedAt != nil && savedVersion == r.FlowVersion {
		var snapshot twitch.Flow
		if json.Unmarshal(pinned, &snapshot) != nil {
			return twitch.Import{}, domain.ErrInvalid
		}
		snapshot.Version = savedVersion
		snapshot.Enabled = f.Enabled
		f = snapshot
	}
	if f.Version != r.FlowVersion || (!f.Enabled && !continuing) {
		return twitch.Import{}, fmt.Errorf("%w: поток остановлен или его настройки изменились", domain.ErrConflict)
	}
	if !continuing {
		if err = confirmedFlow(ctx, tx, f); err != nil {
			return twitch.Import{}, err
		}
	}
	if !sourceMatches(currentFilters, r.Source) || (reservedAt == nil && !sourceEligible(f, r.Source, now)) {
		return twitch.Import{}, fmt.Errorf("%w: исходник не соответствует фильтрам потока", domain.ErrConflict)
	}
	var other string
	err = tx.QueryRow(ctx, `SELECT i.twitch_clip_id FROM twitch_import_keys k JOIN twitch_imports i ON i.id=k.import_id WHERE k.idempotency_key=$1`, r.IdempotencyKey).Scan(&other)
	if err != nil && err != pgx.ErrNoRows {
		return twitch.Import{}, err
	}
	if other != "" && other != r.Source.ClipID {
		return twitch.Import{}, domain.ErrConflict
	}
	id, err := attachFlowSource(ctx, tx, f, r.Source, now)
	if err != nil {
		return twitch.Import{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO twitch_import_keys(idempotency_key,import_id)VALUES($1,$2)ON CONFLICT DO NOTHING`, r.IdempotencyKey, id); err != nil {
		return twitch.Import{}, err
	}
	out, err := flowImport(ctx, tx, id, f.ID)
	if err != nil {
		return out, err
	}
	settings, e := readTwitchSettings(ctx, tx)
	if e != nil {
		return out, e
	}
	if settings.AdmissionMode == "enforce" && out.MediaState != "ready" && !continuing {
		var granted bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_download_grants WHERE import_id=$1 AND flow_id=$2 AND epoch=$3 AND(state='consumed' OR(state='active' AND expires_at>$4)))`, id, f.ID, r.Epoch, now).Scan(&granted); err != nil {
			return out, err
		}
		if !granted {
			return out, waitPreparation("allocation_wait", "Ожидает распределения общего бюджета скачиваний", now)
		}
	}
	if r.ReservationID != "" && r.ReservationID != out.ReservationID {
		return out, domain.ErrConflict
	}
	if continuing && r.UploadGeneration != out.UploadGeneration {
		var sameRetry bool
		if err = tx.QueryRow(ctx, `SELECT COALESCE(request->>'flow_id'=$2 AND request->>'reservation_id'=$3 AND request->>'idempotency_key'=$4 AND COALESCE((request->>'upload_parent_generation')::bigint,-1)=$5 AND upload_generation>$5,FALSE) FROM twitch_imports WHERE id=$1`, id, r.FlowID, r.ReservationID, r.IdempotencyKey, r.UploadGeneration).Scan(&sameRetry); err != nil {
			return out, err
		}
		if !sameRetry {
			return out, fmt.Errorf("%w: upload generation changed", domain.ErrConflict)
		}
		// The renewal response may have been lost before the collector persisted
		// it. Return the issued generation, without allocating a third attempt.
		if out.State != "discovered" {
			if err = admitImportConsumersTx(ctx, tx, id, f.ID, continuing, now); err != nil {
				return out, err
			}
			return out, tx.Commit(ctx)
		}
	}
	if out.Rejected || (!continuing && out.Excluded) {
		return out, fmt.Errorf("%w: исходник исключён", domain.ErrConflict)
	}
	if out.MediaState == "expired" {
		return out, waitPreparation("media_expired", "Файл удалён по сроку хранения; исходник повторно автоматически не скачивается", now)
	}
	if err = admitImportConsumersTx(ctx, tx, id, f.ID, continuing, now); err != nil {
		return out, err
	}
	if out.MediaState == "ready" {
		if err = createFlowShort(ctx, tx, id, f.ID, now); err != nil {
			return out, err
		}
		out, err = flowImport(ctx, tx, id, f.ID)
		if err != nil {
			return out, err
		}
		return out, tx.Commit(ctx)
	}
	var until *time.Time
	var generation int64
	var schema int
	if err = tx.QueryRow(ctx, `SELECT upload_claim_until,upload_generation,COALESCE((request->>'schema_version')::int,1) FROM twitch_imports WHERE id=$1 FOR UPDATE`, id).Scan(&until, &generation, &schema); err != nil {
		return out, err
	}
	if (until != nil && until.After(now)) || (schema == 1 && out.UploadExpiresAt != nil && out.UploadExpiresAt.After(now)) {
		return out, tx.Commit(ctx)
	}
	settings, e = readTwitchSettings(ctx, tx)
	if e != nil {
		return out, e
	}
	if !settings.Enabled {
		return out, fmt.Errorf("%w: сбор Twitch выключен", domain.ErrConflict)
	}
	loc, _ := time.LoadLocation(settings.Timezone)
	day := now.In(loc).Format("2006-01-02")
	var total, channel, backlog int
	if err = tx.QueryRow(ctx, `WITH reserved AS(SELECT import_id,broadcaster_id FROM twitch_download_reservations WHERE(created_at AT TIME ZONE $1)::date=$2::date AND import_id<>$4 UNION SELECT g.import_id,i.request->'source'->>'broadcaster_id' FROM twitch_download_grants g JOIN twitch_imports i ON i.id=g.import_id WHERE(g.created_at AT TIME ZONE $1)::date=$2::date AND g.import_id<>$4 AND g.state<>'released') SELECT count(DISTINCT import_id),count(DISTINCT import_id)FILTER(WHERE broadcaster_id=$3)FROM reserved`, settings.Timezone, day, r.Source.BroadcasterID, id).Scan(&total, &channel); err != nil {
		return out, err
	}
	if total >= settings.DailyDownloads || channel >= settings.PerChannelDailyDownloads {
		return out, fmt.Errorf("%w: достигнут общий лимит скачивания", domain.ErrConflict)
	}
	if err = tx.QueryRow(ctx, `SELECT count(DISTINCT COALESCE(i.media_id,i.id)) FROM twitch_imports i WHERE `+pendingTwitchSource).Scan(&backlog); err != nil {
		return out, err
	}
	if backlog >= settings.BacklogLimit {
		return out, fmt.Errorf("%w: очередь проверки заполнена", domain.ErrConflict)
	}
	parentGeneration := r.UploadGeneration
	generation++
	r.AccountID = f.AccountID
	r.ReservationID = out.ReservationID
	r.UploadGeneration = generation
	if _, err = tx.Exec(ctx, `UPDATE twitch_imports SET request=$2::jsonb||jsonb_build_object('upload_parent_generation',$6::bigint),state='upload_pending',media_state='pending',original='{}',upload_expires_at=NULL,upload_generation=$3,upload_claim_until=$4,download_reserved_at=$5,error=NULL,updated_at=$5 WHERE id=$1`, id, mustJSON(r), generation, now.Add(15*time.Minute), now, parentGeneration); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO twitch_download_reservations(import_id,generation,broadcaster_id,created_at)VALUES($1,$2,$3,$4)`, id, generation, r.Source.BroadcasterID, now); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_flows SET work_started_at=COALESCE(work_started_at,$2)WHERE id=$1`, f.ID, now); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_flow_sources SET reserved_at=COALESCE(reserved_at,$3)WHERE flow_id=$1 AND import_id=$2`, f.ID, id, now); err != nil {
		return out, err
	}
	out, err = flowImport(ctx, tx, id, f.ID)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *Store) SetFlowUpload(ctx context.Context, id string, generation int64, ref workflow.ObjectRef, expires time.Time) error {
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
	tag, err := tx.Exec(ctx, `UPDATE twitch_imports SET original=$3,upload_expires_at=$4 WHERE id=$1 AND upload_generation=$2 AND media_state<>'ready' AND state='upload_pending'`, id, generation, mustJSON(ref), expires)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrConflict
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func storeVerifiedMedia(ctx context.Context, tx pgx.Tx, id string, v *workflow.VerifiedMedia, now time.Time) error {
	if err := twitchResetAllowed(ctx, tx); err != nil {
		return err
	}
	if err := storageRefsAvailable(ctx, tx, v.Original); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO twitch_media(id,sha256,verified,created_at)VALUES($1,$1,$2,$3)ON CONFLICT(sha256)DO NOTHING`, v.SHA256, mustJSON(v), now); err != nil {
		return err
	}
	// The winning immutable file is used by all aliases, including concurrent SHA matches.
	tag, err := tx.Exec(ctx, `UPDATE twitch_imports i SET media_id=m.id,media_state='ready',verified=m.verified,original=m.verified->'original',error=NULL,updated_at=$3,verified_at=COALESCE(verified_at,$3) FROM twitch_media m WHERE i.id=$1 AND m.sha256=$2 AND m.storage_state='available'`, id, v.SHA256, now)
	if err == nil && tag.RowsAffected() == 0 {
		return waitPreparation("media_expired", "Общий исходник уже удалён или удаляется", now)
	}
	return err
}
func (s *Store) FinishFlowImport(ctx context.Context, id string, generation int64, v *workflow.VerifiedMedia, now time.Time) (twitch.Import, error) {
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
	out, err := importByID(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if generation != out.UploadGeneration || generation <= 0 {
		return out, fmt.Errorf("%w: загрузка уже заменена новой попыткой", domain.ErrConflict)
	}
	if out.MediaState == "ready" {
		return out, nil
	}
	if out.State != "upload_pending" {
		return out, fmt.Errorf("%w: upload reservation was reset", domain.ErrConflict)
	}
	if out.Rejected {
		return out, domain.ErrConflict
	}
	if v == nil || v.Original.Object == "" || v.SHA256 == "" || v.DurationSec <= 0 || v.DurationSec > 60.05 {
		return out, domain.ErrInvalid
	}
	if err = storeVerifiedMedia(ctx, tx, id, v, now); err != nil {
		return out, err
	}
	if err = registerAcceptedStagingTx(ctx, tx, out.Original, now); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_imports SET state='ready',upload_claim_until=NULL WHERE id=$1`, id); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_preparation_sources SET duration_sec=$2 WHERE import_id=$1`, id, math.Ceil(v.DurationSec*30-1e-7)/30); err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT flow_id FROM twitch_flow_sources WHERE import_id=$1 AND NOT excluded`, id)
	if err != nil {
		return out, err
	}
	flows := []string{}
	for rows.Next() {
		var f string
		if err = rows.Scan(&f); err != nil {
			rows.Close()
			return out, err
		}
		flows = append(flows, f)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	for _, f := range flows {
		if err = createFlowShort(ctx, tx, id, f, now); err != nil {
			return out, err
		}
	}
	out, err = importByID(ctx, tx, id)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func createFlowShort(ctx context.Context, tx pgx.Tx, id, flow string, now time.Time) error {
	current, err := flowByID(ctx, tx, flow)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !current.Enabled {
		return nil
	}
	if err = confirmedFlow(ctx, tx, current); err != nil {
		return nil
	}
	var b, vb []byte
	var excluded bool
	var media string
	var reservedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT fs.config,fs.excluded,m.verified,m.id,fs.reserved_at FROM twitch_flow_sources fs JOIN twitch_imports i ON i.id=fs.import_id JOIN twitch_media m ON m.id=i.media_id WHERE fs.flow_id=$1 AND fs.import_id=$2 AND NOT i.rejected AND i.media_state='ready' AND m.storage_state='available'`, flow, id).Scan(&b, &excluded, &vb, &media, &reservedAt)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if excluded {
		return nil
	}
	var f twitch.Flow
	var v workflow.VerifiedMedia
	if json.Unmarshal(b, &f) != nil || json.Unmarshal(vb, &v) != nil {
		return domain.ErrInvalid
	}
	if reservedAt == nil {
		f = current
	}
	if !f.ShortsEnabled || v.DurationSec < f.MinDurationSec || v.DurationSec > f.MaxDurationSec+0.05 {
		return nil
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_source_uses WHERE youtube_channel_id=$1 AND format='short' AND media_id=$2) OR EXISTS(SELECT 1 FROM twitch_outputs WHERE flow_id=$3 AND source_id=$4 AND format='short')`, f.YouTubeChannelID, media, flow, id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	out, err := importByID(ctx, tx, id)
	if err != nil {
		return err
	}
	eligibilityAt := now
	if reservedAt != nil {
		eligibilityAt = *reservedAt
	}
	if !sourceEligible(f, out.Source, eligibilityAt) || !sourceMatches(current, out.Source) {
		return nil
	}
	if !preparationSourceFresh(current, out.Source, now, now) {
		return nil
	}
	if err = storageRefsAvailable(ctx, tx, v.Original); err != nil {
		return err
	}
	clip, jid := "twitch-"+uuid.NewString(), uuid.NewString()
	if _, err = reserveOutputPreparationTx(ctx, tx, current, "", "short", []string{id}, now); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return nil
		}
		return err
	}
	title := strings.TrimSpace(out.Source.Title + " | " + out.Source.BroadcasterLogin)
	if len([]rune(title)) > 100 {
		title = string([]rune(title)[:100])
	}
	manifest := workflow.SourceManifest{Original: v.Original, DurationSec: v.DurationSec, Options: msg.JobOptions{Vertical: true, Shape: "rectangle"}, Cues: []workflow.Cue{}}
	rid, err := createTwitchDraft(ctx, tx, jid, clip, "twitch", "short", out.Source.URL, out.Source.ClipID, title, f.AccountID, f.Timezone, manifest, now)
	if err != nil {
		return err
	}
	auto := f.AutoPublish && current.AutoPublish && f.AutoEnabledAt != nil && !now.Before(*f.AutoEnabledAt)
	if _, err = tx.Exec(ctx, `INSERT INTO twitch_outputs(clip_id,flow_id,source_id,account_id,youtube_channel_id,format,config,initial_revision_id,auto_eligible,created_at)VALUES($1,$2,$3,$4,$5,'short',$6,$7,$8,$9)`, clip, flow, id, f.AccountID, f.YouTubeChannelID, mustJSON(f), rid, auto, now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_preparation_reservations r SET clip_id=$3 WHERE r.flow_id=$1 AND r.format='short' AND r.state='active' AND r.clip_id IS NULL AND EXISTS(SELECT 1 FROM twitch_preparation_sources ps WHERE ps.reservation_id=r.id AND ps.import_id=$2)`, flow, id, clip); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO twitch_source_uses(youtube_channel_id,format,media_id,clip_id,created_at)VALUES($1,'short',$2,$3,$4)`, f.YouTubeChannelID, media, clip, now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_flow_sources SET reserved_at=COALESCE(reserved_at,$3),config=$4,flow_version=$5 WHERE flow_id=$1 AND import_id=$2`, flow, id, now, mustJSON(f), f.Version); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE twitch_flows SET work_started_at=COALESCE(work_started_at,$2)WHERE id=$1`, flow, now)
	return err
}
func (s *Store) ListFlowImports(ctx context.Context, flow string) ([]twitch.Import, error) {
	rows, err := s.pool.Query(ctx, `SELECT import_id FROM twitch_flow_sources WHERE flow_id=$1 ORDER BY created_at DESC LIMIT 1000`, flow)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := []twitch.Import{}
	for _, id := range ids {
		v, e := s.GetFlowImport(ctx, id, flow)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Store) TwitchSourcePreview(ctx context.Context, id, flow string) (workflow.ObjectRef, error) {
	var b []byte
	var mediaState, storageState string
	err := s.pool.QueryRow(ctx, `SELECT m.verified->'original',i.media_state,m.storage_state FROM twitch_imports i JOIN twitch_media m ON m.id=i.media_id WHERE i.id=$1 AND($2='' OR EXISTS(SELECT 1 FROM twitch_flow_sources WHERE flow_id=$2 AND import_id=i.id))`, id, flow).Scan(&b, &mediaState, &storageState)
	var ref workflow.ObjectRef
	if err != nil {
		return ref, mapErr(err)
	}
	if mediaState != "ready" || storageState != "available" {
		return ref, waitPreparation("media_expired", "Файл удалён по сроку хранения или ожидает удаления", time.Now().UTC())
	}
	err = json.Unmarshal(b, &ref)
	return ref, err
}

func (s *Store) BlockedTwitchSources(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT twitch_clip_id FROM twitch_imports WHERE rejected OR media_state='expired' ORDER BY twitch_clip_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func rejectFlowDependencies(ctx context.Context, tx pgx.Tx, id string) error {
	const impacted = `SELECT o.clip_id FROM twitch_outputs o JOIN clips c ON c.clip_id=o.clip_id WHERE o.source_id=$1 OR EXISTS(SELECT 1 FROM twitch_compilation_items ci WHERE ci.compilation_revision_id=c.latest_revision_id AND ci.source_id=$1)`
	if _, err := tx.Exec(ctx, `UPDATE twitch_outputs SET auto_eligible=FALSE WHERE clip_id IN(`+impacted+`)`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE clip_revisions SET approved_at=NULL WHERE id IN(SELECT latest_revision_id FROM clips WHERE clip_id IN(`+impacted+`))`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE clips c SET approval=CASE WHEN EXISTS(SELECT 1 FROM twitch_outputs o WHERE o.clip_id=c.clip_id AND o.source_id=$1)THEN 'rejected' ELSE 'pending' END WHERE clip_id IN(`+impacted+`)`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE publication_attempts SET status='superseded',lease_token=NULL,lease_until=NULL WHERE status='active' AND publication_id IN(SELECT id FROM publications WHERE started_at IS NULL AND clip_id IN(`+impacted+`))`, id); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE publications SET status='on_hold',auto_hold=FALSE,version=version+1,error='Исходник исключён глобально' WHERE started_at IS NULL AND status NOT IN('published','cancelled','needs_attention') AND clip_id IN(`+impacted+`)`, id)
	return err
}
