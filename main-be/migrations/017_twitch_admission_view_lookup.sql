-- +goose NO TRANSACTION
-- +goose Up
-- The category-only index still reads old, low-view discoveries from the heap.
-- Admission already filters by min_views; evaluate that filter inside the index
-- before loading the JSON request. Keep the ranking and admission policy intact.
-- Build without blocking discovery/import writes on an existing installation.
CREATE INDEX CONCURRENTLY IF NOT EXISTS twitch_imports_admission_category_views
 ON twitch_imports(
  (request->'source'->>'game_id'),
  (COALESCE((request->'source'->>'view_count')::bigint,0)) DESC,
  (request->'source'->>'language'),id)
 WHERE NOT rejected AND media_state NOT IN('ready','expired')
 AND state IN('discovered','upload_failed');

CREATE INDEX CONCURRENTLY IF NOT EXISTS twitch_imports_admission_broadcaster_views
 ON twitch_imports(
  (request->'source'->>'broadcaster_id'),
  (COALESCE((request->'source'->>'view_count')::bigint,0)) DESC,id)
 WHERE NOT rejected AND media_state NOT IN('ready','expired')
 AND state IN('discovered','upload_failed');

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS twitch_imports_admission_broadcaster_views;
DROP INDEX CONCURRENTLY IF EXISTS twitch_imports_admission_category_views;
