import os
from sqlalchemy import (
    create_engine, Table, Column,
    String, Boolean, Integer, Text, TIMESTAMP, MetaData, UniqueConstraint,
)
from sqlalchemy.dialects.postgresql import ARRAY
from sqlalchemy.orm import sessionmaker
from sqlalchemy.sql import func

DATABASE_URL = os.environ.get("DATABASE_URL")

engine = create_engine(DATABASE_URL) if DATABASE_URL else None
SessionLocal = sessionmaker(bind=engine) if engine else None
metadata = MetaData()

jobs_table = Table(
    "jobs", metadata,
    Column("job_id", String, primary_key=True),
    Column("youtube_url", String, nullable=False),
    Column("is_vertical", Boolean, nullable=False),
    Column("is_subtitles", Boolean, nullable=False),
    Column("is_slice", Boolean, nullable=False),
    Column("is_background", Boolean, nullable=False),
    Column("min_video_duration", Integer, nullable=False, server_default="60"),
    Column("background_id", Integer, nullable=True),
    Column("is_circle", Boolean, nullable=False),
    Column("trace", String, nullable=True),
    Column("status", String, nullable=False),
    Column("fail_place", String, nullable=False, server_default=""),
)

sub_job_table = Table(
    "sub_job", metadata,
    Column("sub_job_id", String, primary_key=True),
    Column("job_id", String, nullable=False),
    Column("is_add_banner", Boolean, nullable=False, server_default="false"),
    Column("banner_id", Integer, nullable=True),
    Column("account_ids", ARRAY(Integer), nullable=True),
    Column("publish_step", Integer, nullable=False, server_default="0"),
    Column("publish_datetime", TIMESTAMP(timezone=True), nullable=True),
    Column("status", String, nullable=False, server_default="pending"),
    Column("fail_place", String, nullable=False, server_default=""),
)

jobs_metadata_table = Table(
    "jobs_metadata", metadata,
    Column("job_id", String, primary_key=True),
    Column("create_datetime", TIMESTAMP(timezone=True), server_default=func.now()),
    Column("last_change_datetime", TIMESTAMP(timezone=True), server_default=func.now()),
)

accounts_table = Table(
    "accounts", metadata,
    Column("id", Integer, primary_key=True, autoincrement=True),
    Column("platform", String, nullable=False),
    Column("name", String, nullable=False),
    Column("url", String, nullable=True),
    Column("note", Text, nullable=True),
    Column("bucket", String, nullable=True),
    Column("file_name", String, nullable=True),
)

banners_table = Table(
    "banners", metadata,
    Column("id", Integer, primary_key=True, autoincrement=True),
    Column("name", String, nullable=False),
    Column("note", Text, nullable=True),
    Column("bucket", String, nullable=True),
    Column("file_name", String, nullable=True),
)

backgrounds_table = Table(
    "backgrounds", metadata,
    Column("id", Integer, primary_key=True, autoincrement=True),
    Column("name", String, nullable=False),
    Column("note", Text, nullable=True),
    Column("bucket", String, nullable=True),
    Column("file_name", String, nullable=True),
)

out_table = Table(
    "out", metadata,
    Column("id", Integer, primary_key=True, autoincrement=True),
    Column("job_id", String, nullable=False),
    Column("sub_job_id", String, nullable=True),
    Column("video_name", String, nullable=True),
    Column("name", String, nullable=True),
    Column("description", Text, nullable=True),
    Column("hashtag", String, nullable=True),
    Column("upload_id", String, nullable=True),
    Column("publish_date", TIMESTAMP(timezone=True), nullable=True),
    Column("published_at", TIMESTAMP(timezone=True), nullable=True),
    Column("status", String, nullable=True),
    Column("work_state", String, nullable=True),
    Column("account_id", Integer, nullable=True),
    Column("platform", String, nullable=True),
    UniqueConstraint("job_id", "sub_job_id", "video_name", "account_id", name="uq_out_job_subjob_video_account"),
)


def get_db():
    if SessionLocal is None:
        yield None
        return
    db = SessionLocal()
    try:
        yield db
    finally:
        db.close()
