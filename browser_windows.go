//go:build windows

package main

import (
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows/registry"
)

const hotkeyRegistryPath = `Software\quick-phrase`

const browserWidth = 320
const browserHeight = 360

type hotkeySpec struct {
	name           string
	modifiers, key uint32
}

func parseHotkey(value string) (hotkeySpec, error) {
	var spec hotkeySpec
	if value == "" {
		return spec, nil
	}
	parts := strings.Split(value, "+")
	for _, part := range parts[:len(parts)-1] {
		var flag uint32
		switch part {
		case "Ctrl":
			flag = 2
		case "Alt":
			flag = 1
		case "Shift":
			flag = 4
		case "Win":
			flag = 8
		default:
			return spec, errors.New("无效的快捷键修饰键")
		}
		if spec.modifiers&flag != 0 {
			return spec, errors.New("快捷键修饰键重复")
		}
		spec.modifiers |= flag
	}
	key := parts[len(parts)-1]
	if len(key) == 1 && ((key[0] >= 'A' && key[0] <= 'Z') || (key[0] >= '0' && key[0] <= '9')) {
		spec.key = uint32(key[0])
	} else if key == "Space" {
		spec.key = vkSpace
	} else if strings.HasPrefix(key, "F") {
		n, err := strconv.Atoi(key[1:])
		if err == nil && n >= 1 && n <= 24 {
			spec.key = uint32(0x6F + n)
		}
	}
	if spec.key == 0 || spec.modifiers == 0 {
		return spec, errors.New("请使用 Ctrl、Alt、Shift 或 Win 加字母、数字、空格或 F1–F24")
	}
	var names []string
	for _, item := range []struct {
		name string
		flag uint32
	}{{"Ctrl", 2}, {"Alt", 1}, {"Shift", 4}, {"Win", 8}} {
		if spec.modifiers&item.flag != 0 {
			names = append(names, item.name)
		}
	}
	spec.name = strings.Join(append(names, key), "+")
	return spec, nil
}

type hotkeyRequest struct {
	value  string
	result chan error
}

type BrowserService struct {
	window              *application.WebviewWindow
	detailWindow        *application.WebviewWindow
	detail              BrowserDetail
	detailID            string
	detailRow           rect
	detailVisible       bool
	detailTriggerActive bool
	detailInitialised   bool
	detailLeave         time.Time
	store               *PhraseStore
	monitor             *InputMonitor
	mu                  sync.Mutex
	visible             bool
	target              uintptr
	requestMu           sync.Mutex
	threadID            uintptr
	requests            chan hotkeyRequest
	current             string
	startupError        string
	registryPath        string
	done                chan struct{}
	anchor              rect
	dragging            bool
	dragSession         uint64
	initialised         bool
}

func (s *BrowserService) start() {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	s.requests = make(chan hotkeyRequest, 1)
	s.done = make(chan struct{})
	path := s.registryPath
	if path == "" {
		path = hotkeyRegistryPath
	}
	ready := make(chan struct{})
	go func() {
		defer close(s.done)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		register := user32.NewProc("RegisterHotKey")
		unregister := user32.NewProc("UnregisterHotKey")
		var message msg
		user32.NewProc("PeekMessageW").Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0, 0)
		s.threadID, _, _ = procGetCurrentThreadID.Call()
		value := "Ctrl+Alt+Space"
		key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
		if err == nil {
			if saved, _, e := key.GetStringValue("BrowserHotkey"); e == nil {
				value = saved
			}
			key.Close()
		}
		activeID := uintptr(1)
		apply := func(value string, persist bool) error {
			spec, err := parseHotkey(value)
			if err != nil {
				return err
			}
			if persist && spec.name == s.current && s.startupError == "" {
				return nil
			}
			nextID := uintptr(3) - activeID
			if spec.key != 0 {
				if ok, _, _ := register.Call(0, nextID, uintptr(spec.modifiers|0x4000), uintptr(spec.key)); ok == 0 {
					return errors.New("快捷键已被占用，请选择其他组合")
				}
			}
			if persist {
				key, _, e := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
				if e == nil {
					e = key.SetStringValue("BrowserHotkey", spec.name)
					key.Close()
				}
				if e != nil {
					unregister.Call(0, nextID)
					return e
				}
			}
			unregister.Call(0, activeID)
			activeID = nextID
			s.current = spec.name
			s.startupError = ""
			return nil
		}
		if err := apply(value, false); err != nil {
			s.current = value
			s.startupError = err.Error()
		}
		close(ready)
		defer func() { unregister.Call(0, activeID) }()
		for {
			ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
			if int32(ret) <= 0 {
				return
			}
			switch message.Message {
			case 0x0312:
				go s.toggle()
			case 0x8001:
				request := <-s.requests
				request.result <- apply(request.value, true)
			}
		}
	}()
	<-ready
	if s.window != nil {
		go s.watchForeground(s.done)
	}
}

func (s *BrowserService) stop() {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	if s.threadID != 0 {
		procPostThreadMessageW.Call(s.threadID, 0x0012, 0, 0)
		s.threadID = 0
		<-s.done
	}
}
func (s *BrowserService) GetHotkey() string {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	return s.current
}
func (s *BrowserService) GetHotkeyError() string {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	return s.startupError
}
func (s *BrowserService) SetHotkey(value string) error {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	if s.threadID == 0 {
		return errors.New("快捷键服务尚未启动")
	}
	request := hotkeyRequest{value, make(chan error, 1)}
	s.requests <- request
	if ok, _, err := procPostThreadMessageW.Call(s.threadID, 0x8001, 0, 0); ok == 0 {
		<-s.requests
		return err
	}
	return <-request.result
}

func (s *BrowserService) toggle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.visible {
		s.hide()
		return
	}
	s.monitor.browserOpen.Store(true)
	s.target, _, _ = procGetForegroundWindow.Call()
	if s.monitor != nil && s.monitor.resetFn != nil {
		s.monitor.resetFn()
	}
	// Manual browsing follows the pointer even when another control exposes a
	// text caret. Read it on every opening, including after dragging the panel.
	var cursor point
	if ok, _, _ := user32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&cursor))); ok != 0 {
		s.anchor = rect{cursor.X, cursor.Y, cursor.X + 1, cursor.Y + 1}
	} else {
		s.anchor = caretScreenRect()
	}
	current, _, _ := procGetForegroundWindow.Call()
	if current != s.target {
		s.monitor.browserOpen.Store(false)
		return
	}
	s.window.EmitEvent("browser-refresh")
	application.InvokeSync(func() {
		hwnd := uintptr(s.window.NativeWindow())
		ensurePopupNoActivate(hwnd)
		s.positionLocked(hwnd)
		if !s.initialised {
			s.window.Show()
			s.initialised = true
		}
		current, _, _ := procGetForegroundWindow.Call()
		if current == hwnd && s.target != 0 {
			procSetForegroundWindow.Call(s.target)
		}
		procShowWindow.Call(hwnd, 4)
	})
	s.visible = true
}

func (s *BrowserService) hide() {
	s.hideDetailLocked()
	s.dragging = false
	s.dragSession++
	s.monitor.browserOpen.Store(false)
	application.InvokeSync(func() { procShowWindow.Call(uintptr(s.window.NativeWindow()), 0) })
	s.visible = false
	s.window.EmitEvent("browser-hidden")
}
func (s *BrowserService) Hide() { s.mu.Lock(); defer s.mu.Unlock(); s.hide() }

// NOACTIVATE windows never receive normal blur notifications. Track the actual
// input target and outside mouse presses without taking its keyboard focus.
func (s *BrowserService) watchForeground(done <-chan struct{}) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			s.mu.Lock()
			if s.visible {
				foreground, _, _ := procGetForegroundWindow.Call()
				if foreground != s.target && foreground != uintptr(s.window.NativeWindow()) && (s.detailWindow == nil || foreground != uintptr(s.detailWindow.NativeWindow())) {
					s.hide()
				}
			}
			s.checkDetailHoverLocked()
			s.mu.Unlock()
		}
	}
}

func (s *BrowserService) mouseDown(root uintptr) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.visible && root != uintptr(s.window.NativeWindow()) && !(s.detailVisible && root == uintptr(s.detailWindow.NativeWindow())) {
		s.hide()
	}
}

func (s *BrowserService) positionLocked(hwnd uintptr) {
	anchor := s.anchor
	monitor, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&anchor)), 2)
	info := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	if ok, _, _ := procGetMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info))); ok == 0 {
		return
	}
	dpi := uintptr(96)
	var x, y uint32
	if hr, _, _ := procGetDpiForMonitor.Call(monitor, 0, uintptr(unsafe.Pointer(&x)), uintptr(unsafe.Pointer(&y))); int32(hr) >= 0 && x > 0 {
		dpi = uintptr(x)
	}
	gap := scaleForDPI(4, dpi)
	bounds := placePopup(anchor, info.Work, scaleForDPI(browserWidth, dpi), scaleForDPI(browserHeight, dpi), gap)
	procSetWindowPos.Call(hwnd, hwndTopmost, uintptr(int64(bounds.Left)), uintptr(int64(bounds.Top)), uintptr(bounds.Right-bounds.Left), uintptr(bounds.Bottom-bounds.Top), swpNoActivate)
}

// Native cursor deltas work outside the WebView and do not start a Windows
// caption move loop, which cannot be relied on for a non-activating popup.
func (s *BrowserService) BeginDrag() {
	s.mu.Lock()
	if !s.visible || s.dragging || !keyPressed(1) {
		s.mu.Unlock()
		return
	}
	s.hideDetailLocked()
	var start point
	var bounds rect
	cursor := user32.NewProc("GetCursorPos")
	cursor.Call(uintptr(unsafe.Pointer(&start)))
	hwnd := uintptr(s.window.NativeWindow())
	getWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&bounds)))
	s.dragging = true
	s.dragSession++
	session := s.dragSession
	s.mu.Unlock()
	go func() {
		previous := start
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-s.done:
				return
			case <-ticker.C:
			}
			s.mu.Lock()
			if s.dragSession != session {
				s.mu.Unlock()
				return
			}
			if !s.visible || !s.dragging || !keyPressed(1) {
				s.dragging = false
				s.mu.Unlock()
				return
			}
			var current point
			cursor.Call(uintptr(unsafe.Pointer(&current)))
			x, y := bounds.Left+current.X-start.X, bounds.Top+current.Y-start.Y
			if current != previous {
				previous = current
				application.InvokeSync(func() {
					procSetWindowPos.Call(hwnd, hwndTopmost, uintptr(int64(x)), uintptr(int64(y)), 0, 0, swpNoActivate|0x0001)
				})
			}
			s.mu.Unlock()
		}
	}()
}

func (s *BrowserService) Insert(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.visible {
		return errors.New("请先用快捷键打开短语浏览")
	}
	foreground, _, _ := procGetForegroundWindow.Call()
	if s.target == 0 || foreground != s.target {
		return errors.New("输入窗口已切换，请关闭悬浮框并在目标窗口重新按快捷键")
	}
	if keyPressed(vkCtrl) || keyPressed(vkMenu) || keyPressed(vkShift) || keyPressed(vkLWin) || keyPressed(vkRWin) {
		return errors.New("请松开修饰键后点击短语")
	}
	for _, node := range s.store.List() {
		if node.ID == id && node.Type == NodeTypePhrase {
			content := node.Content
			if content == "" {
				content = node.Name
			}
			if err := sendReplacement(0, content); err != nil {
				return fmt.Errorf("输入失败: %w", err)
			}
			s.hide()
			return nil
		}
	}
	return errors.New("短语已被删除，请重新打开浏览窗口")
}
