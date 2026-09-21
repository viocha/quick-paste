package main

import (
	"embed"
	"log"
	"os"

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
	browser := &BrowserService{store: store}
	phraseService := NewPhraseService(store)
	clipboardStore, err := NewClipboardStore()
	if err != nil {
		log.Fatal(err)
	}
	clipboard := NewClipboardService(nil, clipboardStore, browser)
	browser.clipboard = clipboard
	var monitor *InputMonitor
	var manager *application.WebviewWindow
	app := application.New(application.Options{
		Name:        "Quick Paste",
		Description: "快捷粘贴",
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.quickpaste.desktop",
			ExitCode: 0,
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				if manager != nil && !isHiddenStart(data.Args) {
					manager.Show().Focus()
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

	manager = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "manager",
		Title:            "Quick Paste",
		Width:            1080,
		Height:           680,
		MinWidth:         820,
		MinHeight:        520,
		Hidden:           isHiddenStart(os.Args[1:]),
		BackgroundColour: application.NewRGB(255, 255, 255),
		URL:              "/",
	})
	browser.manager = manager

	// 关闭管理窗口只隐藏到托盘，不结束进程。
	manager.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		manager.Hide()
		event.Cancel()
	})

	tray := app.SystemTray.New()
	tray.SetIcon(appIcon)
	tray.SetTooltip("Quick Paste")
	trayMenu := app.NewMenu()
	trayMenu.Add("打开窗口").OnClick(func(*application.Context) {
		manager.Show().Focus()
	})
	trayMenu.AddSeparator()
	trayMenu.Add("退出").OnClick(func(*application.Context) {
		app.Quit()
	})
	tray.SetMenu(trayMenu)
	tray.OnClick(func() {
		manager.Show().Focus()
	})

	// 候选窗复用 React 主题，从创建起就禁止激活。
	popup := app.Window.NewWithOptions(application.WebviewWindowOptions{
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
			ExStyle:         wsExNoActivate | 0x00000080 | 0x00000008, // TOOLWINDOW | TOPMOST
		},
	})

	browser.window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "phrase-browser", Title: "快捷面板", Width: browserWidth, Height: browserHeight,
		MinWidth: 300, MinHeight: 260,
		Hidden: true, Frameless: true, DisableResize: false, AlwaysOnTop: true,
		BackgroundColour: application.NewRGB(255, 255, 255), URL: "/#browser",
		Windows: application.WindowsWindow{HiddenOnTaskbar: true, ExStyle: 0x80 | 0x08},
	})
	browser.detailWindow = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "phrase-detail", Title: "短语详情", Width: 300, Height: 160,
		Hidden: true, Frameless: true, DisableResize: true, AlwaysOnTop: true,
		BackgroundType: application.BackgroundTypeTransparent, BackgroundColour: application.NewRGBA(0, 0, 0, 0), URL: "/#detail",
		Windows: application.WindowsWindow{
			HiddenOnTaskbar:                   true,
			DisableFramelessWindowDecorations: true,
			WebView2CompositionHosting:        true,
			// ExStyle replaces Wails defaults, so retain NOREDIRECTIONBITMAP for alpha composition.
			ExStyle: wsExNoActivate | 0x80 | 0x08 | 0x00200000,
		},
	})
	monitor = NewInputMonitor(app, manager, popup, store)
	browser.monitor = monitor
	monitor.closeBrowser = browser.Hide
	monitor.browserMouseDown = browser.mouseDown
	monitor.takeOverWinV = browser.GetWinVTakeover
	monitor.openClipboard = func() { browser.toggle("clipboard") }
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		backup.start()
		if err := monitor.Start(); err != nil {
			log.Printf("启动全局输入监听失败: %v", err)
		}
		browser.start()
		clipboard.start()
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
