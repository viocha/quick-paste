package main

import (
	"embed"
	"log"
	"os"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/windows/icon.png
var appIcon []byte

func main() {
	store, err := NewPhraseStore()
	if err != nil {
		log.Fatal(err)
	}

	backup, err := NewBackupService(store)
	if err != nil {
		log.Fatal(err)
	}

	settings := &SettingsService{}
	inputInterval, err := settings.GetInputIntervalMs()
	if err != nil {
		log.Printf("读取输入间隔失败，使用默认值: %v", err)
	}
	clipboardMethod, err := settings.GetClipboardPasteMethod()
	if err != nil {
		log.Printf("读取剪贴板粘贴方式失败，使用默认值: %v", err)
		clipboardMethod = inputMethodCtrlV
	}
	phraseMethod, err := settings.GetPhraseInsertMethod()
	if err != nil {
		log.Printf("读取短语插入方式失败，使用默认值: %v", err)
		phraseMethod = inputMethodCtrlV
	}
	completionMethod, err := settings.GetCompletionInsertMethod()
	if err != nil {
		log.Printf("读取短语补全方式失败，使用默认值: %v", err)
		completionMethod = inputMethodCtrlV
	}
	inputIntervalMillis.Store(int64(inputInterval))
	clipboardPasteMethodRuntime.Store(runtimeInputMethod(clipboardMethod))
	phraseInsertMethodRuntime.Store(runtimeInputMethod(phraseMethod))
	completionInsertMethodRuntime.Store(runtimeInputMethod(completionMethod))
	settings.onInputIntervalChanged = func(interval int) { inputIntervalMillis.Store(int64(interval)) }
	settings.onClipboardPasteMethodChanged = func(value string) { clipboardPasteMethodRuntime.Store(runtimeInputMethod(value)) }
	settings.onPhraseInsertMethodChanged = func(value string) { phraseInsertMethodRuntime.Store(runtimeInputMethod(value)) }
	settings.onCompletionInsertMethodChanged = func(value string) { completionInsertMethodRuntime.Store(runtimeInputMethod(value)) }
	browser := &BrowserService{store: store}
	phraseService := NewPhraseService(store)
	clipboardStore, err := NewClipboardStore()
	if err != nil {
		log.Fatal(err)
	}
	clipboard := NewClipboardService(nil, clipboardStore, browser)
	browser.clipboard = clipboard
	var monitor *InputMonitor
	var openManager func()
	app := application.New(application.Options{
		Name:        "Quick Paste",
		Description: "Quick Paste",
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.quickpaste.desktop",
			ExitCode: 0,
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				if !isHiddenStart(data.Args) && openManager != nil {
					openManager()
				}
			},
		},
		Services: []application.Service{
			application.NewService(phraseService),
			application.NewService(settings),
			application.NewService(backup),
			application.NewService(browser),
			application.NewService(clipboard),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		OnShutdown: func() {
			backup.stop()
			browser.stop()
			clipboard.stopService()
			if monitor != nil {
				monitor.Stop()
			}
		},
	})
	settings.app = app
	phraseService.changed = func() {
		app.Event.Emit("phrases-changed")
		if monitor != nil && monitor.resetFn != nil {
			monitor.resetFn()
		}
	}
	clipboard.app = app
	app.SetIcon(appIcon)

	managerSlot := &lazyWindow{}
	managerSlot.create = func() *application.WebviewWindow {
		return app.Window.NewWithOptions(application.WebviewWindowOptions{
			Name:             "manager",
			Title:            "Quick Paste",
			Width:            1080,
			Height:           680,
			MinWidth:         820,
			MinHeight:        520,
			BackgroundColour: application.NewRGB(255, 255, 255),
			URL:              "/",
		})
	}
	openManager = func() {
		managerSlot.Ensure().Show().Focus()
	}
	browser.openManager = openManager

	tray := app.SystemTray.New()
	tray.SetIcon(appIcon)
	tray.SetTooltip("Quick Paste")
	trayMenu := app.NewMenu()
	trayMenu.Add("打开窗口").OnClick(func(*application.Context) {
		openManager()
	})
	trayMenu.AddSeparator()
	trayMenu.Add("退出").OnClick(func(*application.Context) {
		app.Quit()
	})
	tray.SetMenu(trayMenu)
	tray.OnClick(func() {
		openManager()
	})

	idleMinutes, err := settings.GetWebViewIdleMinutes()
	if err != nil {
		log.Printf("读取 WebView 空闲回收时间失败，使用默认值: %v", err)
		idleMinutes = defaultWebViewIdleMinutes
	}
	idleWindowRetention := time.Duration(idleMinutes) * time.Minute
	popupSlot := &lazyWindow{idleDelay: idleWindowRetention}
	popupSlot.create = func() *application.WebviewWindow {
		return app.Window.NewWithOptions(application.WebviewWindowOptions{
			Name:             "autocomplete",
			Title:            "Quick Paste Suggestions",
			Width:            360,
			Height:           260,
			Hidden:           true,
			Frameless:        true,
			DisableResize:    true,
			AlwaysOnTop:      true,
			BackgroundColour: application.NewRGB(255, 255, 255),
			URL:              "/#popup",
			Windows: application.WindowsWindow{
				HiddenOnTaskbar: true,
				ExStyle:         wsExNoActivate | 0x00000080 | 0x00000008,
			},
		})
	}

	browserSlot := &lazyWindow{idleDelay: idleWindowRetention}
	browserSlot.create = func() *application.WebviewWindow {
		return app.Window.NewWithOptions(application.WebviewWindowOptions{
			Name: "phrase-browser", Title: "快捷面板", Width: browserWidth, Height: browserHeight,
			MinWidth: 300, MinHeight: 260,
			Hidden: true, Frameless: true, DisableResize: false, AlwaysOnTop: true,
			BackgroundColour: application.NewRGB(255, 255, 255), URL: "/#browser",
			Windows: application.WindowsWindow{HiddenOnTaskbar: true, ExStyle: 0x80 | 0x08},
		})
	}
	detailSlot := &lazyWindow{idleDelay: idleWindowRetention}
	detailSlot.create = func() *application.WebviewWindow {
		return app.Window.NewWithOptions(application.WebviewWindowOptions{
			Name: "phrase-detail", Title: "短语详情", Width: 300, Height: 160,
			Hidden: true, Frameless: true, DisableResize: true, AlwaysOnTop: true,
			BackgroundType: application.BackgroundTypeTransparent, BackgroundColour: application.NewRGBA(0, 0, 0, 0), URL: "/#detail",
			Windows: application.WindowsWindow{
				HiddenOnTaskbar:                   true,
				DisableFramelessWindowDecorations: true,
				WebView2CompositionHosting:        true,
				ExStyle:                           wsExNoActivate | 0x80 | 0x08 | 0x00200000,
			},
		})
	}
	browser.ensureWindows = func() (*application.WebviewWindow, *application.WebviewWindow) {
		return browserSlot.Ensure(), detailSlot.Ensure()
	}
	browser.releaseWindows = func() {
		browserSlot.Release()
		detailSlot.Release()
	}
	settings.onWebViewIdleMinutesChanged = func(minutes int) {
		delay := time.Duration(minutes) * time.Minute
		popupSlot.SetIdleDelay(delay)
		browserSlot.SetIdleDelay(delay)
		detailSlot.SetIdleDelay(delay)
	}

	monitor = NewInputMonitor(app, store)
	monitor.managerWindow = managerSlot.Current
	monitor.ensurePopup = popupSlot.Ensure
	monitor.popupWindow = popupSlot.Current
	monitor.releasePopup = popupSlot.Release
	browser.monitor = monitor
	monitor.closeBrowser = browser.Hide
	monitor.browserMouseDown = browser.mouseDown
	monitor.browserKey = browser.routeKey
	monitor.takeOverWinV = browser.GetWinVTakeover
	monitor.openClipboard = func() { browser.toggle("clipboard") }
	monitor.pasteClipboardText = clipboard.pasteTextWithRemoval
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		backup.start()
		if err := monitor.Start(); err != nil {
			log.Printf("启动全局输入监听失败: %v", err)
		}
		browser.start()
		clipboard.start()
		if !isHiddenStart(os.Args[1:]) {
			openManager()
		}
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
