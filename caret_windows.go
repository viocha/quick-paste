//go:build windows

package main

import (
	"golang.org/x/sys/windows"
	"math"
	"runtime"
	"strings"
	"syscall"
	"unicode/utf16"
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
	sysFreeString              = oleaut32.NewProc("SysFreeString")
	accessibleObjectFromWindow = windows.NewLazySystemDLL("oleacc.dll").NewProc("AccessibleObjectFromWindow")
	getWindowRect              = user32.NewProc("GetWindowRect")
	procQueryProcessImageName  = windows.NewLazySystemDLL("kernel32.dll").NewProc("QueryFullProcessImageNameW")
	edgeGDI                    = windows.NewLazySystemDLL("gdi32.dll")
	procEdgeCreateFont         = edgeGDI.NewProc("CreateFontW")
	procEdgeCreateDC           = edgeGDI.NewProc("CreateCompatibleDC")
	procEdgeSelectObject       = edgeGDI.NewProc("SelectObject")
	procEdgeGetTextExtent      = edgeGDI.NewProc("GetTextExtentPoint32W")
	procEdgeDeleteObject       = edgeGDI.NewProc("DeleteObject")
	procEdgeDeleteDC           = edgeGDI.NewProc("DeleteDC")
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
	iidTextEdit     = caretGUID("{17e21576-996c-4870-99d9-bff323380c06}")
	iidText2        = caretGUID("{506a921a-fcc9-409f-b23b-37eb74106872}")
	iidText         = caretGUID("{32eba289-3583-42c9-9c59-3b6d9a1e9b6a}")
	iidLegacy       = caretGUID("{828055ad-355b-4435-86d5-3b51c14a9b1b}")
	iidValue        = caretGUID("{a94cd8b1-0844-4cd6-9d2d-640537ab39e9}")
	iidAccessible   = caretGUID("{618736e0-3c3d-11cf-810c-00aa00389b71}")
)

// caretScreenRect is used only as a last-resort anchor for the manually opened
// browser. Automatic completion must use autocompleteCaretScreenRect instead so
// a focused control/window rectangle can never be mistaken for a text caret.
func caretScreenRect() rect {
	if bounds, found := autocompleteCaretScreenRect(""); found {
		return bounds
	}
	info := guiThreadInfo{CbSize: uint32(unsafe.Sizeof(guiThreadInfo{}))}
	procGetGUIThreadInfo.Call(0, uintptr(unsafe.Pointer(&info)))
	target := uintptr(info.HwndFocus)
	if target == 0 {
		target, _, _ = procGetForegroundWindow.Call()
	}
	var bounds rect
	getWindowRect.Call(target, uintptr(unsafe.Pointer(&bounds)))
	return bounds
}

type autocompleteCaretSource uint8

const (
	autocompleteCaretUnknown autocompleteCaretSource = iota
	autocompleteCaretComposition
	autocompleteCaretCompositionInline
	autocompleteCaretWin32
	autocompleteCaretAutomation
	autocompleteCaretAutomationQueryRange
	autocompleteCaretAccessible
)

// autocompleteCaretScreenRect returns only coordinates that are credibly a
// text insertion point. It deliberately has no focused-control/window fallback.
func autocompleteCaretScreenRect(query string) (rect, bool) {
	bounds, _, found := autocompleteCaretScreenAnchor(query)
	return bounds, found
}

func autocompleteCaretScreenAnchor(query string) (rect, autocompleteCaretSource, bool) {
	// Edge 地址栏在英文和中文预编辑时可能返回虚假的零长度
	// UIA 光标。优先根据输入框的 ValuePattern 计算补全前缀起点。
	if query != "" && foregroundIsEdge() {
		if bounds, ok := edgeOmniboxQueryStart(query); ok {
			return bounds, autocompleteCaretAutomationQueryRange, true
		}
	}
	info := guiThreadInfo{CbSize: uint32(unsafe.Sizeof(guiThreadInfo{}))}
	ok, _, _ := procGetGUIThreadInfo.Call(0, uintptr(unsafe.Pointer(&info)))
	// During IME composition the Win32 caret often stays at the beginning of the
	// composition string. UIA TextEditPattern exposes the active composition, so
	// use its trailing edge before accepting the stale Win32 caret.
	if bounds, inline, found := automationCompositionCaret(query); found && caretRectCredible(bounds, 64) {
		if inline {
			return bounds, autocompleteCaretCompositionInline, true
		}
		return bounds, autocompleteCaretComposition, true
	}
	if ok != 0 {
		if bounds, found := win32CaretScreenRectFromInfo(info); found && caretRectCredible(bounds, 64) {
			return bounds, autocompleteCaretWin32, true
		}
	}
	// Browser/Electron and other custom editors expose their caret through UIA/MSAA.
	// COM objects are created, used and released on the same MTA thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := coInitializeEx.Call(0, 0)
	if int32(hr) >= 0 {
		defer coUninitialize.Call()
		if bounds, queryRange, found := automationCaret(query); found && caretRectCredible(bounds, 64) {
			if queryRange {
				return bounds, autocompleteCaretAutomationQueryRange, true
			}
			return bounds, autocompleteCaretAutomation, true
		}
		for _, hwnd := range []uintptr{uintptr(info.HwndFocus), 0} {
			if bounds, found := accessibleCaret(hwnd); found && caretRectCredible(bounds, 16) {
				return bounds, autocompleteCaretAccessible, true
			}
		}
	}
	return rect{}, autocompleteCaretUnknown, false
}

func win32CaretScreenRect() (rect, bool) {
	info := guiThreadInfo{CbSize: uint32(unsafe.Sizeof(guiThreadInfo{}))}
	if ok, _, _ := procGetGUIThreadInfo.Call(0, uintptr(unsafe.Pointer(&info))); ok == 0 {
		return rect{}, false
	}
	return win32CaretScreenRectFromInfo(info)
}

func win32CaretScreenRectFromInfo(info guiThreadInfo) (rect, bool) {
	if info.HwndCaret == 0 || info.RcCaret.Bottom <= info.RcCaret.Top {
		return rect{}, false
	}
	origin := point{X: info.RcCaret.Left, Y: info.RcCaret.Top}
	converted, _, _ := procClientToScreen.Call(uintptr(info.HwndCaret), uintptr(unsafe.Pointer(&origin)))
	if converted == 0 {
		return rect{}, false
	}
	return rect{origin.X, origin.Y, origin.X + info.RcCaret.Right - info.RcCaret.Left, origin.Y + info.RcCaret.Bottom - info.RcCaret.Top}, true
}

func queryMovedEditorCaret(start, current rect) bool {
	return queryMovedEditorCaretAtDPI(start, current, dpiForScreenRect(current))
}

func queryMovedEditorCaretAtDPI(start, current rect, dpi uintptr) bool {
	movementTolerance := int32(scaleForDPI(2, dpi))
	if current.Top > start.Bottom+movementTolerance || current.Bottom < start.Top-movementTolerance {
		return true
	}
	return current.Left > start.Left+movementTolerance
}

func shouldApplyQueryLeftOffset(source autocompleteCaretSource, start rect, startValid bool, current rect) bool {
	switch source {
	case autocompleteCaretCompositionInline:
		return true
	case autocompleteCaretComposition, autocompleteCaretAutomationQueryRange:
		// The UIA query range already starts at the rendered prefix; do not
		// subtract the legacy fixed-width compensation a second time.
		return false
	case autocompleteCaretWin32:
		return startValid && queryMovedEditorCaret(start, current)
	default:
		// 得到光标坐标不代表前缀已显示，需另外检查光标左侧的实际文本。
		return false
	}
}

func caretRectCredible(bounds rect, maxWidth int32) bool {
	return caretRectCredibleAtDPI(bounds, maxWidth, dpiForScreenRect(bounds))
}

func caretRectCredibleAtDPI(bounds rect, maxWidth int32, dpi uintptr) bool {
	width := bounds.Right - bounds.Left
	height := bounds.Bottom - bounds.Top
	// Real insertion carets are narrow and roughly one text line high. Reject
	// control/dialog rectangles returned by incomplete accessibility providers.
	return width >= 0 && width <= int32(scaleForDPI(int(maxWidth), dpi)) && height > 0 && height <= int32(scaleForDPI(256, dpi))
}

// 部分内嵌网页/CEF 控件会把子元素的 UIA 坐标按窗口 DPI 再放大一次，
// 而宿主窗口本身仍返回正确的物理屏幕坐标。只有原矩形中心已经跑出
// 当前焦点窗口、按 DPI 反向映射后又落回窗口内时才修正，避免影响
// 正常的 UIA provider。
func normalizeAccessibilityRect(bounds rect) rect {
	host, dpi, ok := focusedWindowMetrics()
	if !ok {
		return bounds
	}
	return normalizeScaledProviderRect(bounds, host, dpi)
}

func focusedWindowMetrics() (rect, uintptr, bool) {
	info := guiThreadInfo{CbSize: uint32(unsafe.Sizeof(guiThreadInfo{}))}
	procGetGUIThreadInfo.Call(0, uintptr(unsafe.Pointer(&info)))
	target := uintptr(info.HwndFocus)
	if target == 0 {
		target = uintptr(info.HwndActive)
	}
	if target == 0 {
		target, _, _ = procGetForegroundWindow.Call()
	}
	if target == 0 {
		return rect{}, 0, false
	}
	var host rect
	if ok, _, _ := getWindowRect.Call(target, uintptr(unsafe.Pointer(&host))); ok == 0 || host.Right <= host.Left || host.Bottom <= host.Top {
		return rect{}, 0, false
	}
	dpi, _, _ := procGetDpiForWindow.Call(target)
	if dpi == 0 {
		dpi = 96
	}
	return host, dpi, true
}

func normalizeScaledProviderRect(bounds, host rect, dpi uintptr) rect {
	if dpi <= 96 || bounds.Right <= bounds.Left || bounds.Bottom <= bounds.Top || host.Right <= host.Left || host.Bottom <= host.Top {
		return bounds
	}
	if rectCenterInside(bounds, host) {
		return bounds
	}

	scaleDelta := func(value int32) int32 {
		return int32(int64(value) * 96 / int64(dpi))
	}
	candidate := rect{
		Left:   host.Left + scaleDelta(bounds.Left-host.Left),
		Top:    host.Top + scaleDelta(bounds.Top-host.Top),
		Right:  host.Left + scaleDelta(bounds.Right-host.Left),
		Bottom: host.Top + scaleDelta(bounds.Bottom-host.Top),
	}
	if candidate.Right <= candidate.Left || candidate.Bottom <= candidate.Top || !rectCenterInside(candidate, host) {
		return bounds
	}
	return candidate
}

func rectCenterInside(bounds, host rect) bool {
	x := int64(bounds.Left) + int64(bounds.Right-bounds.Left)/2
	y := int64(bounds.Top) + int64(bounds.Bottom-bounds.Top)/2
	return x >= int64(host.Left) && x <= int64(host.Right) && y >= int64(host.Top) && y <= int64(host.Bottom)
}

func automationCompositionCaret(query string) (rect, bool, bool) {
	bounds, _, found, inline := automationCompositionState(query)
	return bounds, inline, found
}

func automationCompositionState(query string) (rect, bool, bool, bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := coInitializeEx.Call(0, 0)
	if int32(hr) < 0 {
		return rect{}, false, false, false
	}
	defer coUninitialize.Call()

	var automation *caretCOM
	hr, _, _ = coCreateInstance.Call(uintptr(unsafe.Pointer(clsidAutomation)), 0, 1, uintptr(unsafe.Pointer(iidAutomation)), uintptr(unsafe.Pointer(&automation)))
	if int32(hr) < 0 || automation == nil {
		return rect{}, false, false, false
	}
	defer automation.release()

	var element *caretCOM
	if !automation.call(8, uintptr(unsafe.Pointer(&element))) || element == nil {
		return rect{}, false, false, false
	}
	defer element.release()

	var pattern *caretCOM
	if !element.call(14, 10032, uintptr(unsafe.Pointer(iidTextEdit)), uintptr(unsafe.Pointer(&pattern))) || pattern == nil {
		return rect{}, false, false, false
	}
	defer pattern.release()

	var textRange *caretCOM
	// IUIAutomationTextEditPattern::GetActiveComposition is vtable slot 9.
	if !pattern.call(9, uintptr(unsafe.Pointer(&textRange))) || textRange == nil {
		return rect{}, false, false, false
	}
	defer textRange.release()
	value := compositionRangeText(textRange)
	if query != "" && value != query {
		return rect{}, true, false, false
	}
	inline := compositionRangeIsInline(textRange, value)
	bounds, found := textRangeCaret(textRange, true)
	return bounds, true, found, inline
}

func compositionRangeIsInline(textRange *caretCOM, value string) bool {
	if value == "" {
		return false
	}
	bounds, ok := textRangeBounds(textRange)
	return ok && compositionBoundsInlineAtDPI(bounds, dpiForScreenRect(bounds))
}

func compositionBoundsInlineAtDPI(bounds rect, dpi uintptr) bool {
	return bounds.Right-bounds.Left > int32(scaleForDPI(2, dpi))
}

func compositionRangeText(textRange *caretCOM) string {
	var value uintptr
	if !textRange.call(12, uintptr(^uint32(0)), uintptr(unsafe.Pointer(&value))) || value == 0 {
		return ""
	}
	defer sysFreeString.Call(value)
	return windows.UTF16PtrToString((*uint16)(unsafe.Pointer(value)))
}

func automationFocusedQueryVisible(query string) bool {
	if query == "" {
		return false
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := coInitializeEx.Call(0, 0)
	if int32(hr) < 0 {
		return false
	}
	defer coUninitialize.Call()
	var automation *caretCOM
	hr, _, _ = coCreateInstance.Call(uintptr(unsafe.Pointer(clsidAutomation)), 0, 1, uintptr(unsafe.Pointer(iidAutomation)), uintptr(unsafe.Pointer(&automation)))
	if int32(hr) < 0 || automation == nil {
		return false
	}
	defer automation.release()
	var element *caretCOM
	if !automation.call(8, uintptr(unsafe.Pointer(&element))) || element == nil {
		return false
	}
	defer element.release()
	var isPassword int32
	if element.call(35, uintptr(unsafe.Pointer(&isPassword))) && isPassword != 0 {
		// 密码框可能返回明文或掩码，使用 IA2 核对插入点前的字符。
		return ia2PasswordQueryVisible(element, query)
	}
	for _, spec := range []struct {
		id  uintptr
		iid *windows.GUID
	}{{10024, iidText2}, {10014, iidText}} {
		var pattern *caretCOM
		if !element.call(14, spec.id, uintptr(unsafe.Pointer(spec.iid)), uintptr(unsafe.Pointer(&pattern))) || pattern == nil {
			continue
		}
		visible := textBeforeCaret(pattern, query) == query
		pattern.release()
		if visible {
			return true
		}
	}
	return false
}

func textRangeBeforeCaret(pattern *caretCOM, query string) (*caretCOM, bool) {
	if pattern == nil || query == "" {
		return nil, false
	}
	var ranges *caretCOM
	if !pattern.call(5, uintptr(unsafe.Pointer(&ranges))) || ranges == nil {
		return nil, false
	}
	defer ranges.release()
	var count int32
	if !ranges.call(3, uintptr(unsafe.Pointer(&count))) || count != 1 {
		return nil, false
	}
	var textRange *caretCOM
	if !ranges.call(4, 0, uintptr(unsafe.Pointer(&textRange))) || textRange == nil {
		return nil, false
	}
	if !textRange.call(15, 0, uintptr(unsafe.Pointer(textRange)), 1) {
		textRange.release()
		return nil, false
	}
	length := int32(len(utf16.Encode([]rune(query))))
	var moved int32
	if length == 0 || !textRange.call(14, 0, 0, uintptr(uint32(-length)), uintptr(unsafe.Pointer(&moved))) {
		textRange.release()
		return nil, false
	}
	return textRange, true
}

func textRangeText(textRange *caretCOM, length int32) string {
	if textRange == nil || length <= 0 {
		return ""
	}
	var value uintptr
	if !textRange.call(12, uintptr(length), uintptr(unsafe.Pointer(&value))) || value == 0 {
		return ""
	}
	defer sysFreeString.Call(value)
	return windows.UTF16PtrToString((*uint16)(unsafe.Pointer(value)))
}

func textRangeQueryBounds(pattern *caretCOM, query string) (rect, bool) {
	length := int32(len(utf16.Encode([]rune(query))))
	textRange, ok := textRangeBeforeCaret(pattern, query)
	if !ok {
		return rect{}, false
	}
	defer textRange.release()
	if textRangeText(textRange, length) != query {
		return rect{}, false
	}
	bounds, ok := textRangeBounds(textRange)
	if !ok || bounds.Right <= bounds.Left || bounds.Bottom <= bounds.Top {
		return rect{}, false
	}
	return bounds, true
}

func textBeforeCaret(pattern *caretCOM, query string) string {
	length := int32(len(utf16.Encode([]rune(query))))
	textRange, ok := textRangeBeforeCaret(pattern, query)
	if !ok {
		return ""
	}
	defer textRange.release()
	return textRangeText(textRange, length)
}

// textBeforeCaretAll 返回当前插入点之前的文本，不依赖插入点位于整段文本末尾。
// 先确认选区已折叠，再将范围起点移动到文档开头；不会改变真实输入框的选区。
func textBeforeCaretAll(pattern *caretCOM, maxCharacters int32) (string, bool) {
	if pattern == nil || maxCharacters <= 0 {
		return "", false
	}
	var ranges *caretCOM
	if !pattern.call(5, uintptr(unsafe.Pointer(&ranges))) || ranges == nil {
		return "", false
	}
	defer ranges.release()
	var count int32
	if !ranges.call(3, uintptr(unsafe.Pointer(&count))) || count != 1 {
		return "", false
	}
	var textRange *caretCOM
	if !ranges.call(4, 0, uintptr(unsafe.Pointer(&textRange))) || textRange == nil {
		return "", false
	}
	defer textRange.release()

	var comparison int32
	if !textRange.call(5, 0, uintptr(unsafe.Pointer(textRange)), 1, uintptr(unsafe.Pointer(&comparison))) || comparison != 0 {
		return "", false
	}
	var moved int32
	if !textRange.call(14, 0, 0, uintptr(int32(-maxCharacters)), uintptr(unsafe.Pointer(&moved))) {
		return "", false
	}
	var value uintptr
	if !textRange.call(12, uintptr(^uint32(0)), uintptr(unsafe.Pointer(&value))) || value == 0 {
		return "", false
	}
	defer sysFreeString.Call(value)
	return windows.UTF16PtrToString((*uint16)(unsafe.Pointer(value))), true
}

func automationCaret(query string) (rect, bool, bool) {
	var automation *caretCOM
	hr, _, _ := coCreateInstance.Call(uintptr(unsafe.Pointer(clsidAutomation)), 0, 1, uintptr(unsafe.Pointer(iidAutomation)), uintptr(unsafe.Pointer(&automation)))
	if int32(hr) < 0 || automation == nil {
		return rect{}, false, false
	}
	defer automation.release()
	var element *caretCOM
	if !automation.call(8, uintptr(unsafe.Pointer(&element))) || element == nil {
		return rect{}, false, false
	}
	defer element.release()
	// Chromium/CEF 等自绘输入框往往能通过 IA2 给出字符级光标位置，
	// 而 UIA TextPattern 的零长度选区矩形可能错误地落在输入框左边界。
	var isPassword int32
	var elementBounds rect
	passwordOK := element.call(35, uintptr(unsafe.Pointer(&isPassword)))
	boundsOK := element.call(43, uintptr(unsafe.Pointer(&elementBounds)))
	if boundsOK && elementBounds.Right > elementBounds.Left && elementBounds.Bottom > elementBounds.Top {
		elementBounds = normalizeAccessibilityRect(elementBounds)
		// Prefer the accessible object attached to the focused UIA element itself.
		// Windowless embedded editors can expose IAccessibleText here even when a
		// coordinate hit-test only returns a parent/document accessible object.
		if caret, ok := ia2CaretFromUIAElement(element, elementBounds, query); ok {
			return caret, false, true
		}
		if caret, ok := ia2CaretFromElement(elementBounds, query); ok {
			return caret, false, true
		}
	}
	// 密码控件的 UIA TextPattern 选择范围尤其不可信；IA2 失败后不要再
	// 把输入框本身或 TextPattern 返回的大矩形伪装成插入点。
	if passwordOK && isPassword != 0 {
		return rect{}, false, false
	}
	for _, spec := range []struct {
		id  uintptr
		iid *windows.GUID
	}{{10024, iidText2}, {10014, iidText}} {
		var pattern *caretCOM
		if !element.call(14, spec.id, uintptr(unsafe.Pointer(spec.iid)), uintptr(unsafe.Pointer(&pattern))) || pattern == nil {
			continue
		}
		if query != "" {
			if bounds, ok := textRangeQueryBounds(pattern, query); ok {
				pattern.release()
				if edgeOmniboxLikely(elementBounds) {
					// Edge 地址栏的零长度选区可能只是左侧内边距的
					// 伪光标；优先在统一的 Edge 地址栏分支计算查询起点。
					if anchor, ok := edgeOmniboxMeasuredCaret(element, elementBounds, bounds, query); ok {
						return anchor, true, true
					}
					// A genuinely sized text range ends at the insertion point.
					if bounds.Right-bounds.Left >= int32(scaleForDPI(12, dpiForScreenRect(bounds))) {
						bounds.Left = bounds.Right
					}
				}
				return bounds, true, true
			}
		}
		bounds, ok := patternCaret(pattern, spec.id == 10024)
		pattern.release()
		if ok {
			return bounds, false, true
		}
	}
	// Some embedded web controls expose neither TextPattern nor IA2 caret APIs,
	// but do expose their rendered text as UIA Text descendants. Only use that
	// geometry when ValuePattern proves the current field value ends with the
	// exact query we just observed, so editing in the middle cannot be misplaced.
	if boundsOK && query != "" {
		if caret, ok := automationTextDescendantCaret(automation, element, elementBounds, query); ok {
			return caret, false, true
		}
	}
	return rect{}, false, false
}

// edgeOmniboxLikely deliberately limits the measured fallback to Edge's
// toolbar-sized address bar. Other editors retain their provider coordinates.
func edgeOmniboxLikely(bounds rect) bool {
	if !foregroundIsEdge() {
		return false
	}
	dpi := dpiForScreenRect(bounds)
	if bounds.Right-bounds.Left < int32(scaleForDPI(320, dpi)) ||
		bounds.Bottom-bounds.Top > int32(scaleForDPI(48, dpi)) {
		return false
	}
	host, _, ok := focusedWindowMetrics()
	return ok && bounds.Top >= host.Top &&
		bounds.Top-host.Top < int32(scaleForDPI(170, dpi))
}

func foregroundIsEdge() bool {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return false
	}
	var pid uint32
	procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var name [512]uint16
	length := uint32(len(name))
	ok, _, _ := procQueryProcessImageName.Call(uintptr(handle), 0, uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(&length)))
	return ok != 0 && strings.HasSuffix(strings.ToLower(windows.UTF16ToString(name[:length])), "\\msedge.exe")
}

// Edge Ctrl+F 搜索框位于浏览器自己的独立 owned window 中。
// 其 TextRange 几何可能退化为控件左上角，因此使用插入点前的实际文本计算 X，
// Y 则使用单行文本在输入框内的垂直位置。
func edgeFindBarQueryState(query string) (anchor rect, target, valid bool) {
	foreground, _, _ := procGetForegroundWindow.Call()
	if foreground == 0 {
		return rect{}, false, false
	}
	owner, _, _ := user32.NewProc("GetWindow").Call(foreground, 4) // GW_OWNER
	if owner == 0 {
		return rect{}, false, false
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := coInitializeEx.Call(0, 0)
	if int32(hr) < 0 {
		return rect{}, false, false
	}
	defer coUninitialize.Call()

	var automation *caretCOM
	hr, _, _ = coCreateInstance.Call(uintptr(unsafe.Pointer(clsidAutomation)), 0, 1, uintptr(unsafe.Pointer(iidAutomation)), uintptr(unsafe.Pointer(&automation)))
	if int32(hr) < 0 || automation == nil {
		return rect{}, false, false
	}
	defer automation.release()

	var element *caretCOM
	if !automation.call(8, uintptr(unsafe.Pointer(&element))) || element == nil {
		return rect{}, false, false
	}
	defer element.release()

	className, ok := automationElementCurrentString(element, 30) // CurrentClassName
	if !ok {
		return rect{}, false, false
	}
	name, ok := automationElementCurrentString(element, 23) // CurrentName
	if !ok || !edgeFindBarFieldIdentified(className, name) {
		return rect{}, false, false
	}

	var control rect
	if !element.call(43, uintptr(unsafe.Pointer(&control))) {
		return rect{}, true, false
	}
	control = normalizeAccessibilityRect(control)
	var popup rect
	if got, _, _ := getWindowRect.Call(foreground, uintptr(unsafe.Pointer(&popup))); got == 0 ||
		!rectCenterInside(control, popup) {
		return rect{}, true, false
	}

	value, ok := automationElementValue(element)
	if !ok || len([]rune(value)) > 128 {
		return rect{}, true, false
	}
	var pattern *caretCOM
	if !element.call(14, 10014, uintptr(unsafe.Pointer(iidText)), uintptr(unsafe.Pointer(&pattern))) || pattern == nil {
		return rect{}, true, false
	}
	beforeCaret, readOK := textBeforeCaretAll(pattern, 128)
	pattern.release()
	if !readOK {
		return rect{}, true, false
	}
	prefix, prefixOK := edgeFindBarPrefixBeforeCaret(value, beforeCaret, query)
	if !prefixOK {
		return rect{}, true, false
	}
	anchor, valid = edgeFindBarQueryStartFromPrefixAtDPI(control, prefix, dpiForScreenRect(control))
	return anchor, true, valid
}

func automationElementCurrentString(element *caretCOM, slot int) (string, bool) {
	var value uintptr
	if !element.call(slot, uintptr(unsafe.Pointer(&value))) || value == 0 {
		return "", false
	}
	defer sysFreeString.Call(value)
	return windows.UTF16PtrToString((*uint16)(unsafe.Pointer(value))), true
}

func edgeFindBarFieldIdentified(className, name string) bool {
	label := strings.ToLower(name)
	return className == "Textfield" &&
		(strings.Contains(label, "查找") || strings.Contains(label, "find"))
}

func edgeFindBarQueryStartFromValue(control rect, value, query string) (rect, bool) {
	return edgeFindBarQueryStartFromValueAtDPI(control, value, query, dpiForScreenRect(control))
}

func edgeFindBarPrefixBeforeCaret(value, beforeCaret, query string) (string, bool) {
	if query == "" || len([]rune(value)) > 128 || len([]rune(beforeCaret)) > 128 ||
		!strings.HasPrefix(value, beforeCaret) || !strings.HasSuffix(beforeCaret, query) {
		return "", false
	}
	return strings.TrimSuffix(beforeCaret, query), true
}

func edgeFindBarQueryStartFromValueAtDPI(control rect, value, query string, dpi uintptr) (rect, bool) {
	if query == "" || !strings.HasSuffix(value, query) || len([]rune(value)) > 128 {
		return rect{}, false
	}
	return edgeFindBarQueryStartFromPrefixAtDPI(control, strings.TrimSuffix(value, query), dpi)
}

func edgeFindBarQueryStartFromPrefixAtDPI(control rect, prefix string, dpi uintptr) (rect, bool) {
	if len([]rune(prefix)) > 128 || control.Bottom <= control.Top {
		return rect{}, false
	}
	inset := int32(scaleForDPI(6, dpi))
	x := control.Left + inset
	if prefix != "" {
		width, ok := measureEdgeFindBarText(prefix, dpi)
		if !ok {
			return rect{}, false
		}
		x += width
	}
	height := control.Bottom - control.Top
	lineHeight := int32(scaleForDPI(11, dpi))
	if lineHeight <= 0 || control.Right-control.Left <= inset*2 {
		return rect{}, false
	}
	if lineHeight > height {
		lineHeight = height
	}
	if x < control.Left || x >= control.Right-inset {
		return rect{}, false
	}
	top := control.Top + (height-lineHeight)/2
	return rect{Left: x, Top: top, Right: x + 1, Bottom: top + lineHeight}, true
}

// Edge 的无障碍数据在中文预编辑与英文提交时都包含可见文本。
// 两种模式统一取前缀起点，不读取候选窗口的坐标或尺寸。
func edgeOmniboxQueryStart(query string) (rect, bool) {
	anchor, _, ok := edgeOmniboxQueryState(query)
	return anchor, ok
}

// target 表示当前确实聚焦在 Edge 地址栏。即使 ValuePattern
// 尚未更新，也不能把其它 UIA 伪光标当作地址栏插入点。
func edgeOmniboxQueryState(query string) (anchor rect, target, valid bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := coInitializeEx.Call(0, 0)
	if int32(hr) < 0 {
		return rect{}, false, false
	}
	defer coUninitialize.Call()

	var automation *caretCOM
	hr, _, _ = coCreateInstance.Call(uintptr(unsafe.Pointer(clsidAutomation)), 0, 1, uintptr(unsafe.Pointer(iidAutomation)), uintptr(unsafe.Pointer(&automation)))
	if int32(hr) < 0 || automation == nil {
		return rect{}, false, false
	}
	defer automation.release()

	var element *caretCOM
	if !automation.call(8, uintptr(unsafe.Pointer(&element))) || element == nil {
		return rect{}, false, false
	}
	defer element.release()

	var control rect
	if !element.call(43, uintptr(unsafe.Pointer(&control))) {
		return rect{}, false, false
	}
	control = normalizeAccessibilityRect(control)
	if !edgeOmniboxLikely(control) {
		return rect{}, false, false
	}
	value, ok := automationElementValue(element)
	if !ok {
		return rect{}, true, false
	}
	anchor, valid = edgeOmniboxQueryStartFromValue(control, value, query)
	return anchor, true, valid
}
func edgeOmniboxQueryStartFromValue(control rect, value, query string) (rect, bool) {
	return edgeOmniboxQueryStartFromValueAtDPI(control, value, query, dpiForScreenRect(control))
}

func edgeOmniboxQueryStartFromValueAtDPI(control rect, value, query string, dpi uintptr) (rect, bool) {
	if query == "" || !strings.HasSuffix(value, query) || len([]rune(value)) > 128 {
		return rect{}, false
	}
	inset := int32(scaleForDPI(4, dpi))
	x := control.Left + inset
	prefix := strings.TrimSuffix(value, query)
	if prefix != "" {
		width, ok := measureEdgeOmniboxText(prefix, dpi)
		if !ok {
			return rect{}, false
		}
		x += width
	}
	// 仅用于未发生横向滚动的地址栏：光标的屏幕坐标必须在控件内。
	if x < control.Left || x >= control.Right-inset ||
		control.Bottom-control.Top <= inset*2 {
		return rect{}, false
	}
	return rect{Left: x, Top: control.Top + inset, Right: x + 1, Bottom: control.Bottom - inset}, true
}

// 兼容 UIA TextRange 返回零宽度矩形的 Edge 地址栏。查询起点
// 只取 query 之前的字符宽度，避免把 query 自身宽度算进去。
func edgeOmniboxMeasuredCaret(element *caretCOM, control, bounds rect, query string) (rect, bool) {
	dpi := dpiForScreenRect(control)
	if bounds.Right-bounds.Left >= int32(scaleForDPI(12, dpi)) {
		return rect{}, false
	}
	value, ok := automationElementValue(element)
	if !ok {
		return rect{}, false
	}
	return edgeOmniboxQueryStartFromValue(control, value, query)
}
func measureEdgeOmniboxText(value string, dpi uintptr) (int32, bool) {
	return measureEdgeText(value, dpi, 14)
}

func measureEdgeFindBarText(value string, dpi uintptr) (int32, bool) {
	return measureEdgeText(value, dpi, 11)
}

func measureEdgeText(value string, dpi uintptr, logicalFontHeight int) (int32, bool) {
	if value == "" || logicalFontHeight <= 0 {
		return 0, false
	}
	face, _ := windows.UTF16PtrFromString("Segoe UI")
	height := int32(scaleForDPI(logicalFontHeight, dpi))
	font, _, _ := procEdgeCreateFont.Call(
		uintptr(int64(-height)), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 5, 0,
		uintptr(unsafe.Pointer(face)))
	if font == 0 {
		return 0, false
	}
	defer procEdgeDeleteObject.Call(font)
	dc, _, _ := procEdgeCreateDC.Call(0)
	if dc == 0 {
		return 0, false
	}
	defer procEdgeDeleteDC.Call(dc)
	original, _, _ := procEdgeSelectObject.Call(dc, font)
	if original == 0 {
		return 0, false
	}
	defer procEdgeSelectObject.Call(dc, original)
	chars := utf16.Encode([]rune(value))
	var size struct{ Width, Height int32 }
	ok, _, _ := procEdgeGetTextExtent.Call(dc, uintptr(unsafe.Pointer(&chars[0])), uintptr(len(chars)), uintptr(unsafe.Pointer(&size)))
	return size.Width, ok != 0
}
func automationTextDescendantCaret(automation, element *caretCOM, elementBounds rect, query string) (rect, bool) {
	if automation == nil || element == nil || query == "" {
		return rect{}, false
	}
	value, ok := automationElementValue(element)
	if !ok || !strings.HasSuffix(value, query) {
		return rect{}, false
	}

	var walker *caretCOM
	// IUIAutomation::get_RawViewWalker is vtable slot 16.
	if !automation.call(16, uintptr(unsafe.Pointer(&walker))) || walker == nil {
		return rect{}, false
	}
	defer walker.release()

	var best rect
	found := false
	visited := 0
	var walk func(*caretCOM, int)
	walk = func(parent *caretCOM, depth int) {
		if parent == nil || depth > 8 || visited >= 128 {
			return
		}
		var child *caretCOM
		if !walker.call(4, uintptr(unsafe.Pointer(parent)), uintptr(unsafe.Pointer(&child))) || child == nil {
			return
		}
		for child != nil && visited < 128 {
			visited++
			var controlType int32
			if child.call(21, uintptr(unsafe.Pointer(&controlType))) && controlType == 50020 { // UIA_TextControlTypeId
				var bounds rect
				if child.call(43, uintptr(unsafe.Pointer(&bounds))) {
					bounds = normalizeAccessibilityRect(bounds)
					if bounds.Right > bounds.Left && bounds.Bottom > bounds.Top && rectCenterInside(bounds, elementBounds) {
						if !found || bounds.Top > best.Top || (bounds.Top == best.Top && bounds.Right > best.Right) {
							best = bounds
							found = true
						}
					}
				}
			}
			walk(child, depth+1)
			var next *caretCOM
			walker.call(6, uintptr(unsafe.Pointer(child)), uintptr(unsafe.Pointer(&next)))
			child.release()
			child = next
		}
	}
	walk(element, 0)
	if !found {
		return rect{}, false
	}
	return rect{Left: best.Right, Top: best.Top, Right: best.Right + 1, Bottom: best.Bottom}, true
}

func automationElementValue(element *caretCOM) (string, bool) {
	var pattern *caretCOM
	if !element.call(14, 10002, uintptr(unsafe.Pointer(iidValue)), uintptr(unsafe.Pointer(&pattern))) || pattern == nil {
		return "", false
	}
	defer pattern.release()
	var value uintptr
	// IUIAutomationValuePattern::get_CurrentValue is vtable slot 4.
	if !pattern.call(4, uintptr(unsafe.Pointer(&value))) || value == 0 {
		return "", false
	}
	defer sysFreeString.Call(value)
	return windows.UTF16PtrToString((*uint16)(unsafe.Pointer(value))), true
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
	return textRangeCaret(textRange, false)
}

func textRangeCaret(textRange *caretCOM, collapseToEnd bool) (rect, bool) {
	if textRange == nil {
		return rect{}, false
	}
	if collapseToEnd {
		// Move the start endpoint to the range end without changing the editor's
		// actual selection/composition. The resulting zero-length range is the
		// insertion point after the currently typed IME composition text.
		textRange.call(15, 0, uintptr(unsafe.Pointer(textRange)), 1)
	}
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
	bounds := rect{int32(math.Floor(values[0])), int32(math.Floor(values[1])), int32(math.Ceil(values[0] + values[2])), int32(math.Ceil(values[1] + values[3]))}
	return normalizeAccessibilityRect(bounds), true
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
	bounds := rect{x, y, x + w, y + h}
	if !caretRectCredible(bounds, 16) {
		return rect{}, false
	}
	return bounds, true
}
