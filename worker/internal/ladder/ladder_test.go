package ladder

import "testing"

func TestSelect(t *testing.T) {
	cases := []struct {
		name         string
		sourceHeight int
		wantHeights  []int
	}{
		{"1080p source gets full ladder", 1080, []int{1080, 720, 480, 360}},
		{"480p source gets no upscale", 480, []int{480, 360}},
		{"sub-360p source gets single rendition at source height", 240, []int{240}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Select(c.sourceHeight)
			if len(got) != len(c.wantHeights) {
				t.Fatalf("got %d renditions, want %d: %+v", len(got), len(c.wantHeights), got)
			}
			for i, h := range c.wantHeights {
				if got[i].Height != h {
					t.Errorf("rendition %d: got height %d, want %d", i, got[i].Height, h)
				}
			}
		})
	}

	single := Select(240)
	if single[0].Name != "240p" {
		t.Errorf("sub-360p rendition name = %q, want %q", single[0].Name, "240p")
	}
	if single[0].BitrateKbps != lowestBitrateKbps {
		t.Errorf("sub-360p rendition bitrate = %d, want %d", single[0].BitrateKbps, lowestBitrateKbps)
	}
}
