package middleware

import "net/http"

// Middleware là một function nhận handler và trả về handler mới
type Middleware func(http.Handler) http.Handler

// Chain kết hợp nhiều middleware theo thứ tự
// Middleware đầu tiên trong danh sách sẽ là lớp ngoài cùng (chạy trước)
func Chain(handler http.Handler, middlewares ...Middleware) http.Handler {
	// Áp dụng từ cuối lên đầu
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

// WrapFunc chuyển một func(http.Handler) http.Handler cũ thành Middleware type
func WrapFunc(fn func(http.Handler) http.Handler) Middleware {
	return Middleware(fn)
}
