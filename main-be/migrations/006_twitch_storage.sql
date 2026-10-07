-- +goose Up
CREATE TABLE twitch_storage_objects (
 bucket TEXT NOT NULL, object TEXT NOT NULL, kind TEXT NOT NULL,
 size_bytes BIGINT NOT NULL DEFAULT 0, present BOOLEAN NOT NULL DEFAULT TRUE,
 accepted_staging BOOLEAN NOT NULL DEFAULT FALSE,
 state TEXT NOT NULL DEFAULT 'available' CHECK(state IN('available','deleting','deleted')),
 imported_at TIMESTAMPTZ NOT NULL, modified_at TIMESTAMPTZ NOT NULL,
 last_seen_at TIMESTAMPTZ NOT NULL, deleted_at TIMESTAMPTZ,
 PRIMARY KEY(bucket,object)
);
CREATE TABLE twitch_storage_references (
 bucket TEXT NOT NULL, object TEXT NOT NULL, ref_key TEXT NOT NULL,
 import_id TEXT, clip_id TEXT, revision_id BIGINT,
 PRIMARY KEY(bucket,object,ref_key)
);
CREATE INDEX twitch_storage_ref_clip ON twitch_storage_references(clip_id);
CREATE INDEX twitch_storage_ref_import ON twitch_storage_references(import_id);
CREATE TABLE twitch_storage_pins (
 kind TEXT NOT NULL CHECK(kind IN('import','clip')), target_id TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(kind,target_id)
);
CREATE TABLE twitch_storage_deletions (
 bucket TEXT NOT NULL, object TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN('pending','failed','deleted')),
 attempts INTEGER NOT NULL DEFAULT 0, next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 error TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), deleted_at TIMESTAMPTZ,
 PRIMARY KEY(bucket,object)
);
ALTER TABLE twitch_media ADD COLUMN storage_state TEXT NOT NULL DEFAULT 'available';
ALTER TABLE clip_revisions ADD COLUMN storage_state TEXT NOT NULL DEFAULT 'available';
ALTER TABLE twitch_imports ADD COLUMN verified_at TIMESTAMPTZ;
UPDATE twitch_imports SET verified_at=COALESCE((SELECT created_at FROM twitch_media WHERE id=media_id),updated_at) WHERE media_state='ready';
CREATE TABLE twitch_storage_pressure (
 id INTEGER PRIMARY KEY CHECK(id=1), required BOOLEAN NOT NULL DEFAULT FALSE,
 state TEXT NOT NULL DEFAULT 'off' CHECK(state IN('off','ok','low','unknown')),
 available_bytes BIGINT, total_bytes BIGINT, observed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 error TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL DEFAULT ''
);
INSERT INTO twitch_storage_pressure(id)VALUES(1);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Storage tombstones preserve deduplication and require a reviewed rollback'; END $$;
-- +goose StatementEnd
