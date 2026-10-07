-- +goose Up
ALTER TABLE twitch_channel_limits ADD COLUMN schedule_horizon_hours INTEGER NOT NULL DEFAULT 48 CHECK(schedule_horizon_hours BETWEEN 1 AND 168);
UPDATE twitch_flows SET config=config||jsonb_build_object('schedule_horizon_hours',48,'max_source_age_hours',72);

CREATE TABLE twitch_policy_settings (
 id INTEGER PRIMARY KEY CHECK(id=1), mode TEXT NOT NULL DEFAULT 'preview' CHECK(mode IN('preview','enforce')),
 applied_at TIMESTAMPTZ
);
INSERT INTO twitch_policy_settings(id)VALUES(1);

CREATE TABLE twitch_preparation_reservations (
 id TEXT PRIMARY KEY, flow_id TEXT NOT NULL REFERENCES twitch_flows(id),
 youtube_channel_id TEXT NOT NULL, format TEXT NOT NULL CHECK(format IN('short','compilation')),
 slot_at TIMESTAMPTZ NOT NULL, state TEXT NOT NULL DEFAULT 'active' CHECK(state IN('active','released','consumed')),
 expires_at TIMESTAMPTZ NOT NULL, clip_id TEXT REFERENCES clips(clip_id),
 reason TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX twitch_preparation_slot ON twitch_preparation_reservations(youtube_channel_id,format,slot_at) WHERE state='active';
CREATE INDEX twitch_preparation_clip ON twitch_preparation_reservations(clip_id) WHERE state='active';
CREATE TABLE twitch_preparation_sources (
 reservation_id TEXT NOT NULL REFERENCES twitch_preparation_reservations(id),
 import_id TEXT NOT NULL REFERENCES twitch_imports(id), flow_id TEXT NOT NULL REFERENCES twitch_flows(id),
 duration_sec DOUBLE PRECISION NOT NULL CHECK(duration_sec>0),
 PRIMARY KEY(reservation_id,import_id)
);
CREATE INDEX twitch_preparation_import ON twitch_preparation_sources(import_id);

ALTER TABLE publications ADD COLUMN policy_hold_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE publications ADD COLUMN freshness_override_revision_id BIGINT REFERENCES clip_revisions(id);
ALTER TABLE publications ADD COLUMN freshness_override_publish_at TIMESTAMPTZ;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Freshness reservations require a reviewed rollback'; END $$;
-- +goose StatementEnd
