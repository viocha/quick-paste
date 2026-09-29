//go:build windows

package main

import (
	"sync"
	"testing"
	"time"
)

func testInputState(t *testing.T) *windowsInputState {
	t.Helper()
	s := &windowsInputState{
		monitor: &InputMonitor{store: &PhraseStore{nodes: []PhraseNode{
			{ID: "folder", Type: NodeTypeFolder, Name: "Enabled", CompletionEnabled: true},
			{ID: "abc", ParentID: "folder", Type: NodeTypePhrase, Name: "abc", Content: "completed"},
		}}},
		actions: make(chan struct{}, 1), done: make(chan struct{}), swallowKeyUp: make(map[uint32]bool),
		showView: func(SuggestionPayload) {}, hideView: func() {},
		replaceText: func(int, string) error { return nil }, replaceFullText: func(int, string) error { return nil },
	}
	go s.runActions()
	t.Cleanup(func() { close(s.done) })
	return s
}

func drainActions(t *testing.T, s *windowsInputState) {
	t.Helper()
	done := make(chan struct{})
	s.enqueue(func() { close(done) })
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("action queue stalled")
	}
}

func TestConsecutiveCompletions(t *testing.T) {
	s := testInputState(t)
	var mu sync.Mutex
	replacements := 0
	s.replaceText = func(count int, text string) error {
		if count != 3 || text != "completed" {
			t.Errorf("replacement = %d, %q", count, text)
		}
		mu.Lock()
		replacements++
		mu.Unlock()
		return nil
	}
	for i := 0; i < 3; i++ {
		for _, ch := range "abc" {
			s.append(ch)
		}
		s.markKeyUpForSwallow(vkReturn)
		s.accept()
		if !s.handleKey(wmKeyUp, vkReturn) {
			t.Fatal("Enter release leaked")
		}
		if s.handleKey(wmKeyUp, vkReturn) {
			t.Fatal("Enter release stayed latched")
		}
		drainActions(t, s)
		if s.query != "" || len(s.suggestions) != 0 {
			t.Fatal("completion did not reset state")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if replacements != 3 {
		t.Fatalf("replacements = %d", replacements)
	}
}

func TestEnterAcceptsAfterKeyUp(t *testing.T) {
	s := testInputState(t)
	for _, ch := range "abc" {
		s.append(ch)
	}
	if !s.handleKey(wmKeyDown, vkReturn) {
		t.Fatal("Enter keydown was not swallowed")
	}
	if s.query != "abc" || !s.acceptOnKeyUp {
		t.Fatal("completion ran before Enter keyup")
	}
	if !s.handleKey(wmKeyUp, vkReturn) {
		t.Fatal("Enter keyup was not swallowed")
	}
	if s.query != "" || s.acceptOnKeyUp {
		t.Fatal("completion did not run after Enter keyup")
	}
	drainActions(t, s)
}

func TestCompletionReplacesWholePrefix(t *testing.T) {
	for _, tc := range []struct {
		name       string
		query      string
		content    string
		backspaces int
	}{
		{name: "相同前缀", query: "Lai", content: "LaiJun0805", backspaces: 3},
		{name: "完整内容", query: "hello", content: "hello", backspaces: 5},
		{name: "不同内容", query: "addr", content: "北京市", backspaces: 4},
		{name: "大小写不同", query: "abc", content: "ABC done", backspaces: 3},
		{name: "Unicode 前缀", query: "你好", content: "你好世界", backspaces: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testInputState(t)
			s.nativeInputMode = func() bool { return true }
			s.replaceFullText = func(backspaces int, content string) error {
				if backspaces != tc.backspaces || content != tc.content {
					t.Errorf("replacement = %d, %q; want %d, %q", backspaces, content, tc.backspaces, tc.content)
				}
				return nil
			}
			s.query = tc.query
			s.suggestions = []Suggestion{{Content: tc.content}}
			s.accept()
			drainActions(t, s)
		})
	}
}

func TestEnglishCompletionKeepsPrefixAfterChineseMode(t *testing.T) {
	s := testInputState(t)
	native := true
	s.nativeInputMode = func() bool { return native }
	s.replaceFullText = func(backspaces int, content string) error {
		if backspaces != 3 || content != "LaiJun0805" {
			t.Errorf("Chinese replacement = %d, %q", backspaces, content)
		}
		return nil
	}
	s.replaceText = func(backspaces int, content string) error {
		if backspaces != 0 || content != "Jun0805" {
			t.Errorf("English replacement = %d, %q", backspaces, content)
		}
		return nil
	}
	for i := 0; i < 2; i++ {
		s.query = "Lai"
		s.suggestions = []Suggestion{{Content: "LaiJun0805"}}
		s.accept()
		drainActions(t, s)
		native = false
	}
}

func TestQueuedUpdateDoesNotReshowDismissedCandidates(t *testing.T) {
	s := testInputState(t)
	gate := make(chan struct{})
	s.enqueue(func() { <-gate })
	shows := 0
	s.showView = func(SuggestionPayload) { shows++ }
	for _, ch := range "abc" {
		s.append(ch)
	}
	s.reset()
	close(gate)
	drainActions(t, s)
	if shows != 0 {
		t.Fatalf("stale popup shown %d times", shows)
	}
}

func TestNewQueryDiscardsOldPopupPositionBeforeQueuedHide(t *testing.T) {
	s := testInputState(t)
	gate := make(chan struct{})
	s.enqueue(func() { <-gate })
	s.popupPositioned = true
	oldVersion := s.queryVersion
	s.reset()
	for _, ch := range "abc" {
		s.append(ch)
	}
	if s.popupPositioned || s.queryVersion == oldVersion {
		t.Fatal("new query reused the previous popup position or generation")
	}
	close(gate)
	drainActions(t, s)
}

func TestMouseClickResetsPartialQuery(t *testing.T) {
	s := testInputState(t)
	s.append('a')
	s.append('b')
	s.handleMouseClick()
	for _, ch := range "abc" {
		s.append(ch)
	}
	if s.query != "abc" || len(s.suggestions) != 1 {
		t.Fatalf("query survived click: %q", s.query)
	}
	drainActions(t, s)
}

func TestFirstPopupWaitsForTriggerKeyUp(t *testing.T) {
	s := testInputState(t)
	shows := 0
	s.showView = func(SuggestionPayload) { shows++ }

	s.append('a')
	s.append('b')
	s.appendKey('c', 'C')
	drainActions(t, s)
	if shows != 0 {
		t.Fatalf("popup shown before trigger keyup: %d", shows)
	}

	s.handleKey(wmKeyUp, 'C')
	drainActions(t, s)
	if shows != 1 {
		t.Fatalf("popup shows after trigger keyup = %d", shows)
	}
}

func TestActionQueuePreservesOrderBeyondOldCapacity(t *testing.T) {
	s := testInputState(t)
	gate := make(chan struct{})
	s.enqueue(func() { <-gate })
	next := 0
	for i := 0; i < 300; i++ {
		s.enqueue(func() {
			if next != i {
				t.Errorf("action %d ran at %d", i, next)
			}
			next++
		})
	}
	close(gate)
	drainActions(t, s)
	if next != 300 {
		t.Fatalf("actions executed = %d", next)
	}
}
