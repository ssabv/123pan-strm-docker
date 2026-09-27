package app

// 本地已删清理（反向同步）：Emby 里删除媒体后会连本地的 .strm / 字幕一起删掉，
// 这里把「秒传库里有记录、但本地对应文件已不存在」的条目找出来，
// 经人工勾选确认后从秒传库 JSON 中移除（只动库记录，不碰云盘文件）。

import (
	"path/filepath"
	"strings"
)

// orphanScan: 扫描全部库，返回本地已删条目清单（不执行删除）
func (a *App) orphanScan(outputDir string, includeSubtitles bool) map[string]any {
	cfg := a.cfg.Config()
	if outputDir == "" {
		outputDir = asString(cfg["output_dir"])
	}
	if outputDir == "" {
		outputDir = a.cfg.DefaultOutDir
	}
	outRootDir := filepath.Clean(outputDir)

	// 本地现有 .strm + 字幕，一次 walk 建索引
	existingStrm, existingSubs := getExistingFiles(outRootDir)
	existSet := map[string]bool{}
	for _, p := range existingStrm {
		existSet[p] = true
	}
	for _, p := range existingSubs {
		existSet[p] = true
	}

	var results []map[string]any
	totalMissing := 0
	totalChecked := 0
	for _, info := range a.cfg.ListLibraries() {
		id, _ := info["id"].(string)
		if id == "" {
			continue
		}
		lib, err := a.cfg.LoadLib(id)
		if err != nil {
			continue
		}
		catRoot := a_catRoot(outputDir, asString(lib["category"]))
		files, _ := lib["files"].([]any)
		var missing []map[string]any
		total := 0
		for _, f := range files {
			fm, ok := f.(map[string]any)
			if !ok {
				continue
			}
			rel := safeRelPath(asString(fm["path"]))
			if rel == "" {
				continue
			}
			ext := strings.ToLower(filepath.Ext(rel))
			var expected, kind string
			if VIDEO_EXTS[ext] {
				expected = filepath.Join(catRoot, relWithoutSuffix(rel, ext)+".strm")
				kind = "video"
			} else if includeSubtitles && SUBTITLE_EXTS[ext] {
				// 字幕只在开启"包含字幕"时才应该存在于本地，否则全部误报
				expected = filepath.Join(catRoot, rel)
				kind = "sub"
			} else {
				continue
			}
			total++
			totalChecked++
			if !existSet[expected] {
				missing = append(missing, map[string]any{
					"path": asString(fm["path"]),
					"size": firstInt64(fm, "size"),
					"kind": kind,
				})
			}
		}
		if len(missing) == 0 {
			continue
		}
		ratio := 0.0
		if total > 0 {
			ratio = float64(len(missing)) / float64(total)
		}
		// 按顶层目录（剧/电影名）分组，方便按作品勾选
		groups := map[string][]map[string]any{}
		var groupOrder []string
		for _, m := range missing {
			dir := orphanTopDir(asString(m["path"]))
			if _, ok := groups[dir]; !ok {
				groupOrder = append(groupOrder, dir)
			}
			groups[dir] = append(groups[dir], m)
		}
		var groupsOut []map[string]any
		for _, dir := range groupOrder {
			g := groups[dir]
			var gsize int64
			for _, m := range g {
				gsize += firstInt64(m, "size")
			}
			groupsOut = append(groupsOut, map[string]any{
				"dir":   dir,
				"count": len(g),
				"size":  gsize,
				"files": g,
			})
		}
		results = append(results, map[string]any{
			"lib_id":        asString(lib["id"]),
			"lib_name":      asString(lib["name"]),
			"total":         total,
			"missing_count": len(missing),
			"missing_ratio": ratio,
			// 缺失超过一半：大概率是输出目录变了 / 磁盘没挂载，防误删标记
			"suspicious": total > 0 && ratio > 0.5,
			"groups":     groupsOut,
		})
		totalMissing += len(missing)
	}
	if results == nil {
		results = []map[string]any{}
	}
	return map[string]any{
		"libraries":     results,
		"total_missing": totalMissing,
		"total_checked": totalChecked,
		"output_dir":    outRootDir,
	}
}

// orphanTopDir: 取云盘相对路径的顶层目录名（即剧/电影名），无目录层级归"根目录"
func orphanTopDir(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if i := strings.Index(p, "/"); i > 0 {
		return p[:i]
	}
	return "(根目录)"
}

// orphanApply: 从秒传库移除勾选条目——与去重应用逻辑完全一致（备份后写回、重建 idx），直接复用
func (a *App) orphanApply(libID string, deletePaths []string) map[string]any {
	return a.dedupApply(libID, deletePaths)
}
