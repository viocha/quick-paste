//go:build windows

package main

import (
	"fmt"
	"syscall"
)

// 在线程内、窗口激活前执行；只限制明确设置了 NOACTIVATE 的窗口。
var noActivateCallback = syscall.NewCallback(func(code int, wParam, lParam uintptr) uintptr {
	if code == 5 || code == 9 { // HCBT_ACTIVATE、HCBT_SETFOCUS
		root, _, _ := user32.NewProc("GetAncestor").Call(wParam, 2)
		if root == 0 {
			root = wParam
		}
		index := int32(gwlExStyle)
		style, _, _ := procGetWindowLongW.Call(root, uintptr(uint32(index)))
		if style&wsExNoActivate != 0 {
			return 1
		}
	}
	result, _, _ := procCallNextHookEx.Call(0, uintptr(code), wParam, lParam)
	return result
})

// 必须在窗口 UI 线程调用；钩子随该线程退出由 Windows 释放。
func installNoActivateGuard() error {
	threadID, _, _ := procGetCurrentThreadID.Call()
	hook, _, err := procSetWindowsHookExW.Call(5, noActivateCallback, 0, threadID) // WH_CBT
	if hook == 0 {
		return fmt.Errorf("设置窗口不激活保护失败: %w", err)
	}
	return nil
}
