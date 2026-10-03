# CLAUDE.md — Terex (VE-service)

## Overview

Terex is the video-engine service of LT-Inc: it monitors YouTube channels, downloads new videos, transcribes them, finds highlights, renders vertical clips (highlights mode or full video by intervals) and publishes them to our own channels (by-approve or auto-approve). The admin panel (FE) manages channels, jobs, accounts, banners, backgrounds and clip approval.

## Architecture

```
VPS                                         GPU server
┌───────────────┐  ┌──────────────────┐     ┌──────────────────────────┐
│ FE (Next.js)  │→ │ main-be (Go)     │     │ gpu-be (Go)              │
└───────────────┘  │ 1 cron monitor   │ MQ  │ 2 preproc  (yt-dlp)      │
┌───────────────┐  │ 5 pub manager    │ ⇄   │ 3 candidate(whisper+LLM) │
│ PostgreSQL    │← │ REST API         │gRPC │ 4 renderer (ffmpeg)      │
└───────────────┘  └──────────────────┘ ──→ │ 6 publisher (STUB)       │
                                            │ storage gateway          │
                                            └───────────┬──────────────┘
                                                    ┌───▼────┐
                                                    │ MinIO  │ (only file storage)
                                                    └────────┘
```

- **main-be never touches files.** Postgres stores only `bucket` + `object` refs. Previews/uploads use presigned URLs issued by gpu-be over gRPC; the browser talks to MinIO directly (`S3_PUBLIC_ENDPOINT` must be browser-reachable). Account auth files go to gpu-be via gRPC (`PutAuthFile`).
- **RabbitMQ** = jobs and results (durable queues, contract in `proto/msg`). **gRPC** = synchronous requests (`proto/gpu/v1/gpu.proto`).
- **Strict 3 layers** in both services: `transport → service → repository`, interfaces and entities in `domain/`. No SQL/exec/AMQP in `service`, no business logic in `transport`.

### Queues
`gpu.process_video` (Main→GPU) → preproc → `gpu.candidate` → candidate → `gpu.render` → renderer → `main.clips_ready` (GPU→Main).
`main.job_status` (stage events), `gpu.publish_clip` (Main→GPU) → `main.publish_result`.

### Flow
1. main-be cron polls YouTube RSS of enabled channels; new videos (after the baseline taken when the channel was added) create a job from the channel template.
2. gpu-be: download + 480p/audio proxies → whisper (word timestamps, SRT/ASS) → intervals → vertical/subtitles/banner variants → clips in MinIO. Intervals: `full` = segments grouped by `min_video_duration`; `highlights` = see below.
3. main-be stores clips. `auto_approve` jobs schedule publications immediately; `by_approve` waits for `POST /api/clips/{id}/approve` (FE page *Clips*).
4. Scheduler (every `PUBLISH_INTERVAL`) sends due publications to `gpu.publish_clip`. Publisher is a stub that acknowledges.

## Layout

```
terex/
  proto/              # shared Go module: gpu/v1 gRPC (generated), msg (queue contract), amqpx (RabbitMQ client)
  main-be/            # image terex-main-be (binary cmd/main-be); migrations/ (goose, embedded)
    internal/{transport,service,repository,domain,config}
  gpu-be/             # image terex-gpu-be (binary cmd/gpu-be)
    internal/{transport,service,repository,domain,config}
  FE/                 # Next.js 15 + TS + Tailwind
  docker-compose.yml  # local stack; docker-compose.gpu.yml = overlay for the GPU host
  go.work             # ties proto + main-be + gpu-be
```

## Running locally

```bash
cd terex
docker compose up --build            # FE http://localhost:3000, API :8000, RabbitMQ UI :15672, MinIO UI :9001
# on the GPU host:
GPU_RUNTIME_BASE=nvidia/cuda:12.4.1-runtime-ubuntu22.04 USE_NVENC=true \
  docker compose -f docker-compose.yml -f docker-compose.gpu.yml up --build
```

Set `OPENROUTER_API_KEY` for highlights mode. Locally gpu-be runs on CPU (`ubuntu:22.04`, whisper `base`).

Tests: `cd main-be && go test ./...`, `cd gpu-be && go test ./...`. Regenerate gRPC: `cd proto && buf generate` (needs `buf`, `protoc-gen-go`, `protoc-gen-go-grpc` in PATH).

## Highlights mode (how clips are chosen)

1. **Sentences**: whisper words are grouped into sentences (`.!?…`, a ≥1 s pause, or a 25 s run-on cap). Clip bounds always come from sentence = word timestamps, so a cut can never land mid-word.
2. **Hints per sentence**, computed against the video itself. Empty tags are never written; videos with < 8 sentences get none. `PROMPT_TAGS` selects which hints the LLM sees.
   - `energy:high` — mean loudness (RMS dBFS, 0.4 s windows, ffmpeg 16 kHz mono PCM) ≥ the video's p90 and strictly above p60.
   - `energy:spike` — the sentence is ≥ 6 dB louder than the **median of the previous 45 s** of sentences (needs ≥ 3 earlier sentences): "the speaker raised their voice", works in a loud video too.
   - YAMNet audio events (one pass of `gpu-be/tools/yamnet.py`, `YAMNET_CMD`; probability ≥ max(video p90, floor)): `laugh` (laughter/applause/cheering within 0.3 s before … 2 s after the sentence end, floor 0.30), `shout` (shout/yell/scream during the sentence, floor 0.25), `gasp` (gasp 0.3 s before … 1.5 s after the end, floor 0.25), `music` (music/singing under the speech, mean ≥ 0.5; skipped entirely if it covers > 60 % of the video, since then it is background, not a signal). YAMNet also yields a `silence` score; it is parsed but not used as a tag. If YAMNet is unavailable the hints are skipped with a warning.
   - `fast` — words/sec ≥ p90; `pause_before` — gap ≥ 0.7 s and ≥ p90.
3. **LLM** (OpenRouter, prompt in `gpu-be/internal/repository/llm/llm.go`) gets the numbered sentences with hints and returns clips as `{from, to}` **sentence ids** + title + hashtags. It chooses the length itself (complete thought / setup + payoff) within `CLIP_MIN_SEC`..`CLIP_MAX_SEC` (15..180 s) and at most `MAX_CLIPS`.
   Reasoning models (e.g. `stealth/space-bunny-alpha`, reasoning is mandatory and defaults to `max`) can think for minutes while OpenRouter streams blank keep-alive lines — set `LLM_REASONING_EFFORT=low` (and `LLM_TIMEOUT` if needed); `LLM_CHUNK_CHARS` can be raised for large-context models.
   Resilience: transient failures (empty/whitespace-only body — OpenRouter keep-alive without a result, invalid/truncated JSON, HTTP 408/429/5xx, provider timeout errors) are retried up to 3 times with 3 s/6 s pauses; permanent ones (401, 400 "model does not support json_object", …) fail at once with the provider's message. A transcript over ~80k prompt characters (~25k tokens) is split into consecutive chunks (one request each, picks interleaved by rank), so a long video is not one giant request.
4. **ResolvePicks** (`service/candidate/picks.go`) validates the answer: bad ids dropped, length forced into the limits by trimming/extending whole sentences, overlaps dropped (LLM order = quality), timestamps = word bounds + a small pad that never reaches the neighbouring sentence (`laugh` keeps up to 1.5 s of the reaction). No valid clips → the stage fails (`fail_place=candidate`).

`min_video_duration` applies to `full` mode only.

## Logs and tracing (Tempo)

- `proto/telemetry` sets up OpenTelemetry (OTLP gRPC → Tempo) and `slog`. Tracing is **off** unless `OTEL_EXPORTER_OTLP_ENDPOINT` is set (compose default `http://tempo:4317`; point it at the infra Tempo in prod).
- **One trace per job**: HTTP request / cron tick → `job.create` → `publish gpu.process_video` → `consume` → `preproc/candidate/renderer handle` → child spans for `yt-dlp`, `ffmpeg <op>`, `whisper`, `llm`, `minio upload/download` → `publish main.clips_ready` → `consume` → SQL. Trace context travels in AMQP headers (`amqpx`), gRPC uses otelgrpc, HTTP uses otelhttp, SQL uses otelpgx.
- The job's trace id is stored in `jobs.trace` (FE "Trace" field) — paste it into Grafana → Explore → Tempo.
- Logs: `slog` with `trace_id`/`span_id` when logged with a context. `LOG_FORMAT=json` (Loki/Promtail), `LOG_LEVEL=debug|info|warn|error`. gpu-be logs each stage transition (`job stage`), external tool durations and failures; main-be logs requests, job creation, clips, approvals, publish results.
- Observability starts with the stack (no profile): **Tempo** (traces), **Loki + Promtail** (logs from all containers of the `terex` compose project via the Docker socket; slog logfmt is parsed into the `level` label), **Grafana** http://localhost:3001 (anonymous admin, local only).
- Grafana provisioning lives in `docker/grafana/provisioning` (folder *Terex*): `terex-overview` (error/warn counters, errors by service, ERROR logs, failed job stages, failed traces via TraceQL, recent traces), `terex-main-be`, `terex-gpu-be`, `terex-fe` (INFO/ERROR/WARNING/Full logs panels, same layout as `n8n/docker/grafana`). Clicking `trace_id` in a log line opens the trace in Tempo; a span links back to its logs.
- Prod: the infra Grafana/Loki/Tempo from `n8n/docker` are used instead — set `OTEL_EXPORTER_OTLP_ENDPOINT` to the infra Tempo; the Loki datasource regex there must match `trace_id=<32 hex>` (the old one expects `[trace:<id>]`).

## REST API (main-be, prefix `/api`)

| Area | Endpoints |
|------|-----------|
| Jobs | `GET/POST /jobs`, `PATCH/DELETE /jobs/{id}`, `PATCH/DELETE /sub_jobs/{id}` |
| Accounts | `GET /accounts` (names per platform), `GET /accounts/list`, `POST /accounts/create` (multipart, `auth_file`), `PATCH/DELETE /accounts/{id}`, `GET /accounts/{id}/clips\|scheduled\|published` |
| Banners / backgrounds | `GET /{banners\|backgrounds}` (names), `/list` (with `preview_url`), `POST /presign` → `{url,bucket,object}`, `POST /` register after the browser PUT, `PATCH/DELETE /{id}` |
| Channels | `GET/POST /channels`, `PATCH/DELETE /channels/{id}` (`approve_mode`, `enabled`, `spec` = job template) |
| Clips | `GET /clips?approval=pending\|approved\|rejected`, `POST /clips/{id}/approve\|reject` |
| Health | `GET /healthz` |

## Database (PostgreSQL `main`, goose migration `main-be/migrations/001_init.sql`)

`accounts`, `banners`, `backgrounds` (name, note, bucket, file_name) · `channels` (yt_channel_id, approve_mode, enabled, spec jsonb) · `seen_videos` · `jobs` (slice_mode `highlights|full`, approve_mode, status `pending|queued|processing|done|failed`, fail_place) · `sub_jobs` (banner variant + accounts + schedule + step) · `clips` (bucket/object, banner_object, idx, approval) · `publications` (clip × account, status `scheduled|sent|published|failed`).

Publish time of a clip = `sub_job.publish_datetime` (or approval time) + `clip.idx × publish_step` minutes. Banner variants: each distinct sub-job banner is rendered as its own clip set; a clip is published by the sub-job whose banner matches.

## Environment

| Variable | Service | Default |
|----------|---------|---------|
| `DATABASE_URL` | main-be | required |
| `RABBITMQ_URL` | both | `amqp://guest:guest@rabbitmq:5672/` |
| `GPU_GRPC_ADDR` | main-be | `gpu-be:9090` |
| `HTTP_ADDR`, `MONITOR_INTERVAL`, `PUBLISH_INTERVAL` | main-be | `:8000`, `5m`, `30s` |
| `GRPC_ADDR` | gpu-be | `:9090` |
| `S3_ENDPOINT`, `S3_PUBLIC_ENDPOINT`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_USE_SSL` | gpu-be | `minio:9000`, `localhost:9000` |
| `STATE_DIR`, `TMP_DIR` | gpu-be | `/data/state`, `/tmp/terex` |
| `WHISPER_BIN`, `WHISPER_MODEL` | gpu-be | `whisper`, `base` |
| `OPENROUTER_API_KEY`, `OPENROUTER_MODEL` | gpu-be | — , `openai/gpt-4o-mini` |
| `LLM_REASONING_EFFORT`, `LLM_TIMEOUT`, `LLM_CHUNK_CHARS` | gpu-be | empty (model default), `240s`, `80000` |
| `WORKERS_PER_MODULE`, `USE_NVENC` | gpu-be | `1`, `false` |
| `CLIP_MIN_SEC`, `CLIP_MAX_SEC`, `MAX_CLIPS`, `PROMPT_TAGS`, `YAMNET_CMD` | gpu-be | `15`, `180`, `12`, `energy:high,energy:spike,laugh,shout,gasp,music,fast,pause_before`, `python3 /opt/terex/yamnet.py` |
| `OTEL_SERVICE_NAME`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `LOG_FORMAT`, `LOG_LEVEL` | both | service name, empty (off), `text`, `info` |
| `BACKEND_URL` | FE | `http://localhost:8000` |

## FE notes

- Sidebar groups: Jobs, Accounts, Banners, Backgrounds, **Channels**, **Clips**, plus external service links.
- Upload pages (banners/backgrounds) do presign → browser `PUT` to MinIO → register; they keep a localStorage draft.
- Removed with the Go rewrite: warmup jobs and "capture auth via noVNC" (no backend yet).
- FE → MinIO needs CORS (MinIO default allows all origins).

## Known gaps / TODO

- YouTube publishing uses the PostgreSQL workflow coordinator, confirmed outbox and dedicated GPU workers. See `docs/PUBLISHING.ru.md` for OAuth setup, recovery and provider extension. TikTok/Instagram adapters remain future work.
- k8s manifests and wiki in `docs/` still describe the old Python BE and need updating when deploying the new stack (readiness probe → `GET /healthz`, new env vars, gpu-be runs on the GPU host).
- Chunked highlights cannot produce a clip that spans a chunk boundary.
- Renderer processes the whole video before cutting (as the old pipeline did); cutting first would save GPU time for highlights mode but breaks subtitle timing.
- Auth.
