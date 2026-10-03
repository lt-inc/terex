-- +goose Up
ALTER TABLE accounts ADD COLUMN connection_state TEXT NOT NULL DEFAULT 'disconnected';
ALTER TABLE accounts ADD COLUMN remote_account_id TEXT;
ALTER TABLE accounts ADD COLUMN credential_ref JSONB NOT NULL DEFAULT '{}';
ALTER TABLE accounts ADD COLUMN connection_error TEXT;
ALTER TABLE accounts ADD COLUMN connection_checked_at TIMESTAMPTZ;
ALTER TABLE accounts ADD COLUMN connection_scopes TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE accounts ADD COLUMN credential_expires_at TIMESTAMPTZ;
ALTER TABLE accounts ADD COLUMN connection_version INTEGER NOT NULL DEFAULT 1;

CREATE TABLE oauth_sessions (
    state TEXT PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    redirect_uri TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ
);

CREATE TABLE sub_job_targets (
    sub_job_id TEXT NOT NULL REFERENCES sub_jobs(sub_job_id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    start_at TIMESTAMPTZ,
    interval_seconds INTEGER NOT NULL DEFAULT 0 CHECK (interval_seconds >= 0),
    timezone TEXT NOT NULL DEFAULT 'Asia/Tomsk',
    legacy_shared BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (sub_job_id, account_id)
);
ALTER TABLE sub_jobs ADD COLUMN workflow_version INTEGER NOT NULL DEFAULT 1;
INSERT INTO sub_job_targets(sub_job_id, account_id, start_at, interval_seconds, legacy_shared)
SELECT sj.sub_job_id, a.id, sj.publish_datetime, GREATEST(sj.publish_step,0)*60, TRUE
FROM sub_jobs sj JOIN accounts a ON a.id=ANY(sj.account_ids);

CREATE TABLE clip_revisions (
    id BIGSERIAL PRIMARY KEY,
    clip_id TEXT NOT NULL REFERENCES clips(clip_id) ON DELETE CASCADE,
    revision_no INTEGER NOT NULL,
    spec JSONB NOT NULL,
    render_state TEXT NOT NULL DEFAULT 'ready',
    bucket TEXT,
    object TEXT,
    manifest JSONB NOT NULL DEFAULT '{}',
    approved_at TIMESTAMPTZ,
    error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(clip_id, revision_no)
);
INSERT INTO clip_revisions(clip_id,revision_no,spec,bucket,object,approved_at)
SELECT clip_id,1,jsonb_build_object('start_sec',start_sec,'end_sec',end_sec,'title',title,'description','','hashtags',hashtags,'cues','[]'::jsonb),bucket,object,
CASE WHEN approval='approved' THEN created_at END FROM clips;
ALTER TABLE clips ADD COLUMN latest_revision_id BIGINT REFERENCES clip_revisions(id) ON DELETE SET NULL;
UPDATE clips c SET latest_revision_id=r.id FROM clip_revisions r WHERE r.clip_id=c.clip_id AND r.revision_no=1;

ALTER TABLE publications ADD COLUMN revision_id BIGINT REFERENCES clip_revisions(id);
ALTER TABLE publications ADD COLUMN version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE publications ADD COLUMN timezone TEXT NOT NULL DEFAULT 'Asia/Tomsk';
ALTER TABLE publications ADD COLUMN metadata JSONB NOT NULL DEFAULT '{}';
ALTER TABLE publications ADD COLUMN cancel_requested BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE publications ADD COLUMN next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE publications ADD COLUMN started_at TIMESTAMPTZ;
ALTER TABLE publications ADD COLUMN uploaded_at TIMESTAMPTZ;
ALTER TABLE publications ADD COLUMN processed_at TIMESTAMPTZ;
ALTER TABLE publications ADD COLUMN last_verified_at TIMESTAMPTZ;
ALTER TABLE publications ADD COLUMN external_url TEXT;
ALTER TABLE publications ADD COLUMN legacy_status TEXT;
ALTER TABLE publications ADD COLUMN legacy_upload_id TEXT;
ALTER TABLE publications ADD COLUMN legacy_published_at TIMESTAMPTZ;
UPDATE publications p SET revision_id=c.latest_revision_id, legacy_status=p.status,
legacy_upload_id=p.upload_id, legacy_published_at=p.published_at, published_at=NULL,
upload_id=CASE WHEN p.upload_id LIKE 'stub-%' THEN NULL ELSE p.upload_id END,
status='needs_attention',metadata=r.spec, error='Legacy publication requires manual review before real publishing'
FROM clips c JOIN clip_revisions r ON r.id=c.latest_revision_id WHERE c.clip_id=p.clip_id;

CREATE TABLE publication_attempts (
    id TEXT PRIMARY KEY,
    publication_id BIGINT NOT NULL REFERENCES publications(id) ON DELETE CASCADE,
    publication_version INTEGER NOT NULL,
    worker_id TEXT NOT NULL DEFAULT '',
    lease_token TEXT,
    lease_until TIMESTAMPTZ,
    phase TEXT NOT NULL DEFAULT 'queued',
    status TEXT NOT NULL DEFAULT 'active',
    checkpoint JSONB NOT NULL DEFAULT '{}',
    error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX publication_one_active_attempt ON publication_attempts(publication_id) WHERE status='active';
CREATE INDEX publication_attempt_lease ON publication_attempts(lease_until) WHERE status='active';

CREATE TABLE render_attempts (
    id TEXT PRIMARY KEY,
    revision_id BIGINT NOT NULL REFERENCES clip_revisions(id) ON DELETE CASCADE,
    worker_id TEXT NOT NULL DEFAULT '',
    lease_token TEXT,
    lease_until TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX render_one_active_attempt ON render_attempts(revision_id) WHERE status='active';

CREATE TABLE publication_outbox (
    id TEXT PRIMARY KEY,
    publication_id BIGINT REFERENCES publications(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    payload JSONB NOT NULL,
    delivered_at TIMESTAMPTZ,
    next_delivery_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    attempts INTEGER NOT NULL DEFAULT 0,
    error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX publication_outbox_pending ON publication_outbox(next_delivery_at) WHERE delivered_at IS NULL;

CREATE TABLE publication_events (
    id TEXT PRIMARY KEY,
    publication_id BIGINT NOT NULL REFERENCES publications(id) ON DELETE CASCADE,
    attempt_id TEXT,
    kind TEXT NOT NULL,
    state TEXT NOT NULL,
    detail JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX publication_events_history ON publication_events(publication_id,created_at);
INSERT INTO publication_events(id,publication_id,kind,state,detail)
SELECT 'legacy-'||p.id::text,p.id,'legacy_import','needs_attention',
jsonb_build_object('previous_status',p.legacy_status,'previous_upload_id',p.legacy_upload_id,'previous_published_at',p.legacy_published_at)
FROM publications p WHERE p.legacy_status IS NOT NULL;

-- +goose Down
DROP TABLE publication_events, publication_outbox, publication_attempts, render_attempts;
ALTER TABLE publications DROP COLUMN revision_id, DROP COLUMN version, DROP COLUMN timezone,
DROP COLUMN metadata, DROP COLUMN cancel_requested, DROP COLUMN next_attempt_at, DROP COLUMN started_at,
DROP COLUMN uploaded_at, DROP COLUMN processed_at, DROP COLUMN last_verified_at, DROP COLUMN external_url, DROP COLUMN legacy_status,
DROP COLUMN legacy_upload_id, DROP COLUMN legacy_published_at;
ALTER TABLE clips DROP COLUMN latest_revision_id;
DROP TABLE clip_revisions, sub_job_targets, oauth_sessions;
ALTER TABLE sub_jobs DROP COLUMN workflow_version;
ALTER TABLE accounts DROP COLUMN connection_state, DROP COLUMN remote_account_id, DROP COLUMN credential_ref,
DROP COLUMN connection_error, DROP COLUMN connection_checked_at, DROP COLUMN connection_scopes, DROP COLUMN credential_expires_at, DROP COLUMN connection_version;
