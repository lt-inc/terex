"""add fail_place/status columns to jobs and sub_job

Revision ID: 010
Revises: 009
Create Date: 2026-04-07
"""
from alembic import op
import sqlalchemy as sa

revision = '010'
down_revision = '009'
branch_labels = None
depends_on = None


def upgrade():
    op.add_column("jobs", sa.Column("fail_place", sa.String(), nullable=False, server_default=""))
    op.add_column("sub_job", sa.Column("fail_place", sa.String(), nullable=False, server_default=""))
    op.add_column("sub_job", sa.Column("status", sa.String(), nullable=False, server_default="pending"))


def downgrade():
    op.drop_column("sub_job", "status")
    op.drop_column("sub_job", "fail_place")
    op.drop_column("jobs", "fail_place")
