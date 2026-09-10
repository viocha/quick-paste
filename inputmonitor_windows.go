//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
	"sync"
	"syscall"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	whKeyboardLL = 13
	whMouseLL    = 14

	wmKeyDown    = 0x0100
	wmKeyUp      = 0x0101
	wmSysKeyDown = 0x0104
	wmSysKeyUp   = 0x0105

	wmLButtonDown = 0x0201
	wmRButtonDown = 0x0204
	wmMButtonDown = 0x0207
	wmXButtonDown = 0x020B

	vkBack    = 0x08
	vkTab     = 0x09
	vkReturn  = 0x0D
	vkShift   = 0x10
	vkCtrl    = 0x11
	vkMenu    = 0x12
	vkEscape  = 0x1B
	vkSpace   = 0x20
	vkPrior   = 0x21
	vkNext    = 0x22
	vkEnd     = 0x23
	vkHome    = 0x24
	vkLeft    = 0x25
	vkUp      = 0x26
	vkRight   = 0x27
	vkDown    = 0x28
	vkDelete  = 0x2E
	vkLWin    = 0x5B
	vkRWin    = 0x5C
	vkCapital = 0x14

	quickPhraseInputMarker = 0x51504852

	keyeventfKeyup   = 0x0002
	keyeventfUnicode = 0x0004

	inputKeyboard = 1

	hwndTopmost = ^uintptr(0)

	swpNoActivate = 0x0010

	gwlExStyle     = -20
	wsExNoActivate = 0x08000000
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procSetWindowsHookExW   = user32.NewProc("SetWindowsHookExW")
	procCallNextHookEx      = user32.NewProc("CallNextHookEx")
	procUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procPostThreadMessageW  = user32.NewProc("PostThreadMessageW")
	procGetCurrentThreadID  = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCurrentThreadId")
	procGetAsyncKeyState    = user32.NewProc("GetAsyncKeyState")
	procGetKeyState         = user32.NewProc("GetKeyState")
	procGetGUIThreadInfo    = user32.NewProc("GetGUIThreadInfo")
	procClientToScreen      = user32.NewProc("ClientToScreen")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procGetDpiForWindow     = user32.NewProc("GetDpiForWindow")
	procGetWindowLongW      = user32.NewProc("GetWindowLongW")
	procSetWindowLongW      = user32.NewProc("SetWindowLongW")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procSetWindowPos        = user32.NewProc("SetWindowPos")
	procSendInput           = user32.NewProc("SendInput")
)

type kbdLLHookStruct struct {
	VkCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

type point struct {
	X int32
	Y int32
}

type rect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type guiThreadInfo struct {
	CbSize        uint32
	Flags         uint32
	HwndActive    windows.Handle
	HwndFocus     windows.Handle
	HwndCapture   windows.Handle
	HwndMenuOwner windows.Handle
	HwndMoveSize  windows.Handle
	HwndCaret     windows.Handle
	RcCaret       rect
}

type msg struct {
	Hwnd    windows.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

type keyboardInput struct {
	Type    uint32
	_       uint32
	Ki      keybdInput
	Padding [8]byte
}

type keybdInput struct {
	Vk        uint16
	Scan      uint16
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type windowsInputState struct {
	monitor *InputMonitor

	mu               sync.Mutex
	query            string
	suggestions      []Suggestion
	active           int
	threadID         uint32
	hook             windows.Handle
	mouseHook        windows.Handle
	closed           bool
	lastForeground   uintptr
	swallowKeyUp     map[uint32]bool
	actions          chan struct{}
	actionMu         sync.Mutex
	pending          []func()
	done             chan struct{}
	popupInitialised bool
	showView         func(SuggestionPayload)
	hideView         func()
	replaceText      func(int, string) error
}

func startPlatformInputMonitor(m *InputMonitor) (func(), error) {
	state := &windowsInputState{
		monitor:      m,
		swallowKeyUp: make(map[uint32]bool),
		actions:      make(chan struct{}, 1),
		done:         make(chan struct{}),
	}
	ready := make(chan error, 1)

	m.resetFn = state.reset
	state.showView = state.showPopup
	state.hideView = state.hidePopup
	state.replaceText = sendReplacement
	go state.runActions()
	cancelReady := m.app.Event.On("popup-ready", func(*application.CustomEvent) { state.updatePopup() })

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		threadID, _, _ := procGetCurrentThreadID.Call()
		state.mu.Lock()
		state.threadID = uint32(threadID)
		state.mu.Unlock()

		keyboardCallback := syscall.NewCallback(func(code int, wParam uintptr, lParam uintptr) uintptr {
			if code < 0 {
				return callNextHook(0, code, wParam, lParam)
			}
			data := (*kbdLLHookStruct)(unsafe.Pointer(lParam))
			if data.DwExtraInfo == quickPhraseInputMarker {
				return callNextHook(0, code, wParam, lParam)
			}

			swallow := state.handleKey(uint32(wParam), data.VkCode)
			if swallow {
				return 1
			}
			return callNextHook(0, code, wParam, lParam)
		})

		hook, _, err := procSetWindowsHookExW.Call(whKeyboardLL, keyboardCallback, 0, 0)
		if hook == 0 {
			ready <- err
			return
		}

		mouseCallback := syscall.NewCallback(func(code int, wParam uintptr, lParam uintptr) uintptr {
			if code >= 0 && isMouseButtonDown(uint32(wParam)) {
				if m.browserOpen.Load() && m.browserMouseDown != nil {
					p := *(*point)(unsafe.Pointer(lParam))
					hit, _, _ := user32.NewProc("WindowFromPoint").Call(uintptr(uint64(uint32(p.X)) | uint64(uint32(p.Y))<<32))
					root, _, _ := user32.NewProc("GetAncestor").Call(hit, 2)
					state.enqueue(func() { m.browserMouseDown(root) })
				}
				state.handleMouseClick()
			}
			return callNextHook(0, code, wParam, lParam)
		})
		mouseHook, _, mouseErr := procSetWindowsHookExW.Call(whMouseLL, mouseCallback, 0, 0)
		if mouseHook == 0 {
			procUnhookWindowsHookEx.Call(hook)
			ready <- mouseErr
			return
		}

		state.mu.Lock()
		state.hook = windows.Handle(hook)
		state.mouseHook = windows.Handle(mouseHook)
		state.mu.Unlock()
		ready <- nil

		var message msg
		for {
			ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
			if int32(ret) <= 0 {
				break
			}
		}
		procUnhookWindowsHookEx.Call(mouseHook)
		procUnhookWindowsHookEx.Call(hook)
	}()

	if err := <-ready; err != nil {
		cancelReady()
		close(state.done)
		return nil, err
	}

	if ptr := m.popup.NativeWindow(); ptr != nil {
		ensurePopupNoActivate(uintptr(ptr))
	}

	return func() {
		state.mu.Lock()
		if state.closed {
			state.mu.Unlock()
			return
		}
		state.closed = true
		cancelReady()
		close(state.done)
		threadID := state.threadID
		state.mu.Unlock()
		if threadID != 0 {
			const wmQuit = 0x0012
			procPostThreadMessageW.Call(uintptr(threadID), wmQuit, 0, 0)
		}
	}, nil
}

// Keep UI and SendInput work off the low-level hook thread, in strict FIFO order.
func (s *windowsInputState) enqueue(action func()) {
	s.actionMu.Lock()
	s.pending = append(s.pending, action)
	s.actionMu.Unlock()
	select {
	case s.actions <- struct{}{}:
	default:
	}
}

func (s *windowsInputState) runActions() {
	for {
		select {
		case <-s.done:
			return
		case <-s.actions:
			s.actionMu.Lock()
			pending := s.pending
			s.pending = nil
			s.actionMu.Unlock()
			for _, action := range pending {
				select {
				case <-s.done:
					return
				default:
					action()
				}
			}
		}
	}
}

// Read the current state when the queued update runs; never redisplay stale candidates.
func (s *windowsInputState) updatePopup() {
	s.enqueue(func() {
		s.mu.Lock()
		payload := SuggestionPayload{Query: s.query, Items: append([]Suggestion{}, s.suggestions...), Active: s.active}
		s.mu.Unlock()
		if len(payload.Items) == 0 {
			s.hideView()
		} else {
			s.showView(payload)
		}
	})
}

func callNextHook(hook windows.Handle, code int, wParam, lParam uintptr) uintptr {
	result, _, _ := procCallNextHookEx.Call(uintptr(hook), uintptr(code), wParam, lParam)
	return result
}

func isMouseButtonDown(message uint32) bool {
	switch message {
	case wmLButtonDown, wmRButtonDown, wmMButtonDown, wmXButtonDown:
		return true
	default:
		return false
	}
}

func (s *windowsInputState) handleMouseClick() {
	s.mu.Lock()
	visible := len(s.suggestions) > 0
	s.query = ""
	s.suggestions = nil
	s.active = 0
	s.mu.Unlock()
	if visible {
		s.updatePopup()
	}
}

func (s *windowsInputState) handleKey(message uint32, vk uint32) bool {
	isDown := message == wmKeyDown || message == wmSysKeyDown
	isUp := message == wmKeyUp || message == wmSysKeyUp
	if !isDown && !isUp {
		return false
	}

	if isUp {
		s.mu.Lock()
		swallow := s.swallowKeyUp[vk]
		if swallow {
			delete(s.swallowKeyUp, vk)
		}
		s.mu.Unlock()
		return swallow
	}

	if s.monitor != nil && s.monitor.browserOpen.Load() {
		if vk == vkEscape {
			foreground, _, _ := procGetForegroundWindow.Call()
			var processID uint32
			procGetWindowThreadProcessID.Call(foreground, uintptr(unsafe.Pointer(&processID)))
			if processID == uint32(os.Getpid()) {
				// Let the focused WebView close its context menu or editor first.
				return false
			}
			s.markKeyUpForSwallow(vk)
			s.enqueue(s.monitor.closeBrowser)
			return true
		}
		return false
	}

	foreground, _, _ := procGetForegroundWindow.Call()
	if isDown {
		s.mu.Lock()
		changedWindow := s.lastForeground != 0 && s.lastForeground != foreground
		s.lastForeground = foreground
		s.mu.Unlock()
		if changedWindow {
			s.reset()
		}
	}
	if foreground == s.managerHWND() || foreground == s.popupHWND() {
		return false
	}

	s.mu.Lock()
	visible := len(s.suggestions) > 0
	s.mu.Unlock()

	if keyPressed(vkCtrl) || keyPressed(vkMenu) || keyPressed(vkLWin) || keyPressed(vkRWin) {
		s.reset()
		return false
	}

	if visible {
		switch vk {
		case vkUp:
			s.markKeyUpForSwallow(vk)
			s.move(-1)
			return true
		case vkDown:
			s.markKeyUpForSwallow(vk)
			s.move(1)
			return true
		case vkReturn:
			s.markKeyUpForSwallow(vk)
			s.accept()
			return true
		case vkEscape:
			s.markKeyUpForSwallow(vk)
			s.reset()
			return true
		}
	}

	switch vk {
	case vkBack:
		s.backspace()
		return false
	case vkSpace, vkTab, vkReturn, vkLeft, vkRight, vkHome, vkEnd, vkDelete, vkPrior, vkNext:
		s.reset()
		return false
	}

	ch, ok := printableASCII(vk)
	if !ok {
		return false
	}
	if !isTokenChar(ch) {
		s.reset()
		return false
	}
	s.append(ch)
	return false
}

func (s *windowsInputState) markKeyUpForSwallow(vk uint32) {
	s.mu.Lock()
	s.swallowKeyUp[vk] = true
	s.mu.Unlock()
}

func keyPressed(vk uint32) bool {
	value, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
	return uint16(value)&0x8000 != 0
}

func capsLockOn() bool {
	value, _, _ := procGetKeyState.Call(vkCapital)
	return uint16(value)&1 != 0
}

func printableASCII(vk uint32) (rune, bool) {
	shift := keyPressed(vkShift)
	if vk >= 'A' && vk <= 'Z' {
		upper := shift != capsLockOn()
		if upper {
			return rune(vk), true
		}
		return rune(vk + 32), true
	}
	if vk >= '0' && vk <= '9' {
		if !shift {
			return rune(vk), true
		}
		shifted := ")!@#$%^&*("
		return rune(shifted[vk-'0']), true
	}
	if vk >= 0x60 && vk <= 0x69 {
		return rune('0' + (vk - 0x60)), true
	}
	mapping := map[uint32][2]rune{
		0xBA: {';', ':'}, 0xBB: {'=', '+'}, 0xBC: {',', '<'}, 0xBD: {'-', '_'},
		0xBE: {'.', '>'}, 0xBF: {'/', '?'}, 0xC0: {'`', '~'}, 0xDB: {'[', '{'},
		0xDC: {'\\', '|'}, 0xDD: {']', '}'}, 0xDE: {'\'', '"'},
	}
	pair, ok := mapping[vk]
	if !ok {
		return 0, false
	}
	if shift {
		return pair[1], true
	}
	return pair[0], true
}

func isTokenChar(ch rune) bool {
	if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
		return true
	}
	switch ch {
	case '-', '_', '.', '@', '#':
		return true
	default:
		return false
	}
}

func (s *windowsInputState) append(ch rune) {
	s.mu.Lock()
	s.query += string(ch)
	query := s.query
	s.mu.Unlock()
	s.refresh(query)
}

func (s *windowsInputState) backspace() {
	s.mu.Lock()
	runes := []rune(s.query)
	if len(runes) > 0 {
		runes = runes[:len(runes)-1]
	}
	s.query = string(runes)
	query := s.query
	s.mu.Unlock()
	s.refresh(query)
}

func (s *windowsInputState) refresh(query string) {
	if utf8.RuneCountInString(query) < 3 {
		s.hideSuggestions()
		return
	}
	items := s.monitor.store.Match(query, 8)
	if len(items) == 0 {
		s.hideSuggestions()
		return
	}

	s.mu.Lock()
	s.suggestions = items
	if s.active < 0 || s.active >= len(items) {
		s.active = 0
	}
	s.mu.Unlock()
	s.updatePopup()
}

func (s *windowsInputState) move(delta int) {
	s.mu.Lock()
	if len(s.suggestions) == 0 {
		s.mu.Unlock()
		return
	}
	s.active = (s.active + delta + len(s.suggestions)) % len(s.suggestions)
	s.mu.Unlock()
	s.updatePopup()
}

func (s *windowsInputState) accept() {
	s.mu.Lock()
	if len(s.suggestions) == 0 || s.active < 0 || s.active >= len(s.suggestions) {
		s.mu.Unlock()
		return
	}
	item := s.suggestions[s.active]
	query := s.query
	target := s.lastForeground
	s.query = ""
	s.suggestions = nil
	s.active = 0
	s.mu.Unlock()

	backspaces := utf8.RuneCountInString(query)
	content := item.Content
	s.enqueue(func() {
		s.hideView()
		foreground, _, _ := procGetForegroundWindow.Call()
		if target != 0 && foreground != target {
			return
		}
		if err := s.replaceText(backspaces, content); err != nil {
			log.Printf("补全插入失败: %v", err)
		}
	})
}

func (s *windowsInputState) reset() {
	s.mu.Lock()
	s.query = ""
	s.suggestions = nil
	s.active = 0
	s.mu.Unlock()
	s.updatePopup()
}

func (s *windowsInputState) hideSuggestions() {
	s.mu.Lock()
	s.suggestions = nil
	s.active = 0
	s.mu.Unlock()
	s.updatePopup()
}

func (s *windowsInputState) render(payload SuggestionPayload) {
	s.monitor.popup.EmitEvent("suggestions", payload)
}

func (s *windowsInputState) showPopup(payload SuggestionPayload) {
	s.render(payload)
	hwnd := s.popupHWND()
	if hwnd == 0 {
		return
	}
	ensurePopupNoActivate(hwnd)
	target, _, _ := procGetForegroundWindow.Call()
	caret := caretScreenRect()
	current, _, _ := procGetForegroundWindow.Call()
	if current != target {
		return
	}

	const logicalWidth = 280
	const rowHeight = 28
	const listPadding = 8
	logicalHeight := listPadding + min(len(payload.Items), 6)*rowHeight
	monitor, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&caret)), 2)
	info := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	if ok, _, _ := procGetMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info))); ok == 0 {
		return
	}
	dpi := uintptr(96)
	var dpiX, dpiY uint32
	if hr, _, _ := procGetDpiForMonitor.Call(monitor, 0, uintptr(unsafe.Pointer(&dpiX)), uintptr(unsafe.Pointer(&dpiY))); int32(hr) >= 0 && dpiX > 0 {
		dpi = uintptr(dpiX)
	}
	bounds := placePopup(caret, info.Work, scaleForDPI(logicalWidth, dpi), scaleForDPI(logicalHeight, dpi), scaleForDPI(4, dpi))
	x, y, width, height := bounds.Left, bounds.Top, bounds.Right-bounds.Left, bounds.Bottom-bounds.Top

	application.InvokeSync(func() {
		procSetWindowPos.Call(hwnd, hwndTopmost, uintptr(int64(x)), uintptr(int64(y)), uintptr(width), uintptr(height), swpNoActivate)
		// Initialise the WebView visibility once. Subsequent displays use the native
		// non-activating path, so a second completion cannot steal the editor's focus.
		if !s.popupInitialised {
			foreground, _, _ := procGetForegroundWindow.Call()
			s.monitor.popup.Show()
			current, _, _ := procGetForegroundWindow.Call()
			if current == hwnd && foreground != 0 {
				procSetForegroundWindow.Call(foreground)
			}
			s.popupInitialised = true
		}
		const swShowNoActivate = 4
		procShowWindow.Call(hwnd, swShowNoActivate)
	})
}

func scaleForDPI(value int, dpi uintptr) int {
	return (value*int(dpi) + 48) / 96
}

func (s *windowsInputState) hidePopup() {
	application.InvokeSync(func() {
		if hwnd := s.popupHWND(); hwnd != 0 {
			procShowWindow.Call(hwnd, 0)
		}
	})
}

func ensurePopupNoActivate(hwnd uintptr) {
	// Let DWM own the only outside corner; the WebView fills its entire client area.
	const dwmWindowCornerPreference = 33
	roundSmall := uint32(3)
	procDwmSetWindowAttribute.Call(hwnd, dwmWindowCornerPreference, uintptr(unsafe.Pointer(&roundSmall)), unsafe.Sizeof(roundSmall))
	index := int32(gwlExStyle)
	indexArg := uintptr(uint32(index))
	style, _, _ := procGetWindowLongW.Call(hwnd, indexArg)
	if style&wsExNoActivate != 0 {
		return
	}
	procSetWindowLongW.Call(hwnd, indexArg, style|wsExNoActivate)
}

func (s *windowsInputState) popupHWND() uintptr {
	ptr := s.monitor.popup.NativeWindow()
	if ptr == nil {
		return 0
	}
	return uintptr(ptr)
}

func (s *windowsInputState) managerHWND() uintptr {
	ptr := s.monitor.manager.NativeWindow()
	if ptr == nil {
		return 0
	}
	return uintptr(ptr)
}

func sendReplacement(backspaces int, text string) error {
	inputs := make([]keyboardInput, 0, backspaces*2+len(text)*2)
	for i := 0; i < backspaces; i++ {
		inputs = append(inputs,
			keyboardInput{Type: inputKeyboard, Ki: keybdInput{Vk: vkBack, ExtraInfo: quickPhraseInputMarker}},
			keyboardInput{Type: inputKeyboard, Ki: keybdInput{Vk: vkBack, Flags: keyeventfKeyup, ExtraInfo: quickPhraseInputMarker}},
		)
	}
	for _, unit := range utf16.Encode([]rune(text)) {
		inputs = append(inputs,
			keyboardInput{Type: inputKeyboard, Ki: keybdInput{Scan: unit, Flags: keyeventfUnicode, ExtraInfo: quickPhraseInputMarker}},
			keyboardInput{Type: inputKeyboard, Ki: keybdInput{Scan: unit, Flags: keyeventfUnicode | keyeventfKeyup, ExtraInfo: quickPhraseInputMarker}},
		)
	}
	if len(inputs) == 0 {
		return nil
	}
	sent, _, err := procSendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(inputs[0]))
	if sent != uintptr(len(inputs)) {
		return fmt.Errorf("SendInput: sent %d/%d events: %v", sent, len(inputs), err)
	}
	return nil
}
