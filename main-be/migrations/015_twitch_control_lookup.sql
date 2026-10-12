-- +goose Up
-- Frequent control polling should read only the exceptional rows, including
-- the common empty result, rather than scanning the complete discovery heap.
CREATE INDEX twitch_imports_blocked_sources ON twitch_imports(twitch_clip_id)
 WHERE rejected OR media_state='expired';
-- The canonical pressure predicate starts with this condition. Discoveries
-- without a physical reservation must not dominate its scans either.
CREATE INDEX twitch_imports_physical_pending ON twitch_imports(id,media_id)
 WHERE NOT rejected AND state NOT IN('discovered','failed');

-- +goose Down
DROP INDEX twitch_imports_blocked_sources;
DROP INDEX twitch_imports_physical_pending;
