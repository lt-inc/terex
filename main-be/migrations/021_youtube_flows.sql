-- +goose Up
-- YouTube flows: find popular Shorts and republish them on our own channels.
-- Additive: Twitch tables are untouched; flows publish through ordinary jobs
-- (source_kind='youtube_flow', output_format='short'), so they occupy the same
-- calendar and daily limit as Twitch flows (twitch_channel_limits).
CREATE TABLE yt_flows (
 id TEXT PRIMARY KEY, name TEXT NOT NULL,
 account_id BIGINT NOT NULL REFERENCES accounts(id),
 enabled BOOLEAN NOT NULL DEFAULT FALSE,
 config JSONB NOT NULL, version INTEGER NOT NULL DEFAULT 1,
 last_run_at TIMESTAMPTZ, last_status TEXT NOT NULL DEFAULT '', last_error TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A source is a channel (UC...) watched through its RSS feed and Shorts playlist, or a search query.
CREATE TABLE yt_flow_sources (
 id BIGSERIAL PRIMARY KEY, flow_id TEXT NOT NULL REFERENCES yt_flows(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK(kind IN('channel','search')),
 value TEXT NOT NULL, title TEXT NOT NULL DEFAULT '', params JSONB NOT NULL DEFAULT '{}',
 last_rss_at TIMESTAMPTZ, last_scan_at TIMESTAMPTZ, last_error TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(flow_id,kind,value)
);

CREATE TABLE yt_candidates (
 flow_id TEXT NOT NULL REFERENCES yt_flows(id) ON DELETE CASCADE, video_id TEXT NOT NULL,
 source_id BIGINT REFERENCES yt_flow_sources(id) ON DELETE SET NULL,
 channel_id TEXT NOT NULL DEFAULT '', channel_title TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '',
 published_at TIMESTAMPTZ, duration_sec INTEGER NOT NULL DEFAULT 0,
 views BIGINT NOT NULL DEFAULT 0, likes BIGINT,
 first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(), stats_at TIMESTAMPTZ,
 state TEXT NOT NULL DEFAULT 'new' CHECK(state IN('new','queued','done','failed','skipped')),
 reason TEXT NOT NULL DEFAULT '',
 job_id TEXT, slot_at TIMESTAMPTZ, queued_at TIMESTAMPTZ,
 PRIMARY KEY(flow_id,video_id)
);
CREATE INDEX yt_candidates_pool ON yt_candidates(flow_id,state,published_at DESC);
CREATE INDEX yt_candidates_job ON yt_candidates(job_id) WHERE job_id IS NOT NULL;
CREATE INDEX yt_candidates_slot ON yt_candidates(slot_at) WHERE state='queued';

-- One video is republished at most once per destination channel, whichever flow picked it.
CREATE TABLE yt_uses (
 destination_key TEXT NOT NULL, video_id TEXT NOT NULL, flow_id TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(destination_key,video_id)
);

-- YouTube Data API units spent by flows. day is the Pacific-time date: the Google quota resets at its midnight.
CREATE TABLE yt_quota (
 day DATE NOT NULL, flow_id TEXT NOT NULL, units INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(day,flow_id)
);
-- A day listed here hit a real quotaExceeded: all flow reads stop until the next Pacific day.
CREATE TABLE yt_quota_breaker (day DATE PRIMARY KEY, tripped_at TIMESTAMPTZ NOT NULL DEFAULT now(), message TEXT NOT NULL DEFAULT '');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'YouTube flows migration is additive and requires a reviewed rollback'; END $$;
-- +goose StatementEnd
