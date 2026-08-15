import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { Queue, Worker } from 'bullmq';
import IORedis from 'ioredis';
import { processTranscodeJob } from '../src/processor.js';
import {
  cleanupVideo,
  generateAndUploadSource,
  insertQueuedVideo,
  pgClient,
  poll,
  queryVideo,
  s3Client,
} from './helpers.js';

const execFileAsync = promisify(execFile);
const HLS_TRANSCODE_QUEUE = 'hls-transcode';

async function findTranscodePid(videoId: string): Promise<number | undefined> {
  try {
    const { stdout } = await execFileAsync('pgrep', [
      '-f',
      `bin/transcode --video-id ${videoId}`,
    ]);
    const pid = parseInt(stdout.trim().split('\n')[0], 10);
    return Number.isNaN(pid) ? undefined : pid;
  } catch {
    return undefined; // pgrep exits 1 when no match
  }
}

async function killStrayFfmpeg(): Promise<void> {
  try {
    await execFileAsync('pkill', ['-9', '-f', 'ffmpeg.*reelcast-transcode']);
  } catch {
    // no matching process — fine
  }
}

// spec §10's kill test, per docs/tasks/TASK-hls-worker.md §2.9: kill the Go
// child mid-encode and confirm BullMQ retries a subsequent attempt through
// to `ready`, and that the row never shows `failed` in between (only the
// shim's final --final-attempt flag is allowed to write that).
test('kill test: BullMQ retries after the Go child is killed mid-encode; status never shows failed', async () => {
  const pg = await pgClient();
  const s3 = s3Client();
  const connection = new IORedis('redis://localhost:6379', {
    maxRetriesPerRequest: null,
  });

  // Heavy enough (1080p, full 4-rendition ladder) that the encode has a
  // multi-second window to kill into, per the ~4.6s wall time measured in
  // docs/tasks/TASK-hls-worker.md §5.
  const sourceKey = `worker-e2e/kill-retry/${Date.now()}/source`;
  await generateAndUploadSource(s3, sourceKey, {
    width: 1920,
    height: 1080,
    durationSec: 20,
  });

  const videoId = await insertQueuedVideo(pg, sourceKey);

  const queue = new Queue(HLS_TRANSCODE_QUEUE, { connection });
  await queue.obliterate({ force: true });
  const worker = new Worker(HLS_TRANSCODE_QUEUE, processTranscodeJob, {
    connection,
    concurrency: 1,
  });

  const observedStatuses = new Set<string>();
  const statusWatcher = setInterval(() => {
    void queryVideo(pg, videoId).then(({ status }) =>
      observedStatuses.add(status),
    );
  }, 200);

  try {
    // Two attempts, fast fixed backoff — this test is about retry
    // mechanics, not the production backoff policy (that's asserted
    // directly on QueueService in apps/api).
    await queue.add(
      'transcode',
      { videoId, sourceKey },
      { attempts: 2, backoff: { type: 'fixed', delay: 500 } },
    );

    const pid = await poll(() => findTranscodePid(videoId), {
      timeoutMs: 5_000,
      intervalMs: 100,
    });
    // Give ffmpeg a moment to actually start encoding before the kill, not
    // just the Go process to spawn.
    await new Promise((resolve) => setTimeout(resolve, 800));
    process.kill(pid, 'SIGKILL');
    // SIGKILL on the Go parent doesn't touch its ffmpeg child (no signal
    // propagation, and the deferred tmp-dir cleanup never runs) — reap it
    // immediately so it isn't still burning CPU during the retry's encode.
    await killStrayFfmpeg();

    const completed = await new Promise<boolean>((resolve, reject) => {
      const timeout = setTimeout(
        () => reject(new Error('job did not complete within 30s')),
        30_000,
      );
      worker.on('completed', () => {
        clearTimeout(timeout);
        resolve(true);
      });
    });
    assert.equal(completed, true);

    const { status } = await queryVideo(pg, videoId);
    assert.equal(status, 'ready');
    assert.equal(
      observedStatuses.has('failed'),
      false,
      `video must never show 'failed' while a retry is pending, observed: ${[...observedStatuses]}`,
    );
    assert.equal(
      observedStatuses.has('processing'),
      true,
      'expected to observe processing at least once across the two attempts',
    );
  } finally {
    clearInterval(statusWatcher);
    await worker.close();
    await queue.close();
    await connection.quit();
    await killStrayFfmpeg();
    await cleanupVideo(pg, videoId);
    await pg.end();
  }
});
