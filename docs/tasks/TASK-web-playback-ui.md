# TASK-web-playback-ui

## 1. Current scenario

The backend half of P0 is done and committed: scaffold (`7fc5014`), DB schema + upload API
(`79cdbf3`), HLS worker (`3dbf0b2`). A `POST /videos` → presigned PUT → `POST
/videos/:id/complete` → BullMQ → `apps/worker` shim → Go binary run now ends with a real
adaptive ladder in object storage and `status: ready` in Postgres:

```
videos/<id>/source                       source upload
videos/<id>/hls/master.m3u8              master manifest  (videos.master_manifest_key)
videos/<id>/hls/<name>/playlist.m3u8     per-rendition    (renditions.playlist_key, name = "1080p" | "720p" | "480p" | "360p")
videos/<id>/hls/<name>/seg%05d.ts        segments
videos/<id>/thumb.jpg                    thumbnail        (videos.thumbnail_key)
```

What's missing is everything a human can see — spec §2 items **1** (upload from the
browser), **4** (uploader watches status without a page reload), and **5** (browse page +
watch page that visibly switches rendition). Items 2, 3, 6 are done.

`apps/web` is still an untouched `create-next-app`: Next `16.3.0`, React `19.2.8`, Tailwind
v4 (`@tailwindcss/postcss`), React Compiler on via `next.config.ts`, and `app/page.tsx` is
the stock template. No shadcn/ui, no `hls.js`, no API client, no `.env.local`. Nothing in
the repo consumes `GET /videos`.

Four concrete blockers sit between that scaffold and a working page, all of them in
`apps/api` or config, not in the UI:

1. **The API returns storage keys, not URLs.** `GET /videos/:id` gives
   `masterManifestKey: "videos/<id>/hls/master.m3u8"`. A browser can't load that; something
   has to know the bucket's public base URL.
2. **No CORS.** `apps/api/src/main.ts` is the bare bootstrap — no `enableCors()`, so every
   `fetch` from the web origin fails.
3. **Port collision.** The API listens on `process.env.PORT ?? 3000`, and `next dev`
   defaults to 3000 too. They cannot both run as configured.
4. **`GET /videos` defaults to `status: 'ready'`** (`videos.service.ts:72`) — right for the
   browse page, but it means the upload page must poll `GET /videos/:id` for its own
   in-flight video, which it can (that endpoint returns the row plus its renditions).

Two things that are **not** blockers, verified rather than assumed:

- **MinIO CORS is already permissive.** Measured 2026-08-15 against the running
  `infra-minio-1` (`RELEASE.2025-09-07T16-13-09Z`): a preflight
  `OPTIONS /reelcast/videos/test/source` with `Origin: http://localhost:3000` +
  `Access-Control-Request-Method: PUT` returns `204` with
  `Access-Control-Allow-Origin: http://localhost:3000`, and an anonymous `GET` with an
  `Origin` header reflects the origin plus a wildcard `Access-Control-Expose-Headers`. So
  the browser's presigned PUT and `hls.js`'s segment GETs both work locally with no compose
  change. `[VERIFY: R2 CORS policy at deploy time — R2 does not reflect arbitrary origins
  by default; check Cloudflare's current R2 CORS docs before the first deploy, not now.]`
- **The bucket is already public-read** (`mc anonymous set download local/reelcast` in
  `infra/docker-compose.yml`), matching spec §6's public-read + CDN model, so manifests and
  segments are fetchable unsigned.

## 2. Planned changes

### 2.1 Dependencies (check current docs before adding, per `CLAUDE.md` §2.0)

- **`hls.js@1.7.0`** (`latest` dist-tag as of 2026-08-15; `1.7.0-rc.3`/canary exist — take
  the stable one) in `apps/web`. Spec §6's stated player.
  `[VERIFY: hls.js 1.7 API surface before writing the component — Hls.isSupported(),
  Events.MANIFEST_PARSED / LEVEL_SWITCHED payload shape, hls.levels[].height/bitrate,
  hls.currentLevel/nextLevel semantics — against hls.js's own current API docs, not memory.]`
- **shadcn/ui** via `pnpm dlx shadcn@latest init` in `apps/web` (spec §6), then
  `shadcn add` for only the primitives used: `button`, `card`, `input`, `label`,
  `textarea`, `progress`, `badge`, `sonner`. `[VERIFY: shadcn CLI 4.18.0's Next 16 +
  Tailwind v4 support and the exact init flags/component names against shadcn's current
  docs — do not hand-author these components.]`
- No new dependency in `apps/api`.

Per `apps/web/AGENTS.md`, read the relevant guide under
`apps/web/node_modules/next/dist/docs/01-app/` before writing components — this Next major
may differ from training data (route props typing, `fetch` caching defaults, `next/image`
config for remote hosts are the three most likely to bite).

### 2.2 `apps/api` — hand the browser URLs, not keys

**`src/storage/storage.service.ts`** — add a public-URL helper alongside `presignPut`:

```ts
publicUrl(key: string): string   // `${S3_PUBLIC_BASE_URL}/${key}`
```

reading a new `S3_PUBLIC_BASE_URL` (local: `http://localhost:9000/reelcast`; prod: the R2
public/custom-domain origin). Separate from `S3_ENDPOINT` on purpose — in production the
CDN hostname the browser reads from is not the S3 API endpoint the SDK signs against, and
spec §11's open CDN question is exactly about that split.

**`src/videos/videos.service.ts`** — `findOne` and `findAll` map rows through a small
serializer that adds, next to the existing key fields:

- `masterManifestUrl: string | null` (null until `ready`)
- `thumbnailUrl: string | null`

Renditions in `findOne` keep `height`/`bitrateKbps` (the player's quality labels come from
`hls.js`'s parsed levels, not from these — these are for the "what got produced" list on
the watch page).

*Alternative rejected:* let `apps/web` build URLs from a `NEXT_PUBLIC_S3_PUBLIC_BASE_URL` +
the key. That duplicates the storage layout across the TS/Go boundary contract described in
`CLAUDE.md` §4 and puts bucket topology in a client bundle; the API already owns storage
config.

**`src/main.ts`** — two lines:

- `app.enableCors({ origin: process.env.WEB_ORIGIN ?? 'http://localhost:3000' })`
- listen on `process.env.PORT ?? 3001`, resolving blocker 3 (web keeps 3000, the port
  `next dev` and every Next tutorial assume).

**`test/videos.e2e-spec.ts`** — extend the existing real-infra e2e: assert `POST /videos`
still returns a usable presigned URL, and that `GET /videos/:id` now carries
`masterManifestUrl`/`thumbnailUrl` (null for a fresh `uploading` row). A separate case
asserts the CORS preflight: `OPTIONS /videos` with an `Origin` header returns
`access-control-allow-origin`.

### 2.3 `apps/web` — the three pages

**`lib/api.ts`** — one typed client module, `NEXT_PUBLIC_API_URL` based, exporting
`createVideo`, `completeVideo`, `getVideo`, `listVideos` and the `Video`/`Rendition` types
mirroring the API's response shape (hand-written types; there's no shared package and
generating an OpenAPI client for four endpoints is not worth the machinery — noted so the
choice is deliberate).

**`app/page.tsx` — browse** (server component). `listVideos()` → card grid: thumbnail
(`thumbnailUrl`), title, duration badge from `durationSec`, link to `/watch/<id>`. Empty
state links to `/upload`. `[VERIFY: Next 16 fetch caching default for server-component
fetches — this list must not be statically cached across uploads; set the documented
no-store/dynamic option rather than assuming 15's behavior carried over.]`

**`app/upload/page.tsx` — upload** (client component). Drives spec §7.1 steps 1–3 and 5:

1. Form: file input (`accept="video/*"`), title, optional description.
2. `POST /videos` → `{ id, uploadUrl }`.
3. `PUT` the file to `uploadUrl` via `XMLHttpRequest` (not `fetch`) — `xhr.upload.onprogress`
   is the only way to drive a real percentage bar; `fetch` has no upload-progress event.
   Rendered with shadcn `progress`.
4. `POST /videos/:id/complete`.
5. Poll `GET /videos/:id` every **2s** until `ready` or `failed`, rendering a stepper over
   the status enum (`uploading → queued → processing → ready`), `failureReason` on failure.
   Polling (not SSE) is spec §6's deliberate choice; the interval is stated here so it's a
   decision on record, and polling stops on terminal status and on unmount.
6. On `ready`: a link/redirect to `/watch/<id>`.

**`app/watch/[id]/page.tsx` — watch** (server component). Fetches the video, renders title,
description, duration, the produced-rendition list, and `<HlsPlayer>`. `notFound()` for a
missing id; for a non-`ready` video, render the same status view as the upload page instead
of a dead player.

**`components/hls-player.tsx`** (client component) — the demo moment, so it does more than
`new Hls()`:

- `Hls.isSupported()` → attach to `<video>`; else if
  `video.canPlayType('application/vnd.apple.mpegurl')` → set `src` directly (Safari plays
  HLS natively and hls.js reports unsupported there, per spec §6).
- An always-visible **current rendition indicator** (e.g. `720p · 2800 kbps`) driven by
  `LEVEL_SWITCHED`, plus a small **quality selector** (`Auto` + each parsed level) that sets
  `hls.currentLevel`. Spec §10's success criterion is *observing* the drop under throttling;
  a UI that shows the active level is what makes that observable instead of anecdotal.
- Cleanup: `hls.destroy()` on unmount. Error surface: `Hls.Events.ERROR` fatal →
  visible message, not a silent black box.

**`app/layout.tsx`** — replace the `create-next-app` metadata (`title: "Create Next App"`)
and add a minimal header (Reelcast → browse, Upload link).

### 2.4 Config and docs

- `apps/api/.env` + root `.env.example`: `PORT=3001`, `WEB_ORIGIN=http://localhost:3000`,
  `S3_PUBLIC_BASE_URL=http://localhost:9000/reelcast`.
- `apps/web/.env.local` (gitignored) + `.env.example`:
  `NEXT_PUBLIC_API_URL=http://localhost:3001`.
- `README.md`: run order (`pnpm infra:up` → `go build` the worker → `apps/api` → `apps/worker`
  → `apps/web`), and a short **demo script** for the throttling moment (open a watch page,
  devtools Network → throttle, observe the indicator drop, un-throttle, observe it climb).
- `docs/spec.md`: mark §2 items 1/4/5 done in the status line; record the resolved public-URL
  decision in §6's storage row and §11's CDN question (local answer: public-read bucket
  origin via `S3_PUBLIC_BASE_URL`; R2/CDN still open for deploy).
- `CLAUDE.md`: status paragraph — P0 feature-complete, next up is deploy/P1.

### 2.5 Verification

- `apps/api`: `pnpm test:e2e` (real Postgres/Redis/MinIO via `infra/docker-compose.yml`, as
  today) with the extended assertions above; `pnpm build`; `pnpm lint`.
- `apps/web`: `pnpm build` and `pnpm lint` must pass (build catches the Next 16 API drift
  the `[VERIFY]`s above are about).
- **Playwright e2e** (`apps/web/e2e/upload-watch.spec.ts`, `pnpm test:e2e`) against the real
  stack — infra up, API and `apps/worker` running, a small committed source clip uploaded
  through the actual UI. Asserts: the status view reaches `ready` without a reload, the
  watch page's `<video>` advances `currentTime` past 0, and the player reports a parsed
  level matching a `renditions` row. This is the repeatable half.
  `[VERIFY: Playwright's current install/config for Next 16 + pnpm workspaces — use
  `pnpm create playwright` rather than hand-writing playwright.config.ts, per CLAUDE.md §2.0.]`
- **Manual browser drive for the throttling moment**, which Playwright can't stage
  convincingly (CDP throttling mid-playback asserted in code proves the API call, not the
  visible switch): run the full stack, play a clip, throttle the network, capture the
  rendition indicator before/after. Screenshots recorded in this doc's §5 — same
  "measured, not eyeballed" bar as the worker's bitrate tolerance.

## 3. Why

- **This is the last P0 slice, and the only one a visitor sees.** The pipeline's evidence
  today is Go tests and Postgres rows; spec §1 promises a page where quality visibly changes
  under throttling. Everything else in P0 exists to make this page possible.
- **The API changes are small and belong to this task, not a separate one.** CORS, the port,
  and key→URL mapping are each a handful of lines, and each is only observable through the
  UI — splitting them into their own commit would produce a change nothing exercises.
- **URL construction in the API keeps the storage layout on one side of the boundary.**
  `CLAUDE.md` §4 says the job payload and the schema are the only cross-boundary contracts;
  bucket key structure shouldn't become a third one, in a client bundle.
- **The rendition indicator is a spec requirement, not decoration.** Spec §10 grades
  adaptive switching by observation; without an on-screen level readout there's nothing to
  observe but vibes.
- **Two-second polling, stated explicitly**, because spec §6 chose polling over SSE and the
  interval is the whole substance of that choice.

## 4. Affected files

| File | Change | Notes |
|---|---|---|
| `apps/api/src/main.ts` | edit | `enableCors({ origin: WEB_ORIGIN })`; default port 3000 → 3001 |
| `apps/api/src/storage/storage.service.ts` | edit | `publicUrl(key)` from new `S3_PUBLIC_BASE_URL` |
| `apps/api/src/videos/videos.service.ts` | edit | serialize `masterManifestUrl` / `thumbnailUrl` in `findOne`/`findAll` |
| `apps/api/test/videos.e2e-spec.ts` | edit | assert new URL fields + CORS preflight header |
| `apps/api/.env` | edit | `PORT`, `WEB_ORIGIN`, `S3_PUBLIC_BASE_URL` |
| `apps/web/package.json` | edit | `hls.js@1.7.0`, shadcn's deps (added by its CLI) |
| `apps/web/components.json`, `lib/utils.ts`, `components/ui/*` | new | generated by `shadcn init`/`add` — not hand-authored |
| `apps/web/lib/api.ts` | new | typed API client + `Video`/`Rendition` types |
| `apps/web/app/page.tsx` | edit | stock template → browse grid |
| `apps/web/app/layout.tsx` | edit | real metadata + header nav |
| `apps/web/app/upload/page.tsx` | new | form → presigned PUT (XHR progress) → complete → 2s status poll |
| `apps/web/app/watch/[id]/page.tsx` | new | video detail + player, non-`ready` fallback |
| `apps/web/components/hls-player.tsx` | new | hls.js attach, Safari native fallback, level indicator + selector |
| `apps/web/.env.local` | new | `NEXT_PUBLIC_API_URL` (gitignored) |
| `apps/web/playwright.config.ts` | new | generated by `pnpm create playwright`, not hand-authored |
| `apps/web/e2e/upload-watch.spec.ts` | new | upload → `ready` → playback against real infra |
| `apps/web/e2e/fixtures/*.mp4` | new | small committed source clip (reuse the worker's golden fixture if suitable) |
| `.env.example` | edit | the four new vars above |
| `README.md` | edit | run order + throttling demo script |
| `docs/spec.md` | edit | §2 status, §6/§11 public-URL decision |
| `CLAUDE.md` | edit | status paragraph |

## 5. Evidence

- `apps/api`: `pnpm build`, `pnpm lint`, `pnpm test:e2e` all pass (5/5), including the new
  `masterManifestUrl`/`thumbnailUrl`-null assertion and the CORS preflight assertion, against
  real Postgres/Redis/MinIO.
- `apps/web`: `pnpm build` and `pnpm lint` pass. `/` and `/watch/[id]` are dynamic (`ƒ`) —
  confirms `cache: 'no-store'` on `listVideos`/`getVideo` actually defeats static
  prerendering, resolving that `[VERIFY]`.
- **Playwright** (`apps/web/e2e/upload-watch.spec.ts`) passes against the real stack: drives
  the actual upload form with a committed synthetic fixture
  (`apps/web/e2e/fixtures/sample.mp4`, generated the same way the Go tests generate their
  fixtures — `ffmpeg -f lavfi testsrc2`/`sine`, not hand-authored), the status stepper's
  `data-status` attribute reaches `ready` under real ffmpeg transcoding, the redirect to
  `/watch/:id` is a client-side navigation (no reload), `<video>.currentTime` advances past
  `0` after `play()`, and the on-screen rendition indicator's parsed height is cross-checked
  against that video's actual `renditions` rows via a direct API call from the test.
- **Manual throttling drive: not captured this session.** First attempt used the `repro`
  fixture video (12s source → 3× 4s segments per rendition); `hls.js`'s default buffering
  (~30s ahead) very likely fetched the entire ladder before DevTools throttling was applied,
  so there was nothing left to switch mid-playback — not a bug in the indicator/selector, a
  clip-length problem. Fix for next attempt: use a source of at least 60s (15+ segments per
  rendition) so there's buffer left to react to throttling after a few seconds of playback —
  README.md's demo script now says so. Screenshots still owed before this task is fully
  closed out.
