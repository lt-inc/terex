"""full schema

Revision ID: 001
Revises:
Create Date: 2026-04-08
"""
from alembic import op

revision = '001'
down_revision = None
branch_labels = None
depends_on = None


def upgrade():
    op.execute("""
        CREATE TABLE IF NOT EXISTS jobs (
            job_id          VARCHAR PRIMARY KEY,
            youtube_url     VARCHAR NOT NULL,
            is_vertical     BOOLEAN NOT NULL DEFAULT false,
            is_subtitles    BOOLEAN NOT NULL DEFAULT false,
            is_slice        BOOLEAN NOT NULL DEFAULT false,
            is_background   BOOLEAN NOT NULL DEFAULT false,
            is_circle       BOOLEAN NOT NULL DEFAULT false,
            min_video_duration INTEGER NOT NULL DEFAULT 60,
            background_id   INTEGER,
            trace           VARCHAR,
            status          VARCHAR NOT NULL DEFAULT 'pending',
            fail_place      VARCHAR NOT NULL DEFAULT ''
        )
    """)

    op.execute("""
        CREATE TABLE IF NOT EXISTS banners (
            id        SERIAL PRIMARY KEY,
            name      VARCHAR UNIQUE NOT NULL,
            note      TEXT,
            bucket    VARCHAR,
            file_name TEXT
        )
    """)

    op.execute("""
        CREATE TABLE IF NOT EXISTS backgrounds (
            id        SERIAL PRIMARY KEY,
            name      VARCHAR UNIQUE NOT NULL,
            note      TEXT,
            bucket    VARCHAR,
            file_name TEXT
        )
    """)

    op.execute("""
        CREATE TABLE IF NOT EXISTS accounts (
            id        SERIAL PRIMARY KEY,
            platform  VARCHAR NOT NULL,
            name      VARCHAR NOT NULL,
            url       VARCHAR,
            note      TEXT,
            bucket    VARCHAR,
            file_name TEXT,
            UNIQUE (platform, name)
        )
    """)

    op.execute("""
        CREATE TABLE IF NOT EXISTS sub_job (
            sub_job_id       VARCHAR PRIMARY KEY,
            job_id           VARCHAR NOT NULL REFERENCES jobs(job_id) ON DELETE CASCADE,
            is_add_banner    BOOLEAN NOT NULL DEFAULT false,
            banner_id        INTEGER REFERENCES banners(id) ON DELETE SET NULL,
            account_ids      INTEGER[],
            publish_step     INTEGER NOT NULL DEFAULT 0,
            publish_datetime TIMESTAMPTZ,
            status           VARCHAR NOT NULL DEFAULT 'pending',
            fail_place       VARCHAR NOT NULL DEFAULT ''
        )
    """)
    op.execute("CREATE INDEX IF NOT EXISTS ix_sub_job_job_id ON sub_job (job_id)")

    op.execute("""
        CREATE TABLE IF NOT EXISTS out (
            id           SERIAL PRIMARY KEY,
            job_id       VARCHAR NOT NULL REFERENCES jobs(job_id) ON DELETE CASCADE,
            sub_job_id   VARCHAR REFERENCES sub_job(sub_job_id) ON DELETE SET NULL,
            video_name   VARCHAR,
            name         VARCHAR,
            description  TEXT,
            hashtag      VARCHAR,
            upload_id    VARCHAR,
            publish_date TIMESTAMPTZ,
            published_at TIMESTAMPTZ,
            status       VARCHAR,
            work_state   VARCHAR,
            account_id   INTEGER,
            platform     VARCHAR
        )
    """)
    op.execute("CREATE INDEX IF NOT EXISTS ix_out_job_id ON out (job_id)")
    op.execute("CREATE INDEX IF NOT EXISTS ix_out_sub_job_id ON out (sub_job_id)")
    op.execute("""
        DO $$ BEGIN
            ALTER TABLE out ADD CONSTRAINT uq_out_job_subjob_video_account
                UNIQUE (job_id, sub_job_id, video_name, account_id);
        EXCEPTION WHEN duplicate_table THEN NULL;
        END $$
    """)

    op.execute("""
        CREATE TABLE IF NOT EXISTS jobs_metadata (
            job_id              VARCHAR PRIMARY KEY REFERENCES jobs(job_id) ON DELETE CASCADE,
            create_datetime     TIMESTAMPTZ NOT NULL DEFAULT now(),
            last_change_datetime TIMESTAMPTZ NOT NULL DEFAULT now()
        )
    """)

    # Add any columns that may be missing on existing DBs
    for col_sql in [
        "ALTER TABLE jobs      ADD COLUMN IF NOT EXISTS min_video_duration INTEGER NOT NULL DEFAULT 60",
        "ALTER TABLE jobs      ADD COLUMN IF NOT EXISTS background_id INTEGER",
        "ALTER TABLE jobs      ADD COLUMN IF NOT EXISTS fail_place VARCHAR NOT NULL DEFAULT ''",
        "ALTER TABLE sub_job   ADD COLUMN IF NOT EXISTS is_add_banner BOOLEAN NOT NULL DEFAULT false",
        "ALTER TABLE sub_job   ADD COLUMN IF NOT EXISTS publish_step INTEGER NOT NULL DEFAULT 0",
        "ALTER TABLE sub_job   ADD COLUMN IF NOT EXISTS status VARCHAR NOT NULL DEFAULT 'pending'",
        "ALTER TABLE sub_job   ADD COLUMN IF NOT EXISTS fail_place VARCHAR NOT NULL DEFAULT ''",
        "ALTER TABLE out       ADD COLUMN IF NOT EXISTS status VARCHAR",
        "ALTER TABLE out       ADD COLUMN IF NOT EXISTS work_state VARCHAR",
        "ALTER TABLE out       ADD COLUMN IF NOT EXISTS account_id INTEGER",
        "ALTER TABLE out       ADD COLUMN IF NOT EXISTS platform VARCHAR",
        "ALTER TABLE out       ADD COLUMN IF NOT EXISTS published_at TIMESTAMPTZ",
        "ALTER TABLE out       ADD COLUMN IF NOT EXISTS hashtag VARCHAR",
        "ALTER TABLE banners   ADD COLUMN IF NOT EXISTS bucket VARCHAR",
        "ALTER TABLE banners   ADD COLUMN IF NOT EXISTS file_name TEXT",
        "ALTER TABLE backgrounds ADD COLUMN IF NOT EXISTS bucket VARCHAR",
        "ALTER TABLE backgrounds ADD COLUMN IF NOT EXISTS file_name TEXT",
        "ALTER TABLE accounts  ADD COLUMN IF NOT EXISTS bucket VARCHAR",
        "ALTER TABLE accounts  ADD COLUMN IF NOT EXISTS file_name TEXT",
        "ALTER TABLE sub_job   DROP COLUMN IF EXISTS platform",
    ]:
        op.execute(col_sql)


def downgrade():
    pass
