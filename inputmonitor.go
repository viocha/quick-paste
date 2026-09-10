package main

import "github.com/wailsapp/wails/v3/pkg/application"
import "sync/atomic"

type InputMonitor struct {
	app              *application.App
	manager          *application.WebviewWindow
	popup            *application.WebviewWindow
	store            *PhraseStore
	stopFn           func()
	resetFn          func()
	browserOpen      atomic.Bool
	closeBrowser     func()
	browserMouseDown func(uintptr)
	takeOverWinV     func() bool
	openClipboard    func()
}

func NewInputMonitor(app *application.App, manager, popup *application.WebviewWindow, store *PhraseStore) *InputMonitor {
	return &InputMonitor{app: app, manager: manager, popup: popup, store: store}
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
