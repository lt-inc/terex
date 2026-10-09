-- +goose Up
CREATE TABLE twitch_monitoring_snapshots (
 scope TEXT NOT NULL, key TEXT NOT NULL, epoch BIGINT NOT NULL,
 generated_at TIMESTAMPTZ NOT NULL, payload JSONB NOT NULL,
 PRIMARY KEY(scope,key)
);

-- +goose Down
DROP TABLE twitch_monitoring_snapshots;
