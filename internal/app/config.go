package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 库列表摘要缓存：库文件动辄几十 MB，列表接口只需 id/名称/文件数，
// 命中时按 (mtime, size) 判定，避免每次都完整反序列化
type libSummary struct {
	mtime time.Time
	size  int64
	row   map[string]any
}

type Config struct {
	DataDir           string
	LibDir            string
	ConfigFile        string
	SettingsPath      string
	CachePath         string
	DefaultOutDir     string
	IndexHTMLPath     string
	DefaultHost       string
	DefaultPort       string
	DefaultServerBase string

	settings map[string]any

	libSumMu sync.Mutex
	libSums  map[string]libSummary
}

func NewConfig(baseDir string) *Config {
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		if _, err := os.Stat("/data"); err == nil {
			dataDir = "/data"
		} else {
			dataDir = filepath.Join(baseDir, "strm_data")
		}
	}
	dataDir = expandPath(dataDir)

	libDir := filepath.Join(dataDir, "libraries")
	configFile := filepath.Join(dataDir, "config.json")
	settingsPath := os.Getenv("SETTINGS_PATH")
	if settingsPath == "" {
		settingsPath = filepath.Join(dataDir, "settings.yaml")
	}
	cachePath := os.Getenv("CACHE_PATH")
	if cachePath == "" {
		cachePath = filepath.Join(dataDir, "cache.json")
	}
	defaultOut := os.Getenv("STRM_OUTPUT_DIR")
	if defaultOut == "" {
		if _, err := os.Stat("/strm"); err == nil {
			defaultOut = "/strm"
		} else {
			defaultOut = filepath.Join(baseDir, "STRM输出")
		}
	}
	defaultHost := os.Getenv("HOST")
	if defaultHost == "" {
		defaultHost = "0.0.0.0"
	}
	defaultPort := os.Getenv("PORT")
	if defaultPort == "" {
		defaultPort = "8000"
	}
	serverBase := os.Getenv("SERVER_BASE")

	return &Config{
		DataDir:           dataDir,
		LibDir:            libDir,
		ConfigFile:        configFile,
		SettingsPath:      settingsPath,
		CachePath:         cachePath,
		DefaultOutDir:     defaultOut,
		IndexHTMLPath:     filepath.Join(baseDir, "index.html"),
		DefaultHost:       defaultHost,
		DefaultPort:       defaultPort,
		DefaultServerBase: serverBase,
	}
}

func expandPath(p string) string {
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

func (c *Config) initDirs() {
	os.MkdirAll(c.DataDir, 0o755)
	os.MkdirAll(c.LibDir, 0o755)
}

func (c *Config) EnsureSettingsYAML(builtin []byte) {
	if _, err := os.Stat(c.SettingsPath); err == nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.SettingsPath), 0o755); err == nil {
		os.WriteFile(c.SettingsPath, builtin, 0o644)
	}
}

func (c *Config) EnsureCacheFile() {
	os.MkdirAll(filepath.Dir(c.CachePath), 0o755)
	if _, err := os.Stat(c.CachePath); os.IsNotExist(err) {
		data := map[string]any{
			"accessToken":     "",
			"tokenCreateTime": "",
			"lastDeleteTime":  "",
			"accountHash":     "",
		}
		b, _ := json.MarshalIndent(data, "", "  ")
		os.WriteFile(c.CachePath, b, 0o644)
	}
}

func (c *Config) loadSettingsYAML() map[string]any {
	c.settings = LoadYAMLMap(c.SettingsPath)
	if c.settings == nil {
		c.settings = map[string]any{}
	}
	return c.settings
}

func (c *Config) Settings() map[string]any {
	if c.settings == nil {
		return c.loadSettingsYAML()
	}
	return c.settings
}

func (c *Config) getIntSetting(key string, def int) int {
	v := c.Settings()[key]
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		if n, err := strconv.Atoi(t); err == nil {
			return n
		}
	}
	return def
}

func (c *Config) defaultServerBase() string {
	if c.DefaultServerBase != "" {
		return c.DefaultServerBase
	}
	return "http://127.0.0.1:" + c.DefaultPort
}

func (c *Config) Config() map[string]any {
	out := map[string]any{}
	if _, err := os.Stat(c.ConfigFile); err == nil {
		out = ReadJSONFile(c.ConfigFile)
	}
	if out == nil {
		out = map[string]any{}
	}
	if _, ok := out["output_dir"]; !ok {
		out["output_dir"] = c.DefaultOutDir
	}
	if _, ok := out["server_base"]; !ok {
		out["server_base"] = c.defaultServerBase()
	}
	if _, ok := out["include_subtitles"]; !ok {
		out["include_subtitles"] = false
	}
	if _, ok := out["mode"]; !ok {
		out["mode"] = "cache"
	}
	if _, ok := out["pan_username"]; !ok {
		out["pan_username"] = c.Settings()["123PAN_USERNAME"]
	}
	if _, ok := out["pan_password"]; !ok {
		out["pan_password"] = c.Settings()["123PAN_PASSWORD"]
	}
	if _, ok := out["cache_folder_id"]; !ok {
		out["cache_folder_id"] = int64(c.getIntSetting("CACHE_FOLDER_ID", 0))
	}
	if _, ok := out["cache_folder_name"]; !ok {
		out["cache_folder_name"] = ""
	}
	return out
}

func (c *Config) SaveConfig(v map[string]any) {
	WriteJSONFile(c.ConfigFile, v)
}

func (c *Config) UpdateSettingsAccount(username, password string) {
	data := c.Settings()
	ou, _ := data["123PAN_USERNAME"].(string)
	op, _ := data["123PAN_PASSWORD"].(string)
	data["123PAN_USERNAME"] = username
	data["123PAN_PASSWORD"] = password
	WriteYAMLFile(c.SettingsPath, data)
	if ou != username || op != password {
		if _, err := os.Stat(c.CachePath); err == nil {
			os.Remove(c.CachePath)
		}
	}
	c.settings = data
}

func (c *Config) LibPath(libID string) string {
	return filepath.Join(c.LibDir, safeName(libID)+".json")
}

func (c *Config) LoadLib(libID string) (map[string]any, error) {
	p := c.LibPath(libID)
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var d map[string]any
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	return d, nil
}

func (c *Config) NormalizeLibrary(raw any, name, category string) (map[string]any, error) {
	var files []any
	var common string
	var meta map[string]any
	switch t := raw.(type) {
	case map[string]any:
		f, ok := t["files"].([]any)
		if !ok {
			return nil, errUnsupportedFormat
		}
		files = f
		common, _ = t["commonPath"].(string)
		meta = map[string]any{}
		for k, v := range t {
			if k != "files" {
				meta[k] = v
			}
		}
	case []any:
		files = t
	default:
		return nil, errUnsupportedFormat
	}

	out := []FileInfo{}
	for _, item := range files {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		pathv := firstString(m, "path", "Path", "name", "FileName", "filename")
		etag := firstString(m, "etag", "Etag", "ETag", "md5", "hash")
		size := firstInt64(m, "size", "Size")
		if pathv == "" || etag == "" {
			continue
		}
		out = append(out, FileInfo{
			Idx:  len(out),
			Path: strings.ReplaceAll(pathv, "\\", "/"),
			Etag: etag,
			Size: size,
		})
	}

	metaName, _ := meta["commonPath"].(string)
	if metaName == "" {
		metaName, _ = meta["name"].(string)
	}
	baseName := name
	if baseName == "" {
		baseName = metaName
	}
	lid := safeName(strings.Trim(baseName, "/\\"))
	if lid == "" {
		lid = safeName("library_" + strconvItoa(time.Now().Unix()))
	}
	libName := strings.Trim(name, "/\\")
	if libName == "" {
		libName = strings.Trim(metaName, "/\\")
	}
	if libName == "" {
		libName = lid
	}
	return map[string]any{
		"id":         lid,
		"name":       libName,
		"commonPath": common,
		"createdAt":  time.Now().Unix(),
		"meta":       meta,
		"files":      out,
		"category":   category,
	}, nil
}

func (c *Config) ListLibraries() []map[string]any {
	rows := []map[string]any{}
	entries, err := filepath.Glob(filepath.Join(c.LibDir, "*.json"))
	if err != nil {
		return rows
	}
	type fileStat struct {
		id  string
		mod time.Time
		sz  int64
	}
	stats := make([]fileStat, 0, len(entries))
	for _, p := range entries {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		stats = append(stats, fileStat{
			id:  strings.TrimSuffix(filepath.Base(p), ".json"),
			mod: fi.ModTime(),
			sz:  fi.Size(),
		})
	}
	// 按修改时间倒序
	sort.Slice(stats, func(i, j int) bool { return stats[i].mod.After(stats[j].mod) })
	for _, s := range stats {
		if row := c.libSummaryCached(s.id, s.mod, s.sz); row != nil {
			rows = append(rows, row)
		}
	}
	return rows
}

// 命中 (mtime, size) 一致的缓存则直接返回，否则重新解析并缓存
func (c *Config) libSummaryCached(id string, mod time.Time, size int64) map[string]any {
	c.libSumMu.Lock()
	if s, ok := c.libSums[id]; ok && s.size == size && s.mtime.Equal(mod) {
		c.libSumMu.Unlock()
		return s.row
	}
	c.libSumMu.Unlock()

	d, err := c.LoadLib(id)
	if err != nil {
		return nil
	}
	libID, _ := d["id"].(string)
	if libID == "" {
		libID = id
	}
	name, _ := d["name"].(string)
	if name == "" {
		name = libID
	}
	cat, _ := d["category"].(string)
	fileList, _ := d["files"].([]any)
	total := len(fileList)
	video := 0
	for _, f := range fileList {
		if fm, ok := f.(map[string]any); ok {
			fp, _ := fm["path"].(string)
			if VIDEO_EXTS[strings.ToLower(filepath.Ext(fp))] {
				video++
			}
		}
	}
	row := map[string]any{
		"id":        libID,
		"name":      name,
		"total":     total,
		"video":     video,
		"createdAt": d["createdAt"],
		"category":  cat,
	}

	c.libSumMu.Lock()
	if c.libSums == nil {
		c.libSums = map[string]libSummary{}
	}
	c.libSums[id] = libSummary{mtime: mod, size: size, row: row}
	c.libSumMu.Unlock()
	return row
}

// 库文件被改写后丢弃对应缓存，避免出现旧名称/旧文件数
func (c *Config) invalidateLibSummary(p string) {
	id := strings.TrimSuffix(filepath.Base(p), ".json")
	c.libSumMu.Lock()
	delete(c.libSums, id)
	c.libSumMu.Unlock()
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

func firstInt64(m map[string]any, keys ...string) int64 {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case int64:
				return t
			case int:
				return int64(t)
			case int32:
				return int64(t)
			case float64:
				return int64(t)
			case float32:
				return int64(t)
			case string:
				var n int64
				for _, ch := range t {
					if ch < '0' || ch > '9' {
						return 0
					}
				}
				if len(t) > 0 {
					n = parseInt(t)
					return n
				}
			}
		}
	}
	return 0
}
