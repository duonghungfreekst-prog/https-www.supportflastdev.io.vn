package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supportflast_engine/database"
	"supportflast_engine/security"

	"golang.org/x/crypto/bcrypt"
)

func TestHashPassword_CostAndSecurity(t *testing.T) {
	password := "SupportFlast@2026#SecurePassword"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	// 1. Kiểm tra tiền tố BCrypt ($2a$, $2b$, hoặc $2y$)
	if !strings.HasPrefix(hash, "$2a$") && !strings.HasPrefix(hash, "$2b$") {
		t.Fatalf("Expected valid BCrypt prefix, got: %s", hash)
	}

	// 2. Kiểm tra chi phí băm (cost) >= 12 theo Rule 8.2 & 3.2
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		t.Fatalf("bcrypt.Cost failed: %v", err)
	}
	if cost < 12 {
		t.Fatalf("BCrypt cost MUST be >= 12 according to Rule 8.2, got: %d", cost)
	}

	// 3. Kiểm tra xác thực mật khẩu đúng
	if !CheckPassword(hash, password) {
		t.Fatalf("CheckPassword failed with correct password")
	}

	// 4. Kiểm tra từ chối mật khẩu sai
	if CheckPassword(hash, "WrongPassword@123") {
		t.Fatalf("CheckPassword should have rejected wrong password")
	}

	// 5. Kiểm tra mật khẩu rỗng
	if _, err := HashPassword(""); err == nil {
		t.Fatalf("Expected error when hashing empty password")
	}

	// 6. Kiểm tra giới hạn 72 ký tự của BCrypt
	longPassword := strings.Repeat("A", 73)
	if _, err := HashPassword(longPassword); err == nil {
		t.Fatalf("Expected error for password longer than 72 bytes")
	}
}

func TestNeedsRehash(t *testing.T) {
	// Băm với cost thấp (ví dụ cost 10)
	lowCostHash, err := bcrypt.GenerateFromPassword([]byte("test_pass"), 10)
	if err != nil {
		t.Fatalf("Failed to generate low cost hash: %v", err)
	}

	// Phải báo cần rehash vì target là 12
	if !NeedsRehash(string(lowCostHash), 12) {
		t.Fatalf("NeedsRehash should return true for cost 10 when target is 12")
	}

	// Băm với cost 12
	properHash, err := bcrypt.GenerateFromPassword([]byte("test_pass"), 12)
	if err != nil {
		t.Fatalf("Failed to generate cost 12 hash: %v", err)
	}

	if NeedsRehash(string(properHash), 12) {
		t.Fatalf("NeedsRehash should return false for cost 12 when target is 12")
	}
}

func TestLoginHandler_RateLimitingIntegration(t *testing.T) {
	// Reset rate limits trước khi test
	security.ResetAllRateLimits()
	defer security.ResetAllRateLimits()

	InitAuth()

	testIP := "192.0.2.100"

	// Thử đăng nhập sai 5 lần liên tiếp
	for i := 1; i <= 5; i++ {
		body := `{"username_or_email":"admin","password":"IncorrectPassword!"}`
		req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = testIP + ":54321"
		w := httptest.NewRecorder()

		LoginHandler(w, req)

		resp := w.Result()
		if i < 5 {
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("Attempt %d: expected 401 Unauthorized, got: %d", i, resp.StatusCode)
			}
		} else {
			// Lần thứ 5 phải khóa tài khoản/IP (HTTP 429)
			if resp.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("5th attempt: expected 429 Too Many Requests, got: %d", resp.StatusCode)
			}

			retryAfter := resp.Header.Get("Retry-After")
			if retryAfter == "" || retryAfter == "0" {
				t.Fatalf("Expected valid Retry-After header on 5th failure, got: %s", retryAfter)
			}

			var jsonResp map[string]interface{}
			json.NewDecoder(resp.Body).Decode(&jsonResp)
			if jsonResp["status"] != "locked" {
				t.Errorf("Expected status='locked', got: %v", jsonResp["status"])
			}
		}
	}

	// Lần thứ 6: Dù gửi mật khẩu ĐÚNG vẫn phải bị chặn bởi Rate Limiter vì đang bị khóa 15 phút!
	correctBody := `{"username_or_email":"admin","password":"Admin@2026!SupportFlast"}`
	req6 := httptest.NewRequest("POST", "/api/auth/login", bytes.NewBufferString(correctBody))
	req6.Header.Set("Content-Type", "application/json")
	req6.RemoteAddr = testIP + ":54321"
	w6 := httptest.NewRecorder()

	LoginHandler(w6, req6)

	resp6 := w6.Result()
	if resp6.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("Attempt 6 with correct password MUST be rejected with 429 while locked, got: %d", resp6.StatusCode)
	}
}

func TestLoginHandler_And_GetUserFromToken_RS256(t *testing.T) {
	security.ResetAllRateLimits()
	defer security.ResetAllRateLimits()

	InitAuth()

	// 1. Thực hiện đăng nhập để nhận JWT RS256
	body := `{"username_or_email":"admin","password":"Admin@2026!SupportFlast"}`
	req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.111:54321"
	w := httptest.NewRecorder()

	LoginHandler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("LoginHandler failed: status code %d", resp.StatusCode)
	}

	var loginData struct {
		Status string   `json:"status"`
		Token  string   `json:"token"`
		User   SafeUser `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&loginData); err != nil {
		t.Fatalf("Cannot decode login response: %v", err)
	}

	if loginData.Status != "success" || loginData.Token == "" {
		t.Fatalf("Expected login success with token, got: %+v", loginData)
	}

	// 2. Kiểm tra token có đúng cấu trúc JWT 3 phần RS256
	parts := strings.Split(loginData.Token, ".")
	if len(parts) != 3 {
		t.Fatalf("Token trả về không phải chuẩn JWT RS256 (phải có 3 phần), got: %d parts", len(parts))
	}

	// 3. Thẩm định token bằng security.ValidateRS256Token
	claims, err := security.ValidateRS256Token(loginData.Token)
	if err != nil {
		t.Fatalf("ValidateRS256Token thất bại với token từ Login: %v", err)
	}
	if claims.Role != "admin" {
		t.Errorf("Expected role 'admin', got: %s", claims.Role)
	}
	if claims.Username != "admin" {
		t.Errorf("Expected username 'admin', got: %s", claims.Username)
	}

	// 4. Kiểm tra GetUserFromToken nhận diện chính xác người dùng từ token RS256
	u, ok := GetUserFromToken(loginData.Token)
	if !ok || u == nil {
		t.Fatalf("GetUserFromToken không nhận diện được token RS256")
	}
	if u.Username != "admin" || u.Role != "admin" {
		t.Errorf("User trả về từ GetUserFromToken không chính xác: %+v", u)
	}

	// 5. Thử nghiệm Đăng xuất (Logout) -> Phải thu hồi token (Token Revocation)
	logoutReq := httptest.NewRequest("POST", "/api/auth/logout", nil)
	logoutReq.Header.Set("Authorization", "Bearer "+loginData.Token)
	wLogout := httptest.NewRecorder()

	LogoutHandler(wLogout, logoutReq)

	if wLogout.Result().StatusCode != http.StatusOK {
		t.Fatalf("LogoutHandler thất bại: %d", wLogout.Result().StatusCode)
	}

	// Sau khi logout, token phải bị thu hồi và không thể dùng lại
	_, okAfterLogout := GetUserFromToken(loginData.Token)
	if okAfterLogout {
		t.Fatal("Lỗ hổng: Token vẫn hợp lệ sau khi người dùng đã Logout!")
	}
}

func TestPublicKeyHandler_Endpoint(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/auth/public-key", nil)
	w := httptest.NewRecorder()

	PublicKeyHandler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PublicKeyHandler status code mong đợi 200, nhận: %d", resp.StatusCode)
	}

	var resData map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&resData); err != nil {
		t.Fatalf("Cannot decode public key response: %v", err)
	}

	if resData["status"] != "success" {
		t.Errorf("Expected status='success', got: %v", resData["status"])
	}
	if resData["algorithm"] != "RS256" {
		t.Errorf("Expected algorithm='RS256', got: %v", resData["algorithm"])
	}

	pubKeyStr, ok := resData["public_key"].(string)
	if !ok || !strings.Contains(pubKeyStr, "-----BEGIN PUBLIC KEY-----") {
		t.Fatalf("Public Key không chứa PEM header hợp lệ: %s", pubKeyStr)
	}
}

func TestChangePasswordHandler_And_SecurityPIN(t *testing.T) {
	// Khởi tạo tài khoản test thông thường (developer / member)
	nano := time.Now().UnixNano()
	uid := fmt.Sprintf("usr-cp-%d", nano)
	uname := fmt.Sprintf("cp_user_%d", nano%1000000)
	uemail := fmt.Sprintf("cp_%d@supportflastdev.io.vn", nano%1000000)

	hash, _ := HashPassword("OldPassword@123")
	testUser := User{
		ID:           uid,
		Username:     uname,
		Email:        uemail,
		DisplayName:  "Test Change Pass",
		PasswordHash: hash,
		Role:         "developer",
		IsActive:     true,
	}
	if err := InsertUser(testUser); err != nil {
		t.Fatalf("Không thể khởi tạo test user: %v", err)
	}
	defer func() {
		if db := database.GetDB(); db != nil {
			db.Exec("DELETE FROM users WHERE id = ?", uid)
		}
	}()
	userToken := CreateSession(testUser)

	// 1. Thử đổi mật khẩu với mật khẩu cũ SAI -> 400 Bad Request
	badReqBody := `{"old_password":"WrongOldPassword","new_password":"NewSecretPassword@456"}`
	reqBad := httptest.NewRequest("POST", "/api/auth/change-password", bytes.NewBufferString(badReqBody))
	reqBad.Header.Set("Authorization", "Bearer "+userToken)
	wBad := httptest.NewRecorder()
	ChangePasswordHandler(wBad, reqBad)
	if wBad.Code != http.StatusBadRequest {
		t.Fatalf("Kỳ vọng 400 khi sai mật khẩu cũ, nhận: %d", wBad.Code)
	}

	// 2. Thử đổi mật khẩu với mật khẩu cũ ĐÚNG -> 200 OK
	goodReqBody := `{"old_password":"OldPassword@123","new_password":"NewSecretPassword@456"}`
	reqGood := httptest.NewRequest("POST", "/api/auth/change-password", bytes.NewBufferString(goodReqBody))
	reqGood.Header.Set("Authorization", "Bearer "+userToken)
	wGood := httptest.NewRecorder()
	ChangePasswordHandler(wGood, reqGood)
	if wGood.Code != http.StatusOK {
		t.Fatalf("Kỳ vọng 200 khi đổi mật khẩu đúng, nhận: %d, body: %s", wGood.Code, wGood.Body.String())
	}

	// 3. Thử cài đặt mã PIN bảo mật Cấp 2 với định dạng sai (không đủ 6 số) -> 400 Bad Request
	badPinBody := `{"old_pin":"","new_pin":"1234","security_tier":2}`
	reqBadPin := httptest.NewRequest("POST", "/api/auth/security-pin", bytes.NewBufferString(badPinBody))
	reqBadPin.Header.Set("Authorization", "Bearer "+userToken)
	wBadPin := httptest.NewRecorder()
	SecurityPINHandler(wBadPin, reqBadPin)
	if wBadPin.Code != http.StatusBadRequest {
		t.Fatalf("Kỳ vọng 400 khi mã PIN không đủ 6 số, nhận: %d", wBadPin.Code)
	}

	// 4. Thử cài đặt mã PIN chuẩn 6 số -> 200 OK
	goodPinBody := `{"old_pin":"","new_pin":"123456","security_tier":2}`
	reqGoodPin := httptest.NewRequest("POST", "/api/auth/security-pin", bytes.NewBufferString(goodPinBody))
	reqGoodPin.Header.Set("Authorization", "Bearer "+userToken)
	wGoodPin := httptest.NewRecorder()
	SecurityPINHandler(wGoodPin, reqGoodPin)
	if wGoodPin.Code != http.StatusOK {
		t.Fatalf("Kỳ vọng 200 khi lưu mã PIN 6 số, nhận: %d", wGoodPin.Code)
	}

	// 5. Xác thực mã PIN hợp lệ qua /api/auth/verify-pin -> 200 OK
	verifyReq := httptest.NewRequest("POST", "/api/auth/verify-pin", bytes.NewBufferString(`{"pin":"123456"}`))
	wVerify := httptest.NewRecorder()
	VerifyPINHandler(wVerify, verifyReq)
	if wVerify.Code != http.StatusOK {
		t.Fatalf("Kỳ vọng 200 khi xác thực PIN đúng, nhận: %d", wVerify.Code)
	}
}

func TestRBAC_AdminIsolation_Strict(t *testing.T) {
	nano := time.Now().UnixNano()

	// 1. Tạo tài khoản thông thường (developer / member)
	memberUID := fmt.Sprintf("usr-mem-%d", nano)
	memberUname := fmt.Sprintf("mem_%d", nano%1000000)
	memberEmail := fmt.Sprintf("mem_%d@supportflastdev.io.vn", nano%1000000)

	memberHash, _ := HashPassword("MemberPass@123")
	memberUser := User{
		ID:           memberUID,
		Username:     memberUname,
		Email:        memberEmail,
		DisplayName:  "Người dùng thông thường",
		PasswordHash: memberHash,
		Role:         "developer",
		IsActive:     true,
	}
	if err := InsertUser(memberUser); err != nil {
		t.Fatalf("Không thể tạo member test: %v", err)
	}
	defer func() {
		if db := database.GetDB(); db != nil {
			db.Exec("DELETE FROM users WHERE id = ?", memberUID)
		}
	}()
	memberToken := CreateSession(memberUser)

	// 2. Tạo tài khoản Quản Trị Viên (admin)
	adminUID := fmt.Sprintf("usr-adm-%d", nano)
	adminUname := fmt.Sprintf("adm_%d", nano%1000000)
	adminEmail := fmt.Sprintf("adm_%d@supportflastdev.io.vn", nano%1000000)

	adminHash, _ := HashPassword("AdminMasterPass@2026")
	adminUser := User{
		ID:           adminUID,
		Username:     adminUname,
		Email:        adminEmail,
		DisplayName:  "Quản Trị Viên RBAC",
		PasswordHash: adminHash,
		Role:         "admin",
		IsActive:     true,
	}
	if err := InsertUser(adminUser); err != nil {
		t.Fatalf("Không thể tạo admin test: %v", err)
	}
	defer func() {
		if db := database.GetDB(); db != nil {
			db.Exec("DELETE FROM users WHERE id = ?", adminUID)
		}
	}()
	adminToken := CreateSession(adminUser)

	// A. KIỂM TRA TRUY CẬP /api/admin/users
	// Member -> 403 Forbidden
	reqMemUsers := httptest.NewRequest("GET", "/api/admin/users", nil)
	reqMemUsers.Header.Set("Authorization", "Bearer "+memberToken)
	wMemUsers := httptest.NewRecorder()
	AdminUsersListHandler(wMemUsers, reqMemUsers)
	if wMemUsers.Code != http.StatusForbidden {
		t.Fatalf("LỖ HỔNG RBAC: Người dùng thường truy cập được /api/admin/users (status: %d)", wMemUsers.Code)
	}

	// Admin -> 200 OK
	reqAdmUsers := httptest.NewRequest("GET", "/api/admin/users", nil)
	reqAdmUsers.Header.Set("Authorization", "Bearer "+adminToken)
	wAdmUsers := httptest.NewRecorder()
	AdminUsersListHandler(wAdmUsers, reqAdmUsers)
	if wAdmUsers.Code != http.StatusOK {
		t.Fatalf("Admin bị từ chối truy cập /api/admin/users: %d", wAdmUsers.Code)
	}

	// B. KIỂM TRA TRUY CẬP /api/admin/logs (Audit Logs & Security Events)
	// Member -> 403 Forbidden
	reqMemLogs := httptest.NewRequest("GET", "/api/admin/logs", nil)
	reqMemLogs.Header.Set("Authorization", "Bearer "+memberToken)
	wMemLogs := httptest.NewRecorder()
	AdminLogsHandler(wMemLogs, reqMemLogs)
	if wMemLogs.Code != http.StatusForbidden {
		t.Fatalf("LỖ HỔNG RBAC: Người dùng thường xem được /api/admin/logs (status: %d)", wMemLogs.Code)
	}

	// Admin -> 200 OK
	reqAdmLogs := httptest.NewRequest("GET", "/api/admin/logs", nil)
	reqAdmLogs.Header.Set("Authorization", "Bearer "+adminToken)
	wAdmLogs := httptest.NewRecorder()
	AdminLogsHandler(wAdmLogs, reqAdmLogs)
	if wAdmLogs.Code != http.StatusOK {
		t.Fatalf("Admin bị từ chối truy cập /api/admin/logs: %d", wAdmLogs.Code)
	}

	// C. KIỂM TRA TRUY CẬP /api/admin/sessions
	// Member -> 403 Forbidden
	reqMemSess := httptest.NewRequest("GET", "/api/admin/sessions", nil)
	reqMemSess.Header.Set("Authorization", "Bearer "+memberToken)
	wMemSess := httptest.NewRecorder()
	AdminSessionsHandler(wMemSess, reqMemSess)
	if wMemSess.Code != http.StatusForbidden {
		t.Fatalf("LỖ HỔNG RBAC: Người dùng thường xem được /api/admin/sessions (status: %d)", wMemSess.Code)
	}

	// Admin -> 200 OK
	reqAdmSess := httptest.NewRequest("GET", "/api/admin/sessions", nil)
	reqAdmSess.Header.Set("Authorization", "Bearer "+adminToken)
	wAdmSess := httptest.NewRecorder()
	AdminSessionsHandler(wAdmSess, reqAdmSess)
	if wAdmSess.Code != http.StatusOK {
		t.Fatalf("Admin bị từ chối truy cập /api/admin/sessions: %d", wAdmSess.Code)
	}

	// D. KIỂM TRA ADMIN RESET PASSWORD CHO USER KHÁC
	// Member -> 403 Forbidden
	reqMemReset := httptest.NewRequest("POST", "/api/admin/users/reset-password", bytes.NewBufferString(`{"user_id":"`+memberUser.ID+`","new_password":"NewPass@789"}`))
	reqMemReset.Header.Set("Authorization", "Bearer "+memberToken)
	wMemReset := httptest.NewRecorder()
	AdminResetPasswordHandler(wMemReset, reqMemReset)
	if wMemReset.Code != http.StatusForbidden {
		t.Fatalf("LỖ HỔNG RBAC: Người dùng thường tự gọi được admin reset-password (status: %d)", wMemReset.Code)
	}

	// Admin -> 200 OK
	reqAdmReset := httptest.NewRequest("POST", "/api/admin/users/reset-password", bytes.NewBufferString(`{"user_id":"`+memberUser.ID+`","new_password":"NewPass@789"}`))
	reqAdmReset.Header.Set("Authorization", "Bearer "+adminToken)
	wAdmReset := httptest.NewRecorder()
	AdminResetPasswordHandler(wAdmReset, reqAdmReset)
	if wAdmReset.Code != http.StatusOK {
		t.Fatalf("Admin reset password thất bại: %d", wAdmReset.Code)
	}
}
