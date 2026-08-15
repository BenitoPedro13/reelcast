# Reelcast — Adaptive Streaming Platform (Design Spec)

*A small, YouTube-shaped on-demand video platform: upload a file, get back an
adaptive-bitrate stream you can watch in a browser. Built as a standalone portfolio case
study — separate repo, separate backend, deliberately smaller in scope than a production
platform.*

**Status:** spec drafted, no code yet. Companion project to `plexus` (a general media
pipeline engine); this repo reuses the *lessons* from that project (ffmpeg-backed workers,
plan-before-code, real infra in tests) but not its code or its DAG/plugin generality — this
is one focused pipeline (upload → HLS ladder → play), not a general processor engine.

---

## 1. Problem statement

Demonstrate, end to end, the mechanics behind adaptive bitrate video streaming: a source
file becomes several resolution/bitrate renditions, packaged as HLS segments + manifests,
served over plain HTTP/CDN, and played back by a client that switches quality with
available bandwidth — the same shape as YouTube, Netflix, Twitch VOD, etc., at a scale a
single portfolio project can actually finish and explain.

## 2. Goals (v1 / P0)

**Status:** all six items done as of `docs/tasks/TASK-web-playback-ui.md`. Items 2/3/6
landed with the HLS worker; items 1/4/5 (browser upload, no-reload status, browse/watch
pages with a visibly adaptive player) landed with `apps/web`. P0 is feature-complete.

1. Upload a video file from the browser.
2. Backend transcodes it into an adaptive bitrate **HLS ladder** (multiple
   resolution/bitrate renditions + a master manifest).
3. Backend extracts a thumbnail.
4. Uploader sees processing status (queued → processing → ready/failed) without a hard
   page reload.
5. A browse page lists ready videos; a watch page plays them with a real adaptive player
   (visibly switches rendition under throttled bandwidth — this is the demo moment).
6. Videos and metadata persist across restarts.

## 3. Non-goals (v1)

- **Live streaming** — VOD only. No RTMP ingest, no live segmenter.
- **Auth / multi-tenancy** — single implicit owner is fine for v1; real auth is a P1
  candidate (see §9), not required to prove the streaming mechanics.
- **DASH, DRM, captions** — HLS only for v1; WebVTT captions are a P1 candidate.
- **Comments, likes, recommendations, monetization** — no social/product layer.
- **GPU-accelerated transcoding** — CPU ffmpeg is enough at portfolio scale; noted as a
  future optimization, not attempted here.
- **Mobile apps** — web only.

## 4. Terminology

- **Rendition** — one resolution/bitrate encode of the source (e.g. 720p @ 2800kbps).
- **Ladder** — the fixed set of renditions produced per upload (see §7.2).
- **Master manifest** — `master.m3u8`, lists all renditions; the player reads this first.
- **Variant manifest** — per-rendition `.m3u8` listing that rendition's segments.

## 5. Architecture overview

```
┌────────────┐  presigned PUT   ┌──────────────┐
│  Browser    │ ───────────────▶│ Object storage│◀────────────┐
│  (Next.js,  │                  │ (R2, public-  │             │
│  hls.js)    │                  │  read + CDN)  │   segments  │
└─────┬───────┘                  └──────┬────────┘  + manifests│
      │ REST (metadata,                 ▲                     │
      │ status polling)                 │ upload output       │
      ▼                                 │                     │
┌────────────┐   enqueue job     ┌──────────────┐
│  API        │ ─────────────────▶│ Redis/BullMQ │
│ (Nest,      │◀──────────────────│ queue        │
│  separate   │  status updates   └──────┬───────┘
│  service)   │                          │ dequeue
└─────┬───────┘                          ▼
      │ reads/writes            ┌──────────────────────┐
      ▼                         │ apps/worker (TS shim) │
┌────────────┐                  │ holds the BullMQ job  │
│ Postgres    │◀───────┐        │ lock, nothing else    │
│ (Drizzle)   │        │        └──────────┬────────────┘
└────────────┘         │                   │ spawn, argv
                        │ status +          ▼
                        │ renditions   ┌──────────────────────┐
                        └──────────────│ worker/ (Go binary)   │────▶ Object storage
                                       │ ffprobe + ffmpeg HLS  │      (segments +
                                       │ packaging + S3 upload │       manifests)
                                       └──────────────────────┘
```

Three pieces, not two: **API** (Node/Nest, I/O-bound — metadata, presigned URLs, job
status), **apps/worker** (TS, a thin BullMQ consumer — holds the job lock and spawns the Go
binary, nothing else), and **worker/** (Go, CPU-bound — ffprobe/ffmpeg/S3/Postgres). The
API still enqueues `{ videoId, sourceKey }` onto `hls-transcode` exactly as before; only the
consumer side gained a step. The split exists because BullMQ has no official Go client (see
`docs/tasks/TASK-hls-worker.md` §2.1) — official Node BullMQ owns retry/backoff/stalled
recovery (the guarantee §10 tests), while the Go binary keeps every byte of media, storage,
and database work, matching Plexus's Go/TS split of not running CPU-bound transcoding
inside the same process that handles queue/HTTP traffic.

## 6. Stack

| Layer | Choice | Why |
|---|---|---|
| Frontend | Next.js (latest stable major — verify, don't pin from memory), shadcn/ui, `hls.js` | `hls.js` is the standard adaptive-HLS player for non-Safari browsers; Safari gets native `<video>` HLS support. |
| Backend API | Node.js + NestJS, separate service/repo folder from the worker | Explicit ask: separate backend, not Next.js API routes. Nest gives structured modules for videos/upload/jobs without much ceremony. |
| Worker | Go + ffmpeg (shell out, never reimplement codecs) | Same "ffmpeg/libvips do the media work" rule as Plexus. One processor: source → HLS ladder + thumbnail. |
| Queue | Redis + BullMQ, consumed by a thin `apps/worker` TS shim that spawns the Go binary per job | Simpler and more legible to a reviewer than NATS JetStream — this project has one job type, not a general dispatch/replay system, so the heavier tool isn't earning its keep here. The shim exists because BullMQ has no official Go client (`docs/tasks/TASK-hls-worker.md` §2.1); it holds the job lock only, all media/storage/DB work stays in Go. |
| Database | PostgreSQL + Drizzle ORM | Consistent with Plexus tooling; no relational need this project has that Postgres doesn't cover. |
| Object storage | S3-compatible, public-read bucket behind a CDN — **Cloudflare R2** is the default pick (free egress matters once a demo gets traffic) | HLS playback fetches many small segments continuously; presigned-per-segment URLs don't fit that access pattern the way a public CDN-backed bucket does. **Resolved locally:** the API owns a `S3_PUBLIC_BASE_URL` config value, separate from the SDK's signing `S3_ENDPOINT`, and maps `masterManifestKey`/`thumbnailKey` to `masterManifestUrl`/`thumbnailUrl` in its `GET /videos*` responses — bucket key structure never crosses into the browser bundle (`docs/tasks/TASK-web-playback-ui.md` §2.2). `[VERIFY: R2 current public-bucket + custom domain setup steps]` still open for the deploy-time value of that same config var. |
| Realtime status | Short-interval polling (`GET /videos/:id`) | No SSE/WebSocket infra for a single job type — polling is a defensible, easy-to-explain simplification here. |
| Deploy | Railway (API + worker + Postgres + Redis), R2 for storage, Vercel or Railway for frontend | Reuses infra already set up for Plexus. |

## 7. Processing pipeline

### 7.1 Flow

1. Client requests `POST /videos` → API creates a `videos` row (`status: uploading`),
   returns a presigned PUT URL for the source object.
2. Client uploads directly to object storage.
3. Client calls `POST /videos/:id/complete` → API enqueues a BullMQ job
   `{ videoId, sourceKey }`, sets `status: queued`.
4. Worker dequeues, runs `ffprobe` (duration, source resolution — renditions above source
   resolution are skipped, never upscaled), then packages HLS, uploads outputs, updates
   `status: ready` (or `failed` with a reason).
5. Client polls `GET /videos/:id` until `status: ready`, then loads `master.m3u8` into the
   player.

### 7.2 Rendition ladder (default, v1)

| Rendition | Target bitrate | Included when source height ≥ |
|---|---|---|
| 1080p | 5000 kbps | 1080 |
| 720p | 2800 kbps | 720 |
| 480p | 1400 kbps | 480 |
| 360p | 800 kbps | 360 |

**Resolved** (measured against ffmpeg 9.0, see `docs/tasks/TASK-hls-worker.md` §2.4/§5 for
the full invocation and evidence): one ffmpeg process per source, `split` + `scale` per
rendition, `-hls_playlist_type vod` with `-f hls`/`-hls_time 4`/`-var_stream_map` naming
variants by resolution (`name:1080p` etc.), 4-second segments named `seg%05d.ts`. Two
things the flag docs alone didn't reveal:

- `-force_key_frames "expr:gte(t,n_forced*4)"` is required, not cosmetic — without it,
  segments land on natural keyframes (measured 4.8s/3.2s/4.8s…) and aren't guaranteed to
  align across renditions, which stalls the adaptive-switching demo in §10 at every switch.
- A silent source hard-fails `-map a:0` (`Stream map '' matches no streams`); the command
  builder branches on `ffprobe`-detected audio presence.

### 7.3 Thumbnail

Single frame grab at a fixed offset (e.g. 3s or 10% of duration, whichever is smaller) via
ffmpeg, stored alongside the manifest.

## 8. Data model (sketch)

```
videos
  id            uuid pk
  title         text
  description   text nullable
  status        enum(uploading, queued, processing, ready, failed)
  source_key    text            -- object storage key of the original upload
  duration_sec  numeric nullable
  master_manifest_key text nullable
  thumbnail_key text nullable
  failure_reason text nullable
  created_at    timestamptz

renditions
  id            uuid pk
  video_id      uuid fk -> videos.id
  height        int             -- 1080, 720, 480, 360
  bitrate_kbps  int
  playlist_key  text            -- object storage key of this rendition's .m3u8
```

## 9. Phasing

- **P0 (this spec's scope):** everything in §2.
- **P1:** resumable/chunked upload for large files (tus protocol or S3 multipart), basic
  auth (single-user or simple email/GitHub login), delete/re-process a video, WebVTT
  captions.
- **P2:** DASH manifest as a second packaging format, view counts, watch-time analytics,
  search.

## 10. Success metrics (concrete, not "works"/"fast")

- Time from `POST /videos/:id/complete` to `status: ready`, measured for a fixed 1-minute
  1080p source clip on a stated worker spec. `[VERIFY: baseline once worker exists —
  don't publish a number until measured]`
- Each rendition's *actual* encoded bitrate falls within ±15% of its table target
  (ffprobe the output, assert in a golden-fixture test — same "measured, not eyeballed"
  rule Plexus applies to recipe fidelity). **Measured on the video stream only**
  (`ffprobe -select_streams v:0`, packet bytes summed over the rendition's segments), not
  whole-segment bytes: muxed segment bytes include the 128kbps audio track plus MPEG-TS
  overhead, which pushed 360p to +19.2% against target in measurement — a false failure of
  an encoder that was in fact exact on the video stream (`docs/tasks/TASK-hls-worker.md`
  §5).
- Adaptive switching is demonstrable: throttle bandwidth in devtools mid-playback and
  observe the player drop to a lower rendition without a playback stall.
- A worker killed mid-job does not silently lose the video: job reappears in the queue
  and a retry completes it (BullMQ's built-in retry/backoff, verified with an integration
  test, not assumed).

## 11. Open questions

- **Auth in v1 at all**, even a stub? Leaning no — adds no evidence for the streaming
  story this case study is meant to demonstrate.
- **Upload strategy** — single presigned PUT (simple, fine up to some size ceiling) vs.
  multipart/resumable from day one. Leaning single PUT for P0, multipart moved to P1
  per §9.
- **CDN in front of R2** — R2's own public bucket vs. a Cloudflare CDN/Worker in front for
  cache control headers on manifests (short TTL) vs. segments (long TTL, immutable).
  **Local answer resolved:** the browser reads manifests/segments/thumbnails straight from
  the public-read bucket origin via `S3_PUBLIC_BASE_URL` (`http://localhost:9000/reelcast`
  against MinIO); MinIO's CORS is permissive enough for both the presigned PUT and hls.js's
  segment GETs with no compose change (`docs/tasks/TASK-web-playback-ui.md` §1). Still open
  for deploy: R2 doesn't reflect arbitrary origins the way MinIO's default config does, so
  `[VERIFY: R2 CORS policy + whether a CDN/Worker in front is actually needed]` before the
  first deploy.
- **Backend framework** — Nest (chosen above) vs. a lighter Fastify/Express service. Nest
  wins for now on consistency with Plexus experience; revisit if the API surface stays
  this small (4-5 endpoints) and Nest's structure starts feeling like overhead.

---

## Next step

Per the plan-before-code approach: write a `docs/tasks/TASK-scaffold-monorepo.md` (or
equivalent) covering the first scaffold — repo layout, package manager choice for the TS
side, `go.mod` for the worker, docker-compose for local Postgres/Redis/MinIO-as-R2-stand-in
— before any code is created, then get it reviewed before scaffolding.
