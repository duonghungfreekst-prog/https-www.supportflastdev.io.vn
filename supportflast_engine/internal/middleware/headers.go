package middleware

import (
	"net/http"
	"supportflast_engine/security"
)

// SecurityHeaders là middleware chuẩn hóa cho kiến trúc engine, áp dụng bộ Security Headers
// bắt buộc theo Rule 3.4 mà không làm mất hoặc ghi đè các header tùy biến hợp lệ của downstream handler
func SecurityHeaders(next http.Handler) http.Handler {
	return security.SecurityHeadersMiddleware(next)
}
