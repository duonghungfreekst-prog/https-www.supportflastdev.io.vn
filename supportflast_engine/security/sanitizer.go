package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	// sqlRegex phát hiện toàn diện các mẫu SQL Injection (Rule 3.1):
	// - UNION / SELECT / INSERT / UPDATE / DELETE / DROP / ALTER / TRUNCATE
	// - Tautology: ' OR 1=1, ' OR '1'='1, " OR ""=", OR 1=1
	// - SQL Comments: --, /*, */, ;--
	// - Stacked queries: ; DROP, ; DELETE, ; INSERT, v.v.
	// - SQL Functions: SLEEP, BENCHMARK, WAITFOR DELAY, EXEC/EXECUTE
	sqlRegex = regexp.MustCompile(`(?i)(` +
		`\bunion\s+(all\s+)?select\b|` +
		`\bselect\s+.*\bfrom\b|` +
		`\binsert\s+into\b|` +
		`\bupdate\s+.*\bset\b|` +
		`\bdelete\s+from\b|` +
		`\b(drop|alter|truncate)\s+(table|database|schema)\b|` +
		`(--|\/\*|\*\/|;--)|` +
		`;\s*(drop|delete|insert|update|alter|truncate|select)\b|` +
		`['"]\s*(or|and)\s+(true|1\s*=\s*1|0\s*=\s*0)\b|` +
		`\b(or|and)\s+(1=1|0=0)\b|` +
		`\b(sleep\s*\([0-9]+|benchmark\s*\([0-9]+|waitfor\s+delay\s+|exec(ute)?\s*\()` +
		`)`)

	sqlStrTautologyRegex = regexp.MustCompile(`(?i)(?:'|")?\s*\b(?:or|and)\b\s+(['"])([^'"]*)['"]\s*=\s*['"]?([^'"]*)`)
	sqlNumTautologyRegex = regexp.MustCompile(`(?i)\b(or|and)\s+([0-9]+)\s*=\s*([0-9]+)`)

	// xssRegex phát hiện toàn diện các mẫu Cross-Site Scripting (XSS) (Rule 3.1):
	// - Thẻ <script>, javascript: pseudo-protocol, data:text/html
	// - Các event handler: onload, onerror, onclick, onmouseover, v.v.
	// - Các thẻ nguy hiểm: <iframe>, <img ... src>, <svg ... onload>, <object>, <embed>
	// - Truy cập DOM nhạy cảm: document.cookie, document.location, window.location
	xssRegex = regexp.MustCompile(`(?i)(` +
		`<\s*script.*?>|` +
		`<\s*/\s*script\s*>|` +
		`javascript\s*:|` +
		`data\s*:\s*text/html|` +
		`vbscript\s*:|` +
		`on[a-z]+\s*=|` +
		`<\s*iframe|` +
		`<\s*img.*?src|` +
		`<\s*svg.*?onload|` +
		`<\s*object|` +
		`<\s*embed|` +
		`document\.(cookie|location)|` +
		`window\.location` +
		`)`)

	// cmdRegex phát hiện Command / Shell Injection (Rule 3.1):
	// - Chaining commands: ; ls, | whoami, && rm, || dir
	// - Subshell execution: $(command), `command`
	// - Direct shell invocations: powershell, cmd.exe, /bin/sh, /bin/bash
	// - Code execution: eval(), exec(), system()
	cmdRegex = regexp.MustCompile(`(?i)(` +
		`;\s*(rm|cat|ls|dir|whoami|sh|bash|cmd|powershell|curl|wget|nc|netcat|chmod|chown|kill|pkill|echo|id)\b|` +
		`(\||\&{1,2})\s*(rm|cat|ls|dir|whoami|sh|bash|cmd|powershell|curl|wget|nc|netcat|id)\b|` +
		`\$\([^)]+\)|` +
		`\x60[^\x60]+\x60|` +
		`\b(powershell(\.exe)?|cmd(\.exe)?)\s+[/-](c|k|e|enc|command|exec)\b|` +
		`\b(exec|eval|system|passthru|shell_exec)\s*\(|` +
		`/bin/(bash|sh|zsh|dash)` +
		`)`)
)

var (
	allowedOriginsMutex sync.RWMutex
	// Danh sách origin được phép cấu hình Exact String Match theo Rule 3.4
	// TUYỆT ĐỐI KHÔNG dùng wildcard '*' hoặc regex pattern matching
	allowedOriginsMap = map[string]bool{
		"http://localhost":                true,
		"http://127.0.0.1":                true,
		"http://localhost:8080":           true,
		"http://127.0.0.1:8080":           true,
		"https://localhost:8443":          true,
		"https://127.0.0.1:8443":          true,
		"http://supportflast.local":       true,
		"https://supportflast.local:8443": true,
		"https://supportflastdev.io.vn":   true,
	}
)

func init() {
	RegisterLanOrigins()
	LoadAllowedOriginsFromEnv()
}

// RegisterLanOrigins tự động đăng ký các địa chỉ IP nội bộ của máy chủ vào CORS Whitelist
func RegisterLanOrigins() {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ip4 := ipnet.IP.To4(); ip4 != nil {
				ipStr := ip4.String()
				RegisterAllowedOrigin("http://" + ipStr)
				RegisterAllowedOrigin("http://" + ipStr + ":80")
				RegisterAllowedOrigin("http://" + ipStr + ":8080")
				RegisterAllowedOrigin("https://" + ipStr + ":8443")
			}
		}
	}
}

// RegisterAllowedOrigin thêm một origin vào danh sách cho phép (Exact String Match theo Rule 3.4)
// TUYỆT ĐỐI KHÔNG dùng wildcard '*' hoặc regex pattern matching dễ bị bypass.
func RegisterAllowedOrigin(origin string) {
	clean := strings.TrimSpace(origin)
	clean = strings.TrimRight(clean, "/")
	// Cấm tuyệt đối wildcard '*' hoặc pattern matching
	if clean == "" || clean == "*" || strings.Contains(clean, "*") {
		return
	}
	allowedOriginsMutex.Lock()
	defer allowedOriginsMutex.Unlock()
	allowedOriginsMap[clean] = true
}

// IsOriginAllowed kiểm tra tính hợp lệ của Origin (Exact String Match theo Rule 3.4)
// TUYỆT ĐỐI KHÔNG dùng wildcard '*' hoặc regex pattern matching dễ bị bypass.
func IsOriginAllowed(origin string) bool {
	clean := strings.TrimSpace(origin)
	clean = strings.TrimRight(clean, "/")
	if clean == "" || clean == "*" || strings.Contains(clean, "*") {
		return false
	}
	allowedOriginsMutex.RLock()
	defer allowedOriginsMutex.RUnlock()
	return allowedOriginsMap[clean]
}

// GetAllowedOrigins trả về danh sách các origin được cho phép
func GetAllowedOrigins() []string {
	allowedOriginsMutex.RLock()
	defer allowedOriginsMutex.RUnlock()
	res := make([]string, 0, len(allowedOriginsMap))
	for k := range allowedOriginsMap {
		res = append(res, k)
	}
	return res
}

// LoadAllowedOriginsFromEnv đọc và phân tích biến môi trường ALLOWED_ORIGINS
// Hỗ trợ danh sách phân tách bởi dấu phẩy, an toàn và tuân thủ Rule 3.4
func LoadAllowedOriginsFromEnv() {
	envVal := os.Getenv("ALLOWED_ORIGINS")
	if strings.TrimSpace(envVal) == "" {
		return
	}
	parts := strings.Split(envVal, ",")
	for _, p := range parts {
		clean := strings.TrimSpace(p)
		clean = strings.TrimRight(clean, "/")
		if clean != "" && !strings.Contains(clean, "*") && (strings.HasPrefix(clean, "http://") || strings.HasPrefix(clean, "https://")) {
			RegisterAllowedOrigin(clean)
		}
	}
}

// SanitizeLogString loại bỏ hoặc escape hoàn toàn '\n' và '\r' trước khi ghi bất kỳ log nào chứa dữ liệu người dùng (Rule 3.6).
// Hàm này loại bỏ hoặc escape các ký tự điều khiển phân dòng (\r, \n, \x00) để chống tấn công CRLF Log Injection.
func SanitizeLogString(input string) string {
	clean := strings.ReplaceAll(input, "\r", "\\r")
	clean = strings.ReplaceAll(clean, "\n", "\\n")
	clean = strings.ReplaceAll(clean, "\x00", "")
	return clean
}

// SanitizeCRLF loại bỏ hoặc escape \r và \n để chống CRLF Log Injection (Rule 3.6).
// Giữ nguyên tính tương thích ngược cho các module hiện hữu trong hệ thống.
func SanitizeCRLF(input string) string {
	return SanitizeLogString(input)
}

// isPathTraversal kiểm tra mẫu tấn công duyệt thư mục (Path Traversal) (Rule 3.1):
// Chặn triệt để '../', '..\', '%2e%2e' (và các biến thể mã hóa URL)
func isPathTraversal(input string) bool {
	lower := strings.ToLower(input)
	if strings.Contains(lower, "../") ||
		strings.Contains(lower, "..\\") ||
		strings.Contains(lower, "%2e%2e") ||
		strings.Contains(lower, "..%2f") ||
		strings.Contains(lower, "..%5c") {
		return true
	}
	// Kiểm tra URL-decoded để chống double-encoding hoặc obfuscation
	if unescaped, err := url.QueryUnescape(input); err == nil && unescaped != input {
		lowerUnesc := strings.ToLower(unescaped)
		if strings.Contains(lowerUnesc, "../") ||
			strings.Contains(lowerUnesc, "..\\") ||
			strings.Contains(lowerUnesc, "%2e%2e") ||
			strings.Contains(lowerUnesc, "..%2f") ||
			strings.Contains(lowerUnesc, "..%5c") {
			return true
		}
	}
	return false
}

// hasNullByte kiểm tra sự xuất hiện của null bytes (0x00) và biến thể %00
func hasNullByte(input string) bool {
	if strings.Contains(input, "\x00") {
		return true
	}
	lower := strings.ToLower(input)
	if strings.Contains(lower, "%00") {
		return true
	}
	return false
}

// isSQLInjection kiểm tra toàn diện các mẫu SQL Injection (Rule 3.1)
func isSQLInjection(input string) bool {
	if sqlRegex.MatchString(input) {
		return true
	}
	// Kiểm tra tautology dạng chuỗi: ' OR '1'='1, " OR ""=", ' OR 'a'='a
	for _, m := range sqlStrTautologyRegex.FindAllStringSubmatch(input, -1) {
		if len(m) >= 4 {
			left := m[2]
			right := strings.TrimSpace(m[3])
			if left == right || (left != "" && strings.HasPrefix(right, left)) {
				return true
			}
		}
	}
	// Kiểm tra tautology dạng số: OR 1=1, AND 2=2
	for _, m := range sqlNumTautologyRegex.FindAllStringSubmatch(input, -1) {
		if len(m) >= 4 {
			if m[2] == m[3] {
				return true
			}
		}
	}
	return false
}

// ValidateInput kiểm tra toàn diện null bytes, path traversal, SQLi, XSS và Command/Shell Injection (Rule 3.1)
func ValidateInput(input string) (bool, string) {
	// 1. Kiểm tra Null Bytes (0x00 và biến thể mã hóa %00)
	if hasNullByte(input) {
		return false, "Null byte detected"
	}
	// 2. Kiểm tra Path Traversal (chặn '../', '..\', '%2e%2e' và các biến thể)
	if isPathTraversal(input) {
		return false, "Path traversal pattern detected"
	}
	// 3. Kiểm tra SQL Injection (SQLi)
	if isSQLInjection(input) {
		return false, "SQL injection pattern detected"
	}
	// 4. Kiểm tra Cross-Site Scripting (XSS)
	if xssRegex.MatchString(input) {
		return false, "XSS injection pattern detected"
	}
	// 5. Kiểm tra Command / Shell Injection
	if cmdRegex.MatchString(input) {
		return false, "Command injection pattern detected"
	}

	// 6. Kiểm tra lại chuỗi sau khi URL-decode nếu có mã hóa (chống obfuscation)
	if unescaped, err := url.QueryUnescape(input); err == nil && unescaped != input {
		if hasNullByte(unescaped) {
			return false, "Null byte detected"
		}
		if isPathTraversal(unescaped) {
			return false, "Path traversal pattern detected"
		}
		if isSQLInjection(unescaped) {
			return false, "SQL injection pattern detected"
		}
		if xssRegex.MatchString(unescaped) {
			return false, "XSS injection pattern detected"
		}
		if cmdRegex.MatchString(unescaped) {
			return false, "Command injection pattern detected"
		}
	}

	return true, ""
}

// FormatSafeError chuẩn hóa phản hồi lỗi theo Rule 3.5 (Information Disclosure Guard).
// Đảm bảo client chỉ nhận được thông báo lỗi chung và mã lỗi generic (ví dụ: 'ERR_INTERNAL_500'),
// tuyệt đối không bao giờ làm lộ stack trace, tên bảng DB, tên file hay đường dẫn hệ thống ra bên ngoài.
// Toàn bộ chi tiết lỗi nội bộ được ghi log an toàn ở phía Server với SanitizeLogString chống CRLF.
func FormatSafeError(err error, traceCode string) (int, map[string]interface{}) {
	safeCode := strings.TrimSpace(traceCode)
	if safeCode == "" {
		safeCode = "ERR_INTERNAL_500"
	}

	// Ghi log chi tiết lỗi ở Server Side (Rule 3.5 & Rule 3.6)
	if err != nil {
		log.Printf("[ENGINE] [SECURITY] [SAFE_ERROR] TraceCode: %s | Internal Detail: %s",
			SanitizeLogString(safeCode),
			SanitizeLogString(err.Error()))
	}

	// Ánh xạ mã lỗi ra HTTP Status Code chuẩn và thông báo generic an toàn
	statusCode := http.StatusInternalServerError
	safeMessage := "Internal server error"

	codeUpper := strings.ToUpper(safeCode)
	switch {
	case strings.Contains(codeUpper, "400") || strings.Contains(codeUpper, "BAD_REQUEST") || strings.Contains(codeUpper, "INVALID"):
		statusCode = http.StatusBadRequest
		safeMessage = "Bad request"
	case strings.Contains(codeUpper, "401") || strings.Contains(codeUpper, "UNAUTHORIZED") || strings.Contains(codeUpper, "AUTH_"):
		statusCode = http.StatusUnauthorized
		safeMessage = "Unauthorized"
	case strings.Contains(codeUpper, "403") || strings.Contains(codeUpper, "FORBIDDEN") || strings.Contains(codeUpper, "ACCESS_DENIED"):
		statusCode = http.StatusForbidden
		safeMessage = "Forbidden"
	case strings.Contains(codeUpper, "404") || strings.Contains(codeUpper, "NOT_FOUND"):
		statusCode = http.StatusNotFound
		safeMessage = "Resource not found"
	case strings.Contains(codeUpper, "409") || strings.Contains(codeUpper, "CONFLICT"):
		statusCode = http.StatusConflict
		safeMessage = "Conflict"
	case strings.Contains(codeUpper, "429") || strings.Contains(codeUpper, "RATE_LIMIT") || strings.Contains(codeUpper, "TOO_MANY"):
		statusCode = http.StatusTooManyRequests
		safeMessage = "Too many requests"
	default:
		statusCode = http.StatusInternalServerError
		safeMessage = "Internal server error"
	}

	res := map[string]interface{}{
		"error": safeMessage,
		"code":  safeCode,
	}

	return statusCode, res
}

// WriteSafeError ghi phản hồi lỗi an toàn định dạng JSON ra http.ResponseWriter theo Rule 3.5
func WriteSafeError(w http.ResponseWriter, err error, traceCode string) {
	statusCode, body := FormatSafeError(err, traceCode)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(body)
}

// ConstantTimeVerify so sánh an toàn hai chuỗi secret (token, key) chống Timing Attack (Rule 3.6)
// Sử dụng hàm băm SHA-256 trước khi so sánh subtle.ConstantTimeCompare để đảm bảo:
// 1. Độ dài đầu vào luôn cố định 32 bytes, không làm rò rỉ độ dài token qua chênh lệch thời gian (Length Timing Leak).
// 2. So sánh toàn bộ 32 bytes trong thời gian không đổi (Constant-Time).
func ConstantTimeVerify(a, b string) bool {
	hA := sha256.Sum256([]byte(a))
	hB := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(hA[:], hB[:]) == 1
}

// ConstantTimeCompareBytes so sánh an toàn hai mảng byte secret chống Timing Side-Channel (Rule 3.6)
func ConstantTimeCompareBytes(a, b []byte) bool {
	hA := sha256.Sum256(a)
	hB := sha256.Sum256(b)
	return subtle.ConstantTimeCompare(hA[:], hB[:]) == 1
}

// ComputeHMAC tính toán mã xác thực thông điệp HMAC-SHA256
func ComputeHMAC(message, secret []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(message)
	return mac.Sum(nil)
}

// VerifyHMAC kiểm tra tính hợp lệ của chữ ký HMAC sử dụng so sánh thời gian không đổi chống Timing Attack (Rule 3.6)
func VerifyHMAC(message, expectedMAC, secret []byte) bool {
	mac := hmac.New(sha256.New, secret)
	mac.Write(message)
	actualMAC := mac.Sum(nil)
	return hmac.Equal(actualMAC, expectedMAC)
}

// SecurityResponseWriter bọc http.ResponseWriter để áp dụng các Security Headers bắt buộc
// và triệt tiêu các header rò rỉ thông tin máy chủ (Server, X-Powered-By, X-AspNet-Version) theo Rule 3.4 & 3.5.
type SecurityResponseWriter struct {
	http.ResponseWriter
	headerWritten bool
}

// ApplyHeaders áp dụng toàn bộ header bảo mật bắt buộc chuẩn quân sự cho MỌI HTTP response
func (w *SecurityResponseWriter) ApplyHeaders() {
	h := w.ResponseWriter.Header()

	// 1. Cấu hình đầy đủ bộ Security Headers bắt buộc chuẩn quân sự theo Rule 3.4
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
	h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' https://challenges.cloudflare.com; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; font-src 'self' data:; connect-src 'self' ws: wss: https://challenges.cloudflare.com; frame-src 'self' https://challenges.cloudflare.com https://www.youtube.com https://*.youtube.com https://*.googlevideo.com; media-src 'self' data: blob: https:;")
	h.Set("X-XSS-Protection", "1; mode=block")
	h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")

	// 2. Chống cache trình duyệt nghiêm ngặt đối với mọi HTTP response & UI
	h.Set("Cache-Control", "no-cache, no-store, must-revalidate, max-age=0")
	h.Set("Pragma", "no-cache")
	h.Set("Expires", "0")

	// 3. Triệt tiêu hoàn toàn các header lộ thông tin công nghệ theo Rule 3.4 & 3.5
	h.Del("Server")
	h.Del("X-Powered-By")
	h.Del("X-AspNet-Version")
	h.Del("server")
	h.Del("x-powered-by")
	h.Del("x-aspnet-version")
}

func (w *SecurityResponseWriter) WriteHeader(statusCode int) {
	if !w.headerWritten {
		w.ApplyHeaders()
		w.headerWritten = true
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *SecurityResponseWriter) Write(b []byte) (int, error) {
	if !w.headerWritten {
		w.ApplyHeaders()
		w.headerWritten = true
	}
	return w.ResponseWriter.Write(b)
}

// SecurityHeadersMiddleware gắn toàn bộ security headers bắt buộc theo Rule 3.4 vào tất cả response
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bọc response writer để đảm bảo mọi phản hồi (kể cả lỗi) đều có đủ Security Headers
		secWriter := &SecurityResponseWriter{ResponseWriter: w}
		secWriter.ApplyHeaders()

		// Kiểm tra CORS Exact match (Rule 3.4: không dùng wildcard hoặc regex)
		origin := r.Header.Get("Origin")
		if origin != "" && IsOriginAllowed(origin) {
			cleanOrigin := strings.TrimRight(strings.TrimSpace(origin), "/")
			secWriter.Header().Set("Access-Control-Allow-Origin", cleanOrigin)
			secWriter.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, HEAD")
			secWriter.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With, X-API-Key, Accept, Origin")
			secWriter.Header().Set("Access-Control-Allow-Credentials", "true")
			secWriter.Header().Set("Access-Control-Max-Age", "86400")
			secWriter.Header().Set("Vary", "Origin, Access-Control-Request-Method, Access-Control-Request-Headers")
		}

		if r.Method == http.MethodOptions {
			secWriter.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(secWriter, r)
	})
}

// InputValidationMiddleware kiểm tra các query parameters và URL path chống mã độc injection
func InputValidationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Kiểm tra URL Path
		if valid, reason := ValidateInput(r.URL.Path); !valid {
			log.Printf("[ENGINE] [SECURITY] Blocked malicious URL Path: %s (Reason: %s)",
				SanitizeLogString(r.URL.Path), SanitizeLogString(reason))
			WriteSafeError(w, nil, "ERR_BAD_REQUEST_400")
			return
		}
		// 2. Kiểm tra RawQuery
		if r.URL.RawQuery != "" {
			if valid, reason := ValidateInput(r.URL.RawQuery); !valid {
				log.Printf("[ENGINE] [SECURITY] Blocked malicious Query: %s (Reason: %s)",
					SanitizeLogString(r.URL.RawQuery), SanitizeLogString(reason))
				WriteSafeError(w, nil, "ERR_BAD_REQUEST_400")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ──────────────────────────────────────────────────────────────────────────────
// CSRF Protection: Token Generation, Validation & Middleware (Rule 3.4)
// Sử dụng crypto/rand + HMAC-SHA256 để chống Cross-Site Request Forgery
// ──────────────────────────────────────────────────────────────────────────────

var (
	csrfSecretOnce sync.Once
	csrfSecret     []byte // 32 bytes random secret, khởi tạo 1 lần duy nhất
)

// getCSRFSecret trả về CSRF HMAC secret key (lazy init, thread-safe).
// Secret được sinh bằng crypto/rand, tồn tại suốt vòng đời process.
func getCSRFSecret() []byte {
	csrfSecretOnce.Do(func() {
		// Ưu tiên đọc từ biến môi trường CSRF_SECRET nếu được cấu hình
		if envSecret := strings.TrimSpace(os.Getenv("CSRF_SECRET")); envSecret != "" {
			csrfSecret = []byte(envSecret)
		} else {
			csrfSecret = make([]byte, 32)
			if _, err := rand.Read(csrfSecret); err != nil {
				log.Fatalf("[ENGINE] [SECURITY] [FATAL] Không thể sinh CSRF secret: %v", err)
			}
		}
	})
	return csrfSecret
}

// GenerateCSRFToken sinh CSRF token gồm: random_nonce + "." + HMAC-SHA256(nonce|timestamp).
// Token có thời hạn mặc định được kiểm tra khi validate.
func GenerateCSRFToken() string {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		log.Printf("[ENGINE] [SECURITY] [WARN] CSRF nonce generation error: %v", err)
		return ""
	}
	nonceHex := hex.EncodeToString(nonce)
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	payload := nonceHex + "|" + timestamp

	mac := hmac.New(sha256.New, getCSRFSecret())
	mac.Write([]byte(payload))
	signature := hex.EncodeToString(mac.Sum(nil))

	return payload + "." + signature
}

// ValidateCSRFToken kiểm tra tính hợp lệ và thời hạn (1 giờ) của CSRF token.
// Token format: nonce|timestamp.signature
func ValidateCSRFToken(token string) bool {
	if token == "" {
		return false
	}
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return false
	}
	payload := parts[0]
	signatureHex := parts[1]

	// Verify HMAC signature
	mac := hmac.New(sha256.New, getCSRFSecret())
	mac.Write([]byte(payload))
	expectedSig := mac.Sum(nil)
	actualSig, err := hex.DecodeString(signatureHex)
	if err != nil {
		return false
	}
	if !hmac.Equal(expectedSig, actualSig) {
		return false
	}

	// Verify timestamp (token hết hạn sau 1 giờ)
	payloadParts := strings.SplitN(payload, "|", 2)
	if len(payloadParts) != 2 {
		return false
	}
	var ts int64
	if _, err := fmt.Sscanf(payloadParts[1], "%d", &ts); err != nil {
		return false
	}
	if time.Now().Unix()-ts > 3600 {
		return false // Token expired (> 1 hour)
	}

	return true
}

// CSRFMiddleware kiểm tra header X-CSRF-Token cho các request POST/PUT/DELETE.
// Bỏ qua cho /api/ endpoints vì đã được bảo vệ bởi JWT authentication.
func CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bỏ qua các phương thức an toàn (GET, HEAD, OPTIONS)
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		// Bỏ qua cho /api/ endpoints — đã có JWT authentication bảo vệ
		if strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		// Kiểm tra CSRF token trong header X-CSRF-Token
		csrfToken := r.Header.Get("X-CSRF-Token")
		if !ValidateCSRFToken(csrfToken) {
			clientIP := GetRealClientIP(r)
			log.Printf("[ENGINE] [SECURITY] [CSRF] Blocked request from %s to %s %s — Invalid or missing CSRF token",
				clientIP, r.Method, SanitizeLogString(r.URL.Path))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "CSRF token validation failed",
				"code":  "ERR_CSRF_403",
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}

// ──────────────────────────────────────────────────────────────────────────────
// Request ID Tracking Middleware
// Sinh UUID v4 cho mỗi request, set vào header X-Request-ID response
// ──────────────────────────────────────────────────────────────────────────────

// generateUUIDv4 sinh UUID version 4 (random) theo RFC 4122 sử dụng crypto/rand.
func generateUUIDv4() string {
	uuid := make([]byte, 16)
	if _, err := rand.Read(uuid); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	// Set version 4 (bits 12-15 of time_hi_and_version)
	uuid[6] = (uuid[6] & 0x0f) | 0x40
	// Set variant (bits 6-7 of clock_seq_hi_and_reserved)
	uuid[8] = (uuid[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
}

// RequestIDMiddleware sinh UUID v4 cho mỗi request và set vào header X-Request-ID.
// Nếu client gửi kèm X-Request-ID, sẽ sử dụng giá trị đó (sau khi sanitize).
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if requestID == "" || len(requestID) > 128 {
			requestID = generateUUIDv4()
		}
		// Sanitize: chỉ cho phép alphanumeric, dấu gạch ngang và gạch dưới
		sanitized := strings.Map(func(c rune) rune {
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
				return c
			}
			return -1
		}, requestID)
		if sanitized == "" {
			sanitized = generateUUIDv4()
		}

		w.Header().Set("X-Request-ID", sanitized)
		next.ServeHTTP(w, r)
	})
}
