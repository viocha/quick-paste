//go:build windows

package main

import (
	"syscall"
	"testing"
	"unsafe"
)

func TestPasteConfirmationChecksPasswordBeforeReadingText(t *testing.T) {
	var isPassword int32
	var propertyFailed bool
	var patternRequests int
	table := &[64]uintptr{}
	table[35] = syscall.NewCallback(func(_ uintptr, out uintptr) uintptr {
		if propertyFailed {
			return 0x80004005
		}
		*(*int32)(unsafe.Pointer(out)) = isPassword
		return 0
	})
	table[14] = syscall.NewCallback(func(_ uintptr, patternID, iid, out uintptr) uintptr {
		patternRequests++
		return 0x80004005
	})
	element := &caretCOM{vtable: table}
	for _, tc := range []struct {
		name            string
		password        int32
		propertyFailed  bool
		patternRequests int
	}{
		{"密码控件直接输入", 1, false, 0},
		{"无法识别控件直接输入", 0, true, 0},
		{"普通控件读取文本", 0, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isPassword, propertyFailed, patternRequests = tc.password, tc.propertyFailed, 0
			if confirmation := newPasteConfirmationFromElement(element, "补全内容", 3); confirmation != nil {
				confirmation.release()
				t.Fatal("不可读取的文本不能建立粘贴确认")
			}
			if patternRequests != tc.patternRequests {
				t.Fatalf("文本接口请求次数 = %d，期望 %d", patternRequests, tc.patternRequests)
			}
		})
	}
	if newPasteConfirmationFromElement(nil, "补全内容", 3) != nil {
		t.Fatal("缺少焦点控件时不能建立粘贴确认")
	}
}
