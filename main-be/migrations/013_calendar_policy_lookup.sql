-- +goose Up
-- Historical compilations may still reference an import by its source clip ID.
CREATE INDEX twitch_imports_source_clip ON twitch_imports(clip_id) WHERE clip_id IS NOT NULL;
-- The collector refreshes exclusions frequently; don't scan every candidate.
CREATE INDEX twitch_flow_sources_excluded ON twitch_flow_sources(flow_id,import_id) WHERE excluded;

-- +goose Down
DROP INDEX twitch_imports_source_clip;
DROP INDEX twitch_flow_sources_excluded;
