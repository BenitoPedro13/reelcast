# TASK-hls-worker

## 1. Current scenario

`TASK-db-schema-upload-api` is done and committed (`79cdbf3`). The API covers spec §7.1
steps 1–3 and 5: `POST /videos` (presigned PUT), `POST /videos/:id/complete` (flips to
`queued`, enqueues `{ videoId, sourceKey }` onto the `hls-transcode` BullMQ queue),
`GET /videos/:id`, `GET /videos`. `videos`/`renditions` tables exist and are migrated.

**Nothing consumes the queue.** `worker/` contains only `go.mod` (module
`github.com/BenitoPedro13/reelcast/worker`, `go 1.26.5`) — no source files. Jobs land in
Redis and sit there forever; a video never leaves `queued`.

Two things blocked this task and are now resolved (evidence in §5):

1. **spec §7.2's ffmpeg `[VERIFY]`** — the multi-variant HLS invocation is now verified
   empirically against ffmpeg 9.0, not assumed.
2. **A design hole not visible at spec time: BullMQ has no official Go client.** Official
   ports are Node, Python, Rust, PHP, Elixir. The only Go option
   ([`gobullmq`](https://github.com/Codycody31/gobullmq)) is third-party, release-candidate,
   19 stars, with embedded Lua pinned to **BullMQ v4.12.2** against the API's **6.1.1**.
   Spec §10 makes BullMQ retry/stalled-recovery a *tested guarantee*, so betting it on an
   RC third-party protocol reimplementation was not acceptable. Decision in §2.1.

**Prerequisite not yet met:** the Go toolchain is not installed on this machine (`go` is
not on `PATH`, despite `worker/go.mod` declaring `go 1.26.5`). `brew install go` — and a
check that the installed version satisfies the `go` directive — comes before any Go code.
ffmpeg 9.0 and ffprobe are installed (`/opt/homebrew/bin`).

## 2. Planned changes

### 2.1 Architecture: Node shim consumer + Go transcoder binary

```
BullMQ job ──▶ apps/worker (TS, official bullmq)   ← owns job lifecycle only
                     │ spawn, pass job as argv/env
                     ▼
               worker/ (Go binary)                  ← ffprobe, ffmpeg, S3 upload, DB writes
                     │ exit 0 / non-zero
                     ▼
          job completed  /  retried by BullMQ
```

The shim holds the BullMQ lock and does nothing else; the Go binary does all media work,
all uploads, and all Postgres writes. Media work stays CPU-bound in Go per spec §5, and
retry/backoff/stalled recovery stay in the official BullMQ implementation so spec §10's
kill-and-retry test is validating the real thing.

**Alternatives rejected:** Go + `gobullmq` (RC lib, two-major Lua/protocol gap vs. the
API's bullmq 6.1.1 — a mismatch would surface as silently lost or stuck jobs, i.e. exactly
the failure spec §10 exists to rule out); raw Redis list queue (amends spec §6 and makes
retry/backoff/stalled-recovery hand-rolled code we'd have to prove ourselves); TS-only
worker (drops the Go/CPU split that spec §5 deliberately sells).

Spec §5's diagram already shows the worker writing status updates to Postgres, and
`CLAUDE.md` §4 already names the `videos`/`renditions` schema as a legitimate TS↔Go
contract — so Go owning the DB writes is the design as specified, not a new liberty.

### 2.2 New workspace package — `apps/worker/` (TS shim)

Plain TS + `bullmq` (same 6.1.1 as the API) + `ioredis`. No Nest — it has no HTTP surface.

- `src/main.ts` — constructs `new Worker('hls-transcode', processor, { connection, concurrency: 1 })`.
- `src/processor.ts` — spawns the Go binary via `node:child_process.spawn`, passing
  `--video-id`, `--source-key`, and `--final-attempt` when
  `job.attemptsMade + 1 >= job.opts.attempts`. Streams the child's stderr into the shim's
  log. Resolves on exit 0; throws on non-zero so BullMQ records the failure and retries.
- Job options set **on the producer side** (`apps/api`'s `QueueService.enqueueTranscode`):
  `attempts: 3`, `backoff: { type: 'exponential', delay: 5000 }`. This is an edit to
  existing API code, not new worker code.
- `GO_WORKER_BIN` env var points at the compiled binary (built to `worker/bin/transcode`).

`concurrency: 1` because each job already saturates the CPU across 4 simultaneous encodes
(measured 645% CPU in §5) — a second concurrent job would only cause contention.

### 2.3 Go layout — `worker/`

```
worker/
  cmd/transcode/main.go     flag parsing, orchestration, exit codes
  internal/probe/probe.go   ffprobe: duration, width, height, has-audio
  internal/ladder/ladder.go rendition selection (no upscale), bitrate table
  internal/hls/hls.go       builds + runs the ffmpeg invocation, thumbnail grab
  internal/storage/s3.go    aws-sdk-go-v2 S3 upload (MinIO/R2), content types
  internal/store/store.go   pgx: status transitions, renditions rows
```

Dependencies (`aws-sdk-go-v2` for S3, `jackc/pgx/v5` for Postgres) —
`[VERIFY: resolve exact module versions via `go get` against the module proxy at install
time; Go isn't installed yet so nothing is pinned here from memory]`.

### 2.4 The ffmpeg invocation (verified — see §5)

Built dynamically from the selected ladder. For a 1080p source with audio, all four
renditions:

```
ffmpeg -i <source> \
 -filter_complex "[0:v]split=4[v1][v2][v3][v4];\
                  [v1]scale=-2:1080[v1o];[v2]scale=-2:720[v2o];\
                  [v3]scale=-2:480[v3o];[v4]scale=-2:360[v4o]" \
 -map "[v1o]" -c:v:0 libx264 -preset veryfast -b:v:0 5000k -maxrate:v:0 5350k -bufsize:v:0 7500k \
 -map "[v2o]" -c:v:1 libx264 -preset veryfast -b:v:1 2800k -maxrate:v:1 2996k -bufsize:v:1 4200k \
 -map "[v3o]" -c:v:2 libx264 -preset veryfast -b:v:2 1400k -maxrate:v:2 1498k -bufsize:v:2 2100k \
 -map "[v4o]" -c:v:3 libx264 -preset veryfast -b:v:3 800k  -maxrate:v:3 856k  -bufsize:v:3 1200k \
 -map a:0 -map a:0 -map a:0 -map a:0 -c:a aac -b:a 128k -ac 2 \
 -force_key_frames "expr:gte(t,n_forced*4)" \
 -f hls -hls_time 4 -hls_playlist_type vod -hls_flags independent_segments \
 -hls_segment_type mpegts -hls_segment_filename "<tmp>/%v/seg%05d.ts" \
 -master_pl_name master.m3u8 \
 -var_stream_map "v:0,a:0,name:1080p v:1,a:1,name:720p v:2,a:2,name:480p v:3,a:3,name:360p" \
 "<tmp>/%v/playlist.m3u8"
```

Notes, each verified in §5:

- `name:` in `-var_stream_map` makes `%v` expand to `1080p`/`720p`/… so output directories
  (and therefore object keys) are human-readable rather than `0`/`1`/`2`.
- `-force_key_frames "expr:gte(t,n_forced*4)"` is **required**, not cosmetic: without it
  segments land on natural keyframes (measured 4.8s/3.2s/4.8s…); with it every segment is
  exactly 4.000s and boundaries are identical across renditions, which is what lets the
  player switch variants without a stall (spec §10's demo moment).
- `-hls_playlist_type vod` forces `hls_list_size` to 0 — verified all segments stay listed
  and `#EXT-X-ENDLIST` is emitted, so **no explicit `-hls_list_size 0` is needed**.
- `seg%05d.ts`, not `%03d`: `%03d` overflows past 999 segments (~66 min at 4s).
- **No-audio sources must take a different command.** `-map a:0` on a silent source is a
  hard error (`Stream map '' matches no streams`). `probe` reports whether an audio stream
  exists; when absent, the audio `-map`/`-c:a` flags are omitted and `var_stream_map`
  entries drop the `a:N` component (`"v:0,name:360p …"`). Verified working.
- `-preset veryfast` is the starting point; it hit target bitrates within 1% (§5). Revisit
  only against a measured quality/time tradeoff, not by taste.

Thumbnail (spec §7.3), verified: `ffmpeg -ss <offset> -i <source> -frames:v 1 -vf scale=640:-2 thumb.jpg`
where `offset = min(3s, 10% of duration)`.

### 2.5 Ladder selection (spec §7.1: never upscale)

Include a rendition when `sourceHeight >= renditionHeight`. Edge case the spec doesn't
cover: a source shorter than 360p qualifies for nothing — emit a **single** rendition at
the source height using the 360p bitrate, so every video still gets a playable master
manifest rather than an empty ladder.

### 2.6 Object key layout

| Object | Key |
|---|---|
| Master manifest | `videos/{id}/hls/master.m3u8` |
| Variant playlist | `videos/{id}/hls/{name}/playlist.m3u8` |
| Segments | `videos/{id}/hls/{name}/seg00000.ts` |
| Thumbnail | `videos/{id}/thumb.jpg` |

Upload with correct content types (`application/vnd.apple.mpegurl` for `.m3u8`,
`video/mp2t` for `.ts`, `image/jpeg`) and `Cache-Control`: short TTL on manifests, long
`immutable` on segments — this is the cheap half of spec §11's open CDN question and can be
set at upload time regardless of how the CDN question resolves.

### 2.7 Status transitions and failure semantics

Go is the only writer to Postgres in this pipeline:

- On start: `queued → processing`.
- On success: `→ ready`, setting `durationSec`, `masterManifestKey`, `thumbnailKey`, and
  inserting one `renditions` row per encoded variant.
- On failure: exit non-zero. Status is left as `processing` so BullMQ can retry. Only when
  the shim passed `--final-attempt` does Go write `→ failed` with `failureReason`. This is
  why the shim passes attempt state: a video must not show `failed` in the UI while retries
  are still pending.

**Idempotency on retry:** object keys are deterministic (overwrite is safe), and the run
deletes any existing `renditions` rows for that `videoId` before inserting, so a retry
can't leave duplicates. All ffmpeg output goes to a temp dir that is removed on exit.

### 2.8 Env

New in `.env.example`: `GO_WORKER_BIN`. The Go binary reuses the existing `DATABASE_URL`
and `S3_*` vars; the shim reuses `REDIS_URL`. No new infrastructure.

### 2.9 Tests

Go, golden-fixture style per `CLAUDE.md` — fixtures are **generated at test setup** with
`ffmpeg -f lavfi -i testsrc2=…` rather than committed as binaries (deterministic, keeps the
repo free of media blobs):

- 1080p source → 4 renditions; assert each rendition's resolution, and its **video-stream**
  bitrate within ±15% of target (spec §10).
- Assert every `#EXTINF` is 4.000s and identical across renditions (switchability).
- Assert `#EXT-X-PLAYLIST-TYPE:VOD` + `#EXT-X-ENDLIST`, and that the master lists exactly
  the expected variants.
- 480p source → exactly 480p/360p, no upscaled variants.
- Silent source → succeeds, master has no audio codec in `CODECS`.
- Sub-360p source → single rendition at source height.

TS/integration, against the running `infra/docker-compose.yml` stack (the convention set by
the previous task — no testcontainers):

- Full path: enqueue → shim → Go → objects in MinIO → row is `ready` with renditions.
- **Spec §10's kill test:** kill the Go child mid-encode; assert BullMQ retries and a
  subsequent attempt drives the video to `ready` — and that the row never showed `failed`
  in between.

> Measurement note that changes how the §10 assertion must be written: the ±15% tolerance
> has to be measured on the **video stream**, not on segment file bytes. Segment bytes
> include the 128k audio track plus MPEG-TS overhead, which pushes 360p to **+19.2%** — a
> false failure. Video-only is within +0.9% across the ladder (§5).

### 2.10 Documentation to update before this task is done

- `docs/spec.md` §7.2 — replace the `[VERIFY]` block with the resolved invocation.
- `docs/spec.md` §10 — state that the bitrate tolerance is measured video-stream-only, and
  why.
- `docs/spec.md` §5/§6 — record the shim, since the diagram currently implies Go dequeues
  BullMQ directly.
- `CLAUDE.md` §0 status, and §4's layout block (add `apps/worker`).
- `.env.example` (`GO_WORKER_BIN`), `README.md` (how to build/run the worker).

## 3. Why

This is the piece that makes the product actually work: today an upload reaches `queued`
and stops. It's also the task carrying the project's real engineering claim — an adaptive
ladder whose segments are aligned well enough to switch cleanly — so it's the one where
"measured, not eyeballed" matters most.

The shim exists because of a specific, verifiable gap (no official Go BullMQ client) rather
than a preference, and it's deliberately as thin as possible: it holds a lock and spawns a
process. If BullMQ ever ships an official Go client, the shim deletes cleanly and the Go
binary is unaffected — the seam is a process boundary with a two-flag contract, not a
library entanglement.

Resolving §7.2 by *running* ffmpeg rather than reading its docs already paid for itself
twice: `force_key_frames` (without which the ABR demo stalls at every switch) and the
no-audio hard failure (which would have crashed on the first silent upload) are both things
the flag list alone would not have revealed.

## 4. Affected files

| File | Change type | Notes |
|---|---|---|
| `worker/cmd/transcode/main.go` | new | flags, orchestration, exit codes |
| `worker/internal/probe/probe.go` | new | ffprobe wrapper: duration, dimensions, has-audio |
| `worker/internal/ladder/ladder.go` | new | rendition selection, no upscale |
| `worker/internal/hls/hls.go` | new | ffmpeg command construction + thumbnail |
| `worker/internal/storage/s3.go` | new | aws-sdk-go-v2 upload, content types, cache headers |
| `worker/internal/store/store.go` | new | pgx status transitions + renditions rows |
| `worker/*_test.go` | new | golden-fixture tests, ffmpeg-generated sources |
| `worker/go.mod` / `go.sum` | edit | aws-sdk-go-v2, pgx (versions resolved via `go get`) |
| `apps/worker/package.json` | new | TS shim: bullmq, ioredis, tsx/tsc |
| `apps/worker/src/main.ts` | new | BullMQ `Worker` construction |
| `apps/worker/src/processor.ts` | new | spawns Go binary, maps exit code → job outcome |
| `apps/api/src/queue/queue.service.ts` | edit | add `attempts: 3` + exponential backoff |
| `pnpm-workspace.yaml` | edit | include `apps/worker` |
| `infra/docker-compose.yml` | no change | no new infra |
| `.env.example` | edit | `GO_WORKER_BIN` |
| `docs/spec.md` | edit | §7.2 resolved, §10 measurement note, §5/§6 shim |
| `CLAUDE.md` | edit | §0 status, §4 layout |
| `README.md` | edit | build/run the worker |

## 5. Verification items — resolved (measured 2026-08-15, ffmpeg 9.0, Apple Silicon)

All numbers below come from real runs on a generated 20s 1920x1080 30fps source, not from
documentation.

**Flags exist and behave as spec §7.2 assumed.** `-var_stream_map`, `-master_pl_name`,
`-hls_time`, `-hls_playlist_type vod`, `-hls_segment_filename` all confirmed present in
ffmpeg 9.0's `hls` muxer.

**`-hls_playlist_type vod` forces `hls_list_size` to 0.** First run was ambiguous (a 20s
clip at 4s = exactly 5 segments, which coincidentally equals the default `hls_list_size` of
5). Re-ran at `-hls_time 2` → 10 segments on disk, 10 listed in the playlist, `EXT-X-ENDLIST`
present. Confirmed; no explicit `-hls_list_size 0` required.

**Bitrate accuracy — and the measurement trap.**

| Rendition | Resolution | Target | Video-only | Δ | Muxed total | Δ |
|---|---|---|---|---|---|---|
| 1080p | 1920x1080 | 5000k | 5020k | +0.4% | 5266k | +5.3% |
| 720p | 1280x720 | 2800k | 2824k | +0.9% | 3023k | +8.0% |
| 480p | 854x480 | 1400k | 1404k | +0.3% | 1572k | +12.3% |
| 360p | 640x360 | 800k | 800k | +0.0% | 954k | **+19.2%** |

Measured against segment bytes, 360p **fails** spec §10's ±15% gate; measured against the
video stream it is exact. The tolerance is a statement about the encoder, so the assertion
must probe the video stream (`ffprobe -select_streams v:0 -show_entries packet=size`
summed over duration). §2.9 and the §10 doc update both reflect this.

**Segment alignment.** Without `-force_key_frames`, `EXTINF` values were 4.8/3.2/4.8/3.2/4.0
— uneven and not guaranteed to align across variants. With
`-force_key_frames "expr:gte(t,n_forced*4)"`, every segment is exactly 4.000000s and the
720p and 360p playlists are boundary-identical.

**No-audio sources hard-fail.** `-map a:0` against a silent source aborts with
`Stream map '' matches no streams` / `Error opening output files`. The video-only variant
(no audio maps, `var_stream_map "v:0,name:360p"`) succeeds and produces a valid master with
`CODECS="avc1.64001e"` and no audio codec. Requires a branch in the Go command builder.

**Master manifest** is generated correctly with `BANDWIDTH`, `AVERAGE-BANDWIDTH`,
`RESOLUTION`, and `CODECS` per variant, written to the parent of the `%v` directories.

**Thumbnail** grab at a 3s offset produced a valid 640x360 JPEG (15,904 bytes).

**Throughput datapoint (not yet the spec §10 baseline):** 20s of 1080p → all 4 renditions
in **4.59s wall** (645% CPU, `-preset veryfast`). Spec §10 wants a 1-minute 1080p clip on a
stated worker spec — measure and publish that only once the worker actually exists.

## 6. Deferred / still open

- Spec §11's CDN-vs-R2-public-bucket question is untouched here; this task only sets
  sensible `Cache-Control` at upload time, which is correct under either outcome.
- Go module versions are unpinned until the toolchain is installed (§2.3).
- `-preset veryfast` is a starting point, not a tuned choice.
