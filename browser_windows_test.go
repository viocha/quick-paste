//go:build windows

package main

import (
	"fmt"
	"golang.org/x/sys/windows/registry"
	"testing"
	"time"
)

func TestParseHotkey(t *testing.T) {
	for _, test := range []struct {
		input, name    string
		modifiers, key uint32
	}{
		{"", "", 0, 0}, {"Alt+Ctrl+Space", "Ctrl+Alt+Space", 3, 32},
		{"Ctrl+Shift+A", "Ctrl+Shift+A", 6, 65}, {"Win+9", "Win+9", 8, 57},
		{"Alt+F24", "Alt+F24", 1, 0x87},
	} {
		got, err := parseHotkey(test.input)
		if err != nil || got.name != test.name || got.modifiers != test.modifiers || got.key != test.key {
			t.Fatalf("%q: %+v %v", test.input, got, err)
		}
	}
	for _, value := range []string{"A", "Ctrl", "Ctrl+", "Ctrl+Ctrl+A", "Ctrl+F25", "Ctrl+F0", "Ctrl+Enter", "Meta+A", "Ctrl+A+B"} {
		if _, err := parseHotkey(value); err == nil {
			t.Errorf("accepted invalid shortcut %q", value)
		}
	}
}

func TestGlobalHotkeyPersistenceAndConflict(t *testing.T) {
	makeService := func(suffix string) *BrowserService {
		path := fmt.Sprintf(`Software\quick-phrase-hotkey-test-%d-%s`, time.Now().UnixNano(), suffix)
		key, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
		if err != nil {
			t.Fatal(err)
		}
		if err := key.SetStringValue("BrowserHotkey", ""); err != nil {
			t.Fatal(err)
		}
		key.Close()
		s := &BrowserService{registryPath: path}
		s.start()
		t.Cleanup(func() { s.stop(); registry.DeleteKey(registry.CURRENT_USER, path) })
		return s
	}
	first, second := makeService("first"), makeService("second")
	const shortcut = "Ctrl+Alt+Shift+F23"
	if err := first.SetHotkey(shortcut); err != nil {
		t.Skipf("test shortcut unavailable: %v", err)
	}
	if err := second.SetHotkey(shortcut); err == nil {
		t.Fatal("conflicting shortcut accepted")
	}
	if second.GetHotkey() != "" {
		t.Fatal("failed registration changed settings")
	}
	const clipboardShortcut = "Ctrl+Alt+Shift+F22"
	if err := first.SetClipboardHotkey(clipboardShortcut); err != nil {
		t.Skipf("test clipboard shortcut unavailable: %v", err)
	}
	if err := second.SetClipboardHotkey(clipboardShortcut); err == nil {
		t.Fatal("conflicting clipboard shortcut accepted")
	}
	if first.GetClipboardHotkey() != clipboardShortcut || first.GetHotkey() != shortcut {
		t.Fatal("clipboard shortcut changed the phrase shortcut")
	}
	if err := first.SetHotkey("invalid"); err == nil {
		t.Fatal("invalid shortcut accepted")
	}
	if first.GetHotkey() != shortcut {
		t.Fatal("previous shortcut lost")
	}
	first.stop()
	first.start()
	if first.GetHotkey() != shortcut || first.GetHotkeyError() != "" {
		t.Fatal("shortcut was not restored")
	}
	if err := second.SetHotkey(shortcut); err == nil {
		t.Fatal("restored shortcut not registered")
	}
	if err := first.SetHotkey(""); err != nil {
		t.Fatal(err)
	}
	if err := second.SetHotkey(shortcut); err != nil {
		t.Fatalf("disabled shortcut not released: %v", err)
	}
	first.stop()
	first.start()
	if first.GetHotkey() != "" {
		t.Fatal("disabled setting was not restored")
	}
}
