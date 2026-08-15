// Package hls builds and runs the ffmpeg invocation that packages a source
// video into a multi-variant HLS ladder, and grabs a thumbnail. The
// invocation is verified empirically against ffmpeg 9.0 — see
// docs/tasks/TASK-hls-worker.md §2.4 and §5 for the measured evidence
// (force_key_frames is required for cross-variant segment alignment; a
// silent source needs the no-audio branch; %v subdirectories don't need to
// be pre-created).
package hls

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BenitoPedro13/reelcast/worker/internal/ladder"
)

const (
	segmentDurationSec = 4
	maxrateFactor      = 1.07
	bufsizeFactor      = 1.5
	thumbnailMaxOffset = 3.0
	thumbnailFraction  = 0.10
)

// Encode runs the multi-variant ffmpeg HLS invocation and writes, under
// outDir: one subdirectory per rendition name (playlist.m3u8 + segNNNNN.ts)
// and a master.m3u8 in outDir itself.
func Encode(ctx context.Context, sourcePath, outDir string, renditions []ladder.Rendition, hasAudio bool) ([]byte, error) {
	args := buildEncodeArgs(sourcePath, outDir, renditions, hasAudio)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("ffmpeg encode: %w", err)
	}
	return out, nil
}

func buildEncodeArgs(sourcePath, outDir string, renditions []ladder.Rendition, hasAudio bool) []string {
	n := len(renditions)

	splitLabels := make([]string, n)
	for i := range renditions {
		splitLabels[i] = fmt.Sprintf("[v%d]", i+1)
	}
	filterParts := []string{fmt.Sprintf("[0:v]split=%d%s", n, strings.Join(splitLabels, ""))}
	for i, r := range renditions {
		filterParts = append(filterParts, fmt.Sprintf("[v%d]scale=-2:%d[v%do]", i+1, r.Height, i+1))
	}

	args := []string{
		"-y",
		"-i", sourcePath,
		"-filter_complex", strings.Join(filterParts, ";"),
	}

	streamMapEntries := make([]string, n)
	for i, r := range renditions {
		maxrateKbps := int(float64(r.BitrateKbps)*maxrateFactor + 0.5)
		bufsizeKbps := int(float64(r.BitrateKbps)*bufsizeFactor + 0.5)
		args = append(args,
			"-map", fmt.Sprintf("[v%do]", i+1),
			fmt.Sprintf("-c:v:%d", i), "libx264",
			"-preset", "veryfast",
			fmt.Sprintf("-b:v:%d", i), fmt.Sprintf("%dk", r.BitrateKbps),
			fmt.Sprintf("-maxrate:v:%d", i), fmt.Sprintf("%dk", maxrateKbps),
			fmt.Sprintf("-bufsize:v:%d", i), fmt.Sprintf("%dk", bufsizeKbps),
		)
		if hasAudio {
			streamMapEntries[i] = fmt.Sprintf("v:%d,a:%d,name:%s", i, i, r.Name)
		} else {
			streamMapEntries[i] = fmt.Sprintf("v:%d,name:%s", i, r.Name)
		}
	}

	if hasAudio {
		for range renditions {
			args = append(args, "-map", "a:0")
		}
		args = append(args, "-c:a", "aac", "-b:a", "128k", "-ac", "2")
	}

	args = append(args,
		"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", segmentDurationSec),
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%d", segmentDurationSec),
		"-hls_playlist_type", "vod",
		"-hls_flags", "independent_segments",
		"-hls_segment_type", "mpegts",
		"-hls_segment_filename", filepath.Join(outDir, "%v", "seg%05d.ts"),
		"-master_pl_name", "master.m3u8",
		"-var_stream_map", strings.Join(streamMapEntries, " "),
		filepath.Join(outDir, "%v", "playlist.m3u8"),
	)

	return args
}

// Thumbnail grabs a single JPEG frame at min(3s, 10% of duration).
func Thumbnail(ctx context.Context, sourcePath, outPath string, durationSec float64) ([]byte, error) {
	offset := durationSec * thumbnailFraction
	if offset > thumbnailMaxOffset {
		offset = thumbnailMaxOffset
	}

	args := []string{
		"-y",
		"-ss", fmt.Sprintf("%.3f", offset),
		"-i", sourcePath,
		"-frames:v", "1",
		"-vf", "scale=640:-2",
		outPath,
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("ffmpeg thumbnail: %w", err)
	}
	return out, nil
}
