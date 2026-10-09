-- +goose Up
CREATE TABLE twitch_view_samples (
 clip_id TEXT NOT NULL,
 observed_at TIMESTAMPTZ NOT NULL,
 view_count BIGINT NOT NULL CHECK(view_count>=0),
 counter_epoch BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY(clip_id,observed_at)
);
CREATE INDEX twitch_view_samples_recent ON twitch_view_samples(observed_at);
CREATE INDEX twitch_growth_broadcaster ON twitch_imports((request->'source'->>'broadcaster_id'));

-- +goose Down
DROP INDEX twitch_growth_broadcaster;
DROP TABLE twitch_view_samples;
