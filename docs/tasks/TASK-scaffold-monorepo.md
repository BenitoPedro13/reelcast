# TASK-scaffold-monorepo

## 1. Current scenario

Spec-stage only. The repo has `CLAUDE.md`, `README.md`, and `docs/spec.md` — no code, no
package manifests, no `go.mod`, no compose file. `docs/spec.md`'s "Next step" section calls
for exactly this task doc before any scaffold file is created.

Nothing to build on yet: this task doc defines the *first* commit's shape only (repo
layout, package manager choice, `go.mod`, local docker-compose for Postgres/Redis/MinIO).
It does not scaffold `apps/web`'s or `apps/api`'s internals, write worker code, or design
the DB schema/migrations — those are separate follow-on task docs (see §3, "Explicitly out
of scope").

**Execution note:** per the user's request, this doc is plan-only. Benito will run the
scaffold commands himself; nothing below has been executed.

## 2. Planned changes

### 2.1 Repo layout

Create the directory skeleton from `CLAUDE.md` §4:

```
apps/web       Next.js frontend (TS)
apps/api       NestJS backend (TS)
worker/        Go worker
infra/         docker-compose.yml — local Postgres + Redis + MinIO (R2 stand-in)
docs/          already exists (spec.md, tasks/)
```

**Confirmed:** the generators (§2.3) run in this same task — `apps/web` and `apps/api` get
their real scaffolds now, not as empty placeholder directories deferred to a follow-on doc.

### 2.2 Package manager (TS side) — confirmed: **pnpm**

`CLAUDE.md` §4 requires picking one package manager at scaffold time and recording the
choice here.

**Confirmed: pnpm workspaces**, via a root `pnpm-workspace.yaml`:

```yaml
packages:
  - "apps/*"
```

Rationale:
- Native workspace protocol (`workspace:*`) for anything shared between `apps/web` and
  `apps/api` — `CLAUDE.md` §4 explicitly requires workspace deps over copy-paste for shared
  code (e.g. the `videos`/`renditions` types that mirror the queue payload contract).
- Disk-efficient (content-addressable store) and fast installs — relevant since this repo
  will have two independent TS app dependency trees (Next.js, NestJS) plus dev tooling.
- Default/first-class support in both `pnpm create next-app` and `nest new`'s
  `--package-manager pnpm` flag, so neither generator fights the workspace choice.

Alternative considered: npm workspaces (zero extra install, but slower and weaker
workspace-protocol ergonomics for a two-app monorepo) — rejected, not enough upside over
pnpm's defaults. Yarn Berry not considered — no reason in this repo to take on its PnP
config surface.

**Confirmed by Benito** — no further alignment needed on this point.

### 2.3 Frontend/backend scaffolds (via generators, per `CLAUDE.md` §2)

Not hand-authored — run the official generators, verifying current flags/defaults against
each tool's own docs first (`CLAUDE.md` §2.0):

- `apps/web`: `pnpm create next-app@latest apps/web` — TypeScript, App Router, Tailwind
  (needed for shadcn/ui per spec §6). `[VERIFY: current `create-next-app` flags/prompts —
  they change across major versions — before running]` **Done** — scaffolded on Next.js
  16.3.0 (Turbopack), builds clean.
- `apps/api`: `nest new apps/api --package-manager pnpm` — NestJS default TS template.
  `[VERIFY: current `nest new` CLI flags]` **Done**, builds clean. `nest new` initializes
  its own git repo by default — that nested `apps/api/.git` (zero commits) was removed
  post-scaffold so `apps/api` is tracked as part of the root repo, not a submodule-like
  gitlink.
- shadcn/ui init deferred to the frontend build task, not this scaffold — it needs actual
  UI to attach to.
- **Cleanup note:** `pnpm create next-app` also wrote its own `apps/web/pnpm-workspace.yaml`
  + `apps/web/pnpm-lock.yaml`, which would have made `apps/web` its own isolated pnpm
  workspace root instead of a member of the root one. Removed both and re-ran `pnpm
  install` from the repo root — `pnpm-lock.yaml` now has proper `importers` entries for
  both `apps/api` and `apps/web` under the single root workspace, and both apps still
  build.

### 2.4 Worker (Go)

- `worker/go.mod` via `go mod init github.com/BenitoPedro13/reelcast/worker` (module path
  inferred from the `origin` remote — confirmed by Benito).
- No source files yet beyond what `go mod init` produces — a `main.go` stub is a judgment
  call for this task vs. the worker build task; recommend leaving `worker/` as just
  `go.mod` here, since the worker's actual shape (ffprobe/ffmpeg invocation) is still
  `[VERIFY]`-flagged in spec §7.2 and shouldn't be guessed at in a scaffold commit.
- `[VERIFY: current Go version to target — `go version` locally vs. what `go.mod`'s `go`
  directive should pin]`

### 2.5 Local infra — `infra/docker-compose.yml` — done

Four services, matching spec §6/§8's stack:

- **postgres** — `postgres:16-alpine`, `reelcast`/`reelcast`/`reelcast` db/user/password
  (local dev only), port `5432`, named volume, `pg_isready` healthcheck.
- **redis** — `redis:7-alpine`, port `6379`, named volume, `redis-cli ping` healthcheck.
- **minio** — local R2 stand-in (S3-compatible API), port `9000` (API) / `9001` (console),
  named volume, `mc ready local` healthcheck (bundled `mc` binary; `curl` was removed from
  the image upstream).
  **`[VERIFY]` resolved, with a caveat:** MinIO stopped publishing free Docker Hub images
  in Oct 2025 and archived the `minio/minio` repo — community edition is now source-only,
  and the last published tag (`RELEASE.2025-09-07T16-13-09Z`) will never get another
  security patch. Confirmed with Benito to pin to that frozen tag anyway, since this
  container is local-only dev infra, not anything internet-facing — re-verify before
  reusing this compose file in any other context.
- **minio-init** — one-shot `minio/mc:RELEASE.2025-08-13T08-35-41Z` sidecar (same
  archival situation as `minio/minio`, same "fine for local dev" call), waits on `minio`'s
  healthcheck, creates the `reelcast` bucket and sets it public-read
  (`mc anonymous set download`) to mirror the public-read R2 bucket in spec §6.

All ports/credentials feed directly into `.env.example` (§2.6) so the values aren't
duplicated by hand later.

### 2.6 Root-level files

| Concern | File | Notes |
|---|---|---|
| Workspace manifest | `pnpm-workspace.yaml` | packages: `apps/*` |
| Root scripts/metadata | `package.json` | name, private:true, minimal root scripts (e.g. `dev`, `build` fan-out) — no dependencies at root beyond shared devDeps if any emerge later |
| Git ignore | `.gitignore` | **Done.** `node_modules/`, `.next/`, `dist/`/`build/`, `.env` (keeps `.env.example`), OS/editor/log noise. Compose volumes are named Docker volumes, not bind mounts, so nothing to ignore there. |
| Env template | `.env.example` | **Done.** `DATABASE_URL`, `REDIS_URL`, `S3_*` (endpoint/region/keys/bucket) — mirrors `infra/docker-compose.yml` exactly. API/worker-specific vars (that aren't just "read the compose service") get added when those apps' code reads them. |
| Node version pin | `.nvmrc` or `.node-version` | Pin to whatever Next.js/Nest's current docs recommend as of scaffold time `[VERIFY]` |

### 2.7 Explicitly out of scope for this task

- Drizzle schema/migrations (needs `apps/api` to exist first; separate task doc).
- Any actual ffmpeg/ffprobe invocation in the worker (spec §7.2's `[VERIFY]` blocks this
  until resolved).
- CI config — not mentioned in spec or `CLAUDE.md` yet; don't invent it here.
- Deploy config (Railway/Vercel) — spec §6 names the target, but wiring it up is not part
  of a *local* scaffold task.

## 3. Why

`docs/spec.md`'s own "Next step" section and `CLAUDE.md` §1 both require this exact
sequence: task doc → alignment → scaffold. Doing the repo layout, package manager choice,
`go.mod`, and local infra together in one task doc (rather than splitting into three) keeps
the first commit coherent — these four decisions are interdependent (e.g. the compose
file's Postgres port has to match what Drizzle's `apps/api` env expects later, the pnpm
workspace glob has to match the actual `apps/*` layout) and reviewing them together is
easier than three small docs that reference each other.

Generators over hand-authored boilerplate (§2.3) follows `CLAUDE.md` §2 directly — a
scaffold task is exactly the case that rule is written for.

## 4. Affected files

| File | Change type | Notes |
|---|---|---|
| `apps/web/` | new (generator output) | `pnpm create next-app@latest` |
| `apps/api/` | new (generator output) | `nest new` |
| `worker/go.mod` | new | `go mod init github.com/BenitoPedro13/reelcast/worker` |
| `infra/docker-compose.yml` | new | postgres, redis, minio services |
| `pnpm-workspace.yaml` | new | `packages: [apps/*]` |
| `package.json` (root) | new | private, workspace root scripts |
| `.gitignore` | new | node/go/env/volume excludes |
| `.env.example` | new | compose-derived vars (Postgres/Redis/MinIO) |
| `.nvmrc` / `.node-version` | new | pinned per current Next.js/Nest docs `[VERIFY]` |
| `README.md` | edit | status line + quickstart once compose/apps exist (per `CLAUDE.md` §3) |
| `docs/spec.md` | no change expected | nothing in this task resolves an open question in §11 |

## 5. Status

Confirmed by Benito on 2026-08-11:

1. **Package manager: pnpm** (§2.2).
2. **Go module path: `github.com/BenitoPedro13/reelcast/worker`** (§2.4).
3. **Generators run in this task**, not deferred (§2.1/§2.3).

**Execution is Benito's, not Claude's** — this doc stays plan-only. Remaining work before
running anything is resolving the `[VERIFY]` tags scattered through §2 against each tool's
current docs, per `CLAUDE.md` §2.0 — that's a per-command check at execution time, not
something to pre-resolve in this doc.
