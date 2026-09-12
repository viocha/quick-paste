//go:build windows

package main

import (
	"encoding/binary"
	"reflect"
	"testing"
)

func TestClipboardUTF16RoundTrip(t *testing.T) {
	for _, value := range []string{"hello", "中文\nemoji 😀", ""} {
		if got := decodeUTF16Clipboard(encodeUTF16Clipboard(value)); got != value {
			t.Fatalf("round trip %q = %q", value, got)
		}
	}
}

func TestDecodeHTMLClipboardText(t *testing.T) {
	data := []byte("Version:0.9\r\n<!--StartFragment--><pre><code><span>go build </span><span>-trimpath</span><span> </span><span>-ldflags</span><span>=</span><span>'-s -w'</span></code></pre><!--EndFragment-->\x00")
	want := "go build -trimpath -ldflags='-s -w'"
	if got := decodeHTMLClipboardText(data); got != want {
		t.Fatalf("decoded HTML = %q, want %q", got, want)
	}
}

func TestPrepareRichHTMLAddsPlainTextFallback(t *testing.T) {
	store := testClipboardStore(t)
	html := []byte("Version:0.9\r\n<!--StartFragment--><b>hello &amp; world</b><!--EndFragment-->\x00")
	item, err := store.Add(ClipboardItem{Kind: ClipboardRich, Preview: "富文本"}, map[uint32][]byte{storedHTML: html})
	if err != nil {
		t.Fatal(err)
	}
	service := &ClipboardService{store: store}
	formats, err := service.prepareItem(item.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeUTF16Clipboard(formats[cfUnicodeText]); got != "hello & world" {
		t.Fatalf("plain fallback = %q", got)
	}
	if string(formats[storedHTML]) != string(html) {
		t.Fatal("rich HTML payload was changed")
	}
}

func TestDecodeHDrop(t *testing.T) {
	data := make([]byte, 20)
	binary.LittleEndian.PutUint32(data[:4], 20)
	binary.LittleEndian.PutUint32(data[16:20], 1)
	for _, path := range []string{`C:\a.txt`, `D:\目录\b.png`} {
		data = append(data, encodeUTF16Clipboard(path)...)
	}
	data = append(data, 0, 0)
	want := []string{`C:\a.txt`, `D:\目录\b.png`}
	if got := decodeHDrop(data); !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %#v", got)
	}
}

func TestValidateClipboardLimits(t *testing.T) {
	if err := validateClipboardLimits(defaultClipboardLimits()); err != nil {
		t.Fatalf("defaults rejected: %v", err)
	}
	for _, limits := range []ClipboardLimits{
		{MaxItems: 99, MaxBytesMB: 64, TextItemMB: 2, ImageItemMB: 16},
		{MaxItems: 10000, MaxBytesMB: 15, TextItemMB: 2, ImageItemMB: 16},
		{MaxItems: 10000, MaxBytesMB: 64, TextItemMB: 65, ImageItemMB: 16},
		{MaxItems: 10000, MaxBytesMB: 64, TextItemMB: 2, ImageItemMB: 65},
	} {
		if err := validateClipboardLimits(limits); err == nil {
			t.Fatalf("invalid limits accepted: %#v", limits)
		}
	}
}
