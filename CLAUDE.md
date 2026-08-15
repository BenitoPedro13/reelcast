# Workflow Guidelines — Reelcast (Adaptive Streaming Platform)

> Ported from the `plexus` project's workflow (plan before you touch anything, lean on
> existing tooling while you work, treat documentation as part of the deliverable when you
> finish), trimmed to this project's smaller scope. Reelcast is a standalone portfolio case
> study, not a general media pipeline engine — it reuses that project's *process*, not its
> code or its DAG/plugin generality.

---

## 0. Project context

The design lives in `docs/spec.md`. Reelcast is a small, YouTube-shaped on-demand video
platform: upload a file, the backend transcodes it into an adaptive-bitrate HLS ladder, a
browser player watches it back and switches quality with bandwidth. See `docs/spec.md`
§1–§4 for the full problem statement, goals, and non-goals — in short: VOD only (no live
streaming), no auth/DRM/captions in v1, HLS only (no DASH).

**Status:** P0 feature-complete. Four tasks committed: scaffold (`7fc5014`), DB schema +
upload API (`79cdbf3`), the HLS worker (`3dbf0b2`), and `apps/web`'s browse/upload/watch UI
(`docs/tasks/TASK-web-playback-ui.md`) — upload now goes all the way from a browser file
picker through a real ffmpeg-produced adaptive ladder to a playing `hls.js` player that
visibly switches rendition under throttled bandwidth. `apps/api` also grew CORS, a
`PORT`/`WEB_ORIGIN` split from `apps/web`'s port, and a `S3_PUBLIC_BASE_URL`-based
key→URL mapping so the browser never sees bucket keys directly. Next up: deploy (R2 CORS +
public bucket, Railway/Vercel) or a P1 item from spec §9.

### Stack (per spec §6 — see `docs/spec.md` for full rationale)

| Layer | Choice |
|---|---|
| Frontend | Next.js (TS) + shadcn/ui + `hls.js` for adaptive playback |
| Backend API | NestJS (TS), a **separate service** from the frontend — not Next.js API routes (explicit choice, see spec §11) |
| Worker | Go + ffmpeg (shell out, never reimplement codecs), consumed via a thin `apps/worker` TS BullMQ shim — see §4 |
| Queue | Redis + BullMQ — one job type, chosen over NATS for legibility (spec §6) |
| Database | PostgreSQL + Drizzle ORM |
| Object storage | S3-compatible, public-read bucket behind a CDN (Cloudflare R2 default pick) |
| Realtime status | Polling (`GET /videos/:id`), not SSE/WebSocket — one job type doesn't earn that infra |

Version numbers and any "current" claims in this file or the spec are a snapshot, not a
pin — verify against the framework's own docs before scaffolding (§2.0).

### How to write in this repo

- **Never invent an API, codec flag, or storage-provider behavior.** Write
  `[VERIFY: what to check and where]` inline instead — spec §7.2 and §11 already carry
  several of these for the ffmpeg HLS invocation and R2 bucket/CDN setup. Resolve them
  before the code that depends on them ships, not after.
- Be specific to the point of discomfort: exact ffmpeg flags, exact bitrate targets, exact
  status enum values — no acceptance criterion may rely on "works" or "fast". Spec §10 sets
  the pattern (measured bitrate tolerance, measured time-to-ready, a real kill-and-retry
  test for the queue).

### Tests

- **Integration tests against real infra** (testcontainers: Postgres, Redis) for the API
  and queue — no mocking the database or the job queue in that layer's tests.
- **Golden fixtures for the worker** — small committed source clips, assertions on
  measurable output (rendition count, each rendition's actual resolution/bitrate via
  ffprobe within the tolerance in spec §10), not byte-equality.
- **The queue's retry guarantee is a test, not an assumption** — kill a worker mid-job in
  a test and confirm another run picks it up (spec §10, "no lost jobs" equivalent).

---

## 1. Plan before executing — write a task document first

**Rule:** Before editing or creating **any** code file, write a task document at
`docs/tasks/TASK-<slug>.md`. This applies from the first scaffold commit onward — no code
exists yet, so the initial repo scaffold gets a task doc before any file is created.

### 1.1 Required sections

1. **Current scenario** — what exists today, what's missing/blocked, concrete file/module
   names where applicable.
2. **Planned changes** — file by file, what's added/modified/removed and how it connects.
   Note alternatives considered and rejected if any.
3. **Why** — the justification, so a reviewer can agree or push back before code exists.
4. **Affected files** — a table: file, change type (new/edit/removal), notes.

### 1.2 How to apply it

- Write the document, then summarize in 2–3 lines and wait for alignment on anything
  non-trivial before writing code.
- One document per task, short kebab-case slug: `TASK-scaffold-monorepo.md`,
  `TASK-hls-worker.md`, `TASK-upload-flow.md`.
- Keep it in sync if the plan changes mid-task — it's a living record, not write-once.

---

## 2. Use CLIs, generators, and SDKs — don't write everything by hand

### 2.0 Check current docs before scaffolding anything

Before scaffolding or adding a dependency for **any** part of this stack — Next.js, Nest,
BullMQ, Drizzle, ffmpeg flags, R2/S3 SDK — check the tool's own current docs first, then
use its official CLI/generator (`pnpm create next-app@latest`, `nest new` / `nest g
resource`, the Drizzle CLI's generate/migrate commands, `go mod init`). Hand-authoring what
a generator produces correctly is the wrong default.

### 2.1 In practice

- Media operations go through ffmpeg — the worker shells out to it, never reimplements
  encoding/segmenting.
- Object storage operations via the S3-compatible SDK or `mc`-equivalent CLI, not
  hand-built requests.
- Database work via Drizzle's own migration tooling and `psql`.
- Prefer the agent's dedicated file tools over `cat`/`sed`/`awk` for reads and edits.

---

## 3. Update documentation after executing

**Rule:** Before considering a task done, update every doc the change affects.

- **`CLAUDE.md`** (this file) — if the change alters stack, architecture, or conventions.
- **`docs/spec.md`** — if the change resolves an open question (§11) or changes scope;
  update the specific section, don't just append.
- **`.env.example`** (once code exists) — every env var the code reads must be listed here.
- **`README.md`** — status line, quickstart, once there's something to run.
- Grep `docs/*.md` for names of things you changed (endpoint, table, env var) to catch
  stale references.

---

## 4. Project conventions (proposed layout)

```
apps/web       Next.js frontend — browse, watch, upload UI (TS)
apps/api       NestJS backend — video metadata, presigned uploads, job status (TS)
apps/worker    Plain TS BullMQ shim — holds the hls-transcode job lock, spawns worker/'s
               Go binary, nothing else (no official Go BullMQ client — see
               docs/tasks/TASK-hls-worker.md §2.1)
worker/        Go binary — ffprobe + ffmpeg HLS packaging + S3 upload + Postgres writes
infra/         docker-compose.yml — local Postgres + Redis + MinIO (R2 stand-in)
docs/          spec, task docs
```

- TS side: pick one package manager at scaffold time (record the choice in the scaffold
  task doc) and use workspace deps for anything shared between `apps/web` and `apps/api`
  — no copy-paste. `apps/worker` is deliberately plain TS (no Nest): it has no HTTP surface.
- Go side: standard module layout under `worker/`, `go.mod` owns versions.
- The **queue job payload** (`{ videoId, sourceKey }`) and the **videos/renditions schema**
  (spec §8) are the only contracts between the TS and Go sides — no other JSON shape
  duplicated by hand across that boundary.

### 4.1 Commit conventions

- Commit automatically once a task doc's work is complete and verified (build/lint/tests
  passing per its own scope) — don't wait to be asked for each one. Standing authorization
  scoped to work that followed the task-doc process in §1; not blanket permission for
  destructive git operations, which still need explicit confirmation.
- **Never add a `Co-Authored-By` trailer to commits in this repo.**

---

## TL;DR

Plan (`docs/tasks/TASK-<slug>.md`) → align → build with framework CLIs/generators, ffmpeg
for all media work → update `docs/spec.md`/`README.md`/`.env.example` → commit (no
`Co-Authored-By`) → done. Never broken: VOD only in v1, no invented API/codec behavior
(`[VERIFY: ...]` instead), measured success criteria per spec §10.
