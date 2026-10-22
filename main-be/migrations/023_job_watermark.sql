-- +goose Up
-- Watermark blur of a job (msg.WatermarkOptions as JSON); NULL = the watermark stays.
ALTER TABLE jobs ADD COLUMN watermark JSONB;

-- +goose Down
ALTER TABLE jobs DROP COLUMN watermark;
