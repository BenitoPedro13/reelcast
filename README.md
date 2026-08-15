# Reelcast

A small, YouTube-shaped on-demand video streaming platform — upload a video, get back an
adaptive-bitrate HLS stream, watch it in a browser player that switches quality with
bandwidth. Built as a standalone portfolio case study.

**Status:** upload-to-playback pipeline works end to end against local
Postgres/Redis/MinIO. `apps/api` has a working DB schema + upload API (`POST /videos`,
`POST /videos/:id/complete`, `GET /videos/:id`, `GET /videos`); `apps/worker` (BullMQ shim)
and `worker/` (Go transcoder) turn a `queued` upload into a `ready` adaptive HLS ladder in
object storage. `apps/web` is not built yet — see `docs/tasks/` for what's done and what's
next.

See [`docs/spec.md`](docs/spec.md) for the full design: architecture, stack, processing
pipeline, data model, phasing, and open questions.

## Quickstart (local dev)

```bash
pnpm install
pnpm infra:up          # Postgres + Redis + MinIO (infra/docker-compose.yml)
cp .env.example apps/api/.env
cp .env.example apps/worker/.env   # then edit: worker only needs REDIS_URL/DATABASE_URL/
                                    # S3_*/GO_WORKER_BIN, drop the rest

cd apps/api
npx drizzle-kit migrate # apply the videos/renditions schema
pnpm start:dev           # API on http://localhost:3000

# in another terminal: build and run the Go transcode binary + its TS shim
cd worker
go build -o bin/transcode ./cmd/transcode

cd ../apps/worker
pnpm start:dev           # consumes the hls-transcode queue, spawns worker/bin/transcode
```

Bring local infra down with `pnpm infra:down` from the repo root.

### Testing the worker

```bash
cd worker
go test ./...            # unit + golden-fixture tests (real ffmpeg/ffprobe, real MinIO/Postgres)

cd apps/worker
pnpm test:e2e             # full pipeline + BullMQ kill-and-retry test, against local infra
```

Both require `infra/docker-compose.yml` running and, for the Go tests, `ffmpeg`/`ffprobe`
on `PATH`.
