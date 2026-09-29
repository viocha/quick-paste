//go:build windows

package main

import "testing"

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
	if !shouldApplyQueryLeftOffset(autocompleteCaretAutomation, rect{}, false, moved) {
		t.Fatal("custom accessibility caret must preserve the existing offset behavior")
	}
}
