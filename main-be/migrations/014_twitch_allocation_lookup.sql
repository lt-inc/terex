-- +goose Up
-- Canonical selection starts from the small verified-media set, rather than
-- reading every discovery of every stream on each admission/diagnostic tick.
CREATE INDEX twitch_imports_ready_selection ON twitch_imports(id,media_id)
 WHERE media_state='ready' AND NOT rejected;

CREATE INDEX twitch_imports_admission_category ON twitch_imports(
 (request->'source'->>'game_id'),(request->'source'->>'language'),id)
 WHERE NOT rejected AND media_state NOT IN('ready','expired')
 AND state IN('discovered','upload_failed');

-- +goose Down
DROP INDEX twitch_imports_admission_category;
DROP INDEX twitch_imports_ready_selection;
