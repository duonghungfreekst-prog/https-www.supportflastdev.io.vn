package service

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"time"

	"supportflast_engine/internal/model"
	"supportflast_engine/internal/repository"
	"supportflast_engine/pkg/core_ffi"
)

// AuthService định nghĩa các operations xác thực
type AuthService interface {
	Register(username, email, password string) (*model.User, error)
	Login(username, password string) (*model.User, string, error) // user, token, error
	ValidateToken(token string) (*model.User, error)
	ChangePassword(userID, oldPassword, newPassword string) error
	GetUserByID(id string) (*model.User, error)
}

type authServiceImpl struct {
	userRepo  repository.UserRepository
	auditRepo repository.AuditRepository
}

func NewAuthService(userRepo repository.UserRepository, auditRepo repository.AuditRepository) AuthService {
	return &authServiceImpl{
		userRepo:  userRepo,
		auditRepo: auditRepo,
	}
}

func generateSalt() ([]byte, error) {
	salt := make([]byte, 16)
	_, err := rand.Read(salt)
	if err != nil {
		return nil, err
	}
	return salt, nil
}

// Register đăng ký tài khoản mới với mật khẩu mã hóa an toàn qua Rust FFI
func (s *authServiceImpl) Register(username, email, password string) (*model.User, error) {
	// Kiểm tra username đã tồn tại
	if existing, _ := s.userRepo.FindByUsername(username); existing != nil {
		return nil, fmt.Errorf("auth: Username đã tồn tại")
	}

	salt, err := generateSalt()
	if err != nil {
		return nil, fmt.Errorf("auth: Lỗi sinh salt: %v", err)
	}

	// Gọi FFI Rust Core (Argon2id KDF) - Rule 7.1 + Rule 3.2
	hashedBytes, err := core_ffi.DeriveKeyArgon2(password, salt)
	if err != nil {
		return nil, fmt.Errorf("auth: Lỗi hash mật khẩu từ Rust Core: %v", err)
	}

	// Lưu DB dạng: salt_hex:hash_hex
	passwordHash := hex.EncodeToString(salt) + ":" + hex.EncodeToString(hashedBytes)

	user := &model.User{
		ID:           "usr_" + hex.EncodeToString(salt[:8]), // Simple ID generation
		Username:     username,
		Email:        email,
		PasswordHash: passwordHash,
		Role:         string(model.RoleUser),
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
		UpdatedAt:    time.Now().UTC().Format(time.RFC3339),
	}

	if err := s.userRepo.Create(user); err != nil {
		return nil, fmt.Errorf("auth: Lỗi lưu user: %v", err)
	}

	return user, nil
}

// Login đăng nhập và tạo JWT
func (s *authServiceImpl) Login(username, password string) (*model.User, string, error) {
	user, err := s.userRepo.FindByUsername(username)
	if err != nil || user == nil {
		return nil, "", fmt.Errorf("auth: Sai tài khoản hoặc mật khẩu")
	}

	// Phân tách salt và hash từ DB
	var saltHex, hashHex string
	fmt.Sscanf(user.PasswordHash, "%s:%s", &saltHex, &hashHex)

	salt, _ := hex.DecodeString(saltHex)
	expectedHash, _ := hex.DecodeString(hashHex)

	// Gọi FFI Rust Core để tính hash của input password
	hashedBytes, err := core_ffi.DeriveKeyArgon2(password, salt)
	if err != nil {
		return nil, "", fmt.Errorf("auth: Lỗi xác thực từ Rust Core: %v", err)
	}

	// So sánh thời gian không đổi bằng crypto/subtle chống Timing Attack (Rule 3.6)
	if subtle.ConstantTimeCompare(hashedBytes, expectedHash) != 1 {
		return nil, "", fmt.Errorf("auth: Sai tài khoản hoặc mật khẩu")
	}

	// Todo: Create JWT token using asymmetric keys (RS256) as per Rule 3.2
	token := "dummy_jwt_token_for_" + user.Username

	user.LastLogin = time.Now().UTC().Format(time.RFC3339)
	s.userRepo.Update(user)

	return user, token, nil
}

func (s *authServiceImpl) ValidateToken(token string) (*model.User, error) {
	return nil, fmt.Errorf("auth: ValidateToken chưa được triển khai")
}

func (s *authServiceImpl) ChangePassword(userID, oldPassword, newPassword string) error {
	return fmt.Errorf("auth: ChangePassword chưa được triển khai")
}

func (s *authServiceImpl) GetUserByID(id string) (*model.User, error) {
	return s.userRepo.FindByID(id)
}
