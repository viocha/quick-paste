package main

import (
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

type NodeType string

const (
	NodeTypeFolder NodeType = "folder"
	NodeTypePhrase NodeType = "phrase"
)

type PhraseNode struct {
	ID                string   `json:"id"`
	ParentID          string   `json:"parentId,omitempty"`
	Type              NodeType `json:"type"`
	Name              string   `json:"name"`
	Content           string   `json:"content,omitempty"`
	Order             int      `json:"order"`
	CompletionEnabled bool     `json:"completionEnabled,omitempty"`
}

type Suggestion struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

type SuggestionPayload struct {
	Query  string       `json:"query"`
	Items  []Suggestion `json:"items"`
	Active int          `json:"active"`
}

type PhraseStore struct {
	mu    sync.RWMutex
	path  string
	nodes []PhraseNode
}

func NewPhraseStore() (*PhraseStore, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(configDir, "quick-paste")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	store := &PhraseStore{path: filepath.Join(dir, "phrases.json")}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *PhraseStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.nodes = defaultNodes()
		return s.saveLocked()
	}
	if err != nil {
		return err
	}
	if len(data) == 0 {
		s.nodes = defaultNodes()
		return s.saveLocked()
	}
	if err := json.Unmarshal(data, &s.nodes); err != nil {
		return fmt.Errorf("读取短语数据失败: %w", err)
	}
	return nil
}

func defaultNodes() []PhraseNode {
	return []PhraseNode{
		{ID: "folder-example", Type: NodeTypeFolder, Name: "示例", Order: 0},
		{ID: "phrase-abc-upper", ParentID: "folder-example", Type: NodeTypePhrase, Name: "ABC状态", Content: "当前进度正常，暂无阻塞。", Order: 0},
		{ID: "phrase-abc-lower", ParentID: "folder-example", Type: NodeTypePhrase, Name: "abc地址", Content: "", Order: 1},
		{ID: "phrase-thanks", ParentID: "folder-example", Type: NodeTypePhrase, Name: "thanks", Content: "谢谢，收到。", Order: 2},
	}
}

func (s *PhraseStore) saveLocked() error {
	data, err := json.MarshalIndent(s.nodes, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func cloneNodes(nodes []PhraseNode) []PhraseNode {
	result := make([]PhraseNode, len(nodes))
	copy(result, nodes)
	return result
}

func (s *PhraseStore) List() []PhraseNode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := cloneNodes(s.nodes)
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].ParentID != result[j].ParentID {
			return result[i].ParentID < result[j].ParentID
		}
		return result[i].Order < result[j].Order
	})
	return result
}

func (s *PhraseStore) Create(parentID string, kind NodeType, names ...string) (PhraseNode, error) {
	return s.create(parentID, kind, "", names...)
}

func (s *PhraseStore) create(parentID string, kind NodeType, content string, names ...string) (PhraseNode, error) {
	if kind != NodeTypeFolder && kind != NodeTypePhrase {
		return PhraseNode{}, errors.New("无效节点类型")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if parentID != "" {
		parent, ok := s.findLocked(parentID)
		if !ok || parent.Type != NodeTypeFolder {
			return PhraseNode{}, errors.New("父文件夹不存在")
		}
	}

	name := "新建短语"
	if kind == NodeTypeFolder {
		name = "新建文件夹"
	}
	if len(names) > 0 {
		name = strings.TrimSpace(names[0])
		if name == "" {
			return PhraseNode{}, errors.New("名称不能为空")
		}
	}
	node := PhraseNode{
		ID:       fmt.Sprintf("%d", time.Now().UnixNano()),
		ParentID: parentID,
		Type:     kind,
		Name:     name,
		Content:  content,
		Order:    s.nextOrderLocked(parentID),
	}
	s.nodes = append(s.nodes, node)
	if err := s.saveLocked(); err != nil {
		s.nodes = s.nodes[:len(s.nodes)-1]
		return PhraseNode{}, err
	}
	return node, nil
}

func (s *PhraseStore) Rename(id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("名称不能为空")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.nodes {
		if s.nodes[i].ID == id {
			previous := s.nodes[i].Name
			s.nodes[i].Name = name
			if err := s.saveLocked(); err != nil {
				s.nodes[i].Name = previous
				return err
			}
			return nil
		}
	}
	return errors.New("节点不存在")
}

func (s *PhraseStore) UpdatePhrase(id, name, content string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("名称不能为空")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.nodes {
		if s.nodes[i].ID == id {
			if s.nodes[i].Type != NodeTypePhrase {
				return errors.New("节点不是短语")
			}
			previousName, previousContent := s.nodes[i].Name, s.nodes[i].Content
			s.nodes[i].Name = name
			s.nodes[i].Content = content
			if err := s.saveLocked(); err != nil {
				s.nodes[i].Name, s.nodes[i].Content = previousName, previousContent
				return err
			}
			return nil
		}
	}
	return errors.New("短语不存在")
}

func (s *PhraseStore) SetFolderCompletion(id string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.nodes {
		if s.nodes[i].ID != id {
			continue
		}
		if s.nodes[i].Type != NodeTypeFolder {
			return errors.New("只有文件夹可以设置自动补全")
		}
		previous := s.nodes[i].CompletionEnabled
		s.nodes[i].CompletionEnabled = enabled
		if err := s.saveLocked(); err != nil {
			s.nodes[i].CompletionEnabled = previous
			return err
		}
		return nil
	}
	return errors.New("文件夹不存在")
}

func (s *PhraseStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.findLocked(id); !ok {
		return errors.New("节点不存在")
	}

	ids := map[string]bool{id: true}
	changed := true
	for changed {
		changed = false
		for _, node := range s.nodes {
			if ids[node.ParentID] && !ids[node.ID] {
				ids[node.ID] = true
				changed = true
			}
		}
	}

	kept := s.nodes[:0]
	for _, node := range s.nodes {
		if !ids[node.ID] {
			kept = append(kept, node)
		}
	}
	s.nodes = kept
	return s.saveLocked()
}

func (s *PhraseStore) UndoDelete(nodes []PhraseNode) error {
	if len(nodes) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing := make(map[string]bool, len(s.nodes)+len(nodes))
	for _, node := range s.nodes {
		existing[node.ID] = true
	}
	for _, node := range nodes {
		if existing[node.ID] {
			return errors.New("节点已存在")
		}
		existing[node.ID] = true
	}
	previous := cloneNodes(s.nodes)
	s.nodes = append(s.nodes, cloneNodes(nodes)...)
	if err := s.saveLocked(); err != nil {
		s.nodes = previous
		return err
	}
	return nil
}

func (s *PhraseStore) findLocked(id string) (PhraseNode, bool) {
	for _, node := range s.nodes {
		if node.ID == id {
			return node, true
		}
	}
	return PhraseNode{}, false
}

func (s *PhraseStore) nextOrderLocked(parentID string) int {
	maxOrder := -1
	for _, node := range s.nodes {
		if node.ParentID == parentID && node.Order > maxOrder {
			maxOrder = node.Order
		}
	}
	return maxOrder + 1
}

func (s *PhraseStore) Match(query string, limit int) []Suggestion {
	if query == "" || limit <= 0 {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	lowerQuery := strings.ToLower(query)
	type ranked struct {
		node PhraseNode
		rank int
	}
	matches := make([]ranked, 0)
	enabledFolders := make(map[string]bool)
	folderParents := make(map[string]string)
	for _, node := range s.nodes {
		if node.Type != NodeTypeFolder {
			continue
		}
		folderParents[node.ID] = node.ParentID
		if node.CompletionEnabled {
			enabledFolders[node.ID] = true
		}
	}
	completionEnabled := func(parentID string) bool {
		visited := make(map[string]bool)
		for parentID != "" && !visited[parentID] {
			if enabledFolders[parentID] {
				return true
			}
			visited[parentID] = true
			parentID = folderParents[parentID]
		}
		return false
	}
	for _, node := range s.nodes {
		if node.Type != NodeTypePhrase {
			continue
		}
		if !completionEnabled(node.ParentID) {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(node.Name), lowerQuery) {
			continue
		}
		rank := 2
		if strings.HasPrefix(node.Name, query) {
			rank = 1
		}
		if node.Name == query {
			rank = 0
		}
		matches = append(matches, ranked{node: node, rank: rank})
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].rank != matches[j].rank {
			return matches[i].rank < matches[j].rank
		}
		li := []rune(matches[i].node.Name)
		lj := []rune(matches[j].node.Name)
		if len(li) != len(lj) {
			return len(li) < len(lj)
		}
		return matches[i].node.Order < matches[j].node.Order
	})

	if len(matches) > limit {
		matches = matches[:limit]
	}
	result := make([]Suggestion, 0, len(matches))
	for _, item := range matches {
		content := item.node.Content
		if content == "" {
			content = item.node.Name
		}
		result = append(result, Suggestion{ID: item.node.ID, Name: item.node.Name, Content: content})
	}
	return result
}

type PhraseService struct {
	store   *PhraseStore
	changed func()
}

func NewPhraseService(store *PhraseStore) *PhraseService {
	return &PhraseService{store: store}
}

func (s *PhraseService) ListNodes() []PhraseNode {
	return s.store.List()
}

func (s *PhraseService) notify(err error) error {
	if err == nil && s.changed != nil {
		s.changed()
	}
	return err
}
func (s *PhraseService) notifyNode(node PhraseNode, err error) (PhraseNode, error) {
	if err == nil && s.changed != nil {
		s.changed()
	}
	return node, err
}

func (s *PhraseService) CreateFolder(parentID string) (PhraseNode, error) {
	return s.notifyNode(s.store.Create(parentID, NodeTypeFolder))
}

func (s *PhraseService) CreatePhrase(parentID string) (PhraseNode, error) {
	return s.notifyNode(s.store.Create(parentID, NodeTypePhrase))
}

func (s *PhraseService) RenameNode(id, name string) error {
	return s.notify(s.store.Rename(id, name))
}

func (s *PhraseService) UpdatePhrase(id, name, content string) error {
	return s.notify(s.store.UpdatePhrase(id, name, content))
}

func (s *PhraseService) SetFolderCompletion(id string, enabled bool) error {
	return s.notify(s.store.SetFolderCompletion(id, enabled))
}

func (s *PhraseService) CreatePhraseWithContent(parentID, name, content string) (PhraseNode, error) {
	return s.notifyNode(s.store.create(parentID, NodeTypePhrase, content, name))
}

func (s *PhraseService) DeleteNode(id string) error {
	return s.notify(s.store.Delete(id))
}

func (s *PhraseService) UndoDelete(nodes []PhraseNode) error {
	return s.notify(s.store.UndoDelete(nodes))
}

func (s *PhraseService) CreateNode(parentID, kind, name string) (PhraseNode, error) {
	return s.notifyNode(s.store.Create(parentID, NodeType(kind), name))
}

// Move changes only the parent. Children retain their IDs and follow their folder.
func (s *PhraseStore) Move(id, parentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	node, ok := s.findLocked(id)
	if !ok {
		return errors.New("节点不存在")
	}
	seen := map[string]bool{id: true}
	for target := parentID; target != ""; {
		if seen[target] {
			return errors.New("不能移动到自身或子文件夹")
		}
		seen[target] = true
		parent, ok := s.findLocked(target)
		if !ok || parent.Type != NodeTypeFolder {
			return errors.New("目标文件夹不存在")
		}
		target = parent.ParentID
	}
	if node.ParentID == parentID {
		return nil
	}
	for i := range s.nodes {
		if s.nodes[i].ID == id {
			previous := s.nodes[i]
			s.nodes[i].ParentID = parentID
			s.nodes[i].Order = s.nextOrderLocked(parentID)
			if err := s.saveLocked(); err != nil {
				s.nodes[i] = previous
				return err
			}
			return nil
		}
	}
	return nil
}
func (s *PhraseService) MoveNode(id, parentID string) error {
	return s.notify(s.store.Move(id, parentID))
}
