//go:build windows

package main

import (
	"fmt"
	"testing"
)

func TestCaretThresholdsAtDisplayDPI(t *testing.T) {
	for _, tc := range []struct {
		dpi                     uintptr
		tolerance, narrow, wide int32
		height                  int32
	}{
		{96, 2, 16, 64, 256},
		{120, 3, 20, 80, 320},
		{144, 3, 24, 96, 384},
		{192, 4, 32, 128, 512},
		{240, 5, 40, 160, 640},
		{288, 6, 48, 192, 768},
		{384, 8, 64, 256, 1024},
	} {
		t.Run(fmt.Sprintf("DPI_%d", tc.dpi), func(t *testing.T) {
			// 覆盖副屏位于主屏左侧时的负坐标。
			start := rect{Left: -1000, Top: -500, Right: -999, Bottom: -480}
			for _, direction := range []string{"right", "up", "down"} {
				for _, extra := range []int32{0, 1} {
					current := start
					switch direction {
					case "right":
						current.Left += tc.tolerance + extra
						current.Right += tc.tolerance + extra
					case "up":
						current.Bottom = start.Top - tc.tolerance - extra
						current.Top = current.Bottom - 20
					case "down":
						current.Top = start.Bottom + tc.tolerance + extra
						current.Bottom = current.Top + 20
					}
					if got := queryMovedEditorCaretAtDPI(start, current, tc.dpi); got != (extra > 0) {
						t.Errorf("光标移动方向 %s，超出阈值 %d 像素：got %v", direction, extra, got)
					}
				}
			}
			for _, width := range []struct{ logical, physical int32 }{{16, tc.narrow}, {64, tc.wide}} {
				for _, size := range []struct {
					width, height int32
					want          bool
				}{
					{width.physical, tc.height, true},
					{width.physical + 1, tc.height, false},
					{width.physical, tc.height + 1, false},
					{0, 1, true},
					{-1, 1, false},
					{1, 0, false},
					{1, -1, false},
				} {
					bounds := rect{Left: -1000, Top: -500, Right: -1000 + size.width, Bottom: -500 + size.height}
					if got := caretRectCredibleAtDPI(bounds, width.logical, tc.dpi); got != size.want {
						t.Errorf("光标尺寸 %d×%d，逻辑宽度上限 %d：got %v, want %v", size.width, size.height, width.logical, got, size.want)
					}
				}
			}
			for _, width := range []int32{0, tc.tolerance, tc.tolerance + 1} {
				bounds := rect{Left: -1000, Top: -500, Right: -1000 + width, Bottom: -480}
				if got := compositionBoundsInlineAtDPI(bounds, tc.dpi); got != (width > tc.tolerance) {
					t.Errorf("行内输入法文本宽度 %d，阈值 %d：got %v", width, tc.tolerance, got)
				}
			}
		})
	}
}

func TestNormalizeScaledProviderRect(t *testing.T) {
	host := rect{Left: 1397, Top: 598, Right: 3094, Bottom: 1798}
	provider := rect{Left: 3597, Top: 1535, Right: 5222, Bottom: 1760}

	got := normalizeScaledProviderRect(provider, host, 240)
	want := rect{Left: 2277, Top: 972, Right: 2927, Bottom: 1062}
	if got != want {
		t.Fatalf("normalized rect = %+v, want %+v", got, want)
	}
}

func TestNormalizeScaledProviderRectKeepsValidPhysicalRect(t *testing.T) {
	host := rect{Left: 1397, Top: 598, Right: 3094, Bottom: 1798}
	physical := rect{Left: 2277, Top: 972, Right: 2927, Bottom: 1062}

	got := normalizeScaledProviderRect(physical, host, 240)
	if got != physical {
		t.Fatalf("valid physical rect changed: %+v", got)
	}
}

func TestQueryMovedEditorCaret(t *testing.T) {
	start := rect{Left: 100, Top: 100, Right: 101, Bottom: 120}
	if queryMovedEditorCaret(start, rect{Left: 100, Top: 100, Right: 101, Bottom: 120}) {
		t.Fatal("stationary editor caret should not apply the 24px query offset")
	}
	if !queryMovedEditorCaret(start, rect{Left: 128, Top: 100, Right: 129, Bottom: 120}) {
		t.Fatal("advanced editor caret should apply the 24px query offset")
	}
}

func TestShouldApplyQueryLeftOffset(t *testing.T) {
	start := rect{Left: 100, Top: 100, Right: 101, Bottom: 120}
	moved := rect{Left: 128, Top: 100, Right: 129, Bottom: 120}
	still := rect{Left: 100, Top: 100, Right: 101, Bottom: 120}

	if !shouldApplyQueryLeftOffset(autocompleteCaretCompositionInline, start, false, moved) {
		t.Fatal("visible inline composition must apply the query offset")
	}
	if shouldApplyQueryLeftOffset(autocompleteCaretComposition, start, true, still) {
		t.Fatal("composition without inline characters must stay aligned with the caret")
	}
	if shouldApplyQueryLeftOffset(autocompleteCaretComposition, start, true, moved) {
		t.Fatal("composition without inline characters must not use caret movement")
	}
	if shouldApplyQueryLeftOffset(autocompleteCaretWin32, start, false, moved) {
		t.Fatal("Win32 caret without a reliable start sample must not apply the offset")
	}
	if shouldApplyQueryLeftOffset(autocompleteCaretWin32, start, true, still) {
		t.Fatal("stationary Win32 caret must not apply the offset")
	}
	if !shouldApplyQueryLeftOffset(autocompleteCaretWin32, start, true, moved) {
		t.Fatal("advanced Win32 caret must apply the offset")
	}
	for _, source := range []autocompleteCaretSource{autocompleteCaretAutomation, autocompleteCaretAccessible, autocompleteCaretUnknown} {
		if shouldApplyQueryLeftOffset(source, rect{}, false, moved) {
			t.Fatal("仅有光标坐标时不能假定左侧已显示前缀")
		}
	}
}
