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

-- PostgreSQL severely underestimates the old JSON category/language lookup and
-- otherwise keeps choosing it, loading thousands of low-view requests. The new
-- category index covers its predicates as well as the admission view threshold.
DROP INDEX CONCURRENTLY IF EXISTS twitch_imports_admission_category;

-- +goose Down
CREATE INDEX CONCURRENTLY IF NOT EXISTS twitch_imports_admission_category
 ON twitch_imports((request->'source'->>'game_id'),
  (request->'source'->>'language'),id)
 WHERE NOT rejected AND media_state NOT IN('ready','expired')
 AND state IN('discovered','upload_failed');
DROP INDEX CONCURRENTLY IF EXISTS twitch_imports_admission_broadcaster_views;
DROP INDEX CONCURRENTLY IF EXISTS twitch_imports_admission_category_views;
