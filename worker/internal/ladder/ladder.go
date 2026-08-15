// Package ladder selects which HLS renditions to encode for a given source,
// per spec §7.1: never upscale above the source resolution.
package ladder

import "fmt"

type Rendition struct {
	Name        string
	Height      int
	BitrateKbps int
}

// standard is the full ladder, ordered highest to lowest quality.
var standard = []Rendition{
	{Name: "1080p", Height: 1080, BitrateKbps: 5000},
	{Name: "720p", Height: 720, BitrateKbps: 2800},
	{Name: "480p", Height: 480, BitrateKbps: 1400},
	{Name: "360p", Height: 360, BitrateKbps: 800},
}

// lowestBitrateKbps is reused for the single-rendition fallback below.
const lowestBitrateKbps = 800

// Select returns the renditions to encode for a source of the given height.
// A rendition is included only when the source is at least as tall as it
// (never upscale). A source shorter than the lowest standard rung (360p)
// still gets exactly one playable rendition, at its own height, using the
// lowest bitrate as the target.
func Select(sourceHeight int) []Rendition {
	var selected []Rendition
	for _, r := range standard {
		if sourceHeight >= r.Height {
			selected = append(selected, r)
		}
	}

	if len(selected) == 0 {
		return []Rendition{{
			Name:        fmt.Sprintf("%dp", sourceHeight),
			Height:      sourceHeight,
			BitrateKbps: lowestBitrateKbps,
		}}
	}

	return selected
}
