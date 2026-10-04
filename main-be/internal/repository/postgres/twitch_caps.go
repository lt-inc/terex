package postgres

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"terex/main-be/internal/domain"
	"time"
)

// Every scheduling mutation acquires lockTwitch before row locks. Counting and
// changing plans therefore share one transaction, including concurrent approvals.
func enforceTwitchCaps(ctx context.Context, tx pgx.Tx) error {
	settings, err := readTwitchSettings(ctx, tx)
	if err != nil {
		return err
	}
	var format, day string
	var count int
	err = tx.QueryRow(ctx, `SELECT j.output_format,(p.publish_at AT TIME ZONE $1)::date::text,count(*)
 FROM publications p JOIN clips c ON c.clip_id=p.clip_id JOIN jobs j ON j.job_id=c.job_id
 WHERE j.source_kind IN('twitch','twitch_compilation') AND p.status NOT IN('cancelled')
 GROUP BY p.account_id,j.output_format,(p.publish_at AT TIME ZONE $1)::date
 HAVING count(*)>CASE WHEN j.output_format='compilation' THEN $2::int ELSE $3::int END
 LIMIT 1`, settings.Timezone, settings.DailyCompilations, settings.DailyShorts).Scan(&format, &day, &count)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: daily %s publication limit exceeded on %s; choose another date or cancel a plan", domain.ErrConflict, format, day)
}

func requireTwitchSources(ctx context.Context, tx pgx.Tx, clipID string, revisionID int64) error {
	var denied bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_imports WHERE clip_id=$1 AND rejected) OR EXISTS(SELECT 1 FROM twitch_compilation_items t JOIN twitch_imports i ON i.clip_id=t.source_clip_id JOIN clips c ON c.clip_id=i.clip_id WHERE t.compilation_revision_id=$2 AND (i.rejected OR c.approval='rejected'))`, clipID, revisionID).Scan(&denied)
	if err != nil {
		return err
	}
	if denied {
		return fmt.Errorf("%w: a Twitch source is rejected", domain.ErrConflict)
	}
	return nil
}

// Backlog from earlier dates also shares today's upload cap. A queued command
// reserves capacity before another scheduler/approval can admit one more item.
func twitchRuntimeCapacity(ctx context.Context, tx pgx.Tx, publicationID int64, now time.Time) (bool, error) {
	settings, err := readTwitchSettings(ctx, tx)
	if err != nil {
		return false, err
	}
	var kind, format string
	var account int64
	var alreadyStarted, cancelling bool
	err = tx.QueryRow(ctx, `SELECT j.source_kind,j.output_format,p.account_id,p.started_at IS NOT NULL,p.cancel_requested FROM publications p JOIN clips c ON c.clip_id=p.clip_id JOIN jobs j ON j.job_id=c.job_id WHERE p.id=$1`, publicationID).Scan(&kind, &format, &account, &alreadyStarted, &cancelling)
	if err != nil {
		return false, err
	}
	if (kind != "twitch" && kind != "twitch_compilation") || alreadyStarted || cancelling {
		return true, nil
	}
	loc, _ := time.LoadLocation(settings.Timezone)
	day := now.In(loc).Format("2006-01-02")
	var count int
	err = tx.QueryRow(ctx, `SELECT count(*) FROM publications p JOIN clips c ON c.clip_id=p.clip_id JOIN jobs j ON j.job_id=c.job_id
 WHERE p.account_id=$1 AND j.output_format=$2 AND j.source_kind IN('twitch','twitch_compilation') AND p.id<>$3 AND p.status<>'cancelled'
 AND ((COALESCE(p.started_at,p.published_at) AT TIME ZONE $4)::date=$5::date OR
  (p.status='queued' AND EXISTS(SELECT 1 FROM publication_attempts a WHERE a.publication_id=p.id AND a.status='active' AND (a.created_at AT TIME ZONE $4)::date=$5::date)))`, account, format, publicationID, settings.Timezone, day).Scan(&count)
	limit := settings.DailyShorts
	if format == "compilation" {
		limit = settings.DailyCompilations
	}
	return count < limit, err
}
