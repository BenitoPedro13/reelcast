// Command transcode is the Go binary spawned by apps/worker (the TS BullMQ
// shim) for each hls-transcode job. It owns all media, storage, and
// database work; the shim owns only the queue lock. Contract:
//
//	--video-id, --source-key   required; identify the job's video row and
//	                           its uploaded source object.
//	--final-attempt            set by the shim when this is BullMQ's last
//	                           configured attempt for the job — only then
//	                           does a failure get written to Postgres as
//	                           `failed` (see internal/store).
//
// Exit 0 on success; non-zero on any failure so BullMQ records it and
// retries (unless this was the final attempt).
//
// See docs/tasks/TASK-hls-worker.md §2.7 for the status-transition and
// idempotency contract this orchestrates.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BenitoPedro13/reelcast/worker/internal/hls"
	"github.com/BenitoPedro13/reelcast/worker/internal/ladder"
	"github.com/BenitoPedro13/reelcast/worker/internal/probe"
	"github.com/BenitoPedro13/reelcast/worker/internal/storage"
	"github.com/BenitoPedro13/reelcast/worker/internal/store"
)

func main() {
	videoID := flag.String("video-id", "", "video row id (required)")
	sourceKey := flag.String("source-key", "", "object key of the uploaded source (required)")
	finalAttempt := flag.Bool("final-attempt", false, "set when this is BullMQ's last attempt for the job")
	flag.Parse()

	if *videoID == "" || *sourceKey == "" {
		fmt.Fprintln(os.Stderr, "transcode: --video-id and --source-key are required")
		os.Exit(2)
	}

	if err := run(context.Background(), *videoID, *sourceKey, *finalAttempt); err != nil {
		fmt.Fprintf(os.Stderr, "transcode: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, videoID, sourceKey string, finalAttempt bool) error {
	db, err := store.Connect(ctx, requireEnv("DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer db.Close(ctx)

	s3 := storage.New(storage.Config{
		Endpoint:        requireEnv("S3_ENDPOINT"),
		Region:          requireEnv("S3_REGION"),
		AccessKeyID:     requireEnv("S3_ACCESS_KEY_ID"),
		SecretAccessKey: requireEnv("S3_SECRET_ACCESS_KEY"),
		Bucket:          requireEnv("S3_BUCKET"),
	})

	if err := db.MarkProcessing(ctx, videoID); err != nil {
		return fmt.Errorf("mark processing: %w", err)
	}

	if err := transcode(ctx, db, s3, videoID, sourceKey); err != nil {
		if finalAttempt {
			if markErr := db.MarkFailed(ctx, videoID, err.Error()); markErr != nil {
				return fmt.Errorf("%w (also failed to mark video failed: %v)", err, markErr)
			}
		}
		return err
	}

	return nil
}

func transcode(ctx context.Context, db *store.Store, s3 *storage.Client, videoID, sourceKey string) error {
	tmpDir, err := os.MkdirTemp("", "reelcast-transcode-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	sourcePath := filepath.Join(tmpDir, "source")
	if err := s3.DownloadFile(ctx, sourceKey, sourcePath); err != nil {
		return fmt.Errorf("download source: %w", err)
	}

	info, err := probe.Probe(ctx, sourcePath)
	if err != nil {
		return fmt.Errorf("probe source: %w", err)
	}

	renditions := ladder.Select(info.Height)

	outDir := filepath.Join(tmpDir, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	if out, err := hls.Encode(ctx, sourcePath, outDir, renditions, info.HasAudio); err != nil {
		return fmt.Errorf("encode: %w\n%s", err, out)
	}

	thumbPath := filepath.Join(tmpDir, "thumb.jpg")
	if out, err := hls.Thumbnail(ctx, sourcePath, thumbPath, info.DurationSec); err != nil {
		return fmt.Errorf("thumbnail: %w\n%s", err, out)
	}

	prefix := fmt.Sprintf("videos/%s", videoID)
	masterKey := prefix + "/hls/master.m3u8"
	thumbnailKey := prefix + "/thumb.jpg"

	if err := s3.UploadFile(ctx, filepath.Join(outDir, "master.m3u8"), masterKey); err != nil {
		return fmt.Errorf("upload master manifest: %w", err)
	}
	if err := s3.UploadFile(ctx, thumbPath, thumbnailKey); err != nil {
		return fmt.Errorf("upload thumbnail: %w", err)
	}

	renditionRows := make([]store.RenditionRow, 0, len(renditions))
	for _, r := range renditions {
		variantDir := filepath.Join(outDir, r.Name)
		entries, err := os.ReadDir(variantDir)
		if err != nil {
			return fmt.Errorf("read rendition dir %s: %w", r.Name, err)
		}

		playlistKey := fmt.Sprintf("%s/hls/%s/playlist.m3u8", prefix, r.Name)
		for _, e := range entries {
			localPath := filepath.Join(variantDir, e.Name())
			objectKey := fmt.Sprintf("%s/hls/%s/%s", prefix, r.Name, e.Name())
			if err := s3.UploadFile(ctx, localPath, objectKey); err != nil {
				return fmt.Errorf("upload %s: %w", objectKey, err)
			}
		}

		renditionRows = append(renditionRows, store.RenditionRow{
			Height:      r.Height,
			BitrateKbps: r.BitrateKbps,
			PlaylistKey: playlistKey,
		})
	}

	if err := db.MarkReady(ctx, videoID, info.DurationSec, masterKey, thumbnailKey, renditionRows); err != nil {
		return fmt.Errorf("mark ready: %w", err)
	}

	return nil
}

func requireEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		fmt.Fprintf(os.Stderr, "transcode: required env var %s is not set\n", name)
		os.Exit(2)
	}
	return v
}
