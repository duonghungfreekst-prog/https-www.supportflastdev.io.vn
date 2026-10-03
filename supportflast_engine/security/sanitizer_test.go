package security

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 1. Kiểm tra Log Injection Guard (CRLF Attack) theo Rule 3.6
func TestSanitizeLogString(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "CRLF newline injection",
			input:    "user_input\r\n[ADMIN] Login bypass",
			expected: "user_input\\r\\n[ADMIN] Login bypass",
		},
		{
			name:     "Multiple carriage returns and line feeds",
			input:    "line1\rline2\nline3\r\nline4",
			expected: "line1\\rline2\\nline3\\r\\nline4",
		},
		{
			name:     "Null byte in log input",
			input:    "malicious\x00user\r\n[SYSTEM] Action",
			expected: "malicioususer\\r\\n[SYSTEM] Action",
		},
		{
			name:     "Clean input without CRLF",
			input:    "Standard log message for user 12345",
			expected: "Standard log message for user 12345",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeLogString(tc.input)
			if got != tc.expected {
				t.Errorf("SanitizeLogString(%q) = %q; want %q", tc.input, got, tc.expected)
			}
			// Kiểm tra hàm tương thích ngược SanitizeCRLF
			gotCRLF := SanitizeCRLF(tc.input)
			if gotCRLF != tc.expected {
				t.Errorf("SanitizeCRLF(%q) = %q; want %q", tc.input, gotCRLF, tc.expected)
			}
		})
	}
}

// 2. Kiểm tra phát hiện mã độc SQL Injection (SQLi)
func TestValidateInput_SQLInjection(t *testing.T) {
	sqliPayloads := []struct {
		name    string
		payload string
	}{
		{"Classic SELECT ... FROM", "SELECT * FROM users WHERE id = 1"},
		{"UNION SELECT", "UNION SELECT null, username, password FROM accounts"},
		{"UNION ALL SELECT", "1' UNION ALL SELECT 1, 2, 3, 4 --"},
		{"SQL comment bypass", "admin' --"},
		{"SQL block comment", "admin' /* internal comment */ AND '1'='1"},
		{"Semicolon comment", "test';--"},
		{"Boolean tautology ' OR '1'='1", "admin' OR '1'='1"},
		{"Boolean tautology ' OR 1=1", "' OR 1=1 --"},
		{"Double quote tautology \" OR \"\"=\"", "\" OR \"\"=\""},
		{"String tautology ' OR 'a'='a", "' OR 'a'='a"},
		{"Stacked query DROP TABLE", "1; DROP TABLE users;"},
		{"Stacked query DELETE FROM", "1; DELETE FROM accounts WHERE 1=1;"},
		{"Stacked query INSERT INTO", "1; INSERT INTO admins VALUES ('hacker');"},
		{"Stacked query UPDATE SET", "1; UPDATE users SET role='admin';"},
		{"DROP DATABASE command", "DROP DATABASE supportflast"},
		{"ALTER TABLE command", "ALTER TABLE users ADD COLUMN backdoor TEXT"},
		{"TRUNCATE TABLE command", "TRUNCATE TABLE logs"},
		{"Blind SQLi SLEEP", "1' AND (SELECT SLEEP(5)) --"},
		{"Blind SQLi BENCHMARK", "1' AND BENCHMARK(1000000, MD5(1)) --"},
		{"Blind SQLi WAITFOR DELAY", "'; WAITFOR DELAY '0:0:5' --"},
		{"URL Encoded SQLi", "%27%20OR%201%3D1--"},
	}

	for _, tc := range sqliPayloads {
		t.Run(tc.name, func(t *testing.T) {
			valid, reason := ValidateInput(tc.payload)
			if valid {
				t.Errorf("Security flaw: Failed to detect SQLi payload [%s]: %s", tc.name, tc.payload)
			}
			if reason != "SQL injection pattern detected" {
				t.Logf("Detected with reason: %s", reason)
			}
		})
	}
}

// 3. Kiểm tra phát hiện mã độc Cross-Site Scripting (XSS)
func TestValidateInput_XSS(t *testing.T) {
	xssPayloads := []struct {
		name    string
		payload string
	}{
		{"Standard script tag", "<script>alert('pwned')</script>"},
		{"Uppercase script tag with source", "<SCRIPT SRC=\"http://evil.com/xss.js\"></SCRIPT>"},
		{"Closing script tag", "</script><script>alert(1)</script>"},
		{"Javascript pseudo-protocol", "javascript:alert(document.cookie)"},
		{"Image onerror handler", "<img src=x onerror=alert(1)>"},
		{"SVG onload handler", "<svg onload=alert(1)>"},
		{"Body onload handler", "<body onload=\"alert('xss')\">"},
		{"Iframe tag", "<iframe src=\"https://malicious.com\"></iframe>"},
		{"Object tag", "<object data=\"data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==\"></object>"},
		{"Embed tag", "<embed src=\"http://evil.com/malware.swf\">"},
		{"DOM cookie access", "document.cookie"},
		{"DOM location redirection", "window.location = 'http://attacker.com'"},
		{"URL Encoded script tag", "%3Cscript%3Ealert(1)%3C/script%3E"},
	}

	for _, tc := range xssPayloads {
		t.Run(tc.name, func(t *testing.T) {
			valid, reason := ValidateInput(tc.payload)
			if valid {
				t.Errorf("Security flaw: Failed to detect XSS payload [%s]: %s", tc.name, tc.payload)
			}
			if reason != "XSS injection pattern detected" {
				t.Logf("Detected with reason: %s", reason)
			}
		})
	}
}

// 4. Kiểm tra phát hiện Path Traversal (chặn '../', '..\', '%2e%2e')
func TestValidateInput_PathTraversal(t *testing.T) {
	traversalPayloads := []struct {
		name    string
		payload string
	}{
		{"Unix path traversal", "../../etc/passwd"},
		{"Windows path traversal", "..\\..\\windows\\system32\\cmd.exe"},
		{"Percent encoded dot-dot-slash (%2e%2e/)", "%2e%2e/secret/keys.json"},
		{"Uppercase encoded dot-dot (%2E%2E)", "%2E%2E/config/settings.env"},
		{"Encoded slash (..%2f)", "..%2f..%2fapp.db"},
		{"Encoded backslash (..%5c)", "..%5c..%5cboot.ini"},
		{"Full percent encoded (%2e%2e%2f)", "%2e%2e%2f%2e%2e%2fetc%2fshadow"},
		{"Double encoded traversal (%252e%252e%252f)", "%252e%252e%252fetc%252fpasswd"},
	}

	for _, tc := range traversalPayloads {
		t.Run(tc.name, func(t *testing.T) {
			valid, reason := ValidateInput(tc.payload)
			if valid {
				t.Errorf("Security flaw: Failed to detect Path Traversal [%s]: %s", tc.name, tc.payload)
			}
			if reason != "Path traversal pattern detected" {
				t.Logf("Detected with reason: %s", reason)
			}
		})
	}
}

// 5. Kiểm tra phát hiện Null Byte (0x00 & %00)
func TestValidateInput_NullBytes(t *testing.T) {
	nullBytePayloads := []struct {
		name    string
		payload string
	}{
		{"Raw null byte injection", "shell.php\x00.jpg"},
		{"Encoded null byte injection", "invoice.pdf%00.exe"},
	}

	for _, tc := range nullBytePayloads {
		t.Run(tc.name, func(t *testing.T) {
			valid, reason := ValidateInput(tc.payload)
			if valid {
				t.Errorf("Security flaw: Failed to detect Null Byte [%s]: %s", tc.name, tc.payload)
			}
			if reason != "Null byte detected" {
				t.Logf("Detected with reason: %s", reason)
			}
		})
	}
}

// 6. Kiểm tra phát hiện Command / Shell Injection
func TestValidateInput_CommandInjection(t *testing.T) {
	cmdPayloads := []struct {
		name    string
		payload string
	}{
		{"Semicolon command chaining", "input; cat /etc/passwd"},
		{"Pipe command chaining", "test | whoami"},
		{"Logical AND chaining", "app && rm -rf /var/log"},
		{"Logical OR chaining", "file || dir"},
		{"Subshell dollar syntax", "$(whoami)"},
		{"Subshell backtick syntax", "`id`"},
		{"PowerShell encoded command", "powershell.exe -enc AAAA"},
		{"Cmd.exe execution", "cmd.exe /c dir"},
		{"Direct bash execution", "/bin/bash -c 'id'"},
		{"Eval execution", "eval('process.exit()')"},
		{"System call execution", "system('whoami')"},
	}

	for _, tc := range cmdPayloads {
		t.Run(tc.name, func(t *testing.T) {
			valid, reason := ValidateInput(tc.payload)
			if valid {
				t.Errorf("Security flaw: Failed to detect Command Injection [%s]: %s", tc.name, tc.payload)
			}
			if reason != "Command injection pattern detected" {
				t.Logf("Detected with reason: %s", reason)
			}
		})
	}
}

// 7. Kiểm tra dữ liệu hợp lệ không bị chặn sai (False Positives Prevention)
func TestValidateInput_LegitimateData(t *testing.T) {
	cleanInputs := []struct {
		name  string
		input string
	}{
		{"Vietnamese query", "Hệ thống supportflast.dev.io.vn hỗ trợ 5 subagents"},
		{"Email address", "developer.support@supportflastdev.io.vn"},
		{"Vietnamese description", "Ứng dụng tiện ích đóng gói MSIX và tối ưu pipeline"},
		{"URL with HTTPS", "https://supportflastdev.io.vn/api/apps/details"},
		{"Standard search text", "Hướng dẫn tích hợp Cloudflare WAF và Zero-Trust"},
		{"Normal numbers and math", "Version 2.0.1 build 1042 released today"},
	}

	for _, tc := range cleanInputs {
		t.Run(tc.name, func(t *testing.T) {
			valid, reason := ValidateInput(tc.input)
			if !valid {
				t.Errorf("False positive: Valid input was incorrectly blocked [%s]: %s (Reason: %s)", tc.name, tc.input, reason)
			}
		})
	}
}

// 8. Kiểm tra Information Disclosure Guard theo Rule 3.5 (FormatSafeError)
func TestFormatSafeError(t *testing.T) {
	// Giả lập lỗi cơ sở dữ liệu và đường dẫn hệ thống nhạy cảm
	sensitiveErr := errors.New("pq: relation 'users_credentials' does not exist at /var/www/internal/db.go:42 (DB: postgres_prod, table: secret_users)")

	// Case 1: Lỗi 500 mặc định khi không truyền trace code
	status500, resp500 := FormatSafeError(sensitiveErr, "")
	if status500 != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d", status500)
	}
	if resp500["code"] != "ERR_INTERNAL_500" {
		t.Errorf("Expected code ERR_INTERNAL_500, got %v", resp500["code"])
	}
	if resp500["error"] != "Internal server error" {
		t.Errorf("Expected error 'Internal server error', got %v", resp500["error"])
	}

	// Xác nhận TUYỆT ĐỐI KHÔNG làm lộ chi tiết bảng, file, đường dẫn, hay stack trace ra ngoài client (Rule 3.5)
	respBytes, _ := json.Marshal(resp500)
	respStr := string(respBytes)
	forbiddenLeaks := []string{
		"users_credentials",
		"secret_users",
		"postgres_prod",
		"internal",
		"db.go",
		"/var/www",
		"pq:",
	}
	for _, leak := range forbiddenLeaks {
		if strings.Contains(respStr, leak) {
			t.Fatalf("CRITICAL SECURITY LEAK: Client response contains internal detail '%s': %s", leak, respStr)
		}
	}

	// Case 2: Kiểm tra ánh xạ mã lỗi 400
	status400, resp400 := FormatSafeError(sensitiveErr, "ERR_BAD_REQUEST_400")
	if status400 != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", status400)
	}
	if resp400["code"] != "ERR_BAD_REQUEST_400" || resp400["error"] != "Bad request" {
		t.Errorf("Incorrect mapping for 400: %v", resp400)
	}

	// Case 3: Kiểm tra ánh xạ mã lỗi 401
	status401, resp401 := FormatSafeError(sensitiveErr, "ERR_UNAUTHORIZED_401")
	if status401 != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", status401)
	}
	if resp401["error"] != "Unauthorized" {
		t.Errorf("Incorrect mapping for 401: %v", resp401)
	}

	// Case 4: Kiểm tra ánh xạ mã lỗi 403
	status403, resp403 := FormatSafeError(sensitiveErr, "ERR_FORBIDDEN_403")
	if status403 != http.StatusForbidden {
		t.Errorf("Expected status 403, got %d", status403)
	}
	if resp403["error"] != "Forbidden" {
		t.Errorf("Incorrect mapping for 403: %v", resp403)
	}

	// Case 5: Kiểm tra ánh xạ mã lỗi 404
	status404, resp404 := FormatSafeError(sensitiveErr, "ERR_NOT_FOUND_404")
	if status404 != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", status404)
	}
	if resp404["error"] != "Resource not found" {
		t.Errorf("Incorrect mapping for 404: %v", resp404)
	}

	// Case 6: Kiểm tra ánh xạ mã lỗi 429
	status429, resp429 := FormatSafeError(sensitiveErr, "ERR_RATE_LIMIT_429")
	if status429 != http.StatusTooManyRequests {
		t.Errorf("Expected status 429, got %d", status429)
	}
	if resp429["error"] != "Too many requests" {
		t.Errorf("Incorrect mapping for 429: %v", resp429)
	}

	// Case 7: Xử lý an toàn khi err == nil
	statusNil, respNil := FormatSafeError(nil, "ERR_INTERNAL_500")
	if statusNil != http.StatusInternalServerError || respNil["code"] != "ERR_INTERNAL_500" {
		t.Errorf("Handling nil error failed: %v", respNil)
	}
}

// 9. Kiểm tra WriteSafeError ghi response JSON chuẩn
func TestWriteSafeError(t *testing.T) {
	w := httptest.NewRecorder()
	err := errors.New("database connection refused at 10.0.0.5:5432")

	WriteSafeError(w, err, "ERR_INTERNAL_500")

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Expected Content-Type application/json, got %s", ct)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("Failed to parse JSON response: %v", err)
	}

	if body["code"] != "ERR_INTERNAL_500" {
		t.Errorf("Expected code ERR_INTERNAL_500, got %v", body["code"])
	}
	if strings.Contains(w.Body.String(), "10.0.0.5") {
		t.Fatalf("Internal IP address leaked in response body!")
	}
}

// 10. Kiểm tra InputValidationMiddleware
func TestInputValidationMiddleware(t *testing.T) {
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("PASSED"))
	})

	middleware := InputValidationMiddleware(nextHandler)

	// 10.1: Chặn Path Traversal trong URL Path
	reqBadPath := httptest.NewRequest("GET", "/api/files/../../etc/passwd", nil)
	wBadPath := httptest.NewRecorder()
	middleware.ServeHTTP(wBadPath, reqBadPath)
	if wBadPath.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for path traversal, got %d", wBadPath.Code)
	}

	// 10.2: Chặn SQL Injection trong Query Parameters
	reqBadQuery := httptest.NewRequest("GET", "/api/apps?q=SELECT+*+FROM+users", nil)
	wBadQuery := httptest.NewRecorder()
	middleware.ServeHTTP(wBadQuery, reqBadQuery)
	if wBadQuery.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for SQL injection query, got %d", wBadQuery.Code)
	}

	// 10.3: Cho phép request an toàn
	reqClean := httptest.NewRequest("GET", "/api/apps?category=utilities", nil)
	wClean := httptest.NewRecorder()
	middleware.ServeHTTP(wClean, reqClean)
	if wClean.Code != http.StatusOK {
		t.Errorf("Expected status 200 for clean request, got %d", wClean.Code)
	}
}

// 11. Các bài kiểm tra gốc: ConstantTimeVerify, ConstantTimeCompareBytes, HMAC, SecurityHeaders, CORS
func TestConstantTimeVerify(t *testing.T) {
	tokenA := "secure-support-token-2026"
	tokenB := "secure-support-token-2026"
	tokenC := "wrong-support-token-XXXX"
	tokenD := "short"

	if !ConstantTimeVerify(tokenA, tokenB) {
		t.Fatalf("Expected tokens to match")
	}
	if ConstantTimeVerify(tokenA, tokenC) {
		t.Fatalf("Expected tokens to not match")
	}
	if ConstantTimeVerify(tokenA, tokenD) {
		t.Fatalf("Expected different length tokens to not match")
	}
}

func TestConstantTimeCompareBytes(t *testing.T) {
	b1 := []byte("secret_payload_data_123")
	b2 := []byte("secret_payload_data_123")
	b3 := []byte("secret_payload_data_456")

	if !ConstantTimeCompareBytes(b1, b2) {
		t.Fatalf("Expected byte slices to match")
	}
	if ConstantTimeCompareBytes(b1, b3) {
		t.Fatalf("Expected different byte slices to not match")
	}
}

func TestHMACVerification(t *testing.T) {
	secret := []byte("super-secret-key-32-bytes-length!")
	message := []byte("supportflastdev.io.vn|user-123|session")

	mac := ComputeHMAC(message, secret)

	// Kiểm tra xác thực HMAC đúng
	if !VerifyHMAC(message, mac, secret) {
		t.Fatalf("HMAC verification failed for valid MAC")
	}

	// Kiểm tra HMAC sai khi tin nhắn bị sửa đổi
	tamperedMsg := []byte("supportflastdev.io.vn|user-999|session")
	if VerifyHMAC(tamperedMsg, mac, secret) {
		t.Fatalf("HMAC verification should fail for tampered message")
	}

	// Kiểm tra HMAC sai khi secret khác
	wrongSecret := []byte("different-secret-key-32-bytes!!")
	if VerifyHMAC(message, mac, wrongSecret) {
		t.Fatalf("HMAC verification should fail for wrong secret")
	}
}

func TestSecurityHeadersMiddleware(t *testing.T) {
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Thử gán header rò rỉ thông tin máy chủ xem middleware có loại bỏ không
		w.Header().Set("Server", "Apache/2.4.41 (Ubuntu)")
		w.Header().Set("X-Powered-By", "PHP/7.4.3")
		w.Header().Set("X-AspNet-Version", "4.0.30319")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	wrapped := SecurityHeadersMiddleware(dummyHandler)

	req := httptest.NewRequest("GET", "https://supportflastdev.io.vn/api/test", nil)
	req.Header.Set("Origin", "https://supportflastdev.io.vn")
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	resp := w.Result()

	// 1. Kiểm tra các header bảo mật bắt buộc theo Rule 3.4
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("Expected X-Content-Type-Options: nosniff, got: %s", got)
	}
	if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("Expected X-Frame-Options: DENY, got: %s", got)
	}
	if got := resp.Header.Get("Strict-Transport-Security"); got != "max-age=31536000; includeSubDomains; preload" {
		t.Errorf("Expected Strict-Transport-Security: max-age=31536000; includeSubDomains; preload, got: %s", got)
	}
	expectedCSP := "default-src 'self'; script-src 'self' 'unsafe-inline' https://challenges.cloudflare.com; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; font-src 'self' data:; connect-src 'self' ws: wss: https://supportflastdev.io.vn https://www.supportflastdev.io.vn https://supportflastdev-io-vn.onrender.com https://challenges.cloudflare.com https://*.cloudflare.com; frame-src 'self' https://challenges.cloudflare.com https://www.youtube.com https://*.youtube.com https://*.googlevideo.com; media-src 'self' data: blob: https:;"
	if got := resp.Header.Get("Content-Security-Policy"); got != expectedCSP {
		t.Errorf("Expected Content-Security-Policy: %s, got: %s", expectedCSP, got)
	}
	if got := resp.Header.Get("X-XSS-Protection"); got != "1; mode=block" {
		t.Errorf("Expected X-XSS-Protection: 1; mode=block, got: %s", got)
	}
	if got := resp.Header.Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
		t.Errorf("Expected Referrer-Policy: strict-origin-when-cross-origin, got: %s", got)
	}
	expectedPermissions := "geolocation=(), microphone=(), camera=()"
	if got := resp.Header.Get("Permissions-Policy"); got != expectedPermissions {
		t.Errorf("Expected Permissions-Policy: %s, got: %s", expectedPermissions, got)
	}

	// 2. Kiểm tra KHÔNG để lộ Server, X-Powered-By, X-AspNet-Version theo Rule 3.4
	if s := resp.Header.Get("Server"); s != "" {
		t.Errorf("Server header MUST be stripped, but got: %s", s)
	}
	if p := resp.Header.Get("X-Powered-By"); p != "" {
		t.Errorf("X-Powered-By header MUST be stripped, but got: %s", p)
	}
	if a := resp.Header.Get("X-AspNet-Version"); a != "" {
		t.Errorf("X-AspNet-Version header MUST be stripped, but got: %s", a)
	}

	// 3. Kiểm tra các header chống cache nghiêm ngặt
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache, no-store, must-revalidate, max-age=0" {
		t.Errorf("Expected Cache-Control: no-cache, no-store, must-revalidate, max-age=0, got: %s", cc)
	}
	if pragma := resp.Header.Get("Pragma"); pragma != "no-cache" {
		t.Errorf("Expected Pragma: no-cache, got: %s", pragma)
	}
	if exp := resp.Header.Get("Expires"); exp != "0" {
		t.Errorf("Expected Expires: 0, got: %s", exp)
	}

	// 4. Kiểm tra CORS Exact match (Rule 3.4)
	if cors := resp.Header.Get("Access-Control-Allow-Origin"); cors != "https://supportflastdev.io.vn" {
		t.Errorf("Expected Access-Control-Allow-Origin: https://supportflastdev.io.vn, got: %s", cors)
	}
}

func TestCORS_AllowedOriginsAndPreflight(t *testing.T) {
	// 0. Kiểm tra danh sách 3 exact origins mặc định theo Rule 3.4
	if !IsOriginAllowed("http://localhost:8080") {
		t.Errorf("Expected http://localhost:8080 to be allowed by default")
	}
	if !IsOriginAllowed("http://127.0.0.1:8080") {
		t.Errorf("Expected http://127.0.0.1:8080 to be allowed by default")
	}
	if !IsOriginAllowed("https://supportflastdev.io.vn") {
		t.Errorf("Expected https://supportflastdev.io.vn to be allowed by default")
	}
	if !IsOriginAllowed("https://www.supportflastdev.io.vn") {
		t.Errorf("Expected https://www.supportflastdev.io.vn to be allowed by default")
	}
	if !IsOriginAllowed("https://supportflastdev-io-vn.onrender.com") {
		t.Errorf("Expected https://supportflastdev-io-vn.onrender.com to be allowed by default")
	}

	// Kiểm tra chống bypass tên miền (Regex/Substring bypass attempt)
	if IsOriginAllowed("https://supportflastdev.io.vn.evil.com") {
		t.Errorf("Security flaw: Subdomain spoofing origin must NOT be allowed")
	}
	if IsOriginAllowed("http://localhost:8080.attacker.com") {
		t.Errorf("Security flaw: Suffix spoofing origin must NOT be allowed")
	}

	// 1. Thử nghiệm nạp origin từ biến môi trường
	t.Setenv("ALLOWED_ORIGINS", "https://supportflast-preview.vercel.app, https://custom.supportflast.io, https://cloudflare-page.pages.dev/")
	LoadAllowedOriginsFromEnv()

	if !IsOriginAllowed("https://supportflast-preview.vercel.app") {
		t.Errorf("Expected https://supportflast-preview.vercel.app to be allowed")
	}
	if !IsOriginAllowed("https://custom.supportflast.io") {
		t.Errorf("Expected https://custom.supportflast.io to be allowed")
	}
	if !IsOriginAllowed("https://cloudflare-page.pages.dev") {
		t.Errorf("Expected trailing slash to be normalized and allowed")
	}

	// 2. Kiểm tra không cho phép wildcard theo Rule 3.4
	RegisterAllowedOrigin("*")
	if IsOriginAllowed("*") {
		t.Errorf("Rule 3.4 violation: Wildcard origin MUST NOT be allowed")
	}
	RegisterAllowedOrigin("*.supportflastdev.io.vn")
	if IsOriginAllowed("*.supportflastdev.io.vn") {
		t.Errorf("Rule 3.4 violation: Wildcard subdomain origin MUST NOT be allowed")
	}

	// 3. Kiểm tra Disallowed Origin không được cấp header
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	middleware := SecurityHeadersMiddleware(dummyHandler)

	unauthorizedReq := httptest.NewRequest("GET", "/api/test", nil)
	unauthorizedReq.Header.Set("Origin", "https://malicious-attacker.com")
	wUnauthorized := httptest.NewRecorder()
	middleware.ServeHTTP(wUnauthorized, unauthorizedReq)

	if badCors := wUnauthorized.Header().Get("Access-Control-Allow-Origin"); badCors != "" {
		t.Errorf("Security violation: Unauthorized origin got CORS header: %s", badCors)
	}

	// 4. Kiểm tra Preflight OPTIONS request cho Hosting hợp lệ
	optionsReq := httptest.NewRequest("OPTIONS", "/api/test", nil)
	optionsReq.Header.Set("Origin", "https://supportflast-preview.vercel.app")
	optionsReq.Header.Set("Access-Control-Request-Method", "POST")
	wOptions := httptest.NewRecorder()
	middleware.ServeHTTP(wOptions, optionsReq)

	if wOptions.Code != http.StatusOK {
		t.Errorf("Expected OPTIONS preflight to return 200, got %d", wOptions.Code)
	}
	if originHeader := wOptions.Header().Get("Access-Control-Allow-Origin"); originHeader != "https://supportflast-preview.vercel.app" {
		t.Errorf("Expected allowed origin in preflight, got %s", originHeader)
	}
	if methods := wOptions.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(methods, "POST") {
		t.Errorf("Expected Allow-Methods to contain POST, got %s", methods)
	}
	if creds := wOptions.Header().Get("Access-Control-Allow-Credentials"); creds != "true" {
		t.Errorf("Expected Allow-Credentials: true, got %s", creds)
	}
}
