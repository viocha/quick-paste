//go:build windows

package main

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func testInputState(t *testing.T) *windowsInputState {
	t.Helper()
	method := completionInsertMethodRuntime.Swap(runtimeInputSend)
	t.Cleanup(func() { completionInsertMethodRuntime.Store(method) })
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

func TestIMECommitShiftDismissesCompletion(t *testing.T) {
	for _, vk := range []uint32{vkShift, vkLShift, vkRShift} {
		t.Run(fmt.Sprintf("Shift_%X", vk), func(t *testing.T) {
			s := testInputState(t)
			native := true
			s.nativeInputMode = func() bool { return native }
			shows, hides := 0, 0
			s.showView = func(SuggestionPayload) { shows++ }
			s.hideView = func() { hides++ }
			for _, ch := range "abc" {
				s.append(ch)
			}
			drainActions(t, s)
			shows, hides = 0, 0
			version := s.queryVersion
			if s.handleKey(wmKeyDown, vk) {
				t.Fatal("Shift 按下不能被吞掉")
			}
			if s.query != "abc" {
				t.Fatal("Shift 释放前应保留补全前缀")
			}
			native = false
			if s.handleKey(wmKeyUp, vk) {
				t.Fatal("Shift 释放不能被吞掉")
			}
			drainActions(t, s)
			if s.query != "" || len(s.suggestions) != 0 || s.queryVersion == version || hides != 1 || shows != 0 {
				t.Fatalf("上屏后补全未清理: query=%q hides=%d shows=%d", s.query, hides, shows)
			}
			for _, ch := range "abc" {
				s.append(ch)
			}
			drainActions(t, s)
			if s.query != "abc" || shows == 0 {
				t.Fatal("新输入应从新前缀触发补全")
			}
		})
	}
}

func TestShiftPreservesOrdinaryCompletion(t *testing.T) {
	for _, native := range []bool{false, true} {
		s := testInputState(t)
		s.nativeInputMode = func() bool { return native }
		for _, ch := range "abc" {
			s.append(ch)
		}
		s.handleKey(wmKeyDown, vkLShift)
		if native {
			// 中文状态下 Shift 加字母属于组合输入，不能在释放时清空前缀。
			s.handleKey(wmKeyDown, 'A')
			s.handleKey(wmKeyUp, 'A')
		}
		s.handleKey(wmKeyUp, vkLShift)
		drainActions(t, s)
		want := "abc"
		if native {
			want += "a"
		}
		if s.query != want {
			t.Fatalf("普通输入前缀 = %q，期望 %q", s.query, want)
		}
	}
}

func TestIMECommitShiftDiscardsPendingPopup(t *testing.T) {
	s := testInputState(t)
	s.nativeInputMode = func() bool { return true }
	gate := make(chan struct{})
	s.enqueue(func() { <-gate })
	shows := 0
	s.showView = func(SuggestionPayload) { shows++ }
	s.append('a')
	s.append('b')
	s.appendKey('c', 'C')
	s.handleKey(wmKeyDown, vkLShift)
	s.handleKey(wmKeyUp, vkLShift)
	s.handleKey(wmKeyUp, 'C')
	close(gate)
	drainActions(t, s)
	if s.query != "" || len(s.pendingPopupKeyUp) != 0 || shows != 0 {
		t.Fatal("英文上屏后不能重新显示排队中的候选")
	}
}

func TestResetCancelsIMECommitShift(t *testing.T) {
	s := testInputState(t)
	s.nativeInputMode = func() bool { return true }
	s.append('a')
	s.handleKey(wmKeyDown, vkLShift)
	s.handleMouseClick()
	for _, ch := range "abc" {
		s.append(ch)
	}
	s.handleKey(wmKeyUp, vkLShift)
	drainActions(t, s)
	if s.query != "abc" || len(s.suggestions) != 1 {
		t.Fatal("焦点切换后的新查询不能被上一个 Shift 释放清空")
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

func TestPopupAnchorIgnoresIMECandidateGeometry(t *testing.T) {
	// 英文、中文模式统一由光标所在文本行决定 Y。
	caret := rect{Left: 432, Top: 30, Right: 433, Bottom: 70}
	work := rect{Left: 0, Top: 0, Right: 3872, Bottom: 2352}
	bounds := placePopup(caret, work, 700, 90, 10)
	if bounds.Left != 432 || bounds.Top != 80 {
		t.Fatalf("面板必须紧贴光标所在文本行下方: %+v", bounds)
	}
}

func TestEdgeValueChangeOnlyRetriesInitialPosition(t *testing.T) {
	s := testInputState(t)
	s.query = "abc"
	s.suggestions = []Suggestion{{ID: "abc", Name: "abc"}}
	updates := 0
	s.showView = func(SuggestionPayload) { updates++ }

	// 还没有首次定位时，Edge ValuePattern 更新应继续完成定位。
	s.onEdgeOmniboxValueChange()
	drainActions(t, s)
	if updates != 1 {
		t.Fatalf("首次定位等待文本同步，应触发一次刷新，得到 %d", updates)
	}

	// 第三个键尚未释放，不应提前显示或关闭尚未显示的面板。
	s.pendingPopupKeyUp = make(map[uint32]bool)
	s.pendingPopupKeyUp['C'] = true
	s.onEdgeOmniboxValueChange()
	drainActions(t, s)
	if updates != 1 {
		t.Fatalf("首次触发尚未 keyup，刷新次数 = %d", updates)
	}
	delete(s.pendingPopupKeyUp, 'C')

	// 面板已显示：异步 ValuePattern 事件只代表输入内容变化。
	s.mu.Lock()
	s.popupPositioned = true
	s.popupX, s.popupY, s.popupDPI = 728, 215, 240
	s.mu.Unlock()
	for range 5 {
		s.onEdgeOmniboxValueChange()
	}
	drainActions(t, s)
	if !s.popupPositioned || s.popupX != 728 || s.popupY != 215 || s.popupDPI != 240 {
		t.Fatalf("已显示面板的坐标被 Edge 文本变化覆盖: positioned=%t pos=(%d,%d) dpi=%d",
			s.popupPositioned, s.popupX, s.popupY, s.popupDPI)
	}
	if updates != 1 {
		t.Fatalf("面板持续显示时不应该执行重复的定位刷新，得到 %d", updates)
	}

	s.reset()
	drainActions(t, s)
	s.onEdgeOmniboxValueChange()
	drainActions(t, s)
	if updates != 1 || s.popupPositioned {
		t.Fatalf("已关闭面板不得因 Edge ValuePattern 事件重新显示：updates=%d", updates)
	}
}

func TestPopupPositionFrozenThroughQueryAndListChanges(t *testing.T) {
	s := testInputState(t)
	s.monitor.store.nodes = []PhraseNode{
		{ID: "folder", Type: NodeTypeFolder, Name: "Enabled", CompletionEnabled: true},
		{ID: "abc-1", ParentID: "folder", Type: NodeTypePhrase, Name: "abc-one"},
		{ID: "abcd-2", ParentID: "folder", Type: NodeTypePhrase, Name: "abcd-two"},
		{ID: "abcde-3", ParentID: "folder", Type: NodeTypePhrase, Name: "abcde-three"},
	}
	for _, ch := range "abc" {
		s.append(ch)
	}
	drainActions(t, s)

	// 模拟初次显示时已经选定屏幕锚点。
	s.mu.Lock()
	s.popupPositioned = true
	s.popupX, s.popupY, s.popupDPI = 728, 215, 240
	s.mu.Unlock()

	type snapshot struct {
		query string
		items int
		x, y  int32
	}
	var shown []snapshot
	hidden := 0
	s.showView = func(payload SuggestionPayload) {
		s.mu.Lock()
		shown = append(shown, snapshot{payload.Query, len(payload.Items), s.popupX, s.popupY})
		s.mu.Unlock()
	}
	s.hideView = func() {
		hidden++
		s.mu.Lock()
		s.popupPositioned = false // 与真实 hidePopup 完成关闭后的行为一致
		s.mu.Unlock()
	}

	s.append('d') // abc -> abcd：3 项变成 2 项
	drainActions(t, s)
	s.append('e') // abcd -> abcde：2 项变成 1 项
	drainActions(t, s)
	s.backspace() // abcde -> abcd：恢复 2 项
	drainActions(t, s)
	s.onEdgeOmniboxValueChange() // 无障碍事件不应再次定位
	drainActions(t, s)

	want := []snapshot{
		{query: "abcd", items: 2, x: 728, y: 215},
		{query: "abcde", items: 1, x: 728, y: 215},
		{query: "abcd", items: 2, x: 728, y: 215},
	}
	if len(shown) != len(want) {
		t.Fatalf("更新次数 = %d, want %d; snapshots=%+v", len(shown), len(want), shown)
	}
	for i, expected := range want {
		if shown[i] != expected {
			t.Fatalf("第 %d 次更新: got=%+v want=%+v", i, shown[i], expected)
		}
	}
	if hidden != 0 || !s.popupPositioned || s.popupX != 728 || s.popupY != 215 {
		t.Fatalf("本轮补全面板不应关闭或移动: hidden=%d position=(%d,%d) locked=%t",
			hidden, s.popupX, s.popupY, s.popupPositioned)
	}

	// 没有匹配项时才关闭会话，下一次显示必须重新定位。
	s.append('x')
	drainActions(t, s)
	if hidden != 1 || s.popupPositioned {
		t.Fatalf("无匹配项必须结束本轮定位会话: hidden=%d positioned=%t", hidden, s.popupPositioned)
	}
	s.backspace() // 回到 abcde，匹配再次出现。
	drainActions(t, s)
	if s.popupPositioned {
		t.Fatal("关闭后的新一轮补全必须重新定位，不能使用上一轮坐标")
	}
}

func TestPendingHideDoesNotMovePopupUnlessActuallyClosed(t *testing.T) {
	s := testInputState(t)
	s.monitor.store.nodes = []PhraseNode{
		{ID: "folder", Type: NodeTypeFolder, CompletionEnabled: true},
		{ID: "abcd", ParentID: "folder", Type: NodeTypePhrase, Name: "abcd-one"},
	}
	for _, ch := range "abcd" {
		s.append(ch)
	}
	drainActions(t, s)
	s.mu.Lock()
	s.popupPositioned = true
	s.popupX, s.popupY, s.popupDPI = 432, 148, 240
	s.mu.Unlock()

	// 阻塞 UI action 队列，模拟短时间的无匹配、随后 Backspace。
	gate := make(chan struct{})
	s.enqueue(func() { <-gate })
	shows, hides := 0, 0
	s.showView = func(SuggestionPayload) { shows++ }
	s.hideView = func() {
		hides++
		s.mu.Lock()
		s.popupPositioned = false
		s.mu.Unlock()
	}

	s.append('x') // 查询 abcdx 暂时没有匹配，关闭操作排队但尚未执行。
	if !s.popupPositioned {
		t.Fatal("原生窗口还未真正隐藏，不应提前清空锚点")
	}
	s.backspace() // 在 UI action 执行前恢复 abcd，面板实际上从未关闭。
	close(gate)
	drainActions(t, s)
	if hides != 0 || shows == 0 || !s.popupPositioned ||
		s.popupX != 432 || s.popupY != 148 || s.popupDPI != 240 {
		t.Fatalf("同一可见会话不能重定位: shows=%d hides=%d positioned=%t pos=(%d,%d) dpi=%d",
			shows, hides, s.popupPositioned, s.popupX, s.popupY, s.popupDPI)
	}
}
