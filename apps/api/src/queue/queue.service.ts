import { Injectable, OnModuleDestroy } from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import { Queue } from 'bullmq';
import IORedis from 'ioredis';

export const HLS_TRANSCODE_QUEUE = 'hls-transcode';

export interface HlsTranscodeJob {
  videoId: string;
  sourceKey: string;
}

@Injectable()
export class QueueService implements OnModuleDestroy {
  private readonly connection: IORedis;
  private readonly queue: Queue<HlsTranscodeJob>;

  constructor(config: ConfigService) {
    this.connection = new IORedis(config.getOrThrow<string>('REDIS_URL'), {
      maxRetriesPerRequest: null,
    });
    this.queue = new Queue<HlsTranscodeJob>(HLS_TRANSCODE_QUEUE, {
      connection: this.connection,
    });
  }

  async enqueueTranscode(job: HlsTranscodeJob): Promise<void> {
    await this.queue.add('transcode', job);
  }

  async onModuleDestroy(): Promise<void> {
    await this.queue.close();
    await this.connection.quit();
  }
}
