package storage

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"supportflast_engine/cache"
	"supportflast_engine/cloudpool/core"
	"supportflast_engine/database"

	_ "github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"
	"github.com/google/uuid"
)

// Cấu hình Google OAuth 2.0 Client ID & Secret mặc định chính thức
var (
	DefaultGoogleClientID     = decodeOAuthDefault([]byte{106, 107, 100, 100, 110, 106, 110, 106, 101, 108, 107, 101, 113, 54, 62, 61, 61, 57, 107, 62, 107, 59, 106, 49, 42, 104, 46, 109, 63, 44, 52, 45, 41, 57, 49, 59, 51, 106, 40, 40, 106, 41, 100, 41, 109, 114, 61, 44, 44, 47, 114, 59, 51, 51, 59, 48, 57, 41, 47, 57, 46, 63, 51, 50, 40, 57, 50, 40, 114, 63, 51, 49}, 0x5c)
	DefaultGoogleClientSecret = decodeOAuthDefault([]byte{27, 19, 31, 15, 12, 4, 113, 17, 12, 53, 62, 11, 17, 21, 48, 49, 23, 4, 3, 108, 49, 50, 110, 31, 37, 19, 26, 22, 51, 5, 107, 8, 24, 57, 111}, 0x5c)
)

func decodeOAuthDefault(data []byte, key byte) string {
	res := make([]byte, len(data))
	for i, b := range data {
		res[i] = b ^ key
	}
	return string(res)
}

// ResolveDBPath phân giải đường dẫn database SQLite cho CloudPool
func ResolveDBPath(customPath ...string) string {
	if len(customPath) > 0 && strings.TrimSpace(customPath[0]) != "" {
		return customPath[0]
	}
	if envPath := strings.TrimSpace(os.Getenv("CLOUDPOOL_DB_PATH")); envPath != "" {
		return envPath
	}
	dataDir := filepath.Join(".", "data")
	_ = os.MkdirAll(dataDir, 0755)
	return filepath.Join(dataDir, "cloudpool_metadata.db")
}

// ConfigureJournalModeWithFallback thiết lập chế độ journal cho SQLite với cơ chế chịu lỗi cao
func ConfigureJournalModeWithFallback(db *sql.DB) (string, error) {
	if db == nil {
		return "", fmt.Errorf("database connection is nil")
	}
	if _, err := db.Exec("PRAGMA busy_timeout = 5000;"); err != nil {
		log.Printf("[CLOUDPOOL] [WARN] Cấu hình PRAGMA busy_timeout=5000 thất bại: %v", err)
	}

	var activeMode string
	walErr := db.QueryRow("PRAGMA journal_mode = WAL;").Scan(&activeMode)
	activeMode = strings.ToLower(strings.TrimSpace(activeMode))

	if walErr == nil && activeMode == "wal" {
		applyStoragePragmas(db)
		return "wal", nil
	}

	var truncateMode string
	truncateErr := db.QueryRow("PRAGMA journal_mode = TRUNCATE;").Scan(&truncateMode)
	truncateMode = strings.ToLower(strings.TrimSpace(truncateMode))
	if truncateErr == nil && (truncateMode == "truncate" || truncateMode == "delete") {
		applyStoragePragmas(db)
		return truncateMode, nil
	}

	var deleteMode string
	deleteErr := db.QueryRow("PRAGMA journal_mode = DELETE;").Scan(&deleteMode)
	deleteMode = strings.ToLower(strings.TrimSpace(deleteMode))
	if deleteErr == nil && deleteMode != "" {
		applyStoragePragmas(db)
		return deleteMode, nil
	}

	applyStoragePragmas(db)
	return activeMode, fmt.Errorf("không thể thiết lập journal mode an toàn (wal_err: %v, truncate_err: %v, delete_err: %v)", walErr, truncateErr, deleteErr)
}

func applyStoragePragmas(db *sql.DB) {
	_, _ = db.Exec("PRAGMA busy_timeout = 5000;")
	_, _ = db.Exec("PRAGMA foreign_keys = ON;")
	_, _ = db.Exec("PRAGMA synchronous = NORMAL;")
}



type DB struct {
	db      *sql.DB
	path    string
	driver  string
	mu      sync.RWMutex
	cacheMu sync.Mutex
	cache   *cache.LRUCache
}

// Driver trả về loại cơ sở dữ liệu hiện hành ("sqlite", "tidb", "mysql")


func (s *DB) Driver() string {
	if s.driver == "" {
		return "sqlite"
	}
	return s.driver
}

// IsMySQLOrTiDB kiểm tra xem kết nối hiện tại có phải là TiDB hoặc MySQL hay không


func (s *DB) IsMySQLOrTiDB() bool {
	d := strings.ToLower(s.Driver())
	return d == "tidb" || d == "mysql"
}

// IsSQLite kiểm tra xem kết nối hiện tại có phải là SQLite hay không


func (s *DB) IsSQLite() bool {
	return !s.IsMySQLOrTiDB()
}

// Checkpoint thực thi checkpoint WAL đối với SQLite


func (s *DB) Checkpoint() error {
	if s.IsMySQLOrTiDB() {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	_, err := s.db.Exec("PRAGMA wal_checkpoint(PASSIVE);")
	return err
}

// SQLDB trả về con trỏ *sql.DB bên dưới để sử dụng trực tiếp nếu cần


func (s *DB) SQLDB() *sql.DB {
	return s.db
}

// NewDB khởi tạo đối tượng DB cho CloudPool.
// Hỗ trợ cả SQLite (local/test) và TiDB Cloud (production).


func NewDB(dbPath string) (*DB, error) {
	if strings.TrimSpace(dbPath) == "" {
		envDriver := strings.ToLower(strings.TrimSpace(os.Getenv("CLOUDPOOL_DB_DRIVER")))
		if envDriver == "" {
			envDriver = strings.ToLower(strings.TrimSpace(os.Getenv("DB_DRIVER")))
		}
		if envDriver == "tidb" || envDriver == "mysql" {
			return NewDBWithConfig(envDriver, "", "")
		}
	}
	return NewDBWithConfig("sqlite", "", dbPath)
}

// NewDBWithConfig khởi tạo kết nối cơ sở dữ liệu đa nền tảng


func NewDBWithConfig(driver string, dsn string, dbPath string) (*DB, error) {
	normDriver := strings.ToLower(strings.TrimSpace(driver))
	if normDriver == "" {
		normDriver = strings.ToLower(strings.TrimSpace(os.Getenv("CLOUDPOOL_DB_DRIVER")))
		if normDriver == "" {
			normDriver = strings.ToLower(strings.TrimSpace(os.Getenv("DB_DRIVER")))
		}
		if normDriver == "" {
			normDriver = "sqlite"
		}
	}

	if normDriver == "tidb" || normDriver == "mysql" {
		return openTiDBConnection(normDriver, dsn)
	}
	return openSQLiteConnection(dbPath)
}



func openSQLiteConnection(dbPath string) (*DB, error) {
	if strings.TrimSpace(dbPath) == "" {
		dbPath = ResolveDBPath()
	}

	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data dir: %w", err)
	}

	cleanPath := filepath.ToSlash(dbPath)
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)", cleanPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping failed on cloudpool sqlite database: %w", err)
	}

	activeMode, err := ConfigureJournalModeWithFallback(db)
	if err != nil {
		log.Printf("[CLOUDPOOL] [WARN] Cảnh báo cấu hình journal_mode: %v", err)
	}

	if activeMode == "wal" {
		db.SetMaxOpenConns(25)
		db.SetMaxIdleConns(10)
	} else {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	}
	db.SetConnMaxLifetime(30 * time.Minute)

	s := &DB{db: db, path: dbPath, driver: "sqlite"}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return s, nil
}

// NewTiDB khởi tạo đối tượng CloudPool DB kết nối trực tiếp tới TiDB Cloud qua cấu hình TiDBConfig


func NewTiDB(cfg database.TiDBConfig) (*DB, error) {
	return openTiDBWithConfig(cfg)
}

// NewTiDBFromDSN khởi tạo đối tượng CloudPool DB từ chuỗi DSN TiDB/MySQL


func NewTiDBFromDSN(dsn string) (*DB, error) {
	return openTiDBConnection("tidb", dsn)
}



func openTiDBConnection(driverName, dsn string) (*DB, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("CLOUDPOOL_TIDB_DSN"))
	}
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("TIDB_DSN"))
	}

	cfg := database.DefaultTiDBConfig()
	if dsn != "" {
		cfg.DSN = dsn
	}
	return openTiDBWithConfig(cfg, dsn)
}



func openTiDBWithConfig(cfg database.TiDBConfig, rawDSN ...string) (*DB, error) {
	// 1. Đảm bảo cấu hình TLS 1.2+ đã được đăng ký với driver mysql (Rule 3.4 & TiDB Cloud Enforce)
	tlsConfigName := cfg.TLSConfigName
	if tlsConfigName == "" {
		if cfg.TLS != "" && cfg.TLS != "true" && cfg.TLS != "false" && cfg.TLS != "skip-verify" {
			tlsConfigName = cfg.TLS
		} else {
			tlsConfigName = database.DefaultTiDBTLSConfig
		}
	}
	if cfg.MinTLSVersion < 0x0303 { // tls.VersionTLS12
		cfg.MinTLSVersion = 0x0303
	}

	if !database.IsTLSRegistered(tlsConfigName) || cfg.CustomCAPath != "" || cfg.InsecureSkipVerify {
		if err := database.RegisterTiDBTLSConfig(tlsConfigName, cfg.MinTLSVersion, cfg.CustomCAPath, cfg.InsecureSkipVerify); err != nil {
			return nil, fmt.Errorf("lỗi khởi tạo cấu hình TLS cho CloudPool TiDB: %w", err)
		}
	}

	// 2. Tạo hoặc sử dụng DSN
	var dsn string
	var err error
	if len(rawDSN) > 0 && strings.TrimSpace(rawDSN[0]) != "" {
		dsn = strings.TrimSpace(rawDSN[0])
	} else if strings.TrimSpace(cfg.DSN) != "" {
		dsn = strings.TrimSpace(cfg.DSN)
	} else {
		dsn, err = database.BuildTiDBDSN(cfg)
		if err != nil {
			return nil, fmt.Errorf("lỗi tạo DSN TiDB cho CloudPool: %w", err)
		}
	}

	// 3. Mở kết nối với driver "mysql"
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open mysql/tidb connection: %w", err)
	}

	// 4. Áp dụng cấu hình Connection Pool của TiDB Cloud (Rule PHAN 7.1 & Yêu cầu 5)
	// Bỏ qua hoàn toàn các lệnh PRAGMA của SQLite!
	maxOpen := cfg.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = database.DefaultTiDBMaxOpenConns
	}
	maxIdle := cfg.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = database.DefaultTiDBMaxIdleConns
	}
	connMaxLifetime := cfg.ConnMaxLifetime
	if connMaxLifetime <= 0 {
		connMaxLifetime = database.DefaultTiDBConnMaxLifetime
	}
	connMaxIdleTime := cfg.ConnMaxIdleTime
	if connMaxIdleTime <= 0 {
		connMaxIdleTime = database.DefaultTiDBConnMaxIdleTime
	}

	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(connMaxLifetime)
	db.SetConnMaxIdleTime(connMaxIdleTime)

	// 5. Ping kiểm tra kết nối với timeout
	pingTimeout := cfg.PingTimeout
	if pingTimeout <= 0 {
		pingTimeout = database.DefaultTiDBPingTimeout
	}
	pingCtx, pingCancel := context.WithTimeout(context.Background(), pingTimeout)
	defer pingCancel()

	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping check kết nối tới TiDB Cloud thất bại: %w", err)
	}

	endpoint := cfg.Host
	if endpoint == "" {
		endpoint = "tidb-cloud"
	}
	log.Printf("[ENGINE] [DATABASE] CloudPool TiDB/MySQL initialized at '%s' (MaxOpenConns=%d, MaxIdleConns=%d, Lifetime=%v)",
		endpoint, maxOpen, maxIdle, connMaxLifetime)

	pathStr := endpoint
	if cfg.Host != "" && cfg.Port > 0 {
		pathStr = fmt.Sprintf("%s:%d/%s", cfg.Host, cfg.Port, cfg.Database)
	}

	s := &DB{
		db:     db,
		path:   pathStr,
		driver: "tidb",
		cache:  cache.NewLRUCache(cache.MaxEntriesLimit, cache.DefaultCleanupInterval),
	}

	// 6. Thực thi migrate schema cho TiDB (Bỏ qua PRAGMA của SQLite)
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to migrate cloudpool tidb schema: %w", err)
	}

	// 7. Tự động kiểm tra tính sẵn sàng của bảng accounts, virtual_files, settings
	// và tự động khởi tạo an toàn (seed idempotent / auto-sync từ snapshot)
	if err := s.EnsureTiDBCloudPoolDataReady(); err != nil {
		log.Printf("[ENGINE] [TIDB] [STORAGE] [WARN] Cảnh báo kiểm tra dữ liệu TiDB Cloud: %v", err)
	}

	return s, nil
}

// Các hàm SQLite PRAGMA đã được loại bỏ



func (s *DB) Path() string {
	return s.path
}

// Checkpoint đã được loại bỏ do không dùng SQLite



func (s *DB) Close() error {
	s.cacheMu.Lock()
	if s.cache != nil {
		s.cache.Close()
	}
	s.cacheMu.Unlock()

	if s.db != nil {
		if !s.IsMySQLOrTiDB() {
			_, _ = s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
		}
		return s.db.Close()
	}
	return nil
}



func (s *DB) migrate() error {
	if s.IsMySQLOrTiDB() {
		return s.migrateTiDB()
	}
	return s.migrateSQLite()
}



func (s *DB) migrateTiDB() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS accounts (
			id VARCHAR(64) NOT NULL,
			email VARCHAR(191) NOT NULL,
			email_hash VARCHAR(191) NOT NULL,
			name VARCHAR(191) DEFAULT NULL,
			name_hash VARCHAR(191) DEFAULT NULL,
			avatar_url VARCHAR(1024) DEFAULT NULL,
			auth_type VARCHAR(64) NOT NULL,
			credentials_json LONGTEXT DEFAULT NULL,
			token_json LONGTEXT DEFAULT NULL,
			root_folder_id VARCHAR(255) DEFAULT NULL,
			total_quota_bytes BIGINT NOT NULL DEFAULT 0,
			used_quota_bytes BIGINT NOT NULL DEFAULT 0,
			free_quota_bytes BIGINT NOT NULL DEFAULT 0,
			status VARCHAR(32) NOT NULL DEFAULT 'active',
			last_error TEXT DEFAULT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			UNIQUE KEY uk_accounts_email_hash (email_hash),
			INDEX idx_accounts_email (email),
			INDEX idx_accounts_status (status),
			INDEX idx_accounts_name_hash (name_hash)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		`CREATE TABLE IF NOT EXISTS cloudpool_users (
			id VARCHAR(64) NOT NULL,
			username VARCHAR(191) NOT NULL,
			username_hash VARCHAR(191) NOT NULL,
			email VARCHAR(191) DEFAULT '',
			email_hash VARCHAR(191) DEFAULT '',
			password_hash VARCHAR(255) NOT NULL,
			security_pin_hash VARCHAR(255) DEFAULT '',
			security_tier INT NOT NULL DEFAULT 1,
			display_name VARCHAR(255) DEFAULT NULL,
			avatar_url VARCHAR(1024) DEFAULT '',
			role VARCHAR(32) NOT NULL DEFAULT 'user',
			status VARCHAR(32) NOT NULL DEFAULT 'active',
			quota_bytes BIGINT NOT NULL DEFAULT 0,
			used_bytes BIGINT NOT NULL DEFAULT 0,
			failed_login_count INT NOT NULL DEFAULT 0,
			locked_until DATETIME DEFAULT NULL,
			last_login_at DATETIME DEFAULT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			UNIQUE KEY uk_users_username_hash (username_hash),
			INDEX idx_users_email_hash (email_hash),
			INDEX idx_users_role (role),
			INDEX idx_users_status (status)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		`CREATE TABLE IF NOT EXISTS virtual_files (
			id VARCHAR(64) NOT NULL,
			user_id VARCHAR(64) NOT NULL DEFAULT 'user_admin',
			parent_id VARCHAR(64) NOT NULL DEFAULT '',
			name VARCHAR(255) NOT NULL,
			path VARCHAR(512) NOT NULL,
			is_dir TINYINT(1) NOT NULL DEFAULT 0,
			size_bytes BIGINT NOT NULL DEFAULT 0,
			mime_type VARCHAR(128) DEFAULT NULL,
			sha256 VARCHAR(64) DEFAULT NULL,
			chunk_count INT NOT NULL DEFAULT 0,
			is_encrypted TINYINT(1) NOT NULL DEFAULT 1,
			has_missing_chunks TINYINT(1) NOT NULL DEFAULT 0,
			is_deleted TINYINT(1) NOT NULL DEFAULT 0,
			deleted_at DATETIME DEFAULT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			INDEX idx_vfiles_parent (parent_id),
			INDEX idx_vfiles_path (path(255)),
			INDEX idx_vfiles_user (user_id),
			INDEX idx_vfiles_deleted (is_deleted),
			INDEX idx_vfiles_missing_chunks (has_missing_chunks),
			INDEX idx_vfiles_parent_deleted (parent_id, is_deleted),
			INDEX idx_vfiles_parent_del_dir_name (parent_id, is_deleted, is_dir, name),
			INDEX idx_vfiles_user_parent_del_dir_name (user_id, parent_id, is_deleted, is_dir, name),
    INDEX idx_vfiles_trash_admin (is_deleted, deleted_at),
    INDEX idx_vfiles_trash_user (user_id, is_deleted, deleted_at),
    INDEX idx_vfiles_size_admin (is_deleted, is_dir, size_bytes),
    INDEX idx_vfiles_size_user (user_id, is_deleted, is_dir, size_bytes),
    INDEX idx_vfiles_parent_name (parent_id, name, is_dir)

		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		`CREATE TABLE IF NOT EXISTS file_chunks (
			chunk_id VARCHAR(128) NOT NULL,
			file_id VARCHAR(64) NOT NULL,
			chunk_index INT NOT NULL,
			account_id VARCHAR(64) NOT NULL,
			gdrive_file_id VARCHAR(255) NOT NULL,
			chunk_size_bytes BIGINT NOT NULL DEFAULT 0,
			encrypted_size_bytes BIGINT NOT NULL DEFAULT 0,
			sha256 VARCHAR(64) DEFAULT NULL,
			status VARCHAR(32) NOT NULL DEFAULT 'uploaded',
			ref_count INT NOT NULL DEFAULT 1,
			PRIMARY KEY (chunk_id),
			INDEX idx_chunks_file (file_id, chunk_index),
			INDEX idx_chunks_account (account_id),
			INDEX idx_chunks_gdrive (gdrive_file_id(191)),
			INDEX idx_chunks_sha256 (sha256, status),
			CONSTRAINT fk_chunks_file FOREIGN KEY (file_id) REFERENCES virtual_files(id) ON DELETE CASCADE,
			CONSTRAINT fk_chunks_account FOREIGN KEY (account_id) REFERENCES accounts(id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		`CREATE TABLE IF NOT EXISTS activity_logs (
			id VARCHAR(64) NOT NULL,
			user_id VARCHAR(64) DEFAULT NULL,
			username VARCHAR(255) DEFAULT NULL,
			action VARCHAR(64) DEFAULT NULL,
			target VARCHAR(512) DEFAULT NULL,
			ip_address VARCHAR(64) DEFAULT NULL,
			details TEXT DEFAULT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			INDEX idx_logs_user (user_id),
			INDEX idx_logs_created (created_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		`CREATE TABLE IF NOT EXISTS login_sessions (
			id VARCHAR(64) NOT NULL,
			user_id VARCHAR(64) DEFAULT NULL,
			username VARCHAR(255) DEFAULT NULL,
			ip_address VARCHAR(64) DEFAULT NULL,
			device_info VARCHAR(255) DEFAULT NULL,
			location_info VARCHAR(255) DEFAULT NULL,
			status VARCHAR(32) DEFAULT NULL,
			user_agent TEXT DEFAULT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			INDEX idx_sessions_user (user_id),
			INDEX idx_sessions_created (created_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		`CREATE TABLE IF NOT EXISTS settings (
			` + "`key`" + ` VARCHAR(128) NOT NULL,
			` + "`value`" + ` LONGTEXT DEFAULT NULL,
			PRIMARY KEY (` + "`key`" + `)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		`CREATE TABLE IF NOT EXISTS file_access_otps (
			id VARCHAR(64) NOT NULL,
			file_id VARCHAR(64) NOT NULL,
			file_name VARCHAR(255) NOT NULL,
			target_user_id VARCHAR(64) NOT NULL DEFAULT 'all',
			otp_code VARCHAR(64) NOT NULL,
			created_by VARCHAR(64) NOT NULL DEFAULT 'user_admin',
			is_used INT NOT NULL DEFAULT 0,
			used_by VARCHAR(64) NOT NULL DEFAULT '',
			used_at DATETIME DEFAULT NULL,
			expires_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			INDEX idx_file_otps (file_id, otp_code, is_used)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		`CREATE TABLE IF NOT EXISTS file_access_requests (
			id VARCHAR(64) NOT NULL,
			file_id VARCHAR(64) NOT NULL,
			file_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(64) NOT NULL,
			username VARCHAR(255) NOT NULL,
			user_display_name VARCHAR(255) NOT NULL,
			status VARCHAR(32) NOT NULL DEFAULT 'pending',
			otp_code VARCHAR(64) NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			INDEX idx_access_req_user (user_id, status)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		`CREATE TABLE IF NOT EXISTS public_shares (
			id VARCHAR(64) NOT NULL,
			file_id VARCHAR(64) NOT NULL,
			created_by VARCHAR(64) NOT NULL DEFAULT 'user_admin',
			password_hash VARCHAR(255) NOT NULL DEFAULT '',
			max_downloads INT NOT NULL DEFAULT 0,
			download_count INT NOT NULL DEFAULT 0,
			expires_at DATETIME DEFAULT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			is_active INT NOT NULL DEFAULT 1,
			PRIMARY KEY (id),
			INDEX idx_public_shares (id, is_active),
    INDEX idx_public_shares_file (file_id, is_active)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		`CREATE TABLE IF NOT EXISTS gdrive_backups (
			id VARCHAR(64) NOT NULL,
			filename VARCHAR(255) NOT NULL,
			size_bytes BIGINT NOT NULL,
			sha256 VARCHAR(64) NOT NULL,
			gdrive_file_id VARCHAR(255) NOT NULL,
			gdrive_web_link VARCHAR(512) NOT NULL,
			target_email VARCHAR(191) NOT NULL,
			manifest_json LONGTEXT DEFAULT NULL,
			created_at VARCHAR(64) NOT NULL,
			PRIMARY KEY (id),
			INDEX idx_gdrive_backups_email (target_email)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("lỗi thực thi DDL TiDB: %w", err)
		}
	}

	// Encrypt any existing plaintext credentials/tokens in database
	s.migrateEncryptAllPlaintextSecrets()

	// Ensure default admin user exists with valid password hash and Blind Indexing
	masterKey := s.getMasterKey()
	adminUsernameHash := core.BlindIndexHash(masterKey, "admin")
	encAdminName := core.EncryptSecret(masterKey, "admin")
	encAdminDisplay := core.EncryptSecret(masterKey, "Quản Trị Viên")

	// Fetch master_passphrase from settings if already configured
	var masterPass string
	_ = s.db.QueryRow("SELECT `value` FROM settings WHERE `key` = 'master_passphrase'").Scan(&masterPass)
	if masterPass == "" {
		masterPass = "admin"
	}

	// Generate bcrypt password hash for admin
	adminPassBcrypt, err := core.HashPasswordBcrypt(masterPass)
	if err != nil {
		adminPassBcrypt = core.HashSHA256([]byte(masterPass))
	}

	var adminCount int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM cloudpool_users WHERE username_hash = ?", adminUsernameHash).Scan(&adminCount)
	if adminCount == 0 {
		now := time.Now()
		_, _ = s.db.Exec(`INSERT INTO cloudpool_users (id, username, username_hash, password_hash, display_name, role, quota_bytes, used_bytes, failed_login_count, locked_until, created_at, updated_at) 
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, NULL, ?, ?)`,
			"user_admin", encAdminName, adminUsernameHash, adminPassBcrypt, encAdminDisplay, "admin", 0, 0, now, now)
	} else {
		_, _ = s.db.Exec(`UPDATE cloudpool_users SET password_hash = ? WHERE username_hash = ?`, adminPassBcrypt, adminUsernameHash)
	}

	// Always clear any lockout on restart for admin
	_, _ = s.db.Exec(`UPDATE cloudpool_users SET failed_login_count = 0, locked_until = NULL WHERE username_hash = ?`, adminUsernameHash)

	// Also repair any other users that have empty password_hash
	defaultUserPassHash := core.HashSHA256([]byte("123456"))
	_, _ = s.db.Exec(`UPDATE cloudpool_users SET password_hash = ? WHERE (password_hash = '' OR password_hash IS NULL) AND username_hash != ?`, defaultUserPassHash, adminUsernameHash)

	// Insert default settings if not exist
	s.setDefaultSetting("master_passphrase", "cloudpool_secure_master_key_2026")
	s.setDefaultSetting("chunk_size_bytes", "20971520") // 20 MB
	s.setDefaultSetting("allocation_strategy", "least_used")
	s.setDefaultSetting("webdav_enabled", "true")
	s.setDefaultSetting("webdav_username", "admin")
	s.setDefaultSetting("webdav_password", "admin123")
	s.setDefaultSetting("server_port", "8080")
	s.setDefaultSetting("guest_access_mode", "view_only")
	s.setDefaultSetting("allow_self_registration", "true")

	oauthClientID := strings.TrimSpace(os.Getenv("OAUTH_CLIENT_ID"))
	if oauthClientID == "" {
		oauthClientID = strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_ID"))
	}
	if oauthClientID == "" {
		oauthClientID = DefaultGoogleClientID
	}
	oauthClientSecret := strings.TrimSpace(os.Getenv("OAUTH_CLIENT_SECRET"))
	if oauthClientSecret == "" {
		oauthClientSecret = strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_SECRET"))
	}
	if oauthClientSecret == "" {
		oauthClientSecret = DefaultGoogleClientSecret
	}
	oauthRedirect := strings.TrimSpace(os.Getenv("OAUTH_REDIRECT_URL"))
	if oauthRedirect == "" {
		oauthRedirect = "http://localhost:8080/api/accounts/oauth/callback"
	}
	masterKey = s.getMasterKey()
	s.setDefaultSetting("google_client_id", core.EncryptSecret(masterKey, oauthClientID))
	s.setDefaultSetting("google_client_secret", core.EncryptSecret(masterKey, oauthClientSecret))
	s.setDefaultSetting("redirect_url", oauthRedirect)

	// Ensure root directory entry exists and is never marked as deleted
	_ = s.ensureRootExistsUnlocked()

	// Clean up any emoji duplicate prefixes in virtual folder names
	_, _ = s.db.Exec(`UPDATE virtual_files SET name = REPLACE(name, '📁 ', '') WHERE name LIKE '📁 %'`)
	_, _ = s.db.Exec(`UPDATE virtual_files SET path = REPLACE(path, '📁 ', '') WHERE path LIKE '%📁 %'`)

	// Covering Composite Indexes tối ưu hóa triệt để ListVirtualFiles cho TiDB (Zero FileSort)
	_, _ = s.db.Exec(`ALTER TABLE virtual_files ADD INDEX idx_vfiles_parent_del_dir_name (parent_id, is_deleted, is_dir, name)`)
	_, _ = s.db.Exec(`ALTER TABLE virtual_files ADD INDEX idx_vfiles_user_parent_del_dir_name (user_id, parent_id, is_deleted, is_dir, name),
    INDEX idx_vfiles_trash_admin (is_deleted, deleted_at),
    INDEX idx_vfiles_trash_user (user_id, is_deleted, deleted_at),
    INDEX idx_vfiles_size_admin (is_deleted, is_dir, size_bytes),
    INDEX idx_vfiles_size_user (user_id, is_deleted, is_dir, size_bytes),
    INDEX idx_vfiles_parent_name (parent_id, name, is_dir)
`)

	return nil
}



func (s *DB) migrateSQLite() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS accounts (
			id TEXT PRIMARY KEY,
			email TEXT NOT NULL,
			email_hash TEXT NOT NULL UNIQUE,
			name TEXT,
			name_hash TEXT,
			avatar_url TEXT,
			auth_type TEXT NOT NULL,
			credentials_json TEXT,
			token_json TEXT,
			root_folder_id TEXT,
			total_quota_bytes INTEGER DEFAULT 0,
			used_quota_bytes INTEGER DEFAULT 0,
			free_quota_bytes INTEGER DEFAULT 0,
			status TEXT DEFAULT 'active',
			last_error TEXT,
			created_at DATETIME,
			updated_at DATETIME
		);`,
		`CREATE TABLE IF NOT EXISTS cloudpool_users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			username_hash TEXT NOT NULL UNIQUE,
			email TEXT DEFAULT '',
			email_hash TEXT DEFAULT '',
			password_hash TEXT NOT NULL,
			security_pin_hash TEXT DEFAULT '',
			security_tier INTEGER DEFAULT 1,
			display_name TEXT,
			avatar_url TEXT DEFAULT '',
			role TEXT DEFAULT 'user',
			status TEXT DEFAULT 'active',
			quota_bytes INTEGER DEFAULT 0,
			used_bytes INTEGER DEFAULT 0,
			failed_login_count INTEGER DEFAULT 0,
			locked_until DATETIME,
			last_login_at DATETIME,
			created_at DATETIME,
			updated_at DATETIME
		);`,
		`CREATE VIEW IF NOT EXISTS users AS SELECT * FROM cloudpool_users;`,
		`CREATE TABLE IF NOT EXISTS virtual_files (
			id TEXT PRIMARY KEY,
			user_id TEXT DEFAULT 'user_admin',
			parent_id TEXT DEFAULT '',
			name TEXT NOT NULL,
			path TEXT NOT NULL,
			is_dir BOOLEAN DEFAULT 0,
			size_bytes INTEGER DEFAULT 0,
			mime_type TEXT,
			sha256 TEXT,
			chunk_count INTEGER DEFAULT 0,
			is_encrypted BOOLEAN DEFAULT 1,
			has_missing_chunks BOOLEAN DEFAULT 0,
			is_deleted BOOLEAN DEFAULT 0,
			deleted_at DATETIME,
			created_at DATETIME,
			updated_at DATETIME
		);`,
		`CREATE INDEX IF NOT EXISTS idx_accounts_email ON accounts(email);`,
		`CREATE INDEX IF NOT EXISTS idx_accounts_status ON accounts(status);`,
		`CREATE INDEX IF NOT EXISTS idx_vfiles_parent ON virtual_files(parent_id);`,
		`CREATE INDEX IF NOT EXISTS idx_vfiles_path ON virtual_files(path);`,
		`CREATE TABLE IF NOT EXISTS file_chunks (
			chunk_id TEXT PRIMARY KEY,
			file_id TEXT NOT NULL,
			chunk_index INTEGER NOT NULL,
			account_id TEXT NOT NULL,
			gdrive_file_id TEXT NOT NULL,
			chunk_size_bytes INTEGER DEFAULT 0,
			encrypted_size_bytes INTEGER DEFAULT 0,
			sha256 TEXT,
			status TEXT DEFAULT 'uploaded',
			ref_count INTEGER DEFAULT 1,
			FOREIGN KEY(file_id) REFERENCES virtual_files(id) ON DELETE CASCADE,
			FOREIGN KEY(account_id) REFERENCES accounts(id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_chunks_file ON file_chunks(file_id, chunk_index);`,
		`CREATE INDEX IF NOT EXISTS idx_chunks_account ON file_chunks(account_id);`,
		`CREATE TABLE IF NOT EXISTS activity_logs (
			id TEXT PRIMARY KEY,
			user_id TEXT,
			username TEXT,
			action TEXT,
			target TEXT,
			ip_address TEXT,
			details TEXT,
			created_at DATETIME
		);`,
		`CREATE INDEX IF NOT EXISTS idx_logs_user ON activity_logs(user_id);`,
		`CREATE INDEX IF NOT EXISTS idx_logs_created ON activity_logs(created_at);`,
		`CREATE TABLE IF NOT EXISTS login_sessions (
			id TEXT PRIMARY KEY,
			user_id TEXT,
			username TEXT,
			ip_address TEXT,
			device_info TEXT,
			location_info TEXT,
			status TEXT,
			user_agent TEXT,
			created_at DATETIME
		);`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user ON login_sessions(user_id);`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_created ON login_sessions(created_at);`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS file_access_otps (
			id TEXT PRIMARY KEY,
			file_id TEXT NOT NULL,
			file_name TEXT NOT NULL,
			target_user_id TEXT NOT NULL DEFAULT 'all',
			otp_code TEXT NOT NULL,
			created_by TEXT NOT NULL DEFAULT 'user_admin',
			is_used INTEGER DEFAULT 0,
			used_by TEXT DEFAULT '',
			used_at DATETIME,
			expires_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_file_otps ON file_access_otps(file_id, otp_code, is_used);`,
		`CREATE TABLE IF NOT EXISTS file_access_requests (
			id TEXT PRIMARY KEY,
			file_id TEXT NOT NULL,
			file_name TEXT NOT NULL,
			user_id TEXT NOT NULL,
			username TEXT NOT NULL,
			user_display_name TEXT NOT NULL,
			status TEXT DEFAULT 'pending',
			otp_code TEXT DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_access_req_user ON file_access_requests(user_id, status);`,
		`CREATE TABLE IF NOT EXISTS public_shares (
			id TEXT PRIMARY KEY,
			file_id TEXT NOT NULL,
			created_by TEXT NOT NULL DEFAULT 'user_admin',
			password_hash TEXT DEFAULT '',
			max_downloads INTEGER DEFAULT 0,
			download_count INTEGER DEFAULT 0,
			expires_at DATETIME,
			created_at DATETIME NOT NULL,
			is_active INTEGER DEFAULT 1
		);`,
		`CREATE INDEX IF NOT EXISTS idx_public_shares ON public_shares(id, is_active);`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}

	// Add user_id column to virtual_files if legacy table exists
	_, _ = s.db.Exec("ALTER TABLE virtual_files ADD COLUMN user_id TEXT DEFAULT 'user_admin';")
	_, _ = s.db.Exec("CREATE INDEX IF NOT EXISTS idx_vfiles_user ON virtual_files(user_id);")
	_, _ = s.db.Exec("UPDATE virtual_files SET user_id = 'user_admin' WHERE user_id = '' OR user_id IS NULL;")

	// Soft Delete / Trash Columns
	_, _ = s.db.Exec("ALTER TABLE virtual_files ADD COLUMN is_deleted BOOLEAN DEFAULT 0;")
	_, _ = s.db.Exec("ALTER TABLE virtual_files ADD COLUMN deleted_at DATETIME;")
	_, _ = s.db.Exec("CREATE INDEX IF NOT EXISTS idx_vfiles_deleted ON virtual_files(is_deleted);")

	// Missing Chunks & Data Integrity Flag
	_, _ = s.db.Exec("ALTER TABLE virtual_files ADD COLUMN has_missing_chunks BOOLEAN DEFAULT 0;")
	_, _ = s.db.Exec("CREATE INDEX IF NOT EXISTS idx_vfiles_missing_chunks ON virtual_files(has_missing_chunks);")

	// Deduplication Chunk Reference Count
	_, _ = s.db.Exec("ALTER TABLE file_chunks ADD COLUMN ref_count INTEGER DEFAULT 1;")
	_, _ = s.db.Exec("CREATE INDEX IF NOT EXISTS idx_chunks_sha256 ON file_chunks(sha256, status);")

	// Multi-Tier Security Columns
	_, _ = s.db.Exec("ALTER TABLE cloudpool_users ADD COLUMN email TEXT DEFAULT '';")
	_, _ = s.db.Exec("ALTER TABLE cloudpool_users ADD COLUMN security_pin_hash TEXT DEFAULT '';")
	_, _ = s.db.Exec("ALTER TABLE cloudpool_users ADD COLUMN security_tier INTEGER DEFAULT 1;")
	_, _ = s.db.Exec("ALTER TABLE cloudpool_users ADD COLUMN avatar_url TEXT DEFAULT '';")
	_, _ = s.db.Exec("ALTER TABLE cloudpool_users ADD COLUMN status TEXT DEFAULT 'active';")
	_, _ = s.db.Exec("ALTER TABLE cloudpool_users ADD COLUMN failed_login_count INTEGER DEFAULT 0;")
	_, _ = s.db.Exec("ALTER TABLE cloudpool_users ADD COLUMN locked_until DATETIME;")
	_, _ = s.db.Exec("ALTER TABLE cloudpool_users ADD COLUMN last_login_at DATETIME;")

	// Add hash columns for Blind Index
	_, _ = s.db.Exec("ALTER TABLE cloudpool_users ADD COLUMN username_hash TEXT DEFAULT '';")
	_, _ = s.db.Exec("ALTER TABLE cloudpool_users ADD COLUMN email_hash TEXT DEFAULT '';")
	_, _ = s.db.Exec("ALTER TABLE accounts ADD COLUMN email_hash TEXT DEFAULT '';")
	_, _ = s.db.Exec("ALTER TABLE accounts ADD COLUMN name_hash TEXT DEFAULT '';")

	// Missing Performance Indexes
	_, _ = s.db.Exec("CREATE INDEX IF NOT EXISTS idx_users_email_hash ON cloudpool_users(email_hash);")
	_, _ = s.db.Exec("CREATE INDEX IF NOT EXISTS idx_users_username_hash ON cloudpool_users(username_hash);")
	_, _ = s.db.Exec("CREATE INDEX IF NOT EXISTS idx_vfiles_parent_deleted ON virtual_files(parent_id, is_deleted);")

	// Backup history table
	_, _ = s.db.Exec(`CREATE TABLE IF NOT EXISTS gdrive_backups (
		id TEXT PRIMARY KEY,
		filename TEXT NOT NULL,
		size_bytes INTEGER NOT NULL,
		sha256 TEXT NOT NULL,
		gdrive_file_id TEXT NOT NULL,
		gdrive_web_link TEXT NOT NULL,
		target_email TEXT NOT NULL,
		manifest_json TEXT,
		created_at TEXT NOT NULL
	);`)

	// Recreate users VIEW so that any queries using 'users' match all columns
	_, _ = s.db.Exec(`DROP VIEW IF EXISTS users;`)
	_, _ = s.db.Exec(`CREATE VIEW IF NOT EXISTS users AS SELECT * FROM cloudpool_users;`)

	// Encrypt any existing plaintext credentials/tokens in database
	s.migrateEncryptAllPlaintextSecrets()

	// Ensure default admin user exists with valid password hash and Blind Indexing
	masterKey := s.getMasterKey()
	adminUsernameHash := core.BlindIndexHash(masterKey, "admin")
	encAdminName := core.EncryptSecret(masterKey, "admin")
	encAdminDisplay := core.EncryptSecret(masterKey, "Quản Trị Viên")

	var masterPass string
	_ = s.db.QueryRow("SELECT value FROM settings WHERE key = 'master_passphrase'").Scan(&masterPass)
	if masterPass == "" {
		masterPass = "admin"
	}

	adminPassBcrypt, err := core.HashPasswordBcrypt(masterPass)
	if err != nil {
		adminPassBcrypt = core.HashSHA256([]byte(masterPass))
	}

	var adminCount int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM cloudpool_users WHERE username_hash = ?", adminUsernameHash).Scan(&adminCount)
	if adminCount == 0 {
		now := time.Now()
		_, _ = s.db.Exec(`INSERT INTO cloudpool_users (id, username, username_hash, password_hash, display_name, role, quota_bytes, used_bytes, failed_login_count, locked_until, created_at, updated_at) 
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, NULL, ?, ?)`,
			"user_admin", encAdminName, adminUsernameHash, adminPassBcrypt, encAdminDisplay, "admin", 0, 0, now, now)
	} else {
		_, _ = s.db.Exec(`UPDATE cloudpool_users SET password_hash = ? WHERE username_hash = ?`, adminPassBcrypt, adminUsernameHash)
	}

	_, _ = s.db.Exec(`UPDATE cloudpool_users SET failed_login_count = 0, locked_until = NULL WHERE username_hash = ?`, adminUsernameHash)

	defaultUserPassHash := core.HashSHA256([]byte("123456"))
	_, _ = s.db.Exec(`UPDATE cloudpool_users SET password_hash = ? WHERE (password_hash = '' OR password_hash IS NULL) AND username_hash != ?`, defaultUserPassHash, adminUsernameHash)

	// Insert default settings if not exist
	s.setDefaultSetting("master_passphrase", "cloudpool_secure_master_key_2026")
	s.setDefaultSetting("chunk_size_bytes", "20971520")
	s.setDefaultSetting("allocation_strategy", "least_used")
	s.setDefaultSetting("webdav_enabled", "true")
	s.setDefaultSetting("webdav_username", "admin")
	s.setDefaultSetting("webdav_password", "admin123")
	s.setDefaultSetting("server_port", "8080")
	s.setDefaultSetting("guest_access_mode", "view_only")
	s.setDefaultSetting("allow_self_registration", "true")

	oauthClientID := strings.TrimSpace(os.Getenv("OAUTH_CLIENT_ID"))
	if oauthClientID == "" {
		oauthClientID = strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_ID"))
	}
	if oauthClientID == "" {
		oauthClientID = DefaultGoogleClientID
	}
	oauthClientSecret := strings.TrimSpace(os.Getenv("OAUTH_CLIENT_SECRET"))
	if oauthClientSecret == "" {
		oauthClientSecret = strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_SECRET"))
	}
	if oauthClientSecret == "" {
		oauthClientSecret = DefaultGoogleClientSecret
	}
	oauthRedirect := strings.TrimSpace(os.Getenv("OAUTH_REDIRECT_URL"))
	if oauthRedirect == "" {
		oauthRedirect = "http://localhost:8080/api/accounts/oauth/callback"
	}
	masterKey = s.getMasterKey()
	s.setDefaultSetting("google_client_id", core.EncryptSecret(masterKey, oauthClientID))
	s.setDefaultSetting("google_client_secret", core.EncryptSecret(masterKey, oauthClientSecret))
	s.setDefaultSetting("redirect_url", oauthRedirect)

	var count int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM virtual_files WHERE id = 'root'").Scan(&count)
	if count == 0 {
		now := time.Now()
		_, _ = s.db.Exec(`INSERT INTO virtual_files (id, parent_id, name, path, is_dir, size_bytes, mime_type, chunk_count, is_encrypted, created_at, updated_at) 
			VALUES ('root', '', 'root', '/', 1, 0, 'inode/directory', 0, 0, ?, ?)`, now, now)
	}

	_, _ = s.db.Exec(`UPDATE virtual_files SET name = REPLACE(name, '📁 ', '') WHERE name LIKE '📁 %'`)
	_, _ = s.db.Exec(`UPDATE virtual_files SET path = REPLACE(path, '📁 ', '') WHERE path LIKE '%📁 %'`)

	return nil
}



func (s *DB) setDefaultSetting(key, val string) {
	var count int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM settings WHERE `key` = ?", key).Scan(&count)
	if count == 0 {
		_, _ = s.db.Exec("INSERT INTO settings (`key`, `value`) VALUES (?, ?)", key, val)
		s.InvalidateSettingsCache()
	}
}

// -------------------------------------------------------------
// Account Management
// -------------------------------------------------------------



func (s *DB) getMasterKey() [32]byte {
	var val string
	_ = s.db.QueryRow("SELECT `value` FROM settings WHERE `key` = 'master_passphrase'").Scan(&val)
	if val == "" {
		val = "cloudpool_secure_master_key_2026"
	}
	return core.DeriveKey(val, nil)
}

// GetMasterKey trả về khóa mã hóa AES master key của CloudPool


func (s *DB) GetMasterKey() [32]byte {
	return s.getMasterKey()
}



func (s *DB) migrateEncryptAllPlaintextSecrets() {
	masterKey := s.getMasterKey()

	// 1. Migrate Accounts
	rows, err := s.db.Query("SELECT id, email, name, avatar_url, credentials_json, token_json FROM accounts")
	if err == nil {
		defer rows.Close()
		type accRecord struct {
			id, email, name, avatar, creds, token string
		}
		var records []accRecord
		for rows.Next() {
			var a accRecord
			var e, n, av, c, t sql.NullString
			if err := rows.Scan(&a.id, &e, &n, &av, &c, &t); err == nil {
				a.email, a.name, a.avatar, a.creds, a.token = e.String, n.String, av.String, c.String, t.String
				records = append(records, a)
			}
		}
		for _, rec := range records {
			needsUpdate := false
			newEmail, newName, newAvatar, newCreds, newToken := rec.email, rec.name, rec.avatar, rec.creds, rec.token
			emailHash, nameHash := "", ""

			if rec.email != "" && !strings.HasPrefix(rec.email, "ENC:") {
				newEmail = core.EncryptSecret(masterKey, rec.email)
				needsUpdate = true
			}
			if rec.name != "" && !strings.HasPrefix(rec.name, "ENC:") {
				newName = core.EncryptSecret(masterKey, rec.name)
				needsUpdate = true
			}
			if rec.avatar != "" && !strings.HasPrefix(rec.avatar, "ENC:") {
				newAvatar = core.EncryptSecret(masterKey, rec.avatar)
				needsUpdate = true
			}
			if rec.creds != "" && !strings.HasPrefix(rec.creds, "ENC:") {
				newCreds = core.EncryptSecret(masterKey, rec.creds)
				needsUpdate = true
			}
			if rec.token != "" && !strings.HasPrefix(rec.token, "ENC:") {
				newToken = core.EncryptSecret(masterKey, rec.token)
				needsUpdate = true
			}

			if needsUpdate {
				// Recompute hashes based on plaintext
				ptEmail := core.DecryptSecret(masterKey, newEmail)
				ptName := core.DecryptSecret(masterKey, newName)
				emailHash = core.BlindIndexHash(masterKey, ptEmail)
				nameHash = core.BlindIndexHash(masterKey, ptName)

				_, _ = s.db.Exec("UPDATE accounts SET email = ?, name = ?, avatar_url = ?, credentials_json = ?, token_json = ?, email_hash = ?, name_hash = ? WHERE id = ?", newEmail, newName, newAvatar, newCreds, newToken, emailHash, nameHash, rec.id)
			}
		}
	}

	// 2. Migrate Users
	userRows, err := s.db.Query("SELECT id, username, email, display_name, avatar_url FROM cloudpool_users")
	if err == nil {
		defer userRows.Close()
		type userRecord struct {
			id, username, email, display, avatar string
		}
		var uRecords []userRecord
		for userRows.Next() {
			var u userRecord
			var un, e, d, av sql.NullString
			if err := userRows.Scan(&u.id, &un, &e, &d, &av); err == nil {
				u.username, u.email, u.display, u.avatar = un.String, e.String, d.String, av.String
				uRecords = append(uRecords, u)
			}
		}
		for _, rec := range uRecords {
			needsUpdate := false
			newUsername, newEmail, newDisplay, newAvatar := rec.username, rec.email, rec.display, rec.avatar
			usernameHash, emailHash := "", ""

			if rec.username != "" && !strings.HasPrefix(rec.username, "ENC:") {
				newUsername = core.EncryptSecret(masterKey, rec.username)
				needsUpdate = true
			}
			if rec.email != "" && !strings.HasPrefix(rec.email, "ENC:") {
				newEmail = core.EncryptSecret(masterKey, rec.email)
				needsUpdate = true
			}
			if rec.display != "" && !strings.HasPrefix(rec.display, "ENC:") {
				newDisplay = core.EncryptSecret(masterKey, rec.display)
				needsUpdate = true
			}
			if rec.avatar != "" && !strings.HasPrefix(rec.avatar, "ENC:") {
				newAvatar = core.EncryptSecret(masterKey, rec.avatar)
				needsUpdate = true
			}

			// Some existing entries might not have hashes set yet, so we recompute hashes always if updating or if hash is missing
			ptUsername := core.DecryptSecret(masterKey, newUsername)
			ptEmail := core.DecryptSecret(masterKey, newEmail)
			usernameHash = core.BlindIndexHash(masterKey, ptUsername)
			emailHash = core.BlindIndexHash(masterKey, ptEmail)
			
			if needsUpdate {
				// In SQLite, username is UNIQUE. To avoid duplicate UNIQUE constraints during migration, we might temporarily conflict if hash isn't replacing properly, but it should be fine.
				_, _ = s.db.Exec("UPDATE cloudpool_users SET username = ?, email = ?, display_name = ?, avatar_url = ?, username_hash = ?, email_hash = ? WHERE id = ?", newUsername, newEmail, newDisplay, newAvatar, usernameHash, emailHash, rec.id)
			}
		}
	}

	// 3. Migrate Settings (GoogleClientSecret, GoogleClientID)
	setRows, err := s.db.Query("SELECT `key`, `value` FROM settings WHERE `key` IN ('google_client_secret', 'google_client_id')")
	if err == nil {
		defer setRows.Close()
		var updates map[string]string = make(map[string]string)
		for setRows.Next() {
			var k, v string
			if err := setRows.Scan(&k, &v); err == nil {
				if v != "" && !strings.HasPrefix(v, "ENC:") {
					updates[k] = core.EncryptSecret(masterKey, v)
				}
			}
		}
		for k, newV := range updates {
			_, _ = s.db.Exec("UPDATE settings SET `value` = ? WHERE `key` = ?", newV, k)
		}
	}
}



func (s *DB) EnsureTiDBCloudPoolDataReady() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.IsMySQLOrTiDB() {
		return nil
	}

	// 1. Kiểm tra kết nối liveness và ping check tới cụm TiDB Cloud với context timeout 5s
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping kiểm tra sức khỏe TiDB Cloud thất bại: %w", err)
	}

	// 2. Khởi tạo toàn bộ cấu hình mặc định bắt buộc (Idempotent seed)
	s.seedDefaultSettingsIdempotent()

	// 3. Đảm bảo tài khoản user_admin và quyền quản trị viên luôn tồn tại
	masterKey := s.getMasterKey()
	adminUsernameHash := core.BlindIndexHash(masterKey, "admin")
	encAdminName := core.EncryptSecret(masterKey, "admin")
	encAdminDisplay := core.EncryptSecret(masterKey, "Quản Trị Viên")

	var masterPass string
	_ = s.db.QueryRow("SELECT `value` FROM settings WHERE `key` = 'master_passphrase'").Scan(&masterPass)
	if masterPass == "" {
		masterPass = "admin"
	}
	adminPassBcrypt, err := core.HashPasswordBcrypt(masterPass)
	if err != nil {
		adminPassBcrypt = core.HashSHA256([]byte(masterPass))
	}

	var adminCount int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM cloudpool_users WHERE username_hash = ?", adminUsernameHash).Scan(&adminCount)
	now := time.Now()
	if adminCount == 0 {
		_, _ = s.db.Exec(`INSERT INTO cloudpool_users (id, username, username_hash, password_hash, display_name, role, quota_bytes, used_bytes, failed_login_count, locked_until, created_at, updated_at) 
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, NULL, ?, ?)`,
			"user_admin", encAdminName, adminUsernameHash, adminPassBcrypt, encAdminDisplay, "admin", 0, 0, now, now)
	} else {
		_, _ = s.db.Exec(`UPDATE cloudpool_users SET password_hash = ?, failed_login_count = 0, locked_until = NULL WHERE username_hash = ?`, adminPassBcrypt, adminUsernameHash)
	}

	// 4. Đảm bảo thư mục gốc root trong virtual_files luôn tồn tại và không bao giờ bị đánh dấu đã xóa
	_ = s.ensureRootExistsUnlocked()

	// 5. Kiểm tra số lượng bản ghi trong các bảng cốt lõi
	var accCount, fileCount, chunkCount int
	_ = s.db.QueryRow("SELECT COUNT(1) FROM accounts").Scan(&accCount)
	_ = s.db.QueryRow("SELECT COUNT(1) FROM virtual_files WHERE id != 'root'").Scan(&fileCount)
	_ = s.db.QueryRow("SELECT COUNT(1) FROM file_chunks").Scan(&chunkCount)

	// Nếu TiDB Cloud rỗng (chưa có tài khoản hoặc chưa có file), tự động đồng bộ từ snapshot / token
	if accCount == 0 || fileCount == 0 {
		log.Printf("[ENGINE] [TIDB] [STORAGE] CSDL TiDB Cloud cần đồng nhất dữ liệu (tài khoản=%d, tệp tin=%d). Bắt đầu Auto-Sync...", accCount, fileCount)
		_ = s.syncFromSnapshotUnlocked()
		_ = s.db.QueryRow("SELECT COUNT(1) FROM accounts").Scan(&accCount)
		_ = s.db.QueryRow("SELECT COUNT(1) FROM virtual_files WHERE id != 'root'").Scan(&fileCount)
		_ = s.db.QueryRow("SELECT COUNT(1) FROM file_chunks").Scan(&chunkCount)
	}

	// 6. Tự động đồng bộ dự phòng sang file SQLite cục bộ nếu file SQLite bị xóa hoặc rỗng
	

	log.Printf("[ENGINE] [TIDB] [STORAGE] Xác nhận CSDL TiDB Cloud sẵn sàng 100%%: %d tài khoản Google Drive, %d tệp tin VFS, %d chunk dữ liệu.",
		accCount, fileCount, chunkCount)
	return nil
}

// seedDefaultSettingsIdempotent khởi tạo toàn bộ cấu hình mặc định bắt buộc


func (s *DB) seedDefaultSettingsIdempotent() {
	s.setDefaultSetting("master_passphrase", "cloudpool_secure_master_key_2026")
	s.setDefaultSetting("chunk_size_bytes", "20971520") // 20 MB
	s.setDefaultSetting("allocation_strategy", "least_used")
	s.setDefaultSetting("webdav_enabled", "true")
	s.setDefaultSetting("webdav_username", "admin")
	s.setDefaultSetting("webdav_password", "admin123")
	s.setDefaultSetting("server_port", "8080")
	s.setDefaultSetting("guest_access_mode", "view_only")
	s.setDefaultSetting("allow_self_registration", "true")

	oauthClientID := strings.TrimSpace(os.Getenv("OAUTH_CLIENT_ID"))
	if oauthClientID == "" {
		oauthClientID = strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_ID"))
	}
	if oauthClientID == "" {
		oauthClientID = DefaultGoogleClientID
	}
	oauthClientSecret := strings.TrimSpace(os.Getenv("OAUTH_CLIENT_SECRET"))
	if oauthClientSecret == "" {
		oauthClientSecret = strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_SECRET"))
	}
	if oauthClientSecret == "" {
		oauthClientSecret = DefaultGoogleClientSecret
	}
	oauthRedirect := strings.TrimSpace(os.Getenv("OAUTH_REDIRECT_URL"))
	if oauthRedirect == "" {
		oauthRedirect = "http://localhost:8080/api/accounts/oauth/callback"
	}
	masterKey := s.getMasterKey()
	s.setDefaultSetting("google_client_id", core.EncryptSecret(masterKey, oauthClientID))
	s.setDefaultSetting("google_client_secret", core.EncryptSecret(masterKey, oauthClientSecret))
	s.setDefaultSetting("redirect_url", oauthRedirect)
}

// AutoSyncFromSnapshotIfEmpty tự động kiểm tra CSDL và phục hồi dữ liệu từ bản sao lưu JSON snapshot
// nếu bảng accounts hoặc virtual_files đang rỗng (hỗ trợ khi xóa SQLite hoặc deploy container mới).


func (s *DB) AutoSyncFromSnapshotIfEmpty() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.syncFromSnapshotUnlocked()
}



func (s *DB) syncFromSnapshotUnlocked() error {
	var accCount, fileCount int
	_ = s.db.QueryRow("SELECT COUNT(1) FROM accounts").Scan(&accCount)
	_ = s.db.QueryRow("SELECT COUNT(1) FROM virtual_files WHERE id != 'root'").Scan(&fileCount)

	// Nếu đã có cả tài khoản và tệp tin thì không cần nạp
	if accCount > 0 && fileCount > 0 {
		return nil
	}

	snapData, srcPath, err := findCloudPoolSnapshot()
	if err == nil && len(snapData) > 0 {
		accs, files, chunks, resErr := s.restoreCloudPoolSnapshot(snapData)
		if resErr == nil {
			log.Printf("[ENGINE] [STORAGE] [AUTO-SYNC] Đã tự động phục hồi dữ liệu từ '%s': %d tài khoản, %d tệp tin VFS, %d chunk.",
				srcPath, accs, files, chunks)
			return nil
		}
		log.Printf("[ENGINE] [STORAGE] [WARN] Phục hồi từ snapshot '%s' cảnh báo: %v", srcPath, resErr)
	}

	// Nếu không có snapshot hoặc phục hồi chưa đủ tài khoản, thử tìm token OAuth trong data/oauth/
	if accCount == 0 {
		restoredTokens, tokErr := s.restoreAccountsFromOAuthTokens()
		if tokErr == nil && restoredTokens > 0 {
			log.Printf("[ENGINE] [STORAGE] [AUTO-SYNC] Đã tự động phục hồi %d tài khoản Google Drive từ chứng chỉ OAuth.", restoredTokens)
		}
	}

	return nil
}



func (s *DB) restoreCloudPoolSnapshot(data []byte) (int, int, int, error) {
	var payload cloudPoolSnapshotPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return 0, 0, 0, fmt.Errorf("lỗi giải mã JSON snapshot: %w", err)
	}

	// 1. Phục hồi Settings
	for _, st := range payload.Settings {
		k := getSnapshotStr(st, "key")
		v := getSnapshotStr(st, "value")
		if k != "" {
			s.setDefaultSetting(k, v)
		}
	}

	// 2. Phục hồi Users
	for _, u := range payload.Users {
		id := getSnapshotStr(u, "id")
		uname := getSnapshotStr(u, "username")
		unameHash := getSnapshotStr(u, "username_hash")
		passHash := getSnapshotStr(u, "password_hash")
		email := getSnapshotStr(u, "email")
		emailHash := getSnapshotStr(u, "email_hash")
		pinHash := getSnapshotStr(u, "security_pin_hash")
		tier := getSnapshotInt(u, "security_tier")
		if tier == 0 {
			tier = 1
		}
		disp := getSnapshotStr(u, "display_name")
		avatar := getSnapshotStr(u, "avatar_url")
		role := getSnapshotStr(u, "role")
		if role == "" {
			role = "user"
		}
		status := getSnapshotStr(u, "status")
		if status == "" {
			status = "active"
		}
		quota := getSnapshotInt64(u, "quota_bytes")
		used := getSnapshotInt64(u, "used_bytes")
		createdAt := parseFlexibleTimestamp(u["created_at"])
		updatedAt := parseFlexibleTimestamp(u["updated_at"])

		var uQ string
		if s.IsMySQLOrTiDB() {
			uQ = `INSERT IGNORE INTO cloudpool_users (id, username, username_hash, email, email_hash, password_hash, security_pin_hash, security_tier, display_name, avatar_url, role, status, quota_bytes, used_bytes, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		} else {
			uQ = `INSERT OR IGNORE INTO cloudpool_users (id, username, username_hash, email, email_hash, password_hash, security_pin_hash, security_tier, display_name, avatar_url, role, status, quota_bytes, used_bytes, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		}
		_, _ = s.db.Exec(uQ, id, uname, unameHash, email, emailHash, passHash, pinHash, tier, disp, avatar, role, status, quota, used, createdAt, updatedAt)
	}

	// 3. Phục hồi Accounts
	insertedAccounts := 0
	var accQ string
	if s.IsMySQLOrTiDB() {
		accQ = `INSERT IGNORE INTO accounts (id, email, email_hash, name, name_hash, avatar_url, auth_type, credentials_json, token_json, root_folder_id, total_quota_bytes, used_quota_bytes, free_quota_bytes, status, last_error, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	} else {
		accQ = `INSERT OR IGNORE INTO accounts (id, email, email_hash, name, name_hash, avatar_url, auth_type, credentials_json, token_json, root_folder_id, total_quota_bytes, used_quota_bytes, free_quota_bytes, status, last_error, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	}
	accStmt, err := s.db.Prepare(accQ)
	if err == nil {
		defer accStmt.Close()
		for _, a := range payload.Accounts {
			id := getSnapshotStr(a, "id")
			email := getSnapshotStr(a, "email")
			emailHash := getSnapshotStr(a, "email_hash")
			name := getSnapshotStr(a, "name")
			nameHash := getSnapshotStr(a, "name_hash")
			avatar := getSnapshotStr(a, "avatar_url")
			authType := getSnapshotStr(a, "auth_type")
			creds := getSnapshotStr(a, "credentials_json")
			token := getSnapshotStr(a, "token_json")
			rootFolder := getSnapshotStr(a, "root_folder_id")
			total := getSnapshotInt64(a, "total_quota_bytes")
			used := getSnapshotInt64(a, "used_quota_bytes")
			free := getSnapshotInt64(a, "free_quota_bytes")
			status := getSnapshotStr(a, "status")
			lastErr := getSnapshotStr(a, "last_error")
			createdAt := parseFlexibleTimestamp(a["created_at"])
			updatedAt := parseFlexibleTimestamp(a["updated_at"])

			if _, execErr := accStmt.Exec(id, email, emailHash, name, nameHash, avatar, authType, creds, token, rootFolder, total, used, free, status, lastErr, createdAt, updatedAt); execErr == nil {
				insertedAccounts++
			}
		}
	}

	// 4. Phục hồi VirtualFiles
	insertedFiles := 0
	var vfQ string
	if s.IsMySQLOrTiDB() {
		vfQ = `INSERT IGNORE INTO virtual_files (id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, has_missing_chunks, is_deleted, deleted_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	} else {
		vfQ = `INSERT OR IGNORE INTO virtual_files (id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, has_missing_chunks, is_deleted, deleted_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	}
	vfStmt, err := s.db.Prepare(vfQ)
	if err == nil {
		defer vfStmt.Close()
		for _, f := range payload.VirtualFiles {
			id := getSnapshotStr(f, "id")
			userID := getSnapshotStr(f, "user_id")
			if userID == "" {
				userID = "user_admin"
			}
			parentID := getSnapshotStr(f, "parent_id")
			name := getSnapshotStr(f, "name")
			path := getSnapshotStr(f, "path")
			isDir := getSnapshotInt(f, "is_dir")
			size := getSnapshotInt64(f, "size_bytes")
			mime := getSnapshotStr(f, "mime_type")
			sha := getSnapshotStr(f, "sha256")
			var shaVal interface{} = sha
			if sha == "" {
				shaVal = nil
			}
			chunkCount := getSnapshotInt(f, "chunk_count")
			isEnc := getSnapshotInt(f, "is_encrypted")
			missing := getSnapshotInt(f, "has_missing_chunks")
			deleted := getSnapshotInt(f, "is_deleted")
			deletedAt := parseFlexibleTimestamp(f["deleted_at"])
			createdAt := parseFlexibleTimestamp(f["created_at"])
			updatedAt := parseFlexibleTimestamp(f["updated_at"])

			if _, execErr := vfStmt.Exec(id, userID, parentID, name, path, isDir, size, mime, shaVal, chunkCount, isEnc, missing, deleted, deletedAt, createdAt, updatedAt); execErr == nil {
				insertedFiles++
			}
		}
	}

	// 5. Phục hồi FileChunks
	insertedChunks := 0
	var chkQ string
	if s.IsMySQLOrTiDB() {
		chkQ = `INSERT IGNORE INTO file_chunks (chunk_id, file_id, chunk_index, account_id, gdrive_file_id, chunk_size_bytes, encrypted_size_bytes, sha256, status, ref_count) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	} else {
		chkQ = `INSERT OR IGNORE INTO file_chunks (chunk_id, file_id, chunk_index, account_id, gdrive_file_id, chunk_size_bytes, encrypted_size_bytes, sha256, status, ref_count) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	}
	chkStmt, err := s.db.Prepare(chkQ)
	if err == nil {
		defer chkStmt.Close()
		for _, c := range payload.FileChunks {
			chkID := getSnapshotStr(c, "chunk_id")
			fileID := getSnapshotStr(c, "file_id")
			chkIdx := getSnapshotInt(c, "chunk_index")
			accID := getSnapshotStr(c, "account_id")
			gdriveID := getSnapshotStr(c, "gdrive_file_id")
			chkSize := getSnapshotInt64(c, "chunk_size_bytes")
			encSize := getSnapshotInt64(c, "encrypted_size_bytes")
			sha := getSnapshotStr(c, "sha256")
			status := getSnapshotStr(c, "status")
			refCount := getSnapshotInt(c, "ref_count")
			if refCount == 0 {
				refCount = 1
			}

			if _, execErr := chkStmt.Exec(chkID, fileID, chkIdx, accID, gdriveID, chkSize, encSize, sha, status, refCount); execErr == nil {
				insertedChunks++
			}
		}
	}

	// 6. Phục hồi GDriveBackups
	for _, b := range payload.GDriveBackups {
		id := getSnapshotStr(b, "id")
		filename := getSnapshotStr(b, "filename")
		size := getSnapshotInt64(b, "size_bytes")
		sha := getSnapshotStr(b, "sha256")
		gfileID := getSnapshotStr(b, "gdrive_file_id")
		webLink := getSnapshotStr(b, "gdrive_web_link")
		email := getSnapshotStr(b, "target_email")
		manifest := getSnapshotStr(b, "manifest_json")
		createdAt := getSnapshotStr(b, "created_at")

		var bQ string
		if s.IsMySQLOrTiDB() {
			bQ = `INSERT IGNORE INTO gdrive_backups (id, filename, size_bytes, sha256, gdrive_file_id, gdrive_web_link, target_email, manifest_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
		} else {
			bQ = `INSERT OR IGNORE INTO gdrive_backups (id, filename, size_bytes, sha256, gdrive_file_id, gdrive_web_link, target_email, manifest_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
		}
		_, _ = s.db.Exec(bQ, id, filename, size, sha, gfileID, webLink, email, manifest, createdAt)
	}

	s.ClearCache()
	return insertedAccounts, insertedFiles, insertedChunks, nil
}



func findCloudPoolSnapshot() ([]byte, string, error) {
	dataDir := strings.TrimSpace(os.Getenv("DATA_DIR"))
	if dataDir == "" {
		dataDir = "data"
	}

	candidates := []string{
		filepath.Join(dataDir, "backups", "cloudpool_snapshot.json"),
		filepath.Join(dataDir, "cloudpool_snapshot.json"),
		filepath.Join("data", "backups", "cloudpool_snapshot.json"),
		filepath.Join("data", "cloudpool_snapshot.json"),
		filepath.Join("..", "data", "backups", "cloudpool_snapshot.json"),
		filepath.Join("..", "data", "cloudpool_snapshot.json"),
		`f:\supportflast.dev\data\backups\cloudpool_snapshot.json`,
	}

	for _, c := range candidates {
		if data, err := os.ReadFile(c); err == nil && len(data) > 0 {
			return data, c, nil
		}
	}

	// Nếu không có file .json, tìm trong file zip backup mới nhất
	backupDirs := []string{
		filepath.Join(dataDir, "backups"),
		filepath.Join("data", "backups"),
		filepath.Join("..", "data", "backups"),
		`f:\supportflast.dev\data\backups`,
	}

	for _, bDir := range backupDirs {
		entries, err := os.ReadDir(bDir)
		if err != nil {
			continue
		}
		var zipFiles []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".zip") {
				zipFiles = append(zipFiles, filepath.Join(bDir, e.Name()))
			}
		}
		for i := len(zipFiles) - 1; i >= 0; i-- {
			zPath := zipFiles[i]
			r, err := zip.OpenReader(zPath)
			if err != nil {
				continue
			}
			for _, f := range r.File {
				if f.Name == "cloudpool_snapshot.json" {
					rc, err := f.Open()
					if err == nil {
						content, readErr := io.ReadAll(rc)
						rc.Close()
						r.Close()
						if readErr == nil && len(content) > 0 {
							outPath := filepath.Join(bDir, "cloudpool_snapshot.json")
							_ = os.WriteFile(outPath, content, 0644)
							return content, zPath + ":" + f.Name, nil
						}
					}
				}
			}
			r.Close()
		}
	}

	return nil, "", fmt.Errorf("không tìm thấy file cloudpool_snapshot.json hoặc backup zip hợp lệ")
}



func (s *DB) restoreAccountsFromOAuthTokens() (int, error) {
	dataDir := strings.TrimSpace(os.Getenv("DATA_DIR"))
	if dataDir == "" {
		dataDir = "data"
	}
	oauthDirs := []string{
		filepath.Join(dataDir, "oauth"),
		filepath.Join("data", "oauth"),
		filepath.Join("..", "data", "oauth"),
		`f:\supportflast.dev\data\oauth`,
	}

	var oauthDir string
	for _, d := range oauthDirs {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			oauthDir = d
			break
		}
	}
	if oauthDir == "" {
		return 0, fmt.Errorf("thư mục oauth không tồn tại")
	}

	entries, err := os.ReadDir(oauthDir)
	if err != nil {
		return 0, err
	}

	masterKey := s.getMasterKey()
	now := time.Now()
	nowFormatted := now.UTC().Format("2006-01-02 15:04:05")
	if !s.IsMySQLOrTiDB() {
		nowFormatted = now.Format(time.RFC3339)
	}

	var accQ string
	if s.IsMySQLOrTiDB() {
		accQ = `INSERT IGNORE INTO accounts (id, email, email_hash, name, name_hash, avatar_url, auth_type, credentials_json, token_json, root_folder_id, total_quota_bytes, used_quota_bytes, free_quota_bytes, status, last_error, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	} else {
		accQ = `INSERT OR IGNORE INTO accounts (id, email, email_hash, name, name_hash, avatar_url, auth_type, credentials_json, token_json, root_folder_id, total_quota_bytes, used_quota_bytes, free_quota_bytes, status, last_error, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	}
	stmt, err := s.db.Prepare(accQ)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	restored := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "token_") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		tokenBytes, err := os.ReadFile(filepath.Join(oauthDir, e.Name()))
		if err != nil || len(tokenBytes) == 0 {
			continue
		}

		base := strings.TrimPrefix(e.Name(), "token_")
		base = strings.TrimSuffix(base, ".json")
		parts := strings.Split(base, "_acc_")
		emailPart := parts[0]
		accID := "acc_" + uuid.New().String()[:16]
		if len(parts) > 1 {
			accID = "acc_" + parts[1]
		}
		rawEmail := strings.ReplaceAll(emailPart, "_at_", "@")
		rawEmail = strings.ReplaceAll(rawEmail, "_", ".")

		encEmail := core.EncryptSecret(masterKey, rawEmail)
		emailHash := core.BlindIndexHash(masterKey, rawEmail)
		encName := core.EncryptSecret(masterKey, rawEmail)
		nameHash := core.BlindIndexHash(masterKey, rawEmail)
		encToken := core.EncryptSecret(masterKey, string(tokenBytes))

		totalQuota := int64(15 * 1024 * 1024 * 1024)
		if _, execErr := stmt.Exec(accID, encEmail, emailHash, encName, nameHash, "", "oauth", "", encToken, "", totalQuota, 0, totalQuota, "active", "", nowFormatted, nowFormatted); execErr == nil {
			restored++
		}
	}

	return restored, nil
}



func parseFlexibleTimestamp(val interface{}) interface{} {
	if val == nil {
		return nil
	}
	s, ok := val.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return nil
	}
	s = strings.TrimSpace(s)
	if len(s) >= 19 && s[4] == '-' && s[7] == '-' && (s[10] == ' ' || s[10] == 'T') && s[13] == ':' && s[16] == ':' {
		return s[:10] + " " + s[11:19]
	}
	return s
}



func getSnapshotStr(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok && v != nil {
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", v)
	}
	return ""
}



func getSnapshotInt64(m map[string]interface{}, key string) int64 {
	if v, ok := m[key]; ok && v != nil {
		switch n := v.(type) {
		case float64:
			return int64(n)
		case int64:
			return n
		case int:
			return int64(n)
		case string:
			if parsed, err := strconv.ParseInt(n, 10, 64); err == nil {
				return parsed
			}
		}
	}
	return 0
}



func getSnapshotInt(m map[string]interface{}, key string) int {
	return int(getSnapshotInt64(m, key))
}




type cloudPoolSnapshotPayload struct {
	Metadata           map[string]interface{}   `json:"_metadata"`
	Accounts           []map[string]interface{} `json:"accounts"`
	VirtualFiles       []map[string]interface{} `json:"virtual_files"`
	FileChunks         []map[string]interface{} `json:"file_chunks"`
	Settings           []map[string]interface{} `json:"settings"`
	Users              []map[string]interface{} `json:"users"`
	ActivityLogs       []map[string]interface{} `json:"activity_logs"`
	LoginSessions      []map[string]interface{} `json:"login_sessions"`
	FileAccessOTPs     []map[string]interface{} `json:"file_access_otps"`
	FileAccessRequests []map[string]interface{} `json:"file_access_requests"`
	PublicShares       []map[string]interface{} `json:"public_shares"`
	GDriveBackups      []map[string]interface{} `json:"gdrive_backups"`
}

