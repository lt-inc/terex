"""Add published_at to out

Revision ID: 011
Revises: 010
Create Date: 2026-04-08
"""
from alembic import op
import sqlalchemy as sa

revision = '011'
down_revision = '010'
branch_labels = None
depends_on = None


def upgrade():
    op.add_column('out', sa.Column('published_at', sa.TIMESTAMP(timezone=True), nullable=True))


def downgrade():
    op.drop_column('out', 'published_at')
