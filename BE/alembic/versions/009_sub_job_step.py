"""add publish_step to sub_job

Revision ID: 009
Revises: 008
Create Date: 2026-04-07
"""
from alembic import op
import sqlalchemy as sa

revision = '009'
down_revision = '008'
branch_labels = None
depends_on = None


def upgrade():
    op.add_column('sub_job', sa.Column('publish_step', sa.Integer(), nullable=False, server_default='0'))


def downgrade():
    op.drop_column('sub_job', 'publish_step')
