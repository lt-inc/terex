package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"terex/main-be/internal/domain"
	"terex/proto/twitch"
)

const lockYTQuota = 773219422

// ytScore is views per hour since publication (at least one hour), the ranking used everywhere.
const ytScore = `(c.views::float8/GREATEST(EXTRACT(EPOCH FROM ($1::timestamptz-c.published_at))/3600.0,1.0))`

func ytDay(day time.Time) string { return day.Format("2006-01-02") }

func newYTFlowID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Store) ListYTFlows(ctx context.Context, day time.Time) ([]domain.YTFlow, error) {
	rows, err := s.pool.Query(ctx, `SELECT f.id,f.name,f.account_id,COALESCE(a.name,''),COALESCE(a.remote_account_id,''),f.enabled,f.config,f.version,
 f.last_run_at,f.last_status,f.last_error,COALESCE((SELECT units FROM yt_quota q WHERE q.day=$1::date AND q.flow_id=f.id),0),
 (SELECT min(c.slot_at) FROM yt_candidates c WHERE c.flow_id=f.id AND c.state='queued' AND c.slot_at>now())
 FROM yt_flows f LEFT JOIN accounts a ON a.id=f.account_id ORDER BY f.created_at,f.id`, ytDay(day))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.YTFlow{}
	byID := map[string]int{}
	for rows.Next() {
		var f domain.YTFlow
		var cfg []byte
		if err = rows.Scan(&f.ID, &f.Name, &f.AccountID, &f.AccountName, &f.ChannelID, &f.Enabled, &cfg, &f.Version, &f.LastRunAt, &f.LastStatus, &f.LastError, &f.QuotaUsed, &f.NextSlotAt); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(cfg, &f.Config); err != nil {
			return nil, err
		}
		f.Sources = []domain.YTSource{}
		byID[f.ID] = len(out)
		out = append(out, f)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	srcs, err := s.pool.Query(ctx, `SELECT id,flow_id,kind,value,title,params,last_rss_at,last_scan_at,last_error FROM yt_flow_sources ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer srcs.Close()
	for srcs.Next() {
		var src domain.YTSource
		var params []byte
		if err = srcs.Scan(&src.ID, &src.FlowID, &src.Kind, &src.Value, &src.Title, &params, &src.LastRSSAt, &src.LastScanAt, &src.LastError); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(params, &src.Params)
		if i, ok := byID[src.FlowID]; ok {
			out[i].Sources = append(out[i].Sources, src)
		}
	}
	if err = srcs.Err(); err != nil {
		return nil, err
	}
	srcs.Close()

	counts, err := s.pool.Query(ctx, `SELECT flow_id,state,count(*) FROM yt_candidates GROUP BY 1,2`)
	if err != nil {
		return nil, err
	}
	defer counts.Close()
	for counts.Next() {
		var id, state string
		var n int
		if err = counts.Scan(&id, &state, &n); err != nil {
			return nil, err
		}
		i, ok := byID[id]
		if !ok {
			continue
		}
		c := &out[i].Counts
		switch state {
		case domain.YTNew:
			c.New = n
		case domain.YTQueued:
			c.Queued = n
		case domain.YTDone:
			c.Done = n
		case domain.YTFailed:
			c.Failed = n
		case domain.YTSkipped:
			c.Skipped = n
		}
	}
	return out, counts.Err()
}

func (s *Store) GetYTFlow(ctx context.Context, id string, day time.Time) (domain.YTFlow, error) {
	all, err := s.ListYTFlows(ctx, day)
	if err != nil {
		return domain.YTFlow{}, err
	}
	for _, f := range all {
		if f.ID == id {
			return f, nil
		}
	}
	return domain.YTFlow{}, domain.ErrNotFound
}

// ensureChannelLimits creates the shared per-channel limit row if no flow has created it yet;
// an existing row (Twitch flows own it) is never overwritten.
func ensureChannelLimits(ctx context.Context, tx pgx.Tx, channelID string) error {
	if channelID == "" {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO twitch_channel_limits(youtube_channel_id,timezone,daily_shorts,daily_compilations)VALUES($1,'Asia/Tomsk',5,1)ON CONFLICT(youtube_channel_id)DO NOTHING`, channelID)
	return err
}

func (s *Store) SaveYTFlow(ctx context.Context, f domain.YTFlow, create bool) (domain.YTFlow, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return f, err
	}
	defer tx.Rollback(ctx)
	var platform, remote, state string
	err = tx.QueryRow(ctx, `SELECT platform,COALESCE(remote_account_id,''),connection_state FROM accounts WHERE id=$1`, f.AccountID).Scan(&platform, &remote, &state)
	if err != nil {
		return f, fmt.Errorf("%w: account not found", domain.ErrInvalid)
	}
	if platform != "youtube" {
		return f, fmt.Errorf("%w: destination must be a YouTube account", domain.ErrInvalid)
	}
	if f.Enabled && (state != "connected" || remote == "") {
		return f, fmt.Errorf("%w: connect the YouTube account and confirm its channel first", domain.ErrInvalid)
	}
	cfg, err := json.Marshal(f.Config)
	if err != nil {
		return f, err
	}
	if create {
		f.ID = newYTFlowID()
		_, err = tx.Exec(ctx, `INSERT INTO yt_flows(id,name,account_id,enabled,config)VALUES($1,$2,$3,$4,$5)`, f.ID, f.Name, f.AccountID, f.Enabled, cfg)
	} else {
		var t pgconn.CommandTag
		t, err = tx.Exec(ctx, `UPDATE yt_flows SET name=$2,account_id=$3,enabled=$4,config=$5,version=version+1,updated_at=now() WHERE id=$1`, f.ID, f.Name, f.AccountID, f.Enabled, cfg)
		if err == nil && t.RowsAffected() == 0 {
			err = domain.ErrNotFound
		}
	}
	if err != nil {
		return f, err
	}
	if err = ensureChannelLimits(ctx, tx, remote); err != nil {
		return f, err
	}
	if err = tx.Commit(ctx); err != nil {
		return f, err
	}
	return s.GetYTFlow(ctx, f.ID, time.Now())
}

func (s *Store) DeleteYTFlow(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	// Slots held by unfinished candidates are released together with their destination claims.
	if _, err = tx.Exec(ctx, `DELETE FROM yt_uses WHERE flow_id=$1 AND video_id IN(SELECT video_id FROM yt_candidates WHERE flow_id=$1 AND state IN('new','queued') AND job_id IS NULL)`, id); err != nil {
		return err
	}
	t, err := tx.Exec(ctx, `DELETE FROM yt_flows WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if t.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return tx.Commit(ctx)
}

func (s *Store) AddYTSource(ctx context.Context, src domain.YTSource) (domain.YTSource, error) {
	params, _ := json.Marshal(src.Params)
	err := s.pool.QueryRow(ctx, `INSERT INTO yt_flow_sources(flow_id,kind,value,title,params)VALUES($1,$2,$3,$4,$5)
 ON CONFLICT(flow_id,kind,value)DO UPDATE SET title=CASE WHEN EXCLUDED.title<>'' THEN EXCLUDED.title ELSE yt_flow_sources.title END,params=EXCLUDED.params
 RETURNING id`, src.FlowID, src.Kind, src.Value, src.Title, params).Scan(&src.ID)
	return src, err
}

func (s *Store) DeleteYTSource(ctx context.Context, flowID string, id int64) error {
	t, err := s.pool.Exec(ctx, `DELETE FROM yt_flow_sources WHERE id=$1 AND flow_id=$2`, id, flowID)
	if err == nil && t.RowsAffected() == 0 {
		err = domain.ErrNotFound
	}
	return err
}

func (s *Store) TouchYTSource(ctx context.Context, id int64, rss, scan *time.Time, lastError string) error {
	_, err := s.pool.Exec(ctx, `UPDATE yt_flow_sources SET last_rss_at=COALESCE($2,last_rss_at),last_scan_at=COALESCE($3,last_scan_at),last_error=$4 WHERE id=$1`, id, rss, scan, lastError)
	return err
}

func (s *Store) MarkYTFlowRun(ctx context.Context, id string, at time.Time, status, errText string) error {
	_, err := s.pool.Exec(ctx, `UPDATE yt_flows SET last_run_at=$2,last_status=$3,last_error=$4 WHERE id=$1`, id, at, status, errText)
	return err
}

func (s *Store) YTKnownVideos(ctx context.Context, flowID string, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT video_id FROM yt_candidates WHERE flow_id=$1 AND video_id=ANY($2)`, flowID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// UpsertYTCandidates inserts new videos in the state the caller chose (new, or skipped with a reason)
// and refreshes the statistics of waiting ones; a candidate that already moved on keeps its state.
func (s *Store) UpsertYTCandidates(ctx context.Context, flowID string, sourceID *int64, list []domain.YTCandidate, now time.Time) error {
	if len(list) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, c := range list {
		state := c.State
		if state == "" {
			state = domain.YTNew
		}
		if _, err = tx.Exec(ctx, `INSERT INTO yt_candidates(flow_id,video_id,source_id,channel_id,channel_title,title,published_at,duration_sec,views,likes,first_seen_at,stats_at,state,reason)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11,$12,$13)
 ON CONFLICT(flow_id,video_id)DO UPDATE SET views=EXCLUDED.views,likes=EXCLUDED.likes,stats_at=EXCLUDED.stats_at,title=EXCLUDED.title,duration_sec=EXCLUDED.duration_sec,published_at=COALESCE(EXCLUDED.published_at,yt_candidates.published_at)
 WHERE yt_candidates.state='new'`, flowID, c.VideoID, sourceID, c.ChannelID, c.ChannelTitle, c.Title, c.PublishedAt, c.DurationSec, c.Views, c.Likes, now, state, c.Reason); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) YTCandidatesToRefresh(ctx context.Context, flowID string, statsBefore, publishedAfter time.Time, limit int) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT video_id FROM yt_candidates WHERE flow_id=$1 AND state='new' AND published_at>=$3 AND(stats_at IS NULL OR stats_at<$2) ORDER BY published_at DESC LIMIT $4`, flowID, statsBefore, publishedAfter, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

const ytCandidateCols = `c.flow_id,c.video_id,c.source_id,c.channel_id,c.channel_title,c.title,c.published_at,c.duration_sec,c.views,c.likes,c.first_seen_at,c.stats_at,c.state,c.reason,c.job_id,c.slot_at`

func scanYTCandidate(r pgx.Row, score *float64) (domain.YTCandidate, error) {
	var c domain.YTCandidate
	err := r.Scan(&c.FlowID, &c.VideoID, &c.SourceID, &c.ChannelID, &c.ChannelTitle, &c.Title, &c.PublishedAt, &c.DurationSec, &c.Views, &c.Likes, &c.FirstSeenAt, &c.StatsAt, &c.State, &c.Reason, &c.JobID, &c.SlotAt, score)
	c.Score = *score
	return c, err
}

func (s *Store) ListYTCandidates(ctx context.Context, flowID, state string, limit int) ([]domain.YTCandidate, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	order := `COALESCE(c.slot_at,c.published_at) DESC NULLS LAST`
	if state == domain.YTNew {
		order = `score DESC`
	}
	// $1 = now for the score, $2 = flow, $3 = state filter ('' = all), $4 = limit
	rows, err := s.pool.Query(ctx, `SELECT `+ytCandidateCols+`,COALESCE(`+ytScore+`,0) AS score FROM yt_candidates c WHERE c.flow_id=$2 AND($3='' OR c.state=$3) ORDER BY `+order+` LIMIT $4`, time.Now(), flowID, state, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.YTCandidate{}
	for rows.Next() {
		var score float64
		c, e := scanYTCandidate(rows, &score)
		if e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) SkipYTCandidate(ctx context.Context, flowID, videoID, reason string) error {
	t, err := s.pool.Exec(ctx, `UPDATE yt_candidates SET state='skipped',reason=$3 WHERE flow_id=$1 AND video_id=$2 AND state='new'`, flowID, videoID, reason)
	if err == nil && t.RowsAffected() == 0 {
		err = domain.ErrConflict
	}
	return err
}

var ytDefaultSlots = []string{"09:00", "12:00", "15:00", "18:00", "21:00"}

// ReserveYTSlots pairs the best waiting candidates (highest views per hour first) with the earliest
// free slots of the destination channel. Free slots come from calendarSlotsTx, the function the Twitch
// scheduler uses, so the channel's daily limit and its occupied windows are shared between both.
func (s *Store) ReserveYTSlots(ctx context.Context, flowID string, now time.Time) ([]domain.YTReservation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return nil, err
	}
	var enabled bool
	var cfgRaw []byte
	var remote, state string
	err = tx.QueryRow(ctx, `SELECT f.enabled,f.config,COALESCE(a.remote_account_id,''),COALESCE(a.connection_state,'') FROM yt_flows f JOIN accounts a ON a.id=f.account_id WHERE f.id=$1`, flowID).Scan(&enabled, &cfgRaw, &remote, &state)
	if err != nil {
		return nil, mapErr(err)
	}
	if !enabled || remote == "" || state != "connected" {
		return nil, nil
	}
	var cfg domain.YTFlowConfig
	if err = json.Unmarshal(cfgRaw, &cfg); err != nil {
		return nil, err
	}
	if len(cfg.Slots) == 0 {
		cfg.Slots = ytDefaultSlots
	}
	if err = ensureChannelLimits(ctx, tx, remote); err != nil {
		return nil, err
	}
	var zone string
	if err = tx.QueryRow(ctx, `SELECT timezone FROM twitch_channel_limits WHERE youtube_channel_id=$1`, remote).Scan(&zone); err != nil {
		return nil, err
	}
	loc := mustLocation(zone)
	slots, err := calendarSlotsTx(ctx, tx, twitch.Flow{YouTubeChannelID: remote, ShortsSlots: cfg.Slots}, "short", 0, "", now)
	if err != nil {
		return nil, err
	}
	free := []time.Time{}
	for _, sl := range slots {
		if !sl.Occupied && !sl.Preparing {
			free = append(free, sl.At)
		}
	}
	sort.Slice(free, func(i, j int) bool { return free[i].Before(free[j]) })
	if len(free) == 0 {
		return nil, nil
	}

	// The flow's own caps: slots per local day and jobs created per local day.
	localDay := func(t time.Time) string { return t.In(loc).Format("2006-01-02") }
	perDay := map[string]int{}
	dayStart := time.Date(now.In(loc).Year(), now.In(loc).Month(), now.In(loc).Day(), 0, 0, 0, 0, loc)
	rows, err := tx.Query(ctx, `SELECT slot_at FROM yt_candidates WHERE flow_id=$1 AND state IN('queued','done') AND slot_at>=$2`, flowID, dayStart)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var at time.Time
		if err = rows.Scan(&at); err != nil {
			rows.Close()
			return nil, err
		}
		perDay[localDay(at)]++
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	var downloadsToday int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM yt_candidates WHERE flow_id=$1 AND queued_at>=$2`, flowID, dayStart).Scan(&downloadsToday); err != nil {
		return nil, err
	}

	window := cfg.WindowHours
	if window <= 0 {
		window = 72
	}
	maxDur := cfg.MaxDurationSec
	if maxDur <= 0 {
		maxDur = 180
	}
	cRows, err := tx.Query(ctx, `SELECT `+ytCandidateCols+`,`+ytScore+` FROM yt_candidates c
 WHERE c.flow_id=$2 AND c.state='new' AND c.published_at>=$1::timestamptz-make_interval(hours=>$3) AND c.views>=$4
  AND c.duration_sec BETWEEN $5 AND $6 AND($7::float8<=0 OR `+ytScore+`>=$7)
  AND NOT EXISTS(SELECT 1 FROM yt_uses u WHERE u.destination_key=$8 AND u.video_id=c.video_id)
 ORDER BY `+ytScore+` DESC,c.views DESC LIMIT $9`, now, flowID, window, cfg.MinViews, cfg.MinDurationSec, maxDur, cfg.MinViewsPerHour, remote, len(free)*2+4)
	if err != nil {
		return nil, err
	}
	var cands []domain.YTCandidate
	for cRows.Next() {
		var score float64
		c, e := scanYTCandidate(cRows, &score)
		if e != nil {
			cRows.Close()
			return nil, e
		}
		cands = append(cands, c)
	}
	cRows.Close()
	if err = cRows.Err(); err != nil {
		return nil, err
	}

	var out []domain.YTReservation
	next := 0
	for _, slot := range free {
		if cfg.DailyLimit > 0 && perDay[localDay(slot)] >= cfg.DailyLimit {
			continue
		}
		if cfg.DailyDownloads > 0 && downloadsToday >= cfg.DailyDownloads {
			break
		}
		for next < len(cands) {
			c := cands[next]
			next++
			t, e := tx.Exec(ctx, `INSERT INTO yt_uses(destination_key,video_id,flow_id)VALUES($1,$2,$3)ON CONFLICT DO NOTHING`, remote, c.VideoID, flowID)
			if e != nil {
				return nil, e
			}
			if t.RowsAffected() == 0 {
				continue // another flow took the video for this channel
			}
			if _, e = tx.Exec(ctx, `UPDATE yt_candidates SET state='queued',slot_at=$3,queued_at=$4,reason='' WHERE flow_id=$1 AND video_id=$2`, flowID, c.VideoID, slot, now); e != nil {
				return nil, e
			}
			perDay[localDay(slot)]++
			downloadsToday++
			out = append(out, domain.YTReservation{Candidate: c, SlotAt: slot, Timezone: zone})
			break
		}
	}
	return out, tx.Commit(ctx)
}

func (s *Store) AttachYTJob(ctx context.Context, flowID, videoID, jobID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE yt_candidates SET job_id=$3 WHERE flow_id=$1 AND video_id=$2`, flowID, videoID, jobID)
	return err
}

// ReleaseYTReservation gives the slot back. permanent marks the video failed instead of waiting again.
func (s *Store) ReleaseYTReservation(ctx context.Context, flowID, videoID, reason string, permanent bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	state := domain.YTNew
	if permanent {
		state = domain.YTFailed
	}
	if _, err = tx.Exec(ctx, `UPDATE yt_candidates SET state=$3,reason=$4,slot_at=NULL,queued_at=NULL,job_id=NULL WHERE flow_id=$1 AND video_id=$2 AND state='queued'`, flowID, videoID, state, reason); err != nil {
		return err
	}
	if !permanent {
		if _, err = tx.Exec(ctx, `DELETE FROM yt_uses WHERE flow_id=$1 AND video_id=$2`, flowID, videoID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// SyncYTCandidates moves queued candidates to their final state from the job and publication outcome,
// and frees reservations whose job was never created.
func (s *Store) SyncYTCandidates(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	stmts := []string{
		`UPDATE yt_candidates c SET state='failed',reason='job failed: '||COALESCE(NULLIF(j.fail_place,''),'unknown') FROM jobs j WHERE c.state='queued' AND c.job_id=j.job_id AND j.status='failed'`,
		`UPDATE yt_candidates c SET state='done',reason='' WHERE c.state='queued' AND EXISTS(SELECT 1 FROM publications p JOIN clips cl ON cl.clip_id=p.clip_id WHERE cl.job_id=c.job_id AND p.status='published')`,
		`UPDATE yt_candidates c SET state=CASE WHEN EXISTS(SELECT 1 FROM publications p JOIN clips cl ON cl.clip_id=p.clip_id WHERE cl.job_id=c.job_id AND p.status IN('failed','blocked')) THEN 'failed' ELSE 'skipped' END,reason='publication cancelled or failed'
 WHERE c.state='queued' AND c.job_id IS NOT NULL AND EXISTS(SELECT 1 FROM publications p JOIN clips cl ON cl.clip_id=p.clip_id WHERE cl.job_id=c.job_id)
 AND NOT EXISTS(SELECT 1 FROM publications p JOIN clips cl ON cl.clip_id=p.clip_id WHERE cl.job_id=c.job_id AND p.status NOT IN('cancelled','failed','blocked'))`,
		`WITH r AS(UPDATE yt_candidates SET state='new',reason='reservation expired',slot_at=NULL,queued_at=NULL WHERE state='queued' AND job_id IS NULL AND queued_at<now()-interval '10 minutes' RETURNING flow_id,video_id)
 DELETE FROM yt_uses u USING r WHERE u.flow_id=r.flow_id AND u.video_id=r.video_id`,
	}
	for _, q := range stmts {
		if _, err = tx.Exec(ctx, q); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) YTClipTitle(ctx context.Context, jobID string) (string, bool, error) {
	var title string
	err := s.pool.QueryRow(ctx, `SELECT title FROM yt_candidates WHERE job_id=$1`, jobID).Scan(&title)
	if err == pgx.ErrNoRows {
		return "", false, nil
	}
	return title, err == nil, err
}

func (s *Store) YTReaders(ctx context.Context) ([]domain.YTReader, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,name,remote_account_id,credential_ref->>'bucket',credential_ref->>'object' FROM accounts
 WHERE platform='youtube' AND connection_state='connected' AND COALESCE(remote_account_id,'')<>'' AND COALESCE(credential_ref->>'object','')<>'' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.YTReader
	for rows.Next() {
		var r domain.YTReader
		if err = rows.Scan(&r.AccountID, &r.Name, &r.ChannelID, &r.CredentialRef.Bucket, &r.CredentialRef.Object); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ReserveYTQuota(ctx context.Context, day time.Time, flowID string, units, flowLimit, pool int) (bool, error) {
	if pool <= 0 || units <= 0 {
		return false, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lockYTQuota); err != nil {
		return false, err
	}
	var total, mine int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(sum(units),0)::int,COALESCE(sum(units) FILTER(WHERE flow_id=$2),0)::int FROM yt_quota WHERE day=$1::date`, ytDay(day), flowID).Scan(&total, &mine); err != nil {
		return false, err
	}
	if total+units > pool || (flowLimit > 0 && mine+units > flowLimit) {
		return false, nil
	}
	if _, err = tx.Exec(ctx, `INSERT INTO yt_quota(day,flow_id,units)VALUES($1::date,$2,$3)ON CONFLICT(day,flow_id)DO UPDATE SET units=yt_quota.units+EXCLUDED.units`, ytDay(day), flowID, units); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (s *Store) AdjustYTQuota(ctx context.Context, day time.Time, flowID string, delta int) error {
	if delta == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO yt_quota(day,flow_id,units)VALUES($1::date,$2,GREATEST($3,0))ON CONFLICT(day,flow_id)DO UPDATE SET units=GREATEST(yt_quota.units+$3,0)`, ytDay(day), flowID, delta)
	return err
}

func (s *Store) YTQuotaReport(ctx context.Context, day time.Time, pool int) (domain.YTQuotaReport, error) {
	r := domain.YTQuotaReport{Day: ytDay(day), Pool: pool, ByFlow: map[string]int{}}
	rows, err := s.pool.Query(ctx, `SELECT flow_id,units FROM yt_quota WHERE day=$1::date`, ytDay(day))
	if err != nil {
		return r, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err = rows.Scan(&id, &n); err != nil {
			return r, err
		}
		r.ByFlow[id] = n
		r.Used += n
	}
	if err = rows.Err(); err != nil {
		return r, err
	}
	rows.Close()
	err = s.pool.QueryRow(ctx, `SELECT true,message FROM yt_quota_breaker WHERE day=$1::date`, ytDay(day)).Scan(&r.Tripped, &r.TripNote)
	if err == pgx.ErrNoRows {
		err = nil
	}
	return r, err
}

func (s *Store) TripYTBreaker(ctx context.Context, day time.Time, message string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO yt_quota_breaker(day,message)VALUES($1::date,$2)ON CONFLICT(day)DO NOTHING`, ytDay(day), message)
	return err
}

func (s *Store) YTBreakerTripped(ctx context.Context, day time.Time) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM yt_quota_breaker WHERE day=$1::date)`, ytDay(day)).Scan(&ok)
	return ok, err
}
