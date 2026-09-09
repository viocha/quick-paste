package main

import (
	"path/filepath"
	"testing"
)

func TestMatchPrefersExactCasePrefix(t *testing.T) {
	store := &PhraseStore{nodes: []PhraseNode{
		{ID: "lower", Type: NodeTypePhrase, Name: "abc lower", Content: "lower", Order: 0},
		{ID: "upper-long", Type: NodeTypePhrase, Name: "ABC longer", Content: "upper-long", Order: 1},
		{ID: "upper-short", Type: NodeTypePhrase, Name: "ABC short", Content: "upper-short", Order: 2},
	}}

	got := store.Match("ABC", 10)
	if len(got) != 3 {
		t.Fatalf("匹配数量 = %d，期望 3", len(got))
	}
	if got[0].Name != "ABC short" || got[1].Name != "ABC longer" || got[2].Name != "abc lower" {
		t.Fatalf("大小写优先顺序错误: %#v", got)
	}
}

func TestMatchUsesNameWhenContentEmpty(t *testing.T) {
	store := &PhraseStore{nodes: []PhraseNode{
		{ID: "empty", Type: NodeTypePhrase, Name: "hello", Content: "", Order: 0},
	}}

	got := store.Match("hel", 10)
	if len(got) != 1 {
		t.Fatalf("匹配数量 = %d，期望 1", len(got))
	}
	if got[0].Content != "hello" {
		t.Fatalf("空内容插入值 = %q，期望使用名称", got[0].Content)
	}
}

func TestCreateNamedNodePersistsWithoutDefaultName(t *testing.T) {
	store := &PhraseStore{path: filepath.Join(t.TempDir(), "phrases.json")}
	service := NewPhraseService(store)
	if _, err := service.CreateNode("", "phrase", "  "); err == nil {
		t.Fatal("blank name must be rejected")
	}
	if len(store.List()) != 0 {
		t.Fatal("blank draft was persisted")
	}
	folder, err := service.CreateNode("", "folder", " Work ")
	if err != nil {
		t.Fatal(err)
	}
	phrase, err := service.CreateNode(folder.ID, "phrase", " Hello ")
	if err != nil {
		t.Fatal(err)
	}
	if folder.Name != "Work" || phrase.Name != "Hello" || phrase.ParentID != folder.ID {
		t.Fatalf("unexpected created nodes: %#v %#v", folder, phrase)
	}
	loaded := &PhraseStore{path: store.path}
	if err := loaded.load(); err != nil {
		t.Fatal(err)
	}
	if len(loaded.List()) != 2 {
		t.Fatal("named nodes were not persisted")
	}
}

func TestMoveNodeTreeAndPersistence(t *testing.T) {
	s := &PhraseStore{path: filepath.Join(t.TempDir(), "phrases.json"), nodes: []PhraseNode{
		{ID: "a", Type: NodeTypeFolder, Name: "A"},
		{ID: "b", Type: NodeTypeFolder, Name: "B"},
		{ID: "child", ParentID: "a", Type: NodeTypeFolder, Name: "Child"},
		{ID: "phrase", ParentID: "child", Type: NodeTypePhrase, Name: "Text", Content: "kept"},
	}}
	for _, pair := range [][2]string{{"a", "a"}, {"a", "child"}, {"b", "phrase"}, {"a", "missing"}, {"missing", "b"}} {
		if err := s.Move(pair[0], pair[1]); err == nil {
			t.Fatalf("accepted illegal move: %v", pair)
		}
	}
	if err := s.Move("a", "b"); err != nil {
		t.Fatal(err)
	}
	loaded := &PhraseStore{path: s.path}
	if err := loaded.load(); err != nil {
		t.Fatal(err)
	}
	a, _ := loaded.findLocked("a")
	phrase, _ := loaded.findLocked("phrase")
	if a.ParentID != "b" || phrase.ParentID != "child" || phrase.Content != "kept" {
		t.Fatal("subtree not preserved")
	}
	if err := s.Move("child", ""); err != nil {
		t.Fatal(err)
	}
	child, _ := s.findLocked("child")
	if child.ParentID != "" {
		t.Fatal("move to root failed")
	}
	s.path = filepath.Join(t.TempDir(), "missing", "phrases.json")
	if err := s.Move("a", ""); err == nil {
		t.Fatal("expected save failure")
	}
	a, _ = s.findLocked("a")
	if a.ParentID != "b" {
		t.Fatal("failed save changed parent")
	}
}

func TestUndoDeleteRestoresSubtree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "phrases.json")
	original := []PhraseNode{
		{ID: "root", Type: NodeTypeFolder, Name: "Root", Order: 0},
		{ID: "child", ParentID: "root", Type: NodeTypeFolder, Name: "Child", Order: 0},
		{ID: "phrase", ParentID: "child", Type: NodeTypePhrase, Name: "Phrase", Content: "text", Order: 0},
		{ID: "other", Type: NodeTypePhrase, Name: "Other", Order: 1},
	}
	s := &PhraseStore{path: path, nodes: cloneNodes(original)}
	if err := s.saveLocked(); err != nil {
		t.Fatal(err)
	}
	deleted := cloneNodes(original[:3])
	if err := s.Delete("root"); err != nil {
		t.Fatal(err)
	}
	if err := s.UndoDelete(deleted); err != nil {
		t.Fatal(err)
	}
	loaded := &PhraseStore{path: path}
	if err := loaded.load(); err != nil {
		t.Fatal(err)
	}
	got := loaded.List()
	if len(got) != len(original) {
		t.Fatalf("restored node count = %d, want %d", len(got), len(original))
	}
	for _, want := range original {
		found := false
		for _, node := range got {
			if node == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing restored node: %#v", want)
		}
	}
}
