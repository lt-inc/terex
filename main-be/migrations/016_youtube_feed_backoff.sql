-- +goose Up
ALTER TABLE channels
    ADD COLUMN feed_error_code text NOT NULL DEFAULT '',
    ADD COLUMN last_feed_error text NOT NULL DEFAULT '',
    ADD COLUMN feed_failure_count integer NOT NULL DEFAULT 0,
    ADD COLUMN next_feed_check_at timestamptz;

-- +goose Down
ALTER TABLE channels
    DROP COLUMN next_feed_check_at,
    DROP COLUMN feed_failure_count,
    DROP COLUMN last_feed_error,
    DROP COLUMN feed_error_code;
