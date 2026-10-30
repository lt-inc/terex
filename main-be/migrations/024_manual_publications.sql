-- +goose Up
-- Independent manual targets and detached media snapshots. The existing global
-- clip/account uniqueness prevents automatic schedules duplicating manual work.
ALTER TABLE clip_revisions ADD COLUMN publication_only BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE clip_revisions DROP CONSTRAINT clip_revisions_clip_id_revision_no_key;
CREATE UNIQUE INDEX clip_editor_revision_unique ON clip_revisions(clip_id,revision_no) WHERE NOT publication_only;

ALTER TABLE publications ADD COLUMN manual BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE publications ADD COLUMN manual_source_revision_id BIGINT REFERENCES clip_revisions(id);
ALTER TABLE publications ADD COLUMN manual_media_kind TEXT;

CREATE TABLE manual_publication_requests (
    request_id UUID PRIMARY KEY,
    clip_id TEXT NOT NULL REFERENCES clips(clip_id) ON DELETE CASCADE,
    fingerprint TEXT NOT NULL,
    publication_id BIGINT NOT NULL REFERENCES publications(id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE manual_publication_requests;
DELETE FROM publications WHERE manual;
DELETE FROM clip_revisions WHERE publication_only;
ALTER TABLE publications DROP COLUMN manual, DROP COLUMN manual_source_revision_id, DROP COLUMN manual_media_kind;
DROP INDEX clip_editor_revision_unique;
ALTER TABLE clip_revisions DROP COLUMN publication_only;
ALTER TABLE clip_revisions ADD UNIQUE(clip_id,revision_no);
