package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"terex/main-be/internal/domain"
	"terex/proto/msg"
)

func (s *Store) CreateJob(ctx context.Context, j domain.NewJob) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `INSERT INTO jobs(job_id, channel_id, youtube_url, is_vertical, is_subtitles, slice_mode,
		min_video_duration, background_id, is_circle, approve_mode, trace) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''))`,
		j.JobID, j.ChannelID, j.YoutubeURL, j.IsVertical, j.IsSubtitles, j.SliceMode,
		j.MinVideoDuration, j.BackgroundID, j.IsCircle, j.ApproveMode, j.Trace)
	if err != nil {
		return err
	}
	for _, sj := range j.SubJobs {
		if _, err = tx.Exec(ctx, `INSERT INTO sub_jobs(sub_job_id, job_id, banner_id, account_ids, publish_step, publish_datetime)
			VALUES ($1,$2,$3,$4,$5,$6)`, sj.SubJobID, j.JobID, sj.BannerID, sj.AccountIDs, sj.PublishStep, sj.PublishDatetime); err != nil {
			return err
		}
		targets := sj.Targets
		if targets == nil {
			for _, aid := range sj.AccountIDs {
				targets = append(targets, domain.AccountTarget{AccountID: aid, StartAt: sj.PublishDatetime, IntervalSeconds: sj.PublishStep * 60, Timezone: "Asia/Tomsk"})
			}
		}
		for _, target := range targets {
			zone := target.Timezone
			if zone == "" {
				zone = "Asia/Tomsk"
			}
			if _, err = tx.Exec(ctx, `INSERT INTO sub_job_targets(sub_job_id,account_id,start_at,interval_seconds,timezone)VALUES($1,$2,$3,$4,$5)`, sj.SubJobID, target.AccountID, target.StartAt, target.IntervalSeconds, zone); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

const jobCols = `j.job_id, j.channel_id, j.youtube_url, j.is_vertical, j.is_subtitles, j.slice_mode, j.min_video_duration,
	j.background_id, bg.name, j.is_circle, j.approve_mode, j.status, j.fail_place, j.trace, j.is_archived, j.created_at, j.updated_at`

func scanJob(r pgx.Row) (domain.Job, error) {
	var j domain.Job
	var created, updated time.Time
	err := r.Scan(&j.JobID, &j.ChannelID, &j.YoutubeURL, &j.IsVertical, &j.IsSubtitles, &j.SliceMode, &j.MinVideoDuration,
		&j.BackgroundID, &j.BackgroundName, &j.IsCircle, &j.ApproveMode, &j.Status, &j.FailPlace, &j.Trace, &j.IsArchived, &created, &updated)
	j.IsBackground = j.BackgroundID != nil
	j.CreateDatetime, j.LastChange = &created, &updated
	j.SubJobs = []domain.SubJob{}
	return j, err
}

func (s *Store) ListJobs(ctx context.Context, showArchived bool) ([]domain.Job, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+jobCols+` FROM jobs j LEFT JOIN backgrounds bg ON bg.id=j.background_id
		WHERE ($1 OR NOT j.is_archived) ORDER BY j.created_at DESC`, showArchived)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []domain.Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return jobs, s.hydrate(ctx, jobs)
}

func (s *Store) GetJob(ctx context.Context, id string) (domain.Job, error) {
	j, err := scanJob(s.pool.QueryRow(ctx, `SELECT `+jobCols+` FROM jobs j LEFT JOIN backgrounds bg ON bg.id=j.background_id WHERE j.job_id=$1`, id))
	if err != nil {
		return j, mapErr(err)
	}
	jobs := []domain.Job{j}
	if err := s.hydrate(ctx, jobs); err != nil {
		return j, err
	}
	return jobs[0], nil
}

// hydrate fills sub-jobs (with account/banner names) and publications for the given jobs.
func (s *Store) hydrate(ctx context.Context, jobs []domain.Job) error {
	if len(jobs) == 0 {
		return nil
	}
	ids := make([]string, len(jobs))
	idx := map[string]int{}
	for i, j := range jobs {
		ids[i] = j.JobID
		idx[j.JobID] = i
	}

	accNames := map[int64]string{}
	rows, err := s.pool.Query(ctx, "SELECT id, name FROM accounts")
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var n string
		if err := rows.Scan(&id, &n); err != nil {
			rows.Close()
			return err
		}
		accNames[id] = n
	}
	rows.Close()

	rows, err = s.pool.Query(ctx, `SELECT sj.sub_job_id, sj.job_id, sj.banner_id, b.name, sj.account_ids, sj.publish_step,
		sj.publish_datetime, sj.status, sj.fail_place,sj.workflow_version FROM sub_jobs sj LEFT JOIN banners b ON b.id=sj.banner_id
		WHERE sj.job_id = ANY($1) ORDER BY sj.sub_job_id`, ids)
	if err != nil {
		return err
	}
	subIdx := map[string][2]int{}
	for rows.Next() {
		var sj domain.SubJob
		if err := rows.Scan(&sj.SubJobID, &sj.JobID, &sj.BannerID, &sj.BannerName, &sj.AccountIDs, &sj.PublishStep,
			&sj.PublishDatetime, &sj.Status, &sj.FailPlace, &sj.Version); err != nil {
			rows.Close()
			return err
		}
		sj.IsAddBanner = sj.BannerID != nil
		sj.AccountNames = []string{}
		for _, id := range sj.AccountIDs {
			if n, ok := accNames[id]; ok {
				sj.AccountNames = append(sj.AccountNames, n)
			}
		}
		sj.Outs = []domain.Publication{}
		ji := idx[sj.JobID]
		jobs[ji].SubJobs = append(jobs[ji].SubJobs, sj)
		subIdx[sj.SubJobID] = [2]int{ji, len(jobs[ji].SubJobs) - 1}
	}
	rows.Close()

	for ji := range jobs {
		for si := range jobs[ji].SubJobs {
			targets, e := s.Targets(ctx, jobs[ji].SubJobs[si].SubJobID)
			if e != nil {
				return e
			}
			jobs[ji].SubJobs[si].Targets = targets
		}
	}

	prows, err := s.pool.Query(ctx, pubSelect+` WHERE c.job_id = ANY($1) ORDER BY p.id`, ids)
	if err != nil {
		return err
	}
	defer prows.Close()
	for prows.Next() {
		p, err := scanPub(prows)
		if err != nil {
			return err
		}
		if p.SubJobID == nil {
			continue
		}
		if pos, ok := subIdx[*p.SubJobID]; ok {
			sj := &jobs[pos[0]].SubJobs[pos[1]]
			sj.Outs = append(sj.Outs, p)
		}
	}
	return prows.Err()
}

func (s *Store) UpdateJob(ctx context.Context, id string, u domain.JobUpdate) error {
	_, err := s.pool.Exec(ctx, `UPDATE jobs SET youtube_url=COALESCE($2,youtube_url), is_vertical=COALESCE($3,is_vertical),
		is_subtitles=COALESCE($4,is_subtitles), slice_mode=COALESCE($5,slice_mode), min_video_duration=COALESCE($6,min_video_duration),
		background_id=COALESCE($7,background_id), is_circle=COALESCE($8,is_circle), status=COALESCE($9,status),
		fail_place=COALESCE($10,fail_place), is_archived=COALESCE($11,is_archived), updated_at=now() WHERE job_id=$1`,
		id, u.YoutubeURL, u.IsVertical, u.IsSubtitles, u.SliceMode, u.MinVideoDuration, u.BackgroundID, u.IsCircle,
		u.Status, u.FailPlace, u.IsArchived)
	return err
}

func (s *Store) DeleteJob(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE publications SET status=CASE WHEN started_at IS NULL THEN 'cancelled' ELSE 'cancel_pending' END,cancel_requested=TRUE,version=version+1,next_attempt_at=now() WHERE clip_id IN (SELECT clip_id FROM clips WHERE job_id=$1) AND status NOT IN ('published','cancelled')`, id); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE jobs SET is_archived=TRUE,updated_at=now() WHERE job_id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return tx.Commit(ctx)
}

func (s *Store) UpdateSubJob(ctx context.Context, id string, u domain.SubJobUpdate, accountIDs []int64, bannerID *int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE sub_jobs SET publish_datetime=COALESCE($2,publish_datetime), status=COALESCE($3,status),
		fail_place=COALESCE($4,fail_place), account_ids=COALESCE($5,account_ids), banner_id=COALESCE($6,banner_id) WHERE sub_job_id=$1`,
		id, u.PublishDatetime, u.Status, u.FailPlace, accountIDs, bannerID)
	return err
}

func (s *Store) DeleteSubJob(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE publications SET status=CASE WHEN started_at IS NULL THEN 'cancelled' ELSE 'cancel_pending' END,cancel_requested=TRUE,version=version+1,next_attempt_at=now() WHERE sub_job_id=$1 AND status NOT IN ('published','cancelled')`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE publication_attempts SET status='superseded',lease_token=NULL WHERE publication_id IN (SELECT id FROM publications WHERE sub_job_id=$1) AND status='active'`, id); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM sub_jobs WHERE sub_job_id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return tx.Commit(ctx)
}

func (s *Store) SetJobStatus(ctx context.Context, id, status, failPlace string) error {
	_, err := s.pool.Exec(ctx, "UPDATE jobs SET status=$2, fail_place=$3, updated_at=now() WHERE job_id=$1", id, status, failPlace)
	return err
}

func (s *Store) JobProcessMessage(ctx context.Context, id string) (msg.ProcessVideo, error) {
	var m msg.ProcessVideo
	var circle, bg bool
	var bgFile *string
	var slice string
	err := s.pool.QueryRow(ctx, `SELECT j.job_id, j.youtube_url, j.is_vertical, j.is_subtitles, j.slice_mode, j.min_video_duration,
		j.is_circle, j.background_id IS NOT NULL, bg.file_name FROM jobs j LEFT JOIN backgrounds bg ON bg.id=j.background_id
		WHERE j.job_id=$1`, id).Scan(&m.JobID, &m.YTURL, &m.Options.Vertical, &m.Options.Subtitles, &slice,
		&m.Options.MinDuration, &circle, &bg, &bgFile)
	if err != nil {
		return m, mapErr(err)
	}
	m.Options.SliceMode = msg.SliceMode(slice)
	m.Options.Shape = "rectangle"
	if circle {
		m.Options.Shape = "circle"
	}
	if bg && bgFile != nil {
		m.Options.BackgroundID = *bgFile
	}
	rows, err := s.pool.Query(ctx, `SELECT COALESCE(b.file_name,'') FROM sub_jobs sj LEFT JOIN banners b ON b.id=sj.banner_id
		WHERE sj.job_id=$1 ORDER BY sj.sub_job_id`, id)
	if err != nil {
		return m, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return m, err
		}
		if !seen[f] {
			seen[f] = true
			m.Options.BannerIDs = append(m.Options.BannerIDs, f)
		}
	}
	return m, rows.Err()
}
