-- +goose Up
-- Hot Twitch tables churn constantly; the default 20% scale factor lets the
-- visibility map go stale, so index-only scans fall back to heap reads.
ALTER TABLE twitch_flow_sources SET (autovacuum_vacuum_scale_factor=0.02, autovacuum_analyze_scale_factor=0.02);
ALTER TABLE twitch_imports SET (autovacuum_vacuum_scale_factor=0.02, autovacuum_analyze_scale_factor=0.02);
ALTER TABLE clips SET (autovacuum_vacuum_scale_factor=0.02, autovacuum_analyze_scale_factor=0.02);

-- +goose Down
ALTER TABLE twitch_flow_sources RESET (autovacuum_vacuum_scale_factor, autovacuum_analyze_scale_factor);
ALTER TABLE twitch_imports RESET (autovacuum_vacuum_scale_factor, autovacuum_analyze_scale_factor);
ALTER TABLE clips RESET (autovacuum_vacuum_scale_factor, autovacuum_analyze_scale_factor);
