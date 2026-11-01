//go:build windows

package main

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestBrowserDetailPreparedOnDemand(t *testing.T) {
	first, second := &application.WebviewWindow{}, &application.WebviewWindow{}
	window := first
	calls := 0
	s := &BrowserService{ensureDetailWindow: func() *application.WebviewWindow {
		calls++
		return window
	}}
	s.PrepareDetail()
	if calls != 0 {
		t.Fatal("隐藏面板不应创建详情窗口")
	}
	s.visible = true
	s.PrepareDetail()
	if calls != 1 || s.detailWindow != first {
		t.Fatal("悬停准备应创建详情窗口")
	}
	s.detailInitialised = true
	s.detailID = "previous"
	token := s.detail.Token
	s.PrepareDetail()
	if !s.detailInitialised || s.detailID != "previous" || s.detail.Token != token {
		t.Fatal("复用窗口不应重置当前详情")
	}
	window = second
	s.PrepareDetail()
	if s.detailWindow != second || s.detailInitialised || s.detailID != "" || s.detail.Token <= token {
		t.Fatal("回收后创建的新窗口应清除过期详情")
	}
	s.dragging = true
	s.PrepareDetail()
	if calls != 3 {
		t.Fatal("拖动面板时不应准备详情窗口")
	}
}

func TestBrowserDetailSidePlacement(t *testing.T) {
	work := rect{0, 0, 1920, 1040}
	panel := rect{100, 100, 420, 460}
	right := placeBrowserDetail(panel, work, 200, 300, 180, 4)
	if right.Left != 424 || right.Top != 110 || right.Right-right.Left != 300 {
		t.Fatalf("right placement: %+v", right)
	}
	panel = rect{1500, 700, 1820, 1060}
	left := placeBrowserDetail(panel, work, 950, 300, 180, 4)
	if left.Right != 1496 || left.Top != 860 || left.Bottom > work.Bottom {
		t.Fatalf("left placement: %+v", left)
	}
	for _, tc := range []struct {
		panel, work        rect
		width, height, gap int
	}{
		{rect{-400, -800, -80, -440}, rect{-1920, -1080, 0, 0}, 420, 480, 4},
		{rect{200, 50, 520, 410}, rect{0, 0, 640, 480}, 640, 600, 4},
		{rect{1920, 20, 2400, 560}, rect{1920, 0, 3840, 2100}, 450, 480, 6},
		{rect{0, 0, 320, 360}, rect{0, 0, 320, 200}, 300, 320, 4},
	} {
		b := placeBrowserDetail(tc.panel, tc.work, tc.panel.Top, tc.width, tc.height, tc.gap)
		if b.Left < tc.work.Left || b.Top < tc.work.Top || b.Right > tc.work.Right || b.Bottom > tc.work.Bottom || b.Right <= b.Left || b.Bottom <= b.Top {
			t.Fatalf("outside work area: %+v", b)
		}
	}
}

func TestBrowserDetailCentersOnTargetAndClampsAtScreenEdges(t *testing.T) {
	work := rect{0, 0, 1920, 1040}
	panel := rect{500, 100, 820, 460}
	for _, height := range []int{44, 120, 320} {
		bounds := placeBrowserDetail(panel, work, 300, 300, height, 4)
		if (bounds.Top+bounds.Bottom)/2 != 300 {
			t.Fatalf("height %d: not centered: %+v", height, bounds)
		}
	}
	top := placeBrowserDetail(panel, work, 10, 300, 120, 4)
	bottom := placeBrowserDetail(panel, work, 1030, 300, 120, 4)
	if top.Top != 0 || bottom.Bottom != 1040 {
		t.Fatalf("edge clamp: %+v %+v", top, bottom)
	}
}

func TestBrowserDetailHeightAndScrolling(t *testing.T) {
	for _, tc := range []struct {
		content, work int
		dpi           uintptr
		physical      int
		scroll        bool
	}{
		{62, 1040, 96, 64, false}, {62, 1040, 120, 80, false}, {62, 1040, 144, 96, false},
		{479, 1040, 96, 480, false},
		{480, 1040, 96, 480, false}, {481, 1040, 96, 480, true}, {900, 1040, 144, 720, true},
		{90, 100, 144, 99, true}, {30, 1040, 0, 32, false},
	} {
		height, scroll := browserDetailHeight(tc.content, tc.work, tc.dpi)
		if height != tc.physical || scroll != tc.scroll {
			t.Fatalf("%+v: height=%d scroll=%v", tc, height, scroll)
		}
	}
}
