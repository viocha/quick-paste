package main

import "github.com/wailsapp/wails/v3/pkg/application"
import "sync/atomic"

type InputMonitor struct {
	app                *application.App
	store              *PhraseStore
	managerWindow      func() *application.WebviewWindow
	ensurePopup        func() *application.WebviewWindow
	popupWindow        func() *application.WebviewWindow
	releasePopup       func()
	stopFn             func()
	resetFn            func()
	browserOpen        atomic.Bool
	closeBrowser       func()
	browserMouseDown   func(uintptr)
	browserKey         func(string)
	takeOverWinV       func() bool
	openClipboard      func()
	pasteClipboardText func(string, uintptr, func() error) error
}

func NewInputMonitor(app *application.App, store *PhraseStore) *InputMonitor {
	return &InputMonitor{app: app, store: store}
}

func (m *InputMonitor) Start() error {
	stop, err := startPlatformInputMonitor(m)
	if err != nil {
		return err
	}
	m.stopFn = stop
	return nil
}

func (m *InputMonitor) Stop() {
	if m.stopFn != nil {
		m.stopFn()
		m.stopFn = nil
	}
}
