package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
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

// GetInternalSecretFilePath trả về đường dẫn tệp khóa bí mật nội bộ chia sẻ giữa Go và Python (data/keys/.internal_secret)
func GetInternalSecretFilePath() string {
	return GetPersistentSecretFilePath("internal_secret")
}

// GetPersistentSecretFilePath trả về đường dẫn tệp khóa bí mật lưu bền vững trong thư mục keys (data/keys/.{name})
func GetPersistentSecretFilePath(name string) string {
	keysDir := strings.TrimSpace(os.Getenv("JWT_KEYS_DIR"))
	if keysDir == "" {
		dataDir := strings.TrimSpace(os.Getenv("DATA_DIR"))
		if dataDir == "" {
			dataDir = "data"
		}
		keysDir = filepath.Join(dataDir, "keys")
	}
	return filepath.Join(keysDir, "."+name)
}

// readOrGeneratePersistentSecret đọc secret từ env, fallback sang tệp persistent trong data/keys/, hoặc tự sinh CSPRNG và lưu file
func readOrGeneratePersistentSecret(envKey, fileName string, length int, isBadFunc func(string) bool, formatFunc func(string) string) string {
	val := strings.TrimSpace(Get(envKey, ""))
	filePath := GetPersistentSecretFilePath(fileName)

	// 1. Nếu biến môi trường đã có và hợp lệ -> đồng bộ vào file bền vững nếu file chưa có
	if val != "" && !isBadFunc(val) {
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			_ = os.MkdirAll(filepath.Dir(filePath), 0700)
			_ = os.WriteFile(filePath, []byte(val), 0600)
		}
		return val
	}

	// 2. Thử đọc từ tệp bền vững
	if data, err := os.ReadFile(filePath); err == nil {
		saved := strings.TrimSpace(string(data))
		if saved != "" && !isBadFunc(saved) {
			_ = os.Setenv(envKey, saved)
			return saved
		}
	}

	// 3. Nếu vẫn thiếu hoặc không hợp lệ -> sinh mới CSPRNG và lưu bền vững vào file
	if randHex, err := GenerateSecureHex(length); err == nil {
		finalVal := randHex
		if formatFunc != nil {
			finalVal = formatFunc(randHex)
		}
		_ = os.Setenv(envKey, finalVal)
		_ = os.MkdirAll(filepath.Dir(filePath), 0700)
		_ = os.WriteFile(filePath, []byte(finalVal), 0600)
		log.Printf("[ENGINE] [SECURITY] Đã tự động sinh và lưu bền vững %s tại '%s' (Quyền: 0600)", envKey, filePath)
		return finalVal
	}

	return ""
}

// GetInternalServiceSecret trả về token bí mật phục vụ xác thực giao tiếp nội bộ giữa Go Engine và Python AI Subagents.
// Đồng bộ 100% với Python AI qua biến môi trường hoặc tệp dùng chung (Audit P0).
func GetInternalServiceSecret() string {
	if s := strings.TrimSpace(os.Getenv("INTERNAL_SERVICE_SECRET")); s != "" && !IsInsecureInternalSecret(s) {
		return s
	}

	// Đọc từ shared secret file (data/keys/.internal_secret) nếu biến môi trường rỗng
	filePath := GetInternalSecretFilePath()
	if data, err := os.ReadFile(filePath); err == nil {
		sec := strings.TrimSpace(string(data))
		if sec != "" && !IsInsecureInternalSecret(sec) {
			_ = os.Setenv("INTERNAL_SERVICE_SECRET", sec)
			return sec
		}
	}

	if jwt := strings.TrimSpace(os.Getenv("JWT_SECRET")); jwt != "" && !IsInsecureInternalSecret(jwt) {
		return jwt
	}
	return ""
}

// EnsureProductionSecrets tự động kiểm tra và sinh các khóa CSPRNG an toàn nếu thiếu biến môi trường trong môi trường Production.
// Bắt buộc lưu trữ bền vững vào data/keys/ để chống mất phiên đăng nhập, mất khóa giải mã hoặc lệch mật khẩu khi restart (Audit P0).
func EnsureProductionSecrets() {
	if !IsProduction() {
		return
	}

	// 1. Tự động sinh và lưu bền vững JWT_SECRET nếu rỗng hoặc mang giá trị placeholder (32 hex bytes = 64 chars)
	readOrGeneratePersistentSecret("JWT_SECRET", "jwt_secret", 32, func(s string) bool {
		if len(s) < 32 || strings.HasPrefix(s, "<GENERATE") || strings.Contains(strings.ToLower(s), "change_me") {
			return true
		}
		for _, bad := range insecureJWTPlaceholders {
			if s == bad {
				return true
			}
		}
		return false
	}, nil)

	// 2. Tự động sinh và lưu bền vững CLOUDPOOL_MASTER_KEY nếu rỗng hoặc placeholder (32 hex bytes = 64 chars)
	readOrGeneratePersistentSecret("CLOUDPOOL_MASTER_KEY", "master_key", 32, func(s string) bool {
		return s == "" || s == "cloudpool_secure_master_key_2026" || strings.HasPrefix(s, "<GENERATE") || strings.Contains(strings.ToLower(s), "change_me") || len(s) < 16
	}, nil)

	// 3. Tự động sinh và lưu bền vững INTERNAL_SERVICE_SECRET nếu rỗng (Audit P0)
	readOrGeneratePersistentSecret("INTERNAL_SERVICE_SECRET", "internal_secret", 32, IsInsecureInternalSecret, nil)

	// 4. Tự động sinh và lưu bền vững ADMIN_PASSWORD ngẫu nhiên CSPRNG nếu rỗng hoặc mang giá trị placeholder (Audit P0)
	readOrGeneratePersistentSecret("ADMIN_PASSWORD", "admin_secret", 16, func(s string) bool {
		return s == "" || strings.HasPrefix(s, "<GENERATE")
	}, func(randHex string) string {
		return "Admin@" + randHex + "!Secured"
	})
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
	if adminPass == "" || strings.HasPrefix(adminPass, "<GENERATE") {
		return fmt.Errorf("ADMIN_PASSWORD chưa được thiết lập trong Production.")
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
