package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadEnvFile(t *testing.T) {
	// Tạo file .env tạm thời để test
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")

	content := "\xef\xbb\xbf# Dòng chú thích đầu tiên\n" +
		"TEST_PORT=9090\n" +
		"export TEST_HOST=127.0.0.1\n" +
		"TEST_QUOTED=\"hello world\\nnewline\"\n" +
		"TEST_SINGLE_QUOTED='single quoted string'\n" +
		"TEST_INLINE=some_value # Chú thích inline\n" +
		"   TEST_TRIMMED   =   trimmed_value   \n" +
		"\n" +
		"# Dòng trống ở trên\n" +
		"TEST_EMPTY=\n"

	if err := os.WriteFile(envPath, []byte(content), 0644); err != nil {
		t.Fatalf("Không thể tạo file test .env: %v", err)
	}

	count, err := LoadEnvFile(envPath)
	if err != nil {
		t.Fatalf("LoadEnvFile lỗi: %v", err)
	}

	if count < 5 {
		t.Errorf("Số lượng biến nạp được mong đợi >= 5, thực tế: %d", count)
	}

	if val := os.Getenv("TEST_PORT"); val != "9090" {
		t.Errorf("TEST_PORT mong đợi '9090', thực tế: '%s'", val)
	}

	if val := os.Getenv("TEST_HOST"); val != "127.0.0.1" {
		t.Errorf("TEST_HOST mong đợi '127.0.0.1', thực tế: '%s'", val)
	}

	if val := os.Getenv("TEST_QUOTED"); val != "hello world\nnewline" {
		t.Errorf("TEST_QUOTED unescape sai, thực tế: '%s'", val)
	}

	if val := os.Getenv("TEST_SINGLE_QUOTED"); val != "single quoted string" {
		t.Errorf("TEST_SINGLE_QUOTED mong đợi 'single quoted string', thực tế: '%s'", val)
	}

	if val := os.Getenv("TEST_INLINE"); val != "some_value" {
		t.Errorf("TEST_INLINE mong đợi 'some_value', thực tế: '%s'", val)
	}

	if val := os.Getenv("TEST_TRIMMED"); val != "trimmed_value" {
		t.Errorf("TEST_TRIMMED mong đợi 'trimmed_value', thực tế: '%s'", val)
	}
}

func TestGenerateSecurePassword(t *testing.T) {
	pass1, err := GenerateSecurePassword(24)
	if err != nil {
		t.Fatalf("GenerateSecurePassword lỗi: %v", err)
	}
	if len(pass1) != 24 {
		t.Errorf("Độ dài mật khẩu mong đợi 24, thực tế: %d", len(pass1))
	}

	pass2, err := GenerateSecurePassword(24)
	if err != nil {
		t.Fatalf("GenerateSecurePassword lần 2 lỗi: %v", err)
	}

	if pass1 == pass2 {
		t.Errorf("Hai mật khẩu liên tiếp không được trùng nhau")
	}

	// Kiểm tra độ phức tạp: chữ hoa, chữ thường, số, ký tự đặc biệt
	hasUpper := strings.ContainsAny(pass1, "ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	hasLower := strings.ContainsAny(pass1, "abcdefghijklmnopqrstuvwxyz")
	hasDigit := strings.ContainsAny(pass1, "0123456789")
	hasSpecial := strings.ContainsAny(pass1, "!@#$%^&*-_=+")

	if !hasUpper || !hasLower || !hasDigit || !hasSpecial {
		t.Errorf("Mật khẩu sinh ra thiếu một trong các nhóm ký tự: upper=%v lower=%v digit=%v special=%v",
			hasUpper, hasLower, hasDigit, hasSpecial)
	}
}

func TestGenerateSecureHex(t *testing.T) {
	hex1, err := GenerateSecureHex(32)
	if err != nil {
		t.Fatalf("GenerateSecureHex lỗi: %v", err)
	}
	// 32 bytes = 64 hex characters
	if len(hex1) != 64 {
		t.Errorf("Độ dài hex mong đợi 64, thực tế: %d", len(hex1))
	}

	hex2, err := GenerateSecureHex(32)
	if err != nil {
		t.Fatalf("GenerateSecureHex lần 2 lỗi: %v", err)
	}
	if hex1 == hex2 {
		t.Errorf("Hai chuỗi hex liên tiếp không được trùng nhau")
	}
}

func TestAutoGenerateEnvFromExample(t *testing.T) {
	tempDir := t.TempDir()
	exampleFile := filepath.Join(tempDir, ".env.example")
	targetEnv := filepath.Join(tempDir, ".env")

	template := "# Test Template\n" +
		"PORT=8080\n" +
		"ADMIN_PASSWORD=PLACEHOLDER_PASS\n" +
		"JWT_SECRET=PLACEHOLDER_SECRET\n"

	if err := os.WriteFile(exampleFile, []byte(template), 0644); err != nil {
		t.Fatalf("Không thể tạo file test .env.example: %v", err)
	}

	// Thiết lập ENV_EXAMPLE_FILE tạm thời
	os.Setenv("ENV_EXAMPLE_FILE", exampleFile)
	defer os.Unsetenv("ENV_EXAMPLE_FILE")

	createdPath, err := autoGenerateEnvFromExample()
	if err != nil {
		t.Fatalf("autoGenerateEnvFromExample lỗi: %v", err)
	}

	if createdPath != targetEnv {
		t.Errorf("Đường dẫn tạo ra mong đợi '%s', thực tế: '%s'", targetEnv, createdPath)
	}

	createdBytes, err := os.ReadFile(targetEnv)
	if err != nil {
		t.Fatalf("Không thể đọc file .env vừa tạo: %v", err)
	}
	content := string(createdBytes)

	if strings.Contains(content, "PLACEHOLDER_PASS") {
		t.Errorf("File .env vẫn còn giữ placeholder password chưa được thay thế")
	}
	if strings.Contains(content, "PLACEHOLDER_SECRET") {
		t.Errorf("File .env vẫn còn giữ placeholder secret chưa được thay thế")
	}

	if !strings.Contains(content, "ADMIN_PASSWORD=") || !strings.Contains(content, "JWT_SECRET=") {
		t.Errorf("File .env thiếu các trường ADMIN_PASSWORD hoặc JWT_SECRET")
	}
}

func TestConfigHelpers(t *testing.T) {
	os.Setenv("TEST_CFG_STR", "production")
	os.Setenv("TEST_CFG_INT", "8080")
	os.Setenv("TEST_CFG_BOOL", "true")

	if val := Get("TEST_CFG_STR", "dev"); val != "production" {
		t.Errorf("Get mong đợi 'production', nhận được: '%s'", val)
	}
	if val := Get("TEST_CFG_NON_EXISTENT", "default_val"); val != "default_val" {
		t.Errorf("Get fallback mong đợi 'default_val', nhận được: '%s'", val)
	}

	if val := GetInt("TEST_CFG_INT", 3000); val != 8080 {
		t.Errorf("GetInt mong đợi 8080, nhận được: %d", val)
	}
	if val := GetInt("TEST_CFG_NON_EXISTENT_INT", 3000); val != 3000 {
		t.Errorf("GetInt fallback mong đợi 3000, nhận được: %d", val)
	}

	if val := GetBool("TEST_CFG_BOOL", false); !val {
		t.Errorf("GetBool mong đợi true, nhận được false")
	}
	if val := GetBool("TEST_CFG_NON_EXISTENT_BOOL", true); !val {
		t.Errorf("GetBool fallback mong đợi true, nhận được false")
	}
}

func TestInitEnv(t *testing.T) {
	err := InitEnv()
	if err != nil {
		t.Fatalf("InitEnv trả về lỗi: %v", err)
	}

	loadedPath := GetLoadedEnvPath()
	if loadedPath == "" {
		t.Fatalf("InitEnv không nạp được đường dẫn file .env")
	}

	if !fileExists(loadedPath) {
		t.Errorf("File .env không tồn tại trên đĩa sau khi InitEnv: %s", loadedPath)
	}

	if GetLoadedCount() == 0 {
		t.Errorf("Số lượng biến nạp được bằng 0")
	}

	// Xác nhận các biến cốt lõi đã có trong môi trường
	requiredVars := []string{
		"PORT",
		"HOST",
		"DOMAIN",
		"ENV",
		"DATA_DIR",
		"STORAGE_DIR",
		"UI_DIR",
		"ADMIN_USERNAME",
		"ADMIN_PASSWORD",
		"JWT_SECRET",
	}

	for _, v := range requiredVars {
		if os.Getenv(v) == "" {
			t.Errorf("Biến môi trường bắt buộc '%s' không được nạp vào os.Environ", v)
		}
	}
}

func TestAutoGenerateDefaultEnv_WhenExampleMissing(t *testing.T) {
	tempDir := t.TempDir()
	os.Setenv("ENV_DIR", tempDir)
	defer os.Unsetenv("ENV_DIR")

	path, err := autoGenerateDefaultEnv()
	if err != nil {
		t.Fatalf("autoGenerateDefaultEnv thất bại: %v", err)
	}

	expectedPath := filepath.Join(tempDir, ".env")
	if path != expectedPath {
		t.Errorf("Đường dẫn mong đợi '%s', nhận được '%s'", expectedPath, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Không thể đọc file .env mặc định vừa sinh: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "ADMIN_USERNAME=admin") {
		t.Errorf("File .env thiếu ADMIN_USERNAME=admin")
	}
	if !strings.Contains(content, "ADMIN_PASSWORD=Admin@2026!SupportFlast") {
		t.Errorf("File .env thiếu ADMIN_PASSWORD=Admin@2026!SupportFlast")
	}
	if !strings.Contains(content, "JWT_SECRET=") {
		t.Errorf("File .env thiếu JWT_SECRET")
	}
	if !strings.Contains(content, "CLOUDPOOL_MASTER_KEY=") {
		t.Errorf("File .env thiếu CLOUDPOOL_MASTER_KEY")
	}
}
