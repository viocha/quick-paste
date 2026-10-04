//go:build windows

package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 测试使用真实 Win32 剪贴板，结束时恢复用户内容。
func temporaryClipboardTestService(t *testing.T) *ClipboardService {
	t.Helper()
	s := &ClipboardService{htmlFormat: registerClipboardFormat("HTML Format"), rtfFormat: registerClipboardFormat("Rich Text Format")}
	original := readClipboardBackup(t)
	t.Cleanup(func() { setClipboardBackup(t, s, original) })
	return s
}

func openTestClipboard(t *testing.T, hwnd uintptr) {
	t.Helper()
	for deadline := time.Now().Add(time.Second); ; {
		if ok, _, _ := procOpenClipboard.Call(hwnd); ok != 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("剪贴板持续被占用")
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func readClipboardBackup(t *testing.T) map[uint32][]byte {
	t.Helper()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	class, _ := windows.UTF16PtrFromString("STATIC")
	hwnd, _, _ := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(class)), 0, 0, 0, 0, 0, 0, hwndMessage, 0, 0, 0)
	defer procDestroyWindow.Call(hwnd)
	openTestClipboard(t, hwnd)
	defer procCloseClipboard.Call()
	backup, err := backupOpenClipboard()
	if err != nil {
		t.Fatal(err)
	}
	return backup
}

func setClipboardBackup(t *testing.T, s *ClipboardService, formats map[uint32][]byte) {
	t.Helper()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if len(formats) > 0 {
		if err := s.write(formats); err != nil {
			t.Fatal(err)
		}
		return
	}
	openTestClipboard(t, 0)
	defer procCloseClipboard.Call()
	if ok, _, err := procEmptyClipboard.Call(); ok == 0 {
		t.Fatal(err)
	}
}

func assertClipboardBackup(t *testing.T, expected map[uint32][]byte) {
	t.Helper()
	actual := readClipboardBackup(t)
	if len(actual) != len(expected) {
		t.Fatalf("格式数 = %d，期望 %d", len(actual), len(expected))
	}
	for format, data := range expected {
		if !bytes.Equal(actual[format], data) {
			t.Fatalf("剪贴板格式 %d 未完整恢复", format)
		}
	}
}

func TestTemporaryClipboardRestoresAfterReaderCloses(t *testing.T) {
	s := temporaryClipboardTestService(t)
	exclude := registerClipboardFormat("ExcludeClipboardContentFromMonitorProcessing")
	custom := registerClipboardFormat("quick-paste-test-custom")
	dib := make([]byte, 44)
	binary.LittleEndian.PutUint32(dib, 40)
	binary.LittleEndian.PutUint32(dib[4:], 1)
	binary.LittleEndian.PutUint32(dib[8:], 1)
	binary.LittleEndian.PutUint16(dib[12:], 1)
	binary.LittleEndian.PutUint16(dib[14:], 32)
	copy(dib[40:], []byte{20, 40, 80, 255})
	for _, fixture := range []struct {
		name    string
		formats map[uint32][]byte
	}{
		{"空剪贴板", nil},
		{"图片", map[uint32][]byte{cfDIB: dib, exclude: {0, 0, 0, 0}}},
		{"富文本和自定义格式", map[uint32][]byte{cfUnicodeText: encodeUTF16Clipboard("原内容 😀"), s.htmlFormat: []byte("<b>原内容</b>\x00"), custom: {1, 2, 3}, exclude: {0, 0, 0, 0}}},
		{"文件列表", map[uint32][]byte{cfHDrop: append([]byte{20, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0}, append(encodeUTF16Clipboard(`C:\test.txt`), 0, 0)...), exclude: {0, 0, 0, 0}}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			setClipboardBackup(t, s, fixture.formats)
			before := readClipboardBackup(t)
			start, release := make(chan struct{}), make(chan struct{})
			cancel := make(chan struct{})
			read := make(chan string, 1)
			ready, readerDone := make(chan uintptr, 1), make(chan struct{})
			go func() {
				runtime.LockOSThread()
				defer runtime.UnlockOSThread()
				defer close(readerDone)
				class, _ := windows.UTF16PtrFromString("EDIT")
				hwnd, _, _ := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(class)), 0, 0, 0, 0, 0, 0, hwndMessage, 0, 0, 0)
				defer procDestroyWindow.Call(hwnd)
				ready <- hwnd
				select {
				case <-start:
				case <-cancel:
					return
				}
				openTestClipboard(t, hwnd)
				data, err := readClipboardFormat(cfUnicodeText)
				if err != nil {
					read <- "读取失败: " + err.Error()
				} else {
					read <- decodeUTF16Clipboard(data)
				}
				<-release
				procCloseClipboard.Call()
			}()
			hwnd := <-ready
			result := make(chan error, 1)
			go func() { result <- s.pasteText("临时短语 😀", hwnd, func() error { close(start); return nil }) }()
			select {
			case got := <-read:
				if got != "临时短语 😀" {
					t.Errorf("粘贴内容 = %q", got)
				}
			case err := <-result:
				close(cancel)
				close(release)
				<-readerDone
				t.Fatalf("读取前事务结束: %v", err)
			case <-time.After(6 * time.Second):
				close(cancel)
				close(release)
				t.Fatal("目标未读取临时短语")
			}
			select {
			case err := <-result:
				close(release)
				<-readerDone
				t.Fatalf("读取者关闭剪贴板前事务已完成: %v", err)
			default:
			}
			close(release)
			<-readerDone
			select {
			case err := <-result:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(6 * time.Second):
				t.Fatal("恢复超时")
			}
			assertClipboardBackup(t, before)
		})
	}
}

func TestTemporaryClipboardPastesIntoNativeEdit(t *testing.T) {
	s := temporaryClipboardTestService(t)
	setClipboardBackup(t, s, map[uint32][]byte{cfUnicodeText: encodeUTF16Clipboard("手动粘贴保留内容"), registerClipboardFormat("ExcludeClipboardContentFromMonitorProcessing"): {0, 0, 0, 0}})
	before := readClipboardBackup(t)
	ready := make(chan uintptr, 1)
	start := make(chan struct{})
	text := make(chan string, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		class, _ := windows.UTF16PtrFromString("EDIT")
		hwnd, _, _ := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(class)), 0, 4, 0, 0, 100, 100, 0, 0, 0, 0)
		defer procDestroyWindow.Call(hwnd)
		ready <- hwnd
		<-start
		// 原生控件以关联窗口请求数据，避免 OpenClipboard(NULL) 的来源歧义。
		openTestClipboard(t, hwnd)
		if _, err := readClipboardFormat(cfUnicodeText); err != nil {
			procCloseClipboard.Call()
			text <- err.Error()
			return
		}
		user32.NewProc("SendMessageW").Call(hwnd, 0x0302, 0, 0) // WM_PASTE
		procCloseClipboard.Call()
		buffer := make([]uint16, 128)
		procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
		text <- windows.UTF16ToString(buffer)
	}()
	hwnd := <-ready
	if hwnd == 0 {
		t.Fatal("创建原生编辑框失败")
	}
	if err := s.pasteText("原生编辑框短语 😀", hwnd, func() error { close(start); return nil }); err != nil {
		t.Fatal(err)
	}
	if got := <-text; got != "原生编辑框短语 😀" {
		t.Fatalf("原生编辑框内容 = %q", got)
	}
	assertClipboardBackup(t, before)
}

func TestTemporaryClipboardRestoresOnSendFailureAndTimeout(t *testing.T) {
	s := temporaryClipboardTestService(t)
	for _, test := range []struct {
		name string
		send func() error
	}{
		{"发送失败", func() error { return errors.New("测试发送失败") }},
		{"目标未读取", func() error { return nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			setClipboardBackup(t, s, map[uint32][]byte{cfUnicodeText: encodeUTF16Clipboard("保留内容"), registerClipboardFormat("ExcludeClipboardContentFromMonitorProcessing"): {0, 0, 0, 0}})
			before := readClipboardBackup(t)
			if err := s.pasteText("临时短语", 0, test.send); err == nil {
				t.Fatal("应返回错误")
			}
			assertClipboardBackup(t, before)
		})
	}
}

func TestTemporaryClipboardKeepsNewCopy(t *testing.T) {
	s := temporaryClipboardTestService(t)
	// 外部写入不经过服务的互斥锁，模拟其他应用。
	external := &ClipboardService{}
	err := s.pasteText("临时短语", 0, func() error {
		return external.write(map[uint32][]byte{cfUnicodeText: encodeUTF16Clipboard("用户新复制"), registerClipboardFormat("ExcludeClipboardContentFromMonitorProcessing"): {0, 0, 0, 0}})
	})
	if err == nil {
		t.Fatal("剪贴板被改写后应取消粘贴")
	}
	if got := decodeUTF16Clipboard(readClipboardBackup(t)[cfUnicodeText]); got != "用户新复制" {
		t.Fatalf("覆盖了用户新复制内容: %q", got)
	}
}

// 浏览器回归需先聚焦 textarea，并指定测试窗口；不在普通测试中操作桌面。
func TestTemporaryClipboardBrowserConfirmation(t *testing.T) {
	value := os.Getenv("QUICK_PASTE_TEST_BROWSER_HWND")
	if value == "" {
		t.Skip("未指定浏览器测试窗口")
	}
	hwnd, err := strconv.ParseUint(value, 0, 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := restoreWindowFocus(uintptr(hwnd), 0); err != nil {
		t.Fatal(err)
	}
	s := temporaryClipboardTestService(t)
	setClipboardBackup(t, s, map[uint32][]byte{cfUnicodeText: encodeUTF16Clipboard("原剪贴板保留内容"), registerClipboardFormat("ExcludeClipboardContentFromMonitorProcessing"): {0, 0, 0, 0}})
	before := readClipboardBackup(t)
	method := runtimeInputCtrlV
	sequence := clipboardSequence()
	usedClipboard := false
	if os.Getenv("QUICK_PASTE_TEST_BROWSER_SHIFT_INSERT") == "1" {
		method = runtimeInputShiftInsert
	}
	if os.Getenv("QUICK_PASTE_TEST_BROWSER_COMPLETION") == "1" {
		previous := completionInsertMethodRuntime.Swap(method)
		defer completionInsertMethodRuntime.Store(previous)
		result := make(chan error, 1)
		state := &windowsInputState{
			monitor: &InputMonitor{pasteClipboardText: func(text string, target uintptr, remove int, send func() error) error {
				err := s.pasteTextWithRemoval(text, target, remove, func() error { usedClipboard = true; return send() })
				result <- err
				return err
			}},
			query: "qptest", suggestions: []Suggestion{{Content: "浏览器短语回归 😀"}},
			lastForeground: uintptr(hwnd), actions: make(chan struct{}, 1), done: make(chan struct{}),
			swallowKeyUp: make(map[uint32]bool), hideView: func() {}, nativeInputMode: func() bool { return false },
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
	} else {
		if err := s.pasteText("浏览器短语回归 😀", uintptr(hwnd), func() error { usedClipboard = true; return sendPasteShortcut(method) }); err != nil {
			t.Fatal(err)
		}
	}
	if os.Getenv("QUICK_PASTE_TEST_EXPECT_FALLBACK") == "1" && (usedClipboard || clipboardSequence() != sequence) {
		t.Fatal("降级输入不应调用粘贴回调或修改系统剪贴板")
	}
	assertClipboardBackup(t, before)
}
