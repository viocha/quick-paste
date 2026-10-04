//go:build windows

package main

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procEnumClipboardFormats   = user32.NewProc("EnumClipboardFormats")
	procSetLastError           = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetLastError")
	procSetWindowLongPtrW      = user32.NewProc("SetWindowLongPtrW")
	procCallWindowProcW        = user32.NewProc("CallWindowProcW")
	procDispatchMessageW       = user32.NewProc("DispatchMessageW")
	procSetTimer               = user32.NewProc("SetTimer")
	procKillTimer              = user32.NewProc("KillTimer")
	procGetOpenClipboardWindow = user32.NewProc("GetOpenClipboardWindow")
	temporaryClipboards        sync.Map
	temporaryClipboardCallback = syscall.NewCallback(temporaryClipboardWindowProc)
)

const (
	wmRenderFormat        = 0x0305
	wmDestroyClipboard    = 0x0307
	wmTimer               = 0x0113
	wmTryRestoreClipboard = 0x8003
)

type temporaryClipboard struct {
	service            *ClipboardService
	hwnd, previousProc uintptr
	targetPID          uint32
	text               []preparedClipboardFormat
	backup             []preparedClipboardFormat
	rendered, finished bool
	restoring          bool
	active             bool
	deadline           time.Time
	err                error
}

func temporaryClipboardWindowProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	value, ok := temporaryClipboards.Load(hwnd)
	if !ok {
		return 0
	}
	t := value.(*temporaryClipboard)
	switch message {
	case wmDestroyClipboard:
		if t.active && !t.restoring {
			t.finished = true
			if !t.rendered {
				t.err = errors.New("系统剪贴板已被改写，已取消粘贴")
			}
		}
		return 0
	case wmRenderFormat:
		if wParam != cfUnicodeText || t.rendered {
			return 0
		}
		// 只接受可确认来自目标进程的读取请求，防止后台读取器提前触发恢复。
		reader, _, _ := procGetOpenClipboardWindow.Call()
		var pid uint32
		if reader != 0 {
			procGetWindowThreadProcessID.Call(reader, uintptr(unsafe.Pointer(&pid)))
		}
		if pid == 0 || t.targetPID == 0 || pid != t.targetPID {
			return 0
		}
		item := &t.text[0]
		if result, _, err := procSetClipboardData.Call(cfUnicodeText, item.handle); result == 0 {
			t.err = fmt.Errorf("提供临时短语失败: %w", err)
		} else {
			item.handle = 0
			t.rendered = true
		}
		// 回调返回后读取者才取得数据；恢复必须等待它关闭剪贴板。
		procSetTimer.Call(hwnd, 1, 15, 0)
		procPostMessageW.Call(hwnd, wmTryRestoreClipboard, 0, 0)
		return 0
	case wmTryRestoreClipboard, wmTimer:
		if t.finished {
			return 0
		}
		if !t.rendered && t.err == nil && time.Now().Before(t.deadline) {
			return 0
		}
		if !t.rendered && t.err == nil {
			t.err = errors.New("目标程序未读取短语，已取消粘贴")
			procSetTimer.Call(hwnd, 1, 15, 0)
		}
		t.restore()
		return 0
	}
	result, _, _ := procCallWindowProcW.Call(t.previousProc, hwnd, uintptr(message), wParam, lParam)
	return result
}

// backupOpenClipboard 完整保存可用的内存格式，而非只保存历史支持的格式。
// 无法安全复制的 GDI/私有句柄直接拒绝，不清空用户剪贴板。
func backupOpenClipboard() (map[uint32][]byte, error) {
	formats := make(map[uint32][]byte)
	var total int
	for format := uint32(0); ; {
		procSetLastError.Call(0)
		next, _, err := procEnumClipboardFormats.Call(uintptr(format))
		if next == 0 {
			if err != syscall.Errno(0) {
				return nil, fmt.Errorf("枚举剪贴板格式失败: %w", err)
			}
			break
		}
		format = uint32(next)
		if format == 2 && (clipboardFormatAvailable(cfDIB) || clipboardFormatAvailable(cfDIBV5)) {
			continue // CF_BITMAP 可由保存的 DIB 自动合成。
		}
		switch {
		case format == 2 || format == 3 || format == 9 || format == 14 || format == 0x80 || format == 0x82 || format == 0x83 || format == 0x8e || (format >= 0x200 && format <= 0x3ff):
			return nil, fmt.Errorf("剪贴板格式 %d 无法安全备份，请使用 SendInput 输入方式", format)
		}
		data, err := readClipboardFormat(format)
		if err != nil {
			return nil, fmt.Errorf("备份剪贴板格式 %d 失败: %w", format, err)
		}
		total += len(data)
		if total > 256*1024*1024 {
			return nil, errors.New("剪贴板备份超过 256 MB，请使用 SendInput 输入方式")
		}
		formats[format] = data
	}
	return formats, nil
}

func (s *ClipboardService) closeSuppressedClipboard() {
	s.mu.Lock()
	procCloseClipboard.Call()
	s.suppressSeq = clipboardSequence()
	s.lastSeq = s.suppressSeq
	s.mu.Unlock()
}

func (t *temporaryClipboard) restore() {
	if ok, _, _ := procOpenClipboard.Call(t.hwnd); ok == 0 {
		// Windows 没有 CloseClipboard 通知，仅在事务生命周期内重试锁。
		if time.Now().After(t.deadline.Add(3 * time.Second)) {
			t.err = errors.Join(t.err, errors.New("系统剪贴板持续被占用，恢复失败"))
			t.finished = true
		}
		return
	}
	owner, _, _ := procGetClipboardOwner.Call()
	if owner != t.hwnd {
		procCloseClipboard.Call()
		t.finished = true // 用户或其他程序的新复制优先，不能覆盖。
		if !t.rendered {
			t.err = errors.New("系统剪贴板已被改写，已取消粘贴")
		}
		return
	}
	t.restoring = true
	defer func() { t.restoring = false }()
	if ok, _, err := procEmptyClipboard.Call(); ok == 0 {
		procCloseClipboard.Call()
		t.err = errors.Join(t.err, fmt.Errorf("恢复剪贴板失败: %w", err))
		t.finished = true
		return
	}
	for i := range t.backup {
		item := &t.backup[i]
		if result, _, err := procSetClipboardData.Call(uintptr(item.format), item.handle); result == 0 {
			t.err = errors.Join(t.err, fmt.Errorf("恢复剪贴板格式 %d 失败: %w", item.format, err))
		} else {
			item.handle = 0
		}
	}
	t.service.closeSuppressedClipboard()
	t.finished = true
}

// pasteText 使用延迟渲染确认读取，再恢复备份；send 可注入替换/粘贴动作。
func (s *ClipboardService) pasteText(text string, target uintptr, send func() error) error {
	result := make(chan error, 1)
	go func() { result <- s.runTemporaryClipboard(text, target, send) }()
	return <-result
}

func (s *ClipboardService) runTemporaryClipboard(text string, target uintptr, send func() error) error {
	s.clipboardMu.Lock()
	defer s.clipboardMu.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	class, _ := windows.UTF16PtrFromString("STATIC")
	hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(class)), 0, 0, 0, 0, 0, 0, hwndMessage, 0, 0, 0)
	if hwnd == 0 {
		return fmt.Errorf("创建临时剪贴板窗口失败: %w", err)
	}
	defer procDestroyWindow.Call(hwnd)
	t := &temporaryClipboard{service: s, hwnd: hwnd, deadline: time.Now().Add(2 * time.Second)}
	procGetWindowThreadProcessID.Call(target, uintptr(unsafe.Pointer(&t.targetPID)))
	temporaryClipboards.Store(hwnd, t)
	defer temporaryClipboards.Delete(hwnd)
	index := int32(-4) // GWLP_WNDPROC
	t.previousProc, _, err = procSetWindowLongPtrW.Call(hwnd, uintptr(index), temporaryClipboardCallback)
	if t.previousProc == 0 {
		return fmt.Errorf("设置临时剪贴板回调失败: %w", err)
	}
	defer procSetWindowLongPtrW.Call(hwnd, uintptr(index), t.previousProc)
	if timer, _, err := procSetTimer.Call(hwnd, 1, 2000, 0); timer == 0 {
		return fmt.Errorf("创建剪贴板恢复定时器失败: %w", err)
	}
	defer procKillTimer.Call(hwnd, 1)
	t.text, err = s.prepareClipboardFormats(map[uint32][]byte{cfUnicodeText: encodeUTF16Clipboard(text)})
	if err != nil {
		return err
	}
	defer freePreparedClipboardFormats(t.text)
	exclude := registerClipboardFormat("ExcludeClipboardContentFromMonitorProcessing")
	marker, err := s.prepareClipboardFormats(map[uint32][]byte{exclude: {0, 0, 0, 0}})
	if err != nil {
		return err
	}
	defer freePreparedClipboardFormats(marker)
	opened := false
	for i := 0; i < 5; i++ {
		if ok, _, openErr := procOpenClipboard.Call(hwnd); ok != 0 {
			opened = true
			break
		} else {
			err = openErr
		}
		time.Sleep(time.Duration(i+1) * 15 * time.Millisecond)
	}
	if !opened {
		return fmt.Errorf("打开剪贴板失败: %w", err)
	}
	backup, err := backupOpenClipboard()
	if err == nil && len(backup) > 0 {
		t.backup, err = s.prepareClipboardFormats(backup)
	}
	if err != nil {
		procCloseClipboard.Call()
		return err
	}
	defer freePreparedClipboardFormats(t.backup)
	s.mu.Lock()
	s.temporaryWindow = hwnd
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.temporaryWindow = 0; s.mu.Unlock() }()
	if ok, _, err := procEmptyClipboard.Call(); ok == 0 {
		procCloseClipboard.Call()
		return err
	}
	if value, _, err := procSetClipboardData.Call(uintptr(exclude), marker[0].handle); value == 0 {
		t.err = fmt.Errorf("排除系统剪贴板历史失败: %w", err)
	} else {
		marker[0].handle = 0
	}
	// NULL 句柄启用延迟渲染，返回 NULL 不代表失败。
	procSetClipboardData.Call(cfUnicodeText, 0)
	if !clipboardFormatAvailable(cfUnicodeText) {
		t.err = errors.Join(t.err, errors.New("创建临时文本格式失败"))
	}
	s.closeSuppressedClipboard()
	t.active = true
	t.deadline = time.Now().Add(2 * time.Second)
	procSetTimer.Call(hwnd, 1, 2000, 0)
	if t.err == nil {
		sendErr := send()
		t.err = errors.Join(t.err, sendErr)
	}
	if t.err != nil && !t.finished {
		procSetTimer.Call(hwnd, 1, 15, 0)
		t.restore()
	}
	var message msg
	for !t.finished {
		value, _, err := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), hwnd, 0, 0)
		if int32(value) <= 0 {
			t.err = errors.Join(t.err, fmt.Errorf("等待剪贴板读取失败: %v", err))
			t.restore()
			break
		}
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
	}
	return t.err
}
