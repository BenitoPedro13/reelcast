package store

import (
	"context"
	"testing"
)

const testDatabaseURL = "postgresql://reelcast:reelcast@localhost:5432/reelcast"

// insertTestVideo mirrors apps/api's VideosService.create insert, minus the
// presigned-upload side effect, so the worker's writes have a real row to
// act on. Postgres generates the id (gen_random_uuid(), per the videos
// table default) rather than the test picking one, avoiding a new UUID
// dependency just for tests.
func insertTestVideo(t *testing.T, s *Store) string {
	t.Helper()
	var id string
	err := s.conn.QueryRow(context.Background(),
		`INSERT INTO videos (title, status, source_key) VALUES ('test video', 'queued', 'videos/test/source') RETURNING id`,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert test video: %v", err)
	}
	return id
}

func cleanupTestVideo(t *testing.T, s *Store, id string) {
	t.Helper()
	ctx := context.Background()
	s.conn.Exec(ctx, `DELETE FROM renditions WHERE video_id = $1`, id)
	s.conn.Exec(ctx, `DELETE FROM videos WHERE id = $1`, id)
}

func TestMarkProcessingThenReady_ThenRetryIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s, err := Connect(ctx, testDatabaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer s.Close(ctx)

	id := insertTestVideo(t, s)
	defer cleanupTestVideo(t, s, id)

	if err := s.MarkProcessing(ctx, id); err != nil {
		t.Fatalf("MarkProcessing: %v", err)
	}
	status := queryStatus(t, s, id)
	if status != "processing" {
		t.Fatalf("status after MarkProcessing = %q, want processing", status)
	}

	renditions := []RenditionRow{
		{Height: 1080, BitrateKbps: 5000, PlaylistKey: "videos/" + id + "/hls/1080p/playlist.m3u8"},
		{Height: 360, BitrateKbps: 800, PlaylistKey: "videos/" + id + "/hls/360p/playlist.m3u8"},
	}
	if err := s.MarkReady(ctx, id, 12.5, "videos/"+id+"/hls/master.m3u8", "videos/"+id+"/thumb.jpg", renditions); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}

	status = queryStatus(t, s, id)
	if status != "ready" {
		t.Fatalf("status after MarkReady = %q, want ready", status)
	}
	count := countRenditions(t, s, id)
	if count != 2 {
		t.Fatalf("renditions after first MarkReady = %d, want 2", count)
	}

	// Simulate a retried run re-encoding a smaller ladder (e.g. a source
	// that only qualifies for 360p on retry) — the old rows must not
	// linger per §2.7's idempotency requirement.
	if err := s.MarkReady(ctx, id, 12.5, "videos/"+id+"/hls/master.m3u8", "videos/"+id+"/thumb.jpg",
		[]RenditionRow{{Height: 360, BitrateKbps: 800, PlaylistKey: "videos/" + id + "/hls/360p/playlist.m3u8"}}); err != nil {
		t.Fatalf("MarkReady (retry): %v", err)
	}
	count = countRenditions(t, s, id)
	if count != 1 {
		t.Fatalf("renditions after retried MarkReady = %d, want 1 (stale rows not cleared)", count)
	}
}

func TestMarkFailed(t *testing.T) {
	ctx := context.Background()
	s, err := Connect(ctx, testDatabaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer s.Close(ctx)

	id := insertTestVideo(t, s)
	defer cleanupTestVideo(t, s, id)

	if err := s.MarkFailed(ctx, id, "ffmpeg exited 1: boom"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	status := queryStatus(t, s, id)
	if status != "failed" {
		t.Fatalf("status after MarkFailed = %q, want failed", status)
	}
}

func queryStatus(t *testing.T, s *Store, id string) string {
	t.Helper()
	var status string
	err := s.conn.QueryRow(context.Background(), `SELECT status FROM videos WHERE id = $1`, id).Scan(&status)
	if err != nil {
		t.Fatalf("query status: %v", err)
	}
	return status
}

func countRenditions(t *testing.T, s *Store, id string) int {
	t.Helper()
	var count int
	err := s.conn.QueryRow(context.Background(), `SELECT count(*) FROM renditions WHERE video_id = $1`, id).Scan(&count)
	if err != nil {
		t.Fatalf("count renditions: %v", err)
	}
	return count
}
