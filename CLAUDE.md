# CLAUDE.md — Terex Admin Panel

## Overview

Admin panel for the LT-Inc video pipeline. Manages video upload jobs, platform accounts, banners, and backgrounds. Reads/writes a dedicated PostgreSQL database (`main`) and stores files in MinIO.

## Stack

| Layer | Tech |
|-------|------|
| Frontend | Next.js 15 (App Router), TypeScript, TailwindCSS |
| Backend | FastAPI (Python), SQLAlchemy Core, Alembic |
| Database | PostgreSQL — database `main` (shared PostgreSQL container) |
| Storage | MinIO — buckets `banners`, `backgrounds`, `cookies` |
| Video processing | ffmpeg (stream copy → h264 fallback) for MP4 preview generation |
| Tracing | OpenTelemetry → Tempo (OTLP gRPC) |

## Running

### Via Docker (recommended)

```bash
cd n8n/
docker compose --file docker/docker-compose.local.yml up --build
```

Frontend: `https://terex.legendary-trio-local.me`
Backend: `http://localhost:8000`

### Local development

```bash
# Backend
cd terex/BE
pip install -r requirements.txt
DATABASE_URL=postgresql://... S3_ENDPOINT=... python main.py   # :8000

# Frontend
cd terex/FE
npm install
npm run dev    # :3000, proxies /api/* → http://localhost:8000
```

## Project Structure

```
terex/
  BE/
    main.py              # FastAPI app — all routes, business logic
    db.py                # SQLAlchemy engine, session, all table definitions
    storage.py           # MinIO client, upload/list helpers
    requirements.txt
    alembic.ini
    alembic/versions/
      001_initial_schema.py        # jobs, sub_job, out, jobs_metadata
      002_accounts_banners.py      # accounts, banners; FK sub_job.banner_id
      003_file_refs.py             # bucket+file_name cols; backgrounds table
      004_background_id.py         # jobs.background_id FK → backgrounds
      005_min_video_duration.py    # jobs.min_video_duration (default 60)
      006_sub_job_is_add_banner.py # sub_job.is_add_banner boolean
      007_out_work_state.py        # out.status
      008_out_account_platform_unique.py  # out.account_id, out.platform, unique constraint
      009_sub_job_step.py          # sub_job.publish_step (default 0)
      010_jobs_sub_job_fail_place_status.py # fail_place on jobs; fail_place+status on sub_job
      011_out_published_at.py      # out.published_at timestamp
      012_out_hashtag.py           # out.hashtag
    docker/
      Dockerfile         # python:3.11-slim + ffmpeg + pip install
      entrypoint.sh      # create DB if missing → alembic upgrade head → uvicorn --reload
  FE/
    next.config.mjs      # rewrites /api/* → BACKEND_URL (default :8000)
    src/
      app/
        layout.tsx                         # root layout: Sidebar + main content
        page.tsx                           # placeholder
        admin/
          upload-video/page.tsx            # submit video jobs
          create-account/page.tsx          # upload platform accounts
          banners/page.tsx                 # upload banner files (localStorage draft)
          backgrounds/page.tsx             # upload background files (localStorage draft)
          accounts/page.tsx                # view & manage accounts
          banners-list/page.tsx            # view & manage banners (with preview)
          backgrounds-list/page.tsx        # view & manage backgrounds (with preview)
          jobs/page.tsx                    # view & manage jobs + sub-jobs
      components/
        layout/
          Sidebar.tsx          # collapsible Admin group + Other Services group
          Header.tsx
          Footer.tsx
        upload-video/
          VideoCard.tsx        # video job card with options, accounts, schedules, banners
          BannerCard.tsx       # nested banner card inside VideoCard
        create-account/
          AccountCard.tsx      # account card: platform/name/url/authfile/note
        assets/
          AssetCard.tsx        # banner/background upload card (name + dropzone + note)
        records/
          AssetRecordCard.tsx  # banner/background view card with preview + edit/delete
          AccountRecordCard.tsx # account view card with edit/delete
        jobs/
          JobCard.tsx          # collapsible job card with edit mode
          SubJobCard.tsx       # collapsible sub-job card inside JobCard with edit mode
        ui/
          Toggle.tsx           # sliding toggle with disabled state
          MultiSelect.tsx      # checkbox dropdown
          FileDropzone.tsx     # drag-and-drop or click file picker
      types/index.ts     # all shared TypeScript types
      lib/api.ts         # fetch wrappers for all backend endpoints
    docker/
      Dockerfile         # node:20-alpine, source mounted as volume for hot reload
  k8s/
    namespace.yaml       # namespace: admin
    configmap.yaml       # terex-config ConfigMap
    secret.yaml          # terex-secret Secret
    be-deployment.yaml   # terex-be, image registry.legendary-trio.me/lt-inc/terex-be:latest, port 8000
    fe-deployment.yaml   # terex-fe, image registry.legendary-trio.me/lt-inc/terex-fe:latest, port 3000
    be-service.yaml
    fe-service.yaml
    ingress.yaml         # host admin.legendary-trio.me → terex-fe:3000, TLS terex-tls
```

## Sidebar Navigation

- **Admin**
  - View Jobs → `/admin/jobs`
  - Upload Video → `/admin/upload-video`
  - View Accounts → `/admin/accounts`
  - Create Account → `/admin/create-account`
  - View Banners → `/admin/banners-list`
  - Create Banners → `/admin/banners`
  - View Backgrounds → `/admin/backgrounds-list`
  - Create Backgrounds → `/admin/backgrounds`
- **Other Services** (open in new tab)
  - Grafana, N8N, MinIO, RabbitMQ, PgAdmin, Portainer

## Backend API

### Jobs

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/jobs` | List all jobs with sub-jobs + metadata timestamps |
| POST | `/api/jobs` | Create job + sub-jobs from VideoCard payload |
| PATCH | `/api/jobs/{job_id}` | Update job fields; updates last_change_datetime |
| DELETE | `/api/jobs/{job_id}` | Delete job (cascades to sub-jobs) |
| PATCH | `/api/sub_jobs/{sub_job_id}` | Update sub-job fields (accounts, banner, platform, status, fail_place, publish_datetime) |
| DELETE | `/api/sub_jobs/{sub_job_id}` | Delete sub-job |

### Accounts

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/accounts` | `{ youtube, tiktok, instagram }` — name lists for dropdowns |
| GET | `/api/accounts/list` | Full account records from DB |
| POST | `/api/accounts/create` | Create/update account + upload auth file to MinIO cookies bucket |
| PATCH | `/api/accounts/{id}` | Update account fields |
| DELETE | `/api/accounts/{id}` | Delete account from DB |

### Banners

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/banners` | Filenames from MinIO (for dropdowns) |
| GET | `/api/banners/list` | Full banner records from DB |
| GET | `/api/banners/preview/{file_name}` | Stream file from MinIO (video → redirects to `preview/*.mp4`) |
| POST | `/api/banners/upload` | Upload file to MinIO + save record to DB |
| PATCH | `/api/banners/{id}` | Update name/note |
| DELETE | `/api/banners/{id}` | Delete from DB + remove file(s) from MinIO |

### Backgrounds

Same pattern as Banners — endpoints under `/api/backgrounds/`.

## Database Schema (PostgreSQL `main`)

### `jobs`
| Column | Type | Notes |
|--------|------|-------|
| `job_id` | VARCHAR(10) PK | 10 hex chars |
| `youtube_url` | TEXT NOT NULL | |
| `is_vertical` | BOOLEAN | |
| `is_subtitles` | BOOLEAN | |
| `is_slice` | BOOLEAN | |
| `is_background` | BOOLEAN | |
| `is_circle` | BOOLEAN | |
| `min_video_duration` | INTEGER | default 60 (seconds) |
| `background_id` | INTEGER FK → backgrounds | nullable |
| `trace` | TEXT | nullable |
| `status` | VARCHAR(50) NOT NULL | `pending`, `processing`, `done`, `failed`; default `pending` |
| `fail_place` | VARCHAR(50) NOT NULL | default `""` |

### `sub_job`
| Column | Type | Notes |
|--------|------|-------|
| `sub_job_id` | VARCHAR(21) PK | `{job_id}-{10chars}` |
| `job_id` | VARCHAR(10) FK → jobs | CASCADE DELETE |
| `banner_id` | INTEGER FK → banners | nullable, SET NULL on delete |
| `account_ids` | INTEGER[] | array of account IDs |
| `platform` | VARCHAR(50) NOT NULL | `youtube`, `tiktok`, `instagram` |
| `publish_datetime` | TIMESTAMP WITH TIME ZONE | nullable |
| `publish_step` | INTEGER | default 0 |
| `status` | VARCHAR(50) | default `pending` |
| `fail_place` | VARCHAR(50) | default `""` |
| `is_add_banner` | BOOLEAN | default false |

Index: `ix_sub_job_job_id`

### `jobs_metadata`
| Column | Type |
|--------|------|
| `job_id` | VARCHAR(10) FK PK → jobs, CASCADE DELETE |
| `create_datetime` | TIMESTAMP WITH TIME ZONE (server default now()) |
| `last_change_datetime` | TIMESTAMP WITH TIME ZONE (server default now()) |

### `out`
Tracks per-clip publishing results.

| Column | Type | Notes |
|--------|------|-------|
| `id` | INTEGER PK autoincrement | |
| `job_id` | VARCHAR FK → jobs | CASCADE DELETE |
| `sub_job_id` | VARCHAR FK → sub_job | nullable, SET NULL |
| `video_name` | VARCHAR | nullable |
| `name` | VARCHAR | nullable |
| `description` | TEXT | nullable |
| `hashtag` | VARCHAR | nullable |
| `upload_id` | VARCHAR | nullable |
| `publish_date` | TIMESTAMP WITH TIME ZONE | nullable |
| `published_at` | TIMESTAMP WITH TIME ZONE | nullable |
| `status` | VARCHAR | nullable |
| `account_id` | INTEGER | nullable |
| `platform` | VARCHAR | nullable |

UniqueConstraint: `(job_id, sub_job_id, video_name, account_id)`
Indexes: `ix_out_job_id`, `ix_out_sub_job_id`

### `accounts`
| Column | Type | Notes |
|--------|------|-------|
| `id` | SERIAL PK | |
| `platform` | VARCHAR NOT NULL | UniqueConstraint with name |
| `name` | VARCHAR NOT NULL | UniqueConstraint with platform |
| `url` | TEXT | nullable |
| `note` | TEXT | nullable |
| `bucket` | VARCHAR | nullable — MinIO bucket for auth file |
| `file_name` | TEXT | nullable — path within bucket |

### `banners` / `backgrounds`
| Column | Type |
|--------|------|
| `id` | SERIAL PK |
| `name` | VARCHAR UNIQUE |
| `note` | TEXT nullable |
| `bucket` | VARCHAR nullable |
| `file_name` | TEXT nullable |

## MinIO Storage

| Bucket | Contents |
|--------|---------|
| `banners` | Original banner files + `preview/{base}.mp4` for video files |
| `backgrounds` | Original background files + `preview/{base}.mp4` for video files |
| `cookies` | Account auth files at `{platform}/{name}/{filename}` |

### Video Preview Generation

On upload of any video file (`.mp4`, `.mov`, `.avi`, `.mkv`, `.webm`, `.flv`, `.wmv`, `.m4v`, `.ts`):
1. Original file saved to MinIO as-is
2. ffmpeg remux attempted: `-c copy -movflags +faststart` (no re-encoding; timeout 120s)
3. If codec incompatible with MP4 (e.g. ProRes) → fallback: `-c:v libx264 -preset ultrafast -crf 23 -c:a aac -b:a 128k` (timeout 300s)
4. MP4 preview saved to `preview/{base}.mp4` in same bucket

Preview endpoint auto-redirects to `preview/{base}.mp4` for video `file_name`s.

## Data Types (TypeScript)

### VideoCardData
| Field | Type |
|-------|------|
| `id` | string (UUID) |
| `youtubeUrl` | string |
| `makeVertical` | boolean |
| `addSubtitles` | boolean |
| `sliceVideo` | boolean |
| `minVideoDuration` | number |
| `addBackground` | boolean (disabled when makeVertical=false) |
| `shape` | `"circle" \| "rectangle"` |
| `backgroundName` | string |
| `youtubeAccounts` | string[] |
| `tiktokAccounts` | string[] |
| `instagramAccounts` | string[] |
| `schedules` | `Partial<Record<Platform, PublishSchedule>>` |
| `banners` | `BannerConfig[]` |

### BannerConfig
| Field | Type |
|-------|------|
| `id` | string (UUID) |
| `bannerName` | string |
| `youtubeAccounts` | string[] |
| `tiktokAccounts` | string[] |
| `instagramAccounts` | string[] |
| `schedules` | `Partial<Record<Platform, PublishSchedule>>` |

### JobData
| Field | Type |
|-------|------|
| `job_id` | string |
| `youtube_url` | string |
| `is_vertical` | boolean |
| `is_subtitles` | boolean |
| `is_slice` | boolean |
| `is_background` | boolean |
| `min_video_duration` | number |
| `background_id` | number \| null |
| `background_name` | string \| null |
| `is_circle` | boolean |
| `status` | string |
| `trace` | string \| null |
| `create_datetime` | string \| null |
| `last_change_datetime` | string \| null |
| `fail_place` | string |
| `sub_jobs` | SubJobData[] |

### SubJobData
| Field | Type |
|-------|------|
| `sub_job_id` | string |
| `platform` | string |
| `publish_datetime` | string \| null |
| `status` | string |
| `fail_place` | string |
| `account_ids` | number[] |
| `account_names` | string[] |
| `is_add_banner` | boolean |
| `banner_id` | number \| null |
| `banner_name` | string \| null |

### AssetRecord (banners/backgrounds)
| Field | Type |
|-------|------|
| `id` | number |
| `name` | string |
| `note` | string \| null |
| `bucket` | string \| null |
| `file_name` | string \| null |

### AccountRecord
| Field | Type |
|-------|------|
| `id` | number |
| `platform` | string |
| `name` | string |
| `url` | string \| null |
| `note` | string \| null |
| `bucket` | string \| null |
| `file_name` | string \| null |

## Key Behaviors

- "Add background?" toggle disabled when "Make vertical?" is off
- Per-platform publish schedule visible when ≥1 account selected for that platform
- Each banner in VideoCard has its own account selection and schedule
- Upload pages (banners/backgrounds) use localStorage for draft state; file objects are not persisted
- View pages stream file previews through the backend (browser never hits MinIO directly)
- Video preview box: `object-contain` — scales to fit without cropping
- Hover on video preview → play; mouse out → pause + reset to start
- SubJobCard locked (read-only) when parent JobCard is in edit mode

## Kubernetes Deployment

Manifests in `k8s/`, deploy to `admin` namespace on k3s cluster.

| Resource | Details |
|----------|---------|
| Ingress host | `admin.legendary-trio.me` |
| BE image | `registry.legendary-trio.me/lt-inc/terex-be:latest` |
| FE image | `registry.legendary-trio.me/lt-inc/terex-fe:latest` |
| BE readiness | GET `/docs` on port 8000 |
| FE readiness | GET `/` on port 3000 |
| Resources | requests: 100m CPU / 256Mi RAM; limits: 500m CPU / 512Mi RAM |
| Ingress timeouts | proxy-read-timeout 300s, proxy-send-timeout 300s |

## Environment Variables

| Variable | Service | Description |
|----------|---------|-------------|
| `DATABASE_URL` | BE | PostgreSQL connection string |
| `S3_ENDPOINT` | BE | MinIO host (e.g. `minio:9000`) |
| `S3_ACCESS_KEY` | BE | MinIO access key |
| `S3_SECRET_KEY` | BE | MinIO secret key |
| `S3_BUCKET_BANNERS` | BE | default `banners` |
| `S3_BUCKET_BACKGROUNDS` | BE | default `backgrounds` |
| `S3_BUCKET_COOKIES` | BE | default `cookies` |
| `DEV` | BE | `true` enables uvicorn `--reload` |
| `OTEL_SERVICE_NAME` | BE | `terex-be` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | BE | `http://tempo:4317` |
| `BACKEND_URL` | FE | Backend base URL (default `http://localhost:8000`) |

## TODO

- Real job submission to RabbitMQ (currently only saves to DB)
- Auth
- Deploy to k3s via k8s/ manifests
