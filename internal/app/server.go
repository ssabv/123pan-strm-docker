package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // 内嵌时区数据库, 容器内无需安装 tzdata 也可识别 TZ
)

// ensure_cache_file: 确保 cache.json 存在
func (a *App) ensureCacheFile() {
	a.cfg.EnsureCacheFile()
}

func (a *App) getPanDriver(forceLogin bool) (*Pan123, error) {
	settingsData := LoadYAMLMap(a.cfg.SettingsPath)
	if settingsData == nil {
		settingsData = map[string]any{}
	}
	username, _ := settingsData["123PAN_USERNAME"].(string)
	password, _ := settingsData["123PAN_PASSWORD"].(string)
	driver := NewPan123()
	a.ensureCacheFile()
	cacheData := ReadJSONFile(a.cfg.CachePath)
	if cacheData == nil {
		cacheData = map[string]any{}
	}
	if !forceLogin {
		if tok, _ := cacheData["accessToken"].(string); tok != "" {
			if ct, ok := cacheData["tokenCreateTime"]; ok {
				var t int64
				switch v := ct.(type) {
				case float64:
					t = int64(v)
				case int64:
					t = v
				case string:
					t = parseInt(v)
				}
				if time.Now().Unix()-t < 25*24*60*60 {
					driver.setAccessToken(tok)
				}
			}
		}
	}
	if driver.getAccessToken() == "" {
		if !driver.doLogin(username, password) {
			return nil, fmt.Errorf("123 网盘登录失败，请检查账号密码")
		}
		cacheData["accessToken"] = driver.getAccessToken()
		cacheData["tokenCreateTime"] = float64(time.Now().Unix())
		WriteJSONFile(a.cfg.CachePath, cacheData)
	}
	return driver, nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	b, _ := json.Marshal(v)
	w.Write(b)
}

func (a *App) handler() http.Handler {
	mux := http.NewServeMux()

	// 首页
	mux.HandleFunc("/", a.handleIndex)

	// 配置
	mux.HandleFunc("/api/config", a.handleConfig)
	mux.HandleFunc("/api/libraries", a.handleLibraries)
	mux.HandleFunc("/api/libraries/", a.handleLibraryItem)
	mux.HandleFunc("/api/generate", a.handleGenerate)
	mux.HandleFunc("/api/sync/all", a.handleSyncAll)
	mux.HandleFunc("/api/dedup/scan", a.handleDedupScan)
	mux.HandleFunc("/api/dedup/apply", a.handleDedupApply)
	mux.HandleFunc("/api/pan/list", a.handlePanList)
	mux.HandleFunc("/api/pan/export", a.handlePanExport)
	mux.HandleFunc("/api/task/", a.handleTaskStatus)
	mux.HandleFunc("/api/mode", a.handleMode)
	mux.HandleFunc("/api/backup", a.handleBackup)
	mux.HandleFunc("/api/restore/upload", a.handleRestoreUpload)

	// 播放
	mux.HandleFunc("/play/", a.handlePlay)

	// 定时归档
	mux.HandleFunc("/api/archive/jobs", a.handleArchiveJobs)
	mux.HandleFunc("/api/archive/jobs/", a.handleArchiveJobItem)
	mux.HandleFunc("/archive", a.handleArchivePage)

	return a.cors(gzipHandler(mux))
}

func (a *App) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if _, err := os.Stat(a.cfg.IndexHTMLPath); err == nil {
		b, err := os.ReadFile(a.cfg.IndexHTMLPath)
		if err == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(b)
			return
		}
	}
	if len(indexHTMLBytes) > 0 {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTMLBytes)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte("<h1>123 STRM API</h1><p>Place index.html in project dir or <a href='/api/libraries'>view API</a></p>"))
}

// GET/POST /api/config
func (a *App) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := a.cfg.Config()
		cfg["ok"] = true
		writeJSON(w, 200, cfg)
	case http.MethodPost:
		var req ConfigReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		c := a.cfg.Config()
		if req.OutputDir != "" {
			c["output_dir"] = req.OutputDir
		}
		if req.ServerBase != "" {
			c["server_base"] = req.ServerBase
		}
		if req.PanUsername != "" {
			c["pan_username"] = req.PanUsername
		}
		if req.PanPassword != "" {
			c["pan_password"] = req.PanPassword
		}
		if req.CacheFolderID != nil {
			c["cache_folder_id"] = *req.CacheFolderID
			if req.CacheFolderName != "" {
				c["cache_folder_name"] = req.CacheFolderName
			} else if *req.CacheFolderID == 0 {
				c["cache_folder_name"] = ""
			}
		}
		c["include_subtitles"] = req.IncludeSubtitles
		a.cfg.SaveConfig(c)
		a.cfg.UpdateSettingsAccount(asString(c["pan_username"]), asString(c["pan_password"]))
		a.ensureCacheFile()
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// GET/POST /api/libraries
func (a *App) handleLibraries(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, map[string]any{"items": a.cfg.ListLibraries()})
	case http.MethodPost:
		var req SaveReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		lib, err := a.cfg.NormalizeLibrary(req.Content, req.Name, req.Category)
		if err != nil {
			writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		p := a.cfg.LibPath(asString(lib["id"]))
		if _, err := os.Stat(p); err == nil {
			lib["id"] = safeName(fmt.Sprintf("%s_%d", asString(lib["id"]), time.Now().Unix()))
			p = a.cfg.LibPath(asString(lib["id"]))
		}
		a.cfg.writeLibraryFile(p, lib)
		writeJSON(w, 200, map[string]any{"ok": true, "id": lib["id"], "files": filesCount(lib["files"])})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// GET/PUT/DELETE /api/libraries/{lib_id}
func (a *App) handleLibraryItem(w http.ResponseWriter, r *http.Request) {
	libID := strings.TrimPrefix(r.URL.Path, "/api/libraries/")
	switch r.Method {
	case http.MethodGet:
		lib, err := a.cfg.LoadLib(libID)
		if err != nil {
			writeJSON(w, 404, map[string]any{"ok": false, "error": "library not found"})
			return
		}
		writeJSON(w, 200, lib)
	case http.MethodPut:
		var req UpdateLibReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		lib, err := a.cfg.LoadLib(libID)
		if err != nil {
			writeJSON(w, 404, map[string]any{"ok": false, "error": "library not found"})
			return
		}
		oldPath := a.cfg.LibPath(libID)
		if req.Name != "" {
			newID := safeName(strings.Trim(req.Name, "/\\"))
			lib["name"] = strings.Trim(req.Name, "/\\")
			if newID != libID {
				lib["id"] = newID
				newPath := a.cfg.LibPath(newID)
				if _, err := os.Stat(newPath); err == nil && newPath != oldPath {
					lib["id"] = safeName(fmt.Sprintf("%s_%d", newID, time.Now().Unix()))
					newPath = a.cfg.LibPath(asString(lib["id"]))
				}
				os.Rename(oldPath, newPath)
				oldPath = newPath
			}
		}
		if req.Category != "" {
			lib["category"] = req.Category
		}
		if req.Files != nil {
			files := asAnySlice(req.Files)
			for i, f := range files {
				if fm, ok := f.(map[string]any); ok {
					fm["idx"] = i
				}
			}
			lib["files"] = files
		}
		if req.CommonPath != nil {
			lib["commonPath"] = *req.CommonPath
		}
		a.cfg.writeLibraryFile(oldPath, lib)
		writeJSON(w, 200, map[string]any{"ok": true, "id": lib["id"], "files": filesCount(lib["files"])})
	case http.MethodDelete:
		p := a.cfg.LibPath(libID)
		os.Remove(p)
		a.cfg.invalidateLibSummary(p)
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func asAnySlice(v any) []any {
	switch t := v.(type) {
	case []any:
		return t
	case nil:
		return nil
	}
	return nil
}

func filesCount(v any) int {
	switch t := v.(type) {
	case []any:
		return len(t)
	case []FileInfo:
		return len(t)
	case nil:
		return 0
	}
	return 0
}

// POST /api/generate
func (a *App) handleGenerate(w http.ResponseWriter, r *http.Request) {
	var req GenReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	c := a.cfg.Config()
	out := req.OutputDir
	if out == "" {
		out = asString(c["output_dir"])
	}
	if out == "" {
		out = a.cfg.DefaultOutDir
	}
	base := req.ServerBase
	if base == "" {
		base = asString(c["server_base"])
	}
	if base == "" {
		base = a.cfg.defaultServerBase()
	}
	c["output_dir"] = out
	c["server_base"] = base
	a.cfg.SaveConfig(c)
	taskID := a.startTask("generate", a.generateStrmTask(req.LibID, out, base, req.IncludeSubtitles))
	writeJSON(w, 200, map[string]any{"ok": true, "task_id": taskID})
}

// POST /api/strm/sync
func (a *App) handleSyncAll(w http.ResponseWriter, r *http.Request) {
	var req *GenReq
	if r.Body != nil {
		req = &GenReq{}
		if err := json.NewDecoder(r.Body).Decode(req); err != nil {
			req = nil
		}
	}
	var out, base string
	incSub := false
	if req != nil {
		out = req.OutputDir
		base = req.ServerBase
		incSub = req.IncludeSubtitles
	}
	taskID := a.startTask("sync_all", a.syncAllStrmTask(out, base, incSub))
	writeJSON(w, 200, map[string]any{"ok": true, "task_id": taskID})
}

// POST /api/dedup/scan
func (a *App) handleDedupScan(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.dedupScanAll())
}

// POST /api/dedup/apply
func (a *App) handleDedupApply(w http.ResponseWriter, r *http.Request) {
	var req DedupReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, a.dedupApply(req.LibID, req.DeletePaths))
}

// GET /api/pan/list?parentFileId=0
func (a *App) handlePanList(w http.ResponseWriter, r *http.Request) {
	parentFileID := int64(0)
	if v := r.URL.Query().Get("parentFileId"); v != "" {
		parentFileID, _ = strconv.ParseInt(v, 10, 64)
	}
	driver, err := a.getPanDriver(false)
	if err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	res := driver.listFilesSingle(parentFileID)
	if e, ok := res["error"]; ok {
		writeJSON(w, 400, map[string]any{"ok": false, "error": e})
		return
	}
	items := []map[string]any{}
	for _, it := range asAnySlice(res["items"]) {
		im, _ := it.(map[string]any)
		items = append(items, map[string]any{
			"fileId": im["FileId"],
			"name":   im["FileName"],
			"type":   im["Type"],
			"size":   im["Size"],
			"etag":   im["Etag"],
		})
	}
	writeJSON(w, 200, map[string]any{"ok": true, "items": items})
}

// POST /api/pan/export
func (a *App) handlePanExport(w http.ResponseWriter, r *http.Request) {
	var req PanExportReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	driver, err := a.getPanDriver(false)
	if err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	taskID := a.startTask("pan_export", a.panExportTask(driver, req.Folders, req.Files))
	writeJSON(w, 200, map[string]any{"ok": true, "task_id": taskID})
}

// GET /api/task/{task_id}
func (a *App) handleTaskStatus(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimPrefix(r.URL.Path, "/api/task/")
	t := a.getTask(taskID)
	if t == nil {
		writeJSON(w, 404, map[string]any{"ok": false, "error": "任务不存在或已过期"})
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok":       true,
		"state":    t.State,
		"progress": t.Progress,
		"message":  t.Message,
		"result":   t.Result,
		"error":    t.Error,
	})
}

// GET/POST /api/mode
func (a *App) handleMode(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		c := a.cfg.Config()
		mode, _ := c["mode"].(string)
		if mode == "" {
			mode = "cache"
		}
		writeJSON(w, 200, map[string]any{"mode": mode})
	case http.MethodPost:
		var req ModeReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if req.Mode != "cache" && req.Mode != "fast" {
			writeJSON(w, 400, map[string]any{"ok": false, "error": "invalid mode"})
			return
		}
		c := a.cfg.Config()
		c["mode"] = req.Mode
		a.cfg.SaveConfig(c)
		label := "缓存模式(24小时清理)"
		if req.Mode == "fast" {
			label = "入库模式(1分钟清理)"
		}
		writeJSON(w, 200, map[string]any{"ok": true, "mode": req.Mode, "label": label})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// GET /api/backup
func (a *App) handleBackup(w http.ResponseWriter, r *http.Request) {
	libs := []map[string]any{}
	entries, _ := filepath.Glob(filepath.Join(a.cfg.LibDir, "*.json"))
	for _, p := range entries {
		if d := ReadJSONFile(p); d != nil {
			libs = append(libs, d)
		}
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="backup-%d.json"`, time.Now().Unix()))
	writeJSON(w, 200, map[string]any{
		"version":    1,
		"exportedAt": time.Now().Unix(),
		"app":        "123pan-strm-docker",
		"libraries":  libs,
	})
}

// POST /api/restore/upload
func (a *App) handleRestoreUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "error": "parse error"})
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "error": "parse error"})
		return
	}
	defer file.Close()
	b, _ := io.ReadAll(file)
	var data map[string]any
	if err := json.Unmarshal(b, &data); err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "error": "parse error"})
		return
	}
	if data["app"] != "123pan-strm-docker" {
		writeJSON(w, 400, map[string]any{"ok": false, "error": "invalid format"})
		return
	}
	restored := 0
	skipped := 0
	for _, lib := range asAnySlice(data["libraries"]) {
		lm, ok := lib.(map[string]any)
		if !ok {
			continue
		}
		if _, has := lm["category"]; !has {
			lm["category"] = ""
		}
		if _, has := lm["mode"]; !has {
			lm["mode"] = "cache"
		}
		lid := asString(lm["id"])
		if lid == "" {
			lid = asString(lm["name"])
		}
		if lid == "" {
			continue
		}
		p := a.cfg.LibPath(lid)
		if _, err := os.Stat(p); err == nil {
			skipped++
			continue
		}
		a.cfg.writeLibraryFile(p, lm)
		restored++
	}
	writeJSON(w, 200, map[string]any{"ok": true, "restored": restored, "skipped": skipped})
}

// GET /play/{file_id}/{etag}/{size}/{filename} 以及 /play/{lib_id}/{idx}
func (a *App) handlePlay(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/play/")
	parts := strings.Split(rest, "/")
	if len(parts) == 2 {
		// legacy: /play/{lib_id}/{idx}
		libID := parts[0]
		idx, err := strconv.Atoi(parts[1])
		if err != nil {
			writeJSON(w, 404, map[string]any{"ok": false, "error": "not found"})
			return
		}
		lib, err := a.cfg.LoadLib(libID)
		if err != nil {
			writeJSON(w, 404, map[string]any{"ok": false, "error": "not found"})
			return
		}
		files := asAnySlice(lib["files"])
		if idx < 0 || idx >= len(files) {
			writeJSON(w, 404, map[string]any{"ok": false, "error": "not found"})
			return
		}
		fm, _ := files[idx].(map[string]any)
		name := filepath.Base(safeRelPath(asString(fm["path"])))
		cfg := a.cfg.Config()
		fastMode := asString(cfg["mode"]) == "fast"
		url := a.getFileURLWithEtagCandidates(name, asString(fm["etag"]), firstInt64(fm, "size"), fastMode)
		if url == "" {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("failed"))
			return
		}
		http.Redirect(w, r, url, http.StatusFound)
		return
	}
	// 新格式: /play/{file_id}/{etag}/{size}/{filename}
	if len(parts) >= 4 {
		_, err1 := strconv.Atoi(parts[0])
		etag := parts[1]
		size, err2 := strconv.ParseInt(parts[2], 10, 64)
		filename := strings.Join(parts[3:], "/")
		if err1 != nil || err2 != nil {
			writeJSON(w, 404, map[string]any{"ok": false, "error": "not found"})
			return
		}
		cfg := a.cfg.Config()
		fastMode := asString(cfg["mode"]) == "fast"
		url := a.getFileURLWithEtagCandidates(filename, etag, size, fastMode)
		if url == "" {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("failed"))
			return
		}
		http.Redirect(w, r, url, http.StatusFound)
		return
	}
	writeJSON(w, 404, map[string]any{"ok": false, "error": "not found"})
}

func RunServer() {
	// 时区: 优先使用 TZ 环境变量(compose 里 TZ=Asia/Shanghai), 保证 cron/库名时间与本地一致
	if tz := os.Getenv("TZ"); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			time.Local = loc
			fmt.Printf("时区: %s\n", tz)
		} else {
			fmt.Printf("警告: TZ=%s 解析失败: %v\n", tz, err)
		}
	}
	baseDir, err := os.Getwd()
	if err != nil {
		baseDir = "."
	}
	cfg := NewConfig(baseDir)
	cfg.initDirs()
	cfg.EnsureSettingsYAML(settingsYAMLBytes)
	cfg.EnsureCacheFile()
	app := NewApp(cfg)
	app.startArchiveScheduler()
	addr := "0.0.0.0:" + cfg.DefaultPort
	if host := os.Getenv("HOST"); host != "" {
		addr = host + ":" + cfg.DefaultPort
	}
	fmt.Printf("STRM API: http://127.0.0.1:%s/\n", cfg.DefaultPort)
	fmt.Printf("定时归档: http://127.0.0.1:%s/archive\n", cfg.DefaultPort)
	http.ListenAndServe(addr, app.handler())
}

// ---------- 定时归档 API ----------

// GET/POST /api/archive/jobs
func (a *App) handleArchiveJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, map[string]any{"items": a.LoadArchiveJobs()})
	case http.MethodPost:
		var job ArchiveJob
		if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
			writeJSON(w, 400, map[string]any{"ok": false, "error": "invalid job: " + err.Error()})
			return
		}
		saved, err := a.SaveArchiveJob(job)
		if err != nil {
			writeJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "id": saved.ID})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// DELETE /api/archive/jobs/{id} | POST /api/archive/jobs/{id}/run | GET /api/archive/jobs/{id}/runs
func (a *App) handleArchiveJobItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/archive/jobs/")
	parts := strings.Split(rest, "/")
	id := parts[0]
	if id == "" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if len(parts) == 1 {
		if r.Method == http.MethodDelete {
			a.DeleteArchiveJob(id)
			writeJSON(w, 200, map[string]any{"ok": true})
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	switch parts[1] {
	case "run":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		job := a.LoadArchiveJob(id)
		if job == nil {
			writeJSON(w, 404, map[string]any{"ok": false, "error": "job not found"})
			return
		}
		jobCopy := *job
		taskID := a.startTask("archive_"+jobCopy.ID, func(emit func(map[string]any)) {
			a.archiveJobRun(jobCopy, emit)
		})
		writeJSON(w, 200, map[string]any{"ok": true, "task_id": taskID})
	case "runs":
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, 200, map[string]any{"items": a.LoadArchiveRuns(id, 50)})
	default:
		writeJSON(w, 404, map[string]any{"ok": false, "error": "not found"})
	}
}

// GET /archive 定时归档页面
func (a *App) handleArchivePage(w http.ResponseWriter, r *http.Request) {
	if len(archiveHTMLBytes) > 0 {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(archiveHTMLBytes)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte("<h1>定时归档</h1><p>archive.html 未嵌入</p>"))
}
