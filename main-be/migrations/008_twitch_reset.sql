-- +goose Up
CREATE TABLE twitch_reset_control (
 id integer PRIMARY KEY CHECK(id=1), epoch bigint NOT NULL DEFAULT 0,
 active boolean NOT NULL DEFAULT false, reset_id text
);
INSERT INTO twitch_reset_control(id) VALUES(1);
CREATE TABLE twitch_reset_previews (
 token text PRIMARY KEY, counts jsonb NOT NULL, expires_at timestamptz NOT NULL,
 used_by text
);
CREATE TABLE twitch_reset_runs (
 id text PRIMARY KEY, epoch bigint NOT NULL UNIQUE,
 state text NOT NULL CHECK(state IN('stopping','deleting','clearing','completed','failed')),
 collector_ack boolean NOT NULL DEFAULT false,gpu_drained boolean NOT NULL DEFAULT false,
 counts jsonb NOT NULL,blockers jsonb NOT NULL DEFAULT '[]',error text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL,updated_at timestamptz NOT NULL,completed_at timestamptz
);
CREATE UNIQUE INDEX twitch_reset_one_active ON twitch_reset_runs((true)) WHERE state <> 'completed';
CREATE TABLE twitch_reset_objects (
 reset_id text NOT NULL REFERENCES twitch_reset_runs(id),bucket text NOT NULL,object text NOT NULL,
 size_bytes bigint NOT NULL DEFAULT 0,state text NOT NULL DEFAULT 'pending' CHECK(state IN('pending','deleted','preserved')),
 PRIMARY KEY(reset_id,bucket,object)
);
ALTER TABLE twitch_imports ADD COLUMN epoch bigint NOT NULL DEFAULT 0;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Reset epochs and deletion tombstones must not be rolled back'; END $$;
-- +goose StatementEnd
