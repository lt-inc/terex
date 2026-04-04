"""add min_video_duration to jobs

Revision ID: 005
Revises: 004
Create Date: 2026-04-05
"""
from alembic import op
import sqlalchemy as sa

revision = '005'
down_revision = '004'
branch_labels = None
depends_on = None


def upgrade():
    op.add_column('jobs', sa.Column('min_video_duration', sa.Integer(), nullable=False, server_default='60'))


def downgrade():
    op.drop_column('jobs', 'min_video_duration')
