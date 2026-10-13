package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"log/slog"
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
func flowSourceConfig(f twitch.Flow) []byte {
	// Runtime display fields are not settings. In particular last_checked_at
	// changes for every discovery and must not rewrite every source snapshot.
	f.LastCheckedAt, f.NextPublicationAt = nil, nil
	f.FoundClips, f.ReadyOutputs = 0, 0
	f.BlockReason, f.ExcludedSourceIDs = "", nil
	return mustJSON(f)
}
func attachFlowSource(ctx context.Context, tx pgx.Tx, f twitch.Flow, src twitch.Source, now time.Time) (string, error) {
	matchConfig := f
	matchConfig.Language = ""
	if !sourceMatches(matchConfig, src) {
		return "", fmt.Errorf("%w: исходник не соответствует источникам потока", domain.ErrInvalid)
	}
	var growthErr error
	src, growthErr = recordTwitchGrowthTx(ctx, tx, f, src, now)
	if growthErr != nil {
		return "", growthErr
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
	_, err = tx.Exec(ctx, `INSERT INTO twitch_flow_sources(id,flow_id,import_id,config,flow_version,created_at)VALUES($1,$2,$3,$4,$5,$6)ON CONFLICT(flow_id,import_id)DO UPDATE SET config=EXCLUDED.config,flow_version=EXCLUDED.flow_version WHERE twitch_flow_sources.reserved_at IS NULL AND (twitch_flow_sources.config IS DISTINCT FROM EXCLUDED.config OR twitch_flow_sources.flow_version IS DISTINCT FROM EXCLUDED.flow_version)`, uuid.NewString(), f.ID, id, flowSourceConfig(f), f.Version, now)
	return id, err
}

// The import and its flow-specific output/grant are read in one query. The
// wrapper appends columns while reusing the canonical import decoder.
type flowImportRow struct {
	row   pgx.Row
	extra []any
}

func (r flowImportRow) Scan(dest ...any) error { return r.row.Scan(append(dest, r.extra...)...) }

const flowImportJoins = ` FROM twitch_imports i LEFT JOIN clips c ON c.clip_id=i.clip_id JOIN twitch_flow_sources fs ON fs.import_id=i.id LEFT JOIN twitch_outputs o ON o.flow_id=fs.flow_id AND o.source_id=fs.import_id AND o.format='short' LEFT JOIN clips fc ON fc.clip_id=o.clip_id LEFT JOIN twitch_download_grants g ON g.clip_id=i.twitch_clip_id AND g.import_id=i.id`
const flowImportExtraCols = `,fs.id,fs.config,fs.excluded,COALESCE(o.clip_id,''),COALESCE(fc.job_id,''),COALESCE(fc.latest_revision_id,0),fs.flow_version,COALESCE(g.primary_format,''),COALESCE(g.sequence,0),COALESCE(i.revalidation->>'failure_token','')`

func scanFlowImport(row pgx.Row, flow string) (twitch.Import, error) {
	var config []byte
	var reservationID, clipID, jobID, format, revalidationToken string
	var excluded bool
	var revisionID, sequence int64
	var version int
	out, err := scanImport(flowImportRow{row: row, extra: []any{&reservationID, &config, &excluded, &clipID, &jobID, &revisionID, &version, &format, &sequence, &revalidationToken}})
	if err != nil {
		return out, err
	}
	var f twitch.Flow
	if err = json.Unmarshal(config, &f); err != nil {
		return out, err
	}
	out.FlowID, out.AccountID, out.Excluded = flow, f.AccountID, excluded
	out.ReservationID, out.ClipID, out.JobID = reservationID, clipID, jobID
	out.RevisionID, out.FlowVersion = revisionID, version
	out.PreparationFormat, out.AdmissionSequence, out.RevalidationToken = format, sequence, revalidationToken
	return out, nil
}
func flowImport(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id, flow string) (twitch.Import, error) {
	return scanFlowImport(q.QueryRow(ctx, `SELECT `+importCols+flowImportExtraCols+flowImportJoins+` WHERE fs.flow_id=$1 AND i.id=$2`, flow, id), flow)
}
func flowImports(ctx context.Context, tx pgx.Tx, ids []string, flow string) ([]twitch.Import, error) {
	rows, err := tx.Query(ctx, `SELECT `+importCols+flowImportExtraCols+flowImportJoins+` WHERE fs.flow_id=$1 AND i.id=ANY($2::text[])`, flow, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	imports := make(map[string]twitch.Import, len(ids))
	for rows.Next() {
		out, e := scanFlowImport(rows, flow)
		if e != nil {
			return nil, e
		}
		imports[out.ImportID] = out
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := make([]twitch.Import, 0, len(ids))
	for _, id := range ids {
		v, ok := imports[id]
		if !ok {
			return nil, domain.ErrNotFound
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Store) GetFlowImport(ctx context.Context, id, flow string) (twitch.Import, error) {
	return flowImport(ctx, s.pool, id, flow)
}
func (s *Store) ObserveTwitchSources(ctx context.Context, r twitch.ObservationRequest, now time.Time) (out []twitch.Import, err error) {
	started := time.Now()
	phase, committed := "pool_acquire", false
	var poolWaitMS, lockMS, preloadMS, writeMS int64
	defer func() {
		slog.InfoContext(ctx, "twitch observations finished", "flow_id", r.FlowID, "flow_version", r.FlowVersion, "source_count", len(r.Sources), "phase", phase, "committed", committed, "pool_wait_ms", poolWaitMS, "lock_ms", lockMS, "preload_ms", preloadMS, "write_ms", writeMS, "duration_ms", time.Since(started).Milliseconds(), "err", err)
	}()
	tx, err := s.pool.Begin(ctx)
	poolWaitMS = time.Since(started).Milliseconds()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	phase = "coordinator_lock"
	lockStarted := time.Now()
	if err = lockTwitch(ctx, tx); err != nil {
		lockMS = time.Since(lockStarted).Milliseconds()
		return nil, err
	}
	lockMS = time.Since(lockStarted).Milliseconds()
	phase = "validate_flow"
	if err = twitchResetRequestAllowed(ctx, tx, r.Epoch); err != nil {
		return nil, err
	}
	f, err := scanFlow(tx.QueryRow(ctx, `SELECT `+collectorFlowCols+` FROM twitch_flows WHERE id=$1 AND NOT archived`, r.FlowID))
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
	matchConfig := f
	matchConfig.Language = ""
	for _, src := range r.Sources {
		if !sourceMatches(matchConfig, src) {
			return nil, fmt.Errorf("%w: исходник не соответствует источникам потока", domain.ErrInvalid)
		}
	}
	phase = "preload_growth"
	preloadStarted := time.Now()
	growth, err := loadTwitchGrowthBatch(ctx, tx, f, r.Sources, now)
	preloadMS = time.Since(preloadStarted).Milliseconds()
	if err != nil {
		return nil, err
	}
	epoch, _, err := twitchResetEpoch(ctx, tx)
	if err != nil {
		return nil, err
	}
	writes := &pgx.Batch{}
	ids := make([]string, 0, len(r.Sources))
	ready := map[string]bool{}
	config := flowSourceConfig(f)
	phase = "write_observations"
	writeStarted := time.Now()
	for _, src := range r.Sources {
		src, err = growth.record(ctx, tx, writes, src)
		if err != nil {
			return nil, err
		}
		c := growth.clips[src.ClipID]
		id := c.ImportID
		if id == "" {
			id = uuid.NewString()
			c.ImportID = id
			request := twitch.ImportRequest{SchemaVersion: 2, IdempotencyKey: "source:" + src.ClipID, Source: src, FlowID: f.ID, FlowVersion: f.Version, AccountID: f.AccountID, Epoch: epoch}
			writes.Queue(`INSERT INTO twitch_imports(id,idempotency_key,twitch_clip_id,request,state,created_at,updated_at,epoch)VALUES($1,$2,$3,$4,'discovered',$5,$5,$6)`, id, request.IdempotencyKey, src.ClipID, mustJSON(request), now, epoch)
		} else if !src.ObservedAt.IsZero() {
			writes.Queue(`UPDATE twitch_imports SET request=jsonb_set(request,'{source}',$2::jsonb),updated_at=$3 WHERE id=$1 AND COALESCE((request->'source'->>'observed_at')::timestamptz,'epoch')<=$3 AND request->'source' IS DISTINCT FROM $2::jsonb`, id, mustJSON(src), src.ObservedAt)
		}
		growth.metadataUpdated(src)
		writes.Queue(`INSERT INTO twitch_import_keys(idempotency_key,import_id)VALUES($1,$2)ON CONFLICT DO NOTHING`, "source:"+src.ClipID, id)
		writes.Queue(`INSERT INTO twitch_flow_sources(id,flow_id,import_id,config,flow_version,created_at)VALUES($1,$2,$3,$4,$5,$6)ON CONFLICT(flow_id,import_id)DO UPDATE SET config=EXCLUDED.config,flow_version=EXCLUDED.flow_version WHERE twitch_flow_sources.reserved_at IS NULL AND (twitch_flow_sources.config IS DISTINCT FROM EXCLUDED.config OR twitch_flow_sources.flow_version IS DISTINCT FROM EXCLUDED.flow_version)`, uuid.NewString(), f.ID, id, config, f.Version, now)
		ids = append(ids, id)
		ready[id] = c.MediaState == "ready"
	}
	if writes.Len() > 0 {
		if err = tx.SendBatch(ctx, writes).Close(); err != nil {
			writeMS = time.Since(writeStarted).Milliseconds()
			return nil, err
		}
	}
	writeMS = time.Since(writeStarted).Milliseconds()
	phase = "ready_outputs"
	// Most discoveries have no file. Only verified sources need the release
	// path; re-reading ready candidates preserves its existing atomic checks.
	for _, id := range ids {
		if ready[id] {
			if err = createFlowShortWithCurrent(ctx, tx, id, f, now); err != nil {
				return nil, err
			}
		}
	}
	phase = "response"
	out, err = flowImports(ctx, tx, ids, f.ID)
	if err != nil {
		return nil, err
	}
	phase = "commit"
	err = tx.Commit(ctx)
	committed = err == nil
	return out, err
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
	if !continuing && !currentFilters.Enabled {
		return twitch.Import{}, waitPreparation("flow_paused", "Поток приостановлен; скачивание продолжится после включения", now.Add(time.Minute))
	}
	if !continuing && currentFilters.Version != r.FlowVersion {
		return twitch.Import{}, waitPreparation("flow_version_changed", "Настройки потока изменились: исходник ожидает повторной проверки по текущим фильтрам", now.Add(time.Minute))
	}
	if continuing {
		var active bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_flow_sources fs JOIN twitch_flows f ON f.id=fs.flow_id JOIN accounts a ON a.id=f.account_id JOIN twitch_imports i ON i.id=fs.import_id WHERE i.twitch_clip_id=$1 AND NOT i.rejected AND NOT fs.excluded AND f.enabled AND NOT f.archived AND EXISTS(SELECT 1 FROM twitch_flow_destinations dest JOIN accounts da ON da.id=dest.account_id WHERE dest.flow_id=f.id AND dest.enabled AND da.connection_state='connected' AND da.remote_account_id=dest.remote_account_id) AND (`+currentFlowSourceEligible+`))`, r.Source.ClipID).Scan(&active); err != nil {
			return twitch.Import{}, err
		}
		if !active {
			return twitch.Import{}, waitPreparation("source_filtered", "Нет активного потока, которому исходник подходит по текущим фильтрам", now.Add(time.Minute))
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
		return twitch.Import{}, waitPreparation("flow_version_changed", "Настройки потока изменились; требуется повторная проверка исходника", now.Add(time.Minute))
	}
	if !continuing {
		if err = confirmedFlow(ctx, tx, f); err != nil {
			return twitch.Import{}, err
		}
	}
	// A pinned shared download may finish for another currently eligible flow;
	// the former owner does not decide that flow's language or source filters.
	if !continuing && (!sourceMatches(currentFilters, r.Source) || (reservedAt == nil && !sourceEligible(f, r.Source, now))) {
		if r.Source.CreatedAt.Before(now.Add(-time.Duration(f.WindowHours)*time.Hour)) || !preparationSourceFresh(f, r.Source, now, now) {
			return twitch.Import{}, waitPreparation("source_stale", "Клип устарел для текущего окна публикации и повторно не скачивается", now.Add(time.Minute))
		}
		return twitch.Import{}, waitPreparation("source_filtered", "Исходник не соответствует текущим языку, источникам, просмотрам или длительности потока", now.Add(time.Minute))
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
		if r.AdmissionSequence == 0 && out.RevalidationToken != "" {
			return out, fmt.Errorf("%w: admission sequence is required after revalidation", domain.ErrConflict)
		}
		var granted bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_download_grants WHERE import_id=$1 AND flow_id=$2 AND epoch=$3 AND($5::bigint=0 OR sequence=$5) AND(state='consumed' OR(state='active' AND expires_at>$4)))`, id, f.ID, r.Epoch, now, r.AdmissionSequence).Scan(&granted); err != nil {
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
	if settings.DailyDownloadLimitReached(total) {
		return out, waitPreparation("daily_download_limit", "Достигнут общий дневной лимит скачиваний", now)
	}
	if channel >= settings.PerChannelDailyDownloads {
		return out, waitPreparation("broadcaster_daily_limit", "Достигнут дневной лимит скачиваний для этого стримера", now)
	}
	if err = tx.QueryRow(ctx, `SELECT count(DISTINCT COALESCE(i.media_id,i.id)) FROM twitch_imports i WHERE i.id<>$1 AND (`+pendingTwitchSource+`)`, id).Scan(&backlog); err != nil {
		return out, err
	}
	if backlog >= settings.BacklogLimit {
		return out, waitPreparation("backlog_full", "Достигнут лимит уникальных исходников в подготовке; существующие загрузки продолжают проверяться", now)
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
	if v == nil || v.Original.Object == "" || v.SHA256 == "" || v.DurationSec <= 0 || v.DurationSec > twitch.MaxClipDurationSec+0.05 {
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
	current, err := scanFlow(tx.QueryRow(ctx, `SELECT `+collectorFlowCols+` FROM twitch_flows WHERE id=$1 AND NOT archived`, flow))
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return createFlowShortWithCurrent(ctx, tx, id, current, now)
}

// Observe holds the coordinator lock and has already loaded the current flow.
// Reuse it instead of calculating UI counts once for each discovered source.
func createFlowShortWithCurrent(ctx context.Context, tx pgx.Tx, id string, current twitch.Flow, now time.Time) error {
	flow := current.ID
	if !current.Enabled {
		return nil
	}
	var b, vb []byte
	var excluded bool
	var media string
	var reservedAt *time.Time
	err := tx.QueryRow(ctx, `SELECT fs.config,fs.excluded,m.verified,m.id,fs.reserved_at FROM twitch_flow_sources fs JOIN twitch_imports i ON i.id=fs.import_id JOIN twitch_media m ON m.id=i.media_id WHERE fs.flow_id=$1 AND fs.import_id=$2 AND NOT i.rejected AND i.media_state='ready' AND m.storage_state='available'`, flow, id).Scan(&b, &excluded, &vb, &media, &reservedAt)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if excluded {
		return nil
	}
	if err = confirmedFlow(ctx, tx, current); err != nil {
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
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_outputs WHERE flow_id=$1 AND source_id=$2 AND format='short')`, flow, id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	var eligibleDestinations []twitch.Destination
	for _, d := range current.PublicationDestinations() {
		if !d.Enabled {
			continue
		}
		var eligible bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE id=$1 AND connection_state='connected' AND remote_account_id=$2) AND NOT EXISTS(SELECT 1 FROM twitch_source_uses WHERE youtube_channel_id=$3 AND format='short' AND media_id=$4)`, d.AccountID, d.RemoteAccountID, d.CapacityKey(), media).Scan(&eligible); err != nil {
			return err
		}
		if eligible {
			eligibleDestinations = append(eligibleDestinations, d)
		}
	}
	if len(eligibleDestinations) == 0 {
		return nil
	}
	current.Destinations = eligibleDestinations
	f = destinationFlow(f, eligibleDestinations[0])
	f.Destinations = eligibleDestinations
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
	title := publicTwitchTitle(strings.TrimSpace(out.Source.Title + " | " + out.Source.BroadcasterLogin))
	if len([]rune(title)) > 100 {
		title = string([]rune(title)[:100])
	}
	// Bad publication text must not roll back successful media verification.
	// The ready source can produce its release after the flow settings are fixed.
	if metadata := twitch.GenerateMetadata(flowMetadataInput(current, "short", title, []twitch.Source{out.Source})); len(metadata.Errors) > 0 {
		slog.WarnContext(ctx, "Twitch source ready; release metadata invalid", "flow_id", flow, "import_id", id, "reason", "metadata_invalid", "errors", metadata.Errors)
		return nil
	}
	if _, err = reserveOutputPreparationTx(ctx, tx, current, "", "short", []string{id}, now); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return nil
		}
		return err
	}
	manifest := workflow.SourceManifest{Original: v.Original, DurationSec: v.DurationSec, Options: msg.JobOptions{Vertical: true, Shape: "rectangle"}, Cues: []workflow.Cue{}}
	// Publication templates apply when a new release is created, even if its
	// physical download was reserved under an older flow configuration.
	f.Metadata = current.Metadata
	f.Destinations = current.Destinations
	rid, err := createTwitchDraft(ctx, tx, jid, clip, "twitch", "short", out.Source.URL, out.Source.ClipID, title, f.AccountID, f.Timezone, manifest, now, flowMetadataInput(current, "short", title, []twitch.Source{out.Source}))
	if err != nil {
		return err
	}
	auto := f.AutoPublish && current.AutoPublish && f.AutoEnabledAt != nil && !now.Before(*f.AutoEnabledAt)
	if _, err = tx.Exec(ctx, `INSERT INTO twitch_outputs(clip_id,flow_id,source_id,account_id,youtube_channel_id,format,config,initial_revision_id,auto_eligible,created_at)VALUES($1,$2,$3,$4,$5,'short',$6,$7,$8,$9)`, clip, flow, id, f.AccountID, flowDestinationKey(f), mustJSON(f), rid, auto, now); err != nil {
		return err
	}
	if err = snapshotOutputDestinations(ctx, tx, clip, current, "short", auto, now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_preparation_reservations r SET clip_id=$3 WHERE r.flow_id=$1 AND r.format='short' AND r.state='active' AND r.clip_id IS NULL AND EXISTS(SELECT 1 FROM twitch_preparation_sources ps WHERE ps.reservation_id=r.id AND ps.import_id=$2)`, flow, id, clip); err != nil {
		return err
	}
	for _, d := range current.PublicationDestinations() {
		if !d.Enabled {
			continue
		}
		if _, err = tx.Exec(ctx, `INSERT INTO twitch_source_uses(youtube_channel_id,format,media_id,clip_id,created_at)VALUES($1,'short',$2,$3,$4)ON CONFLICT DO NOTHING`, d.CapacityKey(), media, clip, now); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_flow_sources SET reserved_at=COALESCE(reserved_at,$3),config=$4,flow_version=$5 WHERE flow_id=$1 AND import_id=$2`, flow, id, now, mustJSON(f), f.Version); err != nil {
		return err
	}
	if err = tightenQueuedRender(ctx, tx, rid, now); err != nil {
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
