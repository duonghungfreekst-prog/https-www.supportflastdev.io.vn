package security

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGenerateAndValidateRS256Token_Success(t *testing.T) {
	// Đảm bảo cặp khóa RSA được nạp
	if err := EnsureRSAKeys(); err != nil {
		t.Fatalf("EnsureRSAKeys thất bại: %v", err)
	}

	claims := &UserClaims{
		UserID:    "usr-test-123",
		Username:  "quocviet_developer",
		Email:     "developer@supportflastdev.io.vn",
		Role:      "developer",
		IssuedAt:  time.Now().Unix(),
		ExpiresAt: time.Now().Add(15 * time.Minute).Unix(),
		Issuer:    "supportflast-auth",
		Subject:   "usr-test-123",
	}

	tokenStr, err := GenerateRS256Token(claims)
	if err != nil {
		t.Fatalf("GenerateRS256Token thất bại: %v", err)
	}

	if tokenStr == "" {
		t.Fatal("Token sinh ra không được rỗng")
	}

	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT phải có cấu trúc 3 phần ngăn cách bởi dấu chấm, nhận được: %d phần", len(parts))
	}

	// Thẩm định token
	validatedClaims, err := ValidateRS256Token(tokenStr)
	if err != nil {
		t.Fatalf("ValidateRS256Token thất bại với token hợp lệ: %v", err)
	}

	if validatedClaims.UserID != claims.UserID {
		t.Errorf("UserID mong đợi %s, nhận được %s", claims.UserID, validatedClaims.UserID)
	}
	if validatedClaims.Username != claims.Username {
		t.Errorf("Username mong đợi %s, nhận được %s", claims.Username, validatedClaims.Username)
	}
	if validatedClaims.Email != claims.Email {
		t.Errorf("Email mong đợi %s, nhận được %s", claims.Email, validatedClaims.Email)
	}
	if validatedClaims.Role != claims.Role {
		t.Errorf("Role mong đợi %s, nhận được %s", claims.Role, validatedClaims.Role)
	}
	if validatedClaims.Issuer != claims.Issuer {
		t.Errorf("Issuer mong đợi %s, nhận được %s", claims.Issuer, validatedClaims.Issuer)
	}
}

func TestValidateExpiredToken(t *testing.T) {
	pastTime := time.Now().Add(-1 * time.Hour).Unix()
	claims := &UserClaims{
		UserID:    "usr-expired",
		Username:  "expired_user",
		Role:      "user",
		IssuedAt:  pastTime - 3600,
		ExpiresAt: pastTime,
	}

	tokenStr, err := GenerateRS256Token(claims)
	if err != nil {
		t.Fatalf("GenerateRS256Token thất bại: %v", err)
	}

	_, err = ValidateRS256Token(tokenStr)
	if err == nil {
		t.Fatal("Mong đợi lỗi hết hạn token nhưng ValidateRS256Token lại thành công")
	}

	if err != ErrTokenExpired {
		t.Errorf("Mong đợi ErrTokenExpired, nhận được: %v", err)
	}
}

func TestTamperedToken_SignatureFail(t *testing.T) {
	claims := &UserClaims{
		UserID:    "usr-victim",
		Username:  "normal_user",
		Role:      "developer",
		IssuedAt:  time.Now().Unix(),
		ExpiresAt: time.Now().Add(1 * time.Hour).Unix(),
	}

	tokenStr, err := GenerateRS256Token(claims)
	if err != nil {
		t.Fatalf("GenerateRS256Token thất bại: %v", err)
	}

	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		t.Fatalf("Token không đúng định dạng 3 phần")
	}

	// Giả mạo payload nâng quyền lên "admin"
	tamperedPayload := map[string]interface{}{
		"user_id":  "usr-victim",
		"username": "normal_user",
		"role":     "admin", // Cố tình leo thang đặc quyền
		"exp":      time.Now().Add(1 * time.Hour).Unix(),
	}
	tamperedBytes, _ := json.Marshal(tamperedPayload)
	tamperedPayloadB64 := base64.RawURLEncoding.EncodeToString(tamperedBytes)

	tamperedToken := parts[0] + "." + tamperedPayloadB64 + "." + parts[2]

	_, err = ValidateRS256Token(tamperedToken)
	if err == nil {
		t.Fatal("Kẻ tấn công giả mạo payload nhưng chữ ký vẫn được chấp nhận!")
	}
	if err != ErrSignatureInvalid {
		t.Logf("Token giả mạo bị từ chối chính xác: %v", err)
	}

	// Thử làm hỏng chữ ký số
	corruptedSig := parts[2][:len(parts[2])-4] + "AAAA"
	corruptedToken := parts[0] + "." + parts[1] + "." + corruptedSig
	_, err = ValidateRS256Token(corruptedToken)
	if err == nil {
		t.Fatal("Chữ ký bị sửa đổi nhưng vẫn được chấp nhận!")
	}
}

func TestAlgorithmConfusionAttack(t *testing.T) {
	// Giả mạo token với header "alg": "none" (tấn công vượt qua xác thực)
	noneHeader := map[string]string{
		"alg": "none",
		"typ": "JWT",
	}
	hBytes, _ := json.Marshal(noneHeader)
	hB64 := base64.RawURLEncoding.EncodeToString(hBytes)

	payload := map[string]interface{}{
		"user_id":  "admin-001",
		"username": "admin",
		"role":     "admin",
		"exp":      time.Now().Add(1 * time.Hour).Unix(),
	}
	pBytes, _ := json.Marshal(payload)
	pB64 := base64.RawURLEncoding.EncodeToString(pBytes)

	noneToken := hB64 + "." + pB64 + "." // Không có signature

	_, err := ValidateRS256Token(noneToken)
	if err == nil {
		t.Fatal("Lỗ hổng nghiêm trọng: Hệ thống chấp nhận token với alg: none!")
	}
	if !strings.Contains(err.Error(), "RS256") {
		t.Errorf("Mong đợi lỗi thuật toán không hỗ trợ, nhận được: %v", err)
	}

	// Giả mạo token với "alg": "HS256" (tấn công Algorithm Confusion biến Public Key thành HMAC Secret)
	hsHeader := map[string]string{
		"alg": "HS256",
		"typ": "JWT",
	}
	hsBytes, _ := json.Marshal(hsHeader)
	hsB64 := base64.RawURLEncoding.EncodeToString(hsBytes)
	hsToken := hsB64 + "." + pB64 + ".somehmacsignature"

	_, err = ValidateRS256Token(hsToken)
	if err == nil {
		t.Fatal("Hệ thống chấp nhận thuật toán HS256 trong khi quy định bắt buộc RS256!")
	}
}

func TestKeyGenerationAndPersistence(t *testing.T) {
	// Sử dụng thư mục tạm thời trong ổ F theo Rule 1.4
	testKeysDir := filepath.Join(`f:\supportflast.dev\data`, "test_keys_temp")
	_ = os.RemoveAll(testKeysDir)
	defer os.RemoveAll(testKeysDir)

	SetCustomKeysDir(testKeysDir)
	defer SetCustomKeysDir("") // Reset về mặc định

	// Đảm bảo sinh khóa trong thư mục test
	if err := EnsureRSAKeys(); err != nil {
		t.Fatalf("Khởi tạo khóa tại thư mục test thất bại: %v", err)
	}

	privPath := filepath.Join(testKeysDir, "private.pem")
	pubPath := filepath.Join(testKeysDir, "public.pem")

	if _, err := os.Stat(privPath); err != nil {
		t.Fatalf("File private.pem không được tạo: %v", err)
	}
	if _, err := os.Stat(pubPath); err != nil {
		t.Fatalf("File public.pem không được tạo: %v", err)
	}

	pubPEM := GetPublicKeyPEM()
	if !strings.Contains(pubPEM, "-----BEGIN PUBLIC KEY-----") {
		t.Fatalf("GetPublicKeyPEM không chứa header PEM chuẩn: %s", pubPEM)
	}

	// Kiểm tra độ dài khóa RSA là 2048 bit
	rsaMutex.RLock()
	keyLen := rsaPrivateKey.N.BitLen()
	pubKey := rsaPublicKey
	rsaMutex.RUnlock()

	if keyLen != 2048 {
		t.Fatalf("Độ dài khóa RSA mong đợi 2048 bit, nhận được: %d bit", keyLen)
	}
	if pubKey == nil {
		t.Fatal("Public Key bị nil")
	}

	// Đặt lại rsaPrivateKey = nil để kiểm tra cơ chế đọc lại khóa đã lưu từ đĩa
	rsaMutex.Lock()
	rsaPrivateKey = nil
	rsaPublicKey = nil
	rsaPublicKeyPEM = ""
	rsaMutex.Unlock()

	if err := EnsureRSAKeys(); err != nil {
		t.Fatalf("Đọc lại khóa từ đĩa thất bại: %v", err)
	}

	rsaMutex.RLock()
	reloadedLen := rsaPrivateKey.N.BitLen()
	rsaMutex.RUnlock()

	if reloadedLen != 2048 {
		t.Fatalf("Khóa nạp lại từ đĩa có độ dài không đúng: %d bit", reloadedLen)
	}
}

func TestConstantTimeCompareTokenHash(t *testing.T) {
	tokenA := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signatureA"
	tokenB := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signatureA"
	tokenC := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signatureB"

	if !ConstantTimeCompareTokenHash(tokenA, tokenB) {
		t.Fatal("Hai token giống nhau nhưng ConstantTimeCompareTokenHash trả về false")
	}

	if ConstantTimeCompareTokenHash(tokenA, tokenC) {
		t.Fatal("Hai token khác nhau nhưng ConstantTimeCompareTokenHash lại trả về true")
	}
}
