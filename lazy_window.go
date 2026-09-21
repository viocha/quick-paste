package main

import (
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

type lazyWindow struct {
	mu        sync.Mutex
	window    *application.WebviewWindow
	timer     *time.Timer
	idleDelay time.Duration
	create    func() *application.WebviewWindow
}

func (s *lazyWindow) Ensure() *application.WebviewWindow {
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if s.window != nil {
		window := s.window
		s.mu.Unlock()
		return window
	}
	window := s.create()
	s.window = window
	s.mu.Unlock()

	window.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) {
		s.clear(window)
	})
	return window
}

func (s *lazyWindow) Current() *application.WebviewWindow {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.window
}

func (s *lazyWindow) SetIdleDelay(delay time.Duration) {
	s.mu.Lock()
	s.idleDelay = delay
	if s.timer != nil {
		s.timer.Stop()
		s.timer = time.AfterFunc(s.idleDelay, s.CloseNow)
	}
	s.mu.Unlock()
}

func (s *lazyWindow) Release() {
	s.mu.Lock()
	if s.window == nil {
		s.mu.Unlock()
		return
	}
	if s.timer != nil {
		s.timer.Stop()
	}
	if s.idleDelay <= 0 {
		window := s.window
		s.window = nil
		s.timer = nil
		s.mu.Unlock()
		window.Close()
		return
	}
	s.timer = time.AfterFunc(s.idleDelay, s.CloseNow)
	s.mu.Unlock()
}

func (s *lazyWindow) CloseNow() {
	s.mu.Lock()
	window := s.window
	s.window = nil
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.mu.Unlock()
	if window != nil {
		window.Close()
	}
}

func (s *lazyWindow) clear(window *application.WebviewWindow) {
	s.mu.Lock()
	if s.window == window {
		s.window = nil
	}
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.mu.Unlock()
}
