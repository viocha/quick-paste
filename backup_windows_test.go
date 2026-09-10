//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWebDAVBackupRestoreAndSchedule(t *testing.T) {
	var mu sync.Mutex
	files := map[string][]byte{}
	denied := false
	uploads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		user, pass, ok := r.BasicAuth()
		if denied || !ok || user != "tester" || pass != "secret-password" {
			w.WriteHeader(401)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/dav/quick-paste/") {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/dav/quick-paste/")
		switch r.Method {
		case "MKCOL":
			w.WriteHeader(201)
		case "PUT":
			if name != backupFileName {
				t.Error("unexpected backup name")
			}
			uploads++
			files[name], _ = io.ReadAll(r.Body)
			w.WriteHeader(201)
		case "GET":
			if data, ok := files[name]; ok {
				w.Write(data)
			} else {
				w.WriteHeader(404)
			}
		case "PROPFIND":
			if r.Header.Get("Depth") != "1" {
				t.Error("wrong depth")
			}
			w.WriteHeader(207)
			fmt.Fprint(w, `<d:multistatus xmlns:d="DAV:">`)
			for name, data := range files {
				fmt.Fprintf(w, `<d:response><d:href>/dav/quick-paste/%s</d:href><d:propstat><d:prop><d:getcontentlength>%d</d:getcontentlength><d:resourcetype/></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, name, len(data))
			}
			fmt.Fprint(w, `</d:multistatus>`)
		default:
			w.WriteHeader(405)
		}
	}))
	defer server.Close()
	original := []PhraseNode{{ID: "f", Type: NodeTypeFolder, Name: "Folder"}, {ID: "a", ParentID: "f", Type: NodeTypePhrase, Name: "Alpha", Content: "remote"}, {ID: "b", Type: NodeTypePhrase, Name: "Beta"}}
	store := &PhraseStore{path: filepath.Join(t.TempDir(), "phrases.json"), nodes: cloneNodes(original)}
	b, err := NewBackupService(store)
	if err != nil {
		t.Fatal(err)
	}
	defer b.stop()
	cfg := BackupConfig{Server: server.URL + "/dav", Username: "tester", Automatic: true, IntervalHours: 6}
	if err = b.SaveConfig(cfg, "secret-password"); err != nil {
		t.Fatal(err)
	}
	changed := cfg
	changed.Server += "/new"
	changed.Username = "tester-updated"
	if err = b.SaveConfig(changed, "secret-password"); err != nil {
		t.Fatalf("changing server or username should not require password re-entry: %v", err)
	}
	if b.GetPassword() != "secret-password" {
		t.Fatal("password changed unexpectedly")
	}
	if err = b.SaveConfig(cfg, "secret-password"); err != nil {
		t.Fatal(err)
	}
	persisted, _ := os.ReadFile(b.configPath)
	if !strings.Contains(string(persisted), "secret-password") {
		t.Fatal("plaintext password not stored")
	}
	exposed, _ := json.Marshal(b.GetState())
	if strings.Contains(string(exposed), "secret") {
		t.Fatal("secret exposed")
	}
	if err = b.TestConnection(); err != nil {
		t.Fatal(err)
	}
	name, err := b.BackupNow()
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	payload := string(files[name])
	mu.Unlock()
	if strings.Contains(payload, "password") || strings.Contains(payload, "tester") {
		t.Fatal("credentials in backup")
	}
	if _, err = b.RestoreBackup(); err != nil {
		t.Fatalf("restore should not require preview: %v", err)
	}
	store.nodes = []PhraseNode{original[0], original[1], {ID: "c", Type: NodeTypePhrase, Name: "Local"}}
	store.nodes[1].Content = "local"
	preview, err := b.PreviewBackup()
	if err != nil {
		t.Fatal(err)
	}
	if preview.Added != 1 || preview.Removed != 1 || preview.Changed != 1 {
		t.Fatalf("wrong differences: %+v", preview)
	}
	if nodesHash(preview.Local) != nodesHash(store.nodes) || nodesHash(preview.Remote) != nodesHash(original) {
		t.Fatal("preview snapshots do not match local and remote data")
	}
	store.nodes[1].Content = "edited after preview"
	before := nodesHash(store.nodes)
	recovery, err := b.RestoreBackup()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(recovery)
	if err != nil {
		t.Fatal(err)
	}
	var saved []PhraseNode
	if err = json.Unmarshal(data, &saved); err != nil || nodesHash(saved) != before {
		t.Fatal("recovery did not preserve local data")
	}
	if nodesHash(store.nodes) != nodesHash(original) {
		t.Fatal("restore mismatch")
	}
	loaded := &PhraseStore{path: store.path}
	if err = loaded.load(); err != nil || nodesHash(loaded.nodes) != nodesHash(original) {
		t.Fatal("restore not persisted")
	}
	restarted, err := NewBackupService(store)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.stop()
	restarted.runScheduled(time.Now())
	mu.Lock()
	count := uploads
	mu.Unlock()
	if count != 1 {
		t.Fatal("backup before due")
	}
	store.nodes[1].Content = "new upload"
	restarted.runScheduled(time.Now().Add(7 * time.Hour))
	mu.Lock()
	count = uploads
	if len(files) != 1 {
		t.Error("multiple remote backups")
	}
	if !strings.Contains(string(files[backupFileName]), "new upload") {
		t.Error("remote file not replaced")
	}
	denied = true
	mu.Unlock()
	if count != 2 {
		t.Fatal("due backup not run")
	}
	if _, err = restarted.BackupNow(); err == nil {
		t.Fatal("authentication error ignored")
	}
	state := restarted.GetState()
	due, _ := time.Parse(time.RFC3339, state.NextBackup)
	if state.LastError == "" || time.Until(due) > 16*time.Minute || time.Until(due) < 14*time.Minute {
		t.Fatal("failure retry not scheduled")
	}
	if err = b.begin(); err != nil {
		t.Fatal(err)
	}
	if _, err = b.PreviewBackup(); err == nil {
		t.Fatal("concurrent operation allowed")
	}
	b.end()
}

func TestBackupRejectsInvalidTrees(t *testing.T) {
	cases := [][]PhraseNode{nil, {{ID: "a", Type: NodeTypeFolder, Name: "A", ParentID: "b"}, {ID: "b", Type: NodeTypeFolder, Name: "B", ParentID: "a"}}, {{ID: "a", Name: "A", Type: NodeTypePhrase, ParentID: "missing"}}, {{ID: "a", Name: "A", Type: NodeTypePhrase}, {ID: "a", Name: "B", Type: NodeTypePhrase}}}
	for i, nodes := range cases {
		if validateBackupNodes(nodes) == nil {
			t.Errorf("invalid tree %d accepted", i)
		}
	}
	if err := validateBackupNodes([]PhraseNode{}); err != nil {
		t.Fatal(err)
	}
}
