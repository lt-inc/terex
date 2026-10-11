# Render-cost rollout

This change preserves worker counts and one heavy FFmpeg encoding/filter slot
per GPU process. Video verification has its own single slot and waits at most
10 seconds; an occupied slot is temporary `resource_wait`, never corrupt media.
Decoder/encoder/filter threads default to two; verification uses one.

New application settings:

| Variable | Default | Meaning |
|---|---|---|
| `TWITCH_RENDER_FAST_PATH` | `false` | One pass for ordinary Twitch vertical rectangle Shorts, without subtitles/custom background, optionally a PNG/JPEG banner |
| `ASSEMBLY_LOCAL_REUSE` | `false` | Keep a newly assembled original locally through final upload; skip original GET and full-range Cut |
| `FFMPEG_THREADS` | `2` | Threads per decoder/encoder/filter graph |
| `MEDIA_VERIFY_WAIT` | `10s` | Maximum wait for the verification slot |
| `MEDIA_SCRATCH_BUDGET_BYTES` | `10737418240` | Shared application scratch budget, 10 GiB |
| `MEDIA_MIN_FREE_BYTES` | `2147483648` | Minimum free space on the underlying filesystem, 2 GiB |
| `ASSEMBLY_BUDGET_BYTES` | `8589934592` | Existing assembly scratch budget, 8 GiB |

Stage uses `TMP_DIR=/tmp/terex`, `ASSEMBLY_TMP_DIR=/tmp/terex/assembly` on one
12 GiB emptyDir. Nested roots are counted once. Startup clears marked temporary
attempt directories and existing assembly attempt directories before consumers
start. State at `/data/state`, credentials, imported objects and final artifacts
are outside cleanup. Only an attempt's temporary directories are removed after
completion/cancellation/error; no object-storage retention policy is changed.

The budget monitor checks once per second, cancels active media attempts when
capacity is exceeded, and lets their owners clean up. This is a soft application
budget, with a 2 GiB margin before the emptyDir limit, not atomic byte reservation.
`statfs` free space is filesystem free space; Kubernetes emptyDir sizeLimit is
independent. Temporary render/publish pressure retries after at least one minute.
Unknown YouTube upload outcomes retain `needs_attention` and are never silently
re-uploaded.

Enable each optimization independently after the production-runtime smoke tests
and [saved-source comparison](cmd/render-benchmark/README.md). Start with one
Short, inspect framing/audio/banner/trim, then enable the Short flag. Test one
assembly with the reuse flag separately. Both can be switched off to return to
the existing pipeline without changing stored revisions or editor originals.
Unsupported templates select the existing pipeline before execution, with a
`fallback_reason`. A new-path runtime error is reported normally, rather than
repeating the expensive render through a second pipeline.

The fast Short profile keeps the complete foreground at 1080×1920 and blurs a
270×480 background (radius 10/chroma 5) before upscaling. It accurately trims and
encodes H.264/AAC once. Assembly normalization, SHA-256 verification, AAC handling
and concatenation remain unchanged. The original is still stored for editor
revisions; only the brand-new full-range final upload can reuse its local bytes.

Structured logs include numeric `duration_ms`, child CPU seconds, input/output
bytes, `max_rss_kib`, `result_code`, selected profile and job/clip/revision/attempt
identity. Benchmark RSS is individual-process peak; use pod/cgroup monitoring
for combined memory. No additional workers/replicas should be enabled as part
of this rollout.
