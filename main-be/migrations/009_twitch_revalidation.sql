-- +goose Up
ALTER TABLE twitch_imports ADD COLUMN revalidation jsonb;

-- +goose Down
ALTER TABLE twitch_imports DROP COLUMN revalidation;
