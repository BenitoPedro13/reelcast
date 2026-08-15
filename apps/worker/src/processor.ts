import { spawn } from 'node:child_process';
import type { Job } from 'bullmq';

export interface HlsTranscodeJob {
  videoId: string;
  sourceKey: string;
}

// Holds the BullMQ job lock and does nothing else — all media, storage, and
// database work happens in the Go binary. See
// docs/tasks/TASK-hls-worker.md §2.1/§2.2 for why this split exists (no
// official Go BullMQ client) and §2.7 for why --final-attempt matters: a
// video must not show `failed` in the UI while BullMQ still has retries
// queued, so only the last configured attempt is allowed to write that
// status.
export async function processTranscodeJob(
  job: Job<HlsTranscodeJob>,
): Promise<void> {
  const goWorkerBin = requireEnv('GO_WORKER_BIN');
  const isFinalAttempt = job.attemptsMade + 1 >= (job.opts.attempts ?? 1);

  const args = [
    '--video-id',
    job.data.videoId,
    '--source-key',
    job.data.sourceKey,
  ];
  if (isFinalAttempt) {
    args.push('--final-attempt');
  }

  await new Promise<void>((resolve, reject) => {
    const child = spawn(goWorkerBin, args, {
      stdio: ['ignore', 'ignore', 'pipe'],
    });

    child.stderr.on('data', (chunk: Buffer) => {
      process.stderr.write(`[transcode ${job.id}] ${chunk.toString()}`);
    });

    child.on('error', (err) => reject(err));
    child.on('exit', (code) => {
      if (code === 0) {
        resolve();
      } else {
        reject(new Error(`transcode binary exited with code ${code}`));
      }
    });
  });
}

function requireEnv(name: string): string {
  const value = process.env[name];
  if (!value) {
    throw new Error(`${name} is not set`);
  }
  return value;
}
