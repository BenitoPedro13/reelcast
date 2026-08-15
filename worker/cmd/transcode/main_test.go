package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BenitoPedro13/reelcast/worker/internal/storage"
)

const testDatabaseURL = "postgresql://reelcast:reelcast@localhost:5432/reelcast"

func setTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", testDatabaseURL)
	t.Setenv("S3_ENDPOINT", "http://localhost:9000")
	t.Setenv("S3_REGION", "us-east-1")
	t.Setenv("S3_ACCESS_KEY_ID", "minioadmin")
	t.Setenv("S3_SECRET_ACCESS_KEY", "minioadmin123")
	t.Setenv("S3_BUCKET", "reelcast")
}

func testStorageClient() *storage.Client {
	return storage.New(storage.Config{
		Endpoint:        "http://localhost:9000",
		Region:          "us-east-1",
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "minioadmin123",
		Bucket:          "reelcast",
	})
}

// testDBConn is a connection the test owns directly, for row setup and
// verification — separate from the connection `run` opens internally via
// internal/store, so this exercises the real DATABASE_URL-driven path.
func testDBConn(t *testing.T) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), testDatabaseURL)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	return conn
}

func insertQueuedVideo(t *testing.T, conn *pgx.Conn, sourceKey string) string {
	t.Helper()
	var id string
	err := conn.QueryRow(context.Background(),
		`INSERT INTO videos (title, status, source_key) VALUES ('smoke test video', 'queued', $1) RETURNING id`,
		sourceKey,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert queued video: %v", err)
	}
	return id
}

func cleanupVideo(t *testing.T, conn *pgx.Conn, videoID string) {
	t.Helper()
	ctx := context.Background()
	conn.Exec(ctx, `DELETE FROM renditions WHERE video_id = $1`, videoID)
	conn.Exec(ctx, `DELETE FROM videos WHERE id = $1`, videoID)
}

func queryVideo(t *testing.T, conn *pgx.Conn, videoID string) (status string, masterManifestKey string) {
	t.Helper()
	var key *string
	err := conn.QueryRow(context.Background(),
		`SELECT status, master_manifest_key FROM videos WHERE id = $1`, videoID,
	).Scan(&status, &key)
	if err != nil {
		t.Fatalf("query video: %v", err)
	}
	if key != nil {
		masterManifestKey = *key
	}
	return status, masterManifestKey
}

func generateAndUploadSource(t *testing.T, s3 *storage.Client, key string) {
	t.Helper()
	dir := t.TempDir()
	local := filepath.Join(dir, "source.mp4")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y", "-f", "lavfi", "-i", "testsrc2=size=640x360:duration=8:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=1000:duration=8",
		"-c:v", "libx264", "-c:a", "aac", "-shortest", local,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate source: %v\n%s", err, out)
	}

	if err := s3.UploadFile(context.Background(), local, key); err != nil {
		t.Fatalf("upload source: %v", err)
	}
}

func TestRun_FullPipeline_MarksReady(t *testing.T) {
	setTestEnv(t)
	ctx := context.Background()
	conn := testDBConn(t)
	s3 := testStorageClient()

	sourceKey := fmt.Sprintf("worker-test/main/%d/source", time.Now().UnixNano())
	generateAndUploadSource(t, s3, sourceKey)

	videoID := insertQueuedVideo(t, conn, sourceKey)
	defer cleanupVideo(t, conn, videoID)

	if err := run(ctx, videoID, sourceKey, false); err != nil {
		t.Fatalf("run: %v", err)
	}

	status, masterKey := queryVideo(t, conn, videoID)
	if status != "ready" {
		t.Fatalf("status = %q, want ready", status)
	}
	expectedMasterKey := "videos/" + videoID + "/hls/master.m3u8"
	if masterKey != expectedMasterKey {
		t.Fatalf("master_manifest_key = %q, want %q", masterKey, expectedMasterKey)
	}

	// The uploaded master manifest must actually exist in the bucket, not
	// just be recorded in Postgres.
	dir := t.TempDir()
	downloaded := filepath.Join(dir, "master.m3u8")
	if err := s3.DownloadFile(ctx, expectedMasterKey, downloaded); err != nil {
		t.Fatalf("download uploaded master manifest: %v", err)
	}
	if info, err := os.Stat(downloaded); err != nil || info.Size() == 0 {
		t.Fatalf("uploaded master manifest missing or empty: %v", err)
	}
}

func TestRun_NonFinalAttemptFailure_LeavesStatusProcessing(t *testing.T) {
	setTestEnv(t)
	ctx := context.Background()
	conn := testDBConn(t)

	// Source key that was never uploaded — download fails, simulating a
	// mid-pipeline crash.
	sourceKey := fmt.Sprintf("worker-test/main/%d/does-not-exist", time.Now().UnixNano())
	videoID := insertQueuedVideo(t, conn, sourceKey)
	defer cleanupVideo(t, conn, videoID)

	if err := run(ctx, videoID, sourceKey, false); err == nil {
		t.Fatal("run: expected an error for a missing source object, got nil")
	}

	status, _ := queryVideo(t, conn, videoID)
	if status != "processing" {
		t.Fatalf("status after non-final-attempt failure = %q, want processing (must not show failed while retries pending)", status)
	}
}

func TestRun_FinalAttemptFailure_MarksFailed(t *testing.T) {
	setTestEnv(t)
	ctx := context.Background()
	conn := testDBConn(t)

	sourceKey := fmt.Sprintf("worker-test/main/%d/does-not-exist", time.Now().UnixNano())
	videoID := insertQueuedVideo(t, conn, sourceKey)
	defer cleanupVideo(t, conn, videoID)

	if err := run(ctx, videoID, sourceKey, true); err == nil {
		t.Fatal("run: expected an error for a missing source object, got nil")
	}

	status, _ := queryVideo(t, conn, videoID)
	if status != "failed" {
		t.Fatalf("status after final-attempt failure = %q, want failed", status)
	}
}
