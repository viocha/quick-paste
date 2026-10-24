//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

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

// 输入矩形使用物理屏幕坐标，按其所在显示器的缩放换算逻辑像素阈值。
func dpiForScreenRect(bounds rect) uintptr {
	monitor, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&bounds)), 2)
	if monitor != 0 {
		var dpiX, dpiY uint32
		if hr, _, _ := procGetDpiForMonitor.Call(monitor, 0, uintptr(unsafe.Pointer(&dpiX)), uintptr(unsafe.Pointer(&dpiY))); int32(hr) >= 0 && dpiX > 0 {
			return uintptr(dpiX)
		}
	}
	return 96
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
	g := int32(gap)
	left, top, right, bottom := work.Left+g, work.Top+g, work.Right-g, work.Bottom-g
	if right <= left || bottom <= top {
		left, top, right, bottom = work.Left, work.Top, work.Right, work.Bottom
	}
	w := min(int32(width), right-left)
	h := min(int32(height), bottom-top)
	if w <= 0 || h <= 0 {
		return work
	}
	x := anchor.Right + g
	y := anchor.Bottom + g
	x = max(left, min(x, right-w))
	y = max(top, min(y, bottom-h))
	return rect{x, y, x + w, y + h}
}
