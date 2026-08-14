import {
  Body,
  Controller,
  Get,
  Param,
  ParseUUIDPipe,
  Post,
  Query,
} from '@nestjs/common';
import { CreateVideoDto } from './dto/create-video.dto';
import { VideosService } from './videos.service';

@Controller('videos')
export class VideosController {
  constructor(private readonly videos: VideosService) {}

  @Post()
  create(@Body() dto: CreateVideoDto) {
    return this.videos.create(dto);
  }

  @Post(':id/complete')
  complete(@Param('id', ParseUUIDPipe) id: string) {
    return this.videos.complete(id);
  }

  @Get(':id')
  findOne(@Param('id', ParseUUIDPipe) id: string) {
    return this.videos.findOne(id);
  }

  @Get()
  findAll(
    @Query('status')
    status?: 'uploading' | 'queued' | 'processing' | 'ready' | 'failed',
  ) {
    return this.videos.findAll(status);
  }
}
