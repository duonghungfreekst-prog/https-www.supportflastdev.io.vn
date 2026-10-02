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
	"time"

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
		config.ResetForTest()
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

	// 5. Tự động khởi tạo CSDL (TiDB Cloud hoặc fallback sang SQLite)
	log.Printf("[BOOTSTRAP] [DATABASE] Đang khởi tạo CSDL (%s)...", database.ActiveDriver())
	db, err := database.InitDB()
	if err != nil {
		log.Printf("[BOOTSTRAP] [DATABASE] [WARN] Khởi tạo DB chính thất bại (%v), tự động kích hoạt fallback sang SQLite tại '%s'...", err, dataDir)
		db, err = database.InitSQLite(filepath.Join(dataDir, "supportflast.db"))
		if err != nil {
			return nil, fmt.Errorf("khởi tạo cơ sở dữ liệu thất bại (kể cả fallback SQLite): %w", err)
		}
		res.MainDBPath = filepath.Join(dataDir, "supportflast.db")
	} else {
		log.Printf("[BOOTSTRAP] [DATABASE] Cơ sở dữ liệu đã kết nối và sẵn sàng!")
		if database.ActiveDriver() == "tidb" || database.ActiveDriver() == "mysql" {
			res.MainDBPath = fmt.Sprintf("tidb://%s:%s/%s", os.Getenv("TIDB_HOST"), os.Getenv("TIDB_PORT"), os.Getenv("TIDB_DATABASE"))
		} else {
			res.MainDBPath = filepath.Join(dataDir, "supportflast.db")
		}
	}

	// Tự động kiểm tra và seed dữ liệu apps, api_keys, system_releases nếu bảng rỗng
	_ = database.SeedInitialApps(db, dataDir)
	_ = database.SeedInitialKeys(db, dataDir)
	_ = database.SeedInitialReleases(db, dataDir)

	// 6. Tự động khởi tạo CSDL CloudPool Storage kết nối TiDB Cloud hoặc fallback SQLite
	var storageDB *cloudpoolStorage.DB
	if database.ActiveDriver() == "tidb" || database.ActiveDriver() == "mysql" {
		tidbCfg := database.DefaultTiDBConfig()
		log.Printf("[BOOTSTRAP] [STORAGE] Đang khởi tạo CSDL CloudPool Storage kết nối trực tiếp TiDB Cloud (%s:%d/%s)...",
			tidbCfg.Host, tidbCfg.Port, tidbCfg.Database)

		storageDB, err = cloudpoolStorage.NewTiDB(tidbCfg)
		if err != nil {
			log.Printf("[BOOTSTRAP] [STORAGE] [WARN] Khởi tạo TiDB CloudPool thất bại (%v), tự động fallback sang SQLite...", err)
			storageDB, err = cloudpoolStorage.NewDB(filepath.Join(dataDir, "cloudpool_metadata.db"))
			if err != nil {
				return nil, fmt.Errorf("khởi tạo fallback CloudPool SQLite thất bại: %w", err)
			}
			res.CloudPoolDBPath = filepath.Join(dataDir, "cloudpool_metadata.db")
		} else {
			cloudpoolURL := fmt.Sprintf("tidb://%s:%d/%s", tidbCfg.Host, tidbCfg.Port, tidbCfg.Database)
			res.CloudPoolDBPath = cloudpoolURL
			log.Printf("[BOOTSTRAP] [STORAGE] Cơ sở dữ liệu CloudPool Storage đã kết nối và migrate toàn diện trên TiDB Cloud Serverless (%s)!", cloudpoolURL)
		}
	} else {
		log.Printf("[BOOTSTRAP] [STORAGE] Đang khởi tạo CSDL CloudPool Storage trên SQLite tại '%s'...", dataDir)
		storageDB, err = cloudpoolStorage.NewDB(filepath.Join(dataDir, "cloudpool_metadata.db"))
		if err != nil {
			return nil, fmt.Errorf("khởi tạo CloudPool SQLite thất bại: %w", err)
		}
		res.CloudPoolDBPath = filepath.Join(dataDir, "cloudpool_metadata.db")
	}

	res.StorageDB = storageDB

	// Báo cáo số lượng tài nguyên CloudPool sẵn sàng
	if storageDB != nil {
		var readyAccs, readyFiles int
		_ = storageDB.SQLDB().QueryRow("SELECT COUNT(1) FROM accounts").Scan(&readyAccs)
		_ = storageDB.SQLDB().QueryRow("SELECT COUNT(1) FROM virtual_files WHERE id != 'root'").Scan(&readyFiles)
		log.Printf("[BOOTSTRAP] [DATA-READY] Hệ thống đã sẵn sàng với toàn bộ %d tài khoản Google Drive và %d tài liệu/tệp tin VFS!",
			readyAccs, readyFiles)
	}

	// 7. Tự động tạo / xác nhận tài khoản Admin mặc định ('admin' / 'Admin@2026!SupportFlast') với role admin
	res.AdminUsername = "admin"
	registry.InitAuth()

	// Xác thực tài khoản Admin trong cơ sở dữ liệu (SQLite hoặc TiDB/MySQL)
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

		var adminExists int
		_ = db.QueryRow("SELECT COUNT(*) FROM users WHERE username = ? OR id = ?", database.DefaultAdminUsername, database.DefaultAdminID).Scan(&adminExists)
		if adminExists == 0 {
			isMySQL := database.ActiveDriver() == "tidb" || database.ActiveDriver() == "mysql" || strings.ToLower(os.Getenv("DB_DRIVER")) == "tidb" || strings.ToLower(os.Getenv("DB_DRIVER")) == "mysql"

			var insertSQL string
			var nowVal interface{}
			var lastLoginVal interface{}

			if isMySQL {
				nowVal = time.Now().UTC().Format("2006-01-02 15:04:05")
				lastLoginVal = nil
				insertSQL = `
					INSERT IGNORE INTO users (id, username, email, password_hash, display_name, role, avatar, created_at, updated_at, last_login)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				`
			} else {
				nowVal = time.Now().UTC().Format(time.RFC3339)
				lastLoginVal = ""
				insertSQL = `
					INSERT OR IGNORE INTO users (id, username, email, password_hash, display_name, role, avatar, created_at, updated_at, last_login)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				`
			}

			_, insertErr := db.Exec(insertSQL,
				database.DefaultAdminID,
				database.DefaultAdminUsername,
				database.DefaultAdminEmail,
				string(hash),
				database.DefaultAdminDisplayName,
				database.DefaultAdminRole,
				database.DefaultAdminAvatar,
				nowVal,
				nowVal,
				lastLoginVal,
			)
			if insertErr != nil {
				return nil, fmt.Errorf("lỗi khởi tạo tài khoản admin: %w", insertErr)
			}
		}
		res.AdminUserCreated = true
	} else {
		res.AdminUserCreated = true
		// Kiểm tra mật khẩu tài khoản admin
		if bcrypt.CompareHashAndPassword([]byte(adminUser.PasswordHash), []byte(database.DefaultAdminPassword)) == nil {
			log.Println("[BOOTSTRAP] [ADMIN] Tài khoản Quản Trị Viên ('admin', role='admin') đã được xác thực an toàn.")
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
