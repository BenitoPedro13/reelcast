# Reelcast

A small, YouTube-shaped on-demand video streaming platform — upload a video, get back an
adaptive-bitrate HLS stream, watch it in a browser player that switches quality with
bandwidth. Built as a standalone portfolio case study.

**Status:** monorepo scaffolded; `apps/api` has a working DB schema + upload API
(`POST /videos`, `POST /videos/:id/complete`, `GET /videos/:id`, `GET /videos`) verified
end-to-end against local Postgres/Redis/MinIO. `apps/web` and `worker/` are not built yet —
see `docs/tasks/` for what's done and what's next.

See [`docs/spec.md`](docs/spec.md) for the full design: architecture, stack, processing
pipeline, data model, phasing, and open questions.

## Quickstart (local dev)

```bash
pnpm install
pnpm infra:up          # Postgres + Redis + MinIO (infra/docker-compose.yml)
cp .env.example apps/api/.env

cd apps/api
npx drizzle-kit migrate # apply the videos/renditions schema
pnpm start:dev           # API on http://localhost:3000
```

Bring local infra down with `pnpm infra:down` from the repo root.
