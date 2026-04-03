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
      001_initial_schema.py   # jobs, sub_job, out, jobs_metadata
      002_accounts_banners.py # accounts, banners; FK sub_job.banner_id
      003_file_refs.py        # bucket+file_name cols; backgrounds table
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
| GET | `/api/jobs` | List all jobs with sub-jobs |
| POST | `/api/jobs` | Create job + sub-jobs from VideoCard payload |
| PATCH | `/api/jobs/{job_id}` | Update job fields |
| DELETE | `/api/jobs/{job_id}` | Delete job (cascades to sub-jobs) |
| PATCH | `/api/sub_jobs/{sub_job_id}` | Update sub-job fields |
| DELETE | `/api/sub_jobs/{sub_job_id}` | Delete sub-job |

### Accounts

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/accounts` | `{ youtube, tiktok, instagram }` — name lists for dropdowns |
| GET | `/api/accounts/list` | Full account records from DB |
| POST | `/api/accounts/create` | Create/update account + upload auth file to MinIO |
| PATCH | `/api/accounts/{id}` | Update account fields |
| DELETE | `/api/accounts/{id}` | Delete account from DB |

### Banners

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/banners` | Filenames from MinIO (for dropdowns) |
| GET | `/api/banners/list` | Full banner records from DB |
| GET | `/api/banners/preview/{file_name}` | Stream file from MinIO (video → serves `preview/*.mp4`) |
| POST | `/api/banners/upload` | Upload file to MinIO + save record to DB |
| PATCH | `/api/banners/{id}` | Update name/note |
| DELETE | `/api/banners/{id}` | Delete from DB + remove file(s) from MinIO |

### Backgrounds

Same pattern as Banners — endpoints under `/api/backgrounds/`.

## Database Schema (PostgreSQL `main`)

### `jobs`
| Column | Type | Notes |
|--------|------|-------|
| `job_id` | VARCHAR(10) PK | 10 alphanumeric chars |
| `youtube_url` | TEXT | |
| `is_vertical` | BOOLEAN | |
| `is_subtitles` | BOOLEAN | |
| `is_slice` | BOOLEAN | |
| `is_background` | BOOLEAN | |
| `is_circle` | BOOLEAN | |
| `trace` | TEXT | |
| `status` | VARCHAR(50) | `pending`, `processing`, `done`, `failed` |

### `sub_job`
| Column | Type | Notes |
|--------|------|-------|
| `sub_job_id` | VARCHAR(21) PK | `{job_id}-{10chars}` |
| `job_id` | VARCHAR(10) FK → jobs | CASCADE DELETE |
| `banner_id` | INTEGER FK → banners | nullable |
| `account_ids` | INTEGER[] | array of account IDs |
| `platform` | VARCHAR(50) | `youtube`, `tiktok`, `instagram` |
| `publish_datetime` | TIMESTAMP WITH TIME ZONE | nullable |

### `jobs_metadata`
| Column | Type |
|--------|------|
| `job_id` | VARCHAR(10) FK PK |
| `create_datetime` | TIMESTAMP WITH TIME ZONE |
| `last_change_datetime` | TIMESTAMP WITH TIME ZONE |

### `accounts`
| Column | Type | Notes |
|--------|------|-------|
| `id` | SERIAL PK | |
| `platform` | VARCHAR(50) | UNIQUE with name |
| `name` | VARCHAR(255) | UNIQUE with platform |
| `url` | TEXT | nullable |
| `note` | TEXT | nullable |
| `bucket` | VARCHAR(255) | MinIO bucket for auth file |
| `file_name` | TEXT | path within bucket |

### `banners` / `backgrounds`
| Column | Type |
|--------|------|
| `id` | SERIAL PK |
| `name` | VARCHAR(255) UNIQUE |
| `note` | TEXT |
| `bucket` | VARCHAR(255) |
| `file_name` | TEXT |

## MinIO Storage

| Bucket | Contents |
|--------|---------|
| `banners` | Original banner files + `preview/{base}.mp4` for video files |
| `backgrounds` | Original background files + `preview/{base}.mp4` for video files |
| `cookies` | Account auth files at `{platform}/{name}/{filename}` |

### Video Preview Generation

On upload of any video file (`.mov`, `.avi`, `.mkv`, `.webm`, `.flv`, `.wmv`, `.m4v`, `.ts`):
1. Original file saved to MinIO as-is
2. ffmpeg remux attempted with `-c copy -movflags +faststart` (no re-encoding, instant)
3. If codec incompatible with MP4 (e.g. ProRes) → fallback to `-c:v libx264 -preset ultrafast -crf 23`
4. MP4 saved to `preview/{base}.mp4` in same bucket

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
| `addBackground` | boolean (disabled when makeVertical=false) |
| `shape` | `"circle" \| "rectangle"` |
| `backgroundName` | string |
| `youtubeAccounts` | string[] |
| `tiktokAccounts` | string[] |
| `instagramAccounts` | string[] |
| `schedules` | `Partial<Record<Platform, PublishSchedule>>` |
| `banners` | `BannerConfig[]` |

### AssetRecord (banners/backgrounds)
| Field | Type |
|-------|------|
| `id` | number |
| `name` | string |
| `note` | string \| null |
| `bucket` | string \| null |
| `file_name` | string \| null |

### JobData / SubJobData
Returned by `/api/jobs`. Sub-jobs include resolved `account_names` and `banner_name`.

## Key Behaviors

- "Add background?" toggle disabled when "Make vertical?" is off
- Per-platform publish schedule visible when ≥1 account selected for that platform
- Each banner in VideoCard has its own account selection and schedule
- Upload pages (banners/backgrounds) use localStorage for draft state; file objects are not persisted
- View pages stream file previews through the backend (browser never hits MinIO directly)
- Video preview box: `object-contain` — scales to fit without cropping
- Hover on video preview → play; mouse out → pause + reset to start

## TODO

- Real job submission to RabbitMQ
- Auth
- Deploy to k3s / prod nginx
