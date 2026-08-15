import { Worker } from 'bullmq';
import IORedis from 'ioredis';
import { processTranscodeJob } from './processor.js';

const HLS_TRANSCODE_QUEUE = 'hls-transcode';

function requireEnv(name: string): string {
  const value = process.env[name];
  if (!value) {
    throw new Error(`${name} is not set`);
  }
  return value;
}

const connection = new IORedis(requireEnv('REDIS_URL'), {
  maxRetriesPerRequest: null,
});

// concurrency: 1 — each job already saturates the CPU across simultaneous
// renditions (measured 645% CPU in docs/tasks/TASK-hls-worker.md §5), so a
// second concurrent job would only add contention, not throughput.
const worker = new Worker(HLS_TRANSCODE_QUEUE, processTranscodeJob, {
  connection,
  concurrency: 1,
});

worker.on('completed', (job) => {
  console.log(`[hls-transcode] job ${job.id} completed`);
});

worker.on('failed', (job, err) => {
  console.error(`[hls-transcode] job ${job?.id} failed: ${err.message}`);
});

async function shutdown(): Promise<void> {
  await worker.close();
  await connection.quit();
  process.exit(0);
}

process.on('SIGTERM', () => void shutdown());
process.on('SIGINT', () => void shutdown());
