//go:build windows

package main

import (
	"golang.org/x/sys/windows"
	"math"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	ole32                      = windows.NewLazySystemDLL("ole32.dll")
	coInitializeEx             = ole32.NewProc("CoInitializeEx")
	coUninitialize             = ole32.NewProc("CoUninitialize")
	coCreateInstance           = ole32.NewProc("CoCreateInstance")
	oleaut32                   = windows.NewLazySystemDLL("oleaut32.dll")
	safeArrayDestroy           = oleaut32.NewProc("SafeArrayDestroy")
	safeArrayGetLBound         = oleaut32.NewProc("SafeArrayGetLBound")
	safeArrayGetUBound         = oleaut32.NewProc("SafeArrayGetUBound")
	safeArrayAccessData        = oleaut32.NewProc("SafeArrayAccessData")
	safeArrayUnaccessData      = oleaut32.NewProc("SafeArrayUnaccessData")
	accessibleObjectFromWindow = windows.NewLazySystemDLL("oleacc.dll").NewProc("AccessibleObjectFromWindow")
	getWindowRect              = user32.NewProc("GetWindowRect")
)

// COM slots below follow the Windows SDK UIAutomationClient.h and oleacc.h.
type caretCOM struct{ vtable *[64]uintptr }

//go:uintptrescapes
func (c *caretCOM) call(slot int, args ...uintptr) bool {
	values := append([]uintptr{uintptr(unsafe.Pointer(c))}, args...)
	hr, _, _ := syscall.SyscallN(c.vtable[slot], values...)
	return int32(hr) >= 0
}
func (c *caretCOM) release() {
	if c != nil {
		c.call(2)
	}
}
func caretGUID(value string) *windows.GUID {
	g, err := windows.GUIDFromString(value)
	if err != nil {
		panic(err)
	}
	return &g
}

var (
	clsidAutomation = caretGUID("{ff48dba4-60ef-4201-aa87-54103eef594e}")
	iidAutomation   = caretGUID("{30cbe57d-d9d0-452a-ab13-7ac5ac4825ee}")
	iidText2        = caretGUID("{506a921a-fcc9-409f-b23b-37eb74106872}")
	iidText         = caretGUID("{32eba289-3583-42c9-9c59-3b6d9a1e9b6a}")
	iidAccessible   = caretGUID("{618736e0-3c3d-11cf-810c-00aa00389b71}")
)

func caretScreenRect() rect { return caretScreenRectWithFallback(false) }

func caretScreenRectWithFallback(mouseFallback bool) rect {
	info := guiThreadInfo{CbSize: uint32(unsafe.Sizeof(guiThreadInfo{}))}
	ok, _, _ := procGetGUIThreadInfo.Call(0, uintptr(unsafe.Pointer(&info)))
	if ok != 0 && info.HwndCaret != 0 && info.RcCaret.Bottom > info.RcCaret.Top {
		origin := point{X: info.RcCaret.Left, Y: info.RcCaret.Top}
		converted, _, _ := procClientToScreen.Call(uintptr(info.HwndCaret), uintptr(unsafe.Pointer(&origin)))
		if converted != 0 {
			return rect{origin.X, origin.Y, origin.X + info.RcCaret.Right - info.RcCaret.Left, origin.Y + info.RcCaret.Bottom - info.RcCaret.Top}
		}
	}
	// Browser/Electron and other custom editors expose their caret through UIA/MSAA.
	// COM objects are created, used and released on the same MTA thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := coInitializeEx.Call(0, 0)
	if int32(hr) >= 0 {
		defer coUninitialize.Call()
		if bounds, found := automationCaret(); found {
			return bounds
		}
		for _, hwnd := range []uintptr{uintptr(info.HwndFocus), 0} {
			if bounds, found := accessibleCaret(hwnd); found {
				return bounds
			}
		}
	}
	// The manually opened browser can fall back to the pointer when no text
	// caret is exposed. Automatic completion keeps its focused-control fallback.
	if mouseFallback {
		var cursor point
		if ok, _, _ := user32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&cursor))); ok != 0 {
			return rect{cursor.X, cursor.Y, cursor.X + 1, cursor.Y + 1}
		}
	}
	// If an editor exposes no caret, anchor to the focused control, never the mouse.
	target := uintptr(info.HwndFocus)
	if target == 0 {
		target, _, _ = procGetForegroundWindow.Call()
	}
	var bounds rect
	getWindowRect.Call(target, uintptr(unsafe.Pointer(&bounds)))
	return bounds
}

func automationCaret() (rect, bool) {
	var automation *caretCOM
	hr, _, _ := coCreateInstance.Call(uintptr(unsafe.Pointer(clsidAutomation)), 0, 1, uintptr(unsafe.Pointer(iidAutomation)), uintptr(unsafe.Pointer(&automation)))
	if int32(hr) < 0 || automation == nil {
		return rect{}, false
	}
	defer automation.release()
	var element *caretCOM
	if !automation.call(8, uintptr(unsafe.Pointer(&element))) || element == nil {
		return rect{}, false
	}
	defer element.release()
	for _, spec := range []struct {
		id  uintptr
		iid *windows.GUID
	}{{10024, iidText2}, {10014, iidText}} {
		var pattern *caretCOM
		if !element.call(14, spec.id, uintptr(unsafe.Pointer(spec.iid)), uintptr(unsafe.Pointer(&pattern))) || pattern == nil {
			continue
		}
		bounds, ok := patternCaret(pattern, spec.id == 10024)
		pattern.release()
		if ok {
			return bounds, true
		}
	}
	return rect{}, false
}
func patternCaret(pattern *caretCOM, text2 bool) (rect, bool) {
	var textRange *caretCOM
	if text2 {
		var active int32
		if !pattern.call(10, uintptr(unsafe.Pointer(&active)), uintptr(unsafe.Pointer(&textRange))) || active == 0 {
			textRange.release()
			return rect{}, false
		}
	} else {
		var ranges *caretCOM
		if !pattern.call(5, uintptr(unsafe.Pointer(&ranges))) || ranges == nil {
			return rect{}, false
		}
		defer ranges.release()
		var count int32
		if !ranges.call(3, uintptr(unsafe.Pointer(&count))) || count != 1 {
			return rect{}, false
		}
		if !ranges.call(4, 0, uintptr(unsafe.Pointer(&textRange))) {
			return rect{}, false
		}
		// Collapse the selection to its end, where subsequent typing will occur.
		if textRange != nil {
			textRange.call(15, 0, uintptr(unsafe.Pointer(textRange)), 1)
		}
	}
	if textRange == nil {
		return rect{}, false
	}
	defer textRange.release()
	if bounds, ok := textRangeBounds(textRange); ok {
		return bounds, true
	}
	// Some providers return no rectangle for a zero-length range. Use the preceding
	// character's trailing edge instead (without changing the editor's selection).
	var moved int32
	if textRange.call(14, 0, 0, uintptr(^uint32(0)), uintptr(unsafe.Pointer(&moved))) && moved != 0 {
		if bounds, ok := textRangeBounds(textRange); ok {
			bounds.Left = bounds.Right
			return bounds, true
		}
	}
	if textRange.call(6, 0) {
		return textRangeBounds(textRange)
	}
	return rect{}, false
}
func textRangeBounds(textRange *caretCOM) (rect, bool) {
	var array uintptr
	if !textRange.call(10, uintptr(unsafe.Pointer(&array))) || array == 0 {
		return rect{}, false
	}
	defer safeArrayDestroy.Call(array)
	var lower, upper int32
	h1, _, _ := safeArrayGetLBound.Call(array, 1, uintptr(unsafe.Pointer(&lower)))
	h2, _, _ := safeArrayGetUBound.Call(array, 1, uintptr(unsafe.Pointer(&upper)))
	if int32(h1) < 0 || int32(h2) < 0 || upper-lower+1 < 4 {
		return rect{}, false
	}
	var data *[4]float64
	hr, _, _ := safeArrayAccessData.Call(array, uintptr(unsafe.Pointer(&data)))
	if int32(hr) < 0 || data == nil {
		return rect{}, false
	}
	values := *data
	safeArrayUnaccessData.Call(array)
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1e7 {
			return rect{}, false
		}
	}
	if values[3] <= 0 {
		return rect{}, false
	}
	return rect{int32(math.Floor(values[0])), int32(math.Floor(values[1])), int32(math.Ceil(values[0] + values[2])), int32(math.Ceil(values[1] + values[3]))}, true
}
func accessibleCaret(hwnd uintptr) (rect, bool) {
	var accessible *caretCOM
	hr, _, _ := accessibleObjectFromWindow.Call(hwnd, 0xfffffff8, uintptr(unsafe.Pointer(iidAccessible)), uintptr(unsafe.Pointer(&accessible)))
	if int32(hr) < 0 || accessible == nil {
		return rect{}, false
	}
	defer accessible.release()
	// VARIANT(VT_I4, CHILDID_SELF); Win64 passes the 24-byte value by reference.
	child := struct {
		vt       uint16
		reserved [3]uint16
		value    int64
		padding  [8]byte
	}{vt: 3}
	var x, y, w, h int32
	if !accessible.call(22, uintptr(unsafe.Pointer(&x)), uintptr(unsafe.Pointer(&y)), uintptr(unsafe.Pointer(&w)), uintptr(unsafe.Pointer(&h)), uintptr(unsafe.Pointer(&child))) || h <= 0 {
		return rect{}, false
	}
	return rect{x, y, x + w, y + h}, true
}
