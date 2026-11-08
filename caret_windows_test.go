//go:build windows

package main

import (
	"fmt"
	"strings"
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
	for _, source := range []autocompleteCaretSource{autocompleteCaretAutomation, autocompleteCaretAutomationQueryRange, autocompleteCaretAccessible, autocompleteCaretUnknown} {
		if shouldApplyQueryLeftOffset(source, rect{}, false, moved) {
			t.Fatal("仅有光标坐标时不能假定左侧已显示前缀")
		}
	}
}

func TestEdgeOmniboxTextWidthAtHighDPI(t *testing.T) {
	width, ok := measureEdgeOmniboxText("          abc", 240)
	if !ok || width < 135 || width > 210 {
		t.Fatalf("Edge 地址栏文本宽度不可信：width=%d ok=%t", width, ok)
	}
	single, singleOK := measureEdgeOmniboxText("abc", 240)
	if !singleOK || single <= 0 || single >= width {
		t.Fatalf("字符测量未考虑前置空格：abc=%d, spaces+abc=%d", single, width)
	}
}

func TestEdgeOmniboxQueryStartBeforeTypedQuery(t *testing.T) {
	control := rect{Left: 310, Top: 20, Right: 2938, Bottom: 80}
	for _, count := range []int{0, 3, 10} {
		prefix := strings.Repeat(" ", count)
		wantWidth := int32(0)
		if count > 0 {
			var ok bool
			wantWidth, ok = measureEdgeOmniboxText(prefix, 240)
			if !ok {
				t.Fatalf("无法测量 %d 个空格", count)
			}
		}
		got, ok := edgeOmniboxQueryStartFromValueAtDPI(control, prefix+"abc", "abc", 240)
		if !ok {
			t.Fatalf("无法定位 %d 个空格后的查询", count)
		}
		if want := control.Left + int32(scaleForDPI(4, 240)) + wantWidth; got.Left != want {
			t.Fatalf("%d 个空格: prefix start=%d, want=%d", count, got.Left, want)
		}
		if got.Top != 30 || got.Bottom != 70 {
			t.Fatalf("地址栏光标高度无效：%+v", got)
		}
	}
	if _, ok := edgeOmniboxQueryStartFromValueAtDPI(control, "hello abc world", "abc", 240); ok {
		t.Fatal("查询不是 ValuePattern 后缀时不能推算位置")
	}
	if _, ok := edgeOmniboxQueryStartFromValueAtDPI(control, "      abc", "", 240); ok {
		t.Fatal("空查询不应生成定位锚点")
	}
}

func TestEdgeQueryStartDoesNotAdvancePastQuery(t *testing.T) {
	control := rect{Left: 260, Top: 20, Right: 2938, Bottom: 80}
	start, ok := edgeOmniboxQueryStartFromValueAtDPI(control, "   abc", "abc", 240)
	if !ok {
		t.Fatal("查询位置不可用")
	}
	prefixWidth, _ := measureEdgeOmniboxText("   ", 240)
	fullWidth, _ := measureEdgeOmniboxText("   abc", 240)
	if start.Left != control.Left+int32(scaleForDPI(4, 240))+prefixWidth {
		t.Fatalf("补全面板应向左回到 abc 的首字符：%+v", start)
	}
	if start.Left >= control.Left+int32(scaleForDPI(4, 240))+fullWidth {
		t.Fatalf("补全面板错误对齐到 abc 的末尾：%+v", start)
	}
}

func TestEdgeFindBarPrefixBeforeCaret(t *testing.T) {
	for _, tc := range []struct {
		name, value, beforeCaret, query, wantPrefix string
		wantOK                                      bool
	}{
		{"光标位于文本末尾", "          abc", "          abc", "abc", "          ", true},
		{"光标位于文本中间", "          abcxyz", "          abc", "abc", "          ", true},
		{"光标左侧有普通文本", "hello abc world", "hello abc", "abc", "hello ", true},
		{"查询必须紧邻光标", "hello abcx", "hello abcx", "abc", "", false},
		{"光标位于查询之前", "hello abc", "hello ", "abc", "", false},
		{"无关的光标前文本", "hello abc", "other abc", "abc", "", false},
		{"空查询", "abc", "abc", "", "", false},
		{"文本超过测量上限", strings.Repeat("x", 129), strings.Repeat("x", 129), "abc", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := edgeFindBarPrefixBeforeCaret(tc.value, tc.beforeCaret, tc.query)
			if got != tc.wantPrefix || ok != tc.wantOK {
				t.Fatalf("prefix=%q ok=%t, want prefix=%q ok=%t", got, ok, tc.wantPrefix, tc.wantOK)
			}
		})
	}
}

func TestEdgeFindBarTextWidthAtHighDPI(t *testing.T) {
	width, ok := measureEdgeFindBarText(strings.Repeat(" ", 10), 240)
	if !ok || width < 75 || width > 85 {
		t.Fatalf("Edge 查找框 10 个空格的实际字号测量不可信：width=%d ok=%t", width, ok)
	}
}

func TestEdgeFindBarQueryStartUsesTextLineAndMeasuredPrefix(t *testing.T) {
	control := rect{Left: 2233, Top: 110, Right: 2633, Bottom: 190}
	work := rect{Left: 0, Top: 0, Right: 3872, Bottom: 2352}
	lineHeight := int32(scaleForDPI(11, 240))
	wantTop := control.Top + (control.Bottom-control.Top-lineHeight)/2
	gap := int32(scaleForDPI(8, 240))
	for _, spaces := range []int{0, 3, 10} {
		prefix := strings.Repeat(" ", spaces)
		anchor, ok := edgeFindBarQueryStartFromValueAtDPI(control, prefix+"abc", "abc", 240)
		if !ok {
			t.Fatalf("搜索框前有 %d 个空格时无法定位", spaces)
		}
		if anchor.Top != wantTop || anchor.Bottom != wantTop+lineHeight {
			t.Fatalf("垂直锚点应采用输入框内的文本行: %+v, want y=%d..%d", anchor, wantTop, wantTop+lineHeight)
		}
		width := int32(0)
		if spaces > 0 {
			width, _ = measureEdgeFindBarText(prefix, 240)
		}
		wantX := control.Left + int32(scaleForDPI(6, 240)) + width
		if anchor.Left != wantX {
			t.Fatalf("前置 %d 个空格: X=%d, want=%d", spaces, anchor.Left, wantX)
		}
		if spaces == 10 && anchor.Left != 2328 {
			t.Fatalf("10 个空格后的查询起点应精确对齐到屏幕 X=2328，得到 %d", anchor.Left)
		}
		popup := placePopup(anchor, work, 700, 90, int(gap))
		if popup.Top != anchor.Bottom+gap {
			t.Fatalf("补全面板应紧跟文本行并保留阴影间距: control=%+v anchor=%+v popup=%+v", control, anchor, popup)
		}
	}
	for _, value := range []string{"ab", "abcde", "some abc suffix"} {
		if _, ok := edgeFindBarQueryStartFromValueAtDPI(control, value, "abc", 240); ok {
			t.Fatalf("失配的查找文本不得推算坐标: %q", value)
		}
	}
	if _, ok := edgeFindBarQueryStartFromValueAtDPI(control, "      abc", "", 240); ok {
		t.Fatal("空查询不应生成定位锚点")
	}
}

func TestEdgeFindBarRecognizeOnlyFindTextfield(t *testing.T) {
	for _, tc := range []struct {
		class, name string
		want        bool
	}{
		{"Textfield", "在页面上查找", true},
		{"Textfield", "Find on page", true},
		{"Textfield", "地址和搜索栏", false},
		{"OmniboxViewViews", "Find on page", false},
		{"Textfield", "Rename tab", false},
	} {
		if got := edgeFindBarFieldIdentified(tc.class, tc.name); got != tc.want {
			t.Errorf("class=%q name=%q got=%t want=%t", tc.class, tc.name, got, tc.want)
		}
	}
}

func TestEdgeFindBarPopupAtStandardDPI(t *testing.T) {
	control := rect{Left: 893, Top: 44, Right: 1053, Bottom: 76}
	anchor, ok := edgeFindBarQueryStartFromValueAtDPI(control, "abc", "abc", 96)
	if !ok {
		t.Fatal("100% DPI 下无法定位 Edge 查找框")
	}
	bounds := placePopup(anchor, rect{Left: 0, Top: 0, Right: 1920, Bottom: 1080}, 280, 36, 8)
	if bounds.Top != 73 || bounds.Left != 899 {
		t.Fatalf("100%% DPI 下弹窗没有跟随文本行: %+v", bounds)
	}
}
