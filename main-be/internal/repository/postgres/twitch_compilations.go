package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"math"
	"reflect"
	"strings"
	"terex/main-be/internal/domain"
	"terex/proto/msg"
	"terex/proto/twitch"
	"terex/proto/workflow"
	"time"
)

const compilationCols = `c.clip_id,r.id,r.revision_no,COALESCE(r.spec->>'title',''),r.render_state,r.approved_at,r.created_at,COALESCE(r.bucket,''),COALESCE(r.object,''),r.manifest,COALESCE(r.error,''),COALESCE(t.flow_id,''),(SELECT config FROM twitch_outputs WHERE clip_id=c.clip_id)`

func scanCompilation(row pgx.Row) (domain.Compilation, error) {
	var c domain.Compilation
	var data []byte
	var config []byte
	err := row.Scan(&c.ClipID, &c.RevisionID, &c.Version, &c.Title, &c.State, &c.ApprovedAt, &c.CreatedAt, &c.Bucket, &c.Object, &data, &c.Error, &c.FlowID, &config)
	if err != nil {
		return c, mapErr(err)
	}
	if len(config) > 0 {
		var f twitch.Flow
		if err = json.Unmarshal(config, &f); err != nil {
			return c, err
		}
		c.FlowConfig = &f
	}
	var m workflow.SourceManifest
	if err = json.Unmarshal(data, &m); err != nil {
		return c, err
	}
	c.Items = []twitch.CompilationItemRequest{}
	if m.Assembly != nil {
		for _, v := range m.Assembly.Items {
			c.Items = append(c.Items, twitch.CompilationItemRequest{ClipID: v.SourceClipID, RevisionID: v.SourceRevisionID, StartSec: v.StartSec, EndSec: v.EndSec, SourceID: v.TwitchSourceID})
		}
	}
	return c, nil
}
func compilationByID(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id string) (domain.Compilation, error) {
	return scanCompilation(q.QueryRow(ctx, `SELECT `+compilationCols+` FROM twitch_compilations t JOIN clips c ON c.clip_id=t.clip_id JOIN clip_revisions r ON r.id=c.latest_revision_id WHERE t.clip_id=$1`, id))
}
func (s *Store) GetCompilation(ctx context.Context, id string) (domain.Compilation, error) {
	return compilationByID(ctx, s.pool, id)
}
func (s *Store) ListCompilations(ctx context.Context) ([]domain.Compilation, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+compilationCols+` FROM twitch_compilations t JOIN clips c ON c.clip_id=t.clip_id JOIN clip_revisions r ON r.id=c.latest_revision_id ORDER BY t.created_at DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Compilation{}
	for rows.Next() {
		c, e := scanCompilation(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func freezeAssembly(ctx context.Context, tx pgx.Tx, req twitch.CompilationRequest, settings twitch.Settings, owner ...string) (workflow.SourceManifest, error) {
	m := workflow.SourceManifest{Options: msg.JobOptions{Vertical: false, Subtitles: false}, Cues: []workflow.Cue{}, Assembly: &workflow.AssemblySpec{SchemaVersion: 1, Profile: "landscape_1080p30", BumperDurationFrames: 30, Items: []workflow.AssemblyItem{}}}
	if strings.TrimSpace(req.Title) == "" || len([]rune(req.Title)) > 100 {
		return m, fmt.Errorf("%w: title must have 1 to 100 characters", domain.ErrInvalid)
	}
	if len(req.Items) < settings.CompilationMinClips || len(req.Items) > settings.CompilationMaxClips {
		return m, fmt.Errorf("%w: compilation requires %d to %d clips", domain.ErrInvalid, settings.CompilationMinClips, settings.CompilationMaxClips)
	}
	seen := map[string]bool{}
	for _, item := range req.Items {
		if seen[item.ClipID] || item.ClipID == "" {
			return m, fmt.Errorf("%w: compilation items must be unique", domain.ErrInvalid)
		}
		seen[item.ClipID] = true
		var request, verified, manifest []byte
		var rid int64
		var state, approval string
		var rejected bool
		err := tx.QueryRow(ctx, `SELECT i.request,i.verified,r.manifest,r.id,r.render_state,c.approval,i.rejected FROM twitch_imports i JOIN clips c ON c.clip_id=i.clip_id JOIN clip_revisions r ON r.clip_id=c.clip_id AND r.id=CASE WHEN $2::bigint=0 THEN c.latest_revision_id ELSE $2 END WHERE c.clip_id=$1 AND i.state='ready'`, item.ClipID, item.RevisionID).Scan(&request, &verified, &manifest, &rid, &state, &approval, &rejected)
		if err != nil {
			return m, mapErr(err)
		}
		if rejected || approval == "rejected" || state != "ready" {
			return m, fmt.Errorf("%w: selected source is rejected or not ready", domain.ErrConflict)
		}
		var r twitch.ImportRequest
		var v workflow.VerifiedMedia
		var source workflow.SourceManifest
		if json.Unmarshal(request, &r) != nil || json.Unmarshal(verified, &v) != nil || json.Unmarshal(manifest, &source) != nil {
			return m, fmt.Errorf("%w: source manifest is invalid", domain.ErrConflict)
		}
		start, end := item.StartSec, item.EndSec
		if end == 0 {
			var spec domain.EditSpec
			var specJSON []byte
			if err = tx.QueryRow(ctx, `SELECT spec FROM clip_revisions WHERE id=$1`, rid).Scan(&specJSON); err != nil {
				return m, err
			}
			if err = json.Unmarshal(specJSON, &spec); err != nil {
				return m, err
			}
			start, end = spec.StartSec, spec.EndSec
		}
		if math.IsNaN(start) || math.IsNaN(end) || math.IsInf(start, 0) || math.IsInf(end, 0) || start < 0 || end <= start || end > v.DurationSec+0.05 {
			return m, fmt.Errorf("%w: clip bounds exceed verified source", domain.ErrInvalid)
		}
		if source.Original != v.Original || v.SHA256 == "" {
			return m, fmt.Errorf("%w: immutable source provenance does not match import", domain.ErrConflict)
		}
		ownerID := ""
		if len(owner) > 0 {
			ownerID = owner[0]
		}
		var used bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_source_uses u JOIN accounts a ON a.remote_account_id=u.youtube_channel_id WHERE a.id=$1 AND u.format='compilation' AND u.media_id=$2 AND($3='' OR u.clip_id IS DISTINCT FROM $3))`, settings.TargetAccountID, v.SHA256, ownerID).Scan(&used); err != nil {
			return m, err
		}
		if used {
			return m, fmt.Errorf("%w: исходник уже использован в подборке этого канала", domain.ErrConflict)
		}
		m.Assembly.Items = append(m.Assembly.Items, workflow.AssemblyItem{SourceClipID: item.ClipID, SourceRevisionID: rid, Original: v.Original, SHA256: v.SHA256, StartSec: start, EndSec: end, BroadcasterLogin: r.Source.BroadcasterLogin})
		m.DurationSec += end - start
	}
	m.DurationSec += float64(len(req.Items) - 1)
	return m, nil
}
func saveAssemblyItems(ctx context.Context, tx pgx.Tx, rid int64, spec *workflow.AssemblySpec) error {
	for n, v := range spec.Items {
		if _, err := tx.Exec(ctx, `INSERT INTO twitch_compilation_items(compilation_revision_id,source_revision_id,source_clip_id,ordinal,source_id,media_id)VALUES($1,NULLIF($2,0),NULLIF($3,''),$4,COALESCE(NULLIF($5,''),(SELECT id FROM twitch_imports WHERE clip_id=$3)),COALESCE(NULLIF($6,''),(SELECT media_id FROM twitch_imports WHERE clip_id=$3)))`, rid, v.SourceRevisionID, v.SourceClipID, n, v.TwitchSourceID, v.MediaID); err != nil {
			return err
		}
	}
	return nil
}

func assemblyProvenance(ctx context.Context, tx pgx.Tx, spec *workflow.AssemblySpec) (string, error) {
	urls := make([]string, 0, len(spec.Items))
	for _, item := range spec.Items {
		var sourceURL string
		if err := tx.QueryRow(ctx, `SELECT request->'source'->>'url' FROM twitch_imports WHERE ($1<>'' AND clip_id=$1) OR($2<>'' AND id=$2)`, item.SourceClipID, item.TwitchSourceID).Scan(&sourceURL); err != nil {
			return "", err
		}
		urls = append(urls, sourceURL)
	}
	return strings.Join(urls, "\n"), nil
}
func createCompilationTx(ctx context.Context, tx pgx.Tx, req twitch.CompilationRequest, localDate *time.Time, now time.Time) (domain.Compilation, error) {
	if req.FlowID != "" {
		return createFlowCompilationTx(ctx, tx, req, localDate, now)
	}
	settings, err := readTwitchSettings(ctx, tx)
	if err != nil {
		return domain.Compilation{}, err
	}
	if settings.TargetAccountID <= 0 {
		return domain.Compilation{}, fmt.Errorf("%w: choose the target YouTube account first", domain.ErrConflict)
	}
	manifest, err := freezeAssembly(ctx, tx, req, settings)
	if err != nil {
		return domain.Compilation{}, err
	}
	jid, id := uuid.NewString(), "compilation-"+uuid.NewString()
	f, err := flowByID(ctx, tx, "default")
	if err != nil {
		return domain.Compilation{}, err
	}
	sources, err := assemblyMetadataSources(ctx, tx, manifest.Assembly)
	if err != nil {
		return domain.Compilation{}, err
	}
	rid, err := createTwitchDraft(ctx, tx, jid, id, "twitch_compilation", "compilation", "", id, req.Title, settings.TargetAccountID, settings.Timezone, manifest, now, flowMetadataInput(f, "compilation", req.Title, sources))
	if err != nil {
		return domain.Compilation{}, err
	}
	var date any
	if localDate != nil {
		loc, _ := time.LoadLocation(settings.Timezone)
		date = localDate.In(loc).Format("2006-01-02")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO twitch_compilations(clip_id,flow_id,local_date,timezone)VALUES($1,'default',$2::date,$3)`, id, date, settings.Timezone); err != nil {
		return domain.Compilation{}, err
	}
	if err = saveAssemblyItems(ctx, tx, rid, manifest.Assembly); err != nil {
		return domain.Compilation{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO twitch_outputs(clip_id,flow_id,account_id,youtube_channel_id,format,config,initial_revision_id,created_at)VALUES($1,'default',$2,$3,'compilation',$4,$5,$6)`, id, settings.TargetAccountID, f.YouTubeChannelID, mustJSON(f), rid, now); err != nil {
		return domain.Compilation{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO twitch_source_uses(youtube_channel_id,format,media_id,clip_id,created_at)SELECT $1,'compilation',media_id,$2,$3 FROM twitch_compilation_items WHERE compilation_revision_id=$4 AND media_id IS NOT NULL AND $1<>'' ON CONFLICT DO NOTHING`, f.YouTubeChannelID, id, now, rid); err != nil {
		return domain.Compilation{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_flows SET work_started_at=COALESCE(work_started_at,$1)WHERE id='default'`, now); err != nil {
		return domain.Compilation{}, err
	}
	if err = tightenQueuedRender(ctx, tx, rid, now); err != nil {
		return domain.Compilation{}, err
	}
	return compilationByID(ctx, tx, id)
}
func (s *Store) CreateCompilation(ctx context.Context, req twitch.CompilationRequest, date *time.Time, now time.Time) (domain.Compilation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Compilation{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return domain.Compilation{}, err
	}
	out, err := createCompilationTx(ctx, tx, req, date, now)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *Store) UpdateCompilation(ctx context.Context, id string, u domain.CompilationUpdate, now time.Time) (domain.Compilation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Compilation{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return domain.Compilation{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT clip_id FROM clips WHERE clip_id=$1 FOR UPDATE`, id); err != nil {
		return domain.Compilation{}, err
	}
	prev, err := compilationByID(ctx, tx, id)
	if err != nil {
		return prev, err
	}
	if u.ExpectedVersion <= 0 || prev.Version != u.ExpectedVersion {
		return prev, domain.ErrConflict
	}
	settings, err := readTwitchSettings(ctx, tx)
	if err != nil {
		return prev, err
	}
	var m workflow.SourceManifest
	var flow twitch.Flow
	if u.FlowID != "" && u.FlowID != prev.FlowID {
		return prev, domain.ErrConflict
	}
	var oldManifest []byte
	if err = tx.QueryRow(ctx, `SELECT manifest FROM clip_revisions WHERE id=$1`, prev.RevisionID).Scan(&oldManifest); err != nil {
		return prev, err
	}
	var oldSource workflow.SourceManifest
	if err = json.Unmarshal(oldManifest, &oldSource); err != nil {
		return prev, err
	}
	if oldSource.Assembly != nil && oldSource.Assembly.SchemaVersion == 2 {
		var snapshot []byte
		err = tx.QueryRow(ctx, `SELECT config FROM twitch_outputs WHERE clip_id=$1`, id).Scan(&snapshot)
		if err == nil {
			err = json.Unmarshal(snapshot, &flow)
		}
		if err == nil {
			u.FlowID = prev.FlowID
			var current twitch.Flow
			current, err = currentFlowPolicy(ctx, tx, prev.FlowID)
			if err == nil {
				flow.Language = current.Language
				flow.SourceMode = current.SourceMode
				flow.CategoryID = current.CategoryID
				flow.BroadcasterIDs = current.BroadcasterIDs
				m, err = freezeFlowAssembly(ctx, tx, u.CompilationRequest, flow, id)
			}
		}
	} else {
		if prev.FlowConfig != nil {
			settings.TargetAccountID = prev.FlowConfig.AccountID
			settings.Timezone = prev.FlowConfig.Timezone
			settings.CompilationMinClips = prev.FlowConfig.CompilationMinClips
			settings.CompilationMaxClips = min(prev.FlowConfig.CompilationMaxClips, 10)
		}
		m, err = freezeAssembly(ctx, tx, u.CompilationRequest, settings, id)
	}
	if err != nil {
		return prev, err
	}
	old, err := scanRevision(tx.QueryRow(ctx, `SELECT `+revisionCols+` FROM clip_revisions WHERE id=$1`, prev.RevisionID))
	if err != nil {
		return prev, err
	}
	if err = storageRevisionAvailable(ctx, tx, prev.RevisionID); err != nil {
		return prev, err
	}
	spec := old.EditSpec
	spec.StartSec, spec.EndSec, spec.Title, spec.Cues, spec.SubtitlesEnabled = 0, m.DurationSec, u.Title, []domain.Cue{}, false
	if spec.MetadataSnapshot != nil {
		sources, e := assemblyMetadataSources(ctx, tx, m.Assembly)
		if e != nil {
			return prev, e
		}
		if e = applyTwitchMetadata(&spec, spec.MetadataSnapshot.ToInput(u.Title, sources)); e != nil {
			return prev, e
		}
	}
	rerender := !reflect.DeepEqual(m.Assembly, oldSource.Assembly) || old.StartSec != spec.StartSec || old.EndSec != spec.EndSec || old.SubtitlesEnabled || len(old.Cues) > 0 || prev.State != "ready" || prev.Object == ""
	state, bucket, object := prev.State, prev.Bucket, prev.Object
	if rerender {
		state, bucket, object = "pending", "", ""
		if prev.FlowID != "" {
			current, e := currentFlowPolicy(ctx, tx, prev.FlowID)
			if e != nil {
				return prev, e
			}
			ids := []string{}
			for _, item := range m.Assembly.Items {
				ids = append(ids, item.TwitchSourceID)
			}
			if _, e = reserveOutputPreparationTx(ctx, tx, current, id, "compilation", ids, now); e != nil {
				return prev, e
			}
		}
	} else {
		// Keep the rendered assembly's saved original for later editor trims.
		m = oldSource
	}
	var rid int64
	if err = tx.QueryRow(ctx, `INSERT INTO clip_revisions(clip_id,revision_no,spec,render_state,manifest,bucket,object)VALUES($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,'')) RETURNING id`, id, prev.Version+1, mustJSON(spec), state, mustJSON(m), bucket, object).Scan(&rid); err != nil {
		return prev, err
	}
	if _, err = tx.Exec(ctx, `UPDATE clips SET latest_revision_id=$2,approval='pending',start_sec=0,end_sec=$3,title=$4 WHERE clip_id=$1`, id, rid, m.DurationSec, u.Title); err != nil {
		return prev, err
	}
	if _, err = tx.Exec(ctx, `UPDATE publication_attempts SET status='superseded',lease_token=NULL,lease_until=NULL WHERE publication_id IN(SELECT id FROM publications WHERE clip_id=$1 AND NOT manual AND started_at IS NULL) AND status='active'`, id); err != nil {
		return prev, err
	}
	if _, err = tx.Exec(ctx, `UPDATE publications SET revision_id=$2,policy_hold_reason=CASE WHEN status IN('scheduled','queued','retry_wait')THEN 'revision_changed' ELSE policy_hold_reason END,status='on_hold',version=version+1,metadata=$3,error=NULL WHERE clip_id=$1 AND NOT manual AND started_at IS NULL AND status NOT IN('cancelled','published','needs_attention')`, id, rid, mustJSON(spec)); err != nil {
		return prev, err
	}
	if err = saveAssemblyItems(ctx, tx, rid, m.Assembly); err != nil {
		return prev, err
	}
	if m.Assembly.SchemaVersion == 2 {
		if err = reserveAssemblyUse(ctx, tx, flow, id, m, now); err != nil {
			return prev, err
		}
	}
	if rerender {
		if err = queueInitialRender(ctx, tx, rid, prev.Version+1, now); err != nil {
			return prev, err
		}
	}
	out, err := compilationByID(ctx, tx, id)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

// Classify each import once so the diagnostic counts use the same eligibility
// rules as automatic selection, including sources used by older revisions.
const autoCompilationSources = `WITH sources AS (
 SELECT c.clip_id,c.latest_revision_id,(i.request->'source'->>'view_count')::int AS view_count,i.created_at,
 CASE
  WHEN i.rejected OR c.approval='rejected' THEN 'rejected'
  WHEN i.state<>'ready' OR c.clip_id IS NULL OR r.render_state IS DISTINCT FROM 'ready' THEN 'not_ready'
  WHEN ((i.request->'source'->>'created_at')::timestamptz >= $1) IS NOT TRUE THEN 'outside_window'
  WHEN EXISTS(SELECT 1 FROM twitch_compilation_items t WHERE t.source_clip_id=c.clip_id) THEN 'already_used'
  ELSE 'eligible'
 END AS eligibility
 FROM twitch_imports i LEFT JOIN clips c ON c.clip_id=i.clip_id LEFT JOIN clip_revisions r ON r.id=c.latest_revision_id
) `

func (s *Store) AutoCompilation(ctx context.Context, now time.Time) (domain.AutoCompilationResult, error) {
	result := domain.AutoCompilationResult{}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return result, err
	}
	settings, err := readTwitchSettings(ctx, tx)
	if err != nil {
		return result, err
	}
	loc, err := time.LoadLocation(settings.Timezone)
	if err != nil {
		return result, err
	}
	local := now.In(loc)
	result.LocalDate, result.Timezone = local.Format("2006-01-02"), settings.Timezone
	result.MinimumClips, result.WindowHours = settings.CompilationMinClips, settings.WindowHours
	result.ScheduledAt = time.Date(local.Year(), local.Month(), local.Day(), settings.CompilationHour, 0, 0, 0, loc)
	if settings.TargetAccountID <= 0 {
		result.Reason = "target_account_missing"
		return result, nil
	}
	if !settings.Enabled {
		result.Reason = "collection_disabled"
		return result, nil
	}
	var targetExists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE id=$1 AND platform='youtube')`, settings.TargetAccountID).Scan(&targetExists); err != nil {
		return result, err
	}
	if !targetExists {
		result.Reason = "target_account_missing"
		return result, nil
	}
	if local.Hour() < settings.CompilationHour {
		result.Reason = "before_scheduled_hour"
		result.NextAttemptAt = &result.ScheduledAt
		return result, nil
	}
	var existingID string
	err = tx.QueryRow(ctx, `SELECT clip_id FROM twitch_compilations WHERE local_date=$1::date AND timezone=$2`, result.LocalDate, settings.Timezone).Scan(&existingID)
	if err != nil && err != pgx.ErrNoRows {
		return result, err
	}
	if err == nil {
		existing, err := compilationByID(ctx, tx, existingID)
		if err != nil {
			return result, err
		}
		result.Reason, result.Compilation = "daily_draft_exists", &existing
		next := result.ScheduledAt.AddDate(0, 0, 1)
		result.NextAttemptAt = &next
		return result, nil
	}
	cutoff := now.Add(-time.Duration(settings.WindowHours) * time.Hour)
	result.Sources = &domain.CompilationSourceCounts{}
	counts := result.Sources
	err = tx.QueryRow(ctx, autoCompilationSources+`SELECT count(*),
 count(*) FILTER(WHERE eligibility='eligible'), count(*) FILTER(WHERE eligibility='rejected'),
 count(*) FILTER(WHERE eligibility='not_ready'), count(*) FILTER(WHERE eligibility='outside_window'),
 count(*) FILTER(WHERE eligibility='already_used') FROM sources`, cutoff).Scan(&counts.Total, &counts.Eligible, &counts.Rejected, &counts.NotReady, &counts.OutsideWindow, &counts.AlreadyUsed)
	if err != nil {
		return result, err
	}
	if counts.Eligible < settings.CompilationMinClips {
		result.Reason = "insufficient_eligible_sources"
		return result, nil
	}
	rows, err := tx.Query(ctx, autoCompilationSources+`SELECT clip_id,latest_revision_id FROM sources WHERE eligibility='eligible' ORDER BY view_count DESC,created_at LIMIT $2`, cutoff, settings.CompilationMaxClips)
	if err != nil {
		return result, err
	}
	req := twitch.CompilationRequest{Title: "Twitch clips · " + local.Format("2006-01-02"), Items: []twitch.CompilationItemRequest{}}
	for rows.Next() {
		var v twitch.CompilationItemRequest
		if err = rows.Scan(&v.ClipID, &v.RevisionID); err != nil {
			rows.Close()
			return result, err
		}
		req.Items = append(req.Items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	if len(req.Items) < settings.CompilationMinClips {
		result.Reason = "insufficient_eligible_sources"
		// Render completion/rejection can change while the rows are selected.
		counts.Eligible = len(req.Items)
		return result, nil
	}
	out, err := createCompilationTx(ctx, tx, req, &now, now)
	if err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, err
	}
	result.Created, result.Compilation, result.Reason = true, &out, "created"
	return result, nil
}
