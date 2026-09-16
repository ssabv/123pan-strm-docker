package app

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 大响应应被 gzip 压缩，小响应保持明文
func TestGzipHandler(t *testing.T) {
	big := strings.Repeat("秒传库数据", 500) // > 1KB
	small := "ok"

	h := gzipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/big" {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Write([]byte(big))
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write([]byte(small))
	}))

	// 1) 大响应 -> gzip
	req := httptest.NewRequest("GET", "/big", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if enc := rec.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("大响应应 gzip, 实际 Content-Encoding=%q", enc)
	}
	if rec.Header().Get("Vary") == "" {
		t.Fatalf("应设置 Vary: Accept-Encoding")
	}
	gr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("解压失败: %v", err)
	}
	got, _ := io.ReadAll(gr)
	if string(got) != big {
		t.Fatalf("解压内容不一致: 期望 %d 字节, 实际 %d 字节", len(big), len(got))
	}

	// 2) 小响应 -> 不压缩
	req2 := httptest.NewRequest("GET", "/small", nil)
	req2.Header.Set("Accept-Encoding", "gzip")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if enc := rec2.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("小响应不应压缩, 实际 Content-Encoding=%q", enc)
	}
	if rec2.Body.String() != small {
		t.Fatalf("小响应内容被改动: %q", rec2.Body.String())
	}

	// 3) 客户端不支持 gzip -> 不压缩
	req3 := httptest.NewRequest("GET", "/big", nil)
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req3)
	if enc := rec3.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("未声明 gzip 的客户端不应收到压缩内容, 实际 %q", enc)
	}
	if rec3.Body.String() != big {
		t.Fatalf("不压缩时内容应原样返回")
	}

	// 4) 大响应跨多次 Write 也应压缩完整
	h2 := gzipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 10; i++ {
			w.Write([]byte(strings.Repeat("x", 300)))
		}
	}))
	req4 := httptest.NewRequest("GET", "/chunked", nil)
	req4.Header.Set("Accept-Encoding", "gzip")
	rec4 := httptest.NewRecorder()
	h2.ServeHTTP(rec4, req4)
	gr2, err := gzip.NewReader(rec4.Body)
	if err != nil {
		t.Fatalf("分块写入解压失败: %v", err)
	}
	got2, _ := io.ReadAll(gr2)
	if len(got2) != 3000 || bytes.Count(got2, []byte("x")) != 3000 {
		t.Fatalf("分块写入内容不一致: %d 字节", len(got2))
	}
}

// 压缩后仍应是合法 JSON（前端依赖）
func TestGzipHandlerKeepsJSONValid(t *testing.T) {
	payload := map[string]any{"items": []any{strings.Repeat("a", 2000)}}
	h := gzipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, payload)
	}))
	req := httptest.NewRequest("GET", "/api/libraries", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("大 JSON 应被压缩")
	}
	gr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("解压失败: %v", err)
	}
	b, _ := io.ReadAll(gr)
	if !bytes.Contains(b, []byte(`"items"`)) {
		t.Fatalf("解压后不是预期 JSON: %s", string(b)[:min(120, len(b))])
	}
}
