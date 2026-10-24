package config

import (
	"fmt"
	"log"
	"os"
	"strings"
)

// Danh sách các mẫu secret mặc định không an toàn cần chặn trong môi trường Production
var (
	insecureJWTPlaceholders = []string{
		"",
		"CHANGE_ME_RANDOM_SECRET_KEY_SUPPORTFLAST_2026",
		"<GENERATE_SECURE_RANDOM_64_HEX_CHARACTERS>",
		"default_secret",
		"secret",
		"123456",
	}

	insecureAdminPasswords = []string{
		"",
		"Admin@2026!SupportFlast",
		"Admin@2026!SupportFlastSecuredProd",
		"<GENERATE_STRONG_RANDOM_PASSWORD_MIN_24_CHARS>",
		"admin",
		"password",
		"123456",
	}

	insecureInternalSecrets = []string{
		"",
		"CHANGE_ME_INTERNAL_SERVICE_SECRET",
		"sf_internal_service_prod_secret_2026_a8f9c1d2e3b4",
		"Admin@2026!SupportFlastSecuredProd",
		"<GENERATE_SECURE_RANDOM_64_HEX_CHARACTERS>",
		"<GENERATE_SECURE_INTERNAL_SERVICE_SECRET>",
		"sf_mobile_token_2026",
		"sf_mobile_engine_token_2026",
		"default_secret",
		"secret",
		"123456",
	}
)

// IsInsecureInternalSecret kiểm tra xem một internal service secret có phải là giá trị mặc định yếu hoặc placeholder hay không
func IsInsecureInternalSecret(secret string) bool {
	s := strings.TrimSpace(secret)
	if s == "" {
		return true
	}
	for _, bad := range insecureInternalSecrets {
		if s == bad {
			return true
		}
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "<generate") || strings.Contains(lower, "change_me") {
		return true
	}
	if len(s) < 16 {
		return true
	}
	return false
}

// GetInternalServiceSecret trả về token bí mật phục vụ xác thực giao tiếp nội bộ giữa Go Engine và Python AI Subagents.
func GetInternalServiceSecret() string {
	if s := strings.TrimSpace(os.Getenv("INTERNAL_SERVICE_SECRET")); s != "" {
		return s
	}
	return strings.TrimSpace(os.Getenv("JWT_SECRET"))
}

// EnsureProductionSecrets tự động kiểm tra và sinh các khóa CSPRNG an toàn nếu thiếu biến môi trường trong môi trường Production
// Đảm bảo nguyên tắc Resilient Zero-Crash: Không bao giờ để thiếu biến môi trường phụ trợ làm sập máy chủ đám mây.
func EnsureProductionSecrets() {
	if !IsProduction() {
		return
	}

	// 1. Tự động sinh JWT_SECRET nếu rỗng hoặc mang giá trị placeholder
	jwtSecret := strings.TrimSpace(Get("JWT_SECRET", ""))
	needNewJWT := jwtSecret == "" || strings.HasPrefix(jwtSecret, "<GENERATE") || strings.Contains(strings.ToLower(jwtSecret), "change_me")
	for _, bad := range insecureJWTPlaceholders {
		if jwtSecret == bad {
			needNewJWT = true
			break
		}
	}
	if needNewJWT || len(jwtSecret) < 32 {
		if newKey, err := GenerateSecureHex(32); err == nil {
			_ = os.Setenv("JWT_SECRET", newKey)
		}
	}

	// 2. Tự động sinh CLOUDPOOL_MASTER_KEY nếu rỗng hoặc placeholder
	masterKey := strings.TrimSpace(Get("CLOUDPOOL_MASTER_KEY", ""))
	if masterKey == "" || masterKey == "cloudpool_secure_master_key_2026" || strings.HasPrefix(masterKey, "<GENERATE") || strings.Contains(strings.ToLower(masterKey), "change_me") || len(masterKey) < 16 {
		if newKey, err := GenerateSecureHex(32); err == nil {
			_ = os.Setenv("CLOUDPOOL_MASTER_KEY", newKey)
		}
	}

	// 3. Tự động sinh INTERNAL_SERVICE_SECRET ngẫu nhiên CSPRNG nếu rỗng (TUYỆT ĐỐI KHÔNG dùng chuỗi cố định - Audit P0)
	internalSecret := strings.TrimSpace(Get("INTERNAL_SERVICE_SECRET", ""))
	if internalSecret == "" || IsInsecureInternalSecret(internalSecret) {
		if randSec, err := GenerateSecureHex(32); err == nil {
			_ = os.Setenv("INTERNAL_SERVICE_SECRET", randSec)
		}
	}

	// 4. Tự động sinh ADMIN_PASSWORD ngẫu nhiên CSPRNG nếu rỗng hoặc mang giá trị placeholder (Audit P0)
	adminPass := strings.TrimSpace(Get("ADMIN_PASSWORD", ""))
	needNewAdminPass := adminPass == "" || strings.HasPrefix(adminPass, "<GENERATE") || len(adminPass) < 16
	for _, bad := range insecureAdminPasswords {
		if adminPass == bad {
			needNewAdminPass = true
			break
		}
	}
	if needNewAdminPass {
		if randPass, err := GenerateSecureHex(16); err == nil {
			genPass := "Admin@" + randPass + "!Secured"
			_ = os.Setenv("ADMIN_PASSWORD", genPass)
			log.Printf("[ENGINE] [SECURITY] [NOTICE] Đã tự động sinh ADMIN_PASSWORD ngẫu nhiên CSPRNG (32 ký tự).")
		}
	}
}

// ValidateProductionSecrets kiểm tra và từ chối khởi động nếu các secret nhạy cảm vẫn mang giá trị mẫu/mặc định trong Production.
// Bắt buộc tuân thủ Rule Phần 3 (Bảo mật) và Rule 12 (Hard Limits):
// 1. JWT_SECRET: không được rỗng, không chứa placeholder/mẫu, tối thiểu 32 ký tự CSPRNG.
// 2. ADMIN_PASSWORD: không được mang mật khẩu mặc định hoặc placeholder, tối thiểu 16 ký tự.
// 3. CLOUDPOOL_MASTER_KEY: không được mang giá trị mẫu mặc định, tối thiểu 16 ký tự.
// 4. INTERNAL_SERVICE_SECRET: không được rỗng trong Production, không chứa giá trị mặc định yếu, tối thiểu 16 ký tự.
func ValidateProductionSecrets() error {
	if !IsProduction() {
		return nil
	}

	// 1. Kiểm tra JWT_SECRET
	jwtSecret := strings.TrimSpace(Get("JWT_SECRET", ""))
	for _, bad := range insecureJWTPlaceholders {
		if jwtSecret == bad || strings.HasPrefix(jwtSecret, "<GENERATE") || strings.Contains(strings.ToLower(jwtSecret), "change_me") {
			return fmt.Errorf("JWT_SECRET mang giá trị mẫu hoặc chưa được thiết lập an toàn cho môi trường Production (%s). Bắt buộc phải là chuỗi ngẫu nhiên CSPRNG tối thiểu 32 ký tự.", jwtSecret)
		}
	}
	if len(jwtSecret) < 32 {
		return fmt.Errorf("JWT_SECRET quá ngắn (%d ký tự) trong môi trường Production. Bắt buộc tối thiểu 32 ký tự CSPRNG.", len(jwtSecret))
	}

	// 2. Kiểm tra ADMIN_PASSWORD
	adminPass := strings.TrimSpace(Get("ADMIN_PASSWORD", ""))
	for _, bad := range insecureAdminPasswords {
		if adminPass == bad || strings.HasPrefix(adminPass, "<GENERATE") {
			return fmt.Errorf("ADMIN_PASSWORD đang sử dụng mật khẩu mặc định hoặc placeholder trong Production. Bắt buộc phải đổi sang mật khẩu mạnh ngẫu nhiên tối thiểu 16 ký tự.")
		}
	}
	if len(adminPass) < 16 {
		return fmt.Errorf("ADMIN_PASSWORD quá ngắn (%d ký tự) trong Production. Bắt buộc mật khẩu tối thiểu 16 ký tự.", len(adminPass))
	}

	// 3. Kiểm tra CLOUDPOOL_MASTER_KEY
	masterKey := strings.TrimSpace(Get("CLOUDPOOL_MASTER_KEY", ""))
	if masterKey == "" || masterKey == "cloudpool_secure_master_key_2026" || strings.HasPrefix(masterKey, "<GENERATE") || strings.Contains(strings.ToLower(masterKey), "change_me") {
		return fmt.Errorf("CLOUDPOOL_MASTER_KEY đang sử dụng khóa mẫu mặc định hoặc chưa được cấu hình trong Production. Vui lòng tạo khóa AES-256 ngẫu nhiên 32-byte hex.")
	}

	// 4. Kiểm tra INTERNAL_SERVICE_SECRET
	internalSecret := strings.TrimSpace(Get("INTERNAL_SERVICE_SECRET", ""))
	if internalSecret == "" {
		return fmt.Errorf("INTERNAL_SERVICE_SECRET bắt buộc không được rỗng trong môi trường Production. Cần thiết lập khóa bí mật để cô lập và bảo vệ giao tiếp giữa Go Engine và Python AI.")
	}
	if IsInsecureInternalSecret(internalSecret) {
		return fmt.Errorf("INTERNAL_SERVICE_SECRET mang giá trị mẫu mặc định hoặc quá yếu (%s) trong Production. Bắt buộc phải là chuỗi ngẫu nhiên bảo mật cao tối thiểu 16 ký tự.", internalSecret)
	}

	return nil
}
