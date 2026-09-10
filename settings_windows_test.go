//go:build windows

package main

import (
	"fmt"
	"golang.org/x/sys/windows/registry"
	"os"
	"testing"
	"time"
)

func TestStartupMode(t *testing.T) {
	if isAutostart([]string{"app.exe"}) || !isAutostart([]string{"app.exe", "--autostart"}) {
		t.Fatal("incorrect startup mode")
	}
	if got := startupCommand(`C:\Program Files\quick-paste.exe`); got != `"C:\Program Files\quick-paste.exe" --autostart` {
		t.Fatalf("command: %q", got)
	}
}

func TestTooltipDelayPersistsAndValidates(t *testing.T) {
	path := fmt.Sprintf(`Software\quick-paste-tooltip-test-%d`, time.Now().UnixNano())
	defer registry.DeleteKey(registry.CURRENT_USER, path)
	s := &SettingsService{registryPath: path}
	if value, err := s.GetTooltipDelay(); err != nil || value != 500 {
		t.Fatalf("default: %d %v", value, err)
	}
	for _, delay := range []int{0, 250, 1200, 5000} {
		if err := s.SetTooltipDelay(delay); err != nil {
			t.Fatal(err)
		}
		reloaded := &SettingsService{registryPath: path}
		if value, err := reloaded.GetTooltipDelay(); err != nil || value != delay {
			t.Fatalf("persisted %d: %d %v", delay, value, err)
		}
	}
	for _, delay := range []int{-1, 5001} {
		if err := s.SetTooltipDelay(delay); err == nil {
			t.Fatalf("accepted %d", delay)
		}
	}
	if value, err := s.GetTooltipDelay(); err != nil || value != 5000 {
		t.Fatalf("invalid input changed value: %d %v", value, err)
	}
}
func TestTooltipTimeoutPersistsAndValidates(t *testing.T) {
	path := fmt.Sprintf(`Software\quick-paste-tooltip-timeout-test-%d`, time.Now().UnixNano())
	defer registry.DeleteKey(registry.CURRENT_USER, path)
	s := &SettingsService{registryPath: path}
	if value, err := s.GetTooltipTimeout(); err != nil || value != 0 {
		t.Fatalf("default: %d %v", value, err)
	}
	for _, timeout := range []int{0, 200, 1000, 5000} {
		if err := s.SetTooltipTimeout(timeout); err != nil {
			t.Fatal(err)
		}
		if value, err := (&SettingsService{registryPath: path}).GetTooltipTimeout(); err != nil || value != timeout {
			t.Fatalf("persisted %d: %d %v", timeout, value, err)
		}
	}
	for _, timeout := range []int{-1, 5001} {
		if err := s.SetTooltipTimeout(timeout); err == nil {
			t.Fatalf("accepted %d", timeout)
		}
	}
}
func TestAutostartSettingsRoundTrip(t *testing.T) {
	// Exercise registry writes in an isolated key, never enable real login startup.
	path := fmt.Sprintf(`Software\quick-paste-test-%d`, time.Now().UnixNano())
	defer registry.DeleteKey(registry.CURRENT_USER, path)
	s := &SettingsService{registryPath: path}
	if enabled, err := s.GetAutostart(); err != nil || enabled {
		t.Fatalf("initial: %v %v", enabled, err)
	}
	if err := s.SetAutostart(true); err != nil {
		t.Fatal(err)
	}
	if enabled, err := s.GetAutostart(); err != nil || !enabled {
		t.Fatalf("enabled: %v %v", enabled, err)
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	command, _, err := key.GetStringValue(startupValueName)
	key.Close()
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	if command != startupCommand(exe) {
		t.Fatalf("command: %q", command)
	}
	if err := s.SetAutostart(false); err != nil {
		t.Fatal(err)
	}
	if enabled, err := s.GetAutostart(); err != nil || enabled {
		t.Fatalf("disabled: %v %v", enabled, err)
	}
	if err := s.SetAutostart(false); err != nil {
		t.Fatal(err)
	}
}
