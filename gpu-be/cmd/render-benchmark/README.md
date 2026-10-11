# Render cost comparison

The helper runs the real renderer with a local object-store adapter. It never
connects to PostgreSQL, RabbitMQ, the coordinator, or YouTube. Input files are
read-only; every generated file is in one owned temporary directory and removed
on success/error. Run on the same Ubuntu 26.04 / FFmpeg / NVIDIA runtime as the
deployed GPU worker, separately from a live performance baseline.

Build from `gpu-be`:

```
go build -o render-benchmark ./cmd/render-benchmark
./render-benchmark -manifest fixtures.json -nvenc -iterations 3 -profile both > results.jsonl 2> stages.jsonl
```

The manifest has exactly six Shorts and 3–60 assembly items; the assembly length
including one-second transitions must be 300–480 seconds. Use saved real imports
with different frame rates/aspect ratios, at least one silent clip, and a PNG/JPEG
banner. Each interval must fit the file. Example shape (repeat Shorts/items to
meet the above counts, using actual paths and durations):

```json
{
  "shorts": [
    {"path":"/fixtures/landscape60.mp4","start":0.35,"end":30.35},
    {"path":"/fixtures/silent.mp4","start":0,"end":30,"banner":"/fixtures/logo.png"}
  ],
  "assembly": [
    {"path":"/fixtures/landscape60.mp4","start":0,"end":30,"channel":"alpha"},
    {"path":"/fixtures/silent.mp4","start":0,"end":30,"channel":"beta"}
  ]
}
```

Both profiles use two encoder/filter threads and the same sequential workload;
only `TWITCH_RENDER_FAST_PATH` and `ASSEMBLY_LOCAL_REUSE` differ. No concurrency
increase is included. The first output line records SHA-256 for every input.
Following lines report end-to-end time, child FFmpeg CPU seconds, peak individual
FFmpeg RSS, peak scratch sampled every 100ms, FFmpeg pass counts, and GET/PUT
counts/bytes. GET/PUT use local copies: the bytes represent avoided network
traffic; their measured time does **not** represent MinIO latency. The assembly
still uploads an original for editing and a final artifact in both modes.

Compare medians for each case across three repetitions. Confirm output duration,
audio, framing and banner appearance with the media smoke suite before enabling
either flag. A maximum RSS number is for one FFmpeg process; use cgroup metrics
to assess total pod memory. All flags remain disabled in the normal application
unless explicitly enabled on staging.
