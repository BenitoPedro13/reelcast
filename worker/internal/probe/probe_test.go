package probe

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func generate(t *testing.T, dir, name string, width, height int, durationSec float64, withAudio bool) string {
	t.Helper()
	out := filepath.Join(dir, name)

	args := []string{
		"-y", "-f", "lavfi",
		"-i", fmt.Sprintf("testsrc2=size=%dx%d:duration=%.1f", width, height, durationSec),
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
		t.Fatalf("generate fixture: %v\n%s", err, out)
	}
	return out
}

func TestProbe_WithAudio(t *testing.T) {
	dir := t.TempDir()
	source := generate(t, dir, "with_audio.mp4", 1280, 720, 5, true)

	info, err := Probe(context.Background(), source)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}

	if info.Width != 1280 || info.Height != 720 {
		t.Errorf("dimensions = %dx%d, want 1280x720", info.Width, info.Height)
	}
	if !info.HasAudio {
		t.Error("HasAudio = false, want true")
	}
	if info.DurationSec < 4.5 || info.DurationSec > 5.5 {
		t.Errorf("DurationSec = %.2f, want ~5", info.DurationSec)
	}
}

func TestProbe_SilentSource(t *testing.T) {
	dir := t.TempDir()
	source := generate(t, dir, "silent.mp4", 640, 360, 3, false)

	info, err := Probe(context.Background(), source)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}

	if info.HasAudio {
		t.Error("HasAudio = true for a video-only source, want false")
	}
	if info.Width != 640 || info.Height != 360 {
		t.Errorf("dimensions = %dx%d, want 640x360", info.Width, info.Height)
	}
}
