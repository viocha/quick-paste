//go:build windows

package main

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wmClipboardUpdate = 0x031D
	wmClipboardStop   = 0x8002
	hwndMessage       = ^uintptr(2) // HWND_MESSAGE == (HWND)-3
)

var (
	procCreateWindowExW               = user32.NewProc("CreateWindowExW")
	procDestroyWindow                 = user32.NewProc("DestroyWindow")
	procAddClipboardFormatListener    = user32.NewProc("AddClipboardFormatListener")
	procRemoveClipboardFormatListener = user32.NewProc("RemoveClipboardFormatListener")
	procPostMessageW                  = user32.NewProc("PostMessageW")
)

// runClipboardListener 使用 message-only window 接收 WM_CLIPBOARDUPDATE。
// 没有剪贴板变化时线程会阻塞在 GetMessageW，不产生周期性唤醒。
func (s *ClipboardService) runClipboardListener(done chan struct{}, ready chan<- error) {
	defer close(done)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	className, _ := windows.UTF16PtrFromString("STATIC")
	windowName, _ := windows.UTF16PtrFromString("quick-paste-clipboard-listener")
	hwnd, _, createErr := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(windowName)),
		0,
		0, 0, 0, 0,
		hwndMessage,
		0, 0, 0,
	)
	if hwnd == 0 {
		ready <- fmt.Errorf("CreateWindowExW: %w", createErr)
		return
	}
	defer procDestroyWindow.Call(hwnd)

	if ok, _, err := procAddClipboardFormatListener.Call(hwnd); ok == 0 {
		ready <- fmt.Errorf("AddClipboardFormatListener: %w", err)
		return
	}
	defer procRemoveClipboardFormatListener.Call(hwnd)

	threadID, _, _ := procGetCurrentThreadID.Call()
	s.mu.Lock()
	s.listenerThread = uint32(threadID)
	s.listenerWindow = hwnd
	s.mu.Unlock()
	// 覆盖 start() 读取初始 sequence 到监听器注册完成之间的极短窗口。
	s.handleClipboardUpdate()
	ready <- nil

	var message msg
	for {
		result, _, err := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) == -1 {
			_ = err
			return
		}
		if result == 0 || message.Message == wmClipboardStop {
			return
		}
		if message.Message == wmClipboardUpdate {
			s.handleClipboardUpdate()
		}
	}
}

func postClipboardListenerStop(threadID uint32, hwnd uintptr) bool {
	if hwnd != 0 {
		if ok, _, _ := procPostMessageW.Call(hwnd, wmClipboardStop, 0, 0); ok != 0 {
			return true
		}
	}
	if threadID != 0 {
		const wmQuit = 0x0012
		if ok, _, _ := procPostThreadMessageW.Call(uintptr(threadID), wmQuit, 0, 0); ok != 0 {
			return true
		}
	}
	return false
}
