# Twitch collector

Standalone service with its own `twitch_collector` PostgreSQL schema. It fetches public clip metadata with a Twitch application token, downloads selected public clips with pinned yt-dlp, validates them with ffprobe and imports originals into Terex using temporary upload URLs. Streamer login is not part of this workflow. Uploading to YouTube belongs to Terex.

For the existing k3s deployment, see [the staging setup guide](../docs/TWITCH-K8S.ru.md) and [the short operator guide](../docs/TWITCH-COLLECTOR.ru.md). k3s Terraform owns the collector workload, restricted DB bootstrap Job and Infisical Secret wiring. Terex CI publishes four images and updates only existing Deployment images.

Collection starts disabled. Enable the service and create an active publication flow with a confirmed YouTube destination before using Check now. Changing the polling interval alone does not enable collection. See [publication flows and staging rollout](../docs/TWITCH-FLOWS.ru.md). The panel displays the current blocking reason and refreshes visible results every ten seconds.

The service reads shared settings and flows from `/api/twitch/collector-config`. Each flow watches selected broadcasters or one Twitch category. A language-filtered flow requires the clip's own language metadata; a channel hint is not confirmation. Helix discovery uses equal windows of at most eight hours, 100-clip pages and adaptive splitting down to 15 minutes. Progress is durable; partial coverage is explained in diagnostics. A shared 120-request cycle budget, 5 requests/second and a 50-request rate-limit reserve cover all Helix endpoints. Fresh windows and known-clip refresh share half the budget; durable history gets the other half with unused budget transferred. Defaults remain 72 hours, 100 views, minimum age 60 minutes, duration 5–60 seconds and checks every 144 minutes; the accepted duration ceiling is 180 seconds. Daily reservations use the shared timezone, maximum 10 new clips and 2 per broadcaster. In `shadow`, the previous round-robin selection remains active; `enforce` executes durable main-be grants with a configurable compilation budget (70% by default). The channel catalogue has no ten-channel cap. Observations attach ready shared originals before new downloads; canonical SHA identity and per-destination format reuse are enforced by Terex. See [recovery, growth ranking and monitoring](../docs/TWITCH-RECOVERY-GROWTH.ru.md) and [readiness, allocation and reset](../docs/TWITCH-PREPARATION.ru.md).

The queue is durable. PostgreSQL transactions serialize reservations; task leases expire after 30 seconds and renew every five seconds in the flow worker. The import ID, reservation and generation are saved before downloading. Token and expiry fencing protects completion and recovery after a restart; three claimed attempts exhaust the budget. Temporary network failures retry after 5 then 30 minutes. Deleted/private/forbidden clips and invalid files fail without retry. A flow pause keeps a shared download running when another eligible active flow consumes it; otherwise the task is deferred without spending an attempt. The backlog gate stops new downloads at the shared limit (40 by default), but metadata observations can still attach existing media.

One worker runs each download with a 120-second timeout and a guarded 512 MiB file limit. Linux process groups ensure subprocess termination. Child processes receive no Twitch secret, service tokens or database credentials. Only validated clip IDs are accepted and the download URL is always `https://clips.twitch.tv/<id>`. No cookies, authentication bypass or stealth browser is used. Raw downloader output and signed upload URLs are never returned in diagnostics. The Linux container is the production runtime; Windows cancellation kills the direct process only.

Required environment: `DATABASE_URL`, `TEREX_SERVICE_TOKEN`, `COLLECTOR_SERVICE_TOKEN`. `TEREX_URL` defaults to `http://main-be:8000`; `TEREX_BASE_URL` is a compatible alias. Metadata credentials: `TWITCH_CLIENT_ID`, `TWITCH_CLIENT_SECRET`; absent credentials leave the UI and health available with `credentials_ready=false`. Optional: `HTTP_ADDR` (default `:8080`), `TMP_DIR`, `YTDLP_BIN`, `FFPROBE_BIN`. The collector needs no object-storage master credentials. Build the Dockerfile from the repository root.

Discovery controls: `TWITCH_DISCOVERY_MAX_REQUESTS=120`, `TWITCH_DISCOVERY_RPS=5`,
`TWITCH_DISCOVERY_RATE_REMAINING_FLOOR=50`. Per-flow `ranking_mode` is `views`
(default) or `growth`; main-be owns the shared observation history and ranking.
After a successful PUT the persisted upload marker allows verification retries
without yt-dlp or another PUT. Version-change failures are revalidated through
main-be before the regular allocator can issue a newer admission.

`GET /health` is public. All `/api/*` endpoints require `Authorization: Bearer <COLLECTOR_SERVICE_TOKEN>`. Backend admin routes proxy to these internal endpoints:

- `GET /api/channels`: channel array; `POST` with `{ "input": "login or Twitch channel URL" }` returns the channel.
- `PATCH /api/channels/{id}` with `{ "paused": true }`; `DELETE` archives it.
- `GET /api/clips?limit=100&state=imported`: metadata and durable processing state.
- `GET /api/status`: counters, pause/check state and credential readiness.
- `POST /api/check`: asynchronous check, HTTP 202; concurrent checks return 409.
- `POST /api/flows/{id}/check`: check one flow; `GET /api/categories?q=...`: category search.
- `GET /api/flows/{id}/clip-status?source_ids=...`: durable state for at most 50 visible sources, without signed media URLs.

Run `go test ./...`. PostgreSQL integration tests additionally require an explicitly provided disposable `COLLECTOR_TEST_DATABASE_URL`. They drop the collector schema in that test database; never point them at a production database. Tests use synthetic media and HTTP servers; they do not download Twitch clips or upload to YouTube.

Database startup retries connection checks for up to `DB_STARTUP_TIMEOUT`
(default `30s`), then applies migrations once. Invalid credentials or
configuration fail immediately. Pool defaults and recovery diagnostics are
documented in [POSTGRES-RECOVERY.ru.md](../docs/POSTGRES-RECOVERY.ru.md).

The clip downloader remains dependent on Twitch's public player implementation and yt-dlp maintenance. Technical availability does not grant publication rights; the previously accepted content permissions and review workflow still apply.
