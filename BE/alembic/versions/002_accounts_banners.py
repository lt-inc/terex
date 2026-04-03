"""accounts and banners tables

Revision ID: 002
Revises: 001
Create Date: 2026-04-03

"""
from typing import Sequence, Union

from alembic import op
import sqlalchemy as sa

revision: str = "002"
down_revision: Union[str, None] = "001"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    op.create_table(
        "accounts",
        sa.Column("id", sa.Integer(), primary_key=True, autoincrement=True),
        sa.Column("platform", sa.String(), nullable=False),
        sa.Column("name", sa.String(), nullable=False),
        sa.Column("url", sa.String(), nullable=True),
        sa.Column("note", sa.Text(), nullable=True),
        sa.UniqueConstraint("platform", "name", name="uq_accounts_platform_name"),
    )

    op.create_table(
        "banners",
        sa.Column("id", sa.Integer(), primary_key=True, autoincrement=True),
        sa.Column("name", sa.String(), nullable=False, unique=True),
        sa.Column("note", sa.Text(), nullable=True),
    )

    # Now that accounts and banners exist, add FK on sub_job.banner_id
    op.create_foreign_key(
        "fk_sub_job_banner_id",
        "sub_job", "banners",
        ["banner_id"], ["id"],
        ondelete="SET NULL",
    )


def downgrade() -> None:
    op.drop_constraint("fk_sub_job_banner_id", "sub_job", type_="foreignkey")
    op.drop_table("banners")
    op.drop_table("accounts")
