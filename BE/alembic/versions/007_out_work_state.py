"""add status to out

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
    conn = op.get_bind()
    result = conn.execute(sa.text(
        "SELECT 1 FROM information_schema.columns "
        "WHERE table_name='out' AND column_name='status'"
    ))
    if not result.fetchone():
        op.add_column('out', sa.Column('status', sa.String(), nullable=True))


def downgrade():
    op.drop_column('out', 'status')
