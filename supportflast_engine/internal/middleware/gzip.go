package middleware

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

// GzipMinLength: Ngưỡng kích thước tối thiểu để áp dụng nén gzip (1KB = 1024 bytes).
// Response <= 1024 bytes không nén để tránh chi phí overhead của header/trailer gzip.
const GzipMinLength = 1024

var gzipWriterPool = sync.Pool{
	New: func() interface{} {
		w, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
		return w
	},
}

// gzipResponseWriter quản lý buffer và nén động cho HTTP response
type gzipResponseWriter struct {
	http.ResponseWriter
	req           *http.Request
	buf           *bytes.Buffer
	statusCode    int
	headerWritten bool
	writingGzip   bool
	passthrough   bool
	gz            *gzip.Writer
}

func newGzipResponseWriter(w http.ResponseWriter, r *http.Request) *gzipResponseWriter {
	return &gzipResponseWriter{
		ResponseWriter: w,
		req:            r,
		buf:            &bytes.Buffer{},
		statusCode:     http.StatusOK,
	}
}

// WriteHeader ghi nhận status code và áp dụng passthrough cho các mã đặc thù
func (g *gzipResponseWriter) WriteHeader(code int) {
	if g.headerWritten {
		return
	}
	g.statusCode = code

	// 204 No Content, 304 Not Modified, 206 Partial Content (Range) tuyệt đối không nén gzip
	if code == http.StatusNoContent || code == http.StatusNotModified || code == http.StatusPartialContent {
		g.passthrough = true
		g.headerWritten = true
		g.ResponseWriter.WriteHeader(code)
	}
}

// Write ghi dữ liệu vào buffer đệm hoặc gzip stream
func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if g.passthrough {
		if !g.headerWritten {
			g.headerWritten = true
			g.ResponseWriter.WriteHeader(g.statusCode)
		}
		return g.ResponseWriter.Write(b)
	}

	if g.writingGzip {
		return g.gz.Write(b)
	}

	// Đang ở giai đoạn đệm kiểm tra kích thước
	n, err := g.buf.Write(b)
	if err != nil {
		return n, err
	}

	// Nếu buffer đã vượt quá ngưỡng GzipMinLength (1KB), quyết định nén hay passthrough
	if g.buf.Len() > GzipMinLength {
		g.startResponse(false)
	}

	return n, nil
}

// startResponse quyết định kích hoạt gzip hoặc chuyển sang passthrough
func (g *gzipResponseWriter) startResponse(isClosing bool) {
	if g.headerWritten && !g.writingGzip && !g.passthrough {
		// Header đã gửi ở chế độ passthrough
		return
	}

	// Kiểm tra xem đã có Content-Encoding chưa (nếu đã nén rồi thì không nén lại)
	if g.Header().Get("Content-Encoding") != "" {
		g.passthrough = true
	}

	// Bỏ qua nén với HTTP Range requests
	if g.req.Header.Get("Range") != "" || g.statusCode == http.StatusPartialContent {
		g.passthrough = true
	}

	// Xác định Content-Type
	ct := g.Header().Get("Content-Type")
	if ct == "" && g.buf.Len() > 0 {
		ct = http.DetectContentType(g.buf.Bytes())
		g.Header().Set("Content-Type", ct)
	}

	// Kiểm tra loại nội dung có thể nén được không
	if !g.passthrough && shouldCompress(ct) && (g.buf.Len() > GzipMinLength || (!isClosing && g.buf.Len() > 0)) {
		// Đủ điều kiện nén GZIP
		g.writingGzip = true

		// Cập nhật headers
		g.Header().Set("Content-Encoding", "gzip")
		vary := g.Header().Get("Vary")
		if vary == "" {
			g.Header().Set("Vary", "Accept-Encoding")
		} else if !strings.Contains(strings.ToLower(vary), "accept-encoding") {
			g.Header().Set("Vary", vary+", Accept-Encoding")
		}

		// Xóa Content-Length vì kích thước nén sẽ khác kích thước gốc
		g.Header().Del("Content-Length")

		if !g.headerWritten {
			g.headerWritten = true
			g.ResponseWriter.WriteHeader(g.statusCode)
		}

		// Lấy gzip.Writer từ pool và ghi dữ liệu buffer ra
		gz := gzipWriterPool.Get().(*gzip.Writer)
		gz.Reset(g.ResponseWriter)
		g.gz = gz

		if g.buf.Len() > 0 {
			_, _ = g.gz.Write(g.buf.Bytes())
			g.buf.Reset()
		}
		return
	}

	// Không nén -> passthrough
	g.passthrough = true
	if !g.headerWritten {
		g.headerWritten = true
		g.ResponseWriter.WriteHeader(g.statusCode)
	}
	if g.buf.Len() > 0 {
		_, _ = g.ResponseWriter.Write(g.buf.Bytes())
		g.buf.Reset()
	}
}

// finish hoàn tất quá trình ghi response khi handler kết thúc
func (g *gzipResponseWriter) finish() {
	if g.writingGzip {
		if g.gz != nil {
			_ = g.gz.Close()
			gzipWriterPool.Put(g.gz)
			g.gz = nil
		}
		return
	}

	if g.passthrough {
		// Dữ liệu buffer nếu còn sót thì xả ra
		if g.buf.Len() > 0 {
			_, _ = g.ResponseWriter.Write(g.buf.Bytes())
			g.buf.Reset()
		}
		return
	}

	// Trường hợp tổng kích thước response <= GzipMinLength (1KB)
	if !g.headerWritten {
		g.headerWritten = true
		g.ResponseWriter.WriteHeader(g.statusCode)
	}
	if g.buf.Len() > 0 {
		_, _ = g.ResponseWriter.Write(g.buf.Bytes())
		g.buf.Reset()
	}
}

// Flush hỗ trợ giao diện http.Flusher
func (g *gzipResponseWriter) Flush() {
	if !g.writingGzip && !g.passthrough && g.buf.Len() > 0 {
		g.startResponse(false)
	}

	if g.writingGzip && g.gz != nil {
		_ = g.gz.Flush()
	}

	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack hỗ trợ giao diện http.Hijacker cho WebSocket/TCP
func (g *gzipResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := g.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("underlying ResponseWriter does not implement http.Hijacker")
}

// shouldCompress xác định loại Content-Type có được nén hay không
func shouldCompress(ct string) bool {
	if ct == "" {
		return false
	}
	ct = strings.ToLower(ct)
	if idx := strings.Index(ct, ";"); idx != -1 {
		ct = strings.TrimSpace(ct[:idx])
	}

	// 1. Danh sách định dạng ĐÃ NÉN hoặc KHÔNG ĐƯỢC NÉN (Bỏ qua tuyệt đối)
	if ct == "application/octet-stream" || // Chunk mã hóa binary, VFS raw chunks
		ct == "text/event-stream" || // Server-Sent Events (SSE)
		strings.HasPrefix(ct, "video/") || // Video (mp4, webm, mkv, ...)
		strings.HasPrefix(ct, "audio/") || // Audio (mp3, wav, ogg, ...)
		ct == "image/jpeg" || ct == "image/jpg" || // Ảnh nén JPEG
		ct == "image/png" || ct == "image/gif" || // Ảnh PNG, GIF
		ct == "image/webp" || ct == "image/avif" || // Ảnh WebP, AVIF
		ct == "application/zip" || ct == "application/gzip" || // Archives
		ct == "application/x-gzip" || ct == "application/x-rar-compressed" ||
		ct == "application/x-7z-compressed" || ct == "application/pdf" {
		return false
	}

	// 2. Danh sách định dạng văn bản & dữ liệu CẦN NÉN GZIP
	if strings.Contains(ct, "json") || // application/json, etc.
		strings.Contains(ct, "javascript") || // application/javascript, text/javascript
		strings.HasPrefix(ct, "text/") || // text/html, text/css, text/plain, text/csv...
		strings.Contains(ct, "xml") || // application/xml, text/xml
		ct == "image/svg+xml" { // SVG vector
		return true
	}

	return false
}

// GzipMiddleware tạo middleware nén gzip tự động cho response > 1KB
func GzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bỏ qua nếu client không hỗ trợ gzip
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") ||
			r.Method == http.MethodHead ||
			strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			next.ServeHTTP(w, r)
			return
		}

		gzw := newGzipResponseWriter(w, r)
		defer gzw.finish()

		next.ServeHTTP(gzw, r)
	})
}

// Gzip là bí danh tương thích cho GzipMiddleware
func Gzip(next http.Handler) http.Handler {
	return GzipMiddleware(next)
}
