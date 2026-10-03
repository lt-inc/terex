package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"terex/main-be/internal/domain"
)

const pubSelect = `SELECT p.id, c.job_id, p.sub_job_id, p.clip_id, c.object, c.title, NULL::text,
	NULLIF(array_to_string(c.hashtags,' '),''), p.upload_id, p.publish_at, p.published_at, p.status, p.account_id, a.name, p.platform
	FROM publications p JOIN clips c ON c.clip_id=p.clip_id JOIN accounts a ON a.id=p.account_id`

func scanPub(r pgx.Row) (domain.Publication, error) {
	var p domain.Publication
	err := r.Scan(&p.ID, &p.JobID, &p.SubJobID, &p.ClipID, &p.VideoName, &p.Name, &p.Description, &p.Hashtag,
		&p.UploadID, &p.PublishDate, &p.PublishedAt, &p.Status, &p.AccountID, &p.AccountName, &p.Platform)
	return p, err
}

func (s *Store) SaveClips(ctx context.Context, jobID string, clips []domain.Clip) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, c := range clips {
		hashtags := c.Hashtags
		if hashtags == nil {
			// nil биндится pgx как SQL NULL (а не пустой массив из DEFAULT),
			// что нарушает NOT NULL — gpu-be/LLM может вернуть клип без хэштегов.
			hashtags = []string{}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO clips(clip_id, job_id, banner_object, bucket, object, idx, start_sec, end_sec, title, hashtags)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT (clip_id) DO NOTHING`,
			c.ClipID, jobID, c.BannerObject, c.Bucket, c.Object, c.Idx, c.StartSec, c.EndSec, c.Title, hashtags); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

const clipCols = "clip_id, job_id, banner_object, bucket, object, idx, start_sec, end_sec, title, hashtags, approval, created_at"

func scanClip(r pgx.Row) (domain.Clip, error) {
	var c domain.Clip
	err := r.Scan(&c.ClipID, &c.JobID, &c.BannerObject, &c.Bucket, &c.Object, &c.Idx, &c.StartSec, &c.EndSec,
		&c.Title, &c.Hashtags, &c.Approval, &c.CreatedAt)
	return c, err
}

// ListClips returns clips with the given approval state ("" = all), newest first.
func (s *Store) ListClips(ctx context.Context, approval string) ([]domain.Clip, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+clipCols+" FROM clips WHERE ($1='' OR approval=$1) ORDER BY created_at DESC, idx", approval)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Clip{}
	for rows.Next() {
		c, err := scanClip(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetClip(ctx context.Context, id string) (domain.Clip, error) {
	c, err := scanClip(s.pool.QueryRow(ctx, "SELECT "+clipCols+" FROM clips WHERE clip_id=$1", id))
	return c, mapErr(err)
}

func (s *Store) SetApproval(ctx context.Context, id, approval string) error {
	tag, err := s.pool.Exec(ctx, "UPDATE clips SET approval=$2 WHERE clip_id=$1", id, approval)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

// SchedulePublications creates one publication per (matching sub-job account). The publish time is the
// sub-job's start (or now) plus idx*step minutes. Existing rows are kept (idempotent).
func (s *Store) SchedulePublications(ctx context.Context, clipID string, now time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx, `INSERT INTO publications(clip_id, sub_job_id, account_id, platform, publish_at)
		SELECT c.clip_id, sj.sub_job_id, a.id, a.platform,
		       COALESCE(sj.publish_datetime, $2) + make_interval(mins => c.idx * sj.publish_step)
		FROM clips c
		JOIN sub_jobs sj ON sj.job_id=c.job_id
		LEFT JOIN banners b ON b.id=sj.banner_id
		JOIN accounts a ON a.id = ANY(sj.account_ids)
		WHERE c.clip_id=$1 AND COALESCE(b.file_name,'') = c.banner_object
		ON CONFLICT (clip_id, account_id) DO NOTHING`, clipID, now)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// DuePublications atomically claims scheduled publications whose time has come.
func (s *Store) DuePublications(ctx context.Context, now time.Time, limit int) ([]domain.DuePublication, error) {
	rows, err := s.pool.Query(ctx, `UPDATE publications SET status='sent' WHERE id IN (
		SELECT id FROM publications WHERE status='scheduled' AND publish_at <= $1
		ORDER BY publish_at LIMIT $2 FOR UPDATE SKIP LOCKED)
		RETURNING id, clip_id, account_id, platform, publish_at`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.DuePublication
	for rows.Next() {
		var d domain.DuePublication
		if err := rows.Scan(&d.ID, &d.ClipID, &d.AccountID, &d.Platform, &d.PublishAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) MarkPublication(ctx context.Context, clipID string, accountID int64, status, uploadID, errMsg string) error {
	_, err := s.pool.Exec(ctx, `UPDATE publications SET status=$3, upload_id=NULLIF($4,''), error=NULLIF($5,''),
		published_at = CASE WHEN $3='published' THEN now() ELSE published_at END WHERE clip_id=$1 AND account_id=$2`,
		clipID, accountID, status, uploadID, errMsg)
	return err
}

// AccountPublications kind: "clips" (all), "scheduled" (not yet published), "published".
func (s *Store) AccountPublications(ctx context.Context, accountID int64, kind string) ([]domain.Publication, error) {
	where := " WHERE p.account_id=$1"
	switch kind {
	case "scheduled":
		where += " AND p.status IN ('scheduled','sent')"
	case "published":
		where += " AND p.status='published'"
	}
	rows, err := s.pool.Query(ctx, pubSelect+where+" ORDER BY p.publish_at", accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Publication{}
	for rows.Next() {
		p, err := scanPub(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) JobApproveMode(ctx context.Context, jobID string) (string, error) {
	var m string
	err := s.pool.QueryRow(ctx, "SELECT approve_mode FROM jobs WHERE job_id=$1", jobID).Scan(&m)
	return m, mapErr(err)
}
