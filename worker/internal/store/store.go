// Package store is the Go worker's only path to Postgres — it owns the
// videos/renditions status transitions and rendition rows once a transcode
// job starts, per docs/tasks/TASK-hls-worker.md §2.7.
package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type Store struct {
	conn *pgx.Conn
}

func Connect(ctx context.Context, databaseURL string) (*Store, error) {
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	return &Store{conn: conn}, nil
}

func (s *Store) Close(ctx context.Context) error {
	return s.conn.Close(ctx)
}

// MarkProcessing transitions a video queued -> processing at job start.
func (s *Store) MarkProcessing(ctx context.Context, videoID string) error {
	_, err := s.conn.Exec(ctx,
		`UPDATE videos SET status = 'processing' WHERE id = $1`,
		videoID,
	)
	if err != nil {
		return fmt.Errorf("mark processing: %w", err)
	}
	return nil
}

type RenditionRow struct {
	Height      int
	BitrateKbps int
	PlaylistKey string
}

// MarkReady transitions a video to ready and replaces its renditions rows,
// all in one transaction. Deleting existing renditions before inserting
// makes a retried run idempotent — see §2.7's idempotency note.
func (s *Store) MarkReady(
	ctx context.Context,
	videoID string,
	durationSec float64,
	masterManifestKey, thumbnailKey string,
	renditions []RenditionRow,
) error {
	tx, err := s.conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("mark ready: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	_, err = tx.Exec(ctx,
		`UPDATE videos
		 SET status = 'ready',
		     duration_sec = $2,
		     master_manifest_key = $3,
		     thumbnail_key = $4
		 WHERE id = $1`,
		videoID, durationSec, masterManifestKey, thumbnailKey,
	)
	if err != nil {
		return fmt.Errorf("mark ready: update video: %w", err)
	}

	_, err = tx.Exec(ctx, `DELETE FROM renditions WHERE video_id = $1`, videoID)
	if err != nil {
		return fmt.Errorf("mark ready: clear renditions: %w", err)
	}

	for _, r := range renditions {
		_, err = tx.Exec(ctx,
			`INSERT INTO renditions (video_id, height, bitrate_kbps, playlist_key)
			 VALUES ($1, $2, $3, $4)`,
			videoID, r.Height, r.BitrateKbps, r.PlaylistKey,
		)
		if err != nil {
			return fmt.Errorf("mark ready: insert rendition: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("mark ready: commit: %w", err)
	}
	return nil
}

// MarkFailed transitions a video to failed. Only called for the final
// attempt (see §2.7) — a video must not show failed while BullMQ retries
// are still pending, so status stays processing on non-final failures.
func (s *Store) MarkFailed(ctx context.Context, videoID, reason string) error {
	_, err := s.conn.Exec(ctx,
		`UPDATE videos SET status = 'failed', failure_reason = $2 WHERE id = $1`,
		videoID, reason,
	)
	if err != nil {
		return fmt.Errorf("mark failed: %w", err)
	}
	return nil
}
