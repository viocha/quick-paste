//go:build windows

package main

import "golang.org/x/sys/windows"

var (
	procDwmSetWindowAttribute = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")
	procMonitorFromRect       = user32.NewProc("MonitorFromRect")
	procGetMonitorInfo        = user32.NewProc("GetMonitorInfoW")
	procGetDpiForMonitor      = windows.NewLazySystemDLL("shcore.dll").NewProc("GetDpiForMonitor")
)

type monitorInfo struct {
	Size    uint32
	Monitor rect
	Work    rect
	Flags   uint32
}

// All coordinates are physical screen pixels, including negative monitor origins.
func placePopup(caret, work rect, width, height, gap int) rect {
	margin := int32(gap)
	left, top, right, bottom := work.Left+margin, work.Top+margin, work.Right-margin, work.Bottom-margin
	if right <= left || bottom <= top {
		left, top, right, bottom = work.Left, work.Top, work.Right, work.Bottom
	}
	w, h := min(int32(width), right-left), min(int32(height), bottom-top)
	x, y := caret.Left, caret.Bottom+margin
	if y+h > bottom {
		y = caret.Top - margin - h
	}
	x = max(left, min(x, right-w))
	y = max(top, min(y, bottom-h))
	return rect{x, y, x + w, y + h}
}

// The floating browser prefers the lower-right of the pointer. When that
// placement would cross the work area, shift it only as far as necessary to
// keep the whole window visible instead of flipping the entire panel.
func placeFloatingPanel(anchor, work rect, width, height, gap int) rect {
	w := min(int32(width), work.Right-work.Left)
	h := min(int32(height), work.Bottom-work.Top)
	if w <= 0 || h <= 0 {
		return work
	}
	g := int32(gap)
	x := anchor.Right + g
	y := anchor.Bottom + g
	x = max(work.Left, min(x, work.Right-w))
	y = max(work.Top, min(y, work.Bottom-h))
	return rect{x, y, x + w, y + h}
}
