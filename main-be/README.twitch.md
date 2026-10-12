# Twitch MVP backend

The collector discovers and downloads public clips. Terex owns project settings,
immutable imports, revisions, the review queue, assembly drafts and publication.
Twitch imports bypass the YouTube download and LLM highlight pipeline. A completed
render auto-approves that exact Twitch revision after source checks. Scheduling,
flow enablement and publication holds remain separate controls; legacy YouTube
jobs retain manual approval. See [the operator guide](../docs/TWITCH-PREPARATION.ru.md).

Windowed discovery, measured growth, durable verification retries and the cached
staging dashboard are documented in [recovery and monitoring](../docs/TWITCH-RECOVERY-GROWTH.ru.md).

Collector-only API additions: `GET /api/twitch/flows/{id}/observation-candidates`
(up to 200 candidates for metadata refresh), `POST /api/imports/{id}/revalidate`
(epoch/sequence/generation/failure-token fenced), and fenced import aborts.
These capabilities do not allow changing flows, accounts or publications.

## Configuration

The executable requires `DATABASE_URL`, `ADMIN_USERNAME`, `ADMIN_PASSWORD` and
`TEREX_SERVICE_TOKEN`. `COLLECTOR_URL` is the collector origin (without `/api`),
and `COLLECTOR_SERVICE_TOKEN` authenticates the proxy to that service. The collector
uses `TEREX_SERVICE_TOKEN` to read settings and reserve/complete imports. Never
expose service tokens to browser JavaScript. Use HTTPS for the public operator UI.

In staging, Traefik performs the human-facing Basic Auth. The FE receives its
authenticated-user marker from that middleware, then uses `ADMIN_USERNAME` and
`ADMIN_PASSWORD` only as a server-to-server Basic credential when it calls main-be.
main-be keeps this check so direct ClusterIP and port-forward traffic cannot bypass
the API boundary. `TEREX_SERVICE_TOKEN` remains a separate, narrow collector token;
it must never become an operator credential. Local Compose uses `FE_AUTH_MODE=basic`
and keeps the browser Basic prompt. The OAuth callback is the one public exception:
only `GET /api/accounts/oauth/callback` bypasses operator authentication, and its
single-use, short-lived state remains mandatory.

Settings are stored in `twitch_project_settings`, default to disabled and require
one selected YouTube account before enabling. The HTTP API uses operator Basic
authentication; the narrow Bearer token cannot approve drafts, change settings,
manage accounts or publish. The OAuth callback relies on the existing single-use
state check. `/healthz` is public.

## HTTP routes

| Route | Behavior |
| --- | --- |
| `GET/PUT /api/twitch/settings` | Authoritative complete settings object |
| `GET/POST/PUT /api/twitch/channels` | Proxy to collector `/api/channels` |
| `PATCH/DELETE /api/twitch/channels/{id}` | Pause or remove one source |
| `GET /api/twitch/clips`, `GET /api/twitch/status`, `POST /api/twitch/check` | Collector proxy |
| `GET /api/imports?pending=true` | Review backlog, excluding failed/rejected sources |
| `GET /api/imports?state=ready` | Sources available to the compilation builder |
| `POST /api/imports` | Idempotent reservation and a renewable staging upload URL |
| `POST /api/imports/{id}/complete` | Verify SHA256/media, create draft and enqueue initial render atomically |
| `GET/PATCH /api/imports/{id}` | Read provenance or set `rejected` |
| `GET/POST /api/compilations` | List or create a draft from pinned source revisions/ranges |
| `GET/PUT /api/compilations/{id}` | Read or revise assembly; PUT requires `expected_version` |
| `POST /api/compilations/auto` | Returns creation/blocking details; flow mode targets the nearest available publication date |
| `GET /api/twitch/flows/{id}/diagnostics` | Read-only readiness, reasons, shared quota and collector state |
| `POST /api/twitch/collector/admissions` | Narrow collector token: durable download grants |
| `POST /api/twitch/reset/preview`, `POST /api/twitch/reset`, `GET /api/twitch/reset` | Operator-only full Twitch reset with preview and durable progress |

Request contracts are in `proto/twitch`; assembly and ingestion contracts are in
`proto/workflow`. Complete accepts at most 512 MiB and a valid SHA256. GPU ingestion
validates the actual file and returns the immutable verified object; metadata alone
cannot create a publishable source. Import ID/clip ID/idempotency key and verified
media SHA256 prevent repeated imports. Shorts start with source title plus streamer
login and source URL in the description.

## Compilation and publication

Compilation v1 uses 3–10 unique ready imports, pinned source revisions/ranges,
`landscape_1080p30`, one second between clips and no music. The renderer materializes
one immutable original and uses the existing revision renderer. The coordinator
saves normalized duration and output only under the valid render lease. Reorder,
range edits and metadata edits create another revision; Twitch auto-approval waits
for that revision to be ready and valid;
generic revision save/restore retains dependency records. Source rejection holds
dependent drafts and prevents later approval.

The flow scheduler prepares the nearest publication as soon as enough eligible
ready sources exist, using the full admissible pool, views and configured balance.
It creates at most one automatic draft per target publication local date.
Pending downloads do not count toward ready duration. A configurable soft budget
defaults to 70% for compilations, with shadow admission before enforcement.

The single collector download worker maintains a buffer of at most ten returned
grants per admissions request (or the smaller configured daily limit), rather
than requesting the entire daily quota in one transaction. The daily quota and
format allocation remain in main-be. Discovery windows and their saved cursors
are independent of this executable buffer. Failed allocation requests back off
15/30/60 seconds; `COLLECTOR_ADMISSIONS_TIMEOUT` defaults to 15 seconds.

Unavailable or stale storage pressure prevents new grants before candidate
planning; existing grants are not revoked by this check. If the GPU/MinIO node
is offline, discovered sources and queued render/publication work remain saved,
but downloads, video processing and publication wait for its return. Increasing
the diagnostics budget does not make the storage or video worker available.

The download/backlog limits are checked transactionally. Publication calendar
limits use the project timezone and one configured YouTube account: approvals,
date changes, publish-now and target edits share a transaction lock. Runtime limits
also count overdue work against today's upload budget; continuation of an already
started upload and cancellation remain available. Legacy YouTube jobs retain their
existing target behavior.

## Verification

`go test ./main-be/...` and `go vet ./main-be/...` run from the repository root.
Set `TEST_DATABASE_URL` to a disposable PostgreSQL database to execute repository
integration checks; each check creates its own schema. They cover immutable imports,
hash deduplication, pinned assembly changes, generic edit/restore dependencies,
manual approval, concurrent calendar limits, runtime backlog limits and failure/retry
visibility. HTTP tests check operator/service isolation and the real collector route
shape. No real Twitch download or YouTube publication is implied by these checks.

This MVP does not grant content rights or automate legal clearance. The operator
remains responsible for the selected sources and publication settings.
