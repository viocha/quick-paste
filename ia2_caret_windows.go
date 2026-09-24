//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	accessibleObjectFromPoint = windows.NewLazySystemDLL("oleacc.dll").NewProc("AccessibleObjectFromPoint")
	iidServiceProvider        = caretGUID("{6D5140C1-7436-11CE-8034-00AA006009FA}")
	iidAccessibleText         = caretGUID("{24FD2FFB-3AAD-4A08-8335-A3AD89C0FB4B}")
)

// Chromium 的密码框可通过 IAccessibleText 返回屏幕坐标；UIA TextPattern 的
// 选区矩形在同一控件上可能覆盖整个输入框或落在框外。
func ia2CaretFromElement(bounds rect) (rect, bool) {
	if bounds.Right <= bounds.Left || bounds.Bottom <= bounds.Top {
		return rect{}, false
	}
	x := bounds.Left + (bounds.Right-bounds.Left)/2
	y := bounds.Top + (bounds.Bottom-bounds.Top)/2
	packedPoint := uintptr(uint64(uint32(x)) | uint64(uint32(y))<<32)
	var accessible *caretCOM
	var child struct {
		vt       uint16
		reserved [3]uint16
		value    int64
		padding  [8]byte
	}
	hr, _, _ := accessibleObjectFromPoint.Call(packedPoint, uintptr(unsafe.Pointer(&accessible)), uintptr(unsafe.Pointer(&child)))
	if int32(hr) < 0 || accessible == nil {
		return rect{}, false
	}
	defer accessible.release()
	if child.vt != 3 || child.value != 0 {
		return rect{}, false
	}
	return ia2CaretFromAccessible(accessible, bounds)
}

func ia2CaretFromUIAElement(element *caretCOM, bounds rect) (rect, bool) {
	if element == nil {
		return rect{}, false
	}
	var legacy *caretCOM
	if !element.call(14, 10018, uintptr(unsafe.Pointer(iidLegacy)), uintptr(unsafe.Pointer(&legacy))) || legacy == nil {
		return rect{}, false
	}
	defer legacy.release()

	var accessible *caretCOM
	// IUIAutomationLegacyIAccessiblePattern::GetIAccessible is vtable slot 26.
	if !legacy.call(26, uintptr(unsafe.Pointer(&accessible))) || accessible == nil {
		return rect{}, false
	}
	defer accessible.release()
	return ia2CaretFromAccessible(accessible, bounds)
}

func ia2CaretFromAccessible(accessible *caretCOM, bounds rect) (rect, bool) {
	if accessible == nil {
		return rect{}, false
	}

	var service *caretCOM
	if !accessible.call(0, uintptr(unsafe.Pointer(iidServiceProvider)), uintptr(unsafe.Pointer(&service))) || service == nil {
		return rect{}, false
	}
	defer service.release()
	var text *caretCOM
	if !service.call(3, uintptr(unsafe.Pointer(iidAccessibleText)), uintptr(unsafe.Pointer(iidAccessibleText)), uintptr(unsafe.Pointer(&text))) || text == nil {
		return rect{}, false
	}
	defer text.release()

	var offset int32 = -1
	if !text.call(5, uintptr(unsafe.Pointer(&offset))) || offset < 0 {
		return rect{}, false
	}
	var cx, cy, width, height int32
	if !text.call(6, uintptr(offset), 0, uintptr(unsafe.Pointer(&cx)), uintptr(unsafe.Pointer(&cy)), uintptr(unsafe.Pointer(&width)), uintptr(unsafe.Pointer(&height))) || height <= 0 {
		return rect{}, false
	}
	caret := normalizeAccessibilityRect(rect{cx, cy, cx + max(width, 1), cy + height})
	// 命中控件中的装饰按钮或其他子元素时，不使用其坐标。
	if caret.Left < bounds.Left || caret.Left > bounds.Right || caret.Bottom <= bounds.Top || caret.Top >= bounds.Bottom {
		return rect{}, false
	}
	return caret, true
}
