"""initial schema

Revision ID: 001
Revises:
Create Date: 2026-04-03

"""
from typing import Sequence, Union

from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects import postgresql

revision: str = "001"
down_revision: Union[str, None] = None
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    op.create_table(
        "jobs",
        sa.Column("job_id", sa.String(), primary_key=True),
        sa.Column("youtube_url", sa.String(), nullable=False),
        sa.Column("is_vertical", sa.Boolean(), nullable=False, server_default="false"),
        sa.Column("is_subtitles", sa.Boolean(), nullable=False, server_default="false"),
        sa.Column("is_slice", sa.Boolean(), nullable=False, server_default="false"),
        sa.Column("is_background", sa.Boolean(), nullable=False, server_default="false"),
        sa.Column("is_circle", sa.Boolean(), nullable=False, server_default="false"),
        sa.Column("trace", sa.String(), nullable=True),
        sa.Column("status", sa.String(), nullable=False, server_default="pending"),
    )

    op.create_table(
        "sub_job",
        sa.Column("sub_job_id", sa.String(), primary_key=True),
        sa.Column("job_id", sa.String(), sa.ForeignKey("jobs.job_id", ondelete="CASCADE"), nullable=False),
        sa.Column("banner_id", sa.Integer(), nullable=True),
        sa.Column("account_ids", postgresql.ARRAY(sa.Integer()), nullable=True),
        sa.Column("platform", sa.String(), nullable=False),
        sa.Column("publish_datetime", sa.TIMESTAMP(timezone=True), nullable=True),
    )
    op.create_index("ix_sub_job_job_id", "sub_job", ["job_id"])

    op.create_table(
        "out",
        sa.Column("id", sa.Integer(), primary_key=True, autoincrement=True),
        sa.Column("job_id", sa.String(), sa.ForeignKey("jobs.job_id", ondelete="CASCADE"), nullable=False),
        sa.Column("sub_job_id", sa.String(), sa.ForeignKey("sub_job.sub_job_id", ondelete="SET NULL"), nullable=True),
        sa.Column("video_name", sa.String(), nullable=True),
        sa.Column("name", sa.String(), nullable=True),
        sa.Column("description", sa.Text(), nullable=True),
        sa.Column("upload_id", sa.String(), nullable=True),
        sa.Column("publish_date", sa.TIMESTAMP(timezone=True), nullable=True),
    )
    op.create_index("ix_out_job_id", "out", ["job_id"])
    op.create_index("ix_out_sub_job_id", "out", ["sub_job_id"])

    op.create_table(
        "jobs_metadata",
        sa.Column("job_id", sa.String(), sa.ForeignKey("jobs.job_id", ondelete="CASCADE"), primary_key=True),
        sa.Column("create_datetime", sa.TIMESTAMP(timezone=True), nullable=False, server_default=sa.func.now()),
        sa.Column("last_change_datetime", sa.TIMESTAMP(timezone=True), nullable=False, server_default=sa.func.now()),
    )


def downgrade() -> None:
    op.drop_table("jobs_metadata")
    op.drop_index("ix_out_sub_job_id", "out")
    op.drop_index("ix_out_job_id", "out")
    op.drop_table("out")
    op.drop_index("ix_sub_job_job_id", "sub_job")
    op.drop_table("sub_job")
    op.drop_table("jobs")
