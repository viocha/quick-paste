//go:build windows

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	cfUnicodeText = 13
	cfDIB         = 8
	cfDIBV5       = 17
	cfHDrop       = 15
	gmemMoveable  = 0x0002
	storedHTML    = 0xFFFFFF01
	storedRTF     = 0xFFFFFF02
)

var (
	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procEmptyClipboard             = user32.NewProc("EmptyClipboard")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procSetClipboardData           = user32.NewProc("SetClipboardData")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procGetClipboardSequenceNumber = user32.NewProc("GetClipboardSequenceNumber")
	procRegisterClipboardFormatW   = user32.NewProc("RegisterClipboardFormatW")
	procGetWindowTextW             = user32.NewProc("GetWindowTextW")
	procGetClipboardOwner          = user32.NewProc("GetClipboardOwner")
	procGetWindowThreadProcessID   = user32.NewProc("GetWindowThreadProcessId")
	procGlobalAlloc                = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalAlloc")
	procGlobalFree                 = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalFree")
	procGlobalLock                 = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalLock")
	procGlobalUnlock               = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalUnlock")
	procGlobalSize                 = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalSize")
	procExtractIconExW             = windows.NewLazySystemDLL("shell32.dll").NewProc("ExtractIconExW")
	procCreateCompatibleDC         = windows.NewLazySystemDLL("gdi32.dll").NewProc("CreateCompatibleDC")
	procCreateDIBSection           = windows.NewLazySystemDLL("gdi32.dll").NewProc("CreateDIBSection")
	procSelectObject               = windows.NewLazySystemDLL("gdi32.dll").NewProc("SelectObject")
	procDeleteObject               = windows.NewLazySystemDLL("gdi32.dll").NewProc("DeleteObject")
	procDeleteDC                   = windows.NewLazySystemDLL("gdi32.dll").NewProc("DeleteDC")
	procDrawIconExW                = user32.NewProc("DrawIconEx")
	procDestroyIcon                = user32.NewProc("DestroyIcon")
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

type ClipboardService struct {
	app            *application.App
	store          *ClipboardStore
	browser        *BrowserService
	mu             sync.Mutex
	clipboardMu    sync.Mutex
	enabled        bool
	done           chan struct{}
	listenerThread uint32
	listenerWindow uintptr
	lastSeq        uint32
	suppressSeq    uint32
	htmlFormat     uint32
	rtfFormat      uint32
	deletedItem    *ClipboardItem
	deletedFormats map[uint32][]byte
	iconCache      map[string]string
}

func NewClipboardService(app *application.App, store *ClipboardStore, browser *BrowserService) *ClipboardService {
	return &ClipboardService{app: app, store: store, browser: browser}
}

func registerClipboardFormat(name string) uint32 {
	ptr, _ := windows.UTF16PtrFromString(name)
	value, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(ptr)))
	return uint32(value)
}

func (s *ClipboardService) start() {
	s.mu.Lock()
	if s.done != nil {
		s.mu.Unlock()
		return
	}
	s.htmlFormat = registerClipboardFormat("HTML Format")
	s.rtfFormat = registerClipboardFormat("Rich Text Format")
	s.enabled = s.readEnabled()
	_ = s.store.SetLimits(s.readLimits())
	s.lastSeq = clipboardSequence()
	done := make(chan struct{})
	s.done = done
	s.mu.Unlock()

	ready := make(chan error, 1)
	go s.runClipboardListener(done, ready)
	if err := <-ready; err != nil {
		log.Printf("启动剪贴板监听失败: %v", err)
		<-done
		s.mu.Lock()
		if s.done == done {
			s.done = nil
			s.listenerThread = 0
			s.listenerWindow = 0
		}
		s.mu.Unlock()
	}
}

func (s *ClipboardService) stopService() {
	s.mu.Lock()
	if s.done == nil {
		s.mu.Unlock()
		return
	}
	done := s.done
	threadID, hwnd := s.listenerThread, s.listenerWindow
	s.mu.Unlock()
	if !postClipboardListenerStop(threadID, hwnd) {
		log.Printf("停止剪贴板监听失败: 无法唤醒监听线程")
		return
	}
	<-done
	s.mu.Lock()
	if s.done == done {
		s.done = nil
		s.listenerThread = 0
		s.listenerWindow = 0
	}
	s.mu.Unlock()
}

func clipboardSequence() uint32 {
	value, _, _ := procGetClipboardSequenceNumber.Call()
	return uint32(value)
}

func (s *ClipboardService) handleClipboardUpdate() {
	seq := clipboardSequence()
	s.mu.Lock()
	if seq == 0 || seq == s.lastSeq {
		s.mu.Unlock()
		return
	}
	enabled, suppressed := s.enabled, seq == s.suppressSeq
	if !enabled || suppressed {
		s.lastSeq = seq
	}
	s.mu.Unlock()
	if !enabled || suppressed {
		return
	}
	source, executable := clipboardOwnerInfo()
	item, formats, err := s.capture(source, executable)
	if err != nil {
		log.Printf("读取剪贴板失败: %v", err)
		return
	}
	if clipboardSequence() != seq {
		return
	}
	s.mu.Lock()
	s.lastSeq = seq
	s.mu.Unlock()
	if _, err := s.store.Add(item, formats); err != nil {
		log.Printf("保存剪贴板历史失败: %v", err)
		return
	}
	if s.app != nil {
		s.app.Event.Emit("clipboard-changed")
	}
}

func (s *ClipboardService) capture(source, executable string) (ClipboardItem, map[uint32][]byte, error) {
	s.clipboardMu.Lock()
	formats, err := s.captureFormats()
	s.clipboardMu.Unlock()
	if err != nil {
		return ClipboardItem{}, nil, err
	}
	item, err := clipboardItemFromFormats(formats)
	if err != nil {
		return ClipboardItem{}, nil, err
	}
	item.Source, item.SourceIcon = source, s.applicationIcon(executable)
	return item, formats, nil
}

func (s *ClipboardService) captureFormats() (map[uint32][]byte, error) {
	var last error
	for i := 0; i < 5; i++ {
		if ok, _, err := procOpenClipboard.Call(0); ok != 0 {
			formats, readErr := s.readOpenClipboardFormats()
			procCloseClipboard.Call()
			return formats, readErr
		} else {
			last = err
		}
		time.Sleep(time.Duration(i+1) * 15 * time.Millisecond)
	}
	return nil, last
}

func (s *ClipboardService) readOpenClipboardFormats() (map[uint32][]byte, error) {
	formats := make(map[uint32][]byte)
	imageFormat := uint32(0)
	if clipboardFormatAvailable(cfDIBV5) {
		imageFormat = cfDIBV5
	} else if clipboardFormatAvailable(cfDIB) {
		imageFormat = cfDIB
	}
	for _, candidate := range []struct{ format, stored uint32 }{{cfHDrop, cfHDrop}, {imageFormat, imageFormat}, {s.htmlFormat, storedHTML}, {s.rtfFormat, storedRTF}, {cfUnicodeText, cfUnicodeText}} {
		if candidate.format == 0 || !clipboardFormatAvailable(candidate.format) {
			continue
		}
		data, err := readClipboardFormat(candidate.format)
		if err == nil && len(data) > 0 {
			formats[candidate.stored] = data
		}
	}
	if len(formats) == 0 {
		return nil, errors.New("没有支持的剪贴板格式")
	}
	return formats, nil
}

func clipboardItemFromFormats(formats map[uint32][]byte) (ClipboardItem, error) {
	if len(formats) == 0 {
		return ClipboardItem{}, errors.New("没有支持的剪贴板格式")
	}
	text := richPlainText(formats)
	item := ClipboardItem{Text: text}
	switch {
	case len(formats[cfHDrop]) > 0:
		item.Kind = ClipboardFiles
		item.Files = decodeHDrop(formats[cfHDrop])
		item.Preview = strings.Join(item.Files, "\n")
	case len(formats[cfDIBV5]) > 0 || len(formats[cfDIB]) > 0:
		item.Kind = ClipboardImage
		data := formats[cfDIBV5]
		if len(data) == 0 {
			data = formats[cfDIB]
		}
		item.Preview = dibDescription(data)
	case len(formats[storedHTML]) > 0 || len(formats[storedRTF]) > 0:
		item.Kind = ClipboardRich
		item.Preview = compactPreview(text, "富文本")
	default:
		item.Kind = ClipboardText
		item.Preview = compactPreview(text, "文本")
	}
	return item, nil
}

func clipboardFormatAvailable(format uint32) bool {
	ok, _, _ := procIsClipboardFormatAvailable.Call(uintptr(format))
	return ok != 0
}

func readClipboardFormat(format uint32) ([]byte, error) {
	handle, _, err := procGetClipboardData.Call(uintptr(format))
	if handle == 0 {
		return nil, err
	}
	size, _, _ := procGlobalSize.Call(handle)
	if size == 0 || size > 256*1024*1024 {
		return nil, errors.New("剪贴板数据大小无效")
	}
	ptr, _, err := procGlobalLock.Call(handle)
	if ptr == 0 {
		return nil, err
	}
	defer procGlobalUnlock.Call(handle)
	data := make([]byte, int(size))
	copy(data, unsafe.Slice((*byte)(unsafe.Pointer(ptr)), int(size)))
	return data, nil
}

func decodeUTF16Clipboard(data []byte) string {
	if len(data) < 2 {
		return ""
	}
	values := make([]uint16, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		value := binary.LittleEndian.Uint16(data[i:])
		if value == 0 {
			break
		}
		values = append(values, value)
	}
	return string(utf16.Decode(values))
}

func richPlainText(formats map[uint32][]byte) string {
	if text := decodeUTF16Clipboard(formats[cfUnicodeText]); text != "" {
		return text
	}
	if data := formats[storedHTML]; len(data) > 0 {
		return decodeHTMLClipboardText(data)
	}
	return ""
}

func decodeHDrop(data []byte) []string {
	if len(data) < 20 || binary.LittleEndian.Uint32(data[16:20]) == 0 {
		return nil
	}
	offset := int(binary.LittleEndian.Uint32(data[:4]))
	if offset < 20 || offset >= len(data) {
		return nil
	}
	all := decodeUTF16ClipboardList(data[offset:])
	return all
}

func decodeUTF16ClipboardList(data []byte) []string {
	var result []string
	start := 0
	for i := 0; i+1 < len(data); i += 2 {
		if binary.LittleEndian.Uint16(data[i:]) != 0 {
			continue
		}
		if i == start {
			break
		}
		result = append(result, decodeUTF16Clipboard(data[start:i+2]))
		start = i + 2
	}
	return result
}

func dibDescription(data []byte) string {
	if len(data) >= 12 {
		width := int32(binary.LittleEndian.Uint32(data[4:8]))
		height := int32(binary.LittleEndian.Uint32(data[8:12]))
		if height < 0 {
			height = -height
		}
		if width > 0 && height > 0 {
			return fmt.Sprintf("图片 %d × %d", width, height)
		}
	}
	return "图片"
}

func compactPreview(text, fallback string) string {
	text = strings.TrimSpace(strings.Join(strings.Fields(text), " "))
	if text == "" {
		return fallback
	}
	runes := []rune(text)
	if len(runes) > 240 {
		text = string(runes[:240]) + "…"
	}
	return text
}

func foregroundTitle() string {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return ""
	}
	buffer := make([]uint16, 260)
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buffer[:n])
}

func clipboardOwnerInfo() (string, string) {
	hwnd, _, _ := procGetClipboardOwner.Call()
	if hwnd == 0 {
		return foregroundTitle(), ""
	}
	var processID uint32
	procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&processID)))
	if processID != 0 {
		if process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, processID); err == nil {
			defer windows.CloseHandle(process)
			buffer := make([]uint16, 1024)
			size := uint32(len(buffer))
			if windows.QueryFullProcessImageName(process, 0, &buffer[0], &size) == nil {
				name := strings.TrimSuffix(filepath.Base(windows.UTF16ToString(buffer[:size])), filepath.Ext(windows.UTF16ToString(buffer[:size])))
				if name != "" {
					return name, windows.UTF16ToString(buffer[:size])
				}
			}
		}
	}
	buffer := make([]uint16, 260)
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if n == 0 {
		return foregroundTitle(), ""
	}
	return windows.UTF16ToString(buffer[:n]), ""
}

func (s *ClipboardService) applicationIcon(executable string) string {
	if executable == "" {
		return ""
	}
	s.mu.Lock()
	if s.iconCache == nil {
		s.iconCache = make(map[string]string)
	}
	if icon, ok := s.iconCache[executable]; ok {
		s.mu.Unlock()
		return icon
	}
	s.mu.Unlock()
	icon := extractApplicationIcon(executable)
	s.mu.Lock()
	s.iconCache[executable] = icon
	s.mu.Unlock()
	return icon
}

func extractApplicationIcon(executable string) string {
	path, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return ""
	}
	var icon uintptr
	count, _, _ := procExtractIconExW.Call(uintptr(unsafe.Pointer(path)), 0, 0, uintptr(unsafe.Pointer(&icon)), 1)
	if count == 0 || icon == 0 {
		return ""
	}
	defer procDestroyIcon.Call(icon)
	dc, _, _ := procCreateCompatibleDC.Call(0)
	if dc == 0 {
		return ""
	}
	defer procDeleteDC.Call(dc)
	info := bitmapInfo{Header: bitmapInfoHeader{Size: uint32(unsafe.Sizeof(bitmapInfoHeader{})), Width: 32, Height: -32, Planes: 1, BitCount: 32}}
	var bits uintptr
	bitmap, _, _ := procCreateDIBSection.Call(dc, uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bitmap == 0 || bits == 0 {
		return ""
	}
	defer procDeleteObject.Call(bitmap)
	previous, _, _ := procSelectObject.Call(dc, bitmap)
	if previous == 0 {
		return ""
	}
	defer procSelectObject.Call(dc, previous)
	if ok, _, _ := procDrawIconExW.Call(dc, 0, 0, icon, 32, 32, 0, 0, 3); ok == 0 {
		return ""
	}
	raw := unsafe.Slice((*byte)(unsafe.Pointer(bits)), 32*32*4)
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	hasAlpha := false
	for i := 0; i < 32*32; i++ {
		if raw[i*4+3] != 0 {
			hasAlpha = true
			break
		}
	}
	for i := 0; i < 32*32; i++ {
		alpha := raw[i*4+3]
		if !hasAlpha && (raw[i*4] != 0 || raw[i*4+1] != 0 || raw[i*4+2] != 0) {
			alpha = 255
		}
		img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = raw[i*4+2], raw[i*4+1], raw[i*4], alpha
	}
	var output bytes.Buffer
	if png.Encode(&output, img) != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(output.Bytes())
}

func (s *ClipboardService) readEnabled() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, hotkeyRegistryPath, registry.QUERY_VALUE)
	if err != nil {
		return true
	}
	defer key.Close()
	value, _, err := key.GetIntegerValue("ClipboardCaptureEnabled")
	if err != nil {
		return true
	}
	return value != 0
}

func (s *ClipboardService) readLimits() ClipboardLimits {
	limits := defaultClipboardLimits()
	key, err := registry.OpenKey(registry.CURRENT_USER, hotkeyRegistryPath, registry.QUERY_VALUE)
	if err != nil {
		return limits
	}
	defer key.Close()
	for name, target := range map[string]*int{"ClipboardMaxItems": &limits.MaxItems, "ClipboardMaxBytesMB": &limits.MaxBytesMB, "ClipboardTextItemMB": &limits.TextItemMB, "ClipboardImageItemMB": &limits.ImageItemMB} {
		if value, _, err := key.GetIntegerValue(name); err == nil && value > 0 {
			*target = int(value)
		}
	}
	if validateClipboardLimits(limits) != nil {
		return defaultClipboardLimits()
	}
	return limits
}

func validateClipboardLimits(limits ClipboardLimits) error {
	if limits.MaxItems < 100 || limits.MaxItems > 100000 {
		return errors.New("最大记录数须为 100–100000")
	}
	if limits.MaxBytesMB < 16 || limits.MaxBytesMB > 4096 {
		return errors.New("总占用上限须为 16–4096 MB")
	}
	if limits.TextItemMB < 1 || limits.TextItemMB > 64 {
		return errors.New("文本单项上限须为 1–64 MB")
	}
	if limits.ImageItemMB < 1 || limits.ImageItemMB > 256 {
		return errors.New("图片单项上限须为 1–256 MB")
	}
	if limits.TextItemMB > limits.MaxBytesMB || limits.ImageItemMB > limits.MaxBytesMB {
		return errors.New("单项上限不能超过总占用上限")
	}
	return nil
}

func (s *ClipboardService) GetLimits() ClipboardLimits { return s.store.Limits() }
func (s *ClipboardService) GetStats() ClipboardStats   { return s.store.Stats() }
func (s *ClipboardService) SetLimits(limits ClipboardLimits) error {
	if err := validateClipboardLimits(limits); err != nil {
		return err
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, hotkeyRegistryPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	for name, value := range map[string]int{"ClipboardMaxItems": limits.MaxItems, "ClipboardMaxBytesMB": limits.MaxBytesMB, "ClipboardTextItemMB": limits.TextItemMB, "ClipboardImageItemMB": limits.ImageItemMB} {
		if err := key.SetDWordValue(name, uint32(value)); err != nil {
			return err
		}
	}
	return s.store.SetLimits(limits)
}

func (s *ClipboardService) GetEnabled() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.enabled }

func (s *ClipboardService) SetEnabled(enabled bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, hotkeyRegistryPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	var value uint32
	if enabled {
		value = 1
	}
	if err := key.SetDWordValue("ClipboardCaptureEnabled", value); err != nil {
		return err
	}
	s.mu.Lock()
	s.enabled = enabled
	s.lastSeq = clipboardSequence()
	s.mu.Unlock()
	if s.app != nil {
		s.app.Event.Emit("clipboard-state-changed", enabled)
	}
	return nil
}

func (s *ClipboardService) List(query string) []ClipboardItem { return s.store.List(query) }

func (s *ClipboardService) GetText(id string) (string, error) {
	item, formats, err := s.store.Payload(id)
	if err != nil {
		return "", err
	}
	if text := richPlainText(formats); text != "" {
		return text, nil
	}
	if item.Kind == ClipboardFiles {
		return strings.Join(item.Files, "\n"), nil
	}
	return "", errors.New("此项目没有纯文本内容")
}

func (s *ClipboardService) GetPreviewData(id string) (string, error) {
	return s.previewData(id)
}

func (s *ClipboardService) previewData(id string) (string, error) {
	item, formats, err := s.store.Payload(id)
	if err != nil {
		return "", err
	}
	if item.Kind != ClipboardImage {
		return "", nil
	}
	dib := formats[cfDIBV5]
	if len(dib) == 0 {
		dib = formats[cfDIB]
	}
	if len(dib) < 40 {
		return "", errors.New("图片格式已损坏")
	}
	headerSize := binary.LittleEndian.Uint32(dib[:4])
	if headerSize < 40 || int(headerSize) > len(dib) {
		return "", errors.New("图片格式已损坏")
	}
	bitCount := binary.LittleEndian.Uint16(dib[14:16])
	compression := binary.LittleEndian.Uint32(dib[16:20])
	colors := binary.LittleEndian.Uint32(dib[32:36])
	if colors == 0 && bitCount <= 8 {
		colors = 1 << bitCount
	}
	extra := colors * 4
	if compression == 3 && headerSize == 40 {
		extra += 12
	}
	pixelOffset := uint32(14) + headerSize + extra
	bmp := make([]byte, 14+len(dib))
	copy(bmp[14:], dib)
	copy(bmp[:2], []byte{'B', 'M'})
	binary.LittleEndian.PutUint32(bmp[2:6], uint32(len(bmp)))
	binary.LittleEndian.PutUint32(bmp[10:14], pixelOffset)
	return "data:image/bmp;base64," + base64.StdEncoding.EncodeToString(bmp), nil
}
func (s *ClipboardService) TogglePinned(id string) error {
	err := s.store.TogglePinned(id)
	if err == nil && s.app != nil {
		s.app.Event.Emit("clipboard-changed")
	}
	return err
}
func (s *ClipboardService) Delete(id string) error {
	item, formats, err := s.store.Payload(id)
	if err != nil {
		return err
	}
	err = s.store.Delete(id)
	if err == nil {
		s.mu.Lock()
		s.deletedItem, s.deletedFormats = &item, formats
		s.mu.Unlock()
		go func(id string) {
			time.Sleep(10 * time.Second)
			s.mu.Lock()
			if s.deletedItem != nil && s.deletedItem.ID == id {
				s.deletedItem, s.deletedFormats = nil, nil
			}
			s.mu.Unlock()
		}(item.ID)
	}
	if err == nil && s.app != nil {
		s.app.Event.Emit("clipboard-changed")
	}
	return err
}
func (s *ClipboardService) UndoDelete() error {
	s.mu.Lock()
	item, formats := s.deletedItem, s.deletedFormats
	s.deletedItem, s.deletedFormats = nil, nil
	s.mu.Unlock()
	if item == nil {
		return errors.New("没有可撤销的删除")
	}
	if _, err := s.store.Add(*item, formats); err != nil {
		return err
	}
	if s.app != nil {
		s.app.Event.Emit("clipboard-changed")
	}
	return nil
}
func (s *ClipboardService) Clear(includePinned bool) error {
	err := s.store.Clear(includePinned)
	if err == nil && s.app != nil {
		s.app.Event.Emit("clipboard-changed")
	}
	return err
}

func (s *ClipboardService) Copy(id string, plain bool) error {
	if err := s.writeItem(id, plain); err != nil {
		return err
	}
	return s.store.MarkUsed(id)
}

func (s *ClipboardService) writeItem(id string, plain bool) error {
	formats, err := s.prepareItem(id, plain)
	if err != nil {
		return err
	}
	return s.write(formats)
}

func (s *ClipboardService) prepareItem(id string, plain bool) (map[uint32][]byte, error) {
	item, formats, err := s.store.Payload(id)
	if err != nil {
		return nil, err
	}
	if item.Kind == ClipboardFiles {
		for _, path := range item.Files {
			if _, err := os.Stat(path); err != nil {
				return nil, fmt.Errorf("文件已不存在：%s", path)
			}
		}
	}
	text := richPlainText(formats)
	if text == "" {
		text = item.Text
	}
	if item.Kind == ClipboardRich && text != "" && len(formats[cfUnicodeText]) == 0 {
		withText := make(map[uint32][]byte, len(formats)+1)
		for format, data := range formats {
			withText[format] = data
		}
		withText[cfUnicodeText] = encodeUTF16Clipboard(text)
		formats = withText
	}
	if plain {
		if text == "" {
			return nil, errors.New("此项目没有纯文本内容")
		}
		formats = map[uint32][]byte{cfUnicodeText: encodeUTF16Clipboard(text)}
	}
	return formats, nil
}

func (s *ClipboardService) Paste(id string, plain bool) error {
	formats, err := s.prepareItem(id, plain)
	if err != nil {
		return err
	}
	if err := s.browser.pasteClipboard(func() error { return s.write(formats) }); err != nil {
		return err
	}
	return s.store.MarkUsed(id)
}

func encodeUTF16Clipboard(text string) []byte {
	values := utf16.Encode([]rune(text))
	values = append(values, 0)
	data := make([]byte, len(values)*2)
	for i, value := range values {
		binary.LittleEndian.PutUint16(data[i*2:], value)
	}
	return data
}

func (s *ClipboardService) write(formats map[uint32][]byte) error {
	prepared, err := s.prepareClipboardFormats(formats)
	if err != nil {
		return err
	}
	defer func() {
		for _, item := range prepared {
			if item.handle != 0 {
				procGlobalFree.Call(item.handle)
			}
		}
	}()

	s.clipboardMu.Lock()
	defer s.clipboardMu.Unlock()
	var openErr error
	opened := false
	for i := 0; i < 5; i++ {
		if ok, _, err := procOpenClipboard.Call(0); ok != 0 {
			opened = true
			break
		} else {
			openErr = err
		}
		time.Sleep(time.Duration(i+1) * 15 * time.Millisecond)
	}
	if !opened {
		return openErr
	}
	if ok, _, err := procEmptyClipboard.Call(); ok == 0 {
		procCloseClipboard.Call()
		return err
	}
	for i := range prepared {
		item := &prepared[i]
		if result, _, err := procSetClipboardData.Call(uintptr(item.format), item.handle); result == 0 {
			procCloseClipboard.Call()
			return err
		}
		// SetClipboardData 成功后句柄所有权移交给系统。
		item.handle = 0
	}
	procCloseClipboard.Call()
	s.mu.Lock()
	s.suppressSeq = clipboardSequence()
	s.lastSeq = s.suppressSeq
	s.mu.Unlock()
	return nil
}

type preparedClipboardFormat struct {
	format uint32
	handle uintptr
}

func (s *ClipboardService) prepareClipboardFormats(formats map[uint32][]byte) ([]preparedClipboardFormat, error) {
	prepared := make([]preparedClipboardFormat, 0, len(formats))
	for format, data := range formats {
		if format == storedHTML {
			format = s.htmlFormat
		}
		if format == storedRTF {
			format = s.rtfFormat
		}
		if format == 0 || len(data) == 0 {
			continue
		}
		handle, _, err := procGlobalAlloc.Call(gmemMoveable, uintptr(len(data)))
		if handle == 0 {
			freePreparedClipboardFormats(prepared)
			return nil, err
		}
		ptr, _, err := procGlobalLock.Call(handle)
		if ptr == 0 {
			procGlobalFree.Call(handle)
			freePreparedClipboardFormats(prepared)
			return nil, err
		}
		copy(unsafe.Slice((*byte)(unsafe.Pointer(ptr)), len(data)), data)
		procGlobalUnlock.Call(handle)
		prepared = append(prepared, preparedClipboardFormat{format: format, handle: handle})
	}
	if len(prepared) == 0 {
		return nil, errors.New("没有可写入的剪贴板格式")
	}
	return prepared, nil
}

func freePreparedClipboardFormats(prepared []preparedClipboardFormat) {
	for _, item := range prepared {
		if item.handle != 0 {
			procGlobalFree.Call(item.handle)
		}
	}
}
