-- +goose Up
-- Branded layout of a job (msg.BrandOptions as JSON); NULL = an ordinary job.
ALTER TABLE jobs ADD COLUMN brand JSONB;

-- +goose Down
ALTER TABLE jobs DROP COLUMN brand;
