-- +goose Up
CREATE TABLE twitch_download_grants (
 clip_id TEXT PRIMARY KEY,
 import_id TEXT NOT NULL REFERENCES twitch_imports(id) ON DELETE CASCADE,
 flow_id TEXT NOT NULL REFERENCES twitch_flows(id) ON DELETE CASCADE,
 primary_format TEXT NOT NULL CHECK(primary_format IN('short','compilation')),
 sequence BIGSERIAL UNIQUE,
 epoch BIGINT NOT NULL DEFAULT 0,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 expires_at TIMESTAMPTZ NOT NULL,
 state TEXT NOT NULL DEFAULT 'active' CHECK(state IN('active','consumed','released'))
);
CREATE INDEX twitch_download_grants_fairness ON twitch_download_grants(flow_id,sequence DESC);
CREATE INDEX twitch_download_grants_daily ON twitch_download_grants(created_at,primary_format);
UPDATE twitch_project_settings SET settings=jsonb_build_object('compilation_budget_percent',70,'admission_mode','shadow')||settings;

-- +goose Down
DROP TABLE twitch_download_grants;
UPDATE twitch_project_settings SET settings=settings-'compilation_budget_percent'-'admission_mode';
