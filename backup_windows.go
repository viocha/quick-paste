//go:build windows

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxBackupBytes = 16 << 20

const backupFileName = "backup.json"

type BackupConfig struct {
	Server        string `json:"server"`
	Username      string `json:"username"`
	Automatic     bool   `json:"automatic"`
	IntervalHours int    `json:"intervalHours"`
}
type backupSettings struct {
	BackupConfig
	Password   string `json:"password"`
	LastBackup string `json:"lastBackup"`
	NextBackup string `json:"nextBackup"`
	LastError  string `json:"lastError"`
}
type BackupState struct {
	BackupConfig
	HasPassword bool   `json:"hasPassword"`
	DataPath    string `json:"dataPath"`
	LastBackup  string `json:"lastBackup"`
	NextBackup  string `json:"nextBackup"`
	LastError   string `json:"lastError"`
	Running     bool   `json:"running"`
}
type BackupSnapshot struct {
	Version   int          `json:"version"`
	CreatedAt string       `json:"createdAt"`
	Nodes     []PhraseNode `json:"nodes"`
}
type BackupDifference struct {
	Kind       string      `json:"kind"`
	ID         string      `json:"id"`
	Local      *PhraseNode `json:"local"`
	Remote     *PhraseNode `json:"remote"`
	LocalPath  string      `json:"localPath"`
	RemotePath string      `json:"remotePath"`
}
type BackupPreview struct {
	Token       string             `json:"token"`
	Name        string             `json:"name"`
	CreatedAt   string             `json:"createdAt"`
	LocalCount  int                `json:"localCount"`
	RemoteCount int                `json:"remoteCount"`
	Added       int                `json:"added"`
	Removed     int                `json:"removed"`
	Changed     int                `json:"changed"`
	Local       []PhraseNode       `json:"local"`
	Remote      []PhraseNode       `json:"remote"`
	Differences []BackupDifference `json:"differences"`
}
type BackupService struct {
	store      *PhraseStore
	mu         sync.Mutex
	operation  sync.Mutex
	config     backupSettings
	configPath string
	running    bool
	client     *http.Client
	ctx        context.Context
	cancel     context.CancelFunc
}

func NewBackupService(store *PhraseStore) (*BackupService, error) {
	ctx, cancel := context.WithCancel(context.Background())
	b := &BackupService{store: store, configPath: filepath.Join(filepath.Dir(store.path), "backup-settings.json"), ctx: ctx, cancel: cancel}
	b.config.IntervalHours = 6
	data, err := os.ReadFile(b.configPath)
	if err == nil {
		if err = json.Unmarshal(data, &b.config); err != nil {
			cancel()
			return nil, fmt.Errorf("备份配置读取失败: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		cancel()
		return nil, err
	}
	if b.config.IntervalHours < 1 || b.config.IntervalHours > 168 {
		b.config.IntervalHours = 6
	}
	b.client = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("重定向次数过多")
		}
		if req.URL.Scheme != via[0].URL.Scheme || req.URL.Host != via[0].URL.Host {
			return errors.New("不允许跨服务器重定向")
		}
		return nil
	}}
	return b, nil
}
func (b *BackupService) start() {
	go func() {
		b.runScheduled(time.Now())
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-b.ctx.Done():
				return
			case now := <-ticker.C:
				b.runScheduled(now)
			}
		}
	}()
}
func (b *BackupService) stop() { b.cancel() }
func (b *BackupService) runScheduled(now time.Time) {
	b.mu.Lock()
	cfg := b.config
	b.mu.Unlock()
	due, err := time.Parse(time.RFC3339, cfg.NextBackup)
	if !cfg.Automatic || (err == nil && now.Before(due)) {
		return
	}
	// BackupNow serializes automatic and manual actions. Busy actions are retried next tick.
	_, _ = b.BackupNow()
}
func (b *BackupService) begin() error {
	if !b.operation.TryLock() {
		return errors.New("已有备份操作正在进行")
	}
	b.mu.Lock()
	b.running = true
	b.mu.Unlock()
	return nil
}
func (b *BackupService) end() { b.mu.Lock(); b.running = false; b.mu.Unlock(); b.operation.Unlock() }
func (b *BackupService) GetState() BackupState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return BackupState{BackupConfig: b.config.BackupConfig, HasPassword: b.config.Password != "", DataPath: b.store.path, LastBackup: b.config.LastBackup, NextBackup: b.config.NextBackup, LastError: b.config.LastError, Running: b.running}
}
func (b *BackupService) GetPassword() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.config.Password
}

func (b *BackupService) SaveConfig(config BackupConfig, password string) error {
	if err := b.begin(); err != nil {
		return err
	}
	defer b.end()
	config.Server = strings.TrimSpace(config.Server)
	config.Username = strings.TrimSpace(config.Username)
	if config.IntervalHours < 1 || config.IntervalHours > 168 {
		return errors.New("备份间隔应为 1 到 168 小时")
	}
	if config.Server != "" {
		if _, err := backupDirectory(config.Server); err != nil {
			return err
		}
	} else if config.Automatic {
		return errors.New("请先填写服务器地址")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	next := b.config
	next.Password = password
	scheduleChanged := config != next.BackupConfig
	next.BackupConfig = config
	if scheduleChanged || next.NextBackup == "" {
		next.NextBackup = time.Now().Add(time.Duration(config.IntervalHours) * time.Hour).UTC().Format(time.RFC3339)
	}
	if !config.Automatic {
		next.NextBackup = ""
	}
	next.LastError = ""
	if err := writeJSONAtomic(b.configPath, next); err != nil {
		return err
	}
	b.config = next
	return nil
}
func (b *BackupService) OpenDataDirectory() error {
	return exec.Command("explorer.exe", filepath.Dir(b.store.path)).Start()
}
func backupDirectory(server string) (*url.URL, error) {
	u, err := url.Parse(server)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("请输入有效的 http 或 https WebDAV 目录地址，不要在地址中包含账号密码")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/quick-phrase/"
	u.RawPath = ""
	return u, nil
}
func (b *BackupService) request(method, name string, body []byte) ([]byte, int, error) {
	b.mu.Lock()
	cfg := b.config
	b.mu.Unlock()
	u, err := backupDirectory(cfg.Server)
	if err != nil {
		return nil, 0, err
	}
	if name != "" {
		if name != backupFileName {
			return nil, 0, errors.New("无效的备份文件名")
		}
		u.Path += name
	}
	req, err := http.NewRequestWithContext(b.ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.SetBasicAuth(cfg.Username, cfg.Password)
	if method == "PROPFIND" {
		req.Header.Set("Depth", "1")
		req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	}
	if method == "PUT" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("WebDAV 连接失败（%s）", method)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBackupBytes+1))
	if err != nil {
		return nil, resp.StatusCode, errors.New("读取服务端响应失败")
	}
	if len(data) > maxBackupBytes {
		return nil, resp.StatusCode, errors.New("备份或响应超过 16 MB 限制")
	}
	return data, resp.StatusCode, nil
}
func davStatus(code int) error {
	switch code {
	case 401, 403:
		return errors.New("WebDAV 认证失败或没有访问权限")
	case 404:
		return errors.New("WebDAV 目录或备份不存在")
	default:
		return fmt.Errorf("WebDAV 请求失败，状态码 %d", code)
	}
}
func (b *BackupService) ensureDirectory() error {
	_, code, err := b.request("MKCOL", "", nil)
	if err != nil {
		return err
	}
	if code != 201 && code != 405 {
		return davStatus(code)
	}
	return nil
}
func (b *BackupService) TestConnection() error {
	if err := b.begin(); err != nil {
		return err
	}
	defer b.end()
	if err := b.ensureDirectory(); err != nil {
		return err
	}
	_, code, err := b.request("PROPFIND", "", []byte(`<?xml version="1.0"?><d:propfind xmlns:d="DAV:"><d:prop><d:resourcetype/></d:prop></d:propfind>`))
	if err != nil {
		return err
	}
	if code != 207 {
		return davStatus(code)
	}
	return nil
}
func snapshotNodes(s *PhraseStore) []PhraseNode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneNodes(s.nodes)
}
func randomToken() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(data[:])
}
func (b *BackupService) BackupNow() (name string, err error) {
	if err = b.begin(); err != nil {
		return "", err
	}
	defer b.end()
	defer func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		next := b.config
		now := time.Now().UTC()
		if err != nil {
			next.LastError = err.Error()
			if next.Automatic {
				next.NextBackup = now.Add(15 * time.Minute).Format(time.RFC3339)
			}
		} else {
			next.LastError = ""
			next.LastBackup = now.Format(time.RFC3339)
			if next.Automatic {
				next.NextBackup = now.Add(time.Duration(next.IntervalHours) * time.Hour).Format(time.RFC3339)
			}
		}
		if saveErr := writeJSONAtomic(b.configPath, next); saveErr != nil && err == nil {
			err = fmt.Errorf("备份已上传，但本地状态保存失败: %w", saveErr)
		}
		b.config = next
	}()
	if err = b.ensureDirectory(); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	name = backupFileName
	nodes := snapshotNodes(b.store)
	if err = validateBackupNodes(nodes); err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(BackupSnapshot{Version: 1, CreatedAt: now.Format(time.RFC3339), Nodes: nodes}, "", "  ")
	if len(data) > maxBackupBytes {
		return "", errors.New("本地数据超过 16 MB 限制")
	}
	_, code, err := b.request("PUT", name, data)
	if err != nil {
		return "", err
	}
	if code != 201 && code != 204 && code != 200 {
		return "", davStatus(code)
	}
	return name, nil
}
func validateBackupNodes(nodes []PhraseNode) error {
	if nodes == nil {
		return errors.New("备份缺少节点数据")
	}
	if len(nodes) > 100000 {
		return errors.New("备份节点过多")
	}
	index := map[string]PhraseNode{}
	for _, node := range nodes {
		if node.ID == "" || node.ID == "draft" || strings.TrimSpace(node.Name) == "" || (node.Type != NodeTypeFolder && node.Type != NodeTypePhrase) {
			return errors.New("备份包含无效节点")
		}
		if _, ok := index[node.ID]; ok {
			return errors.New("备份节点 ID 重复")
		}
		index[node.ID] = node
	}
	done := map[string]bool{}
	for _, node := range nodes {
		chain := map[string]bool{}
		for id := node.ID; id != "" && !done[id]; {
			if chain[id] {
				return errors.New("备份中存在循环层级")
			}
			chain[id] = true
			n := index[id]
			if n.ParentID != "" {
				p, ok := index[n.ParentID]
				if !ok || p.Type != NodeTypeFolder {
					return errors.New("备份中存在无效父文件夹")
				}
			}
			id = n.ParentID
		}
		for id := range chain {
			done[id] = true
		}
	}
	return nil
}
func nodesHash(nodes []PhraseNode) string {
	nodes = cloneNodes(nodes)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	data, _ := json.Marshal(nodes)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
func nodePath(node PhraseNode, index map[string]PhraseNode) string {
	parts := []string{node.Name}
	seen := map[string]bool{node.ID: true}
	for node.ParentID != "" {
		if seen[node.ParentID] {
			break
		}
		seen[node.ParentID] = true
		p, ok := index[node.ParentID]
		if !ok {
			break
		}
		parts = append([]string{p.Name}, parts...)
		node = p
	}
	return strings.Join(parts, " / ")
}
func compareBackup(local, remote []PhraseNode) BackupPreview {
	p := BackupPreview{LocalCount: len(local), RemoteCount: len(remote), Local: cloneNodes(local), Remote: cloneNodes(remote), Differences: []BackupDifference{}}
	li, ri := map[string]PhraseNode{}, map[string]PhraseNode{}
	for _, n := range local {
		li[n.ID] = n
	}
	for _, n := range remote {
		ri[n.ID] = n
	}
	for _, n := range remote {
		l, exists := li[n.ID]
		kind := ""
		if !exists {
			kind = "added"
			p.Added++
		} else if l.Name != n.Name || l.Content != n.Content || l.ParentID != n.ParentID || l.Type != n.Type {
			kind = "changed"
			p.Changed++
		}
		if kind != "" {
			diff := BackupDifference{Kind: kind, ID: n.ID, Remote: &n, RemotePath: nodePath(n, ri)}
			if exists {
				diff.Local = &l
				diff.LocalPath = nodePath(l, li)
			}
			p.Differences = append(p.Differences, diff)
		}
	}
	for _, n := range local {
		if _, ok := ri[n.ID]; !ok {
			p.Removed++
			p.Differences = append(p.Differences, BackupDifference{Kind: "removed", ID: n.ID, Local: &n, LocalPath: nodePath(n, li)})
		}
	}
	sort.Slice(p.Differences, func(i, j int) bool { return p.Differences[i].ID < p.Differences[j].ID })
	return p
}
func (b *BackupService) PreviewBackup() (BackupPreview, error) {
	if err := b.begin(); err != nil {
		return BackupPreview{}, err
	}
	defer b.end()
	data, code, err := b.request("GET", backupFileName, nil)
	if err != nil {
		return BackupPreview{}, err
	}
	if code != 200 {
		return BackupPreview{}, davStatus(code)
	}
	var snapshot BackupSnapshot
	if err = json.Unmarshal(data, &snapshot); err != nil || snapshot.Version != 1 {
		return BackupPreview{}, errors.New("无效或不支持的备份格式")
	}
	if err = validateBackupNodes(snapshot.Nodes); err != nil {
		return BackupPreview{}, err
	}
	local := snapshotNodes(b.store)
	preview := compareBackup(local, snapshot.Nodes)
	preview.Token = randomToken()
	preview.Name = backupFileName
	preview.CreatedAt = snapshot.CreatedAt
	return preview, nil
}
func (b *BackupService) RestoreBackup() (string, error) {
	if err := b.begin(); err != nil {
		return "", err
	}
	defer b.end()
	data, code, err := b.request("GET", backupFileName, nil)
	if err != nil {
		return "", err
	}
	if code != 200 {
		return "", davStatus(code)
	}
	var snapshot BackupSnapshot
	if err = json.Unmarshal(data, &snapshot); err != nil || snapshot.Version != 1 {
		return "", errors.New("无效或不支持的备份格式")
	}
	if err = validateBackupNodes(snapshot.Nodes); err != nil {
		return "", err
	}
	s := b.store
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(filepath.Dir(s.path), "recovery")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	recovery := filepath.Join(dir, "before-restore-"+time.Now().UTC().Format("20060102T150405")+"-"+randomToken()+".json")
	if err := writeJSONAtomic(recovery, cloneNodes(s.nodes)); err != nil {
		return "", fmt.Errorf("保存本地回退副本失败: %w", err)
	}
	before := s.nodes
	s.nodes = cloneNodes(snapshot.Nodes)
	if err := s.saveLocked(); err != nil {
		s.nodes = before
		return "", err
	}
	return recovery, nil
}
func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".backup-*.tmp")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
