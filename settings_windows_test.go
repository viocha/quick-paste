//go:build windows

package main

import (
	"encoding/xml"
	"fmt"
	"testing"
	"time"

	"github.com/go-ole/go-ole"
	"golang.org/x/sys/windows/registry"
)

func TestHiddenStartMode(t *testing.T) {
	if isHiddenStart([]string{"app.exe"}) || !isHiddenStart([]string{"app.exe", "--hidden"}) {
		t.Fatal("incorrect hidden start mode")
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

func TestInputIntervalPersistsAndValidates(t *testing.T) {
	path := fmt.Sprintf(`Software\quick-paste-input-interval-test-%d`, time.Now().UnixNano())
	defer registry.DeleteKey(registry.CURRENT_USER, path)
	s := &SettingsService{registryPath: path}
	if value, err := s.GetInputIntervalMs(); err != nil || value != 0 {
		t.Fatalf("default: %d %v", value, err)
	}
	changed := -1
	s.onInputIntervalChanged = func(interval int) { changed = interval }
	for _, interval := range []int{30, 100, maxInsertIntervalMs, 0} {
		if err := s.SetInputIntervalMs(interval); err != nil {
			t.Fatal(err)
		}
		if value, err := (&SettingsService{registryPath: path}).GetInputIntervalMs(); err != nil || value != interval {
			t.Fatalf("persisted %d: %d %v", interval, value, err)
		}
		if changed != interval {
			t.Fatalf("runtime callback: %d", changed)
		}
	}
	for _, interval := range []int{-1, maxInsertIntervalMs + 1} {
		if err := s.SetInputIntervalMs(interval); err == nil {
			t.Fatalf("accepted %d", interval)
		}
	}
}

func TestWebViewIdleMinutesPersistsAndValidates(t *testing.T) {
	path := fmt.Sprintf(`Software\quick-paste-webview-idle-test-%d`, time.Now().UnixNano())
	defer registry.DeleteKey(registry.CURRENT_USER, path)
	s := &SettingsService{registryPath: path}
	if value, err := s.GetWebViewIdleMinutes(); err != nil || value != defaultWebViewIdleMinutes {
		t.Fatalf("default: %d %v", value, err)
	}
	for _, minutes := range []int{1, 5, 30, maxWebViewIdleMinutes} {
		if err := s.SetWebViewIdleMinutes(minutes); err != nil {
			t.Fatal(err)
		}
		if value, err := (&SettingsService{registryPath: path}).GetWebViewIdleMinutes(); err != nil || value != minutes {
			t.Fatalf("persisted %d: %d %v", minutes, value, err)
		}
	}
	for _, minutes := range []int{0, maxWebViewIdleMinutes + 1} {
		if err := s.SetWebViewIdleMinutes(minutes); err == nil {
			t.Fatalf("accepted %d", minutes)
		}
	}

	changed := 0
	s.onWebViewIdleMinutesChanged = func(minutes int) { changed = minutes }
	if err := s.SetWebViewIdleMinutes(12); err != nil {
		t.Fatal(err)
	}
	if changed != 12 {
		t.Fatalf("runtime callback: %d", changed)
	}
}

func TestMissingAutostartTaskIsDisabled(t *testing.T) {
	_, err := withTaskSchedulerRoot(func(root *ole.IDispatch) (struct{}, error) {
		enabled, lookupErr := scheduledTaskEnabled(root, fmt.Sprintf("quick-paste-missing-%d", time.Now().UnixNano()))
		if lookupErr != nil {
			return struct{}{}, lookupErr
		}
		if enabled {
			t.Fatal("missing task reported as enabled")
		}
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAutostartTaskXML(t *testing.T) {
	const executable = `C:\Program Files\quick-paste & tools\quick-paste.exe`
	const username = `DOMAIN\user&name`

	var task struct {
		Triggers struct {
			Logon struct {
				Enabled bool   `xml:"Enabled"`
				UserID  string `xml:"UserId"`
			} `xml:"LogonTrigger"`
		} `xml:"Triggers"`
		Principals struct {
			Principal struct {
				UserID    string `xml:"UserId"`
				LogonType string `xml:"LogonType"`
				RunLevel  string `xml:"RunLevel"`
			} `xml:"Principal"`
		} `xml:"Principals"`
		Settings struct {
			ExecutionTimeLimit string `xml:"ExecutionTimeLimit"`
		} `xml:"Settings"`
		Actions struct {
			Exec struct {
				Command   string `xml:"Command"`
				Arguments string `xml:"Arguments"`
			} `xml:"Exec"`
		} `xml:"Actions"`
	}
	if err := xml.Unmarshal([]byte(autostartTaskXML(executable, username)), &task); err != nil {
		t.Fatal(err)
	}
	if !task.Triggers.Logon.Enabled || task.Triggers.Logon.UserID != username {
		t.Fatalf("logon trigger: %+v", task.Triggers.Logon)
	}
	if task.Principals.Principal.UserID != username || task.Principals.Principal.LogonType != "InteractiveToken" || task.Principals.Principal.RunLevel != "HighestAvailable" {
		t.Fatalf("principal: %+v", task.Principals.Principal)
	}
	if task.Settings.ExecutionTimeLimit != "PT0S" {
		t.Fatalf("execution time limit: %q", task.Settings.ExecutionTimeLimit)
	}
	if task.Actions.Exec.Command != executable || task.Actions.Exec.Arguments != "--hidden" {
		t.Fatalf("action: %+v", task.Actions.Exec)
	}
}
