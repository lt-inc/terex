# Twitch collector

Standalone service with its own `twitch_collector` PostgreSQL schema. It fetches public clip metadata with a Twitch application token, downloads selected public clips with pinned yt-dlp, validates them with ffprobe and imports originals into Terex using temporary upload URLs. Streamer login is not part of this workflow. Uploading to YouTube belongs to Terex.

For the existing k3s deployment, see [the staging setup guide](../docs/TWITCH-K8S.ru.md) and [the short operator guide](../docs/TWITCH-COLLECTOR.ru.md). k3s Terraform owns the collector workload, restricted DB bootstrap Job and Infisical Secret wiring. Terex CI publishes four images and updates only existing Deployment images.

Collection starts disabled. Enable the service and create an active publication flow with a confirmed YouTube destination before using Check now. Changing the polling interval alone does not enable collection. See [publication flows and staging rollout](../docs/TWITCH-FLOWS.ru.md). The panel displays the current blocking reason. Calendar, diagnostics, clip lists and editors load on opening or user actions; use their Refresh button for later updates. Failed refreshes preserve the last successful data. Only an active full reset polls its progress automatically.

Collector control configuration still refreshes independently every five seconds,
with one request at a time and an eight-second response deadline.
It uses a lean backend read without UI counters or calendar scans. A failed request
reports a specific timeout, DNS or connection error with endpoint and elapsed time;
it does not mean an immediate pause while the last confirmed snapshot is fresh.
Without a successful refresh for 30 seconds, new operations stop until recovery.

The service reads shared settings and flows from `/api/twitch/collector-config`. Each flow watches selected broadcasters or one Twitch category. A language-filtered flow requires the clip's own language metadata; a channel hint is not confirmation. Helix discovery uses equal windows of at most eight hours, 100-clip pages and adaptive splitting down to 15 minutes. Progress is durable; partial coverage is explained in diagnostics. A shared 120-request cycle budget, 5 requests/second and a 50-request rate-limit reserve cover all Helix endpoints. Fresh windows and known-clip refresh share half the budget; durable history gets the other half with unused budget transferred. Defaults remain 72 hours, 100 views, minimum age 60 minutes, duration 5–60 seconds and checks every 144 minutes; the accepted duration ceiling is 180 seconds. Daily reservations use the shared timezone. The global daily download cap is disabled by default (`daily_downloads=0`); optional positive caps remain supported. The per-broadcaster limit defaults to two, and backlog, calendar and storage protection remain active. The executable grant buffer stays ten even without a daily cap. In `shadow`, the previous round-robin selection remains active; `enforce` executes durable main-be grants with a configurable compilation budget (70% by default). The channel catalogue has no ten-channel cap. Observations attach ready shared originals before new downloads; canonical SHA identity and per-destination format reuse are enforced by Terex. See [recovery, growth ranking and monitoring](../docs/TWITCH-RECOVERY-GROWTH.ru.md) and [readiness, allocation and reset](../docs/TWITCH-PREPARATION.ru.md).

The queue is durable and has three dispatch states: `waiting_assignment` (no current coordinator permission), `authorized` (may download) and `verification_pending` (PUT finished or its response is uncertain). One download worker and one independent completion worker consume their saved lanes immediately; neither waits for the next polling tick after finishing a task. PostgreSQL transactions serialize reservations; task leases expire after 30 seconds and renew every five seconds. Heartbeats use the cached control snapshot and short lease queries, never a full flow synchronization. Temporary renewal errors retry within the last confirmed lease; reset acknowledgements wait for all registered work to actually stop.

The import ID, immutable reservation and generation are saved before downloading, together with a `reserved` marker proving PUT has not started. SHA/size and the `uploading` marker are persisted before PUT. This closes both restart gaps: a known pre-PUT reservation resumes downloading; an uncertain PUT resumes exact-generation verification without yt-dlp or another PUT. Historical generation-only rows without upload metadata stay quarantined and poll status; they are never blindly downloaded again. Missing metadata is explained as `upload_metadata_missing` and may need operator recovery.

Three physical download attempts exhaust the download budget. Temporary download failures retry after 5 then 30 minutes. Verification has a separate saved counter and retries after 10, 30, 60, 120 and 240 seconds, then at most every five minutes. Confirmed invalid media fails without retry; HTTP 202 alone never means a ready file. Only `media_state=ready` (or the legacy `state=ready` when media_state is absent) completes an import. A paused flow may drain an already uploaded file; Terex does not create new outputs for that flow. A global collector stop pauses both lanes. The backlog gate blocks only new physical sources; already admitted pre-PUT reservations and confirmed missing uploads may recover their existing slot. Metadata observation and uploaded-file verification do not wait for free backlog capacity.

One worker runs each download with a 120-second timeout and a guarded 512 MiB file limit. Linux process groups ensure subprocess termination. Child processes receive no Twitch secret, service tokens or database credentials. Only validated clip IDs are accepted and the download URL is always `https://clips.twitch.tv/<id>`. No cookies, authentication bypass or stealth browser is used. Raw downloader output and signed upload URLs are never returned in diagnostics. The Linux container is the production runtime; Windows cancellation kills the direct process only.

Required environment: `DATABASE_URL`, `TEREX_SERVICE_TOKEN`, `COLLECTOR_SERVICE_TOKEN`. `TEREX_URL` defaults to `http://main-be:8000`; `TEREX_BASE_URL` is a compatible alias. Metadata credentials: `TWITCH_CLIENT_ID`, `TWITCH_CLIENT_SECRET`; absent credentials leave the UI and health available with `credentials_ready=false`. Optional: `HTTP_ADDR` (default `:8080`), `TMP_DIR`, `YTDLP_BIN`, `FFPROBE_BIN`. The collector needs no object-storage master credentials. Build the Dockerfile from the repository root.

Discovery controls: `TWITCH_DISCOVERY_MAX_REQUESTS=120`, `TWITCH_DISCOVERY_RPS=5`,
`TWITCH_DISCOVERY_RATE_REMAINING_FLOOR=50`. Per-flow `ranking_mode` is `views`
(default) or `growth`; main-be owns the shared observation history and ranking.
After a successful PUT the persisted upload marker allows verification retries
without yt-dlp or another PUT. Version-change failures are revalidated through
main-be before the regular allocator can issue a newer admission.

Control-plane intervals: `COLLECTOR_CONFIG_INTERVAL=5s` (request timeout 8s),
`COLLECTOR_CONFIG_MAX_AGE=30s`, `COLLECTOR_PRESSURE_INTERVAL=15s`,
`COLLECTOR_ADMISSIONS_INTERVAL=1m`, `COLLECTOR_ELIGIBILITY_INTERVAL=1m`.
`COLLECTOR_ADMISSIONS_TIMEOUT=15s` bounds allocation requests. Failed requests
retry after 15, 30 and then 60 seconds, avoiding a new allocation transaction on
every five-second control refresh. An expired reconciliation pass does not start
another allocation; the 30-second config freshness gate and reset fencing remain.
One reconciler writes only changed settings/exclusions; runtime counters do not
invalidate the settings fingerprint. Age-dependent eligibility refreshes every
minute and changes only rows whose eligibility changed. Discovery observations
are fenced by the current flow version. The read-only pressure endpoint checks
all persisted assignments in batches of at most 200 and reports explicit
valid/consumed/absent/expired/mismatch/revoked states: omission from a bounded
admissions page never revokes a grant. Missing/stale pressure or required storage
telemetry closes new downloads, while the completion lane continues independently.

Status includes `waiting_assignment`, `verification_pending`, `verifying`,
`oldest_authorized_at` and `oldest_verification_at`; `queued` now counts authorized
downloads. Clip statuses include dispatch state and a specific waiting reason.
Logs `collector assignments checked`, `collector coordination reconciled` and
`collector upload queued for verification` explain allocation, work and handoff.

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
