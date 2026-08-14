import { Module } from '@nestjs/common';
import { QueueModule } from '../queue/queue.module';
import { StorageModule } from '../storage/storage.module';
import { VideosController } from './videos.controller';
import { VideosService } from './videos.service';

@Module({
  imports: [StorageModule, QueueModule],
  controllers: [VideosController],
  providers: [VideosService],
})
export class VideosModule {}
