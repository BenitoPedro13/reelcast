// Package storage uploads the encoded HLS output to S3-compatible object
// storage (MinIO locally, R2 in prod — same config shape as apps/api's
// StorageService).
package storage

import (
	"context"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Config struct {
	Endpoint        string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
}

type Client struct {
	s3     *s3.Client
	bucket string
}

func New(cfg Config) *Client {
	awsCfg := aws.Config{
		Region: cfg.Region,
		Credentials: credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID, cfg.SecretAccessKey, "",
		),
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Endpoint)
		o.UsePathStyle = true
	})

	return &Client{s3: client, bucket: cfg.Bucket}
}

// manifestCacheControl is short so a re-run's rewritten manifest (retry, or
// future re-encode) is picked up promptly by any CDN in front of the bucket.
const manifestCacheControl = "public, max-age=60"

// segmentCacheControl is long+immutable: segment object keys are
// content-addressed by rendition name + sequence number and never change
// meaning once written.
const segmentCacheControl = "public, max-age=31536000, immutable"

// UploadFile uploads a single local file to key, choosing content type and
// cache-control from its extension per
// docs/tasks/TASK-hls-worker.md §2.6.
func (c *Client) UploadFile(ctx context.Context, localPath, key string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", localPath, err)
	}
	defer f.Close()

	contentType, cacheControl := classify(localPath)

	_, err = c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:       aws.String(c.bucket),
		Key:          aws.String(key),
		Body:         f,
		ContentType:  aws.String(contentType),
		CacheControl: aws.String(cacheControl),
	})
	if err != nil {
		return fmt.Errorf("put object %s: %w", key, err)
	}
	return nil
}

// DownloadFile fetches key into localPath, creating parent directories as
// needed. Used to pull the uploaded source object down before ffmpeg (which
// operates on local files, not object storage) can run.
func (c *Client) DownloadFile(ctx context.Context, key, localPath string) error {
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return fmt.Errorf("mkdir for %s: %w", localPath, err)
	}

	out, err := c.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("get object %s: %w", key, err)
	}
	defer out.Body.Close()

	f, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", localPath, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, out.Body); err != nil {
		return fmt.Errorf("write %s: %w", localPath, err)
	}
	return nil
}

func classify(path string) (contentType, cacheControl string) {
	switch filepath.Ext(path) {
	case ".m3u8":
		return "application/vnd.apple.mpegurl", manifestCacheControl
	case ".ts":
		return "video/mp2t", segmentCacheControl
	case ".jpg", ".jpeg":
		return "image/jpeg", manifestCacheControl
	default:
		ct := mime.TypeByExtension(filepath.Ext(path))
		if ct == "" {
			ct = "application/octet-stream"
		}
		return ct, manifestCacheControl
	}
}
