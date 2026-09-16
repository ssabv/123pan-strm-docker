package app

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strings"
	"sync"
)

// 秒传库 JSON 动辄十几 MB，内网/公网传输前 gzip 一遍能省掉绝大部分流量。
// 这里做透明压缩：小于阈值(1KB)的响应保持明文，避免为了几十字节反而多出压缩头。
const gzipMinSize = 1024

var gzipPool = sync.Pool{
	New: func() any {
		// BestSpeed：大 JSON 上压缩比与默认级别接近，但快很多
		w, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed)
		return w
	},
}

type gzipResponseWriter struct {
	http.ResponseWriter
	buf       bytes.Buffer
	status    int
	headerSet bool
	started   bool
	gz        *gzip.Writer
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	if g.headerSet {
		return
	}
	g.headerSet = true
	g.status = code
}

func (g *gzipResponseWriter) Write(p []byte) (int, error) {
	if !g.headerSet {
		g.WriteHeader(http.StatusOK)
	}
	if g.started {
		if g.gz != nil {
			return g.gz.Write(p)
		}
		return g.ResponseWriter.Write(p)
	}
	g.buf.Write(p)
	if g.buf.Len() >= gzipMinSize {
		g.startGzip()
	}
	return len(p), nil
}

// 达到阈值后正式写出响应头并切换到压缩流
func (g *gzipResponseWriter) startGzip() {
	h := g.ResponseWriter.Header()
	if h.Get("Content-Type") == "" {
		h.Set("Content-Type", "application/octet-stream")
	}
	h.Set("Content-Encoding", "gzip")
	h.Del("Content-Length")
	g.ResponseWriter.WriteHeader(g.status)
	gz := gzipPool.Get().(*gzip.Writer)
	gz.Reset(g.ResponseWriter)
	g.gz = gz
	g.started = true
	if g.buf.Len() > 0 {
		gz.Write(g.buf.Bytes())
		g.buf.Reset()
	}
}

func (g *gzipResponseWriter) Close() {
	if !g.started {
		// 没到阈值：原样明文输出
		g.ResponseWriter.WriteHeader(g.status)
		if g.buf.Len() > 0 {
			g.ResponseWriter.Write(g.buf.Bytes())
		}
		return
	}
	g.gz.Close()
	gzipPool.Put(g.gz)
	g.gz = nil
}

func (g *gzipResponseWriter) Flush() {
	if !g.started {
		g.startGzip()
		return
	}
	if g.gz != nil {
		g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// 对支持 gzip 的客户端透明压缩响应体
func gzipHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Accept-Encoding")
		gw := &gzipResponseWriter{ResponseWriter: w, status: http.StatusOK}
		defer gw.Close()
		next.ServeHTTP(gw, r)
	})
}
