-- +goose Up
ALTER TABLE accounts ADD COLUMN session_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE twitch_flows ADD COLUMN destination_key TEXT NOT NULL DEFAULT '';
UPDATE twitch_flows SET destination_key=youtube_channel_id;
ALTER TABLE publications ADD COLUMN destination_key TEXT NOT NULL DEFAULT '';
ALTER TABLE publications ADD COLUMN target_snapshot JSONB NOT NULL DEFAULT '{}';
ALTER TABLE publications ADD COLUMN error_code TEXT NOT NULL DEFAULT '';
ALTER TABLE clip_revisions ADD COLUMN media_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE clip_revisions ADD COLUMN media_size_bytes BIGINT NOT NULL DEFAULT 0;
UPDATE publications p SET destination_key=CASE WHEN a.platform='youtube' THEN COALESCE(a.remote_account_id,'') ELSE a.platform||':'||COALESCE(a.remote_account_id,'') END FROM accounts a WHERE a.id=p.account_id;
CREATE TABLE twitch_flow_destinations (
 id TEXT PRIMARY KEY, flow_id TEXT NOT NULL REFERENCES twitch_flows(id), provider TEXT NOT NULL CHECK(provider IN('youtube','tiktok')),
 account_id BIGINT NOT NULL REFERENCES accounts(id), remote_account_id TEXT NOT NULL, destination_key TEXT NOT NULL,
 config JSONB NOT NULL, enabled BOOLEAN NOT NULL DEFAULT TRUE, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(flow_id,provider)
);
INSERT INTO twitch_flow_destinations(id,flow_id,provider,account_id,remote_account_id,destination_key,config)
SELECT id||':youtube',id,'youtube',account_id,youtube_channel_id,youtube_channel_id,
 jsonb_build_object('id',id||':youtube','provider','youtube','account_id',account_id,'remote_account_id',youtube_channel_id,'enabled',true,'auto_publish',auto_publish,'timezone',config->>'timezone','daily_shorts',(config->>'daily_shorts')::int,'shorts_slots',config->'shorts_slots','schedule_horizon_hours',COALESCE((config->>'schedule_horizon_hours')::int,48),'visibility','public')
FROM twitch_flows WHERE account_id IS NOT NULL AND youtube_channel_id<>'';
CREATE TABLE twitch_output_destinations (
 clip_id TEXT NOT NULL REFERENCES clips(clip_id) ON DELETE CASCADE,
 destination_key TEXT NOT NULL, account_id BIGINT NOT NULL REFERENCES accounts(id), provider TEXT NOT NULL,
 snapshot JSONB NOT NULL, auto_eligible BOOLEAN NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(clip_id,destination_key)
);
INSERT INTO twitch_output_destinations(clip_id,destination_key,account_id,provider,snapshot,auto_eligible,created_at)
SELECT o.clip_id,o.youtube_channel_id,o.account_id,'youtube',COALESCE(d.config,'{}'),o.auto_eligible,o.created_at
FROM twitch_outputs o LEFT JOIN twitch_flow_destinations d ON d.flow_id=o.flow_id AND d.provider='youtube';
UPDATE publications p SET target_snapshot=t.snapshot FROM twitch_output_destinations t WHERE t.clip_id=p.clip_id AND t.account_id=p.account_id;
-- Existing generic job/editor creation paths also pin the destination.
-- +goose StatementBegin
CREATE FUNCTION pin_publication_destination() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.destination_key='' THEN
  SELECT CASE WHEN a.platform='youtube' THEN COALESCE(a.remote_account_id,'') ELSE a.platform||':'||COALESCE(a.remote_account_id,'') END INTO NEW.destination_key FROM accounts a WHERE a.id=NEW.account_id;
 END IF;
 IF NEW.target_snapshot='{}'::jsonb THEN
  SELECT COALESCE(d.snapshot,'{}'::jsonb) INTO NEW.target_snapshot FROM twitch_output_destinations d WHERE d.clip_id=NEW.clip_id AND d.account_id=NEW.account_id LIMIT 1;
  NEW.target_snapshot=COALESCE(NEW.target_snapshot,'{}'::jsonb);
 END IF;
 IF NEW.platform='tiktok' AND NEW.target_snapshot->>'visibility' IS NOT NULL THEN
  NEW.metadata=NEW.metadata||jsonb_build_object('visibility',NEW.target_snapshot->>'visibility');
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER publications_pin_destination BEFORE INSERT ON publications FOR EACH ROW EXECUTE FUNCTION pin_publication_destination();
-- +goose StatementBegin
CREATE FUNCTION preserve_tiktok_metadata() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.platform='tiktok' THEN
  NEW.metadata=NEW.metadata||jsonb_build_object('visibility',COALESCE(NEW.target_snapshot->>'visibility',OLD.metadata->>'visibility','private'));
  IF COALESCE((OLD.metadata->>'caption_manual')::bool,FALSE) AND NOT (NEW.metadata ? 'caption_manual') THEN
   NEW.metadata=NEW.metadata||jsonb_build_object('caption',OLD.metadata->>'caption','caption_manual',true);
  END IF;
  IF NOT (NEW.metadata ? 'caption') THEN
   NEW.metadata=NEW.metadata||jsonb_build_object('caption',trim(COALESCE(NEW.metadata->>'title','')||' '||COALESCE((SELECT string_agg('#'||trim(leading '#' FROM tag),' ' ORDER BY ord) FROM jsonb_array_elements_text(COALESCE(NEW.metadata->'hashtags','[]')) WITH ORDINALITY AS tags(tag,ord) WHERE lower(trim(leading '#' FROM tag))<>'shorts'),'')));
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER publications_preserve_tiktok_metadata BEFORE UPDATE OF metadata ON publications FOR EACH ROW EXECUTE FUNCTION preserve_tiktok_metadata();
CREATE TABLE tiktok_worker_operations (
 attempt_id TEXT PRIMARY KEY,publication_id BIGINT NOT NULL,lease_token TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'active' CHECK(state IN('active','released')),
 started_at TIMESTAMPTZ NOT NULL,released_at TIMESTAMPTZ
);
CREATE INDEX twitch_destination_accounts ON twitch_flow_destinations(destination_key,enabled);
ALTER TABLE publication_attempts ADD COLUMN dispatch_deadline TIMESTAMPTZ;
ALTER TABLE publication_attempts ADD COLUMN last_dispatched_at TIMESTAMPTZ;
ALTER TABLE publication_attempts ADD COLUMN execution_started_at TIMESTAMPTZ;
UPDATE publication_attempts a SET execution_started_at=a.created_at WHERE a.worker_id<>'';
-- Existing attempts and their execution leases remain untouched.

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'TikTok publication rollback must preserve destination snapshots and sessions'; END $$;
-- +goose StatementEnd
