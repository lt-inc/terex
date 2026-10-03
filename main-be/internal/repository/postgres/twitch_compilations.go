package postgres

import (
	"context"
	"encoding/json"
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

const compilationCols = `c.clip_id,r.id,r.revision_no,COALESCE(r.spec->>'title',''),r.render_state,r.approved_at,r.created_at,COALESCE(r.bucket,''),COALESCE(r.object,''),r.manifest,COALESCE(r.error,'')`

func scanCompilation(row pgx.Row) (domain.Compilation, error) {
	var c domain.Compilation
	var data []byte
	err := row.Scan(&c.ClipID, &c.RevisionID, &c.Version, &c.Title, &c.State, &c.ApprovedAt, &c.CreatedAt, &c.Bucket, &c.Object, &data, &c.Error)
	if err != nil {
		return c, mapErr(err)
	}
	var m workflow.SourceManifest
	if err = json.Unmarshal(data, &m); err != nil {
		return c, err
	}
	c.Items = []twitch.CompilationItemRequest{}
	if m.Assembly != nil {
		for _, v := range m.Assembly.Items {
			c.Items = append(c.Items, twitch.CompilationItemRequest{ClipID: v.SourceClipID, RevisionID: v.SourceRevisionID, StartSec: v.StartSec, EndSec: v.EndSec})
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
func freezeAssembly(ctx context.Context, tx pgx.Tx, req twitch.CompilationRequest, settings twitch.Settings) (workflow.SourceManifest, error) {
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
		m.Assembly.Items = append(m.Assembly.Items, workflow.AssemblyItem{SourceClipID: item.ClipID, SourceRevisionID: rid, Original: v.Original, SHA256: v.SHA256, StartSec: start, EndSec: end, BroadcasterLogin: r.Source.BroadcasterLogin})
		m.DurationSec += end - start
	}
	m.DurationSec += float64(len(req.Items) - 1)
	return m, nil
}
func saveAssemblyItems(ctx context.Context, tx pgx.Tx, rid int64, spec *workflow.AssemblySpec) error {
	for n, v := range spec.Items {
		if _, err := tx.Exec(ctx, `INSERT INTO twitch_compilation_items(compilation_revision_id,source_revision_id,source_clip_id,ordinal)VALUES($1,$2,$3,$4)`, rid, v.SourceRevisionID, v.SourceClipID, n); err != nil {
			return err
		}
	}
	return nil
}

func assemblyProvenance(ctx context.Context, tx pgx.Tx, spec *workflow.AssemblySpec) (string, error) {
	urls := make([]string, 0, len(spec.Items))
	for _, item := range spec.Items {
		var sourceURL string
		if err := tx.QueryRow(ctx, `SELECT request->'source'->>'url' FROM twitch_imports WHERE clip_id=$1`, item.SourceClipID).Scan(&sourceURL); err != nil {
			return "", err
		}
		urls = append(urls, sourceURL)
	}
	return strings.Join(urls, "\n"), nil
}
func createCompilationTx(ctx context.Context, tx pgx.Tx, req twitch.CompilationRequest, localDate *time.Time, now time.Time) (domain.Compilation, error) {
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
	rid, err := createTwitchDraft(ctx, tx, jid, id, "twitch_compilation", "compilation", "", id, req.Title, settings.TargetAccountID, settings.Timezone, manifest, now)
	if err != nil {
		return domain.Compilation{}, err
	}
	provenance, err := assemblyProvenance(ctx, tx, manifest.Assembly)
	if err != nil {
		return domain.Compilation{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE clip_revisions SET spec=spec||jsonb_build_object('description',$2::text) WHERE id=$1`, rid, provenance); err != nil {
		return domain.Compilation{}, err
	}
	var date any
	if localDate != nil {
		loc, _ := time.LoadLocation(settings.Timezone)
		date = localDate.In(loc).Format("2006-01-02")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO twitch_compilations(clip_id,local_date,timezone)VALUES($1,$2::date,$3)`, id, date, settings.Timezone); err != nil {
		return domain.Compilation{}, err
	}
	if err = saveAssemblyItems(ctx, tx, rid, manifest.Assembly); err != nil {
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
	m, err := freezeAssembly(ctx, tx, u.CompilationRequest, settings)
	if err != nil {
		return prev, err
	}
	old, err := scanRevision(tx.QueryRow(ctx, `SELECT `+revisionCols+` FROM clip_revisions WHERE id=$1`, prev.RevisionID))
	if err != nil {
		return prev, err
	}
	spec := old.EditSpec
	spec.StartSec, spec.EndSec, spec.Title, spec.Cues, spec.SubtitlesEnabled = 0, m.DurationSec, u.Title, []domain.Cue{}, false
	spec.Description, err = assemblyProvenance(ctx, tx, m.Assembly)
	if err != nil {
		return prev, err
	}
	var rid int64
	if err = tx.QueryRow(ctx, `INSERT INTO clip_revisions(clip_id,revision_no,spec,render_state,manifest)VALUES($1,$2,$3,'pending',$4) RETURNING id`, id, prev.Version+1, mustJSON(spec), mustJSON(m)).Scan(&rid); err != nil {
		return prev, err
	}
	if _, err = tx.Exec(ctx, `UPDATE clips SET latest_revision_id=$2,approval='pending',start_sec=0,end_sec=$3,title=$4 WHERE clip_id=$1`, id, rid, m.DurationSec, u.Title); err != nil {
		return prev, err
	}
	if _, err = tx.Exec(ctx, `UPDATE publication_attempts SET status='superseded',lease_token=NULL,lease_until=NULL WHERE publication_id IN(SELECT id FROM publications WHERE clip_id=$1 AND started_at IS NULL) AND status='active'`, id); err != nil {
		return prev, err
	}
	if _, err = tx.Exec(ctx, `UPDATE publications SET revision_id=$2,status='on_hold',version=version+1,metadata=$3,cancel_requested=FALSE,error=NULL WHERE clip_id=$1 AND started_at IS NULL AND status NOT IN('cancelled','published','needs_attention')`, id, rid, mustJSON(spec)); err != nil {
		return prev, err
	}
	if err = saveAssemblyItems(ctx, tx, rid, m.Assembly); err != nil {
		return prev, err
	}
	if err = queueInitialRender(ctx, tx, rid, prev.Version+1, now); err != nil {
		return prev, err
	}
	out, err := compilationByID(ctx, tx, id)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *Store) AutoCompilation(ctx context.Context, now time.Time) (*domain.Compilation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return nil, err
	}
	settings, err := readTwitchSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	if !settings.Enabled || settings.TargetAccountID <= 0 {
		return nil, nil
	}
	loc, _ := time.LoadLocation(settings.Timezone)
	local := now.In(loc)
	if local.Hour() < settings.CompilationHour {
		return nil, nil
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_compilations WHERE local_date=$1::date AND timezone=$2)`, local.Format("2006-01-02"), settings.Timezone).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `SELECT c.clip_id,c.latest_revision_id FROM twitch_imports i JOIN clips c ON c.clip_id=i.clip_id JOIN clip_revisions r ON r.id=c.latest_revision_id WHERE i.state='ready' AND NOT i.rejected AND c.approval<>'rejected' AND r.render_state='ready' AND (i.request->'source'->>'created_at')::timestamptz>=$1 AND NOT EXISTS(SELECT 1 FROM twitch_compilation_items t WHERE t.source_clip_id=c.clip_id) ORDER BY (i.request->'source'->>'view_count')::int DESC,i.created_at LIMIT $2`, now.Add(-time.Duration(settings.WindowHours)*time.Hour), settings.CompilationMaxClips)
	if err != nil {
		return nil, err
	}
	req := twitch.CompilationRequest{Title: "Twitch clips · " + local.Format("2006-01-02"), Items: []twitch.CompilationItemRequest{}}
	for rows.Next() {
		var v twitch.CompilationItemRequest
		if err = rows.Scan(&v.ClipID, &v.RevisionID); err != nil {
			rows.Close()
			return nil, err
		}
		req.Items = append(req.Items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(req.Items) < settings.CompilationMinClips {
		return nil, nil
	}
	out, err := createCompilationTx(ctx, tx, req, &now, now)
	if err != nil {
		return nil, err
	}
	return &out, tx.Commit(ctx)
}
