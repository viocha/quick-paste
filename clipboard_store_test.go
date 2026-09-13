package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testClipboardStore(t *testing.T) *ClipboardStore {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "items"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &ClipboardStore{dir: dir, index: filepath.Join(dir, "index.json"), maxItems: 10000, maxBytes: 64 * 1024 * 1024, maxTextBytes: 2 * 1024 * 1024, maxImageBytes: 16 * 1024 * 1024}
}

func TestClipboardStoreDeduplicatesAndPreservesPin(t *testing.T) {
	s := testClipboardStore(t)
	first, err := s.Add(ClipboardItem{Kind: ClipboardText, Preview: "first", Text: "same", Source: "A"}, map[uint32][]byte{13: {1, 2, 3}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.TogglePinned(first.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	second, err := s.Add(ClipboardItem{Kind: ClipboardText, Preview: "second", Text: "same", Source: "B"}, map[uint32][]byte{13: {1, 2, 3}})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || !second.Pinned {
		t.Fatalf("duplicate was not merged with pin: %#v", second)
	}
	items := s.List("second")
	if len(items) != 1 || items[0].Source != "B" {
		t.Fatalf("merged metadata not searchable: %#v", items)
	}
	_, formats, err := s.Payload(first.ID)
	if err != nil || len(formats[13]) != 3 {
		t.Fatalf("payload mismatch: %v %#v", err, formats)
	}
}

func TestClipboardStoreListPageIsLightweightAndPaged(t *testing.T) {
	s := testClipboardStore(t)
	for index, item := range []ClipboardItem{
		{Kind: ClipboardText, Preview: "one", Text: "body one", Source: "Code", SourceIcon: "data:image/png;base64,icon"},
		{Kind: ClipboardFiles, Preview: "two", Files: []string{`C:\\two.txt`}, Source: "Code", SourceIcon: "data:image/png;base64,icon"},
		{Kind: ClipboardImage, Preview: "图片 10 × 10", Source: "Edge", SourceIcon: "data:image/png;base64,edge"},
	} {
		if _, err := s.Add(item, map[uint32][]byte{13: {byte(index + 1)}}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}

	page := s.ListPage("", 0, 2)
	if page.Total != 3 || len(page.Items) != 2 {
		t.Fatalf("page = %#v", page)
	}
	if page.Items[0].Preview != "图片 10 × 10" || page.Items[1].Preview != "two" {
		t.Fatalf("wrong page order: %#v", page.Items)
	}
	if !page.Items[1].HasText {
		t.Fatal("file item should expose plain-text capability")
	}
	if page.Icons["Edge"] != "data:image/png;base64,edge" || page.Icons["Code"] != "data:image/png;base64,icon" {
		t.Fatalf("icons = %#v", page.Icons)
	}

	page = s.ListPage("body one", 0, 100)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Preview != "one" || !page.Items[0].HasText {
		t.Fatalf("search page = %#v", page)
	}
}

func TestClipboardStoreSortsAndClearsOrdinaryItems(t *testing.T) {
	s := testClipboardStore(t)
	a, _ := s.Add(ClipboardItem{Kind: ClipboardText, Preview: "a"}, map[uint32][]byte{13: {1}})
	time.Sleep(time.Millisecond)
	b, _ := s.Add(ClipboardItem{Kind: ClipboardText, Preview: "b"}, map[uint32][]byte{13: {2}})
	if err := s.MarkUsed(a.ID); err != nil {
		t.Fatal(err)
	}
	if got := s.List(""); len(got) != 2 || got[0].ID != a.ID {
		t.Fatalf("recently used item not promoted: %#v", got)
	}
	if err := s.TogglePinned(b.ID); err != nil {
		t.Fatal(err)
	}
	if got := s.List(""); got[0].ID != b.ID {
		t.Fatal("pinned item not first")
	}
	if err := s.Clear(false); err != nil {
		t.Fatal(err)
	}
	if got := s.List(""); len(got) != 1 || got[0].ID != b.ID {
		t.Fatalf("ordinary clear removed wrong items: %#v", got)
	}
	if err := s.Clear(true); err != nil {
		t.Fatal(err)
	}
	if len(s.List("")) != 0 {
		t.Fatal("clear all left items")
	}
}

func TestClipboardStorePrunesOldestToCapacity(t *testing.T) {
	s := testClipboardStore(t)
	s.maxItems = 2
	first, _ := s.Add(ClipboardItem{Kind: ClipboardText, Preview: "first"}, map[uint32][]byte{13: {1}})
	time.Sleep(time.Millisecond)
	_, _ = s.Add(ClipboardItem{Kind: ClipboardText, Preview: "second"}, map[uint32][]byte{13: {2}})
	time.Sleep(time.Millisecond)
	_, _ = s.Add(ClipboardItem{Kind: ClipboardText, Preview: "third"}, map[uint32][]byte{13: {3}})
	if got := s.List(""); len(got) != 2 || got[0].Preview != "third" || got[1].Preview != "second" {
		t.Fatalf("wrong capacity result: %#v", got)
	}
	if _, _, err := s.Payload(first.ID); err == nil {
		t.Fatal("pruned payload still exists")
	}
}

func TestDefaultClipboardLimits(t *testing.T) {
	got := defaultClipboardLimits()
	want := ClipboardLimits{MaxItems: 10000, MaxBytesMB: 64, TextItemMB: 2, ImageItemMB: 16}
	if got != want {
		t.Fatalf("defaults = %#v, want %#v", got, want)
	}
}

func TestClipboardStoreAppliesPerTypeItemLimits(t *testing.T) {
	s := testClipboardStore(t)
	s.maxTextBytes = 2
	s.maxImageBytes = 4
	if _, err := s.Add(ClipboardItem{Kind: ClipboardText}, map[uint32][]byte{13: {1, 2, 3}}); err == nil {
		t.Fatal("oversized text was accepted")
	}
	if _, err := s.Add(ClipboardItem{Kind: ClipboardImage}, map[uint32][]byte{8: {1, 2, 3}}); err != nil {
		t.Fatalf("image within its separate limit was rejected: %v", err)
	}
}

func TestClipboardStorePrunesByTotalStoredBytes(t *testing.T) {
	s := testClipboardStore(t)
	first, err := s.Add(ClipboardItem{Kind: ClipboardText, Preview: "first"}, map[uint32][]byte{13: {1}})
	if err != nil {
		t.Fatal(err)
	}
	firstBytes := s.List("")[0].Bytes
	s.maxBytes = firstBytes
	time.Sleep(time.Millisecond)
	if _, err := s.Add(ClipboardItem{Kind: ClipboardText, Preview: "second"}, map[uint32][]byte{13: {2}}); err != nil {
		t.Fatal(err)
	}
	got := s.List("")
	if len(got) != 1 || got[0].Preview != "second" {
		t.Fatalf("wrong byte-capacity result: %#v", got)
	}
	if _, _, err := s.Payload(first.ID); err == nil {
		t.Fatal("byte-pruned payload still exists")
	}
}
