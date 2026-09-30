package api

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"supportflast_engine/registry"
	"supportflast_engine/security"
)

// InitJWTKeys nạp hoặc khởi tạo cặp khóa RSA 2048-bit chung của toàn hệ thống (Rule 3.2).
// Tích hợp trực tiếp với security.EnsureRSAKeys() của SupportFlast Hub để dùng chung cặp khóa tuyệt đối.
func InitJWTKeys(baseDir string) error {
	if err := security.EnsureRSAKeys(); err != nil {
		return fmt.Errorf("không thể khởi tạo cặp khóa RSA 2048-bit hợp nhất: %w", err)
	}
	log.Printf("[ENGINE] [SECURITY] [SSO] Đã đồng bộ cặp khóa RSA 2048-bit (RS256) từ Hub dùng chung cho CloudPool")
	return nil
}

// GenerateJWT tạo token JWT RS256 chuẩn SupportFlast cho một userID
func GenerateJWT(userID string) (string, error) {
	role := "user"
	username := userID
	cleanID := strings.TrimSpace(userID)
	if cleanID == "user_admin" || cleanID == "admin" || cleanID == "usr-admin-001" {
		role = "admin"
		username = "admin"
	}
	return GenerateJWTWithRole(cleanID, username, role)
}

// GenerateJWTWithRole tạo token JWT RS256 với đầy đủ vai trò và thông tin người dùng
func GenerateJWTWithRole(userID, username, role string) (string, error) {
	cleanID := strings.TrimSpace(userID)
	if cleanID == "" {
		return "", errors.New("userID không được để trống")
	}
	if username == "" {
		username = cleanID
	}
	if role == "" {
		role = "user"
	}
	ttl := registry.GetSessionDuration()
	
	claims := &security.UserClaims{
		UserID:    cleanID,
		Username:  username,
		Role:      role,
		IssuedAt:  time.Now().Unix(),
		ExpiresAt: time.Now().Add(ttl).Unix(),
		Issuer:    "supportflast-auth",
		Subject:   cleanID,
	}
	return security.GenerateRS256Token(claims)
}

// VerifyJWT thẩm định tính hợp lệ của token JWT RS256 và trả về UserID
func VerifyJWT(tokenString string) (string, error) {
	claims, err := VerifyJWTClaims(tokenString)
	if err != nil {
		return "", err
	}
	uid := strings.TrimSpace(claims.UserID)
	if uid == "" {
		uid = strings.TrimSpace(claims.Subject)
	}
	if uid == "" {
		uid = strings.TrimSpace(claims.Username)
	}
	return uid, nil
}

// VerifyJWTClaims thẩm định token JWT RS256 qua security.ValidateRS256Token và trả về đầy đủ UserClaims
func VerifyJWTClaims(tokenString string) (*security.UserClaims, error) {
	cleanToken := strings.TrimSpace(tokenString)
	if cleanToken == "" {
		return nil, security.ErrInvalidToken
	}
	// Kiểm tra xem token đã bị thu hồi/đăng xuất chưa (Bảo mật thu hồi phiên)
	if registry.IsTokenRevoked(cleanToken) {
		return nil, errors.New("token đã bị thu hồi hoặc đã đăng xuất")
	}
	return security.ValidateRS256Token(cleanToken)
}

