package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type ClipboardKind string

const (
	ClipboardText  ClipboardKind = "text"
	ClipboardRich  ClipboardKind = "rich"
	ClipboardImage ClipboardKind = "image"
	ClipboardFiles ClipboardKind = "files"
)

type ClipboardItem struct {
	ID         string        `json:"id"`
	Kind       ClipboardKind `json:"kind"`
	Preview    string        `json:"preview"`
	Text       string        `json:"text,omitempty"`
	Files      []string      `json:"files,omitempty"`
	Source     string        `json:"source,omitempty"`
	SourceIcon string        `json:"sourceIcon,omitempty"`
	CreatedAt  time.Time     `json:"createdAt"`
	UsedAt     time.Time     `json:"usedAt,omitempty"`
	Pinned     bool          `json:"pinned,omitempty"`
	Hash       string        `json:"hash"`
	Bytes      int64         `json:"bytes"`
}

type ClipboardListItem struct {
	ID        string        `json:"id"`
	Kind      ClipboardKind `json:"kind"`
	Preview   string        `json:"preview"`
	Source    string        `json:"source,omitempty"`
	CreatedAt time.Time     `json:"createdAt"`
	Pinned    bool          `json:"pinned,omitempty"`
	HasText   bool          `json:"hasText,omitempty"`
}

type ClipboardPage struct {
	Items []ClipboardListItem `json:"items"`
	Total int                 `json:"total"`
	Icons map[string]string   `json:"icons,omitempty"`
}

type clipboardPayload struct {
	Formats map[uint32][]byte `json:"formats"`
}

type ClipboardLimits struct {
	MaxItems    int `json:"maxItems"`
	MaxBytesMB  int `json:"maxBytesMB"`
	TextItemMB  int `json:"textItemMB"`
	ImageItemMB int `json:"imageItemMB"`
}

type ClipboardStats struct {
	Items  int   `json:"items"`
	Pinned int   `json:"pinned"`
	Bytes  int64 `json:"bytes"`
}

func defaultClipboardLimits() ClipboardLimits {
	return ClipboardLimits{MaxItems: 10000, MaxBytesMB: 64, TextItemMB: 2, ImageItemMB: 16}
}

type ClipboardStore struct {
	mu            sync.RWMutex
	dir           string
	index         string
	items         []ClipboardItem
	maxItems      int
	maxBytes      int64
	maxTextBytes  int64
	maxImageBytes int64
}

func NewClipboardStore() (*ClipboardStore, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(configDir, "quick-paste", "clipboard")
	if err := os.MkdirAll(filepath.Join(dir, "items"), 0o755); err != nil {
		return nil, err
	}
	limits := defaultClipboardLimits()
	s := &ClipboardStore{dir: dir, index: filepath.Join(dir, "index.json"), maxItems: limits.MaxItems, maxBytes: int64(limits.MaxBytesMB) * 1024 * 1024, maxTextBytes: int64(limits.TextItemMB) * 1024 * 1024, maxImageBytes: int64(limits.ImageItemMB) * 1024 * 1024}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *ClipboardStore) load() error {
	data, err := os.ReadFile(s.index)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &s.items); err != nil {
		return fmt.Errorf("读取剪贴板历史失败: %w", err)
	}
	s.pruneLocked()
	s.cleanOrphansLocked()
	return s.saveLocked()
}

func (s *ClipboardStore) saveLocked() error {
	data, err := json.MarshalIndent(s.items, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.index + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.index)
}

func (s *ClipboardStore) payloadPath(id string) string {
	return filepath.Join(s.dir, "items", id+".json")
}

func clipboardHash(formats map[uint32][]byte) string {
	h := sha256.New()
	keys := make([]int, 0, len(formats))
	for format := range formats {
		keys = append(keys, int(format))
	}
	sort.Ints(keys)
	for _, key := range keys {
		_, _ = fmt.Fprintf(h, "%d:", key)
		_, _ = h.Write(formats[uint32(key)])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *ClipboardStore) Add(item ClipboardItem, formats map[uint32][]byte) (ClipboardItem, error) {
	if len(formats) == 0 {
		return ClipboardItem{}, errors.New("剪贴板中没有支持的格式")
	}
	payloadData, err := json.Marshal(clipboardPayload{Formats: formats})
	if err != nil {
		return ClipboardItem{}, err
	}
	s.mu.RLock()
	textLimit, imageLimit := s.maxTextBytes, s.maxImageBytes
	s.mu.RUnlock()
	var rawBytes int64
	for _, data := range formats {
		rawBytes += int64(len(data))
	}
	limit := textLimit
	label := "文本"
	if item.Kind == ClipboardImage {
		limit = imageLimit
		label = "图片"
	} else if item.Kind == ClipboardFiles {
		label = "文件列表"
	}
	if limit > 0 && rawBytes > limit {
		return ClipboardItem{}, fmt.Errorf("%s项目超过单项上限 %d MB，已跳过", label, limit/(1024*1024))
	}
	item.Hash = clipboardHash(formats)
	item.Bytes = int64(len(payloadData))
	item.CreatedAt = time.Now()
	textRunes := []rune(item.Text)
	if len(textRunes) > 4096 {
		item.Text = string(textRunes[:4096])
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].Hash != item.Hash {
			continue
		}
		item.ID = s.items[i].ID
		item.Pinned = s.items[i].Pinned
		item.UsedAt = s.items[i].UsedAt
		if err := os.WriteFile(s.payloadPath(item.ID)+".tmp", payloadData, 0o600); err != nil {
			return ClipboardItem{}, err
		}
		if err := os.Rename(s.payloadPath(item.ID)+".tmp", s.payloadPath(item.ID)); err != nil {
			return ClipboardItem{}, err
		}
		s.items[i] = item
		s.sortLocked()
		return item, s.saveLocked()
	}
	item.ID = fmt.Sprintf("%d", time.Now().UnixNano())
	tmp := s.payloadPath(item.ID) + ".tmp"
	if err := os.WriteFile(tmp, payloadData, 0o600); err != nil {
		return ClipboardItem{}, err
	}
	if err := os.Rename(tmp, s.payloadPath(item.ID)); err != nil {
		return ClipboardItem{}, err
	}
	s.items = append(s.items, item)
	s.pruneLocked()
	if err := s.saveLocked(); err != nil {
		return ClipboardItem{}, err
	}
	found := false
	for _, kept := range s.items {
		if kept.ID == item.ID {
			found = true
			break
		}
	}
	if !found {
		return ClipboardItem{}, errors.New("剪贴板历史已达到容量上限，请清理或取消置顶部分项目")
	}
	return item, nil
}

func (s *ClipboardStore) List(query string) []ClipboardItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	query = strings.ToLower(strings.TrimSpace(query))
	result := make([]ClipboardItem, 0, len(s.items))
	for _, item := range s.items {
		if query != "" && !strings.Contains(strings.ToLower(item.Preview+"\n"+item.Text+"\n"+strings.Join(item.Files, "\n")+"\n"+item.Source), query) {
			continue
		}
		copyItem := item
		copyItem.Files = append([]string(nil), item.Files...)
		result = append(result, copyItem)
	}
	return result
}

func clipboardListItem(item ClipboardItem) ClipboardListItem {
	return ClipboardListItem{
		ID:        item.ID,
		Kind:      item.Kind,
		Preview:   item.Preview,
		Source:    item.Source,
		CreatedAt: item.CreatedAt,
		Pinned:    item.Pinned,
		HasText:   item.Text != "" || len(item.Files) > 0,
	}
}

func clipboardItemMatches(item ClipboardItem, query string) bool {
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(item.Preview+"\n"+item.Text+"\n"+strings.Join(item.Files, "\n")+"\n"+item.Source), query)
}

func (s *ClipboardStore) ListPage(query string, offset, limit int) ClipboardPage {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query = strings.ToLower(strings.TrimSpace(query))
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 100
	} else if limit > 500 {
		limit = 500
	}

	page := ClipboardPage{
		Items: make([]ClipboardListItem, 0, limit),
		Icons: make(map[string]string),
	}
	if query == "" {
		page.Total = len(s.items)
		if offset >= len(s.items) {
			page.Icons = nil
			return page
		}
		end := min(len(s.items), offset+limit)
		for _, item := range s.items[offset:end] {
			page.Items = append(page.Items, clipboardListItem(item))
			if item.Source != "" && item.SourceIcon != "" {
				if _, exists := page.Icons[item.Source]; !exists {
					page.Icons[item.Source] = item.SourceIcon
				}
			}
		}
		if len(page.Icons) == 0 {
			page.Icons = nil
		}
		return page
	}

	matched := 0
	for _, item := range s.items {
		if !clipboardItemMatches(item, query) {
			continue
		}
		if matched >= offset && len(page.Items) < limit {
			page.Items = append(page.Items, clipboardListItem(item))
			if item.Source != "" && item.SourceIcon != "" {
				if _, exists := page.Icons[item.Source]; !exists {
					page.Icons[item.Source] = item.SourceIcon
				}
			}
		}
		matched++
	}
	page.Total = matched
	if len(page.Icons) == 0 {
		page.Icons = nil
	}
	return page
}

func (s *ClipboardStore) Payload(id string) (ClipboardItem, map[uint32][]byte, error) {
	s.mu.RLock()
	var item ClipboardItem
	found := false
	for _, candidate := range s.items {
		if candidate.ID == id {
			item, found = candidate, true
			break
		}
	}
	s.mu.RUnlock()
	if !found {
		return ClipboardItem{}, nil, errors.New("剪贴板项目不存在")
	}
	data, err := os.ReadFile(s.payloadPath(id))
	if err != nil {
		return ClipboardItem{}, nil, err
	}
	var payload clipboardPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return ClipboardItem{}, nil, err
	}
	return item, payload.Formats, nil
}

func (s *ClipboardStore) MarkUsed(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID == id {
			s.items[i].UsedAt = time.Now()
			s.sortLocked()
			return s.saveLocked()
		}
	}
	return errors.New("剪贴板项目不存在")
}

func (s *ClipboardStore) TogglePinned(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID == id {
			s.items[i].Pinned = !s.items[i].Pinned
			s.sortLocked()
			return s.saveLocked()
		}
	}
	return errors.New("剪贴板项目不存在")
}

func (s *ClipboardStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID == id {
			s.items = append(s.items[:i], s.items[i+1:]...)
			if err := s.saveLocked(); err != nil {
				return err
			}
			if err := os.Remove(s.payloadPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		}
	}
	return errors.New("剪贴板项目不存在")
}

func (s *ClipboardStore) Clear(includePinned bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.items[:0]
	for _, item := range s.items {
		if item.Pinned && !includePinned {
			kept = append(kept, item)
			continue
		}
		_ = os.Remove(s.payloadPath(item.ID))
	}
	s.items = kept
	return s.saveLocked()
}

func (s *ClipboardStore) sortLocked() {
	sort.SliceStable(s.items, func(i, j int) bool {
		if s.items[i].Pinned != s.items[j].Pinned {
			return s.items[i].Pinned
		}
		ai, aj := s.items[i].CreatedAt, s.items[j].CreatedAt
		if s.items[i].UsedAt.After(ai) {
			ai = s.items[i].UsedAt
		}
		if s.items[j].UsedAt.After(aj) {
			aj = s.items[j].UsedAt
		}
		if ai.Equal(aj) && s.items[i].UsedAt.IsZero() != s.items[j].UsedAt.IsZero() {
			return !s.items[i].UsedAt.IsZero()
		}
		return ai.After(aj)
	})
}

func (s *ClipboardStore) pruneLocked() {
	s.sortLocked()
	kept := make([]ClipboardItem, 0, len(s.items))
	ordinary := 0
	for _, item := range s.items {
		remove := !item.Pinned && ordinary >= s.maxItems
		if remove {
			_ = os.Remove(s.payloadPath(item.ID))
			continue
		}
		if !item.Pinned {
			ordinary++
		}
		kept = append(kept, item)
	}
	s.items = kept
	if s.maxBytes > 0 {
		var total int64
		for _, item := range s.items {
			total += item.Bytes
		}
		for i := len(s.items) - 1; i >= 0 && total > s.maxBytes; i-- {
			if s.items[i].Pinned {
				continue
			}
			total -= s.items[i].Bytes
			_ = os.Remove(s.payloadPath(s.items[i].ID))
			s.items = append(s.items[:i], s.items[i+1:]...)
		}
	}
}

func (s *ClipboardStore) Limits() ClipboardLimits {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return ClipboardLimits{MaxItems: s.maxItems, MaxBytesMB: int(s.maxBytes / (1024 * 1024)), TextItemMB: int(s.maxTextBytes / (1024 * 1024)), ImageItemMB: int(s.maxImageBytes / (1024 * 1024))}
}

func (s *ClipboardStore) Stats() ClipboardStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	stats := ClipboardStats{Items: len(s.items)}
	for _, item := range s.items {
		stats.Bytes += item.Bytes
		if item.Pinned {
			stats.Pinned++
		}
	}
	return stats
}

func (s *ClipboardStore) SetLimits(limits ClipboardLimits) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxItems = limits.MaxItems
	s.maxBytes = int64(limits.MaxBytesMB) * 1024 * 1024
	s.maxTextBytes = int64(limits.TextItemMB) * 1024 * 1024
	s.maxImageBytes = int64(limits.ImageItemMB) * 1024 * 1024
	s.pruneLocked()
	return s.saveLocked()
}

func (s *ClipboardStore) cleanOrphansLocked() {
	referenced := make(map[string]bool, len(s.items))
	for _, item := range s.items {
		referenced[item.ID+".json"] = true
	}
	entries, err := os.ReadDir(filepath.Join(s.dir, "items"))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() && !referenced[entry.Name()] {
			_ = os.Remove(filepath.Join(s.dir, "items", entry.Name()))
		}
	}
}
