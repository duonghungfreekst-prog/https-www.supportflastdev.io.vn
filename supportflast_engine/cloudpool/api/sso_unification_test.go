package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"supportflast_engine/cloudpool/storage"
	"supportflast_engine/registry"
	"supportflast_engine/security"
)

func setupTestServer(t *testing.T) (*Server, *storage.DB) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_sso.db")

	db, err := storage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("Không thể khởi tạo test DB: %v", err)
	}

	server := &Server{
		db:      db,
		baseDir: tempDir,
	}

	if err := InitJWTKeys(tempDir); err != nil {
		t.Fatalf("InitJWTKeys thất bại: %v", err)
	}

	return server, db
}

// TestSSO_AdminTokenUnification kiểm tra token RS256 do SupportFlast Hub cấp cho Admin
// được CloudPool chấp nhận 100% qua mọi cơ chế (Bearer, Cookie, Query)
func TestSSO_AdminTokenUnification(t *testing.T) {
	server, db := setupTestServer(t)
	defer db.Close()

	// 1. Giả lập Hub cấp token RS256 cho tài khoản Admin ("usr-admin-001")
	adminHubUser := registry.User{
		ID:       "usr-admin-001",
		Username: "admin",
		Email:    "admin@supportflastdev.io.vn",
		Role:     "admin",
	}

	hubAdminToken, err := registry.IssueRS256Token(adminHubUser, 24*time.Hour)
	if err != nil {
		t.Fatalf("Hub IssueRS256Token thất bại: %v", err)
	}

	// 2. Kiểm tra thẩm định chữ ký RS256 bằng hàm VerifyJWTClaims của CloudPool
	claims, err := VerifyJWTClaims(hubAdminToken)
	if err != nil {
		t.Fatalf("CloudPool VerifyJWTClaims từ chối token của Hub: %v", err)
	}
	if claims.Role != "admin" || claims.Username != "admin" {
		t.Fatalf("Claims không khớp: username=%s, role=%s", claims.Username, claims.Role)
	}

	// 3. Test lấy user qua Header Authorization: Bearer <token>
	reqHeader := httptest.NewRequest("GET", "/api/stats", nil)
	reqHeader.Header.Set("Authorization", "Bearer "+hubAdminToken)
	userFromHeader := server.getUserFromRequest(reqHeader)
	if userFromHeader == nil {
		t.Fatal("getUserFromRequest trả về nil khi dùng Authorization Bearer header")
	}
	if userFromHeader.Role != "admin" {
		t.Fatalf("Yêu cầu quyền admin, nhận được role: %s", userFromHeader.Role)
	}

	// 4. Test lấy user qua Cookie cloudpool_token
	reqCookie := httptest.NewRequest("GET", "/api/accounts", nil)
	reqCookie.AddCookie(&http.Cookie{
		Name:  "cloudpool_token",
		Value: hubAdminToken,
	})
	userFromCookie := server.getUserFromRequest(reqCookie)
	if userFromCookie == nil {
		t.Fatal("getUserFromRequest trả về nil khi dùng cookie cloudpool_token")
	}
	if userFromCookie.Role != "admin" {
		t.Fatalf("Yêu cầu quyền admin qua cookie cloudpool_token, nhận được: %s", userFromCookie.Role)
	}

	// 5. Test lấy user qua Cookie sf_auth_token (SSO cookie từ Hub)
	reqSFCookie := httptest.NewRequest("GET", "/api/files", nil)
	reqSFCookie.AddCookie(&http.Cookie{
		Name:  "sf_auth_token",
		Value: hubAdminToken,
	})
	userFromSFCookie := server.getUserFromRequest(reqSFCookie)
	if userFromSFCookie == nil {
		t.Fatal("getUserFromRequest trả về nil khi dùng cookie sf_auth_token")
	}
	if userFromSFCookie.Role != "admin" {
		t.Fatalf("Yêu cầu quyền admin qua cookie sf_auth_token, nhận được: %s", userFromSFCookie.Role)
	}

	// 6. Test lấy user qua URL Query Parameter (?token=...)
	reqQuery := httptest.NewRequest("GET", "/api/files/download?id=file123&token="+hubAdminToken, nil)
	userFromQuery := server.getUserFromRequest(reqQuery)
	if userFromQuery == nil {
		t.Fatal("getUserFromRequest trả về nil khi dùng URL Query token")
	}
	if userFromQuery.Role != "admin" {
		t.Fatalf("Yêu cầu quyền admin qua Query token, nhận được: %s", userFromQuery.Role)
	}
}

// TestSSO_RegularUserJITProvisioning kiểm tra người dùng mới của Hub chưa có trong CSDL CloudPool
// được tự động đồng bộ JIT và cấp phát quyền hạn tức thì không bị lỗi 403
func TestSSO_RegularUserJITProvisioning(t *testing.T) {
	server, db := setupTestServer(t)
	defer db.Close()

	// 1. Hub cấp token cho developer mới chưa từng truy cập CloudPool
	newDevUser := registry.User{
		ID:       "usr-dev-888999",
		Username: "developer_quang",
		Email:    "quang@supportflastdev.io.vn",
		Role:     "developer",
	}

	token, err := registry.IssueRS256Token(newDevUser, 24*time.Hour)
	if err != nil {
		t.Fatalf("IssueRS256Token thất bại: %v", err)
	}

	// 2. Gửi request tới CloudPool
	req := httptest.NewRequest("GET", "/api/files", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	user := server.getUserFromRequest(req)
	if user == nil {
		t.Fatal("getUserFromRequest thất bại với người dùng JIT mới")
	}

	if user.Username != "developer_quang" {
		t.Fatalf("Expected username 'developer_quang', got: %s", user.Username)
	}
	if user.Role != "developer" {
		t.Fatalf("Expected role 'developer', got: %s", user.Role)
	}

	// 3. Xác minh người dùng đã được lưu vào CSDL CloudPool
	dbUser, err := db.GetUserByID(user.ID)
	if err != nil {
		t.Fatalf("Người dùng không được tìm thấy trong CSDL CloudPool sau khi JIT provisioning: %v", err)
	}
	if dbUser.Username != "developer_quang" {
		t.Fatalf("Dữ liệu CSDL không khớp: %s", dbUser.Username)
	}
}

// TestSSO_TamperedTokenRejected kiểm tra chữ ký token bị giả mạo bị từ chối an toàn
func TestSSO_TamperedTokenRejected(t *testing.T) {
	server, db := setupTestServer(t)
	defer db.Close()

	validToken, err := GenerateJWT("user_admin")
	if err != nil {
		t.Fatalf("GenerateJWT thất bại: %v", err)
	}

	// Giả mạo payload token
	tamperedToken := validToken + "tampered"
	req := httptest.NewRequest("GET", "/api/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+tamperedToken)

	user := server.getUserFromRequest(req)
	if user != nil {
		t.Fatal("Token bị giả mạo nhưng vẫn được chấp nhận!")
	}
}

// TestSSO_ExpiredTokenRejected kiểm tra token hết hạn bị từ chối
func TestSSO_ExpiredTokenRejected(t *testing.T) {
	server, db := setupTestServer(t)
	defer db.Close()

	// Cấp token đã hết hạn 1 giờ trước
	expiredClaims := &security.UserClaims{
		UserID:    "usr-admin-001",
		Username:  "admin",
		Role:      "admin",
		IssuedAt:  time.Now().Add(-2 * time.Hour).Unix(),
		ExpiresAt: time.Now().Add(-1 * time.Hour).Unix(),
		Issuer:    "supportflast-auth",
		Subject:   "usr-admin-001",
	}

	expiredToken, err := security.GenerateRS256Token(expiredClaims)
	if err != nil {
		t.Fatalf("GenerateRS256Token thất bại: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+expiredToken)

	user := server.getUserFromRequest(req)
	if user != nil {
		t.Fatal("Token hết hạn nhưng vẫn được chấp nhận!")
	}
}

// TestOAuthCallbackRoute kiểm tra route /api/accounts/oauth/callback và /api/accounts/oauth/url
// được AccountsHandler định tuyến chính xác tới các handler chuyên biệt (không bị 404 hay 503)
func TestOAuthCallbackRoute(t *testing.T) {
	server, db := setupTestServer(t)
	defer db.Close()

	server.setupSubMuxes()

	// 1. Kiểm tra /api/accounts/oauth/callback khi gọi GET thiếu tham số 'code'
	req := httptest.NewRequest(http.MethodGet, "/api/accounts/oauth/callback", nil)
	rec := httptest.NewRecorder()

	server.AccountsHandler(rec, req)

	// Khi thiếu code, handleOAuthCallback phải phản hồi HTTP 400 Bad Request (chứng minh route khớp 100%)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Kỳ vọng HTTP 400 Bad Request khi thiếu code, thực tế nhận: %d, body: %s", rec.Code, rec.Body.String())
	}

	// 2. Kiểm tra /api/accounts/oauth/url
	reqURL := httptest.NewRequest(http.MethodGet, "/api/accounts/oauth/url", nil)
	recURL := httptest.NewRecorder()

	server.AccountsHandler(recURL, reqURL)

	// Khi chưa cấu hình Google Client ID thì phản hồi 400 (chứng minh route oauth/url khớp 100%)
	if recURL.Code != http.StatusBadRequest && recURL.Code != http.StatusOK {
		t.Fatalf("Kỳ vọng route oauth/url được xử lý hợp lệ, thực tế nhận: %d, body: %s", recURL.Code, recURL.Body.String())
	}
}

