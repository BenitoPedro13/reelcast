import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Client as PgClient } from 'pg';
import { S3Client, PutObjectCommand } from '@aws-sdk/client-s3';

const execFileAsync = promisify(execFile);

// Mirrors infra/docker-compose.yml / .env.example — the real local stack,
// not a mock, per this repo's "integration tests against real infra" rule.
export const TEST_DATABASE_URL =
  'postgresql://reelcast:reelcast@localhost:5432/reelcast';

export function s3Client(): S3Client {
  return new S3Client({
    endpoint: 'http://localhost:9000',
    region: 'us-east-1',
    forcePathStyle: true,
    credentials: {
      accessKeyId: 'minioadmin',
      secretAccessKey: 'minioadmin123',
    },
  });
}

export async function pgClient(): Promise<PgClient> {
  const client = new PgClient({ connectionString: TEST_DATABASE_URL });
  await client.connect();
  return client;
}

export async function insertQueuedVideo(
  pg: PgClient,
  sourceKey: string,
): Promise<string> {
  const result = await pg.query<{ id: string }>(
    `INSERT INTO videos (title, status, source_key) VALUES ('e2e test video', 'queued', $1) RETURNING id`,
    [sourceKey],
  );
  return result.rows[0].id;
}

export async function cleanupVideo(pg: PgClient, videoId: string) {
  await pg.query(`DELETE FROM renditions WHERE video_id = $1`, [videoId]);
  await pg.query(`DELETE FROM videos WHERE id = $1`, [videoId]);
}

export async function queryVideo(
  pg: PgClient,
  videoId: string,
): Promise<{ status: string; masterManifestKey: string | null }> {
  const result = await pg.query<{
    status: string;
    master_manifest_key: string | null;
  }>(`SELECT status, master_manifest_key FROM videos WHERE id = $1`, [
    videoId,
  ]);
  return {
    status: result.rows[0].status,
    masterManifestKey: result.rows[0].master_manifest_key,
  };
}

// Generates a synthetic source clip via ffmpeg's lavfi inputs (fixture
// generated at test time, not committed as a binary) and uploads it.
export async function generateAndUploadSource(
  s3: S3Client,
  key: string,
  opts: { width: number; height: number; durationSec: number },
): Promise<void> {
  const dir = await mkdtemp(join(tmpdir(), 'reelcast-worker-e2e-'));
  const localPath = join(dir, 'source.mp4');

  try {
    await execFileAsync('ffmpeg', [
      '-y',
      '-f',
      'lavfi',
      '-i',
      `testsrc2=size=${opts.width}x${opts.height}:duration=${opts.durationSec}:rate=30`,
      '-f',
      'lavfi',
      '-i',
      `sine=frequency=1000:duration=${opts.durationSec}`,
      '-c:v',
      'libx264',
      '-c:a',
      'aac',
      '-shortest',
      localPath,
    ]);

    const { readFile } = await import('node:fs/promises');
    const body = await readFile(localPath);
    await s3.send(
      new PutObjectCommand({ Bucket: 'reelcast', Key: key, Body: body }),
    );
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
}

// The bucket is anonymous-download (infra/docker-compose.yml's minio-init),
// so a plain HTTP GET is enough to confirm an uploaded object exists.
export async function objectExists(key: string): Promise<boolean> {
  const res = await fetch(`http://localhost:9000/reelcast/${key}`);
  return res.ok;
}

export function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export async function poll<T>(
  fn: () => Promise<T | undefined>,
  { timeoutMs, intervalMs }: { timeoutMs: number; intervalMs: number },
): Promise<T> {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const result = await fn();
    if (result !== undefined) return result;
    if (Date.now() > deadline) {
      throw new Error(`poll: timed out after ${timeoutMs}ms`);
    }
    await sleep(intervalMs);
  }
}
