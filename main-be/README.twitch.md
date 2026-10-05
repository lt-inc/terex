# Twitch MVP backend

The collector discovers and downloads public clips. Terex owns project settings,
immutable imports, revisions, the review queue, assembly drafts and publication.
Twitch imports bypass the YouTube download and LLM highlight pipeline. A completed
render is a draft: the existing operator approval action is required before upload.

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
| `POST /api/compilations/auto` | Returns `{created, compilation}`; applies configured local hour and once-per-day guard |

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
range edits and metadata edits create another revision and require approval again;
generic revision save/restore retains dependency records. Source rejection holds
dependent drafts and prevents later approval.

The minute scheduler creates at most one automatic draft per configured local
date after the configured hour, using the highest-view eligible clips from the
lookback window that have not appeared in an earlier compilation. Fewer than the
minimum clips means no draft. It never approves the result.

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
remains responsible for the selected sources and the final approval decision.
