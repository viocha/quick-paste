//go:build windows

package main

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/user"
	"runtime"
	"slices"
	"strings"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows/registry"
)

const (
	autostartTaskName                = "quick-paste"
	taskCreateOrUpdate               = 6
	taskLogonNone                    = 0
	taskSchedulerFileNotFoundHResult = 0x80070002
	defaultWebViewIdleMinutes        = 60
	maxWebViewIdleMinutes            = 1440
	maxInsertIntervalMs              = 1000
	inputMethodCtrlV                 = "ctrl-v"
	inputMethodShiftInsert           = "shift-insert"
	inputMethodSendInput             = "send-input"
)

type SettingsService struct {
	registryPath                      string
	app                               *application.App
	onWebViewIdleMinutesChanged       func(int)
	onPhraseInsertIntervalChanged     func(int)
	onCompletionInsertIntervalChanged func(int)
	onClipboardPasteMethodChanged     func(string)
	onPhraseInsertMethodChanged       func(string)
	onCompletionInsertMethodChanged   func(string)
}

func (s *SettingsService) GetCompletionInsertIntervalMs() (int, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, s.preferencesPath(), registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer key.Close()
	value, _, err := key.GetIntegerValue("CompletionInsertIntervalMs")
	if errors.Is(err, registry.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if value > maxInsertIntervalMs {
		return 0, nil
	}
	return int(value), nil
}

func (s *SettingsService) SetCompletionInsertIntervalMs(interval int) error {
	if interval < 0 || interval > maxInsertIntervalMs {
		return fmt.Errorf("输入间隔须为 0–%d 毫秒", maxInsertIntervalMs)
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, s.preferencesPath(), registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if err := key.SetDWordValue("CompletionInsertIntervalMs", uint32(interval)); err != nil {
		return err
	}
	if s.onCompletionInsertIntervalChanged != nil {
		s.onCompletionInsertIntervalChanged(interval)
	}
	return nil
}

func (s *SettingsService) GetPhraseInsertIntervalMs() (int, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, s.preferencesPath(), registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer key.Close()
	value, _, err := key.GetIntegerValue("PhraseInsertIntervalMs")
	if errors.Is(err, registry.ErrNotExist) {
		return 0, nil
	}
	if err != nil || value > maxInsertIntervalMs {
		return 0, err
	}
	return int(value), nil
}

func (s *SettingsService) SetPhraseInsertIntervalMs(interval int) error {
	if interval < 0 || interval > maxInsertIntervalMs {
		return fmt.Errorf("输入间隔须为 0–%d 毫秒", maxInsertIntervalMs)
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, s.preferencesPath(), registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if err := key.SetDWordValue("PhraseInsertIntervalMs", uint32(interval)); err != nil {
		return err
	}
	if s.onPhraseInsertIntervalChanged != nil {
		s.onPhraseInsertIntervalChanged(interval)
	}
	return nil
}

func (s *SettingsService) getInputMethod(name string, allowSend bool) (string, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, s.preferencesPath(), registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return inputMethodCtrlV, nil
	}
	if err != nil {
		return "", err
	}
	defer key.Close()
	value, _, err := key.GetStringValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return inputMethodCtrlV, nil
	}
	if err != nil {
		return "", err
	}
	if value == inputMethodCtrlV || value == inputMethodShiftInsert || (allowSend && value == inputMethodSendInput) {
		return value, nil
	}
	return inputMethodCtrlV, nil
}

func (s *SettingsService) setInputMethod(name, value string, allowSend bool, changed func(string)) error {
	if value != inputMethodCtrlV && value != inputMethodShiftInsert && !(allowSend && value == inputMethodSendInput) {
		return errors.New("不支持的输入方式")
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, s.preferencesPath(), registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if err := key.SetStringValue(name, value); err != nil {
		return err
	}
	if changed != nil {
		changed(value)
	}
	return nil
}

func (s *SettingsService) GetClipboardPasteMethod() (string, error) {
	return s.getInputMethod("ClipboardPasteMethod", false)
}

func (s *SettingsService) SetClipboardPasteMethod(value string) error {
	return s.setInputMethod("ClipboardPasteMethod", value, false, s.onClipboardPasteMethodChanged)
}

func (s *SettingsService) GetPhraseInsertMethod() (string, error) {
	return s.getInputMethod("PhraseInsertMethod", true)
}

func (s *SettingsService) SetPhraseInsertMethod(value string) error {
	return s.setInputMethod("PhraseInsertMethod", value, true, s.onPhraseInsertMethodChanged)
}

func (s *SettingsService) GetCompletionInsertMethod() (string, error) {
	return s.getInputMethod("CompletionInsertMethod", true)
}

func (s *SettingsService) SetCompletionInsertMethod(value string) error {
	return s.setInputMethod("CompletionInsertMethod", value, true, s.onCompletionInsertMethodChanged)
}

func (s *SettingsService) preferencesPath() string {
	if s.registryPath != "" {
		return s.registryPath
	}
	return `Software\quick-paste`
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

func (s *SettingsService) GetWebViewIdleMinutes() (int, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, s.preferencesPath(), registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return defaultWebViewIdleMinutes, nil
	}
	if err != nil {
		return 0, err
	}
	defer key.Close()
	value, _, err := key.GetIntegerValue("WebViewIdleMinutes")
	if errors.Is(err, registry.ErrNotExist) {
		return defaultWebViewIdleMinutes, nil
	}
	if err != nil {
		return 0, err
	}
	if value < 1 || value > maxWebViewIdleMinutes {
		return defaultWebViewIdleMinutes, nil
	}
	return int(value), nil
}

func (s *SettingsService) SetWebViewIdleMinutes(minutes int) error {
	if minutes < 1 || minutes > maxWebViewIdleMinutes {
		return fmt.Errorf("空闲回收时间须为 1–%d 分钟", maxWebViewIdleMinutes)
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, s.preferencesPath(), registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if err := key.SetDWordValue("WebViewIdleMinutes", uint32(minutes)); err != nil {
		return err
	}
	if s.onWebViewIdleMinutesChanged != nil {
		s.onWebViewIdleMinutesChanged(minutes)
	}
	return nil
}

func isHiddenStart(args []string) bool { return slices.Contains(args, "--hidden") }

func (s *SettingsService) GetAutostart() (bool, error) {
	return withTaskSchedulerRoot(func(root *ole.IDispatch) (bool, error) {
		return scheduledTaskEnabled(root, autostartTaskName)
	})
}

func scheduledTaskEnabled(root *ole.IDispatch, taskName string) (bool, error) {
	taskVariant, err := oleutil.CallMethod(root, "GetTask", taskName)
	if isTaskSchedulerFileNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("读取开机自启任务失败: %w", err)
	}
	task := taskVariant.ToIDispatch()
	if task == nil {
		return false, errors.New("读取开机自启任务失败: 任务对象无效")
	}
	defer task.Release()

	enabledVariant, err := oleutil.GetProperty(task, "Enabled")
	if err != nil {
		return false, fmt.Errorf("读取开机自启任务状态失败: %w", err)
	}
	defer enabledVariant.Clear()
	return enabledVariant.Val != 0, nil
}

func (s *SettingsService) SetAutostart(enabled bool) error {
	_, err := withTaskSchedulerRoot(func(root *ole.IDispatch) (struct{}, error) {
		if !enabled {
			result, err := oleutil.CallMethod(root, "DeleteTask", autostartTaskName, 0)
			if result != nil {
				result.Clear()
			}
			if isTaskSchedulerFileNotFound(err) {
				return struct{}{}, nil
			}
			if err != nil {
				return struct{}{}, fmt.Errorf("关闭开机自启失败: %w", err)
			}
			return struct{}{}, nil
		}

		executable, err := os.Executable()
		if err != nil {
			return struct{}{}, fmt.Errorf("读取程序路径失败: %w", err)
		}
		currentUser, err := user.Current()
		if err != nil {
			return struct{}{}, fmt.Errorf("读取当前用户失败: %w", err)
		}
		username := strings.TrimSpace(currentUser.Username)
		if username == "" {
			return struct{}{}, errors.New("读取当前用户失败: 用户名为空")
		}

		registeredVariant, err := oleutil.CallMethod(
			root,
			"RegisterTask",
			autostartTaskName,
			autostartTaskXML(executable, username),
			taskCreateOrUpdate,
			nil,
			nil,
			taskLogonNone,
			nil,
		)
		if err != nil {
			return struct{}{}, fmt.Errorf("开启开机自启失败: %w", err)
		}
		if registered := registeredVariant.ToIDispatch(); registered != nil {
			registered.Release()
		}
		return struct{}{}, nil
	})
	return err
}

func autostartTaskXML(executable, username string) string {
	escape := func(value string) string {
		var builder strings.Builder
		_ = xml.EscapeText(&builder, []byte(value))
		return builder.String()
	}
	executable = escape(executable)
	username = escape(username)
	return fmt.Sprintf(`<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><Description>Quick Paste 开机自启</Description></RegistrationInfo>
  <Triggers><LogonTrigger><Enabled>true</Enabled><UserId>%s</UserId></LogonTrigger></Triggers>
  <Principals><Principal id="Author"><UserId>%s</UserId><LogonType>InteractiveToken</LogonType><RunLevel>HighestAvailable</RunLevel></Principal></Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <Enabled>true</Enabled>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
  </Settings>
  <Actions Context="Author"><Exec><Command>%s</Command><Arguments>--hidden</Arguments></Exec></Actions>
</Task>`, username, username, executable)
}

func withTaskSchedulerRoot[T any](fn func(*ole.IDispatch) (T, error)) (T, error) {
	var zero T
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		return zero, fmt.Errorf("初始化计划任务服务失败: %w", err)
	}
	defer ole.CoUninitialize()

	unknown, err := oleutil.CreateObject("Schedule.Service")
	if err != nil {
		return zero, fmt.Errorf("连接计划任务服务失败: %w", err)
	}
	defer unknown.Release()
	service, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return zero, fmt.Errorf("连接计划任务服务失败: %w", err)
	}
	defer service.Release()

	connected, err := oleutil.CallMethod(service, "Connect")
	if connected != nil {
		connected.Clear()
	}
	if err != nil {
		return zero, fmt.Errorf("连接计划任务服务失败: %w", err)
	}
	rootVariant, err := oleutil.CallMethod(service, "GetFolder", `\`)
	if err != nil {
		return zero, fmt.Errorf("打开计划任务根目录失败: %w", err)
	}
	root := rootVariant.ToIDispatch()
	if root == nil {
		return zero, errors.New("打开计划任务根目录失败: 目录对象无效")
	}
	defer root.Release()
	return fn(root)
}

func isTaskSchedulerFileNotFound(err error) bool {
	if err == nil {
		return false
	}
	var oleErr *ole.OleError
	if !errors.As(err, &oleErr) {
		return false
	}
	if uint32(oleErr.Code()) == taskSchedulerFileNotFoundHResult {
		return true
	}
	switch subErr := oleErr.SubError().(type) {
	case ole.EXCEPINFO:
		return subErr.SCODE() == taskSchedulerFileNotFoundHResult
	case *ole.EXCEPINFO:
		return subErr != nil && subErr.SCODE() == taskSchedulerFileNotFoundHResult
	default:
		return false
	}
}
