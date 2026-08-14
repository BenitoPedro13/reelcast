# TASK-db-schema-upload-api

## 1. Current scenario

`TASK-scaffold-monorepo` is done and committed: `apps/web` (Next.js), `apps/api` (bare
NestJS default template — only the generated `AppModule`/`AppController`/`AppService`),
`worker/go.mod` (no source), and `infra/docker-compose.yml` (Postgres/Redis/MinIO, now
running locally and healthy). `pnpm install` has been run at the root. `.env.example`
already lists `DATABASE_URL`, `REDIS_URL`, and `S3_*` matching the compose services.

Nothing exists yet for the actual product: no DB schema, no `videos`/`renditions` tables,
no API endpoints, no S3 client wiring, no queue. This task builds the first vertical slice
of spec §7.1 steps 1–3 (client asks to upload → gets a presigned URL → tells the API the
upload finished → API enqueues a job) plus step 5 (poll status), stopping short of the
worker itself (`worker/`, blocked on spec §7.2's ffmpeg-flags `[VERIFY]`, is a separate,
later task).

## 2. Planned changes

### 2.1 Dependencies (verified against current docs/registry, per `CLAUDE.md` §2.0)

- `drizzle-orm@0.45.2` + `pg@8.23.0` (runtime), `drizzle-kit@0.31.10` + `@types/pg` (dev),
  in `apps/api`. **Decision: pin to the `latest` dist-tag, not the `rc` tag** — Drizzle's
  v1 line is still `1.0.0-rc.5` as of today (2026-08-14), not GA; no reason to take on
  pre-release churn for a portfolio project's DB layer.
- `@aws-sdk/client-s3@3.1111.0` + `@aws-sdk/s3-request-presigner@3.1111.0` — MinIO/R2 are
  both S3-API-compatible, this is the standard AWS SDK v3 presigned-URL pattern
  (`PutObjectCommand` + `getSignedUrl`, default `expiresIn` 900s if unset — we'll set it
  explicitly).
- `bullmq@6.1.1` (producer side only in this task — enqueue, no worker consumer yet since
  no worker exists to consume).
- `@nestjs/config` for env loading (already implied by Nest conventions, not yet in
  `apps/api/package.json` — confirm current recommended setup via `nest g` / Nest's own
  config docs before adding, per §2.0).

### 2.2 Drizzle schema — `apps/api/src/db/schema.ts`

Mirrors spec §8 exactly:

```ts
export const videoStatusEnum = pgEnum("video_status", [
  "uploading", "queued", "processing", "ready", "failed",
]);

export const videos = pgTable("videos", {
  id: uuid("id").primaryKey().defaultRandom(),
  title: text("title").notNull(),
  description: text("description"),
  status: videoStatusEnum("status").notNull().default("uploading"),
  sourceKey: text("source_key").notNull(),
  durationSec: numeric("duration_sec"),
  masterManifestKey: text("master_manifest_key"),
  thumbnailKey: text("thumbnail_key"),
  failureReason: text("failure_reason"),
  createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
});

export const renditions = pgTable("renditions", {
  id: uuid("id").primaryKey().defaultRandom(),
  videoId: uuid("video_id").notNull().references(() => videos.id),
  height: integer("height").notNull(),
  bitrateKbps: integer("bitrate_kbps").notNull(),
  playlistKey: text("playlist_key").notNull(),
});
```

`drizzle.config.ts` at `apps/api/` root, reading `DATABASE_URL` from env, `out:
./drizzle/migrations`, `dialect: "postgresql"`. First migration generated via
`drizzle-kit generate` and applied via `drizzle-kit migrate` against the running local
Postgres — not hand-written SQL (`CLAUDE.md` §2).

### 2.3 Nest module layout — `apps/api/src/`

```
db/
  schema.ts
  drizzle.module.ts     -- DRIZZLE token provider (Pool + drizzle(), from DATABASE_URL)
storage/
  storage.module.ts
  storage.service.ts    -- wraps S3Client; presignPut(key), publicUrl(key)
queue/
  queue.module.ts
  queue.service.ts      -- wraps a BullMQ Queue("hls-transcode"); enqueue({videoId, sourceKey})
videos/
  videos.module.ts
  videos.controller.ts  -- POST /videos, POST /videos/:id/complete, GET /videos/:id, GET /videos
  videos.service.ts
  dto/create-video.dto.ts
  dto/video-response.dto.ts
```

### 2.4 Endpoints (spec §7.1 steps 1, 3, 5 + a list endpoint for the browse page, spec §2.5)

- `POST /videos` — body `{ title, description? }` → inserts `videos` row
  (`status: "uploading"`, `sourceKey` generated as `videos/{id}/source`), returns
  `{ id, uploadUrl }` where `uploadUrl` is a presigned PUT (explicit `expiresIn`, e.g. 900s
  — matches the SDK default, stated explicitly rather than relying on the default per
  `CLAUDE.md`'s "no acceptance criterion may rely on unstated behavior" spirit).
- `POST /videos/:id/complete` — sets `status: "queued"`, enqueues
  `{ videoId, sourceKey }` onto the `hls-transcode` BullMQ queue. 404 if the video doesn't
  exist; 409 if not currently `uploading`.
- `GET /videos/:id` — full row incl. `status`, `renditions[]` once ready, `failureReason`
  if failed.
- `GET /videos?status=ready` — list for the browse page, default filters to `ready` only.

### 2.5 Env additions

No new vars beyond what `.env.example` already lists (`DATABASE_URL`, `REDIS_URL`, `S3_*`)
— this task only wires code to read them, via `@nestjs/config`.

## 3. Why

This is the smallest vertical slice that (a) proves the presigned-upload flow end to end
against real local MinIO, (b) gives the worker task something concrete to dequeue from once
it exists, and (c) gives the frontend task real endpoints to build the upload/browse/watch
UI against. Splitting DB+API from the worker (rather than one giant task) follows spec
§7.2's explicit block on worker code until the ffmpeg `[VERIFY]` is resolved — no reason to
let that block the API layer, which has no such open question.

Drizzle's stable `latest` tag over `rc` avoids pinning to a pre-1.0 API that could still
change before this project ships. AWS SDK v3's presigned-URL pattern is the standard,
current approach and works unmodified against MinIO locally and R2 in prod (both
S3-API-compatible) — no per-provider branching needed.

## 4. Affected files

| File | Change type | Notes |
|---|---|---|
| `apps/api/package.json` | edit | add drizzle-orm, pg, drizzle-kit, @aws-sdk/client-s3, @aws-sdk/s3-request-presigner, bullmq, @nestjs/config |
| `apps/api/drizzle.config.ts` | new | drizzle-kit config, reads `DATABASE_URL` |
| `apps/api/src/db/schema.ts` | new | `videos`, `renditions` tables per spec §8 |
| `apps/api/drizzle/migrations/*` | new (generated) | via `drizzle-kit generate`, not hand-written |
| `apps/api/src/db/drizzle.module.ts` | new | Pool + drizzle() provider |
| `apps/api/src/storage/*` | new | S3 client wrapper, presign helper |
| `apps/api/src/queue/*` | new | BullMQ producer wrapper |
| `apps/api/src/videos/*` | new | controller, service, DTOs |
| `apps/api/src/app.module.ts` | edit | import DrizzleModule, StorageModule, QueueModule, VideosModule |
| `apps/api/test/*` | new/edit | integration tests against real local Postgres/Redis (testcontainers or the already-running local compose stack — `[VERIFY: testcontainers vs. reusing infra/docker-compose.yml for apps/api's test env, pick one before writing tests]`) |
| `README.md` | edit | quickstart once endpoints exist |
| `docs/spec.md` | no change expected | this task implements existing spec, resolves no open question in §11 |

## 5. Verification items — resolved

- `@nestjs/config`: `ConfigModule.forRoot({ isGlobal: true })` in `AppModule`, reading a
  plain `.env` in `apps/api/` (gitignored, copied from the root `.env.example` for local
  dev) — confirmed working, no extra dotenv wiring needed.
- **Test infra decision: reuse the already-running `infra/docker-compose.yml` stack**, not
  testcontainers. Rationale: this repo already requires Docker for local dev (spec §6), so
  testcontainers' main benefit (spinning up disposable infra automatically) doesn't earn
  its keep over `pnpm infra:up` — one less dependency, and the e2e suite in
  `apps/api/test/videos.e2e-spec.ts` runs directly against local Postgres/Redis/MinIO,
  which is exactly the "no mocking DB or queue" rule in `CLAUDE.md`.
- MinIO presigned-PUT: `forcePathStyle: true` on `S3Client` confirmed required and working
  — verified end-to-end (`PUT` to a presigned URL against local MinIO returns 200, object
  lands at the expected key, `mc ls` confirms it).
- BullMQ note not anticipated at spec time: **BullMQ 6.0.0 (2026-07-30) made `ioredis` an
  optional peer dependency** instead of bundling it — added `ioredis` explicitly to
  `apps/api/package.json`; `QueueService` owns the `IORedis` connection directly
  (`maxRetriesPerRequest: null` per BullMQ's own connection docs) and closes it in
  `onModuleDestroy`.
- Drizzle's Postgres `Pool` needed an explicit `OnModuleDestroy` hook
  (`apps/api/src/db/drizzle.module.ts`'s `PgPool` class) — without it, `app.close()` left
  the pool open and Jest reported a leaked handle after the e2e suite.

## 6. Verified end-to-end (2026-08-14)

Full flow run against real local infra (not mocked): `POST /videos` → presigned PUT to
MinIO (`mc ls` confirms the object) → `POST /videos/:id/complete` (row flips to `queued`,
job lands on `bull:hls-transcode:*` in Redis, confirmed via `redis-cli KEYS`) →
`GET /videos/:id` reflects `queued` status. Error paths confirmed: 404 unknown id, 409
double-complete, 400 missing title. Same flow is codified in
`apps/api/test/videos.e2e-spec.ts` (4 tests, run via `pnpm test:e2e` from `apps/api`).
