//go:build windows

package main

import (
	"unicode/utf16"
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
	text := ia2TextFromElement(bounds)
	if text == nil {
		return rect{}, false
	}
	defer text.release()
	return ia2CaretFromText(text, bounds)
}

func ia2TextFromElement(bounds rect) *caretCOM {
	if bounds.Right <= bounds.Left || bounds.Bottom <= bounds.Top {
		return nil
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
		return nil
	}
	defer accessible.release()
	if child.vt != 3 || child.value != 0 {
		return nil
	}
	return ia2TextFromAccessible(accessible)
}

func ia2CaretFromUIAElement(element *caretCOM, bounds rect) (rect, bool) {
	text := ia2TextFromUIAElement(element)
	if text == nil {
		return rect{}, false
	}
	defer text.release()
	return ia2CaretFromText(text, bounds)
}

func ia2TextFromUIAElement(element *caretCOM) *caretCOM {
	if element == nil {
		return nil
	}
	var legacy *caretCOM
	if !element.call(14, 10018, uintptr(unsafe.Pointer(iidLegacy)), uintptr(unsafe.Pointer(&legacy))) || legacy == nil {
		return nil
	}
	defer legacy.release()

	var accessible *caretCOM
	// IUIAutomationLegacyIAccessiblePattern::GetIAccessible is vtable slot 26.
	if !legacy.call(26, uintptr(unsafe.Pointer(&accessible))) || accessible == nil {
		return nil
	}
	defer accessible.release()
	return ia2TextFromAccessible(accessible)
}

// 返回的 IAccessibleText 由调用方释放。
func ia2TextFromAccessible(accessible *caretCOM) *caretCOM {
	if accessible == nil {
		return nil
	}

	var service *caretCOM
	if !accessible.call(0, uintptr(unsafe.Pointer(iidServiceProvider)), uintptr(unsafe.Pointer(&service))) || service == nil {
		return nil
	}
	defer service.release()
	var text *caretCOM
	if !service.call(3, uintptr(unsafe.Pointer(iidAccessibleText)), uintptr(unsafe.Pointer(iidAccessibleText)), uintptr(unsafe.Pointer(&text))) || text == nil {
		return nil
	}
	return text
}

func ia2CaretFromText(text *caretCOM, bounds rect) (rect, bool) {
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

func ia2PasswordQueryVisible(element *caretCOM, query string) bool {
	text := ia2TextFromUIAElement(element)
	if text == nil && element != nil {
		var bounds rect
		if element.call(43, uintptr(unsafe.Pointer(&bounds))) {
			text = ia2TextFromElement(normalizeAccessibilityRect(bounds))
		}
	}
	if text == nil {
		return false
	}
	defer text.release()
	return ia2PasswordTextHasPrefix(text, query)
}

func ia2PasswordTextHasPrefix(text *caretCOM, query string) bool {
	if text == nil || query == "" {
		return false
	}
	var offset int32 = -1
	var selections int32
	length := int32(len(utf16.Encode([]rune(query))))
	// IAccessibleText 的字符偏移按 UTF-16 计数；选区不能当作插入点。
	if !text.call(5, uintptr(unsafe.Pointer(&offset))) || offset < length ||
		!text.call(7, uintptr(unsafe.Pointer(&selections))) || selections != 0 {
		return false
	}
	var value uintptr
	// IAccessibleText::get_text 是 vtable slot 10，只读取插入点前的前缀范围。
	if !text.call(10, uintptr(offset-length), uintptr(offset), uintptr(unsafe.Pointer(&value))) || value == 0 {
		return false
	}
	defer sysFreeString.Call(value)
	return passwordPrefixMatchesQuery(windows.UTF16PtrToString((*uint16)(unsafe.Pointer(value))), query)
}

func passwordPrefixMatchesQuery(value, query string) bool {
	if query == "" || len(utf16.Encode([]rune(value))) != len(utf16.Encode([]rune(query))) {
		return false
	}
	if value == query {
		return true
	}
	// Chromium 等密码控件通过无障碍接口提供掩码；同长度的掩码证明字符已上屏。
	for _, ch := range value {
		switch ch {
		case '\u2022', '\u25cf', '*':
		default:
			return false
		}
	}
	return true
}
