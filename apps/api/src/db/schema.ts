import {
  integer,
  numeric,
  pgEnum,
  pgTable,
  text,
  timestamp,
  uuid,
} from 'drizzle-orm/pg-core';

export const videoStatusEnum = pgEnum('video_status', [
  'uploading',
  'queued',
  'processing',
  'ready',
  'failed',
]);

export const videos = pgTable('videos', {
  id: uuid('id').primaryKey().defaultRandom(),
  title: text('title').notNull(),
  description: text('description'),
  status: videoStatusEnum('status').notNull().default('uploading'),
  sourceKey: text('source_key').notNull(),
  durationSec: numeric('duration_sec'),
  masterManifestKey: text('master_manifest_key'),
  thumbnailKey: text('thumbnail_key'),
  failureReason: text('failure_reason'),
  createdAt: timestamp('created_at', { withTimezone: true })
    .notNull()
    .defaultNow(),
});

export const renditions = pgTable('renditions', {
  id: uuid('id').primaryKey().defaultRandom(),
  videoId: uuid('video_id')
    .notNull()
    .references(() => videos.id),
  height: integer('height').notNull(),
  bitrateKbps: integer('bitrate_kbps').notNull(),
  playlistKey: text('playlist_key').notNull(),
});

export type Video = typeof videos.$inferSelect;
export type NewVideo = typeof videos.$inferInsert;
export type Rendition = typeof renditions.$inferSelect;
export type NewRendition = typeof renditions.$inferInsert;
