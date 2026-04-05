"""add is_add_banner to sub_job

Revision ID: 006
Revises: 005
Create Date: 2026-04-05
"""
from alembic import op
import sqlalchemy as sa

revision = '006'
down_revision = '005'
branch_labels = None
depends_on = None


def upgrade():
    op.add_column('sub_job', sa.Column('is_add_banner', sa.Boolean(), nullable=False, server_default='false'))


def downgrade():
    op.drop_column('sub_job', 'is_add_banner')
