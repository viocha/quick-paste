//go:build windows

package main

import (
	"errors"
	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows/registry"
	"os"
	"slices"
)

const startupRegistryPath = `Software\Microsoft\Windows\CurrentVersion\Run`
const startupValueName = "quick-phrase"

type SettingsService struct {
	registryPath string
	app          *application.App
}

func (s *SettingsService) preferencesPath() string {
	if s.registryPath != "" {
		return s.registryPath
	}
	return `Software\quick-phrase`
}

func (s *SettingsService) GetTooltipDelay() (int, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, s.preferencesPath(), registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return 500, nil
	}
	if err != nil {
		return 0, err
	}
	defer key.Close()
	value, _, err := key.GetIntegerValue("TooltipDelay")
	if errors.Is(err, registry.ErrNotExist) {
		return 500, nil
	}
	if err != nil {
		return 0, err
	}
	if value > 5000 {
		return 500, nil
	}
	return int(value), nil
}

func (s *SettingsService) SetTooltipDelay(delay int) error {
	if delay < 0 || delay > 5000 {
		return errors.New("延迟须为 0–5000 毫秒")
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, s.preferencesPath(), registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if err := key.SetDWordValue("TooltipDelay", uint32(delay)); err != nil {
		return err
	}
	if s.app != nil {
		s.app.Event.Emit("tooltip-delay-changed", delay)
	}
	return nil
}

func (s *SettingsService) GetTooltipTimeout() (int, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, s.preferencesPath(), registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer key.Close()
	value, _, err := key.GetIntegerValue("TooltipTimeout")
	if errors.Is(err, registry.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if value > 5000 {
		return 0, nil
	}
	return int(value), nil
}

func (s *SettingsService) SetTooltipTimeout(timeout int) error {
	if timeout < 0 || timeout > 5000 {
		return errors.New("timeout 须为 0–5000 毫秒")
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, s.preferencesPath(), registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if err := key.SetDWordValue("TooltipTimeout", uint32(timeout)); err != nil {
		return err
	}
	if s.app != nil {
		s.app.Event.Emit("tooltip-timeout-changed", timeout)
	}
	return nil
}

func (s *SettingsService) keyPath() string {
	if s.registryPath != "" {
		return s.registryPath
	}
	return startupRegistryPath
}

func isAutostart(args []string) bool          { return slices.Contains(args, "--autostart") }
func startupCommand(executable string) string { return `"` + executable + `" --autostart` }
func (s *SettingsService) GetAutostart() (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, s.keyPath(), registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer key.Close()
	value, _, err := key.GetStringValue(startupValueName)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	return value != "", err
}
func (s *SettingsService) SetAutostart(enabled bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, s.keyPath(), registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if !enabled {
		err = key.DeleteValue(startupValueName)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	return key.SetStringValue(startupValueName, startupCommand(executable))
}
