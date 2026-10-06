-- +goose Up
-- Additive migration: old jobs, revisions, publications and manifests stay intact.
CREATE TABLE twitch_flows (
 id TEXT PRIMARY KEY, config JSONB NOT NULL, version INTEGER NOT NULL DEFAULT 1,
 account_id BIGINT REFERENCES accounts(id), youtube_channel_id TEXT NOT NULL DEFAULT '',
 enabled BOOLEAN NOT NULL DEFAULT FALSE, auto_publish BOOLEAN NOT NULL DEFAULT FALSE,
 auto_enabled_at TIMESTAMPTZ, work_started_at TIMESTAMPTZ, archived BOOLEAN NOT NULL DEFAULT FALSE,
 last_checked_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO twitch_flows(id,config,account_id,youtube_channel_id,enabled)
SELECT 'default',settings || jsonb_build_object('id','default','name','Основной поток','legacy_length',true,
 'source_mode','channels','shorts_enabled',true,'compilations_enabled',true,
 'account_id',COALESCE((settings->>'target_account_id')::bigint,0)),
 a.id,COALESCE(a.remote_account_id,''),COALESCE((settings->>'enabled')::boolean,false)
FROM twitch_project_settings s LEFT JOIN accounts a ON a.id=COALESCE((settings->>'target_account_id')::bigint,0);

CREATE TABLE twitch_media (
 id TEXT PRIMARY KEY, sha256 TEXT NOT NULL UNIQUE, verified JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE twitch_imports ADD COLUMN media_id TEXT REFERENCES twitch_media(id);
ALTER TABLE twitch_imports ADD COLUMN media_state TEXT NOT NULL DEFAULT 'pending';
ALTER TABLE twitch_imports ADD COLUMN upload_generation BIGINT NOT NULL DEFAULT 0;
ALTER TABLE twitch_imports ADD COLUMN upload_claim_until TIMESTAMPTZ;
ALTER TABLE twitch_imports ADD COLUMN download_reserved_at TIMESTAMPTZ;
DROP INDEX twitch_imports_verified_hash;
INSERT INTO twitch_media(id,sha256,verified,created_at)
SELECT DISTINCT ON(verified->>'sha256') verified->>'sha256',verified->>'sha256',verified,created_at
FROM twitch_imports WHERE COALESCE(verified->>'sha256','')<>'' AND COALESCE(verified->'original'->>'object','')<>''
ORDER BY verified->>'sha256',created_at;
UPDATE twitch_imports SET media_id=verified->>'sha256',media_state='ready' WHERE verified->>'sha256' IN(SELECT id FROM twitch_media);
UPDATE twitch_imports SET download_reserved_at=created_at WHERE state<>'discovered';
CREATE TABLE twitch_download_reservations (
 import_id TEXT NOT NULL REFERENCES twitch_imports(id), generation BIGINT NOT NULL,
 broadcaster_id TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL, PRIMARY KEY(import_id,generation)
);
INSERT INTO twitch_download_reservations SELECT id,0,request->'source'->>'broadcaster_id',created_at FROM twitch_imports;
CREATE TABLE twitch_import_keys (
 idempotency_key TEXT PRIMARY KEY, import_id TEXT NOT NULL REFERENCES twitch_imports(id)
);
INSERT INTO twitch_import_keys SELECT idempotency_key,id FROM twitch_imports;

CREATE TABLE twitch_flow_sources (
 id TEXT PRIMARY KEY, flow_id TEXT NOT NULL REFERENCES twitch_flows(id),
 import_id TEXT NOT NULL REFERENCES twitch_imports(id), config JSONB NOT NULL,
 flow_version INTEGER NOT NULL, excluded BOOLEAN NOT NULL DEFAULT FALSE,
 reserved_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(flow_id,import_id)
);
CREATE INDEX twitch_flow_sources_import ON twitch_flow_sources(import_id);
INSERT INTO twitch_flow_sources(id,flow_id,import_id,config,flow_version,excluded,created_at)
SELECT 'legacy-'||i.id,'default',i.id,f.config,1,i.rejected,i.created_at FROM twitch_imports i CROSS JOIN twitch_flows f WHERE f.id='default';
UPDATE twitch_flow_sources SET reserved_at=created_at;
UPDATE twitch_flows SET config=config||jsonb_build_object('youtube_channel_id',youtube_channel_id,'account_id',COALESCE(account_id,0));
UPDATE twitch_flow_sources fs SET config=f.config FROM twitch_flows f WHERE f.id=fs.flow_id;
-- Existing reservations keep their actual destination, including imports made
-- before the singleton target was changed.
UPDATE twitch_flow_sources fs SET config=fs.config||jsonb_build_object('account_id',a.id,'youtube_channel_id',COALESCE(a.remote_account_id,''))
FROM twitch_imports i,twitch_flows f,accounts a
WHERE i.id=fs.import_id AND f.id=fs.flow_id AND a.id=COALESCE(NULLIF((i.request->>'account_id')::bigint,0),f.account_id);

CREATE TABLE twitch_outputs (
 clip_id TEXT PRIMARY KEY REFERENCES clips(clip_id) ON DELETE CASCADE,
 flow_id TEXT NOT NULL REFERENCES twitch_flows(id), source_id TEXT REFERENCES twitch_imports(id),
 account_id BIGINT NOT NULL REFERENCES accounts(id), youtube_channel_id TEXT NOT NULL,
 format TEXT NOT NULL CHECK(format IN('short','compilation')), config JSONB NOT NULL,
 initial_revision_id BIGINT REFERENCES clip_revisions(id), auto_eligible BOOLEAN NOT NULL DEFAULT FALSE,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX twitch_flow_short ON twitch_outputs(flow_id,source_id) WHERE format='short';
INSERT INTO twitch_outputs(clip_id,flow_id,source_id,account_id,youtube_channel_id,format,config,initial_revision_id,created_at)
SELECT c.clip_id,'default',i.id,a.id,COALESCE(a.remote_account_id,''),'short',f.config,c.latest_revision_id,i.created_at
FROM twitch_imports i JOIN clips c ON c.clip_id=i.clip_id JOIN twitch_flows f ON f.id='default'
JOIN accounts a ON a.id=COALESCE((i.request->>'account_id')::bigint,f.account_id);
INSERT INTO twitch_outputs(clip_id,flow_id,account_id,youtube_channel_id,format,config,initial_revision_id,created_at)
SELECT c.clip_id,'default',a.id,COALESCE(a.remote_account_id,''),'compilation',f.config,c.latest_revision_id,t.created_at
FROM twitch_compilations t JOIN clips c ON c.clip_id=t.clip_id JOIN sub_jobs s ON s.job_id=c.job_id
JOIN sub_job_targets st ON st.sub_job_id=s.sub_job_id JOIN accounts a ON a.id=st.account_id
CROSS JOIN twitch_flows f WHERE f.id='default' ON CONFLICT DO NOTHING;
UPDATE twitch_outputs SET config=config||jsonb_build_object('account_id',account_id,'youtube_channel_id',youtube_channel_id);
UPDATE twitch_flows SET work_started_at=(SELECT min(created_at) FROM twitch_imports) WHERE id='default' AND EXISTS(SELECT 1 FROM twitch_imports);

CREATE TABLE twitch_source_uses (
 youtube_channel_id TEXT NOT NULL, format TEXT NOT NULL, media_id TEXT NOT NULL REFERENCES twitch_media(id),
 clip_id TEXT REFERENCES clips(clip_id) ON DELETE SET NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(youtube_channel_id,format,media_id)
);
INSERT INTO twitch_source_uses(youtube_channel_id,format,media_id,clip_id)
SELECT o.youtube_channel_id,'short',i.media_id,o.clip_id FROM twitch_outputs o JOIN twitch_imports i ON i.id=o.source_id
WHERE i.media_id IS NOT NULL AND o.youtube_channel_id<>'' ON CONFLICT DO NOTHING;
INSERT INTO twitch_source_uses(youtube_channel_id,format,media_id,clip_id)
SELECT o.youtube_channel_id,'compilation',i.media_id,o.clip_id FROM twitch_outputs o JOIN clips c ON c.clip_id=o.clip_id
JOIN clip_revisions r ON r.clip_id=c.clip_id JOIN twitch_compilation_items ci ON ci.compilation_revision_id=r.id JOIN twitch_imports i ON i.clip_id=ci.source_clip_id
WHERE i.media_id IS NOT NULL AND o.youtube_channel_id<>'' ON CONFLICT DO NOTHING;

CREATE TABLE twitch_channel_limits (
 youtube_channel_id TEXT PRIMARY KEY, timezone TEXT NOT NULL,
 daily_shorts INTEGER NOT NULL, daily_compilations INTEGER NOT NULL
);
INSERT INTO twitch_channel_limits SELECT DISTINCT a.remote_account_id,COALESCE(s.settings->>'timezone','Asia/Tomsk'),
 COALESCE((s.settings->>'daily_shorts')::integer,5),COALESCE((s.settings->>'daily_compilations')::integer,1)
FROM accounts a CROSS JOIN twitch_project_settings s WHERE a.platform='youtube' AND COALESCE(a.remote_account_id,'')<>'' ON CONFLICT DO NOTHING;
ALTER TABLE twitch_compilations ADD COLUMN flow_id TEXT REFERENCES twitch_flows(id);
UPDATE twitch_compilations SET flow_id='default';
ALTER TABLE twitch_compilations DROP CONSTRAINT twitch_compilations_local_date_timezone_key;
CREATE UNIQUE INDEX twitch_flow_daily_compilation ON twitch_compilations(flow_id,local_date,timezone);
ALTER TABLE twitch_compilation_items ALTER COLUMN source_revision_id DROP NOT NULL;
ALTER TABLE twitch_compilation_items ALTER COLUMN source_clip_id DROP NOT NULL;
ALTER TABLE twitch_compilation_items ADD COLUMN source_id TEXT REFERENCES twitch_imports(id);
ALTER TABLE twitch_compilation_items ADD COLUMN media_id TEXT REFERENCES twitch_media(id);
UPDATE twitch_compilation_items ci SET source_id=i.id,media_id=i.media_id FROM twitch_imports i WHERE i.clip_id=ci.source_clip_id;
ALTER TABLE publications ADD COLUMN auto_flow_id TEXT REFERENCES twitch_flows(id);
ALTER TABLE publications ADD COLUMN auto_hold BOOLEAN NOT NULL DEFAULT FALSE;
CREATE TABLE twitch_publication_slots (
 youtube_channel_id TEXT NOT NULL,
 flow_id TEXT NOT NULL REFERENCES twitch_flows(id), local_date DATE NOT NULL, format TEXT NOT NULL,
 slot TEXT NOT NULL, publication_id BIGINT NOT NULL UNIQUE REFERENCES publications(id) ON DELETE CASCADE,
 PRIMARY KEY(youtube_channel_id,local_date,format,slot)
);

-- +goose Down
-- Deliberately refuse a destructive rollback once flows have produced work.
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Twitch flows migration is additive and requires a reviewed rollback'; END $$;
-- +goose StatementEnd
