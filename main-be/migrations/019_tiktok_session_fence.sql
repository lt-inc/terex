-- +goose Up
-- Keep the claimed session separate from provider-controlled checkpoints.
-- Existing uploads with no saved session snapshot are fenced; they can be
-- reconciled with the renewed session, never blindly submitted again.
ALTER TABLE publication_attempts ADD COLUMN claimed_session_version BIGINT NOT NULL DEFAULT 0;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Preserve claimed TikTok session fences when rolling back application images'; END $$;
-- +goose StatementEnd
