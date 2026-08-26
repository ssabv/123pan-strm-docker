package app

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

func accountHash(username, password string) string {
	raw := username + "\n" + password
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func resetCacheForAccountChange(cacheData map[string]any, currentHash string) map[string]any {
	if h, _ := cacheData["accountHash"].(string); h != "" && h != currentHash {
		cacheData["accessToken"] = ""
		cacheData["tokenCreateTime"] = ""
		cacheData["lastDeleteTime"] = ""
	}
	cacheData["accountHash"] = currentHash
	return cacheData
}

// getFileURL: 对外入口，token 失效自动清除并重试一次
func (a *App) getFileURL(name, etag string, size int64, fastMode bool) string {
	url := a.getFileURLOnce(name, etag, size, fastMode)
	if url == "" || strings.Contains(url, "222.186.21.40:33333/NGGYU.mp4") {
		cacheData := ReadJSONFile(a.cfg.CachePath)
		if cacheData == nil {
			cacheData = map[string]any{}
		}
		if tok, _ := cacheData["accessToken"].(string); tok != "" {
			cacheData["accessToken"] = ""
			cacheData["tokenCreateTime"] = ""
			WriteJSONFile(a.cfg.CachePath, cacheData)
			url = a.getFileURLOnce(name, etag, size, fastMode)
		}
	}
	return url
}

func (a *App) getFileURLOnce(name, etag string, size int64, fastMode bool) string {
	settingsData := LoadYAMLMap(a.cfg.SettingsPath)
	if settingsData == nil {
		settingsData = map[string]any{}
	}
	username, _ := settingsData["123PAN_USERNAME"].(string)
	password, _ := settingsData["123PAN_PASSWORD"].(string)
	currentHash := accountHash(username, password)

	driver := NewPan123()

	cacheData := ReadJSONFile(a.cfg.CachePath)
	if cacheData == nil {
		cacheData = map[string]any{}
	}
	cacheData = resetCacheForAccountChange(cacheData, currentHash)

	if tok, _ := cacheData["accessToken"].(string); tok != "" {
		if ct, ok := cacheData["tokenCreateTime"]; ok {
			if tt, ok := ct.(float64); ok && time.Now().Unix()-int64(tt) < 25*24*60*60 {
				driver.setAccessToken(tok)
			}
		}
	}
	if driver.getAccessToken() == "" {
		if !driver.doLogin(username, password) {
			log.Printf("[播放] 登录失败，返回占位链接: %s", name)
			return "http://222.186.21.40:33333/NGGYU.mp4"
		}
		cacheData["accessToken"] = driver.getAccessToken()
		cacheData["tokenCreateTime"] = float64(time.Now().Unix())
		cacheData["accountHash"] = currentHash
		WriteJSONFile(a.cfg.CachePath, cacheData)
	}

	// 缓存目录：优先使用管理页面手动指定的文件夹（不再自动创建/清理）；
	// 未指定时保持自动创建 + 24h 自动清理
	cacheFolderId := int64(0)
	manualCacheFolder := false
	var actionResult map[string]any
	var cacheFolderInfo2 map[string]any
	if v, ok := a.cfg.Config()["cache_folder_id"].(float64); ok && int64(v) > 0 {
		cacheFolderId = int64(v)
		manualCacheFolder = true
	} else {
		actionResult = driver.createFolder(0, "__缓存目录_无视即可_24h自动清理__123Pan-Unlimited-WebDAV", true)
		if isFinish, _ := actionResult["isFinish"].(bool); !isFinish {
			log.Printf("[播放] 创建缓存目录失败: %v", actionResult["message"])
			return "http://222.186.21.40:33333/NGGYU.mp4"
		}
		cacheFolderInfo, _ := actionResult["message"].(map[string]any)
		cacheFolderInfo2, _ = cacheFolderInfo["Info"].(map[string]any)
		if fid, ok := cacheFolderInfo2["FileId"].(float64); ok {
			cacheFolderId = int64(fid)
		}
	}

	actionResult = driver.uploadFile(etag, name, cacheFolderId, size, true)
	if isFinish, _ := actionResult["isFinish"].(bool); !isFinish {
		if manualCacheFolder {
			log.Printf("[播放] 秒传上传失败(缓存目录 fileId=%d 可能已失效，请到管理页面重新选择缓存目录): %s: %v", cacheFolderId, name, actionResult["message"])
		} else {
			log.Printf("[播放] 秒传上传失败: %s: %v", name, actionResult["message"])
		}
		return "http://222.186.21.40:33333/NGGYU.mp4"
	}
	fileData, _ := actionResult["message"].(map[string]any)
	fileInfo, _ := fileData["Info"].(map[string]any)

	actionResult = driver.downloadFile(
		asString(fileInfo["Etag"]),
		int64(asFloat(fileInfo["FileId"])),
		asString(fileInfo["S3KeyFlag"]),
		int64(asFloat(fileInfo["Type"])),
		asString(fileInfo["FileName"]),
		int64(asFloat(fileInfo["Size"])),
	)
	if isFinish, _ := actionResult["isFinish"].(bool); !isFinish {
		log.Printf("[播放] 获取下载链接失败: %s: %v", name, actionResult["message"])
		return "http://222.186.21.40:33333/NGGYU.mp4"
	}
	downloadLink, _ := actionResult["message"].(string)

	// 24h 清理缓存：自动创建模式删除整个缓存目录；手动指定模式只清空目录内文件(保留用户目录)
	if ld, ok := cacheData["lastDeleteTime"]; !ok || ld == "" {
		cacheData["lastDeleteTime"] = float64(time.Now().Unix())
		WriteJSONFile(a.cfg.CachePath, cacheData)
	}
	lastDel, _ := cacheData["lastDeleteTime"].(float64)
	if time.Now().Unix()-int64(lastDel) > 24*60*60 {
		if manualCacheFolder {
			// 手动指定目录：列出目录内文件并批量删除，保留目录本身
			res := driver.listFilesSingle(cacheFolderId)
			if e, ok := res["error"]; ok {
				log.Printf("[播放] 24h清理列出缓存目录失败: %v", e)
			} else {
				fileList := []map[string]any{}
				for _, it := range asAnySlice(res["items"]) {
					if im, ok := it.(map[string]any); ok {
						if fid, ok := im["FileId"].(float64); ok && int64(fid) > 0 {
							fileList = append(fileList, map[string]any{"FileId": fid})
						}
					}
				}
				if len(fileList) > 0 {
					actionResult = driver.deleteFile(fileList, true)
					if isFinish, _ := actionResult["isFinish"].(bool); !isFinish {
						log.Printf("[播放] 24h清理清空缓存目录失败: %v", actionResult["message"])
						return "http://222.186.21.40:33333/NGGYU.mp4"
					}
					log.Printf("[播放] 24h清理：已清空缓存目录 %d 个文件 (fileId=%d)", len(fileList), cacheFolderId)
				}
			}
		} else {
			actionResult = driver.deleteFile([]map[string]any{cacheFolderInfo2}, true)
			if isFinish, _ := actionResult["isFinish"].(bool); !isFinish {
				log.Printf("[播放] 24h清理删除缓存目录失败: %v", actionResult["message"])
				return "http://222.186.21.40:33333/NGGYU.mp4"
			}
			log.Printf("[播放] 24h清理：已彻底删除缓存目录 fileId=%d", cacheFolderId)
		}
		cacheData["lastDeleteTime"] = float64(time.Now().Unix())
		WriteJSONFile(a.cfg.CachePath, cacheData)
	}

	// 解析跳转链接
	realURL := ""
	parts := strings.Split(downloadLink, "params=")
	if len(parts) > 1 {
		realURL = strings.Split(parts[len(parts)-1], "&")[0]
	}
	decoded, err := base64.StdEncoding.DecodeString(realURL)
	if err != nil {
		return ""
	}
	realURL = string(decoded)

	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, _ := http.NewRequest("GET", realURL, nil)
	req.Header.Set("Referer", "https://yun.123pan.com/")
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	finalURL := ""
	if resp.StatusCode == 302 {
		finalURL = resp.Header.Get("Location")
	} else if resp.StatusCode < 300 {
		body, _ := io.ReadAll(resp.Body)
		var rd map[string]any
		if err := json.Unmarshal(body, &rd); err == nil {
			if dt, ok := rd["data"].(map[string]any); ok {
				finalURL, _ = dt["redirect_url"].(string)
			}
		}
	} else {
		return ""
	}

	// 播放过程中若自动重新登录刷新了 token，写回
	if tok := driver.getAccessToken(); tok != "" {
		if old, _ := cacheData["accessToken"].(string); tok != old {
			cacheData["accessToken"] = tok
			cacheData["tokenCreateTime"] = float64(time.Now().Unix())
			cacheData["accountHash"] = currentHash
			WriteJSONFile(a.cfg.CachePath, cacheData)
		}
	}

	// 入库模式：60 秒后异步删除临时文件
	if fastMode {
		go func(driver *Pan123, fileInfo map[string]any, name string) {
			time.Sleep(60 * time.Second)
			log.Printf("[播放] 入库模式：删除临时文件 %s", name)
			res := driver.deleteFile([]map[string]any{fileInfo}, true)
			if isFinish, _ := res["isFinish"].(bool); isFinish {
				log.Printf("[播放] 入库模式：临时文件 %s 已删除", name)
			} else {
				log.Printf("[播放] 入库模式：删除临时文件 %s 失败: %v", name, res["message"])
			}
		}(driver, fileInfo, name)
	}

	log.Printf("[播放] 获取到 %s 的真实 URL: %s", name, finalURL)
	return finalURL
}

func (a *App) getFileURLWithEtagCandidates(name, etag string, size int64, fastMode bool) string {
	candidates := base62ToHexCandidates(etag)
	lastURL := ""
	for _, e := range candidates {
		url := a.getFileURL(name, e, size, fastMode)
		lastURL = url
		if url != "" && !strings.Contains(url, "222.186.21.40:33333/NGGYU.mp4") {
			return url
		}
	}
	return lastURL
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	}
	return 0
}
