import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Queue, Worker } from 'bullmq';
import IORedis from 'ioredis';
import { processTranscodeJob } from '../src/processor.js';
import {
  cleanupVideo,
  generateAndUploadSource,
  insertQueuedVideo,
  objectExists,
  pgClient,
  queryVideo,
  s3Client,
} from './helpers.js';

const HLS_TRANSCODE_QUEUE = 'hls-transcode';

// Full path per docs/tasks/TASK-hls-worker.md §2.9: enqueue -> shim -> Go ->
// objects in MinIO -> row is ready with renditions, against the real local
// infra stack (no mocking the queue, database, or object storage).
test('enqueue -> shim -> Go transcode -> objects in MinIO -> video row ready', async () => {
  const pg = await pgClient();
  const s3 = s3Client();
  const connection = new IORedis('redis://localhost:6379', {
    maxRetriesPerRequest: null,
  });

  const sourceKey = `worker-e2e/full-pipeline/${Date.now()}/source`;
  await generateAndUploadSource(s3, sourceKey, {
    width: 1280,
    height: 720,
    durationSec: 12,
  });

  const videoId = await insertQueuedVideo(pg, sourceKey);

  const queue = new Queue(HLS_TRANSCODE_QUEUE, { connection });
  // Jobs from a prior test run (or a previous crashed run) can linger in
  // Redis; without this, a fresh Worker picks those up too and they fail
  // with a stale videoId that cleanup already deleted.
  await queue.obliterate({ force: true });
  const worker = new Worker(HLS_TRANSCODE_QUEUE, processTranscodeJob, {
    connection,
    concurrency: 1,
  });

  try {
    await queue.add(
      'transcode',
      { videoId, sourceKey },
      { attempts: 3, backoff: { type: 'exponential', delay: 5000 } },
    );

    const completed = await new Promise<boolean>((resolve, reject) => {
      const timeout = setTimeout(
        () => reject(new Error('job did not complete within 30s')),
        30_000,
      );
      worker.on('completed', (job) => {
        if (job.id !== undefined) {
          clearTimeout(timeout);
          resolve(true);
        }
      });
      worker.on('failed', (job, err) => {
        clearTimeout(timeout);
        reject(err);
      });
    });
    assert.equal(completed, true);

    const { status, masterManifestKey } = await queryVideo(pg, videoId);
    assert.equal(status, 'ready');
    assert.equal(masterManifestKey, `videos/${videoId}/hls/master.m3u8`);

    // 12s source at 720p: ladder includes every standard rung at or below
    // the source height (never upscale) — 720p, 480p, 360p.
    for (const name of ['720p', '480p', '360p']) {
      const exists = await objectExists(
        `videos/${videoId}/hls/${name}/playlist.m3u8`,
      );
      assert.equal(exists, true, `${name} playlist should exist in MinIO`);
    }
    assert.equal(
      await objectExists(`videos/${videoId}/hls/master.m3u8`),
      true,
    );
    assert.equal(await objectExists(`videos/${videoId}/thumb.jpg`), true);

    const renditions = await pg.query(
      `SELECT height, bitrate_kbps FROM renditions WHERE video_id = $1 ORDER BY height DESC`,
      [videoId],
    );
    assert.deepEqual(
      renditions.rows.map((r) => r.height),
      [720, 480, 360],
    );
  } finally {
    await worker.close();
    await queue.close();
    await connection.quit();
    await cleanupVideo(pg, videoId);
    await pg.end();
  }
});
