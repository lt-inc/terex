# Twitch collector

Standalone service with its own `twitch_collector` PostgreSQL schema. It fetches public clip metadata with a Twitch application token, downloads selected public clips with pinned yt-dlp, validates them with ffprobe and imports originals into Terex using temporary upload URLs. Streamer login is not part of this workflow. Uploading to YouTube belongs to Terex.

For the existing k3s deployment, see [the staging setup guide](../docs/TWITCH-K8S.ru.md). The shared build/deploy workflow publishes the collector image, provisions its restricted DB principal with a Job, connects Secrets and checks all four application rollouts.

The service reads administrator settings from `/api/twitch/settings`. Defaults: top 100 clips created in the previous 72 hours, at least 100 views and 60 minutes old, duration 5–60 seconds, check every 144 minutes. Daily reservations use midnight in the configured timezone, maximum 10 new clips and 2 per channel. Round-robin choices give every channel a first candidate before a second. Exact IDs, intersecting known VOD intervals and hashes of previously imported originals prevent duplicates. At most 10 nonarchived channels can be tracked, including paused channels.

The queue is durable. PostgreSQL transactions serialize reservations; task leases expire after 30 seconds and renew every 2 seconds. Token and expiry fencing protects renewal, completion and failure. An expired lease can be reclaimed after a restart; three claimed attempts exhaust the budget. Temporary network failures retry after 5 then 30 minutes; HTTP 429 waits at least 30 minutes. Deleted/private/forbidden clips and invalid files fail without retry. A pause cancels the active process and returns the task to the queue without consuming an attempt. Settings and channel pause are checked every 2 seconds while processing. The backlog gate stops reservations and downloads when pending Terex imports reach the configured limit (40 by default).

One worker runs each download with a 120-second timeout and a guarded 512 MiB file limit. Linux process groups ensure subprocess termination. Child processes receive no Twitch secret, service tokens or database credentials. Only validated clip IDs are accepted and the download URL is always `https://clips.twitch.tv/<id>`. No cookies, authentication bypass or stealth browser is used. Raw downloader output and signed upload URLs are never returned in diagnostics. The Linux container is the production runtime; Windows cancellation kills the direct process only.

Required environment: `DATABASE_URL`, `TEREX_SERVICE_TOKEN`, `COLLECTOR_SERVICE_TOKEN`. `TEREX_URL` defaults to `http://main-be:8000`; `TEREX_BASE_URL` is a compatible alias. Metadata credentials: `TWITCH_CLIENT_ID`, `TWITCH_CLIENT_SECRET`; absent credentials leave the UI and health available with `credentials_ready=false`. Optional: `HTTP_ADDR` (default `:8080`), `TMP_DIR`, `YTDLP_BIN`, `FFPROBE_BIN`. The collector needs no object-storage master credentials. Build the Dockerfile from the repository root.

`GET /health` is public. All `/api/*` endpoints require `Authorization: Bearer <COLLECTOR_SERVICE_TOKEN>`. Backend admin routes proxy to these internal endpoints:

- `GET /api/channels`: channel array; `POST` with `{ "input": "login or Twitch channel URL" }` returns the channel.
- `PATCH /api/channels/{id}` with `{ "paused": true }`; `DELETE` archives it.
- `GET /api/clips?limit=100&state=imported`: metadata and durable processing state.
- `GET /api/status`: counters, pause/check state and credential readiness.
- `POST /api/check`: asynchronous check, HTTP 202; concurrent checks return 409.

Run `go test ./...`. PostgreSQL integration tests additionally require an explicitly provided disposable `COLLECTOR_TEST_DATABASE_URL`. They drop the collector schema in that test database; never point them at a production database. Tests use synthetic media and HTTP servers; they do not download Twitch clips or upload to YouTube.

The clip downloader remains dependent on Twitch's public player implementation and yt-dlp maintenance. Technical availability does not grant publication rights; the previously accepted content permissions and review workflow still apply.
