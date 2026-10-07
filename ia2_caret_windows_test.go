//go:build windows

package main

import (
	"syscall"
	"testing"
	"unicode/utf16"
	"unsafe"
)

func TestPasswordPrefixMatchesQuery(t *testing.T) {
	for _, tc := range []struct {
		name, value, query string
		want               bool
	}{
		{"明文前缀", "abc", "abc", true},
		{"圆点掩码", "•••", "abc", true},
		{"黑圆掩码", "●●●", "abc", true},
		{"星号掩码", "***", "abc", true},
		{"明文不匹配", "xyz", "abc", false},
		{"字符不足", "••", "abc", false},
		{"超出请求范围", "••••", "abc", false},
		{"空文本", "", "abc", false},
		{"空查询", "", "", false},
		{"混入其他文本", "••x", "abc", false},
		{"UTF16范围", "••••", "a😀b", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := passwordPrefixMatchesQuery(tc.value, tc.query); got != tc.want {
				t.Fatalf("匹配结果 = %v，期望 %v", got, tc.want)
			}
		})
	}
}

func TestIA2PasswordTextHasPrefix(t *testing.T) {
	// 用 COM 回调验证真实 vtable 调用和范围参数，不修改用户输入框。
	var offset, selections int32
	var content, query string
	var failedSlot int
	var reads int
	table := &[64]uintptr{}
	getter := func(slot int, value *int32) uintptr {
		return syscall.NewCallback(func(_ uintptr, out uintptr) uintptr {
			if failedSlot == slot {
				return 0x80004005
			}
			*(*int32)(unsafe.Pointer(out)) = *value
			return 0
		})
	}
	table[5] = getter(5, &offset)
	table[7] = getter(7, &selections)
	table[10] = syscall.NewCallback(func(_ uintptr, start, end, out uintptr) uintptr {
		reads++
		length := int32(len(utf16.Encode([]rune(query))))
		if int32(start) != offset-length || int32(end) != offset {
			t.Errorf("读取范围 = [%d, %d)，期望 [%d, %d)", start, end, offset-length, offset)
		}
		if failedSlot == 10 {
			return 0x80004005
		}
		encoded := utf16.Encode([]rune(content))
		var data *uint16
		if len(encoded) > 0 {
			data = &encoded[0]
		}
		value, _, _ := oleaut32.NewProc("SysAllocStringLen").Call(uintptr(unsafe.Pointer(data)), uintptr(len(encoded)))
		*(*uintptr)(unsafe.Pointer(out)) = value
		return 0
	})
	text := &caretCOM{vtable: table}
	for _, tc := range []struct {
		name, value, query string
		offset, selections int32
		failedSlot         int
		want               bool
		wantReads          int
	}{
		{"密码末尾", "•••", "abc", 3, 0, 0, true, 1},
		{"密码中间", "•••", "abc", 5, 0, 0, true, 1},
		{"不足三个字符", "", "abc", 2, 0, 0, false, 0},
		{"光标在开头", "", "abc", 0, 0, 0, false, 0},
		{"无活动光标", "", "abc", -1, 0, 0, false, 0},
		{"存在选区", "", "abc", 3, 1, 0, false, 0},
		{"明文相符", "abc", "abc", 6, 0, 0, true, 1},
		{"明文不符", "xyz", "abc", 6, 0, 0, false, 1},
		{"空查询", "", "", 3, 0, 0, false, 0},
		{"读取光标失败", "", "abc", 3, 0, 5, false, 0},
		{"读取选区失败", "", "abc", 3, 0, 7, false, 0},
		{"读取文本失败", "", "abc", 3, 0, 10, false, 1},
		{"提供者返回不足", "••", "abc", 3, 0, 0, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			offset, selections, content, query, failedSlot, reads = tc.offset, tc.selections, tc.value, tc.query, tc.failedSlot, 0
			if got := ia2PasswordTextHasPrefix(text, query); got != tc.want {
				t.Fatalf("前缀判断 = %v，期望 %v", got, tc.want)
			}
			if reads != tc.wantReads {
				t.Fatalf("文本读取次数 = %d，期望 %d", reads, tc.wantReads)
			}
		})
	}
	if ia2PasswordTextHasPrefix(nil, "abc") {
		t.Fatal("缺少 IA2 提供者时不能判断前缀已上屏")
	}
}
