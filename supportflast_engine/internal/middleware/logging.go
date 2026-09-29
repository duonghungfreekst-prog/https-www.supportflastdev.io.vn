package middleware

import (
	"log"
	"net/http"
	"time"
)

// statusWriter bọc ResponseWriter để capture status code
type statusWriter struct {
	http.ResponseWriter
	statusCode int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.statusCode = code
	sw.ResponseWriter.WriteHeader(code)
}

// AccessLog ghi log mỗi request với: method, path, status, duration, client IP
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, statusCode: 200}
		next.ServeHTTP(sw, r)
		duration := time.Since(start)
		log.Printf("[ENGINE] [ACCESS] %s %s %d %s %s",
			r.Method, r.URL.Path, sw.statusCode, duration, r.RemoteAddr)
	})
}
