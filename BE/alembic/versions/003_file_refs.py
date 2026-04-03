"""add bucket/file_name to banners and accounts; add backgrounds table

Revision ID: 003
Revises: 002
Create Date: 2026-04-03

"""
from typing import Sequence, Union

from alembic import op
import sqlalchemy as sa

revision: str = "003"
down_revision: Union[str, None] = "002"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    op.add_column("banners", sa.Column("bucket", sa.String(), nullable=True))
    op.add_column("banners", sa.Column("file_name", sa.String(), nullable=True))

    op.add_column("accounts", sa.Column("bucket", sa.String(), nullable=True))
    op.add_column("accounts", sa.Column("file_name", sa.String(), nullable=True))

    op.create_table(
        "backgrounds",
        sa.Column("id", sa.Integer(), primary_key=True, autoincrement=True),
        sa.Column("name", sa.String(), nullable=False, unique=True),
        sa.Column("note", sa.Text(), nullable=True),
        sa.Column("bucket", sa.String(), nullable=True),
        sa.Column("file_name", sa.String(), nullable=True),
    )


def downgrade() -> None:
    op.drop_table("backgrounds")
    op.drop_column("accounts", "file_name")
    op.drop_column("accounts", "bucket")
    op.drop_column("banners", "file_name")
    op.drop_column("banners", "bucket")
