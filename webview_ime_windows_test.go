//go:build windows

package main

import (
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"
	"unsafe"
)

// 聚焦空白邮箱框并启用搜狗中文模式，检查浮动候选与已上屏前缀的定位区别。
func TestWebViewIMEPrefixPosition(t *testing.T) {
	value := os.Getenv("QUICK_PASTE_TEST_WEBVIEW_POSITION")
	if value == "" {
		t.Skip("未指定 WebView2 前缀定位测试窗口")
	}
	hwnd, err := strconv.ParseUint(value, 0, 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := restoreWindowFocus(uintptr(hwnd), 0); err != nil {
		t.Fatal(err)
	}
	start, startValid := win32CaretScreenRect()
	if err := sendReplacementWithInterval(0, "qptest", 0); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(time.Second); ; {
		found, visible := sogouCandidateWindowState()
		if found && visible {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("未进入中文候选状态")
		}
		time.Sleep(20 * time.Millisecond)
	}
	caret, source, found := autocompleteCaretScreenAnchor("qptest")
	if !found {
		t.Fatal("未取得光标位置")
	}
	if automationFocusedQueryVisible("qptest") || shouldApplyQueryLeftOffset(source, start, startValid, caret) {
		t.Fatal("浮动候选中的前缀不能触发左移")
	}
	if err := sendFullReplacementWithInterval(6, "qptest", 0); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(time.Second); !automationFocusedQueryVisible("qptest"); {
		if time.Now().After(deadline) {
			t.Fatal("已上屏前缀未识别为光标左侧文本")
		}
		time.Sleep(20 * time.Millisecond)
	}
	inputs := make([]keyboardInput, 0, 12)
	for i := 0; i < 6; i++ {
		inputs = append(inputs, keyboardInput{Type: inputKeyboard, Ki: keybdInput{Vk: vkLeft, ExtraInfo: quickPasteInputMarker}}, keyboardInput{Type: inputKeyboard, Ki: keybdInput{Vk: vkLeft, Flags: keyeventfKeyup, ExtraInfo: quickPasteInputMarker}})
	}
	if err := sendKeyboardInputs(inputs); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(time.Second); automationFocusedQueryVisible("qptest"); {
		if time.Now().After(deadline) {
			t.Fatal("光标位于前缀前时不能根据输入框后缀触发左移")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// 手动聚焦测试输入框并启用中文输入法；普通测试不操作用户窗口。
func TestWebViewIMECompletion(t *testing.T) {
	value := os.Getenv("QUICK_PASTE_TEST_WEBVIEW_IME")
	if value == "" {
		t.Skip("未指定 WebView2 中文输入测试窗口")
	}
	hwnd, err := strconv.ParseUint(value, 0, 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := restoreWindowFocus(uintptr(hwnd), 0); err != nil {
		t.Fatal(err)
	}
	if err := sendReplacementWithInterval(0, "qptest", 0); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(time.Second); ; {
		found, visible := sogouCandidateWindowState()
		if found && visible {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("测试前缀未进入中文候选状态")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !nativeIMEInputMode() {
		t.Fatal("未识别 WebView2 子进程的中文候选状态")
	}
	s := temporaryClipboardTestService(t)
	before := readClipboardBackup(t)
	method := completionInsertMethodRuntime.Swap(runtimeInputCtrlV)
	defer completionInsertMethodRuntime.Store(method)
	result := make(chan error, 1)
	state := &windowsInputState{
		monitor: &InputMonitor{pasteClipboardText: func(text string, target uintptr, remove int, send func() error) error {
			err := s.pasteTextWithRemoval(text, target, remove, send)
			result <- err
			return err
		}},
		query: "qptest", suggestions: []Suggestion{{Content: "qptest@example.com"}}, lastForeground: uintptr(hwnd),
		actions: make(chan struct{}, 1), done: make(chan struct{}), swallowKeyUp: make(map[uint32]bool),
		hideView: func() {}, nativeInputMode: nativeIMEInputMode,
	}
	go state.runActions()
	defer close(state.done)
	if !state.handleKey(wmKeyDown, vkReturn) || !state.handleKey(wmKeyUp, vkReturn) {
		t.Fatal("Enter 未接受补全")
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("补全超时")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	coInitializeEx.Call(0, 0)
	defer coUninitialize.Call()
	var automation, element *caretCOM
	coCreateInstance.Call(uintptr(unsafe.Pointer(clsidAutomation)), 0, 1, uintptr(unsafe.Pointer(iidAutomation)), uintptr(unsafe.Pointer(&automation)))
	if automation == nil {
		t.Fatal("创建 UIA 失败")
	}
	defer automation.release()
	for deadline := time.Now().Add(time.Second); ; {
		if element != nil {
			element.release()
			element = nil
		}
		if automation.call(8, uintptr(unsafe.Pointer(&element))) && element != nil {
			actual, ok := automationElementValue(element)
			_, visible := sogouCandidateWindowState()
			if ok && actual == "qptest@example.com" && !visible {
				break
			}
			if time.Now().After(deadline) {
				element.release()
				t.Fatalf("补全结果=%q，候选可见=%v", actual, visible)
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("无法读取补全结果")
		}
		time.Sleep(20 * time.Millisecond)
	}
	element.release()
	assertClipboardBackup(t, before)
}
