"""add background_id to jobs

Revision ID: 004
Revises: 003
Create Date: 2026-04-04
"""
from alembic import op
import sqlalchemy as sa

revision = '004'
down_revision = '003'
branch_labels = None
depends_on = None


def upgrade():
    op.add_column('jobs', sa.Column('background_id', sa.Integer(), nullable=True))
    op.create_foreign_key('fk_jobs_background_id', 'jobs', 'backgrounds', ['background_id'], ['id'], ondelete='SET NULL')


def downgrade():
    op.drop_constraint('fk_jobs_background_id', 'jobs', type_='foreignkey')
    op.drop_column('jobs', 'background_id')
