// Package probe wraps ffprobe to extract the source metadata the transcode
// pipeline needs: duration, dimensions, and whether an audio stream exists.
package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
)

type Info struct {
	DurationSec float64
	Width       int
	Height      int
	HasAudio    bool
}

type ffprobeStream struct {
	CodecType string `json:"codec_type"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

type ffprobeFormat struct {
	Duration string `json:"duration"`
}

type ffprobeOutput struct {
	Streams []ffprobeStream `json:"streams"`
	Format  ffprobeFormat   `json:"format"`
}

// Probe runs ffprobe against sourcePath and returns duration, video
// dimensions, and whether an audio stream is present.
func Probe(ctx context.Context, sourcePath string) (Info, error) {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		sourcePath,
	)

	out, err := cmd.Output()
	if err != nil {
		return Info{}, fmt.Errorf("ffprobe: %w", err)
	}

	var parsed ffprobeOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return Info{}, fmt.Errorf("ffprobe: parse output: %w", err)
	}

	duration, err := strconv.ParseFloat(parsed.Format.Duration, 64)
	if err != nil {
		return Info{}, fmt.Errorf("ffprobe: parse duration %q: %w", parsed.Format.Duration, err)
	}

	info := Info{DurationSec: duration}
	videoFound := false
	for _, s := range parsed.Streams {
		switch s.CodecType {
		case "video":
			if !videoFound {
				info.Width = s.Width
				info.Height = s.Height
				videoFound = true
			}
		case "audio":
			info.HasAudio = true
		}
	}

	if !videoFound {
		return Info{}, fmt.Errorf("ffprobe: no video stream found in %s", sourcePath)
	}

	return info, nil
}
