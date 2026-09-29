package main

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"supportflast_engine/config"
	cloudpoolStorage "supportflast_engine/cloudpool/storage"
	"supportflast_engine/database"
	"supportflast_engine/registry"
	"supportflast_engine/security"

	"golang.org/x/crypto/bcrypt"
)

// BootstrapResult chứa thông tin chi tiết về các tài nguyên đã tự động khởi tạo
type BootstrapResult struct {
	EnvPath          string               `json:"env_path"`
	DataDir          string               `json:"data_dir"`
	StorageDir       string               `json:"storage_dir"`
	KeysDir          string               `json:"keys_dir"`
	CreatedDirs      []string             `json:"created_dirs"`
	PrivateKeyPath   string               `json:"private_key_path"`
	PublicKeyPath    string               `json:"public_key_path"`
	RSAKeysGenerated bool                 `json:"rsa_keys_generated"`
	MainDBPath       string               `json:"main_db_path"`
	CloudPoolDBPath  string               `json:"cloudpool_db_path"`
	AdminUserCreated bool                 `json:"admin_user_created"`
	AdminUsername    string               `json:"admin_username"`
	IsFirstRun       bool                 `json:"is_first_run"`
	StorageDB        *cloudpoolStorage.DB `json:"-"`
}

// ResolveDataDir xác định đường dẫn thư mục lưu trữ dữ liệu chính (data)
func ResolveDataDir() string {
	if envDir := strings.TrimSpace(os.Getenv("DATA_DIR")); envDir != "" {
		return envDir
	}

	if runtime.GOOS == "windows" {
		canonicalParent := `f:\supportflast.dev`
		if info, err := os.Stat(canonicalParent); err == nil && info.IsDir() {
			return filepath.Join(canonicalParent, "data")
		}
	}

	candidates := []string{
		filepath.Join("..", "data"),
		"data",
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}

	if cwd, err := os.Getwd(); err == nil {
		if filepath.Base(cwd) == "supportflast_engine" {
			return filepath.Join("..", "data")
		}
	}

	return "data"
}

// ResolveStorageDir xác định đường dẫn thư mục lưu trữ gói ứng dụng (storage)
func ResolveStorageDir() string {
	if envDir := strings.TrimSpace(os.Getenv("STORAGE_DIR")); envDir != "" {
		return envDir
	}

	if runtime.GOOS == "windows" {
		canonicalParent := `f:\supportflast.dev`
		if info, err := os.Stat(canonicalParent); err == nil && info.IsDir() {
			return filepath.Join(canonicalParent, "storage")
		}
	}

	candidates := []string{
		filepath.Join("..", "storage"),
		"storage",
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}

	if cwd, err := os.Getwd(); err == nil {
		if filepath.Base(cwd) == "supportflast_engine" {
			return filepath.Join("..", "storage")
		}
	}

	return "storage"
}

// Bootstrap khởi động toàn bộ tiến trình tự động khởi tạo cho môi trường thực thi mặc định
func Bootstrap() (*BootstrapResult, error) {
	return BootstrapWithDirs("", "", "")
}

// BootstrapWithDirs khởi động tiến trình tự động khởi tạo với khả năng tùy biến thư mục (hỗ trợ kiểm thử cô lập)
func BootstrapWithDirs(customDataDir, customStorageDir, customEnvDir string) (*BootstrapResult, error) {
	log.Println("[BOOTSTRAP] ========================================================")
	log.Println("[BOOTSTRAP] Khởi động Auto-Bootstrapping SupportFlast Engine")
	log.Println("[BOOTSTRAP] ========================================================")

	res := &BootstrapResult{}

	// 1. Tự động kiểm tra, sinh mới file '.env' nếu chưa có, và nạp biến môi trường
	if customEnvDir != "" {
		os.Setenv("ENV_DIR", customEnvDir)
	}
	if err := config.InitEnv(); err != nil {
		log.Printf("[BOOTSTRAP] [WARN] config.InitEnv gặp cảnh báo: %v", err)
	}
	res.EnvPath = config.GetLoadedEnvPath()

	// 2. Xác định thư mục dữ liệu và lưu trữ
	dataDir := customDataDir
	if dataDir == "" {
		dataDir = ResolveDataDir()
	}
	storageDir := customStorageDir
	if storageDir == "" {
		storageDir = ResolveStorageDir()
	}
	keysDir := filepath.Join(dataDir, "keys")

	res.DataDir = dataDir
	res.StorageDir = storageDir
	res.KeysDir = keysDir

	// Đồng bộ biến môi trường tiến trình cho tất cả các submodules
	os.Setenv("DATA_DIR", dataDir)
	os.Setenv("STORAGE_DIR", storageDir)
	os.Setenv("JWT_KEYS_DIR", keysDir)
	security.SetCustomKeysDir(keysDir)

	// 3. Đảm bảo toàn bộ cấu trúc thư mục bắt buộc tồn tại:
	// 'data', 'data/keys', 'data/backups', 'data/oauth', 'data/service_accounts', 'storage'
	requiredDirs := []struct {
		path string
		perm os.FileMode
		desc string
	}{
		{dataDir, 0755, "Thư mục dữ liệu chính (data)"},
		{keysDir, 0700, "Thư mục lưu trữ khóa bảo mật RSA (data/keys)"},
		{filepath.Join(dataDir, "backups"), 0755, "Thư mục sao lưu cơ sở dữ liệu (data/backups)"},
		{filepath.Join(dataDir, "oauth"), 0755, "Thư mục chứng chỉ OAuth Google Drive (data/oauth)"},
		{filepath.Join(dataDir, "service_accounts"), 0755, "Thư mục Service Accounts (data/service_accounts)"},
		{storageDir, 0755, "Thư mục lưu trữ gói phần mềm (storage)"},
		{filepath.Join(storageDir, "packages"), 0755, "Thư mục gói phần mềm phân phối (storage/packages)"},
	}

	for _, d := range requiredDirs {
		if _, err := os.Stat(d.path); os.IsNotExist(err) {
			res.IsFirstRun = true
		}
		if err := os.MkdirAll(d.path, d.perm); err != nil {
			return nil, fmt.Errorf("không thể khởi tạo thư mục '%s' (%s): %w", d.path, d.desc, err)
		}
		res.CreatedDirs = append(res.CreatedDirs, d.path)
		log.Printf("[BOOTSTRAP] [DIR] Đã xác nhận/khởi tạo: %s -> '%s'", d.desc, d.path)
	}

	// 4. Tự động sinh cặp khóa RSA 2048-bit (private.pem / public.pem) tại 'data/keys/' nếu chưa tồn tại
	privKeyPath := filepath.Join(keysDir, "private.pem")
	pubKeyPath := filepath.Join(keysDir, "public.pem")
	res.PrivateKeyPath = privKeyPath
	res.PublicKeyPath = pubKeyPath

	_, privErr := os.Stat(privKeyPath)
	_, pubErr := os.Stat(pubKeyPath)
	if os.IsNotExist(privErr) || os.IsNotExist(pubErr) {
		res.RSAKeysGenerated = true
		log.Printf("[BOOTSTRAP] [RSA] Chưa có cặp khóa RSA tại '%s'. Đang tự động sinh cặp khóa RSA 2048-bit...", keysDir)
	}

	if err := security.EnsureRSAKeys(); err != nil {
		return nil, fmt.Errorf("lỗi khởi tạo cặp khóa RSA 2048-bit: %w", err)
	}
	log.Printf("[BOOTSTRAP] [RSA] Cặp khóa RSA 2048-bit sẵn sàng tại: '%s'", keysDir)

	// 5. Tự động khởi tạo CSDL SQLite 'data/supportflast.db' & seed tài khoản Admin
	dbPath := filepath.Join(dataDir, "supportflast.db")
	res.MainDBPath = dbPath
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		log.Printf("[BOOTSTRAP] [DATABASE] Chưa có CSDL '%s'. Đang tự động khởi tạo bảng và chỉ mục...", dbPath)
	}

	db, err := database.InitDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("khởi tạo cơ sở dữ liệu SQLite 'supportflast.db' thất bại: %w", err)
	}
	log.Printf("[BOOTSTRAP] [DATABASE] Cơ sở dữ liệu 'supportflast.db' đã khởi tạo và migrate DDL thành công tại '%s'", dbPath)

	// 6. Tự động khởi tạo CSDL SQLite CloudPool 'data/cloudpool_metadata.db'
	storageDBPath := filepath.Join(dataDir, "cloudpool_metadata.db")
	res.CloudPoolDBPath = storageDBPath
	if _, err := os.Stat(storageDBPath); os.IsNotExist(err) {
		log.Printf("[BOOTSTRAP] [STORAGE] Chưa có CSDL CloudPool '%s'. Đang tự động khởi tạo schema...", storageDBPath)
	}

	storageDB, err := cloudpoolStorage.NewDB(storageDBPath)
	if err != nil {
		return nil, fmt.Errorf("khởi tạo cơ sở dữ liệu CloudPool 'cloudpool_metadata.db' thất bại: %w", err)
	}
	res.StorageDB = storageDB
	log.Printf("[BOOTSTRAP] [STORAGE] Cơ sở dữ liệu CloudPool Metadata đã khởi tạo và migrate thành công tại '%s'", storageDBPath)

	// 7. Tự động tạo / xác nhận tài khoản Admin mặc định ('admin' / 'Admin@2026!SupportFlast') với role admin
	res.AdminUsername = "admin"
	registry.InitAuth()

	// Xác thực tài khoản Admin trong SQLite
	var adminUser database.User
	err = db.QueryRow("SELECT id, username, email, password_hash, role FROM users WHERE username = 'admin'").Scan(
		&adminUser.ID, &adminUser.Username, &adminUser.Email, &adminUser.PasswordHash, &adminUser.Role,
	)
	if err != nil {
		// Thử chèn tài khoản admin nếu chưa có
		log.Println("[BOOTSTRAP] [ADMIN] Tài khoản Admin chưa có trong CSDL, tiến hành tạo mới...")
		hash, hashErr := bcrypt.GenerateFromPassword([]byte(database.DefaultAdminPassword), 12)
		if hashErr != nil {
			return nil, fmt.Errorf("lỗi băm mật khẩu admin: %w", hashErr)
		}
		now := config.Get("BOOTSTRAP_TIME", "")
		if now == "" {
			now = database.DefaultAdminDisplayName
		}
		_, insertErr := db.Exec(`
			INSERT OR IGNORE INTO users (id, username, email, password_hash, display_name, role, avatar, created_at, updated_at, last_login)
			VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'), '')
		`, database.DefaultAdminID, database.DefaultAdminUsername, database.DefaultAdminEmail, string(hash), database.DefaultAdminDisplayName, database.DefaultAdminRole, database.DefaultAdminAvatar)
		if insertErr != nil {
			return nil, fmt.Errorf("lỗi khởi tạo tài khoản admin: %w", insertErr)
		}
		res.AdminUserCreated = true
	} else {
		res.AdminUserCreated = true
		// Kiểm tra mật khẩu tài khoản admin
		if bcrypt.CompareHashAndPassword([]byte(adminUser.PasswordHash), []byte(database.DefaultAdminPassword)) == nil {
			log.Println("[BOOTSTRAP] [ADMIN] Tài khoản Quản Trị Viên ('admin' / 'Admin@2026!SupportFlast', role='admin') đã được xác thực an toàn.")
		}
	}

	log.Println("[BOOTSTRAP] ========================================================")
	log.Println("[BOOTSTRAP] Hoàn tất Auto-Bootstrapping. Hệ thống sẵn sàng vận hành 100%!")
	log.Println("[BOOTSTRAP] ========================================================")

	return res, nil
}

// ValidateRSAKeys kiểm tra tính hợp lệ của cặp khóa RSA sinh ra trên đĩa
func ValidateRSAKeys(privPath, pubPath string) error {
	privBytes, err := os.ReadFile(privPath)
	if err != nil {
		return fmt.Errorf("không thể đọc private key: %w", err)
	}

	block, _ := pem.Decode(privBytes)
	if block == nil {
		return fmt.Errorf("private key không phải định dạng PEM hợp lệ")
	}

	privKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		// Thử PKCS8
		pkcs8Key, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err2 != nil {
			return fmt.Errorf("lỗi phân tích private key RSA (PKCS1: %v, PKCS8: %v)", err, err2)
		}
		var ok bool
		privKey, ok = pkcs8Key.(*rsa.PrivateKey)
		if !ok {
			return fmt.Errorf("khóa không phải RSA private key")
		}
	}

	if privKey.N.BitLen() < 2048 {
		return fmt.Errorf("độ dài khóa RSA quá ngắn: %d bit (yêu cầu tối thiểu 2048-bit)", privKey.N.BitLen())
	}

	pubBytes, err := os.ReadFile(pubPath)
	if err != nil {
		return fmt.Errorf("không thể đọc public key: %w", err)
	}

	pubBlock, _ := pem.Decode(pubBytes)
	if pubBlock == nil {
		return fmt.Errorf("public key không phải định dạng PEM hợp lệ")
	}

	parsedPub, err := x509.ParsePKIXPublicKey(pubBlock.Bytes)
	if err != nil {
		return fmt.Errorf("lỗi phân tích public key PKIX: %w", err)
	}

	rsaPub, ok := parsedPub.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("public key không phải kiểu RSA")
	}

	if rsaPub.N.Cmp(privKey.N) != 0 {
		return fmt.Errorf("Public Key và Private Key không khớp modulo N")
	}

	return nil
}
