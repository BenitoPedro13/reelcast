CREATE TYPE "public"."video_status" AS ENUM('uploading', 'queued', 'processing', 'ready', 'failed');--> statement-breakpoint
CREATE TABLE "renditions" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"video_id" uuid NOT NULL,
	"height" integer NOT NULL,
	"bitrate_kbps" integer NOT NULL,
	"playlist_key" text NOT NULL
);
--> statement-breakpoint
CREATE TABLE "videos" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"title" text NOT NULL,
	"description" text,
	"status" "video_status" DEFAULT 'uploading' NOT NULL,
	"source_key" text NOT NULL,
	"duration_sec" numeric,
	"master_manifest_key" text,
	"thumbnail_key" text,
	"failure_reason" text,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL
);
--> statement-breakpoint
ALTER TABLE "renditions" ADD CONSTRAINT "renditions_video_id_videos_id_fk" FOREIGN KEY ("video_id") REFERENCES "public"."videos"("id") ON DELETE no action ON UPDATE no action;