-- +goose Up
ALTER TABLE render_attempts ADD COLUMN execution_started_at TIMESTAMPTZ;
ALTER TABLE render_attempts ADD COLUMN dispatch_retry_at TIMESTAMPTZ;
ALTER TABLE render_attempts ADD COLUMN dispatch_deadline TIMESTAMPTZ;
ALTER TABLE render_attempts ADD COLUMN epoch BIGINT NOT NULL DEFAULT 0;
ALTER TABLE render_attempts ADD COLUMN twitch BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE clip_revisions ADD COLUMN render_first_queued_at TIMESTAMPTZ;
ALTER TABLE clip_revisions ADD COLUMN render_dispatch_deadline TIMESTAMPTZ;
ALTER TABLE clip_revisions ADD COLUMN render_next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE clip_revisions ADD COLUMN render_wait_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE clip_revisions ADD COLUMN render_deadline_reason TEXT NOT NULL DEFAULT 'worker_wait_timeout';

-- Assigned work keeps its original execution lease. Unclaimed commands gain a
-- bounded delivery window instead of expiring every two minutes.
UPDATE render_attempts a SET execution_started_at=CASE WHEN a.worker_id<>'' THEN a.created_at END,
 dispatch_retry_at=a.created_at+interval '15 minutes',dispatch_deadline=a.created_at+interval '24 hours',
 epoch=CASE WHEN j.source_kind IN('twitch','twitch_compilation') THEN ctl.epoch ELSE 0 END,
 twitch=j.source_kind IN('twitch','twitch_compilation')
 FROM clip_revisions r JOIN clips c ON c.clip_id=r.clip_id JOIN jobs j ON j.job_id=c.job_id CROSS JOIN twitch_reset_control ctl
 WHERE r.id=a.revision_id AND ctl.id=1;
UPDATE render_attempts SET lease_until=dispatch_deadline WHERE status='active' AND worker_id='';
UPDATE clip_revisions r SET render_first_queued_at=a.first_at,render_dispatch_deadline=a.first_at+interval '24 hours'
 FROM (SELECT revision_id,min(created_at) first_at FROM render_attempts GROUP BY revision_id)a WHERE a.revision_id=r.id;
CREATE INDEX render_dispatch_pending ON render_attempts(dispatch_retry_at) WHERE status='active' AND execution_started_at IS NULL;

-- +goose Down
DROP INDEX render_dispatch_pending;
ALTER TABLE render_attempts DROP COLUMN execution_started_at,DROP COLUMN dispatch_retry_at,DROP COLUMN dispatch_deadline,DROP COLUMN epoch,DROP COLUMN twitch;
ALTER TABLE clip_revisions DROP COLUMN render_first_queued_at,DROP COLUMN render_dispatch_deadline,DROP COLUMN render_next_attempt_at,DROP COLUMN render_wait_reason,DROP COLUMN render_deadline_reason;
