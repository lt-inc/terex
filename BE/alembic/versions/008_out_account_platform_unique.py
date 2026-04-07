"""add account/platform and unique key to out

Revision ID: 008
Revises: 007
Create Date: 2026-04-07
"""
from alembic import op
import sqlalchemy as sa

revision = '008'
down_revision = '007'
branch_labels = None
depends_on = None


def upgrade():
    op.add_column('out', sa.Column('account_id', sa.Integer(), nullable=True))
    op.add_column('out', sa.Column('platform', sa.String(), nullable=True))
    op.create_unique_constraint(
        'uq_out_job_subjob_video_account',
        'out',
        ['job_id', 'sub_job_id', 'video_name', 'account_id'],
    )


def downgrade():
    op.drop_constraint('uq_out_job_subjob_video_account', 'out', type_='unique')
    op.drop_column('out', 'platform')
    op.drop_column('out', 'account_id')
