import {
  ConflictException,
  Inject,
  Injectable,
  NotFoundException,
} from '@nestjs/common';
import { randomUUID } from 'node:crypto';
import { eq } from 'drizzle-orm';
import { DRIZZLE, type DrizzleDb } from '../db/drizzle.module';
import { renditions, videos, type Video } from '../db/schema';
import { QueueService } from '../queue/queue.service';
import { StorageService } from '../storage/storage.service';
import { CreateVideoDto } from './dto/create-video.dto';

@Injectable()
export class VideosService {
  constructor(
    @Inject(DRIZZLE) private readonly db: DrizzleDb,
    private readonly storage: StorageService,
    private readonly queue: QueueService,
  ) {}

  async create(
    dto: CreateVideoDto,
  ): Promise<{ id: string; uploadUrl: string }> {
    const id = randomUUID();
    const sourceKey = `videos/${id}/source`;

    await this.db.insert(videos).values({
      id,
      title: dto.title,
      description: dto.description,
      sourceKey,
    });

    const uploadUrl = await this.storage.presignPut(sourceKey);
    return { id, uploadUrl };
  }

  async complete(id: string): Promise<Video> {
    const video = await this.getOrThrow(id);
    if (video.status !== 'uploading') {
      throw new ConflictException(
        `Video ${id} is not awaiting upload completion (status: ${video.status})`,
      );
    }

    const [updated] = await this.db
      .update(videos)
      .set({ status: 'queued' })
      .where(eq(videos.id, id))
      .returning();

    await this.queue.enqueueTranscode({
      videoId: video.id,
      sourceKey: video.sourceKey,
    });

    return updated;
  }

  async findOne(id: string) {
    const video = await this.getOrThrow(id);
    const videoRenditions = await this.db
      .select()
      .from(renditions)
      .where(eq(renditions.videoId, id));

    return { ...video, renditions: videoRenditions };
  }

  async findAll(status: Video['status'] = 'ready'): Promise<Video[]> {
    return this.db.select().from(videos).where(eq(videos.status, status));
  }

  private async getOrThrow(id: string): Promise<Video> {
    const [video] = await this.db
      .select()
      .from(videos)
      .where(eq(videos.id, id));

    if (!video) {
      throw new NotFoundException(`Video ${id} not found`);
    }

    return video;
  }
}
