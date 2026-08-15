package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// testConfig mirrors infra/docker-compose.yml / .env.example — the real
// local MinIO stack, not a mock.
func testConfig() Config {
	return Config{
		Endpoint:        "http://localhost:9000",
		Region:          "us-east-1",
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "minioadmin123",
		Bucket:          "reelcast",
	}
}

func TestDownloadFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	client := New(testConfig())
	ctx := context.Background()

	src := filepath.Join(dir, "source.mp4")
	want := "fake source bytes for round-trip test"
	if err := os.WriteFile(src, []byte(want), 0o644); err != nil {
		t.Fatalf("write local fixture: %v", err)
	}

	key := "worker-test/download-roundtrip/source.mp4"
	if err := client.UploadFile(ctx, src, key); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}

	dst := filepath.Join(dir, "downloaded", "source.mp4")
	if err := client.DownloadFile(ctx, key, dst); err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != want {
		t.Errorf("downloaded content = %q, want %q", got, want)
	}
}

func TestUploadFile_ContentTypeAndCacheControl(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		filename         string
		content          string
		wantContentType  string
		wantCacheControl string
	}{
		{"master.m3u8", "#EXTM3U", "application/vnd.apple.mpegurl", manifestCacheControl},
		{"seg00000.ts", "not-really-ts-bytes", "video/mp2t", segmentCacheControl},
		{"thumb.jpg", "not-really-jpeg-bytes", "image/jpeg", manifestCacheControl},
	}

	client := New(testConfig())
	ctx := context.Background()

	for _, c := range cases {
		t.Run(c.filename, func(t *testing.T) {
			localPath := filepath.Join(dir, c.filename)
			if err := os.WriteFile(localPath, []byte(c.content), 0o644); err != nil {
				t.Fatalf("write local fixture: %v", err)
			}

			key := "worker-test/" + c.filename
			if err := client.UploadFile(ctx, localPath, key); err != nil {
				t.Fatalf("UploadFile: %v", err)
			}

			head, err := client.s3.HeadObject(ctx, &s3.HeadObjectInput{
				Bucket: aws.String(client.bucket),
				Key:    aws.String(key),
			})
			if err != nil {
				t.Fatalf("HeadObject: %v", err)
			}

			if aws.ToString(head.ContentType) != c.wantContentType {
				t.Errorf("ContentType = %q, want %q", aws.ToString(head.ContentType), c.wantContentType)
			}
			if aws.ToString(head.CacheControl) != c.wantCacheControl {
				t.Errorf("CacheControl = %q, want %q", aws.ToString(head.CacheControl), c.wantCacheControl)
			}
		})
	}
}
