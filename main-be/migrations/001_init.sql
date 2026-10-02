-- +goose Up
CREATE TABLE accounts (
    id        BIGSERIAL PRIMARY KEY,
    platform  TEXT NOT NULL,
    name      TEXT NOT NULL,
    url       TEXT,
    note      TEXT,
    bucket    TEXT,
    file_name TEXT,
    UNIQUE (platform, name)
);

CREATE TABLE banners (
    id        BIGSERIAL PRIMARY KEY,
    name      TEXT NOT NULL UNIQUE,
    note      TEXT,
    bucket    TEXT,
    file_name TEXT
);

CREATE TABLE backgrounds (
    id        BIGSERIAL PRIMARY KEY,
    name      TEXT NOT NULL UNIQUE,
    note      TEXT,
    bucket    TEXT,
    file_name TEXT
);

CREATE TABLE channels (
    id              BIGSERIAL PRIMARY KEY,
    name            TEXT NOT NULL,
    url             TEXT NOT NULL,
    yt_channel_id   TEXT NOT NULL UNIQUE,
    approve_mode    TEXT NOT NULL DEFAULT 'by_approve',
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    spec            JSONB NOT NULL DEFAULT '{}',
    last_checked_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE seen_videos (
    channel_id BIGINT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    video_id   TEXT NOT NULL,
    seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (channel_id, video_id)
);

CREATE TABLE jobs (
    job_id             TEXT PRIMARY KEY,
    channel_id         BIGINT REFERENCES channels(id) ON DELETE SET NULL,
    youtube_url        TEXT NOT NULL,
    is_vertical        BOOLEAN NOT NULL,
    is_subtitles       BOOLEAN NOT NULL,
    slice_mode         TEXT NOT NULL DEFAULT 'full',
    min_video_duration INT NOT NULL DEFAULT 60,
    background_id      BIGINT REFERENCES backgrounds(id) ON DELETE SET NULL,
    is_circle          BOOLEAN NOT NULL DEFAULT FALSE,
    approve_mode       TEXT NOT NULL DEFAULT 'by_approve',
    status             TEXT NOT NULL DEFAULT 'pending',
    fail_place         TEXT NOT NULL DEFAULT '',
    trace              TEXT,
    is_archived        BOOLEAN NOT NULL DEFAULT FALSE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sub_jobs (
    sub_job_id       TEXT PRIMARY KEY,
    job_id           TEXT NOT NULL REFERENCES jobs(job_id) ON DELETE CASCADE,
    banner_id        BIGINT REFERENCES banners(id) ON DELETE SET NULL,
    account_ids      BIGINT[] NOT NULL DEFAULT '{}',
    publish_step     INT NOT NULL DEFAULT 0,
    publish_datetime TIMESTAMPTZ,
    status           TEXT NOT NULL DEFAULT 'pending',
    fail_place       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sub_jobs_job_idx ON sub_jobs(job_id);

CREATE TABLE clips (
    clip_id       TEXT PRIMARY KEY,
    job_id        TEXT NOT NULL REFERENCES jobs(job_id) ON DELETE CASCADE,
    banner_object TEXT NOT NULL DEFAULT '',
    bucket        TEXT NOT NULL,
    object        TEXT NOT NULL,
    idx           INT NOT NULL DEFAULT 0,
    start_sec     DOUBLE PRECISION NOT NULL DEFAULT 0,
    end_sec       DOUBLE PRECISION NOT NULL DEFAULT 0,
    title         TEXT NOT NULL DEFAULT '',
    hashtags      TEXT[] NOT NULL DEFAULT '{}',
    approval      TEXT NOT NULL DEFAULT 'pending',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX clips_job_idx ON clips(job_id);

CREATE TABLE publications (
    id           BIGSERIAL PRIMARY KEY,
    clip_id      TEXT NOT NULL REFERENCES clips(clip_id) ON DELETE CASCADE,
    sub_job_id   TEXT REFERENCES sub_jobs(sub_job_id) ON DELETE SET NULL,
    account_id   BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    platform     TEXT NOT NULL,
    publish_at   TIMESTAMPTZ NOT NULL,
    status       TEXT NOT NULL DEFAULT 'scheduled',
    upload_id    TEXT,
    error        TEXT,
    published_at TIMESTAMPTZ,
    UNIQUE (clip_id, account_id)
);
CREATE INDEX publications_due_idx ON publications(status, publish_at);

-- +goose Down
DROP TABLE publications, clips, sub_jobs, jobs, seen_videos, channels, backgrounds, banners, accounts;
