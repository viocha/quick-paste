//go:build windows

package main

import "testing"

func TestPopupStaysInWorkArea(t *testing.T) {
	cases := []struct {
		name               string
		caret, work        rect
		width, height, gap int
	}{
		{"bottom right", rect{1910, 1030, 1911, 1040}, rect{0, 0, 1920, 1040}, 280, 176, 4},
		{"top left", rect{-10, -10, -9, 2}, rect{0, 0, 1920, 1040}, 280, 176, 4},
		{"negative monitor", rect{-5, -10, -4, 10}, rect{-1920, -1080, 0, 0}, 280, 176, 4},
		{"small work area", rect{90, 60, 91, 70}, rect{0, 0, 100, 80}, 280, 176, 4},
		{"150 percent dpi", rect{3830, 2080, 3832, 2100}, rect{1920, 0, 3840, 2100}, 420, 264, 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := placePopup(tc.caret, tc.work, tc.width, tc.height, tc.gap)
			if b.Left < tc.work.Left || b.Top < tc.work.Top || b.Right > tc.work.Right || b.Bottom > tc.work.Bottom || b.Right <= b.Left || b.Bottom <= b.Top {
				t.Fatalf("popup outside work area: %+v", b)
			}
		})
	}
}
func TestPopupPrefersBelowAndFlipsAbove(t *testing.T) {
	work := rect{0, 0, 1920, 1040}
	b := placePopup(rect{100, 100, 101, 120}, work, 280, 176, 4)
	if b.Left != 100 || b.Top != 124 {
		t.Fatalf("unexpected normal placement: %+v", b)
	}
	b = placePopup(rect{100, 1000, 101, 1020}, work, 280, 176, 4)
	if b.Top != 820 {
		t.Fatalf("did not flip above caret: %+v", b)
	}
}

func TestFloatingPanelPrefersLowerRightAndClampsAtEdges(t *testing.T) {
	monitor := rect{0, 0, 1920, 1080}

	b := placeFloatingPanel(rect{100, 100, 101, 101}, monitor, 380, 480, 4)
	if b != (rect{105, 105, 485, 585}) {
		t.Fatalf("unexpected lower-right placement: %+v", b)
	}

	// Only 435 px remain below the pointer. The panel should use that space and
	// shift upward by the missing 45 px, not flip completely above the pointer.
	b = placeFloatingPanel(rect{100, 640, 101, 641}, monitor, 380, 480, 4)
	if b.Top != 596 || b.Bottom != 1076 {
		t.Fatalf("panel flipped instead of clamping vertically with edge margin: %+v", b)
	}

	// Near the right edge, preserve the rightward preference and shift left only
	// enough to keep the full panel on the monitor.
	b = placeFloatingPanel(rect{1800, 100, 1801, 101}, monitor, 380, 480, 4)
	if b.Left != 1536 || b.Right != 1916 {
		t.Fatalf("panel did not clamp horizontally with edge margin: %+v", b)
	}
}
