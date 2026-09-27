package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 回归：本地已删清理（反向同步）的扫描与应用
func TestOrphanScanAndApply(t *testing.T) {
	base := t.TempDir()
	cfg := NewConfig(base)
	cfg.initDirs()
	a := NewApp(cfg)

	out := filepath.Join(base, "out")
	// 本地只存在: 电影/A (2010)/a.strm；其余全部缺失（模拟 Emby 删除后）
	mk := func(rel string) {
		p := filepath.Join(out, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("http://x"), 0o644)
	}
	mk(filepath.Join("电影", "A (2010)", "a.strm"))
	mk(filepath.Join("电影", "E (2013)", "e.strm"))

	movieLib := map[string]any{
		"id":       "test-lib",
		"name":     "测试库",
		"category": "电影",
		"files": []any{
			map[string]any{"path": "A (2010)/a.mkv", "size": int64(100), "etag": "e1"},
			map[string]any{"path": "C (2011)/c.mkv", "size": int64(200), "etag": "e2"},
			map[string]any{"path": "C (2011)/c.srt", "size": int64(10), "etag": "e3"},
			map[string]any{"path": "d.mkv", "size": int64(30), "etag": "e6"},
			map[string]any{"path": "E (2013)/e.mkv", "size": int64(40), "etag": "e7"},
		},
	}
	tvLib := map[string]any{
		"id":       "tv-lib",
		"name":     "剧集库",
		"category": "剧集",
		"files": []any{
			map[string]any{"path": "B/Season 1/b.mkv", "size": int64(300), "etag": "e5"},
		},
	}
	cfg.writeLibraryFile(cfg.LibPath("test-lib"), movieLib)
	cfg.writeLibraryFile(cfg.LibPath("tv-lib"), tvLib)

	// includeSubtitles 无关：字幕与视频一视同仁，本地缺了就算孤儿
	res := a.orphanScan(out)
	if got := res["total_missing"].(int); got != 4 { // c.mkv + c.srt + d.mkv + b.mkv
		t.Fatalf("期望 4 个缺失, 实际 %d", got)
	}

	libs := res["libraries"].([]map[string]any)
	byID := map[string]map[string]any{}
	for _, l := range libs {
		byID[l["lib_id"].(string)] = l
	}

	// 电影库: 缺 3/5 个文件 (a/e 在, c.mkv+c.srt+d 缺) → ratio 0.6 标记 suspicious
	ml := byID["test-lib"]
	if ml == nil {
		t.Fatal("电影库应出现在结果里")
	}
	if ml["missing_count"].(int) != 3 || !ml["suspicious"].(bool) {
		t.Fatalf("电影库期望 missing=3 suspicious=true, 实际 missing=%v suspicious=%v", ml["missing_count"], ml["suspicious"])
	}
	// 分组: "C (2011)" 含 c.mkv+c.srt，根目录文件归"(根目录)"
	dirSet := map[string]int{}
	for _, g := range ml["groups"].([]map[string]any) {
		dirSet[g["dir"].(string)] = g["count"].(int)
	}
	if dirSet["C (2011)"] != 2 || dirSet["(根目录)"] != 1 {
		t.Fatalf("电影库分组错误: %v", dirSet)
	}

	// 剧集库: 唯一文件缺失 → ratio 1.0 → suspicious（防目录变更/磁盘未挂载误删）
	tl := byID["tv-lib"]
	if tl == nil || !tl["suspicious"].(bool) {
		t.Fatalf("剧集库(全部缺失)应标记 suspicious")
	}

	// 应用：移除 c.mkv 后库文件应剩 3 个，且生成 .bak 备份
	r := a.orphanApply("test-lib", []string{"C (2011)/c.mkv"})
	if !r["ok"].(bool) || r["removed"].(int) != 1 {
		t.Fatalf("orphanApply 期望 removed=1, 实际 %v", r)
	}
	lib, _ := cfg.LoadLib("test-lib")
	files, _ := lib["files"].([]any)
	if len(files) != 4 {
		t.Fatalf("应用后库应剩 4 个文件, 实际 %d", len(files))
	}
	entries, _ := filepath.Glob(cfg.LibPath("test-lib") + ".bak.*")
	if len(entries) == 0 {
		t.Fatal("应用前应自动备份库 JSON")
	}

	// 重复应用同一批（幂等场景）：返回 ok 且不误删
	r2 := a.orphanApply("test-lib", []string{"C (2011)/c.mkv"})
	if !r2["ok"].(bool) || r2["removed"].(int) != 0 {
		t.Fatalf("重复应用期望 removed=0, 实际 %v", r2)
	}

	// idx 重建后应连续
	for i, f := range files {
		fm := f.(map[string]any)
		if int(firstInt64(fm, "idx")) != i {
			t.Fatalf("idx 应重建为连续序列, files[%d].idx=%v", i, fm["idx"])
		}
	}
}

// orphanTopDir: 顶层目录提取
func TestOrphanTopDir(t *testing.T) {
	if got := orphanTopDir("剧名 (2020)/S01/e01.mkv"); got != "剧名 (2020)" {
		t.Fatalf("期望 剧名 (2020), 实际 %s", got)
	}
	if got := orphanTopDir(`剧名\2020\e01.mkv`); got != "剧名" {
		t.Fatalf("反斜杠路径应归一化, 实际 %s", got)
	}
	if got := orphanTopDir("file.mkv"); got != "(根目录)" {
		t.Fatalf("无目录层级应归根目录, 实际 %s", got)
	}
	if !strings.Contains(orphanTopDir("a/b"), "a") {
		t.Fatal("普通路径应取第一段")
	}
}
