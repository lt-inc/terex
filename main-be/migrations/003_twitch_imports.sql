-- +goose Up
ALTER TABLE jobs ALTER COLUMN youtube_url DROP NOT NULL;
ALTER TABLE jobs ADD COLUMN source_kind TEXT NOT NULL DEFAULT 'youtube';
ALTER TABLE jobs ADD COLUMN source_url TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN source_id TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN output_format TEXT NOT NULL DEFAULT 'legacy';

CREATE TABLE twitch_project_settings (
    id INTEGER PRIMARY KEY CHECK (id=1),
    settings JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO twitch_project_settings(id,settings) VALUES(1,'{}');

CREATE TABLE twitch_imports (
    id TEXT PRIMARY KEY,
    idempotency_key TEXT NOT NULL UNIQUE,
    twitch_clip_id TEXT NOT NULL UNIQUE,
    request JSONB NOT NULL,
    state TEXT NOT NULL DEFAULT 'upload_pending',
    original JSONB NOT NULL DEFAULT '{}',
    verified JSONB NOT NULL DEFAULT '{}',
    upload_expires_at TIMESTAMPTZ,
    job_id TEXT REFERENCES jobs(job_id) ON DELETE SET NULL,
    clip_id TEXT REFERENCES clips(clip_id) ON DELETE SET NULL,
    rejected BOOLEAN NOT NULL DEFAULT FALSE,
    error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX twitch_imports_pending ON twitch_imports(state,created_at) WHERE NOT rejected;
CREATE UNIQUE INDEX twitch_imports_verified_hash ON twitch_imports((verified->>'sha256')) WHERE clip_id IS NOT NULL AND COALESCE(verified->>'sha256','')<>'';

CREATE TABLE twitch_compilations (
    clip_id TEXT PRIMARY KEY REFERENCES clips(clip_id) ON DELETE CASCADE,
    local_date DATE,
    timezone TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(local_date,timezone)
);
CREATE TABLE twitch_compilation_items (
    compilation_revision_id BIGINT NOT NULL REFERENCES clip_revisions(id) ON DELETE CASCADE,
    source_revision_id BIGINT NOT NULL REFERENCES clip_revisions(id),
    source_clip_id TEXT NOT NULL REFERENCES clips(clip_id),
    ordinal INTEGER NOT NULL,
    PRIMARY KEY(compilation_revision_id,ordinal),
    UNIQUE(compilation_revision_id,source_clip_id)
);
CREATE INDEX twitch_compilation_sources ON twitch_compilation_items(source_clip_id);

-- +goose Down
DROP TABLE twitch_compilation_items,twitch_compilations,twitch_imports,twitch_project_settings;
ALTER TABLE jobs DROP COLUMN output_format,DROP COLUMN source_id,DROP COLUMN source_url,DROP COLUMN source_kind;
UPDATE jobs SET youtube_url='' WHERE youtube_url IS NULL;
ALTER TABLE jobs ALTER COLUMN youtube_url SET NOT NULL;
