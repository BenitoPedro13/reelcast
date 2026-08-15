# Reelcast

A small, YouTube-shaped on-demand video streaming platform — upload a video, get back an
adaptive-bitrate HLS stream, watch it in a browser player that switches quality with
bandwidth. Built as a standalone portfolio case study.

**Status:** P0 feature-complete. Upload, transcode, browse, and playback all work end to
end against local Postgres/Redis/MinIO. `apps/api` has a working DB schema + upload API
(`POST /videos`, `POST /videos/:id/complete`, `GET /videos/:id`, `GET /videos`), CORS, and
key→URL mapping for the browser; `apps/worker` (BullMQ shim) and `worker/` (Go transcoder)
turn a `queued` upload into a `ready` adaptive HLS ladder in object storage; `apps/web` has
a browse page, an upload page (progress bar + no-reload status polling), and a watch page
with an `hls.js` player that visibly switches quality under throttled bandwidth. See
`docs/tasks/` for what's done and what's next (deploy/P1).

See [`docs/spec.md`](docs/spec.md) for the full design: architecture, stack, processing
pipeline, data model, phasing, and open questions.

## Quickstart (local dev)

```bash
pnpm install
cp .env.example apps/api/.env
cp .env.example apps/worker/.env   # then edit: worker only needs REDIS_URL/DATABASE_URL/
                                    # S3_*/GO_WORKER_BIN, drop the rest
cp apps/web/.env.example apps/web/.env.local

cd apps/api && npx drizzle-kit migrate && cd ../..  # apply the videos/renditions schema, once

pnpm dev:all             # infra up, Go binary built, api+worker+web running — one command
```

`pnpm dev:all` brings up Postgres/Redis/MinIO (`infra/docker-compose.yml`), builds
`worker/bin/transcode`, then runs `apps/api` (`:3001`), `apps/worker`, and `apps/web`
(`:3000`) together via `concurrently`, each line prefixed `[api]`/`[worker]`/`[web]`. Once
infra is up and the Go binary is built, `pnpm dev` (no `infra:up`/`build:worker` step) is
enough for subsequent runs. Bring local infra down with `pnpm infra:down`.

Running each piece by hand instead (e.g. to restart just one):

```bash
pnpm infra:up             # Postgres + Redis + MinIO
pnpm build:worker          # worker/bin/transcode

pnpm --filter api start:dev      # API on http://localhost:3001
pnpm --filter worker start:dev   # consumes hls-transcode, spawns worker/bin/transcode
pnpm --filter web dev            # web on http://localhost:3000
```

### Demo script: adaptive quality switching

The point of this project is watching the player actually change quality under bandwidth
pressure. Use a source clip of **at least 60 seconds** — `hls.js` buffers ~30s ahead by
default, so a short clip (e.g. a 12s test fixture, 3 segments per rendition at the default
4s segment length) can finish downloading before you've even opened DevTools, leaving
nothing left to switch. With the full stack running and a `ready`, sufficiently long video:

1. Open `http://localhost:3000`, click into a watch page, and press play. Note the
   rendition indicator under the video (e.g. `720p · 2800 kbps`).
2. Open Chrome DevTools → Network tab → change throttling from "No throttling" to a slow
   profile (e.g. "Slow 3G").
3. Watch the indicator drop to a lower rendition within a few seconds, without a playback
   stall.
4. Set throttling back to "No throttling" and watch it climb back up.

`docs/tasks/TASK-web-playback-ui.md` §5 has before/after screenshots from this exact
drive.

### Testing the worker

```bash
cd worker
go test ./...            # unit + golden-fixture tests (real ffmpeg/ffprobe, real MinIO/Postgres)

cd apps/worker
pnpm test:e2e             # full pipeline + BullMQ kill-and-retry test, against local infra
```

Both require `infra/docker-compose.yml` running and, for the Go tests, `ffmpeg`/`ffprobe`
on `PATH`.

### Testing apps/web

```bash
cd apps/web
pnpm build && pnpm lint
pnpm exec playwright install chromium   # once
pnpm test:e2e                            # Playwright, drives a real upload through the UI
```

`pnpm test:e2e` starts `apps/web` itself (`playwright.config.ts`'s `webServer`), but
`apps/api` and `apps/worker` (plus `infra/docker-compose.yml`) must already be running —
the test uploads `apps/web/e2e/fixtures/sample.mp4` through the real upload form and waits
for a real ffmpeg transcode to reach `ready`.
