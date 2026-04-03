import io
import logging
import os
import random
import string
import subprocess
import tempfile
from datetime import datetime, timezone
from typing import Optional

from fastapi import FastAPI, UploadFile, File, Form, Depends, HTTPException
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import StreamingResponse
from pydantic import BaseModel
from sqlalchemy.orm import Session
from sqlalchemy import select, insert, update, delete

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
    datefmt="%Y-%m-%d %H:%M:%S",
)

from db import (
    get_db,
    jobs_table,
    sub_job_table,
    jobs_metadata_table,
    accounts_table,
    banners_table,
    backgrounds_table,
)
import storage
from tracing import setup_tracing

setup_tracing()

app = FastAPI(title="Terex Admin API")

try:
    from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor
    FastAPIInstrumentor.instrument_app(app)
except Exception:
    pass


@app.on_event("startup")
def on_startup():
    logging.info("Storage available: %s (endpoint=%s)", storage.is_available(), storage.ENDPOINT)
    from db import engine
    if engine:
        logging.info("DB connected: %s", engine.url)
    else:
        logging.warning("DB not connected — DATABASE_URL not set")


app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

MOCK_ACCOUNTS = {
    "youtube": ["SENATOR", "LTINC", "funny_cliper2000"],
    "tiktok": ["@senator_tiktok", "@ltinc_official"],
    "instagram": ["@senator_ig", "@ltinc_ig"],
}


# ── Pydantic models ──────────────────────────────────────────────────────────

class AccountRecord(BaseModel):
    id: int
    platform: str
    name: str
    url: Optional[str] = None
    note: Optional[str] = None
    bucket: Optional[str] = None
    file_name: Optional[str] = None

class AccountRecordUpdate(BaseModel):
    platform: Optional[str] = None
    name: Optional[str] = None
    url: Optional[str] = None
    note: Optional[str] = None

class AssetRecord(BaseModel):
    id: int
    name: str
    note: Optional[str] = None
    bucket: Optional[str] = None
    file_name: Optional[str] = None

class AssetRecordUpdate(BaseModel):
    name: Optional[str] = None
    note: Optional[str] = None


class SubJobResponse(BaseModel):
    sub_job_id: str
    platform: str
    publish_datetime: Optional[datetime] = None
    account_ids: list[int] = []
    account_names: list[str] = []
    banner_id: Optional[int] = None
    banner_name: Optional[str] = None

class JobResponse(BaseModel):
    job_id: str
    youtube_url: str
    is_vertical: bool
    is_subtitles: bool
    is_slice: bool
    is_background: bool
    is_circle: bool
    status: str
    trace: Optional[str] = None
    create_datetime: Optional[datetime] = None
    last_change_datetime: Optional[datetime] = None
    sub_jobs: list[SubJobResponse] = []

class JobUpdate(BaseModel):
    youtube_url: Optional[str] = None
    is_vertical: Optional[bool] = None
    is_subtitles: Optional[bool] = None
    is_slice: Optional[bool] = None
    is_background: Optional[bool] = None
    is_circle: Optional[bool] = None
    status: Optional[str] = None
    trace: Optional[str] = None

class SubJobUpdate(BaseModel):
    platform: Optional[str] = None
    publish_datetime: Optional[datetime] = None
    account_names: Optional[list[str]] = None
    banner_name: Optional[str] = None

class PublishSchedule(BaseModel):
    date: str = ""
    time: str = ""
    step: str = ""


class BannerConfig(BaseModel):
    id: str
    bannerName: str = ""
    youtubeAccounts: list[str] = []
    tiktokAccounts: list[str] = []
    instagramAccounts: list[str] = []
    schedules: dict[str, PublishSchedule] = {}


class VideoCardPayload(BaseModel):
    id: str
    youtubeUrl: str = ""
    makeVertical: bool = False
    addSubtitles: bool = False
    sliceVideo: bool = False
    addBackground: bool = False
    shape: str = "rectangle"
    backgroundName: str = ""
    youtubeAccounts: list[str] = []
    tiktokAccounts: list[str] = []
    instagramAccounts: list[str] = []
    schedules: dict[str, PublishSchedule] = {}
    banners: list[BannerConfig] = []


# ── Video helpers ────────────────────────────────────────────────────────────

_VIDEO_EXTS = {".mov", ".avi", ".mkv", ".webm", ".flv", ".wmv", ".m4v", ".ts"}

def _is_video(filename: str) -> bool:
    return os.path.splitext(filename)[1].lower() in _VIDEO_EXTS | {".mp4"}

def _make_preview_mp4(content: bytes, original_name: str) -> tuple[bytes | None, str]:
    """
    Remux video to mp4 (stream copy, no re-encoding) for browser preview.
    Returns (mp4_bytes, preview_object_name) where preview_object_name is 'preview/{base}.mp4'.
    Returns (None, ...) on failure.
    """
    ext = os.path.splitext(original_name)[1].lower()
    base = os.path.splitext(original_name)[0]
    preview_name = f"preview/{base}.mp4"

    if ext == ".mp4":
        # already mp4 — store a copy under preview/
        return content, preview_name

    if ext not in _VIDEO_EXTS:
        return None, preview_name

    src_path = dst_path = ""
    try:
        with tempfile.NamedTemporaryFile(suffix=ext, delete=False) as src_f:
            src_f.write(content)
            src_path = src_f.name
        dst_path = src_path + ".mp4"

        # First try: stream copy (fast, no re-encoding)
        result = subprocess.run(
            ["ffmpeg", "-y", "-i", src_path, "-c", "copy", "-movflags", "+faststart", dst_path],
            capture_output=True, timeout=120,
        )
        if result.returncode != 0:
            # Fallback: re-encode with ultrafast preset (handles ProRes, HEVC, etc.)
            logging.info("Stream copy failed for %s, falling back to h264 re-encode", original_name)
            result = subprocess.run(
                [
                    "ffmpeg", "-y", "-i", src_path,
                    "-c:v", "libx264", "-preset", "ultrafast", "-crf", "23",
                    "-c:a", "aac", "-b:a", "128k",
                    "-movflags", "+faststart",
                    dst_path,
                ],
                capture_output=True, timeout=300,
            )

        if result.returncode == 0:
            with open(dst_path, "rb") as f:
                mp4_bytes = f.read()
            logging.info("Preview mp4 generated: %s (%d bytes)", preview_name, len(mp4_bytes))
            return mp4_bytes, preview_name
        else:
            logging.warning("ffmpeg failed for %s: %s", original_name, result.stderr.decode()[-300:])
            return None, preview_name
    except Exception as e:
        logging.warning("ffmpeg error for %s: %s", original_name, e)
        return None, preview_name
    finally:
        for p in (src_path, dst_path):
            try:
                if p:
                    os.unlink(p)
            except Exception:
                pass


# ── ID generation ────────────────────────────────────────────────────────────

_CHARS = string.ascii_letters + string.digits

def _short_id() -> str:
    return "".join(random.choices(_CHARS, k=10))

def _sub_job_id(job_id: str) -> str:
    return f"{job_id}-{_short_id()}"


# ── Helpers ──────────────────────────────────────────────────────────────────

def _parse_datetime(sched: PublishSchedule | None) -> datetime | None:
    if not sched or not sched.date or not sched.time:
        return None
    try:
        return datetime.fromisoformat(f"{sched.date}T{sched.time}").replace(tzinfo=timezone.utc)
    except ValueError:
        return None


def _get_or_create_account(db: Session, platform: str, name: str) -> int:
    """Return account.id, creating the row if it doesn't exist."""
    row = db.execute(
        select(accounts_table.c.id).where(
            accounts_table.c.platform == platform,
            accounts_table.c.name == name,
        )
    ).fetchone()
    if row:
        return row.id
    result = db.execute(
        insert(accounts_table).values(platform=platform, name=name).returning(accounts_table.c.id)
    )
    return result.fetchone().id


def _get_or_create_banner(db: Session, name: str) -> int:
    """Return banner.id, creating the row if it doesn't exist."""
    row = db.execute(
        select(banners_table.c.id).where(banners_table.c.name == name)
    ).fetchone()
    if row:
        return row.id
    result = db.execute(
        insert(banners_table).values(name=name).returning(banners_table.c.id)
    )
    return result.fetchone().id


def _create_sub_jobs(
    db: Session,
    job_id: str,
    platform_accounts: dict[str, list[str]],  # platform → [account names]
    schedules: dict[str, PublishSchedule],
    banner_id: int | None,
) -> None:
    """Insert one sub_job per platform that has at least one account selected."""
    for platform, account_names in platform_accounts.items():
        if not account_names:
            continue
        account_ids = [_get_or_create_account(db, platform, name) for name in account_names]
        publish_dt = _parse_datetime(schedules.get(platform))
        db.execute(
            insert(sub_job_table).values(
                sub_job_id=_sub_job_id(job_id),
                job_id=job_id,
                banner_id=banner_id,
                account_ids=account_ids,
                platform=platform,
                publish_datetime=publish_dt,
            )
        )


# ── Sub-job resolver ─────────────────────────────────────────────────────────

def _resolve_sub_job(db: Session, sj) -> SubJobResponse:
    account_names = []
    if sj.account_ids:
        for acc_id in sj.account_ids:
            row = db.execute(select(accounts_table.c.name).where(accounts_table.c.id == acc_id)).fetchone()
            if row:
                account_names.append(row.name)
    banner_name = None
    if sj.banner_id:
        row = db.execute(select(banners_table.c.name).where(banners_table.c.id == sj.banner_id)).fetchone()
        if row:
            banner_name = row.name
    return SubJobResponse(
        sub_job_id=sj.sub_job_id,
        platform=sj.platform,
        publish_datetime=sj.publish_datetime,
        account_ids=sj.account_ids or [],
        account_names=account_names,
        banner_id=sj.banner_id,
        banner_name=banner_name,
    )


# ── Routes ───────────────────────────────────────────────────────────────────

# ── Accounts management ───────────────────────────────────────────────────────

@app.get("/api/accounts/list", response_model=list[AccountRecord])
def list_accounts(db: Session = Depends(get_db)):
    if db is None:
        return []
    rows = db.execute(select(accounts_table)).fetchall()
    return [AccountRecord(**dict(r._mapping)) for r in rows]

@app.patch("/api/accounts/{account_id}", response_model=AccountRecord)
def patch_account(account_id: int, body: AccountRecordUpdate, db: Session = Depends(get_db)):
    if db is None:
        raise HTTPException(status_code=503, detail="No DB")
    fields = body.model_dump(exclude_none=True)
    if fields:
        db.execute(accounts_table.update().where(accounts_table.c.id == account_id).values(**fields))
        db.commit()
    row = db.execute(select(accounts_table).where(accounts_table.c.id == account_id)).fetchone()
    if not row:
        raise HTTPException(status_code=404, detail="Not found")
    return AccountRecord(**dict(row._mapping))

@app.delete("/api/accounts/{account_id}")
def delete_account(account_id: int, db: Session = Depends(get_db)):
    if db is None:
        raise HTTPException(status_code=503, detail="No DB")
    db.execute(delete(accounts_table).where(accounts_table.c.id == account_id))
    db.commit()
    return {"status": "deleted"}


# ── Banners management ────────────────────────────────────────────────────────

@app.get("/api/banners/list", response_model=list[AssetRecord])
def list_banners_db(db: Session = Depends(get_db)):
    if db is None:
        return []
    rows = db.execute(select(banners_table)).fetchall()
    return [AssetRecord(**dict(r._mapping)) for r in rows]

@app.get("/api/banners/preview/{file_name:path}")
def preview_banner(file_name: str):
    if not storage.is_available():
        raise HTTPException(status_code=503, detail="Storage not available")
    fetch_name = file_name
    if _is_video(file_name):
        base = os.path.splitext(file_name)[0]
        fetch_name = f"preview/{base}.mp4"
    try:
        obj = storage.client.get_object(storage.BUCKET_BANNERS, fetch_name)
        content_type = obj.headers.get("content-type", "application/octet-stream")
        return StreamingResponse(obj, media_type=content_type)
    except Exception as e:
        raise HTTPException(status_code=404, detail=str(e))

@app.patch("/api/banners/{banner_id}", response_model=AssetRecord)
def patch_banner(banner_id: int, body: AssetRecordUpdate, db: Session = Depends(get_db)):
    if db is None:
        raise HTTPException(status_code=503, detail="No DB")
    fields = body.model_dump(exclude_none=True)
    if fields:
        db.execute(banners_table.update().where(banners_table.c.id == banner_id).values(**fields))
        db.commit()
    row = db.execute(select(banners_table).where(banners_table.c.id == banner_id)).fetchone()
    if not row:
        raise HTTPException(status_code=404, detail="Not found")
    return AssetRecord(**dict(row._mapping))

@app.delete("/api/banners/{banner_id}")
def delete_banner(banner_id: int, db: Session = Depends(get_db)):
    if db is None:
        raise HTTPException(status_code=503, detail="No DB")
    row = db.execute(select(banners_table).where(banners_table.c.id == banner_id)).fetchone()
    if row and row.file_name and storage.is_available():
        for obj_name in [row.file_name, f"preview/{os.path.splitext(row.file_name)[0]}.mp4"]:
            try:
                storage.client.remove_object(storage.BUCKET_BANNERS, obj_name)
            except Exception:
                pass
    db.execute(delete(banners_table).where(banners_table.c.id == banner_id))
    db.commit()
    return {"status": "deleted"}


# ── Backgrounds management ────────────────────────────────────────────────────

@app.get("/api/backgrounds/list", response_model=list[AssetRecord])
def list_backgrounds_db(db: Session = Depends(get_db)):
    if db is None:
        return []
    rows = db.execute(select(backgrounds_table)).fetchall()
    return [AssetRecord(**dict(r._mapping)) for r in rows]

@app.get("/api/backgrounds/preview/{file_name:path}")
def preview_background(file_name: str):
    if not storage.is_available():
        raise HTTPException(status_code=503, detail="Storage not available")
    fetch_name = file_name
    if _is_video(file_name):
        base = os.path.splitext(file_name)[0]
        fetch_name = f"preview/{base}.mp4"
    try:
        obj = storage.client.get_object(storage.BUCKET_BACKGROUNDS, fetch_name)
        content_type = obj.headers.get("content-type", "application/octet-stream")
        return StreamingResponse(obj, media_type=content_type)
    except Exception as e:
        raise HTTPException(status_code=404, detail=str(e))

@app.patch("/api/backgrounds/{background_id}", response_model=AssetRecord)
def patch_background(background_id: int, body: AssetRecordUpdate, db: Session = Depends(get_db)):
    if db is None:
        raise HTTPException(status_code=503, detail="No DB")
    fields = body.model_dump(exclude_none=True)
    if fields:
        db.execute(backgrounds_table.update().where(backgrounds_table.c.id == background_id).values(**fields))
        db.commit()
    row = db.execute(select(backgrounds_table).where(backgrounds_table.c.id == background_id)).fetchone()
    if not row:
        raise HTTPException(status_code=404, detail="Not found")
    return AssetRecord(**dict(row._mapping))

@app.delete("/api/backgrounds/{background_id}")
def delete_background(background_id: int, db: Session = Depends(get_db)):
    if db is None:
        raise HTTPException(status_code=503, detail="No DB")
    row = db.execute(select(backgrounds_table).where(backgrounds_table.c.id == background_id)).fetchone()
    if row and row.file_name and storage.is_available():
        for obj_name in [row.file_name, f"preview/{os.path.splitext(row.file_name)[0]}.mp4"]:
            try:
                storage.client.remove_object(storage.BUCKET_BACKGROUNDS, obj_name)
            except Exception:
                pass
    db.execute(delete(backgrounds_table).where(backgrounds_table.c.id == background_id))
    db.commit()
    return {"status": "deleted"}


@app.get("/api/backgrounds")
def get_backgrounds():
    if storage.is_available():
        return storage.list_objects(storage.BUCKET_BACKGROUNDS)
    return []


@app.get("/api/banners")
def get_banners():
    if storage.is_available():
        return storage.list_objects(storage.BUCKET_BANNERS)
    return []


@app.get("/api/accounts")
def get_accounts():
    return MOCK_ACCOUNTS


@app.post("/api/jobs")
def create_job(card: VideoCardPayload, db: Session = Depends(get_db)):
    job_id = _short_id()

    # ── If no DB connection fall back to mock response ──────────────────────
    if db is None:
        return {"status": "queued", "job_id": job_id}

    # ── Insert into jobs ────────────────────────────────────────────────────
    db.execute(
        insert(jobs_table).values(
            job_id=job_id,
            youtube_url=card.youtubeUrl,
            is_vertical=card.makeVertical,
            is_subtitles=card.addSubtitles,
            is_slice=card.sliceVideo,
            is_background=card.addBackground,
            is_circle=(card.shape == "circle"),
            trace=None,
            status="pending",
        )
    )

    # ── Insert into jobs_metadata ───────────────────────────────────────────
    db.execute(insert(jobs_metadata_table).values(job_id=job_id))

    # ── Sub-jobs ────────────────────────────────────────────────────────────
    if not card.banners:
        # Use base account selection directly on the job
        _create_sub_jobs(
            db,
            job_id,
            platform_accounts={
                "youtube": card.youtubeAccounts,
                "tiktok": card.tiktokAccounts,
                "instagram": card.instagramAccounts,
            },
            schedules=card.schedules,
            banner_id=None,
        )
    else:
        # Each banner generates its own set of sub-jobs
        for banner in card.banners:
            banner_id = _get_or_create_banner(db, banner.bannerName) if banner.bannerName else None
            _create_sub_jobs(
                db,
                job_id,
                platform_accounts={
                    "youtube": banner.youtubeAccounts,
                    "tiktok": banner.tiktokAccounts,
                    "instagram": banner.instagramAccounts,
                },
                schedules=banner.schedules,
                banner_id=banner_id,
            )

    db.commit()
    return {"status": "queued", "job_id": job_id}


@app.get("/api/jobs", response_model=list[JobResponse])
def list_jobs(db: Session = Depends(get_db)):
    if db is None:
        return []
    jobs = db.execute(select(jobs_table).order_by(jobs_table.c.job_id)).fetchall()
    result = []
    for job in jobs:
        meta = db.execute(
            select(jobs_metadata_table).where(jobs_metadata_table.c.job_id == job.job_id)
        ).fetchone()
        sj_rows = db.execute(
            select(sub_job_table).where(sub_job_table.c.job_id == job.job_id)
        ).fetchall()
        result.append(JobResponse(
            job_id=job.job_id,
            youtube_url=job.youtube_url,
            is_vertical=job.is_vertical,
            is_subtitles=job.is_subtitles,
            is_slice=job.is_slice,
            is_background=job.is_background,
            is_circle=job.is_circle,
            status=job.status,
            trace=job.trace,
            create_datetime=meta.create_datetime if meta else None,
            last_change_datetime=meta.last_change_datetime if meta else None,
            sub_jobs=[_resolve_sub_job(db, sj) for sj in sj_rows],
        ))
    return result


@app.patch("/api/jobs/{job_id}", response_model=JobResponse)
def patch_job(job_id: str, body: JobUpdate, db: Session = Depends(get_db)):
    if db is None:
        raise HTTPException(status_code=503, detail="No DB")
    fields = body.model_dump(exclude_none=True)
    if not fields:
        raise HTTPException(status_code=400, detail="Nothing to update")
    db.execute(jobs_table.update().where(jobs_table.c.job_id == job_id).values(**fields))
    db.execute(
        jobs_metadata_table.update()
        .where(jobs_metadata_table.c.job_id == job_id)
        .values(last_change_datetime=datetime.now(timezone.utc))
    )
    db.commit()
    job = db.execute(select(jobs_table).where(jobs_table.c.job_id == job_id)).fetchone()
    if not job:
        raise HTTPException(status_code=404, detail="Job not found")
    meta = db.execute(
        select(jobs_metadata_table).where(jobs_metadata_table.c.job_id == job_id)
    ).fetchone()
    sj_rows = db.execute(select(sub_job_table).where(sub_job_table.c.job_id == job_id)).fetchall()
    return JobResponse(
        job_id=job.job_id, youtube_url=job.youtube_url,
        is_vertical=job.is_vertical, is_subtitles=job.is_subtitles,
        is_slice=job.is_slice, is_background=job.is_background,
        is_circle=job.is_circle, status=job.status, trace=job.trace,
        create_datetime=meta.create_datetime if meta else None,
        last_change_datetime=meta.last_change_datetime if meta else None,
        sub_jobs=[_resolve_sub_job(db, sj) for sj in sj_rows],
    )


@app.delete("/api/jobs/{job_id}")
def delete_job(job_id: str, db: Session = Depends(get_db)):
    if db is None:
        raise HTTPException(status_code=503, detail="No DB")
    db.execute(delete(jobs_table).where(jobs_table.c.job_id == job_id))
    db.commit()
    return {"status": "deleted"}


@app.patch("/api/sub_jobs/{sub_job_id}", response_model=SubJobResponse)
def patch_sub_job(sub_job_id: str, body: SubJobUpdate, db: Session = Depends(get_db)):
    if db is None:
        raise HTTPException(status_code=503, detail="No DB")
    sj = db.execute(select(sub_job_table).where(sub_job_table.c.sub_job_id == sub_job_id)).fetchone()
    if not sj:
        raise HTTPException(status_code=404, detail="SubJob not found")

    fields: dict = {}
    if body.platform is not None:
        fields["platform"] = body.platform
    if body.publish_datetime is not None:
        fields["publish_datetime"] = body.publish_datetime
    if body.account_names is not None:
        platform = body.platform or sj.platform
        fields["account_ids"] = [_get_or_create_account(db, platform, n) for n in body.account_names]
    if body.banner_name is not None:
        fields["banner_id"] = _get_or_create_banner(db, body.banner_name) if body.banner_name else None

    if fields:
        db.execute(sub_job_table.update().where(sub_job_table.c.sub_job_id == sub_job_id).values(**fields))
        db.execute(
            jobs_metadata_table.update()
            .where(jobs_metadata_table.c.job_id == sj.job_id)
            .values(last_change_datetime=datetime.now(timezone.utc))
        )
        db.commit()

    sj = db.execute(select(sub_job_table).where(sub_job_table.c.sub_job_id == sub_job_id)).fetchone()
    return _resolve_sub_job(db, sj)


@app.delete("/api/sub_jobs/{sub_job_id}")
def delete_sub_job(sub_job_id: str, db: Session = Depends(get_db)):
    if db is None:
        raise HTTPException(status_code=503, detail="No DB")
    db.execute(delete(sub_job_table).where(sub_job_table.c.sub_job_id == sub_job_id))
    db.commit()
    return {"status": "deleted"}


@app.post("/api/banners/upload")
async def upload_banner(
    name: str = Form(""),
    note: str = Form(""),
    file: Optional[UploadFile] = File(None),
    db: Session = Depends(get_db),
):
    file_name = None
    try:
        if file and file.filename:
            content = await file.read()
            original_name = file.filename
            ext = original_name.rsplit(".", 1)[-1] if "." in original_name else ""
            base = name.strip() or original_name.rsplit(".", 1)[0]
            file_name = f"{base}.{ext}" if ext else base
            if not name.strip():
                name = file_name
            if storage.is_available():
                storage.upload_stream(
                    storage.BUCKET_BANNERS, file_name,
                    data=io.BytesIO(content), length=len(content),
                    content_type=file.content_type or "application/octet-stream",
                )
                logging.info("Banner uploaded to MinIO: %s/%s", storage.BUCKET_BANNERS, file_name)
                if _is_video(original_name):
                    mp4_bytes, preview_name = _make_preview_mp4(content, file_name)
                    if mp4_bytes:
                        storage.upload_stream(
                            storage.BUCKET_BANNERS, preview_name,
                            data=io.BytesIO(mp4_bytes), length=len(mp4_bytes),
                            content_type="video/mp4",
                        )
                        logging.info("Banner preview saved: %s/%s", storage.BUCKET_BANNERS, preview_name)
            else:
                logging.warning("MinIO not available — banner file not saved to storage")
    except Exception as e:
        logging.exception("Failed to upload banner to MinIO: %s", e)
        raise HTTPException(status_code=500, detail=f"Storage upload failed: {e}")

    try:
        if db is not None:
            existing = db.execute(
                select(banners_table.c.id).where(banners_table.c.name == name)
            ).fetchone()
            if existing:
                db.execute(
                    banners_table.update()
                    .where(banners_table.c.name == name)
                    .values(note=note, bucket=storage.BUCKET_BANNERS, file_name=file_name)
                )
            else:
                db.execute(
                    insert(banners_table).values(
                        name=name, note=note,
                        bucket=storage.BUCKET_BANNERS if file_name else None,
                        file_name=file_name,
                    )
                )
            db.commit()
            logging.info("Banner saved to DB: %s", name)
    except Exception as e:
        logging.exception("Failed to save banner to DB: %s", e)
        raise HTTPException(status_code=500, detail=f"DB insert failed: {e}")

    return {"status": "uploaded", "name": name}


@app.post("/api/backgrounds/upload")
async def upload_background(
    name: str = Form(""),
    note: str = Form(""),
    file: Optional[UploadFile] = File(None),
    db: Session = Depends(get_db),
):
    file_name = None
    try:
        if file and file.filename:
            content = await file.read()
            original_name = file.filename
            ext = original_name.rsplit(".", 1)[-1] if "." in original_name else ""
            base = name.strip() or original_name.rsplit(".", 1)[0]
            file_name = f"{base}.{ext}" if ext else base
            if not name.strip():
                name = file_name
            if storage.is_available():
                storage.upload_stream(
                    storage.BUCKET_BACKGROUNDS, file_name,
                    data=io.BytesIO(content), length=len(content),
                    content_type=file.content_type or "application/octet-stream",
                )
                logging.info("Background uploaded to MinIO: %s/%s", storage.BUCKET_BACKGROUNDS, file_name)
                if _is_video(original_name):
                    mp4_bytes, preview_name = _make_preview_mp4(content, file_name)
                    if mp4_bytes:
                        storage.upload_stream(
                            storage.BUCKET_BACKGROUNDS, preview_name,
                            data=io.BytesIO(mp4_bytes), length=len(mp4_bytes),
                            content_type="video/mp4",
                        )
                        logging.info("Background preview saved: %s/%s", storage.BUCKET_BACKGROUNDS, preview_name)
            else:
                logging.warning("MinIO not available — background file not saved to storage")
    except Exception as e:
        logging.exception("Failed to upload background to MinIO: %s", e)
        raise HTTPException(status_code=500, detail=f"Storage upload failed: {e}")

    try:
        if db is not None:
            existing = db.execute(
                select(backgrounds_table.c.id).where(backgrounds_table.c.name == name)
            ).fetchone()
            if existing:
                db.execute(
                    backgrounds_table.update()
                    .where(backgrounds_table.c.name == name)
                    .values(note=note, bucket=storage.BUCKET_BACKGROUNDS, file_name=file_name)
                )
            else:
                db.execute(
                    insert(backgrounds_table).values(
                        name=name, note=note,
                        bucket=storage.BUCKET_BACKGROUNDS if file_name else None,
                        file_name=file_name,
                    )
                )
            db.commit()
            logging.info("Background saved to DB: %s", name)
    except Exception as e:
        logging.exception("Failed to save background to DB: %s", e)
        raise HTTPException(status_code=500, detail=f"DB insert failed: {e}")

    return {"status": "uploaded", "name": name}


@app.post("/api/accounts/create")
async def create_account(
    platform: str = Form(...),
    name: str = Form(...),
    url: str = Form(""),
    note: str = Form(""),
    auth_file: Optional[UploadFile] = File(None),
    db: Session = Depends(get_db),
):
    file_name = None
    if auth_file and auth_file.filename and storage.is_available():
        content = await auth_file.read()
        file_name = f"{platform}/{name}/{auth_file.filename}"
        storage.upload_stream(
            storage.BUCKET_COOKIES,
            file_name,
            data=io.BytesIO(content),
            length=len(content),
            content_type=auth_file.content_type or "application/octet-stream",
        )

    if db is not None:
        existing = db.execute(
            select(accounts_table.c.id).where(
                accounts_table.c.platform == platform,
                accounts_table.c.name == name,
            )
        ).fetchone()
        if existing:
            db.execute(
                accounts_table.update()
                .where(
                    accounts_table.c.platform == platform,
                    accounts_table.c.name == name,
                )
                .values(
                    url=url, note=note,
                    bucket=storage.BUCKET_COOKIES if file_name else None,
                    file_name=file_name,
                )
            )
        else:
            db.execute(
                insert(accounts_table).values(
                    platform=platform, name=name, url=url, note=note,
                    bucket=storage.BUCKET_COOKIES if file_name else None,
                    file_name=file_name,
                )
            )
        db.commit()

    return {"status": "created", "name": name, "platform": platform}


if __name__ == "__main__":
    import uvicorn
    uvicorn.run("main:app", host="0.0.0.0", port=8000, reload=True)
