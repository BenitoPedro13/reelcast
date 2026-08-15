package hls

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BenitoPedro13/reelcast/worker/internal/ladder"
)

// generateSource creates a synthetic source clip via ffmpeg's lavfi inputs
// so fixtures are generated at test time, not committed as binaries.
func generateSource(t *testing.T, dir string, width, height int, durationSec float64, withAudio bool) string {
	t.Helper()
	out := filepath.Join(dir, "source.mp4")

	args := []string{
		"-y", "-f", "lavfi",
		"-i", fmt.Sprintf("testsrc2=size=%dx%d:duration=%.1f:rate=30", width, height, durationSec),
	}
	if withAudio {
		args = append(args, "-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=1000:duration=%.1f", durationSec))
		args = append(args, "-c:v", "libx264", "-c:a", "aac", "-shortest", out)
	} else {
		args = append(args, "-c:v", "libx264", out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate source: %v\n%s", err, out)
	}
	return out
}

func encode(t *testing.T, sourcePath, outDir string, renditions []ladder.Rendition, hasAudio bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if out, err := Encode(ctx, sourcePath, outDir, renditions, hasAudio); err != nil {
		t.Fatalf("encode: %v\n%s", err, out)
	}
}

func ffprobeInt(t *testing.T, args ...string) int {
	t.Helper()
	cmd := exec.Command("ffprobe", args...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffprobe %v: %v", args, err)
	}
	firstLine := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	v, err := strconv.Atoi(firstLine)
	if err != nil {
		t.Fatalf("ffprobe %v: parse int %q: %v", args, out, err)
	}
	return v
}

// videoStreamBitrateKbps sums v:0 packet bytes across every segment file in
// dir and divides by the total playlist duration. Measuring packet bytes
// rather than trusting stream `bit_rate` metadata, and restricting to the
// video stream rather than whole-segment bytes, is required per
// docs/tasks/TASK-hls-worker.md §5: muxed segment bytes (audio + MPEG-TS
// overhead) push 360p to +19.2% against target, a false failure.
func videoStreamBitrateKbps(t *testing.T, dir string) float64 {
	t.Helper()

	segments, err := filepath.Glob(filepath.Join(dir, "seg*.ts"))
	if err != nil || len(segments) == 0 {
		t.Fatalf("glob segments in %s: %v (found %d)", dir, err, len(segments))
	}

	var totalBytes int64
	for _, seg := range segments {
		cmd := exec.Command("ffprobe", "-v", "error",
			"-select_streams", "v:0",
			"-show_entries", "packet=size",
			"-of", "default=nw=1:nk=1",
			seg,
		)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("ffprobe packet sizes for %s: %v", seg, err)
		}
		scanner := bufio.NewScanner(strings.NewReader(string(out)))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			size, err := strconv.ParseInt(line, 10, 64)
			if err != nil {
				t.Fatalf("parse packet size %q: %v", line, err)
			}
			totalBytes += size
		}
	}

	totalDurationSec := totalPlaylistDurationSec(t, dir)
	bits := float64(totalBytes) * 8
	return bits / 1000 / totalDurationSec
}

func totalPlaylistDurationSec(t *testing.T, dir string) float64 {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, "playlist.m3u8"))
	if err != nil {
		t.Fatalf("open playlist: %v", err)
	}
	defer f.Close()

	var total float64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#EXTINF:") {
			field := strings.TrimPrefix(line, "#EXTINF:")
			field = strings.TrimSuffix(field, ",")
			v, err := strconv.ParseFloat(field, 64)
			if err != nil {
				t.Fatalf("parse EXTINF %q: %v", line, err)
			}
			total += v
		}
	}
	return total
}

func extinfValues(t *testing.T, playlistPath string) []float64 {
	t.Helper()
	f, err := os.Open(playlistPath)
	if err != nil {
		t.Fatalf("open playlist: %v", err)
	}
	defer f.Close()

	var values []float64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#EXTINF:") {
			field := strings.TrimSuffix(strings.TrimPrefix(line, "#EXTINF:"), ",")
			v, err := strconv.ParseFloat(field, 64)
			if err != nil {
				t.Fatalf("parse EXTINF %q: %v", line, err)
			}
			values = append(values, v)
		}
	}
	return values
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestEncode_ResolutionAndBitrate(t *testing.T) {
	dir := t.TempDir()
	source := generateSource(t, dir, 1920, 1080, 12, true)
	outDir := filepath.Join(dir, "out")
	renditions := ladder.Select(1080)
	encode(t, source, outDir, renditions, true)

	for _, r := range renditions {
		variantDir := filepath.Join(outDir, r.Name)
		gotHeight := ffprobeInt(t, "-v", "error", "-select_streams", "v:0",
			"-show_entries", "stream=height", "-of", "default=nw=1:nk=1",
			filepath.Join(variantDir, "seg00000.ts"))
		if gotHeight != r.Height {
			t.Errorf("%s: height = %d, want %d", r.Name, gotHeight, r.Height)
		}

		got := videoStreamBitrateKbps(t, variantDir)
		lower := float64(r.BitrateKbps) * 0.85
		upper := float64(r.BitrateKbps) * 1.15
		if got < lower || got > upper {
			t.Errorf("%s: video-stream bitrate = %.0fk, want within ±15%% of %dk", r.Name, got, r.BitrateKbps)
		}
	}
}

func TestEncode_SegmentAlignment(t *testing.T) {
	dir := t.TempDir()
	source := generateSource(t, dir, 1280, 720, 12, true)
	outDir := filepath.Join(dir, "out")
	renditions := ladder.Select(720) // 720p, 360p
	encode(t, source, outDir, renditions, true)

	var reference []float64
	for i, r := range renditions {
		values := extinfValues(t, filepath.Join(outDir, r.Name, "playlist.m3u8"))
		for _, v := range values {
			if v != 4.0 {
				t.Errorf("%s: segment duration = %.6f, want exactly 4.000000", r.Name, v)
			}
		}
		if i == 0 {
			reference = values
		} else if len(values) != len(reference) {
			t.Errorf("%s: %d segments, reference %s has %d", r.Name, len(values), renditions[0].Name, len(reference))
		}
	}
}

func TestEncode_PlaylistTypeAndMaster(t *testing.T) {
	dir := t.TempDir()
	source := generateSource(t, dir, 1280, 720, 8, true)
	outDir := filepath.Join(dir, "out")
	renditions := ladder.Select(720)
	encode(t, source, outDir, renditions, true)

	for _, r := range renditions {
		playlist := readFile(t, filepath.Join(outDir, r.Name, "playlist.m3u8"))
		if !strings.Contains(playlist, "#EXT-X-PLAYLIST-TYPE:VOD") {
			t.Errorf("%s: playlist missing #EXT-X-PLAYLIST-TYPE:VOD", r.Name)
		}
		if !strings.Contains(playlist, "#EXT-X-ENDLIST") {
			t.Errorf("%s: playlist missing #EXT-X-ENDLIST", r.Name)
		}
	}

	master := readFile(t, filepath.Join(outDir, "master.m3u8"))
	for _, r := range renditions {
		if !strings.Contains(master, r.Name+"/playlist.m3u8") {
			t.Errorf("master.m3u8 missing variant entry for %s", r.Name)
		}
	}
}

func TestEncode_SilentSource(t *testing.T) {
	dir := t.TempDir()
	source := generateSource(t, dir, 640, 360, 8, false)
	outDir := filepath.Join(dir, "out")
	renditions := ladder.Select(360)
	encode(t, source, outDir, renditions, false)

	master := readFile(t, filepath.Join(outDir, "master.m3u8"))
	if strings.Contains(master, "mp4a") {
		t.Errorf("master.m3u8 for silent source should not advertise an audio codec, got:\n%s", master)
	}
}

func TestThumbnail(t *testing.T) {
	dir := t.TempDir()
	source := generateSource(t, dir, 1280, 720, 8, true)
	thumbPath := filepath.Join(dir, "thumb.jpg")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if out, err := Thumbnail(ctx, source, thumbPath, 8); err != nil {
		t.Fatalf("thumbnail: %v\n%s", err, out)
	}

	info, err := os.Stat(thumbPath)
	if err != nil {
		t.Fatalf("stat thumbnail: %v", err)
	}
	if info.Size() == 0 {
		t.Error("thumbnail file is empty")
	}

	gotWidth := ffprobeInt(t, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width", "-of", "default=nw=1:nk=1", thumbPath)
	if gotWidth != 640 {
		t.Errorf("thumbnail width = %d, want 640", gotWidth)
	}
}

func TestEncode_SubHeightSingleRendition(t *testing.T) {
	dir := t.TempDir()
	source := generateSource(t, dir, 426, 240, 8, true)
	outDir := filepath.Join(dir, "out")
	renditions := ladder.Select(240)
	if len(renditions) != 1 {
		t.Fatalf("ladder.Select(240) = %d renditions, want 1", len(renditions))
	}
	encode(t, source, outDir, renditions, true)

	master := readFile(t, filepath.Join(outDir, "master.m3u8"))
	if !strings.Contains(master, renditions[0].Name+"/playlist.m3u8") {
		t.Errorf("master.m3u8 missing single rendition %s:\n%s", renditions[0].Name, master)
	}
}
