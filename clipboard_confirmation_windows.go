//go:build windows

package main

import (
	"strings"
	"unsafe"
)

// pasteConfirmation 在同一 COM 线程读取目标控件，确认文本已经应用。
// Clipboard Close 只表示一次读取结束，不能证明异步粘贴已完成。
type pasteConfirmation struct {
	pattern  *caretCOM
	element  *caretCOM
	expected string
}

func newPasteConfirmation(target uintptr, text string, backspaces int) *pasteConfirmation {
	foreground, _, _ := procGetForegroundWindow.Call()
	if foreground != target {
		return nil
	}
	var automation *caretCOM
	hr, _, _ := coCreateInstance.Call(uintptr(unsafe.Pointer(clsidAutomation)), 0, 1, uintptr(unsafe.Pointer(iidAutomation)), uintptr(unsafe.Pointer(&automation)))
	if int32(hr) < 0 || automation == nil {
		return nil
	}
	defer automation.release()
	var element *caretCOM
	if !automation.call(8, uintptr(unsafe.Pointer(&element))) || element == nil {
		return nil
	}
	defer element.release()
	var pattern *caretCOM
	if !element.call(14, 10014, uintptr(unsafe.Pointer(iidText)), uintptr(unsafe.Pointer(&pattern))) || pattern == nil {
		return nil
	}
	keep := false
	defer func() {
		if !keep {
			pattern.release()
		}
	}()
	var document, selections, selection, prefix, suffix *caretCOM
	if !pattern.call(7, uintptr(unsafe.Pointer(&document))) || document == nil {
		return nil
	}
	defer document.release()
	if !pattern.call(5, uintptr(unsafe.Pointer(&selections))) || selections == nil {
		return nil
	}
	defer selections.release()
	var count int32
	if !selections.call(3, uintptr(unsafe.Pointer(&count))) || count != 1 {
		return nil
	}
	if !selections.call(4, 0, uintptr(unsafe.Pointer(&selection))) || selection == nil {
		return nil
	}
	defer selection.release()
	if !document.call(3, uintptr(unsafe.Pointer(&prefix))) || prefix == nil {
		return nil
	}
	defer prefix.release()
	if !document.call(3, uintptr(unsafe.Pointer(&suffix))) || suffix == nil {
		return nil
	}
	defer suffix.release()
	// MoveEndpointByRange：前缀结束于选区起点，后缀起始于选区终点。
	if !prefix.call(15, 1, uintptr(unsafe.Pointer(selection)), 0) || !suffix.call(15, 0, uintptr(unsafe.Pointer(selection)), 1) {
		return nil
	}
	left := []rune(compositionRangeText(prefix))
	if backspaces > len(left) {
		return nil
	}
	if backspaces > 0 {
		left = left[:len(left)-backspaces]
	}
	expected := normalizePasteText(string(left) + text + compositionRangeText(suffix))
	baseline := normalizePasteText(compositionRangeText(document))
	value, hasValue := automationElementValue(element)
	if hasValue {
		value = normalizePasteText(value)
		if value == "" && baseline == "\uFFFC" && backspaces == 0 {
			// Chromium 用对象占位符表示空编辑框，不属于实际文本。
			expected = normalizePasteText(text)
		} else if value != baseline {
			return nil
		}
		baseline = value
	} else if baseline == "\uFFFC" && backspaces == 0 {
		// contenteditable 的空内容同样由对象占位符表示。
		expected = normalizePasteText(text)
	}
	// 未改变文本的替换无法提供可靠的完成信号。
	if expected == baseline {
		return nil
	}
	keep = true
	confirmation := &pasteConfirmation{pattern: pattern, expected: expected}
	if hasValue {
		element.call(1)
		confirmation.element = element
	}
	return confirmation
}

func normalizePasteText(text string) string { return strings.ReplaceAll(text, "\r\n", "\n") }

func (c *pasteConfirmation) completed() bool {
	if c.element != nil {
		value, ok := automationElementValue(c.element)
		return ok && normalizePasteText(value) == c.expected
	}
	var document *caretCOM
	if !c.pattern.call(7, uintptr(unsafe.Pointer(&document))) || document == nil {
		return false
	}
	defer document.release()
	actual := normalizePasteText(compositionRangeText(document))
	return actual == c.expected
}

func (c *pasteConfirmation) release() { c.pattern.release(); c.element.release() }
