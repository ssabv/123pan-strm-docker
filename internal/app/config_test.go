package app

import (
	"os"
	"path/filepath"
	"testing"
)

// ListLibraries 的摘要缓存必须随库文件更新而失效
func TestListLibrariesSummaryCache(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{LibDir: dir}
	p := filepath.Join(dir, "demo.json")

	write := func(files []any) {
		lib := map[string]any{
			"id":         "demo",
			"name":       "演示库",
			"commonPath": "",
			"createdAt":  int64(1),
			"meta":       map[string]any{},
			"files":      files,
			"category":   "电影",
		}
		cfg.writeLibraryFile(p, lib)
	}

	write([]any{
		map[string]any{"path": "电影/a.mp4", "size": int64(1), "etag": "x"},
		map[string]any{"path": "电影/b.txt", "size": int64(1), "etag": "y"},
	})

	rows := cfg.ListLibraries()
	if len(rows) != 1 {
		t.Fatalf("期望 1 个库, 实际 %d", len(rows))
	}
	if rows[0]["total"] != 2 || rows[0]["video"] != 1 {
		t.Fatalf("首次摘要错误: total=%v video=%v", rows[0]["total"], rows[0]["video"])
	}

	// 命中缓存时应返回同一份数据
	if again := cfg.ListLibraries(); again[0]["total"] != 2 {
		t.Fatalf("缓存命中结果错误: %v", again[0]["total"])
	}

	// 改写库文件后必须重新统计
	write([]any{
		map[string]any{"path": "电影/a.mp4", "size": int64(1), "etag": "x"},
		map[string]any{"path": "电影/c.mp4", "size": int64(1), "etag": "z"},
		map[string]any{"path": "电影/d.mp4", "size": int64(1), "etag": "w"},
	})
	rows = cfg.ListLibraries()
	if rows[0]["total"] != 3 || rows[0]["video"] != 3 {
		t.Fatalf("写入后摘要未刷新: total=%v video=%v", rows[0]["total"], rows[0]["video"])
	}

	// 删除库文件后不应再出现在列表里
	os.Remove(p)
	if rows = cfg.ListLibraries(); len(rows) != 0 {
		t.Fatalf("删除后仍返回 %d 个库", len(rows))
	}
}

// 并发调用列表接口不应触发 map 并发读写
func TestListLibrariesSummaryCacheConcurrent(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{LibDir: dir}
	cfg.writeLibraryFile(filepath.Join(dir, "demo.json"), map[string]any{
		"id": "demo", "name": "演示库", "createdAt": int64(1),
		"meta": map[string]any{},
		"files": []any{
			map[string]any{"path": "a.mp4", "size": int64(1), "etag": "x"},
		},
	})
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 20; j++ {
				if rows := cfg.ListLibraries(); len(rows) != 1 {
					t.Errorf("期望 1 个库, 实际 %d", len(rows))
					return
				}
			}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}
