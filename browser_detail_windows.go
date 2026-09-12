//go:build windows

package main

import (
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type BrowserDetail struct {
	Token      int    `json:"token"`
	Content    string `json:"content"`
	Side       string `json:"side"`
	ArrowY     int    `json:"arrowY"`
	Scrollable bool   `json:"scrollable"`
	Image      string `json:"image,omitempty"`
}

func containsPoint(bounds rect, p point) bool {
	return p.X >= bounds.Left && p.X < bounds.Right && p.Y >= bounds.Top && p.Y < bounds.Bottom
}

// Prefer the right side, then the left, reducing width only when neither fits.
func placeBrowserDetail(panel, work rect, rowCenter int32, width, height, gap int) rect {
	g := int32(gap)
	rightSpace := max(int32(0), work.Right-panel.Right-g)
	leftSpace := max(int32(0), panel.Left-work.Left-g)
	w, h := min(int32(width), work.Right-work.Left), min(int32(height), work.Bottom-work.Top)
	x := panel.Right + g
	if rightSpace < w {
		if leftSpace >= w || leftSpace > rightSpace {
			w = min(w, leftSpace)
			x = panel.Left - g - w
		} else if rightSpace > 0 {
			w = min(w, rightSpace)
		}
	}
	if w <= 0 {
		w = min(int32(width), work.Right-work.Left)
	}
	x = max(work.Left, min(x, work.Right-w))
	y := max(work.Top, min(rowCenter-h/2, work.Bottom-h))
	return rect{x, y, x + w, y + h}
}

func browserDetailHeightWithLimit(contentHeight, workHeight int, dpi uintptr, maxLogicalHeight int) (int, bool) {
	if dpi == 0 {
		dpi = 96
	}
	limit := min(maxLogicalHeight, workHeight*96/int(dpi))
	// Rounding allowance must not cause otherwise fitting content to scroll.
	logicalHeight := max(28, min(contentHeight+2, limit))
	return min(workHeight, (logicalHeight*int(dpi)+95)/96), contentHeight > limit
}

func browserDetailHeight(contentHeight, workHeight int, dpi uintptr) (int, bool) {
	return browserDetailHeightWithLimit(contentHeight, workHeight, dpi, 320)
}

func (s *BrowserService) GetDetail() BrowserDetail {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.detail
}

func (s *BrowserService) ShowDetail(id string, top, height int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.visible || s.dragging || s.detailWindow == nil {
		return
	}
	hwnd := uintptr(s.window.NativeWindow())
	var panel rect
	getWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&panel)))
	dpi, _, _ := procGetDpiForWindow.Call(hwnd)
	if dpi == 0 {
		dpi = 96
	}
	row := rect{panel.Left, panel.Top + int32(scaleForDPI(top, dpi)), panel.Right, panel.Top + int32(scaleForDPI(top+height, dpi))}
	var cursor point
	user32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&cursor)))
	// Discard a delayed RPC if the pointer has already left its trigger.
	if !containsPoint(row, cursor) {
		return
	}
	for _, node := range s.store.List() {
		if node.ID != id || node.Type != NodeTypePhrase || node.Content == "" {
			continue
		}
		s.hideDetailLocked()
		s.detailID = id
		s.detailTriggerActive = true
		s.detail.Content = node.Content
		s.detailRow = row
		s.detailWindow.EmitEvent("browser-detail", s.detail)
		return
	}
	if s.clipboard != nil {
		for _, item := range s.clipboard.store.List("") {
			if item.ID != id {
				continue
			}
			s.hideDetailLocked()
			s.detailID = id
			s.detailTriggerActive = true
			s.detailRow = row
			if item.Kind == ClipboardImage {
				s.detail.Image, _ = s.clipboard.previewData(id)
				s.detail.Content = ""
			} else if text, err := s.clipboard.GetText(id); err == nil {
				s.detail.Content = text
			} else {
				s.detail.Content = item.Preview
			}
			s.detailWindow.EmitEvent("browser-detail", s.detail)
			return
		}
	}
}

// The detail WebView reports its rendered height before it is made visible,
// preventing a previously hovered phrase from flashing during a new hover.
func (s *BrowserService) DetailReady(token, height int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.visible || s.dragging || s.detailID == "" || token != s.detail.Token {
		return
	}
	var panel rect
	hwnd := uintptr(s.window.NativeWindow())
	getWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&panel)))
	monitor, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&panel)), 2)
	info := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	if ok, _, _ := procGetMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info))); ok == 0 {
		return
	}
	dpi, _, _ := procGetDpiForWindow.Call(hwnd)
	if dpi == 0 {
		dpi = 96
	}
	// Decide whether content actually exceeds the permitted window height,
	// independently of the WebView's previous viewport and scrollbar state.
	logicalWidth, maxLogicalHeight := 300, 320
	if s.detail.Image != "" {
		logicalWidth, maxLogicalHeight = 520, 460
	}
	physicalHeight, scrollable := browserDetailHeightWithLimit(height, int(info.Work.Bottom-info.Work.Top), dpi, maxLogicalHeight)
	bounds := placeBrowserDetail(panel, info.Work, (s.detailRow.Top+s.detailRow.Bottom)/2, scaleForDPI(logicalWidth, dpi), physicalHeight, 0)
	side := "right"
	if bounds.Right <= panel.Left {
		side = "left"
	}
	arrowY := max(14, min(int((s.detailRow.Top+s.detailRow.Bottom)/2-bounds.Top)*96/int(dpi), int(bounds.Bottom-bounds.Top)*96/int(dpi)-14))
	if s.detail.Side != side || s.detail.ArrowY != arrowY || s.detail.Scrollable != scrollable {
		s.detail.Scrollable = scrollable
		s.detail.Side = side
		s.detail.ArrowY = arrowY
		s.detailWindow.EmitEvent("browser-detail-layout", s.detail)
	}
	application.InvokeSync(func() {
		detailHWND := uintptr(s.detailWindow.NativeWindow())
		// Do not apply the candidate popup's DWM frame/corner decorations here.
		procSetWindowPos.Call(detailHWND, hwndTopmost, uintptr(int64(bounds.Left)), uintptr(int64(bounds.Top)), uintptr(bounds.Right-bounds.Left), uintptr(bounds.Bottom-bounds.Top), swpNoActivate)
		if !s.detailInitialised {
			foreground, _, _ := procGetForegroundWindow.Call()
			s.detailWindow.Show()
			s.detailInitialised = true
			current, _, _ := procGetForegroundWindow.Call()
			if current == detailHWND && foreground != 0 {
				procSetForegroundWindow.Call(foreground)
			}
		}
		procShowWindow.Call(detailHWND, 4)
	})
	s.detailVisible = true
}

func (s *BrowserService) HideDetail(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.detailID == id {
		s.detailTriggerActive = false
		s.detailLeave = time.Now().Add(180 * time.Millisecond)
	}
}

func (s *BrowserService) DismissDetail() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hideDetailLocked()
}

func (s *BrowserService) hideDetailLocked() {
	s.detail.Token++
	s.detail.Content = ""
	s.detail.Image = ""
	s.detail.Scrollable = false
	s.detailID = ""
	s.detailTriggerActive = false
	s.detailLeave = time.Time{}
	if s.detailVisible && s.detailWindow != nil {
		application.InvokeSync(func() { procShowWindow.Call(uintptr(s.detailWindow.NativeWindow()), 0) })
	}
	s.detailVisible = false
}

// Allow crossing the small gap to scroll long details without taking focus.
func (s *BrowserService) checkDetailHoverLocked() {
	if s.detailID == "" {
		return
	}
	var cursor point
	user32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&cursor)))
	inside := s.detailTriggerActive && containsPoint(s.detailRow, cursor)
	if s.detailVisible {
		var bounds rect
		getWindowRect.Call(uintptr(s.detailWindow.NativeWindow()), uintptr(unsafe.Pointer(&bounds)))
		inside = inside || containsPoint(bounds, cursor)
	}
	if inside {
		s.detailLeave = time.Time{}
		return
	}
	if s.detailLeave.IsZero() {
		s.detailLeave = time.Now().Add(180 * time.Millisecond)
	}
	if time.Now().After(s.detailLeave) {
		s.hideDetailLocked()
	}
}
