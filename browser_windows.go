//go:build windows

package main

import (
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows/registry"
)

const hotkeyRegistryPath = `Software\quick-paste`

const browserWidth = 380
const browserHeight = 480

var (
	procAttachThreadInputBrowser      = user32.NewProc("AttachThreadInput")
	procBringWindowToTopBrowser       = user32.NewProc("BringWindowToTop")
	procGetWindowThreadProcessBrowser = user32.NewProc("GetWindowThreadProcessId")
)

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
	kind   string
	value  string
	result chan error
}

type BrowserService struct {
	window                *application.WebviewWindow
	detailWindow          *application.WebviewWindow
	ensureWindows         func() (*application.WebviewWindow, *application.WebviewWindow)
	releaseWindows        func()
	openManager           func()
	detail                BrowserDetail
	detailID              string
	detailRow             rect
	detailVisible         bool
	detailTriggerActive   bool
	detailInitialised     bool
	detailLeave           time.Time
	store                 *PhraseStore
	clipboard             *ClipboardService
	monitor               *InputMonitor
	mu                    sync.Mutex
	visible               bool
	pinned                bool
	target                uintptr
	targetFocus           uintptr
	requestMu             sync.Mutex
	threadID              uintptr
	requests              chan hotkeyRequest
	current               string
	startupError          string
	clipboardCurrent      string
	clipboardStartupError string
	winVTakeover          atomic.Bool
	activeTab             string
	registryPath          string
	done                  chan struct{}
	anchor                rect
	dragging              bool
	dragSession           uint64
	initialised           bool
	showPending           bool
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
		values := map[string]string{"phrase": "Ctrl+Alt+Space", "clipboard": "Alt+V"}
		s.winVTakeover.Store(false)
		key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
		if err == nil {
			if saved, _, e := key.GetStringValue("BrowserHotkey"); e == nil {
				values["phrase"] = saved
			}
			if saved, _, e := key.GetStringValue("ClipboardHotkey"); e == nil {
				values["clipboard"] = saved
			}
			if saved, _, e := key.GetIntegerValue("WinVTakeover"); e == nil {
				s.winVTakeover.Store(saved != 0)
			}
			key.Close()
		}
		activeIDs := map[string]uintptr{"phrase": 1, "clipboard": 2}
		stagingIDs := map[string]uintptr{"phrase": 3, "clipboard": 4}
		apply := func(kind, value string, persist bool) error {
			spec, err := parseHotkey(value)
			if err != nil {
				return err
			}
			current, startupError := s.current, s.startupError
			registryName := "BrowserHotkey"
			if kind == "clipboard" {
				current, startupError, registryName = s.clipboardCurrent, s.clipboardStartupError, "ClipboardHotkey"
			}
			if persist && spec.name == current && startupError == "" {
				return nil
			}
			nextID := stagingIDs[kind]
			if spec.key != 0 {
				if ok, _, _ := register.Call(0, nextID, uintptr(spec.modifiers|0x4000), uintptr(spec.key)); ok == 0 {
					return errors.New("快捷键已被占用，请选择其他组合")
				}
			}
			if persist {
				key, _, e := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
				if e == nil {
					e = key.SetStringValue(registryName, spec.name)
					key.Close()
				}
				if e != nil {
					unregister.Call(0, nextID)
					return e
				}
			}
			unregister.Call(0, activeIDs[kind])
			stagingIDs[kind], activeIDs[kind] = activeIDs[kind], nextID
			if kind == "clipboard" {
				s.clipboardCurrent, s.clipboardStartupError = spec.name, ""
			} else {
				s.current, s.startupError = spec.name, ""
			}
			return nil
		}
		for _, kind := range []string{"phrase", "clipboard"} {
			if err := apply(kind, values[kind], false); err != nil {
				if kind == "clipboard" {
					s.clipboardCurrent, s.clipboardStartupError = values[kind], err.Error()
				} else {
					s.current, s.startupError = values[kind], err.Error()
				}
			}
		}
		close(ready)
		defer func() { unregister.Call(0, activeIDs["phrase"]); unregister.Call(0, activeIDs["clipboard"]) }()
		for {
			ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
			if int32(ret) <= 0 {
				return
			}
			switch message.Message {
			case 0x0312:
				kind := "phrase"
				if message.WParam == activeIDs["clipboard"] {
					kind = "clipboard"
				}
				// A registered Alt-only chord consumes the letter, leaving the
				// target with what looks like a bare Alt tap. Mask that menu
				// activation while Alt is still held, then show immediately.
				if uint32(message.LParam)&3 == 1 && keyPressed(vkMenu) && !keyPressed(vkCtrl) {
					maskBareModifierActivation()
				}
				go s.toggle(kind)
			case 0x8001:
				request := <-s.requests
				request.result <- apply(request.kind, request.value, true)
			}
		}
	}()
	<-ready
	go s.watchForeground(s.done)
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
func (s *BrowserService) GetActiveTab() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeTab == "clipboard" {
		return "clipboard"
	}
	return "phrase"
}
func (s *BrowserService) GetPinned() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pinned
}
func (s *BrowserService) SetPinned(pinned bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pinned = pinned
	if pinned {
		s.captureCurrentTargetLocked()
	}
}
func (s *BrowserService) GetHotkeyError() string {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	return s.startupError
}
func (s *BrowserService) SetHotkey(value string) error {
	return s.setHotkey("phrase", value)
}
func (s *BrowserService) GetClipboardHotkey() string {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	return s.clipboardCurrent
}
func (s *BrowserService) GetClipboardHotkeyError() string {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	return s.clipboardStartupError
}
func (s *BrowserService) SetClipboardHotkey(value string) error {
	return s.setHotkey("clipboard", value)
}

func (s *BrowserService) GetWinVTakeover() bool { return s.winVTakeover.Load() }

func (s *BrowserService) SetWinVTakeover(enabled bool) error {
	path := s.registryPath
	if path == "" {
		path = hotkeyRegistryPath
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	var value uint32
	if enabled {
		value = 1
	}
	if err := key.SetDWordValue("WinVTakeover", value); err != nil {
		return err
	}
	s.winVTakeover.Store(enabled)
	return nil
}

func (s *BrowserService) setHotkey(kind, value string) error {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	if s.threadID == 0 {
		return errors.New("快捷键服务尚未启动")
	}
	request := hotkeyRequest{kind, value, make(chan error, 1)}
	s.requests <- request
	if ok, _, err := procPostThreadMessageW.Call(s.threadID, 0x8001, 0, 0); ok == 0 {
		<-s.requests
		return err
	}
	return <-request.result
}

func maskBareModifierActivation() {
	inputs := []keyboardInput{
		{Type: inputKeyboard, Ki: keybdInput{Vk: vkCtrl, ExtraInfo: quickPasteInputMarker}},
		{Type: inputKeyboard, Ki: keybdInput{Vk: vkCtrl, Flags: keyeventfKeyup, ExtraInfo: quickPasteInputMarker}},
	}
	procSendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(inputs[0]))
}

func (s *BrowserService) toggle(tab string) {
	target, _, _ := procGetForegroundWindow.Call()
	if target == 0 {
		return
	}
	targetFocus := uintptr(0)
	targetInfo := guiThreadInfo{CbSize: uint32(unsafe.Sizeof(guiThreadInfo{}))}
	if ok, _, _ := procGetGUIThreadInfo.Call(0, uintptr(unsafe.Pointer(&targetInfo))); ok != 0 {
		targetFocus = uintptr(targetInfo.HwndFocus)
	}
	if s.ensureWindows != nil {
		window, detailWindow := s.ensureWindows()
		if window == nil || detailWindow == nil {
			return
		}
		browserHWND := uintptr(window.NativeWindow())
		detailHWND := uintptr(detailWindow.NativeWindow())
		current, _, _ := procGetForegroundWindow.Call()
		if current != target {
			if current != browserHWND && current != detailHWND {
				return
			}
			if err := restoreWindowFocus(target, targetFocus); err != nil {
				return
			}
		}
		s.mu.Lock()
		if s.window != window {
			s.initialised = false
			s.visible = false
			s.showPending = false
		}
		if s.detailWindow != detailWindow {
			s.detailInitialised = false
			s.detailVisible = false
			s.detailID = ""
			s.detailTriggerActive = false
			s.detailLeave = time.Time{}
		}
		s.window = window
		s.detailWindow = detailWindow
		s.mu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.showPending {
		s.showPending = false
		s.monitor.browserOpen.Store(false)
		return
	}
	if s.visible {
		if s.activeTab != tab {
			s.activeTab = tab
			s.window.EmitEvent("browser-tab", tab)
			return
		}
		s.hide()
		return
	}
	s.monitor.browserOpen.Store(true)
	if target == 0 || target == uintptr(s.window.NativeWindow()) || (s.detailWindow != nil && target == uintptr(s.detailWindow.NativeWindow())) {
		s.monitor.browserOpen.Store(false)
		return
	}
	s.target = target
	s.targetFocus = targetFocus
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
	s.activeTab = tab
	s.showPending = true
	s.window.EmitEvent("browser-refresh", tab)
}

// ReadyToShow is called after React has committed the loading state. Keeping
// the native window hidden until then prevents a stale frame or blank WebView
// surface from flashing on the first opening.
func (s *BrowserService) ReadyToShow() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.showPending || s.visible {
		return
	}
	current, _, _ := procGetForegroundWindow.Call()
	if current != s.target {
		s.showPending = false
		s.monitor.browserOpen.Store(false)
		return
	}
	application.InvokeSync(func() {
		hwnd := uintptr(s.window.NativeWindow())
		index := int32(gwlExStyle)
		indexArg := uintptr(uint32(index))
		style, _, _ := procGetWindowLongW.Call(hwnd, indexArg)
		// Keep the browser HWND alive between openings. Hiding it with SW_HIDE
		// lets WebView2 reuse the previous compositor surface on the next show,
		// which exposes stale hover and tab-selection pixels. A fully transparent
		// layered window keeps WebView2 painting while remaining invisible.
		visibleStyle := (style | wsExLayered) &^ wsExTransparent
		procSetWindowLongW.Call(hwnd, indexArg, visibleStyle|wsExNoActivate)
		s.positionLocked(hwnd)
		if !s.initialised {
			s.window.Show()
			s.initialised = true
		}
		procShowWindow.Call(hwnd, 4)
		procSetLayeredWindowAttributes.Call(hwnd, 0, 255, lwaAlpha)
		current, _, _ := procGetForegroundWindow.Call()
		if current == hwnd && s.target != 0 {
			procSetForegroundWindow.Call(s.target)
		}
		procSetWindowLongW.Call(hwnd, indexArg, visibleStyle&^wsExNoActivate)
	})
	s.showPending = false
	s.visible = true
}

func (s *BrowserService) routeKey(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.visible && s.window != nil {
		s.window.EmitEvent("browser-key", key)
	}
}

func (s *BrowserService) hide() {
	s.hideDetailLocked()
	s.showPending = false
	s.dragging = false
	s.dragSession++
	s.monitor.browserOpen.Store(false)
	if s.window != nil {
		application.InvokeSync(func() {
			hwnd := uintptr(s.window.NativeWindow())
			index := int32(gwlExStyle)
			indexArg := uintptr(uint32(index))
			style, _, _ := procGetWindowLongW.Call(hwnd, indexArg)
			procSetWindowLongW.Call(hwnd, indexArg, style|wsExLayered|wsExTransparent|wsExNoActivate)
			procSetLayeredWindowAttributes.Call(hwnd, 0, 0, lwaAlpha)
		})
	}
	s.visible = false
	if s.window != nil {
		s.window.EmitEvent("browser-hidden")
	}
	if s.releaseWindows != nil {
		s.releaseWindows()
	}
}
func (s *BrowserService) Hide() { s.mu.Lock(); defer s.mu.Unlock(); s.hide() }

func (s *BrowserService) OpenManager() {
	s.mu.Lock()
	s.hide()
	s.mu.Unlock()
	if s.openManager != nil {
		s.openManager()
	}
}

func (s *BrowserService) captureCurrentTargetLocked() bool {
	foreground, _, _ := procGetForegroundWindow.Call()
	if foreground == 0 || foreground == uintptr(s.window.NativeWindow()) || (s.detailWindow != nil && foreground == uintptr(s.detailWindow.NativeWindow())) {
		return false
	}
	s.target = foreground
	s.targetFocus = 0
	info := guiThreadInfo{CbSize: uint32(unsafe.Sizeof(guiThreadInfo{}))}
	if ok, _, _ := procGetGUIThreadInfo.Call(0, uintptr(unsafe.Pointer(&info))); ok != 0 {
		s.targetFocus = uintptr(info.HwndFocus)
	}
	return true
}

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
				if s.pinned {
					if foreground != uintptr(s.window.NativeWindow()) && (s.detailWindow == nil || foreground != uintptr(s.detailWindow.NativeWindow())) {
						s.captureCurrentTargetLocked()
					}
				} else if foreground != s.target && foreground != uintptr(s.window.NativeWindow()) && (s.detailWindow == nil || foreground != uintptr(s.detailWindow.NativeWindow())) {
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
	if s.visible && !s.pinned && root != uintptr(s.window.NativeWindow()) && !(s.detailVisible && root == uintptr(s.detailWindow.NativeWindow())) {
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
	width, height := scaleForDPI(browserWidth, dpi), scaleForDPI(browserHeight, dpi)
	var current rect
	if ok, _, _ := getWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&current))); ok != 0 && current.Right > current.Left && current.Bottom > current.Top {
		width, height = int(current.Right-current.Left), int(current.Bottom-current.Top)
	}
	bounds := placeFloatingPanel(anchor, info.Monitor, width, height, gap)
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
	browserHWND := uintptr(s.window.NativeWindow())
	if s.pinned && foreground != browserHWND && (s.detailWindow == nil || foreground != uintptr(s.detailWindow.NativeWindow())) {
		s.captureCurrentTargetLocked()
		foreground, _, _ = procGetForegroundWindow.Call()
	}
	if s.target == 0 || (!s.pinned && foreground != s.target && foreground != browserHWND) {
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
			if err := s.restoreTargetLocked(); err != nil {
				return err
			}
			current, _, _ := procGetForegroundWindow.Call()
			if current != s.target {
				return errors.New("输入窗口已切换，已取消输入")
			}
			method := phraseInsertMethodRuntime.Load()
			if !s.pinned {
				s.hide()
			}
			if method == runtimeInputSend {
				if err := sendReplacementWithInterval(0, content, time.Duration(phraseInsertIntervalMillis.Load())*time.Millisecond); err != nil {
					return fmt.Errorf("输入失败: %w", err)
				}
				return nil
			}
			if s.clipboard == nil {
				return errors.New("剪贴板服务未初始化")
			}
			if err := s.clipboard.write(map[uint32][]byte{cfUnicodeText: encodeUTF16Clipboard(content)}); err != nil {
				return fmt.Errorf("写入剪贴板失败: %w", err)
			}
			current, _, _ = procGetForegroundWindow.Call()
			if current != s.target {
				return errors.New("输入窗口已切换，已取消粘贴")
			}
			if err := sendPasteShortcut(method); err != nil {
				return fmt.Errorf("输入失败: %w", err)
			}
			return nil
		}
	}
	return errors.New("短语已被删除，请重新打开浏览窗口")
}

func (s *BrowserService) pasteClipboard(write func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.visible {
		return errors.New("请先用快捷键打开剪贴板")
	}
	foreground, _, _ := procGetForegroundWindow.Call()
	browserHWND := uintptr(s.window.NativeWindow())
	if s.pinned && foreground != browserHWND && (s.detailWindow == nil || foreground != uintptr(s.detailWindow.NativeWindow())) {
		s.captureCurrentTargetLocked()
		foreground, _, _ = procGetForegroundWindow.Call()
	}
	if s.target == 0 || (!s.pinned && foreground != s.target && foreground != browserHWND) {
		return errors.New("输入窗口已切换，请在目标窗口重新打开剪贴板")
	}
	if keyPressed(vkCtrl) || keyPressed(vkMenu) || keyPressed(vkShift) || keyPressed(vkLWin) || keyPressed(vkRWin) {
		return errors.New("请松开修饰键后点击剪贴板项目")
	}
	if err := s.restoreTargetLocked(); err != nil {
		return err
	}
	if err := write(); err != nil {
		return err
	}
	current, _, _ := procGetForegroundWindow.Call()
	if current != s.target {
		return errors.New("输入窗口已切换，已取消粘贴")
	}
	if !s.pinned {
		s.hide()
	}
	return sendPasteShortcut(clipboardPasteMethodRuntime.Load())
}

func (s *BrowserService) restoreTargetLocked() error {
	return restoreWindowFocus(s.target, s.targetFocus)
}

func restoreWindowFocus(target, targetFocus uintptr) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	currentThread, _, _ := procGetCurrentThreadID.Call()
	for attempt := 0; attempt < 6; attempt++ {
		foreground, _, _ := procGetForegroundWindow.Call()
		foregroundThread, _, _ := procGetWindowThreadProcessBrowser.Call(foreground, 0)
		targetThread, _, _ := procGetWindowThreadProcessBrowser.Call(target, 0)
		attachedForeground := foregroundThread != 0 && foregroundThread != currentThread
		attachedTarget := targetThread != 0 && targetThread != currentThread && targetThread != foregroundThread
		if attachedForeground {
			procAttachThreadInputBrowser.Call(currentThread, foregroundThread, 1)
		}
		if attachedTarget {
			procAttachThreadInputBrowser.Call(currentThread, targetThread, 1)
		}
		procBringWindowToTopBrowser.Call(target)
		procSetForegroundWindow.Call(target)
		if targetFocus != 0 {
			if ok, _, _ := user32.NewProc("IsChild").Call(target, targetFocus); ok != 0 || targetFocus == target {
				user32.NewProc("SetFocus").Call(targetFocus)
			}
		}
		if attachedTarget {
			procAttachThreadInputBrowser.Call(currentThread, targetThread, 0)
		}
		if attachedForeground {
			procAttachThreadInputBrowser.Call(currentThread, foregroundThread, 0)
		}
		time.Sleep(time.Duration(20+attempt*10) * time.Millisecond)
		current, _, _ := procGetForegroundWindow.Call()
		if current == target {
			return nil
		}
	}
	return errors.New("无法恢复原输入窗口，请重新打开悬浮框")
}

func sendPasteShortcut(method int32) error {
	if method == runtimeInputShiftInsert {
		return sendKeyboardInputs([]keyboardInput{
			{Type: inputKeyboard, Ki: keybdInput{Vk: vkShift, ExtraInfo: quickPasteInputMarker}},
			{Type: inputKeyboard, Ki: keybdInput{Vk: vkInsert, ExtraInfo: quickPasteInputMarker}},
			{Type: inputKeyboard, Ki: keybdInput{Vk: vkInsert, Flags: keyeventfKeyup, ExtraInfo: quickPasteInputMarker}},
			{Type: inputKeyboard, Ki: keybdInput{Vk: vkShift, Flags: keyeventfKeyup, ExtraInfo: quickPasteInputMarker}},
		})
	}
	return sendKeyboardInputs([]keyboardInput{
		{Type: inputKeyboard, Ki: keybdInput{Vk: vkCtrl, ExtraInfo: quickPasteInputMarker}},
		{Type: inputKeyboard, Ki: keybdInput{Vk: vkV, ExtraInfo: quickPasteInputMarker}},
		{Type: inputKeyboard, Ki: keybdInput{Vk: vkV, Flags: keyeventfKeyup, ExtraInfo: quickPasteInputMarker}},
		{Type: inputKeyboard, Ki: keybdInput{Vk: vkCtrl, Flags: keyeventfKeyup, ExtraInfo: quickPasteInputMarker}},
	})
}
