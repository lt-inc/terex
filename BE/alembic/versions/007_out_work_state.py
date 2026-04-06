"""add work_state to out

Revision ID: 007
Revises: 006
Create Date: 2026-04-06
"""
from alembic import op
import sqlalchemy as sa

revision = '007'
down_revision = '006'
branch_labels = None
depends_on = None


def upgrade():
    op.add_column('out', sa.Column('work_state', sa.String(), nullable=True))


def downgrade():
    op.drop_column('out', 'work_state')
