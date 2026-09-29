package middleware

import (
	"log"
	"net/http"
	"runtime/debug"
)

// Recovery bắt panic và trả về 500 thay vì crash server
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("[ENGINE] [PANIC] %v\n%s", err, debug.Stack())
				http.Error(w, `{"error":"Internal server error"}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
