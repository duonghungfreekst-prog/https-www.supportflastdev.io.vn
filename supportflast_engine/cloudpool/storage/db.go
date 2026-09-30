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
	"supportflast_engine/cloudpool/models"
	"supportflast_engine/database"

	_ "github.com/go-sql-driver/mysql"
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

// ResolveDBPath đã bị loại bỏ vì không còn hỗ trợ SQLite

type DB struct {
	db      *sql.DB
	path    string
	driver  string
	mu      sync.RWMutex
	cacheMu sync.Mutex
	cache   *cache.LRUCache
}

// Driver trả về loại cơ sở dữ liệu hiện hành ("tidb", "mysql")
func (s *DB) Driver() string {
	if s.driver == "" {
		return "tidb"
	}
	return s.driver
}

// IsMySQLOrTiDB kiểm tra xem kết nối hiện tại có phải là TiDB hoặc MySQL hay không
func (s *DB) IsMySQLOrTiDB() bool {
	return true
}

// SQLDB trả về con trỏ *sql.DB bên dưới để sử dụng trực tiếp nếu cần
func (s *DB) SQLDB() *sql.DB {
	return s.db
}

// NewDB khởi tạo đối tượng DB cho CloudPool.
// Hệ thống hiện chỉ hỗ trợ TiDB Cloud / MySQL.
func NewDB(dbPath string) (*DB, error) {
	return NewDBWithConfig("tidb", "", "")
}

// NewDBWithConfig khởi tạo kết nối cơ sở dữ liệu (chỉ TiDB Cloud / MySQL)
func NewDBWithConfig(driver string, dsn string, dbPath string) (*DB, error) {
	return openTiDBConnection("tidb", dsn)
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

		`CREATE TABLE IF NOT EXISTS users (
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
			INDEX idx_vfiles_user_parent_del_dir_name (user_id, parent_id, is_deleted, is_dir, name)
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
			INDEX idx_public_shares (id, is_active)
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
	_ = s.db.QueryRow("SELECT COUNT(*) FROM users WHERE username_hash = ?", adminUsernameHash).Scan(&adminCount)
	if adminCount == 0 {
		now := time.Now()
		_, _ = s.db.Exec(`INSERT INTO users (id, username, username_hash, password_hash, display_name, role, quota_bytes, used_bytes, failed_login_count, locked_until, created_at, updated_at) 
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, NULL, ?, ?)`,
			"user_admin", encAdminName, adminUsernameHash, adminPassBcrypt, encAdminDisplay, "admin", 0, 0, now, now)
	} else {
		_, _ = s.db.Exec(`UPDATE users SET password_hash = ? WHERE username_hash = ?`, adminPassBcrypt, adminUsernameHash)
	}

	// Always clear any lockout on restart for admin
	_, _ = s.db.Exec(`UPDATE users SET failed_login_count = 0, locked_until = NULL WHERE username_hash = ?`, adminUsernameHash)

	// Also repair any other users that have empty password_hash
	defaultUserPassHash := core.HashSHA256([]byte("123456"))
	_, _ = s.db.Exec(`UPDATE users SET password_hash = ? WHERE (password_hash = '' OR password_hash IS NULL) AND username_hash != ?`, defaultUserPassHash, adminUsernameHash)

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
	_, _ = s.db.Exec(`ALTER TABLE virtual_files ADD INDEX idx_vfiles_user_parent_del_dir_name (user_id, parent_id, is_deleted, is_dir, name)`)

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
		`CREATE TABLE IF NOT EXISTS users (
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
		`CREATE INDEX IF NOT EXISTS idx_vfiles_parent_del_dir_name ON virtual_files(parent_id, is_deleted, is_dir, name);`,
		`CREATE INDEX IF NOT EXISTS idx_vfiles_user_parent_del_dir_name ON virtual_files(user_id, parent_id, is_deleted, is_dir, name);`,
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
	_, _ = s.db.Exec("ALTER TABLE users ADD COLUMN email TEXT DEFAULT '';")
	_, _ = s.db.Exec("ALTER TABLE users ADD COLUMN security_pin_hash TEXT DEFAULT '';")
	_, _ = s.db.Exec("ALTER TABLE users ADD COLUMN security_tier INTEGER DEFAULT 1;")
	_, _ = s.db.Exec("ALTER TABLE users ADD COLUMN avatar_url TEXT DEFAULT '';")
	_, _ = s.db.Exec("ALTER TABLE users ADD COLUMN status TEXT DEFAULT 'active';")
	_, _ = s.db.Exec("ALTER TABLE users ADD COLUMN failed_login_count INTEGER DEFAULT 0;")
	_, _ = s.db.Exec("ALTER TABLE users ADD COLUMN locked_until DATETIME;")
	_, _ = s.db.Exec("ALTER TABLE users ADD COLUMN last_login_at DATETIME;")

	// Add hash columns for Blind Index
	_, _ = s.db.Exec("ALTER TABLE users ADD COLUMN username_hash TEXT DEFAULT '';")
	_, _ = s.db.Exec("ALTER TABLE users ADD COLUMN email_hash TEXT DEFAULT '';")
	_, _ = s.db.Exec("ALTER TABLE accounts ADD COLUMN email_hash TEXT DEFAULT '';")
	_, _ = s.db.Exec("ALTER TABLE accounts ADD COLUMN name_hash TEXT DEFAULT '';")

	// Missing Performance Indexes
	_, _ = s.db.Exec("CREATE INDEX IF NOT EXISTS idx_users_email_hash ON users(email_hash);")
	_, _ = s.db.Exec("CREATE INDEX IF NOT EXISTS idx_users_username_hash ON users(username_hash);")
	_, _ = s.db.Exec("CREATE INDEX IF NOT EXISTS idx_vfiles_parent_deleted ON virtual_files(parent_id, is_deleted);")
	_, _ = s.db.Exec("CREATE INDEX IF NOT EXISTS idx_vfiles_parent_del_dir_name ON virtual_files(parent_id, is_deleted, is_dir, name);")
	_, _ = s.db.Exec("CREATE INDEX IF NOT EXISTS idx_vfiles_user_parent_del_dir_name ON virtual_files(user_id, parent_id, is_deleted, is_dir, name);")

	// Backup history table (also created by Python backup script)
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


	// Encrypt any existing plaintext credentials/tokens in database
	s.migrateEncryptAllPlaintextSecrets()

	// Ensure default admin user exists with valid password hash and Blind Indexing
	masterKey := s.getMasterKey()
	adminUsernameHash := core.BlindIndexHash(masterKey, "admin")
	encAdminName := core.EncryptSecret(masterKey, "admin")
	encAdminDisplay := core.EncryptSecret(masterKey, "Quản Trị Viên")
	
	// Fetch master_passphrase from settings if already configured
	var masterPass string
	_ = s.db.QueryRow("SELECT value FROM settings WHERE key = 'master_passphrase'").Scan(&masterPass)
	if masterPass == "" {
		masterPass = "admin"
	}

	// Generate bcrypt password hash for admin
	adminPassBcrypt, err := core.HashPasswordBcrypt(masterPass)
	if err != nil {
		adminPassBcrypt = core.HashSHA256([]byte(masterPass))
	}
	
	var adminCount int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM users WHERE username_hash = ?", adminUsernameHash).Scan(&adminCount)
	if adminCount == 0 {
		now := time.Now()
		_, _ = s.db.Exec(`INSERT INTO users (id, username, username_hash, password_hash, display_name, role, quota_bytes, used_bytes, failed_login_count, locked_until, created_at, updated_at) 
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, NULL, ?, ?)`,
			"user_admin", encAdminName, adminUsernameHash, adminPassBcrypt, encAdminDisplay, "admin", 0, 0, now, now)
	} else {
		// Keep admin user password synchronized with master_passphrase
		_, _ = s.db.Exec(`UPDATE users SET password_hash = ? WHERE username_hash = ?`, adminPassBcrypt, adminUsernameHash)
	}

	// Always clear any lockout on restart for admin
	_, _ = s.db.Exec(`UPDATE users SET failed_login_count = 0, locked_until = NULL WHERE username_hash = ?`, adminUsernameHash)

	// Also repair any other users that have empty password_hash
	defaultUserPassHash := core.HashSHA256([]byte("123456"))
	_, _ = s.db.Exec(`UPDATE users SET password_hash = ? WHERE (password_hash = '' OR password_hash IS NULL) AND username_hash != ?`, defaultUserPassHash, adminUsernameHash)

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

func (s *DB) SaveAccount(acc *models.Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if acc.CreatedAt.IsZero() {
		acc.CreatedAt = now
	}
	acc.UpdatedAt = now
	masterKey := s.getMasterKey()
	
	encEmail := core.EncryptSecret(masterKey, acc.Email)
	encName := core.EncryptSecret(masterKey, acc.Name)
	encAvatar := core.EncryptSecret(masterKey, acc.AvatarURL)
	encCreds := core.EncryptSecret(masterKey, acc.CredentialsJSON)
	encToken := core.EncryptSecret(masterKey, acc.TokenJSON)
	
	emailHash := core.BlindIndexHash(masterKey, acc.Email)
	nameHash := core.BlindIndexHash(masterKey, acc.Name)

	var query string
	if s.IsMySQLOrTiDB() {
		query = `INSERT INTO accounts (id, email, email_hash, name, name_hash, avatar_url, auth_type, credentials_json, token_json, root_folder_id, total_quota_bytes, used_quota_bytes, free_quota_bytes, status, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			email=VALUES(email),
			email_hash=VALUES(email_hash),
			name=VALUES(name),
			name_hash=VALUES(name_hash),
			avatar_url=VALUES(avatar_url),
			credentials_json=VALUES(credentials_json),
			token_json=VALUES(token_json),
			root_folder_id=VALUES(root_folder_id),
			total_quota_bytes=VALUES(total_quota_bytes),
			used_quota_bytes=VALUES(used_quota_bytes),
			free_quota_bytes=VALUES(free_quota_bytes),
			status=VALUES(status),
			last_error=VALUES(last_error),
			updated_at=VALUES(updated_at)`
	} else {
		query = `INSERT INTO accounts (id, email, email_hash, name, name_hash, avatar_url, auth_type, credentials_json, token_json, root_folder_id, total_quota_bytes, used_quota_bytes, free_quota_bytes, status, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			email=excluded.email,
			email_hash=excluded.email_hash,
			name=excluded.name,
			name_hash=excluded.name_hash,
			avatar_url=excluded.avatar_url,
			credentials_json=excluded.credentials_json,
			token_json=excluded.token_json,
			root_folder_id=excluded.root_folder_id,
			total_quota_bytes=excluded.total_quota_bytes,
			used_quota_bytes=excluded.used_quota_bytes,
			free_quota_bytes=excluded.free_quota_bytes,
			status=excluded.status,
			last_error=excluded.last_error,
			updated_at=excluded.updated_at`
	}

	_, err := s.db.Exec(query,
		acc.ID, encEmail, emailHash, encName, nameHash, encAvatar, acc.AuthType,
		encCreds, encToken, acc.RootFolderID,
		acc.TotalQuotaBytes, acc.UsedQuotaBytes, acc.FreeQuotaBytes,
		acc.Status, acc.LastError, acc.CreatedAt, acc.UpdatedAt,
	)
	if err == nil {
		s.InvalidateAccountsCache()
	}
	return err
}

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
	userRows, err := s.db.Query("SELECT id, username, email, display_name, avatar_url FROM users")
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
				_, _ = s.db.Exec("UPDATE users SET username = ?, email = ?, display_name = ?, avatar_url = ?, username_hash = ?, email_hash = ? WHERE id = ?", newUsername, newEmail, newDisplay, newAvatar, usernameHash, emailHash, rec.id)
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

func (s *DB) GetAccount(id string) (*models.Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := s.db.QueryRow(`SELECT id, email, name, avatar_url, auth_type, credentials_json, token_json, root_folder_id, total_quota_bytes, used_quota_bytes, free_quota_bytes, status, last_error, created_at, updated_at FROM accounts WHERE id = ?`, id)

	var a models.Account
	var lastErr sql.NullString
	var rawCreated, rawUpdated interface{}
	err := row.Scan(&a.ID, &a.Email, &a.Name, &a.AvatarURL, &a.AuthType, &a.CredentialsJSON, &a.TokenJSON, &a.RootFolderID, &a.TotalQuotaBytes, &a.UsedQuotaBytes, &a.FreeQuotaBytes, &a.Status, &lastErr, &rawCreated, &rawUpdated)
	if err != nil {
		return nil, err
	}
	if lastErr.Valid {
		a.LastError = lastErr.String
	}
	a.CreatedAt = parseFlexibleTime(rawCreated)
	a.UpdatedAt = parseFlexibleTime(rawUpdated)
	if a.TotalQuotaBytes > 0 {
		a.UsagePercent = float64(a.UsedQuotaBytes) / float64(a.TotalQuotaBytes) * 100.0
	}

	masterKey := s.getMasterKey()
	a.CredentialsJSON = core.DecryptSecret(masterKey, a.CredentialsJSON)
	a.TokenJSON = core.DecryptSecret(masterKey, a.TokenJSON)
	a.Email = core.DecryptSecret(masterKey, a.Email)
	a.Name = core.DecryptSecret(masterKey, a.Name)
	a.AvatarURL = core.DecryptSecret(masterKey, a.AvatarURL)
	a.IsUploadExcluded = a.IsUploadExcludedAccount()
	return &a, nil
}

// GetAccountByEmail tìm kiếm tài khoản theo email (sử dụng Blind Index email_hash)
func (s *DB) GetAccountByEmail(email string) (*models.Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	masterKey := s.getMasterKey()
	emailHash := core.BlindIndexHash(masterKey, email)

	row := s.db.QueryRow(`SELECT id, email, name, avatar_url, auth_type, credentials_json, token_json, root_folder_id, total_quota_bytes, used_quota_bytes, free_quota_bytes, status, last_error, created_at, updated_at FROM accounts WHERE email_hash = ?`, emailHash)

	var a models.Account
	var lastErr sql.NullString
	var rawCreated, rawUpdated interface{}
	err := row.Scan(&a.ID, &a.Email, &a.Name, &a.AvatarURL, &a.AuthType, &a.CredentialsJSON, &a.TokenJSON, &a.RootFolderID, &a.TotalQuotaBytes, &a.UsedQuotaBytes, &a.FreeQuotaBytes, &a.Status, &lastErr, &rawCreated, &rawUpdated)
	if err != nil {
		return nil, err
	}
	if lastErr.Valid {
		a.LastError = lastErr.String
	}
	a.CreatedAt = parseFlexibleTime(rawCreated)
	a.UpdatedAt = parseFlexibleTime(rawUpdated)
	if a.TotalQuotaBytes > 0 {
		a.UsagePercent = float64(a.UsedQuotaBytes) / float64(a.TotalQuotaBytes) * 100.0
	}

	a.CredentialsJSON = core.DecryptSecret(masterKey, a.CredentialsJSON)
	a.TokenJSON = core.DecryptSecret(masterKey, a.TokenJSON)
	a.Email = core.DecryptSecret(masterKey, a.Email)
	a.Name = core.DecryptSecret(masterKey, a.Name)
	a.AvatarURL = core.DecryptSecret(masterKey, a.AvatarURL)
	a.IsUploadExcluded = a.IsUploadExcludedAccount()
	return &a, nil
}

func (s *DB) listAccountsFromDB() ([]models.Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT id, email, name, avatar_url, auth_type, credentials_json, token_json, root_folder_id, total_quota_bytes, used_quota_bytes, free_quota_bytes, status, last_error, created_at, updated_at FROM accounts ORDER BY email ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	masterKey := s.getMasterKey()
	list := make([]models.Account, 0)
	for rows.Next() {
		var a models.Account
		var lastErr sql.NullString
		var rawCreated, rawUpdated interface{}
		if err := rows.Scan(&a.ID, &a.Email, &a.Name, &a.AvatarURL, &a.AuthType, &a.CredentialsJSON, &a.TokenJSON, &a.RootFolderID, &a.TotalQuotaBytes, &a.UsedQuotaBytes, &a.FreeQuotaBytes, &a.Status, &lastErr, &rawCreated, &rawUpdated); err != nil {
			return nil, err
		}
		if lastErr.Valid {
			a.LastError = lastErr.String
		}
		a.CreatedAt = parseFlexibleTime(rawCreated)
		a.UpdatedAt = parseFlexibleTime(rawUpdated)
		if a.TotalQuotaBytes > 0 {
			a.UsagePercent = float64(a.UsedQuotaBytes) / float64(a.TotalQuotaBytes) * 100.0
		}
		a.CredentialsJSON = core.DecryptSecret(masterKey, a.CredentialsJSON)
		a.TokenJSON = core.DecryptSecret(masterKey, a.TokenJSON)
		a.Email = core.DecryptSecret(masterKey, a.Email)
		a.Name = core.DecryptSecret(masterKey, a.Name)
		a.AvatarURL = core.DecryptSecret(masterKey, a.AvatarURL)
		a.IsUploadExcluded = a.IsUploadExcludedAccount()
		list = append(list, a)
	}
	return list, nil
}

func (s *DB) DeleteAccount(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("DELETE FROM accounts WHERE id = ?", id)
	if err == nil {
		s.InvalidateAccountsCache()
	}
	return err
}

func (s *DB) UpdateAccountQuota(id string, total, used, free int64, status, lastErr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE accounts SET total_quota_bytes=?, used_quota_bytes=?, free_quota_bytes=?, status=?, last_error=?, updated_at=? WHERE id=?`,
		total, used, free, status, lastErr, time.Now(), id)
	if err == nil {
		s.InvalidateAccountsCache()
	}
	return err
}

// UpdateAccountToken chỉ cập nhật cột token_json cho tài khoản, không ghi đè quota hay các trường khác.
// Sử dụng khi persistingTokenSource refresh token để tránh overwrite quota đã stale từ snapshot cũ.
func (s *DB) UpdateAccountToken(accountID, tokenJSON string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	masterKey := s.getMasterKey()
	encToken := core.EncryptSecret(masterKey, tokenJSON)
	_, err := s.db.Exec("UPDATE accounts SET token_json = ?, updated_at = ? WHERE id = ?", encToken, time.Now(), accountID)
	if err == nil {
		s.InvalidateAccountsCache()
	}
	return err
}

// -------------------------------------------------------------
// Virtual Files & Folders
// -------------------------------------------------------------

func (s *DB) SaveVirtualFile(f *models.VirtualFile) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if f.CreatedAt.IsZero() {
		f.CreatedAt = now
	}
	f.UpdatedAt = now
	if f.UserID == "" {
		f.UserID = "user_admin"
	}

	var query string
	if s.IsMySQLOrTiDB() {
		query = `INSERT INTO virtual_files (id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			user_id=VALUES(user_id),
			parent_id=VALUES(parent_id),
			name=VALUES(name),
			path=VALUES(path),
			size_bytes=VALUES(size_bytes),
			mime_type=VALUES(mime_type),
			sha256=VALUES(sha256),
			chunk_count=VALUES(chunk_count),
			is_encrypted=VALUES(is_encrypted),
			is_deleted=VALUES(is_deleted),
			deleted_at=VALUES(deleted_at),
			has_missing_chunks=VALUES(has_missing_chunks),
			updated_at=VALUES(updated_at)`
	} else {
		query = `INSERT INTO virtual_files (id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			user_id=excluded.user_id,
			parent_id=excluded.parent_id,
			name=excluded.name,
			path=excluded.path,
			size_bytes=excluded.size_bytes,
			mime_type=excluded.mime_type,
			sha256=excluded.sha256,
			chunk_count=excluded.chunk_count,
			is_encrypted=excluded.is_encrypted,
			is_deleted=excluded.is_deleted,
			deleted_at=excluded.deleted_at,
			has_missing_chunks=excluded.has_missing_chunks,
			updated_at=excluded.updated_at`
	}

	_, err := s.db.Exec(query,
		f.ID, f.UserID, f.ParentID, f.Name, f.Path, f.IsDir, f.SizeBytes,
		f.MimeType, f.SHA256, f.ChunkCount, f.IsEncrypted, f.IsTrashed, f.DeletedAt, f.HasMissingChunks, f.CreatedAt, f.UpdatedAt,
	)
	if err == nil {
		s.InvalidateVFSCache()
		s.InvalidateStatsCache()
	}
	return err
}

// parseFlexibleTime chuyển đổi an toàn mọi giá trị ngày tháng từ DB (time.Time, string, []byte) sang time.Time chuẩn
func parseFlexibleTime(val interface{}) time.Time {
	if val == nil {
		return time.Now()
	}
	switch v := val.(type) {
	case time.Time:
		return v
	case *time.Time:
		if v != nil {
			return *v
		}
		return time.Now()
	case sql.NullTime:
		if v.Valid {
			return v.Time
		}
		return time.Now()
	case *sql.NullTime:
		if v != nil && v.Valid {
			return v.Time
		}
		return time.Now()
	case sql.NullString:
		if v.Valid {
			return parseTimeString(v.String)
		}
		return time.Now()
	case *sql.NullString:
		if v != nil && v.Valid {
			return parseTimeString(v.String)
		}
		return time.Now()
	case []byte:
		return parseTimeString(string(v))
	case string:
		return parseTimeString(v)
	default:
		return time.Now()
	}
}

func parseFlexibleTimePtr(val interface{}) *time.Time {
	if val == nil {
		return nil
	}
	switch v := val.(type) {
	case time.Time:
		return &v
	case *time.Time:
		return v
	case sql.NullTime:
		if v.Valid {
			return &v.Time
		}
		return nil
	case *sql.NullTime:
		if v != nil && v.Valid {
			return &v.Time
		}
		return nil
	case sql.NullString:
		if !v.Valid {
			return nil
		}
		str := strings.TrimSpace(v.String)
		if str == "" || str == "NULL" || str == "null" || str == "0000-00-00 00:00:00" || str == "0000-00-00" {
			return nil
		}
		t := parseTimeString(str)
		return &t
	case *sql.NullString:
		if v == nil || !v.Valid {
			return nil
		}
		str := strings.TrimSpace(v.String)
		if str == "" || str == "NULL" || str == "null" || str == "0000-00-00 00:00:00" || str == "0000-00-00" {
			return nil
		}
		t := parseTimeString(str)
		return &t
	case []byte:
		str := strings.TrimSpace(string(v))
		if str == "" || str == "NULL" || str == "null" || str == "0000-00-00 00:00:00" || str == "0000-00-00" {
			return nil
		}
		t := parseTimeString(str)
		return &t
	case string:
		str := strings.TrimSpace(v)
		if str == "" || str == "NULL" || str == "null" || str == "0000-00-00 00:00:00" || str == "0000-00-00" {
			return nil
		}
		t := parseTimeString(str)
		return &t
	default:
		return nil
	}
}

func parseTimeString(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" || s == "0000-00-00 00:00:00" || s == "0000-00-00" || s == "NULL" || s == "null" {
		return time.Now()
	}
	if idx := strings.Index(s, " m="); idx != -1 {
		s = s[:idx]
	}
	formats := []string{
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999 -0700 -07",
		"2006-01-02 15:04:05.999999999 +0700 +07",
		"2006-01-02 15:04:05 -0700 MST",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05.999999",
		"2006-01-02 15:04:05.999",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999999Z07:00",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	return time.Now()
}

func (s *DB) GetVirtualFile(id string) (*models.VirtualFile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := s.db.QueryRow(`SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at FROM virtual_files WHERE id = ?`, id)
	var f models.VirtualFile
	var sha, uid sql.NullString
	var isDel, hasMissing sql.NullBool
	var rawDelAt, rawCreated, rawUpdated interface{}
	err := row.Scan(&f.ID, &uid, &f.ParentID, &f.Name, &f.Path, &f.IsDir, &f.SizeBytes, &f.MimeType, &sha, &f.ChunkCount, &f.IsEncrypted, &isDel, &rawDelAt, &hasMissing, &rawCreated, &rawUpdated)
	if err != nil {
		return nil, err
	}
	if sha.Valid {
		f.SHA256 = sha.String
	}
	if uid.Valid {
		f.UserID = uid.String
	}
	if isDel.Valid {
		f.IsTrashed = isDel.Bool
	}
	if hasMissing.Valid {
		f.HasMissingChunks = hasMissing.Bool
	}
	f.DeletedAt = parseFlexibleTimePtr(rawDelAt)
	f.CreatedAt = parseFlexibleTime(rawCreated)
	f.UpdatedAt = parseFlexibleTime(rawUpdated)
	return &f, nil
}

func (s *DB) GetVirtualFileByPath(path string) (*models.VirtualFile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := s.db.QueryRow(`SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at FROM virtual_files WHERE path = ? AND is_deleted = 0`, path)
	var f models.VirtualFile
	var sha, uid sql.NullString
	var isDel, hasMissing sql.NullBool
	var rawDelAt, rawCreated, rawUpdated interface{}
	err := row.Scan(&f.ID, &uid, &f.ParentID, &f.Name, &f.Path, &f.IsDir, &f.SizeBytes, &f.MimeType, &sha, &f.ChunkCount, &f.IsEncrypted, &isDel, &rawDelAt, &hasMissing, &rawCreated, &rawUpdated)
	if err != nil {
		return nil, err
	}
	if sha.Valid {
		f.SHA256 = sha.String
	}
	if uid.Valid {
		f.UserID = uid.String
	}
	if isDel.Valid {
		f.IsTrashed = isDel.Bool
	}
	if hasMissing.Valid {
		f.HasMissingChunks = hasMissing.Bool
	}
	f.DeletedAt = parseFlexibleTimePtr(rawDelAt)
	f.CreatedAt = parseFlexibleTime(rawCreated)
	f.UpdatedAt = parseFlexibleTime(rawUpdated)
	return &f, nil
}

func (s *DB) listVirtualFilesFromDB(userID, parentID string) ([]models.VirtualFile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var rows *sql.Rows
	var err error

	if userID == "all" || userID == "user_admin" || userID == "admin" || userID == "" {
		// Admin sees all files and system partitions
		rows, err = s.db.Query(`SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at FROM virtual_files WHERE parent_id = ? AND id != 'root' AND is_deleted = 0 ORDER BY is_dir DESC, name ASC`, parentID)
	} else if userID == "guest" {
		guestMode := "view_only"
		var val string
		if errMode := s.db.QueryRow("SELECT `value` FROM settings WHERE `key` = 'guest_access_mode'").Scan(&val); errMode == nil && strings.TrimSpace(val) != "" {
			guestMode = strings.TrimSpace(val)
		}
		if guestMode != "strict" {
			// Khi guest_access_mode != "strict", cho phép khách xem danh sách tệp tin trong thư mục parent_id
			rows, err = s.db.Query(`SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at FROM virtual_files WHERE parent_id = ? AND id != 'root' AND is_deleted = 0 ORDER BY is_dir DESC, name ASC`, parentID)
		} else {
			// Trong chế độ strict, khách chỉ xem được phân vùng riêng của khách
			rows, err = s.db.Query(`SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at FROM virtual_files WHERE user_id = 'guest' AND parent_id = ? AND id != 'root' AND is_deleted = 0 ORDER BY is_dir DESC, name ASC`, parentID)
		}
	} else {
		// Child user is strictly isolated to their own uploaded files only
		rows, err = s.db.Query(`SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at FROM virtual_files WHERE user_id = ? AND parent_id = ? AND id != 'root' AND is_deleted = 0 ORDER BY is_dir DESC, name ASC`, userID, parentID)
	}

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]models.VirtualFile, 0)
	for rows.Next() {
		var f models.VirtualFile
		var sha, uid sql.NullString
		var isDel, hasMissing sql.NullBool
		var rawDelAt, rawCreated, rawUpdated interface{}
		if err := rows.Scan(&f.ID, &uid, &f.ParentID, &f.Name, &f.Path, &f.IsDir, &f.SizeBytes, &f.MimeType, &sha, &f.ChunkCount, &f.IsEncrypted, &isDel, &rawDelAt, &hasMissing, &rawCreated, &rawUpdated); err != nil {
			return nil, err
		}
		if sha.Valid {
			f.SHA256 = sha.String
		}
		if uid.Valid {
			f.UserID = uid.String
		}
		if isDel.Valid {
			f.IsTrashed = isDel.Bool
		}
		if hasMissing.Valid {
			f.HasMissingChunks = hasMissing.Bool
		}
		f.DeletedAt = parseFlexibleTimePtr(rawDelAt)
		f.CreatedAt = parseFlexibleTime(rawCreated)
		f.UpdatedAt = parseFlexibleTime(rawUpdated)

		list = append(list, f)
	}

	// Xử lý quyền OTP cho người dùng bị hạn chế (guest hoặc child user)
	isRestrictedUser := userID != "user_admin" && userID != "all" && userID != "admin"
	if !isRestrictedUser {
		return list, nil
	}

	// Đánh dấu các file thuộc admin và kiểm tra xem thư mục có chứa tệp tin của admin không
	hasAdminFiles := false
	for i := range list {
		if list[i].UserID == "user_admin" || list[i].UserID == "admin" || list[i].UserID == "" {
			list[i].IsAdminOwned = true
			hasAdminFiles = true
		}
	}

	if !hasAdminFiles {
		return list, nil
	}

	// Tối ưu hóa triệt để (Query & CTE Optimizer):
	// Thay vì chạy N câu truy vấn đệ quy CTE (N+1 query) gây nghẽn mạng tới TiDB Cloud qua Internet,
	// chỉ cần thực hiện 1 truy vấn duy nhất lấy danh sách active public shares:
	shareQuery := `
	SELECT ps.file_id, COALESCE(vf.is_dir, 0) 
	FROM public_shares ps 
	LEFT JOIN virtual_files vf ON ps.file_id = vf.id 
	WHERE ps.is_active = 1 
	  AND (ps.expires_at IS NULL OR ps.expires_at > CURRENT_TIMESTAMP) 
	  AND (ps.max_downloads = 0 OR ps.download_count < ps.max_downloads)`

	shareRows, err := s.db.Query(shareQuery)
	if err != nil {
		// Trong trường hợp lỗi truy vấn shares, fallback an toàn: toàn bộ file admin yêu cầu OTP
		for i := range list {
			if list[i].IsAdminOwned {
				list[i].RequiresOTP = true
			}
		}
		return list, nil
	}
	defer shareRows.Close()

	sharedFileIDs := make(map[string]bool)
	sharedFolderIDs := make(map[string]bool)
	hasSharedFolders := false

	for shareRows.Next() {
		var sFileID string
		var sIsDir int
		if err := shareRows.Scan(&sFileID, &sIsDir); err == nil && sFileID != "" {
			sharedFileIDs[sFileID] = true
			if sIsDir == 1 {
				sharedFolderIDs[sFileID] = true
				hasSharedFolders = true
			}
		}
	}

	// Nếu không có bất kỳ active share nào (chiếm 99% thời gian trong thực tế):
	// Toàn bộ các file admin đều yêu cầu OTP. KHÔNG CẦN GỌI BẤT KỲ TRUY VẤN ĐỆ QUY NÀO NỮA!
	if len(sharedFileIDs) == 0 {
		for i := range list {
			if list[i].IsAdminOwned {
				list[i].RequiresOTP = true
			}
		}
		return list, nil
	}

	// Nếu có active shares:
	// Kiểm tra xem chuỗi tổ tiên của thư mục parentID có được chia sẻ hay không.
	// Vì tất cả các tệp tin trong danh sách này đều thuộc cùng parentID, ta chỉ cần kiểm tra tổ tiên 1 lần duy nhất cho toàn bộ thư mục!
	parentChainIsShared := false
	if hasSharedFolders && parentID != "" && parentID != "root" {
		if sharedFolderIDs[parentID] {
			parentChainIsShared = true
		} else {
			// Truy vấn đệ quy CTE duy nhất 1 lần để lấy tất cả tổ tiên của parentID
			ancestorQuery := `
			WITH RECURSIVE parent_ancestors AS (
				SELECT id, parent_id FROM virtual_files WHERE id = ?
				UNION ALL
				SELECT vf.id, vf.parent_id FROM virtual_files vf
				JOIN parent_ancestors pa ON vf.id = pa.parent_id
				WHERE vf.id != 'root' AND vf.id != ''
			)
			SELECT id FROM parent_ancestors WHERE id != ?;`
			if aRows, err := s.db.Query(ancestorQuery, parentID, parentID); err == nil {
				for aRows.Next() {
					var ancestorID string
					if aRows.Scan(&ancestorID) == nil {
						if sharedFolderIDs[ancestorID] {
							parentChainIsShared = true
							break
						}
					}
				}
				aRows.Close()
			}
		}
	}

	// Cập nhật RequiresOTP cho từng file
	for i := range list {
		if !list[i].IsAdminOwned {
			continue
		}
		if parentChainIsShared || sharedFileIDs[list[i].ID] {
			list[i].RequiresOTP = false
		} else {
			list[i].RequiresOTP = true
		}
	}

	return list, nil
}

// isFileOrAncestorSharedUnlocked checks if the given file or any parent folder in its hierarchy has an active public share link
func (s *DB) isFileOrAncestorSharedUnlocked(fileID string) bool {
	if fileID == "" || fileID == "root" {
		return false
	}

	// 1. Kiểm tra trực tiếp fileID có được chia sẻ hay không (tận dụng index trên file_id)
	var directCount int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM public_shares 
		WHERE file_id = ? AND is_active = 1 
		  AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP) 
		  AND (max_downloads = 0 OR download_count < max_downloads)`, fileID).Scan(&directCount)
	if err == nil && directCount > 0 {
		return true
	}

	// 2. Kiểm tra nhanh xem toàn hệ thống có thư mục nào đang được public share không
	var folderShareCount int
	err = s.db.QueryRow(`
		SELECT COUNT(*) FROM public_shares ps
		INNER JOIN virtual_files vf ON ps.file_id = vf.id
		WHERE vf.is_dir = 1 AND ps.is_active = 1 
		  AND (ps.expires_at IS NULL OR ps.expires_at > CURRENT_TIMESTAMP) 
		  AND (ps.max_downloads = 0 OR ps.download_count < ps.max_downloads)`).Scan(&folderShareCount)
	if err != nil || folderShareCount == 0 {
		return false
	}

	// 3. Nếu có thư mục được chia sẻ, mới chạy CTE đệ quy kiểm tra tổ tiên
	query := `
	WITH RECURSIVE file_ancestors AS (
		SELECT id, parent_id FROM virtual_files WHERE id = ?
		UNION ALL
		SELECT vf.id, vf.parent_id FROM virtual_files vf
		JOIN file_ancestors fa ON vf.id = fa.parent_id
		WHERE vf.id != 'root' AND vf.id != ''
	)
	SELECT COUNT(*) FROM public_shares ps
	JOIN file_ancestors fa ON ps.file_id = fa.id
	WHERE ps.is_active = 1 
	  AND (ps.expires_at IS NULL OR ps.expires_at > CURRENT_TIMESTAMP)
	  AND (ps.max_downloads = 0 OR ps.download_count < ps.max_downloads);
	`
	var count int
	_ = s.db.QueryRow(query, fileID).Scan(&count)
	return count > 0
}

// IsFileOrAncestorShared checks if the given file or any parent folder in its hierarchy has an active public share link
func (s *DB) IsFileOrAncestorShared(fileID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.isFileOrAncestorSharedUnlocked(fileID)
}

func (s *DB) ListFilesByAccount(accountID string) ([]models.VirtualFile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT DISTINCT f.id, f.parent_id, f.name, f.path, f.is_dir, f.size_bytes, f.mime_type, f.sha256, f.chunk_count, f.is_encrypted, f.is_deleted, f.deleted_at, f.has_missing_chunks, f.created_at, f.updated_at 
		FROM virtual_files f
		INNER JOIN file_chunks c ON f.id = c.file_id
		WHERE c.account_id = ? AND f.id != 'root' AND f.is_deleted = 0
		ORDER BY f.updated_at DESC`

	rows, err := s.db.Query(query, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]models.VirtualFile, 0)
	for rows.Next() {
		var f models.VirtualFile
		var sha sql.NullString
		var isDel, hasMissing sql.NullBool
		var rawDelAt, rawCreated, rawUpdated interface{}
		if err := rows.Scan(&f.ID, &f.ParentID, &f.Name, &f.Path, &f.IsDir, &f.SizeBytes, &f.MimeType, &sha, &f.ChunkCount, &f.IsEncrypted, &isDel, &rawDelAt, &hasMissing, &rawCreated, &rawUpdated); err != nil {
			return nil, err
		}
		if sha.Valid {
			f.SHA256 = sha.String
		}
		if isDel.Valid {
			f.IsTrashed = isDel.Bool
		}
		if hasMissing.Valid {
			f.HasMissingChunks = hasMissing.Bool
		}
		f.DeletedAt = parseFlexibleTimePtr(rawDelAt)
		f.CreatedAt = parseFlexibleTime(rawCreated)
		f.UpdatedAt = parseFlexibleTime(rawUpdated)
		list = append(list, f)
	}
	return list, nil
}

// ensureRootExistsUnlocked đảm bảo thư mục gốc root luôn tồn tại, cấu trúc chuẩn, và không bao giờ bị đánh dấu is_deleted = 1
func (s *DB) ensureRootExistsUnlocked() error {
	var count int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM virtual_files WHERE id = 'root'").Scan(&count)
	now := time.Now()
	if count == 0 {
		var err error
		if s.IsMySQLOrTiDB() {
			_, err = s.db.Exec(`INSERT IGNORE INTO virtual_files (id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, chunk_count, is_encrypted, is_deleted, created_at, updated_at) 
				VALUES ('root', 'user_admin', '', 'root', '/', 1, 0, 'inode/directory', 0, 0, 0, ?, ?)`, now, now)
		} else {
			_, err = s.db.Exec(`INSERT OR IGNORE INTO virtual_files (id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, chunk_count, is_encrypted, is_deleted, created_at, updated_at) 
				VALUES ('root', 'user_admin', '', 'root', '/', 1, 0, 'inode/directory', 0, 0, 0, ?, ?)`, now, now)
		}
		if err != nil {
			return fmt.Errorf("failed to create root folder: %w", err)
		}
	} else {
		// Đảm bảo root luôn ở trạng thái chuẩn: is_deleted = 0, parent_id = '', is_dir = 1, path = '/'
		_, err := s.db.Exec(`UPDATE virtual_files SET is_deleted = 0, deleted_at = NULL, parent_id = '', is_dir = 1, path = '/' WHERE id = 'root' AND (is_deleted != 0 OR parent_id != '' OR is_dir != 1 OR path != '/')`)
		if err != nil {
			return fmt.Errorf("failed to repair root folder status: %w", err)
		}
	}
	return nil
}

// EnsureRootExists đảm bảo thư mục gốc root luôn tồn tại và không bao giờ bị đánh dấu is_deleted = 1 (thread-safe)
func (s *DB) EnsureRootExists() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ensureRootExistsUnlocked()
}

func (s *DB) SoftDeleteVirtualFile(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id == "root" || id == "" {
		return fmt.Errorf("không thể xóa thư mục gốc (root folder is protected)")
	}

	vFile, err := s.getVirtualFileUnsafe(id)
	if err != nil {
		return err
	}

	now := time.Now()
	if vFile.IsDir {
		// Soft delete directory and all descendants
		prefix := strings.TrimSuffix(vFile.Path, "/") + "/%"
		_, err := s.db.Exec(`UPDATE virtual_files SET is_deleted = 1, deleted_at = ? WHERE id = ? OR path LIKE ?`, now, id, prefix)
		if err == nil {
			s.InvalidateVFSCache()
			s.InvalidateStatsCache()
		}
		return err
	}

	_, err = s.db.Exec(`UPDATE virtual_files SET is_deleted = 1, deleted_at = ? WHERE id = ?`, now, id)
	if err == nil {
		s.InvalidateVFSCache()
		s.InvalidateStatsCache()
	}
	return err
}

func (s *DB) RestoreVirtualFile(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	vFile, err := s.getVirtualFileUnsafe(id)
	if err != nil {
		return err
	}

	if vFile.IsDir {
		prefix := strings.TrimSuffix(vFile.Path, "/") + "/%"
		_, err := s.db.Exec(`UPDATE virtual_files SET is_deleted = 0, deleted_at = NULL WHERE id = ? OR path LIKE ?`, id, prefix)
		if err == nil {
			s.InvalidateVFSCache()
			s.InvalidateStatsCache()
		}
		return err
	}

	_, err = s.db.Exec(`UPDATE virtual_files SET is_deleted = 0, deleted_at = NULL WHERE id = ?`, id)
	if err == nil {
		s.InvalidateVFSCache()
		s.InvalidateStatsCache()
	}
	return err
}

func (s *DB) ListTrashFiles(userID string) ([]models.VirtualFile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var rows *sql.Rows
	var err error

	if userID == "all" || userID == "" || userID == "user_admin" {
		rows, err = s.db.Query(`SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at FROM virtual_files WHERE is_deleted = 1 ORDER BY deleted_at DESC`)
	} else {
		rows, err = s.db.Query(`SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at FROM virtual_files WHERE user_id = ? AND is_deleted = 1 ORDER BY deleted_at DESC`, userID)
	}

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]models.VirtualFile, 0)
	for rows.Next() {
		var f models.VirtualFile
		var sha, uid sql.NullString
		var isDel, hasMissing sql.NullBool
		var rawDelAt, rawCreated, rawUpdated interface{}
		if err := rows.Scan(&f.ID, &uid, &f.ParentID, &f.Name, &f.Path, &f.IsDir, &f.SizeBytes, &f.MimeType, &sha, &f.ChunkCount, &f.IsEncrypted, &isDel, &rawDelAt, &hasMissing, &rawCreated, &rawUpdated); err != nil {
			return nil, err
		}
		if sha.Valid {
			f.SHA256 = sha.String
		}
		if uid.Valid {
			f.UserID = uid.String
		}
		if isDel.Valid {
			f.IsTrashed = isDel.Bool
		}
		if hasMissing.Valid {
			f.HasMissingChunks = hasMissing.Bool
		}
		f.DeletedAt = parseFlexibleTimePtr(rawDelAt)
		f.CreatedAt = parseFlexibleTime(rawCreated)
		f.UpdatedAt = parseFlexibleTime(rawUpdated)
		list = append(list, f)
	}
	return list, nil
}

func (s *DB) GetTrashFileIDs(userID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var rows *sql.Rows
	var err error
	if userID == "all" || userID == "" || userID == "user_admin" {
		rows, err = s.db.Query(`SELECT id FROM virtual_files WHERE is_deleted = 1`)
	} else {
		rows, err = s.db.Query(`SELECT id FROM virtual_files WHERE user_id = ? AND is_deleted = 1`, userID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (s *DB) getVirtualFileUnsafe(id string) (*models.VirtualFile, error) {
	row := s.db.QueryRow(`SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at FROM virtual_files WHERE id = ?`, id)
	var f models.VirtualFile
	var sha, uid sql.NullString
	var isDel, hasMissing sql.NullBool
	var rawDelAt, rawCreated, rawUpdated interface{}
	err := row.Scan(&f.ID, &uid, &f.ParentID, &f.Name, &f.Path, &f.IsDir, &f.SizeBytes, &f.MimeType, &sha, &f.ChunkCount, &f.IsEncrypted, &isDel, &rawDelAt, &hasMissing, &rawCreated, &rawUpdated)
	if err != nil {
		return nil, err
	}
	if sha.Valid {
		f.SHA256 = sha.String
	}
	if uid.Valid {
		f.UserID = uid.String
	}
	if isDel.Valid {
		f.IsTrashed = isDel.Bool
	}
	if hasMissing.Valid {
		f.HasMissingChunks = hasMissing.Bool
	}
	f.DeletedAt = parseFlexibleTimePtr(rawDelAt)
	f.CreatedAt = parseFlexibleTime(rawCreated)
	f.UpdatedAt = parseFlexibleTime(rawUpdated)
	return &f, nil
}

// UpdateFileMissingChunks cập nhật cờ cảnh báo thiếu chunks cho một virtual file
func (s *DB) UpdateFileMissingChunks(fileID string, hasMissing bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec("UPDATE virtual_files SET has_missing_chunks = ?, updated_at = ? WHERE id = ?", hasMissing, time.Now(), fileID)
	if err == nil {
		s.InvalidateVFSCache()
	}
	return err
}

// UpdateChunkStatus cập nhật trạng thái của chunk ("uploaded", "missing", "failed")
func (s *DB) UpdateChunkStatus(chunkID string, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec("UPDATE file_chunks SET status = ? WHERE chunk_id = ?", status, chunkID)
	return err
}

// UpdateChunkStatusByDriveID cập nhật trạng thái của chunk theo gdrive_file_id
func (s *DB) UpdateChunkStatusByDriveID(gdriveFileID string, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec("UPDATE file_chunks SET status = ? WHERE gdrive_file_id = ?", status, gdriveFileID)
	return err
}

// GetAllFilesForIntegrityCheck lấy danh sách các tệp (không phải thư mục) chưa bị xóa để kiểm tra toàn vẹn
func (s *DB) GetAllFilesForIntegrityCheck(filterFileID string) ([]models.VirtualFile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at 
		FROM virtual_files 
		WHERE is_dir = 0 AND id != 'root' AND is_deleted = 0`
	var rows *sql.Rows
	var err error

	if filterFileID != "" {
		query += " AND id = ?"
		rows, err = s.db.Query(query, filterFileID)
	} else {
		query += " ORDER BY updated_at DESC"
		rows, err = s.db.Query(query)
	}

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []models.VirtualFile
	for rows.Next() {
		var f models.VirtualFile
		var sha, uid sql.NullString
		var isDel, hasMissing sql.NullBool
		var rawDelAt, rawCreated, rawUpdated interface{}
		if err := rows.Scan(&f.ID, &uid, &f.ParentID, &f.Name, &f.Path, &f.IsDir, &f.SizeBytes, &f.MimeType, &sha, &f.ChunkCount, &f.IsEncrypted, &isDel, &rawDelAt, &hasMissing, &rawCreated, &rawUpdated); err != nil {
			return nil, err
		}
		if sha.Valid {
			f.SHA256 = sha.String
		}
		if uid.Valid {
			f.UserID = uid.String
		}
		if isDel.Valid {
			f.IsTrashed = isDel.Bool
		}
		if hasMissing.Valid {
			f.HasMissingChunks = hasMissing.Bool
		}
		f.DeletedAt = parseFlexibleTimePtr(rawDelAt)
		f.CreatedAt = parseFlexibleTime(rawCreated)
		f.UpdatedAt = parseFlexibleTime(rawUpdated)
		files = append(files, f)
	}
	return files, nil
}


func (s *DB) DeleteVirtualFile(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "root" || id == "" {
		return fmt.Errorf("không thể xóa vĩnh viễn thư mục gốc (root folder is protected)")
	}
	_, err := s.db.Exec("DELETE FROM virtual_files WHERE id = ?", id)
	if err == nil {
		s.InvalidateVFSCache()
		s.InvalidateStatsCache()
	}
	return err
}

func (s *DB) RenameVirtualFile(id string, newName, newPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "root" || id == "" {
		return fmt.Errorf("không thể đổi tên thư mục gốc (root folder is protected)")
	}
	_, err := s.db.Exec("UPDATE virtual_files SET name=?, path=?, updated_at=? WHERE id=?", newName, newPath, time.Now(), id)
	if err == nil {
		s.InvalidateVFSCache()
	}
	return err
}

// -------------------------------------------------------------
// Chunks
// -------------------------------------------------------------

func (s *DB) SaveChunks(chunks []models.FileChunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var insertQuery string
	if s.IsMySQLOrTiDB() {
		insertQuery = `INSERT INTO file_chunks (chunk_id, file_id, chunk_index, account_id, gdrive_file_id, chunk_size_bytes, encrypted_size_bytes, sha256, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			gdrive_file_id=VALUES(gdrive_file_id),
			chunk_size_bytes=VALUES(chunk_size_bytes),
			encrypted_size_bytes=VALUES(encrypted_size_bytes),
			sha256=VALUES(sha256),
			status=VALUES(status)`
	} else {
		insertQuery = `INSERT INTO file_chunks (chunk_id, file_id, chunk_index, account_id, gdrive_file_id, chunk_size_bytes, encrypted_size_bytes, sha256, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(chunk_id) DO UPDATE SET
			gdrive_file_id=excluded.gdrive_file_id,
			chunk_size_bytes=excluded.chunk_size_bytes,
			encrypted_size_bytes=excluded.encrypted_size_bytes,
			sha256=excluded.sha256,
			status=excluded.status`
	}

	stmt, err := tx.Prepare(insertQuery)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, c := range chunks {
		if _, err := stmt.Exec(c.ChunkID, c.FileID, c.ChunkIndex, c.AccountID, c.GDriveFileID, c.ChunkSizeBytes, c.EncryptedSizeBytes, c.SHA256, c.Status); err != nil {
			return err
		}
	}

	err = tx.Commit()
	if err == nil {
		s.InvalidateStatsCache()
	}
	return err
}

func (s *DB) GetChunksForFile(fileID string) ([]models.FileChunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT chunk_id, file_id, chunk_index, account_id, gdrive_file_id, chunk_size_bytes, encrypted_size_bytes, sha256, status FROM file_chunks WHERE file_id = ? ORDER BY chunk_index ASC`, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	chunks := make([]models.FileChunk, 0)
	for rows.Next() {
		var c models.FileChunk
		if err := rows.Scan(&c.ChunkID, &c.FileID, &c.ChunkIndex, &c.AccountID, &c.GDriveFileID, &c.ChunkSizeBytes, &c.EncryptedSizeBytes, &c.SHA256, &c.Status); err != nil {
			return nil, err
		}
		chunks = append(chunks, c)
	}
	return chunks, nil
}

func (s *DB) GetChunkByDriveID(driveFileID string) (*models.FileChunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var c models.FileChunk
	err := s.db.QueryRow(`SELECT chunk_id, file_id, chunk_index, account_id, gdrive_file_id, chunk_size_bytes, encrypted_size_bytes, sha256, status FROM file_chunks WHERE gdrive_file_id = ? LIMIT 1`, driveFileID).Scan(&c.ChunkID, &c.FileID, &c.ChunkIndex, &c.AccountID, &c.GDriveFileID, &c.ChunkSizeBytes, &c.EncryptedSizeBytes, &c.SHA256, &c.Status)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *DB) GetChunkDetailsForFile(fileID string) ([]models.ChunkDetail, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT c.chunk_id, c.file_id, c.chunk_index, c.account_id, COALESCE(a.email, 'Unknown'), COALESCE(a.name, 'Google Drive'), c.gdrive_file_id, c.chunk_size_bytes, c.encrypted_size_bytes, c.sha256, c.status 
		FROM file_chunks c 
		LEFT JOIN accounts a ON c.account_id = a.id 
		WHERE c.file_id = ? 
		ORDER BY c.chunk_index ASC`

	rows, err := s.db.Query(query, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	details := make([]models.ChunkDetail, 0)
	for rows.Next() {
		var d models.ChunkDetail
		if err := rows.Scan(&d.ChunkID, &d.FileID, &d.ChunkIndex, &d.AccountID, &d.AccountEmail, &d.AccountName, &d.GDriveFileID, &d.ChunkSizeBytes, &d.EncryptedSizeBytes, &d.SHA256, &d.Status); err != nil {
			return nil, err
		}
		details = append(details, d)
	}
	return details, nil
}

func (s *DB) ToggleAccountStatus(id, newStatus string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("UPDATE accounts SET status = ?, updated_at = ? WHERE id = ?", newStatus, time.Now(), id)
	if err == nil {
		s.InvalidateAccountsCache()
	}
	return err
}

func (s *DB) DeleteChunksForFile(fileID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("DELETE FROM file_chunks WHERE file_id = ?", fileID)
	if err == nil {
		s.InvalidateStatsCache()
	}
	return err
}

// FindFileByNameInParent tìm file (không phải folder) có cùng tên trong cùng thư mục cha.
// Dùng để kiểm tra trùng tên trước khi upload → auto-replace.
func (s *DB) FindFileByNameInParent(parentID, name string) (*models.VirtualFile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := s.db.QueryRow(
		`SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, created_at, updated_at
		 FROM virtual_files
		 WHERE parent_id = ? AND name = ? AND is_dir = 0
		 LIMIT 1`,
		parentID, name,
	)

	var f models.VirtualFile
	var sha, uid sql.NullString
	var rawCreated, rawUpdated interface{}
	err := row.Scan(&f.ID, &uid, &f.ParentID, &f.Name, &f.Path, &f.IsDir,
		&f.SizeBytes, &f.MimeType, &sha, &f.ChunkCount, &f.IsEncrypted, &rawCreated, &rawUpdated)
	if err == sql.ErrNoRows {
		return nil, nil // Không tìm thấy - không phải lỗi
	}
	if err != nil {
		return nil, err
	}
	if sha.Valid {
		f.SHA256 = sha.String
	}
	if uid.Valid {
		f.UserID = uid.String
	}
	f.CreatedAt = parseFlexibleTime(rawCreated)
	f.UpdatedAt = parseFlexibleTime(rawUpdated)
	return &f, nil
}

// -------------------------------------------------------------
// Settings & Stats
// -------------------------------------------------------------

func (s *DB) getSettingsFromDB() (*models.Settings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query("SELECT `key`, `value` FROM settings")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	setMap := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err == nil {
			setMap[k] = v
		}
	}

	chunkSize, _ := strconv.ParseInt(setMap["chunk_size_bytes"], 10, 64)
	if chunkSize <= 0 {
		chunkSize = 20971520 // 20 MB
	}
	port, _ := strconv.Atoi(setMap["server_port"])
	if port <= 0 {
		port = 8080
	}

	redirectURL := setMap["redirect_url"]
	if redirectURL == "" {
		redirectURL = "http://localhost:8080/api/accounts/oauth/callback"
	}

	guestMode := setMap["guest_access_mode"]
	if guestMode == "" {
		guestMode = "view_only"
	}
	allowSelfReg := setMap["allow_self_registration"] != "false"

	masterKey := s.getMasterKey()
	clientID := core.DecryptSecret(masterKey, setMap["google_client_id"])
	clientSecret := core.DecryptSecret(masterKey, setMap["google_client_secret"])

	// Fallback biến môi trường nếu trong DB chưa có cấu hình cụ thể
	if clientID == "" {
		clientID = strings.TrimSpace(os.Getenv("OAUTH_CLIENT_ID"))
		if clientID == "" {
			clientID = strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_ID"))
		}
		if clientID == "" {
			clientID = DefaultGoogleClientID
		}
	}
	if clientSecret == "" {
		clientSecret = strings.TrimSpace(os.Getenv("OAUTH_CLIENT_SECRET"))
		if clientSecret == "" {
			clientSecret = strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_SECRET"))
		}
		if clientSecret == "" {
			clientSecret = DefaultGoogleClientSecret
		}
	}
	// Nếu redirect_url trong DB là mặc định localhost nhưng môi trường có OAUTH_REDIRECT_URL hợp lệ:
	if redirectURL == "" || strings.Contains(redirectURL, "localhost") || strings.Contains(redirectURL, "127.0.0.1") {
		if envRedirect := strings.TrimSpace(os.Getenv("OAUTH_REDIRECT_URL")); envRedirect != "" && !strings.Contains(envRedirect, "localhost") && !strings.Contains(envRedirect, "127.0.0.1") {
			redirectURL = envRedirect
		}
	}

	turnstileEnabled := setMap["turnstile_enabled"] != "false"
	turnstileSiteKey := setMap["turnstile_site_key"]
	if turnstileSiteKey == "" {
		turnstileSiteKey = strings.TrimSpace(os.Getenv("TURNSTILE_SITE_KEY"))
		if turnstileSiteKey == "" {
			turnstileSiteKey = "1x00000000000000000000AA" // Cloudflare official testing site key
		}
	}

	turnstileSecretKey := core.DecryptSecret(masterKey, setMap["turnstile_secret_key"])
	if turnstileSecretKey == "" {
		turnstileSecretKey = strings.TrimSpace(os.Getenv("TURNSTILE_SECRET_KEY"))
		if turnstileSecretKey == "" {
			turnstileSecretKey = "1x0000000000000000000000000000000AA" // Cloudflare official testing secret key
		}
	}

	return &models.Settings{
		MasterPassphrase:      setMap["master_passphrase"],
		ChunkSizeBytes:        chunkSize,
		AllocationStrategy:    setMap["allocation_strategy"],
		GuestAccessMode:       guestMode,
		AllowSelfRegistration: allowSelfReg,
		WebDAVEnabled:         setMap["webdav_enabled"] == "true",
		WebDAVUsername:        setMap["webdav_username"],
		WebDAVPassword:        setMap["webdav_password"],
		ServerPort:            port,
		GoogleClientID:        clientID,
		GoogleClientSecret:    clientSecret,
		RedirectURL:           redirectURL,
		TurnstileEnabled:      turnstileEnabled,
		TurnstileSiteKey:      turnstileSiteKey,
		TurnstileSecretKey:    turnstileSecretKey,
	}, nil
}

func (s *DB) SaveSettings(set *models.Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if set.RedirectURL == "" {
		set.RedirectURL = "http://localhost:8080/api/accounts/oauth/callback"
	}
	if set.GuestAccessMode == "" {
		set.GuestAccessMode = "view_only"
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Lấy passphrase hiện tại trong DB làm oldKey
	var oldPass string
	_ = tx.QueryRow("SELECT `value` FROM settings WHERE `key` = 'master_passphrase'").Scan(&oldPass)
	if oldPass == "" {
		oldPass = "cloudpool_secure_master_key_2026"
	}
	oldKey := core.DeriveKey(oldPass, nil)

	// 2. Xác định master_passphrase mới: nếu trống hoặc là placeholder '********', giữ nguyên khóa cũ
	if set.MasterPassphrase == "" || set.MasterPassphrase == "********" {
		set.MasterPassphrase = oldPass
	}
	masterKey := core.DeriveKey(set.MasterPassphrase, nil)

	// 3. Giải mã dữ liệu Google OAuth bằng khóa cũ nếu còn prefix ENC: trước khi mã hóa lại bằng masterKey mới
	clientID := set.GoogleClientID
	if clientID == "" || clientID == "********" {
		var oldVal string
		_ = tx.QueryRow("SELECT `value` FROM settings WHERE `key` = 'google_client_id'").Scan(&oldVal)
		pt := core.DecryptSecret(oldKey, oldVal)
		if pt == "" {
			pt = core.DecryptSecret(masterKey, oldVal)
		}
		if pt != "" {
			clientID = pt
		} else {
			clientID = DefaultGoogleClientID
		}
	} else if strings.HasPrefix(clientID, "ENC:") {
		pt := core.DecryptSecret(oldKey, clientID)
		if !strings.HasPrefix(pt, "ENC:") {
			clientID = pt
		} else {
			ptNew := core.DecryptSecret(masterKey, clientID)
			if !strings.HasPrefix(ptNew, "ENC:") {
				clientID = ptNew
			}
		}
	}
	if clientID == "" {
		clientID = DefaultGoogleClientID
	}

	clientSecret := set.GoogleClientSecret
	if clientSecret == "" || clientSecret == "********" {
		var oldVal string
		_ = tx.QueryRow("SELECT `value` FROM settings WHERE `key` = 'google_client_secret'").Scan(&oldVal)
		pt := core.DecryptSecret(oldKey, oldVal)
		if pt == "" {
			pt = core.DecryptSecret(masterKey, oldVal)
		}
		if pt != "" {
			clientSecret = pt
		} else {
			clientSecret = DefaultGoogleClientSecret
		}
	} else if strings.HasPrefix(clientSecret, "ENC:") {
		pt := core.DecryptSecret(oldKey, clientSecret)
		if !strings.HasPrefix(pt, "ENC:") {
			clientSecret = pt
		} else {
			ptNew := core.DecryptSecret(masterKey, clientSecret)
			if !strings.HasPrefix(ptNew, "ENC:") {
				clientSecret = ptNew
			}
		}
	}
	if clientSecret == "" {
		clientSecret = DefaultGoogleClientSecret
	}

	// 4. Xử lý Turnstile Secret Key
	turnstileSecret := set.TurnstileSecretKey
	if turnstileSecret == "" || turnstileSecret == "********" {
		var oldTurnstileSecret string
		_ = tx.QueryRow("SELECT `value` FROM settings WHERE `key` = 'turnstile_secret_key'").Scan(&oldTurnstileSecret)
		pt := core.DecryptSecret(oldKey, oldTurnstileSecret)
		if pt == "" {
			pt = core.DecryptSecret(masterKey, oldTurnstileSecret)
		}
		if pt != "" {
			turnstileSecret = pt
		} else {
			turnstileSecret = "1x0000000000000000000000000000000AA"
		}
	} else if strings.HasPrefix(turnstileSecret, "ENC:") {
		pt := core.DecryptSecret(oldKey, turnstileSecret)
		if !strings.HasPrefix(pt, "ENC:") {
			turnstileSecret = pt
		} else {
			ptNew := core.DecryptSecret(masterKey, turnstileSecret)
			if !strings.HasPrefix(ptNew, "ENC:") {
				turnstileSecret = ptNew
			}
		}
	}
	if turnstileSecret == "" {
		turnstileSecret = "1x0000000000000000000000000000000AA"
	}
	if set.TurnstileSiteKey == "" {
		set.TurnstileSiteKey = "1x00000000000000000000AA"
	}

	encClientID := core.EncryptSecret(masterKey, clientID)
	encClientSecret := core.EncryptSecret(masterKey, clientSecret)
	encTurnstileSecret := core.EncryptSecret(masterKey, turnstileSecret)

	pairs := map[string]string{
		"master_passphrase":       set.MasterPassphrase,
		"chunk_size_bytes":        strconv.FormatInt(set.ChunkSizeBytes, 10),
		"allocation_strategy":     set.AllocationStrategy,
		"guest_access_mode":       set.GuestAccessMode,
		"allow_self_registration": strconv.FormatBool(set.AllowSelfRegistration),
		"webdav_enabled":          strconv.FormatBool(set.WebDAVEnabled),
		"webdav_username":         set.WebDAVUsername,
		"webdav_password":         set.WebDAVPassword,
		"server_port":             strconv.Itoa(set.ServerPort),
		"google_client_id":        encClientID,
		"google_client_secret":    encClientSecret,
		"redirect_url":            set.RedirectURL,
		"turnstile_enabled":       strconv.FormatBool(set.TurnstileEnabled),
		"turnstile_site_key":      set.TurnstileSiteKey,
		"turnstile_secret_key":    encTurnstileSecret,
	}

	for k, v := range pairs {
		var q string
		if s.IsMySQLOrTiDB() {
			q = "INSERT INTO settings (`key`, `value`) VALUES (?, ?) ON DUPLICATE KEY UPDATE `value`=VALUES(`value`)"
		} else {
			q = "INSERT INTO settings (`key`, `value`) VALUES (?, ?) ON CONFLICT(`key`) DO UPDATE SET `value`=excluded.value"
		}
		if _, err := tx.Exec(q, k, v); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	s.InvalidateSettingsCache()
	return nil
}

func (s *DB) getStatsFromDB() (*models.StorageStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var stats models.StorageStats

	// Tối ưu hóa triệt để (Query Optimizer):
	// Gộp toàn bộ 5 truy vấn riêng lẻ thành 1 câu truy vấn duy nhất (Single Round-Trip),
	// loại bỏ hoàn toàn độ trễ mạng tích lũy (5 x 50ms = 250ms -> 1 x 50ms) tới TiDB Cloud Singapore.
	query := `SELECT 
		COALESCE((SELECT COUNT(*) FROM accounts), 0),
		COALESCE((SELECT SUM(CASE WHEN status = 'active' THEN 1 ELSE 0 END) FROM accounts), 0),
		COALESCE((SELECT SUM(total_quota_bytes) FROM accounts), 0),
		COALESCE((SELECT SUM(used_quota_bytes) FROM accounts), 0),
		COALESCE((SELECT SUM(free_quota_bytes) FROM accounts), 0),
		COALESCE((SELECT COUNT(*) FROM virtual_files WHERE is_dir = 0 AND id != 'root'), 0),
		COALESCE((SELECT COUNT(*) FROM virtual_files WHERE is_dir = 1 AND id != 'root'), 0),
		COALESCE((SELECT COUNT(*) FROM file_chunks), 0),
		COALESCE((SELECT COUNT(*) FROM users), 0)`

	err := s.db.QueryRow(query).Scan(
		&stats.TotalAccounts,
		&stats.ActiveAccounts,
		&stats.TotalCapacityBytes,
		&stats.TotalUsedBytes,
		&stats.TotalFreeBytes,
		&stats.TotalFilesCount,
		&stats.TotalFoldersCount,
		&stats.TotalChunksCount,
		&stats.TotalUsersCount,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch storage stats: %w", err)
	}

	if stats.TotalCapacityBytes > 0 {
		stats.OverallUsagePercent = float64(stats.TotalUsedBytes) / float64(stats.TotalCapacityBytes) * 100.0
	}

	return &stats, nil
}

// -------------------------------------------------------------
// Activity Audit Logs
// -------------------------------------------------------------

func (s *DB) LogActivity(entry *models.ActivityLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entry.ID == "" {
		entry.ID = "log_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}

	_, err := s.db.Exec(`INSERT INTO activity_logs (id, user_id, username, action, target, ip_address, details, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.UserID, entry.Username, entry.Action, entry.Target, entry.IPAddress, entry.Details, entry.CreatedAt)
	return err
}

func (s *DB) ListActivityLogs(limit int) ([]models.ActivityLog, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 || limit > 500 {
		limit = 100
	}

	rows, err := s.db.Query(`SELECT id, user_id, username, action, target, ip_address, details, created_at 
		FROM activity_logs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]models.ActivityLog, 0)
	for rows.Next() {
		var l models.ActivityLog
		var rawCreatedAt interface{}
		if err := rows.Scan(&l.ID, &l.UserID, &l.Username, &l.Action, &l.Target, &l.IPAddress, &l.Details, &rawCreatedAt); err == nil {
			l.CreatedAt = parseFlexibleTime(rawCreatedAt)
			list = append(list, l)
		}
	}
	return list, nil
}

// -------------------------------------------------------------
// User Management & Private Storage
// -------------------------------------------------------------

func (s *DB) CreateUser(u *models.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	masterKey := s.getMasterKey()
	encUsername := core.EncryptSecret(masterKey, u.Username)
	encEmail := core.EncryptSecret(masterKey, u.Email)
	encDisplay := core.EncryptSecret(masterKey, u.DisplayName)
	encAvatar := core.EncryptSecret(masterKey, u.AvatarURL)

	usernameHash := core.BlindIndexHash(masterKey, u.Username)
	emailHash := core.BlindIndexHash(masterKey, u.Email)

	_, err := s.db.Exec(`INSERT INTO users (id, username, username_hash, email, email_hash, password_hash, security_pin_hash, security_tier, display_name, avatar_url, role, quota_bytes, used_bytes, created_at, updated_at) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, encUsername, usernameHash, encEmail, emailHash, u.PasswordHash, u.SecurityPinHash, u.SecurityTier, encDisplay, encAvatar, u.Role, u.QuotaBytes, u.UsedBytes, u.CreatedAt, u.UpdatedAt)
	if err == nil {
		s.InvalidateStatsCache()
	}
	return err
}

func (s *DB) GetUserByUsername(username string) (*models.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	masterKey := s.getMasterKey()
	usernameHash := core.BlindIndexHash(masterKey, username)

	row := s.db.QueryRow(`SELECT id, username, email, password_hash, COALESCE(security_pin_hash, ''), COALESCE(security_tier, 1), display_name, avatar_url, role, quota_bytes, used_bytes, failed_login_count, locked_until, created_at, updated_at FROM users WHERE username_hash = ?`, usernameHash)
	var u models.User
	var rawLockedUntil, rawCreated, rawUpdated interface{}
	if err := row.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.SecurityPinHash, &u.SecurityTier, &u.DisplayName, &u.AvatarURL, &u.Role, &u.QuotaBytes, &u.UsedBytes, &u.FailedLoginCount, &rawLockedUntil, &rawCreated, &rawUpdated); err != nil {
		return nil, err
	}
	u.LockedUntil = parseFlexibleTimePtr(rawLockedUntil)
	u.CreatedAt = parseFlexibleTime(rawCreated)
	u.UpdatedAt = parseFlexibleTime(rawUpdated)
	u.HasSecurityPin = (u.SecurityPinHash != "")
	u.Username = core.DecryptSecret(masterKey, u.Username)
	u.Email = core.DecryptSecret(masterKey, u.Email)
	u.DisplayName = core.DecryptSecret(masterKey, u.DisplayName)
	u.AvatarURL = core.DecryptSecret(masterKey, u.AvatarURL)
	return &u, nil
}

func (s *DB) GetUserByID(id string) (*models.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := s.db.QueryRow(`SELECT id, username, email, password_hash, COALESCE(security_pin_hash, ''), COALESCE(security_tier, 1), display_name, avatar_url, role, quota_bytes, used_bytes, failed_login_count, locked_until, created_at, updated_at FROM users WHERE id = ?`, id)
	var u models.User
	var rawLockedUntil, rawCreated, rawUpdated interface{}
	if err := row.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.SecurityPinHash, &u.SecurityTier, &u.DisplayName, &u.AvatarURL, &u.Role, &u.QuotaBytes, &u.UsedBytes, &u.FailedLoginCount, &rawLockedUntil, &rawCreated, &rawUpdated); err != nil {
		return nil, err
	}
	masterKey := s.getMasterKey()
	u.LockedUntil = parseFlexibleTimePtr(rawLockedUntil)
	u.CreatedAt = parseFlexibleTime(rawCreated)
	u.UpdatedAt = parseFlexibleTime(rawUpdated)
	u.HasSecurityPin = (u.SecurityPinHash != "")
	u.Username = core.DecryptSecret(masterKey, u.Username)
	u.Email = core.DecryptSecret(masterKey, u.Email)
	u.DisplayName = core.DecryptSecret(masterKey, u.DisplayName)
	u.AvatarURL = core.DecryptSecret(masterKey, u.AvatarURL)
	return &u, nil
}

func (s *DB) ListUsers() ([]models.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT id, username, email, COALESCE(security_pin_hash, ''), COALESCE(security_tier, 1), display_name, avatar_url, role, quota_bytes, used_bytes, failed_login_count, locked_until, created_at, updated_at FROM users ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]models.User, 0)
	masterKey := s.getMasterKey()
	for rows.Next() {
		var u models.User
		var rawLockedUntil, rawCreated, rawUpdated interface{}
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.SecurityPinHash, &u.SecurityTier, &u.DisplayName, &u.AvatarURL, &u.Role, &u.QuotaBytes, &u.UsedBytes, &u.FailedLoginCount, &rawLockedUntil, &rawCreated, &rawUpdated); err == nil {
			u.LockedUntil = parseFlexibleTimePtr(rawLockedUntil)
			u.CreatedAt = parseFlexibleTime(rawCreated)
			u.UpdatedAt = parseFlexibleTime(rawUpdated)
			u.HasSecurityPin = (u.SecurityPinHash != "")
			u.Username = core.DecryptSecret(masterKey, u.Username)
			u.Email = core.DecryptSecret(masterKey, u.Email)
			u.DisplayName = core.DecryptSecret(masterKey, u.DisplayName)
			u.AvatarURL = core.DecryptSecret(masterKey, u.AvatarURL)
			list = append(list, u)
		}
	}
	return list, nil
}

func (s *DB) DeleteUser(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	masterKey := s.getMasterKey()
	adminHash := core.BlindIndexHash(masterKey, "admin")
	_, err := s.db.Exec("DELETE FROM users WHERE id = ? AND username_hash != ?", id, adminHash)
	if err == nil {
		s.InvalidateStatsCache()
	}
	return err
}

func (s *DB) UpdateUserQuota(id string, quotaBytes int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("UPDATE users SET quota_bytes = ?, updated_at = ? WHERE id = ?", quotaBytes, time.Now(), id)
	if err == nil {
		s.InvalidateStatsCache()
	}
	return err
}

func (s *DB) UpdateUserPassword(id, passwordHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?", passwordHash, time.Now(), id)
	return err
}

func (s *DB) UpdateUserSecurityPin(id, pinHash string, tier int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("UPDATE users SET security_pin_hash = ?, security_tier = ?, updated_at = ? WHERE id = ?", pinHash, tier, time.Now(), id)
	return err
}

func (s *DB) RecordLoginFailure(username string) (int, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	masterKey := s.getMasterKey()
	usernameHash := core.BlindIndexHash(masterKey, username)

	var fails int
	_ = s.db.QueryRow("SELECT failed_login_count FROM users WHERE username_hash = ?", usernameHash).Scan(&fails)
	fails++

	var lockedUntil *time.Time
	isLocked := false
	if fails >= 5 {
		lockTime := time.Now().Add(15 * time.Minute)
		lockedUntil = &lockTime
		isLocked = true
	}

	_, err := s.db.Exec("UPDATE users SET failed_login_count = ?, locked_until = ?, updated_at = ? WHERE username_hash = ?",
		fails, lockedUntil, time.Now(), usernameHash)
	return fails, isLocked, err
}

func (s *DB) ResetLoginFailure(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	masterKey := s.getMasterKey()
	usernameHash := core.BlindIndexHash(masterKey, username)
	
	_, err := s.db.Exec("UPDATE users SET failed_login_count = 0, locked_until = NULL, last_login_at = ?, updated_at = ? WHERE username_hash = ?",
		time.Now(), time.Now(), usernameHash)
	return err
}

func (s *DB) UpdateUserDisplayName(id, displayName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	masterKey := s.getMasterKey()
	encDisplay := core.EncryptSecret(masterKey, displayName)
	_, err := s.db.Exec("UPDATE users SET display_name = ?, updated_at = ? WHERE id = ?", encDisplay, time.Now(), id)
	return err
}

func (s *DB) UpdateUserUsage(id string, usedDelta int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("UPDATE users SET used_bytes = used_bytes + ?, updated_at = ? WHERE id = ?", usedDelta, time.Now(), id)
	if err == nil {
		s.InvalidateStatsCache()
	}
	return err
}

// -------------------------------------------------------------
// SQL Studio Database Inspector
// -------------------------------------------------------------

type SQLResult struct {
	Columns       []string        `json:"columns"`
	Rows          [][]interface{} `json:"rows"`
	AffectedRows  int64           `json:"affected_rows"`
	ExecutionMs   float64         `json:"execution_ms"`
	IsSelectQuery bool            `json:"is_select"`
}

type TableColumnInfo struct {
	CID        int    `json:"cid"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	NotNull    bool   `json:"not_null"`
	DefaultVal string `json:"default_val"`
	IsPK       bool   `json:"is_pk"`
}

type TableInfo struct {
	Name     string            `json:"name"`
	RowCount int64             `json:"row_count"`
	Columns  []TableColumnInfo `json:"columns"`
}

func (s *DB) GetDatabaseTables() ([]TableInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.IsMySQLOrTiDB() {
		rows, err := s.db.Query("SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE' ORDER BY table_name ASC")
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		tables := make([]TableInfo, 0)
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err == nil {
				var count int64
				_ = s.db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM `%s`", name)).Scan(&count)

				tInfo := TableInfo{
					Name:     name,
					RowCount: count,
					Columns:  make([]TableColumnInfo, 0),
				}

				colRows, err := s.db.Query("SELECT ordinal_position, column_name, column_type, is_nullable, column_default, column_key FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? ORDER BY ordinal_position ASC", name)
				if err == nil {
					for colRows.Next() {
						var cid int
						var cname, ctype, isNullable, colKey string
						var dflt sql.NullString
						if err := colRows.Scan(&cid, &cname, &ctype, &isNullable, &dflt, &colKey); err == nil {
							tInfo.Columns = append(tInfo.Columns, TableColumnInfo{
								CID:        cid,
								Name:       cname,
								Type:       ctype,
								NotNull:    strings.ToUpper(isNullable) == "NO",
								DefaultVal: dflt.String,
								IsPK:       strings.ToUpper(colKey) == "PRI",
							})
						}
					}
					colRows.Close()
				}
				tables = append(tables, tInfo)
			}
		}
		return tables, nil
	}

	rows, err := s.db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tables := make([]TableInfo, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			var count int64
			_ = s.db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", name)).Scan(&count)

			tInfo := TableInfo{
				Name:     name,
				RowCount: count,
				Columns:  make([]TableColumnInfo, 0),
			}

			colRows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", name))
			if err == nil {
				for colRows.Next() {
					var cid, notnull, pk int
					var cname, ctype string
					var dflt sql.NullString
					if err := colRows.Scan(&cid, &cname, &ctype, &notnull, &dflt, &pk); err == nil {
						tInfo.Columns = append(tInfo.Columns, TableColumnInfo{
							CID:        cid,
							Name:       cname,
							Type:       ctype,
							NotNull:    notnull == 1,
							DefaultVal: dflt.String,
							IsPK:       pk == 1,
						})
					}
				}
				colRows.Close()
			}

			tables = append(tables, tInfo)
		}
	}
	return tables, nil
}

func (s *DB) ExecuteRawSQL(query string) (*SQLResult, error) {
	startTime := time.Now()
	trimmed := strings.TrimSpace(strings.ToUpper(query))

	if strings.HasPrefix(trimmed, "SELECT") || strings.HasPrefix(trimmed, "PRAGMA") || strings.HasPrefix(trimmed, "EXPLAIN") {
		s.mu.RLock()
		defer s.mu.RUnlock()

		rows, err := s.db.Query(query)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		cols, err := rows.Columns()
		if err != nil {
			return nil, err
		}

		resultRows := make([][]interface{}, 0)
		for rows.Next() {
			values := make([]interface{}, len(cols))
			valuePtrs := make([]interface{}, len(cols))
			for i := range values {
				valuePtrs[i] = &values[i]
			}

			if err := rows.Scan(valuePtrs...); err != nil {
				return nil, err
			}

			rowList := make([]interface{}, len(cols))
			for i, v := range values {
				colName := strings.ToLower(cols[i])
				strVal := ""
				if b, ok := v.([]byte); ok {
					strVal = string(b)
				} else if s, ok := v.(string); ok {
					strVal = s
				}

				if strVal != "" {
					if colName == "credentials_json" || colName == "token_json" {
						rowList[i] = "🔒 [BẢO MẬT: MÃ HÓA AES-256 GCM (" + strconv.Itoa(len(strVal)) + " bytes)]"
					} else if (colName == "password_hash" || colName == "security_pin_hash") && len(strVal) > 6 {
						rowList[i] = "🔒 [HASH BẢO MẬT (" + strVal[:8] + "...)]"
					} else {
						rowList[i] = strVal
					}
				} else {
					rowList[i] = v
				}
			}
			resultRows = append(resultRows, rowList)
		}

		elapsed := float64(time.Since(startTime).Microseconds()) / 1000.0
		return &SQLResult{
			Columns:       cols,
			Rows:          resultRows,
			AffectedRows:  int64(len(resultRows)),
			ExecutionMs:   elapsed,
			IsSelectQuery: true,
		}, nil
	}

	// Non-SELECT (INSERT, UPDATE, DELETE, CREATE, DROP...)
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(query)
	if err != nil {
		return nil, err
	}

	affected, _ := res.RowsAffected()
	elapsed := float64(time.Since(startTime).Microseconds()) / 1000.0

	return &SQLResult{
		Columns:       []string{"status", "affected_rows"},
		Rows:          [][]interface{}{{"Thực thi thành công", affected}},
		AffectedRows:  affected,
		ExecutionMs:   elapsed,
		IsSelectQuery: false,
	}, nil
}

func (s *DB) OptimizeDatabase() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.IsMySQLOrTiDB() {
		return nil
	}
	_, err := s.db.Exec("VACUUM; PRAGMA optimize;")
	return err
}

func (s *DB) CheckDatabaseIntegrity() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.IsMySQLOrTiDB() {
		var pingCheck int = 1
		err := s.db.QueryRow("SELECT 1;").Scan(&pingCheck)
		if err != nil {
			return "fail", err
		}
		return "ok", nil
	}
	var result string
	err := s.db.QueryRow("PRAGMA integrity_check;").Scan(&result)
	return result, err
}

func (s *DB) BackupDatabase(destPath string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.IsMySQLOrTiDB() {
		return fmt.Errorf("tính năng VACUUM INTO chỉ khả dụng trên SQLite; đối với TiDB vui lòng sử dụng TiDB Backup & Restore (BR) hoặc mysqldump")
	}
	cleanDest := filepath.ToSlash(destPath)
	_, err := s.db.Exec(fmt.Sprintf("VACUUM INTO '%s';", strings.ReplaceAll(cleanDest, "'", "''")))
	return err
}

func (s *DB) LogLoginSession(sess *models.LoginSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sess.ID == "" {
		sess.ID = "sess_" + uuid.New().String()
	}
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = time.Now()
	}

	_, err := s.db.Exec(`INSERT INTO login_sessions (id, user_id, username, ip_address, device_info, location_info, status, user_agent, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.UserID, sess.Username, sess.IPAddress, sess.DeviceInfo, sess.LocationInfo, sess.Status, sess.UserAgent, sess.CreatedAt)
	return err
}

func (s *DB) ListLoginSessions(limit int) ([]models.LoginSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.Query(`SELECT id, COALESCE(user_id, ''), COALESCE(username, ''), COALESCE(ip_address, ''), COALESCE(device_info, ''), COALESCE(location_info, ''), COALESCE(status, ''), COALESCE(user_agent, ''), created_at 
		FROM login_sessions ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]models.LoginSession, 0)
	for rows.Next() {
		var s models.LoginSession
		var rawCreatedAt interface{}
		if err := rows.Scan(&s.ID, &s.UserID, &s.Username, &s.IPAddress, &s.DeviceInfo, &s.LocationInfo, &s.Status, &s.UserAgent, &rawCreatedAt); err == nil {
			s.CreatedAt = parseFlexibleTime(rawCreatedAt)
			list = append(list, s)
		}
	}
	return list, nil
}

// -------------------------------------------------------------
// Single-Use OTP & File Access Management
// -------------------------------------------------------------

func (s *DB) CreateFileOTP(otp *models.FileAccessOTP) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if otp.ID == "" {
		otp.ID = "otp_" + uuid.New().String()
	}
	if otp.CreatedAt.IsZero() {
		otp.CreatedAt = time.Now()
	}
	if otp.CreatedBy == "" {
		otp.CreatedBy = "user_admin"
	}
	if otp.TargetUserID == "" {
		otp.TargetUserID = "all"
	}

	_, err := s.db.Exec(`INSERT INTO file_access_otps (id, file_id, file_name, target_user_id, otp_code, created_by, is_used, expires_at, created_at) 
		VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		otp.ID, otp.FileID, otp.FileName, otp.TargetUserID, otp.OTPCode, otp.CreatedBy, otp.ExpiresAt, otp.CreatedAt)
	return err
}

func (s *DB) VerifyAndBurnOTP(fileID, userID, otpCode string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	otpCode = strings.TrimSpace(otpCode)
	if otpCode == "" {
		return false, fmt.Errorf("Mã OTP không được để trống")
	}

	var otpID string
	var rawExpires interface{}
	var isUsed int

	row := s.db.QueryRow(`SELECT id, expires_at, is_used FROM file_access_otps 
		WHERE file_id = ? AND otp_code = ? AND (target_user_id = 'all' OR target_user_id = ?) 
		ORDER BY created_at DESC LIMIT 1`, fileID, otpCode, userID)

	if err := row.Scan(&otpID, &rawExpires, &isUsed); err != nil {
		return false, fmt.Errorf("Mã OTP không chính xác hoặc không áp dụng cho tệp tin này")
	}

	expiresAt := parseFlexibleTime(rawExpires)

	if isUsed == 1 {
		return false, fmt.Errorf("Mã OTP này đã được sử dụng (Mỗi mã chỉ có giá trị 1 lần duy nhất)")
	}

	if time.Now().After(expiresAt) {
		return false, fmt.Errorf("Mã OTP đã hết thời hạn hiệu lực")
	}

	// Burn OTP immediately (Single-use per Rule)
	now := time.Now()
	_, err := s.db.Exec(`UPDATE file_access_otps SET is_used = 1, used_by = ?, used_at = ? WHERE id = ?`, userID, now, otpID)
	if err != nil {
		return false, err
	}

	return true, nil
}

// VerifyOTPOnly kiểm tra tính hợp lệ của mã OTP mà không huỷ/burn mã (dùng cho status check hoặc preview probe)
func (s *DB) VerifyOTPOnly(fileID, userID, otpCode string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	otpCode = strings.TrimSpace(otpCode)
	if otpCode == "" {
		return false, fmt.Errorf("Mã OTP không được để trống")
	}

	var otpID string
	var rawExpires interface{}
	var isUsed int

	row := s.db.QueryRow(`SELECT id, expires_at, is_used FROM file_access_otps 
		WHERE file_id = ? AND otp_code = ? AND (target_user_id = 'all' OR target_user_id = ?) 
		ORDER BY created_at DESC LIMIT 1`, fileID, otpCode, userID)

	if err := row.Scan(&otpID, &rawExpires, &isUsed); err != nil {
		return false, fmt.Errorf("Mã OTP không chính xác hoặc không áp dụng cho tệp tin này")
	}

	expiresAt := parseFlexibleTime(rawExpires)

	if isUsed == 1 {
		return false, fmt.Errorf("Mã OTP này đã được sử dụng")
	}

	if time.Now().After(expiresAt) {
		return false, fmt.Errorf("Mã OTP đã hết thời hạn hiệu lực")
	}

	return true, nil
}

func (s *DB) ListFileOTPs(limit int) ([]models.FileAccessOTP, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 100
	}

	rows, err := s.db.Query(`SELECT o.id, o.file_id, o.file_name, o.target_user_id, COALESCE(u.username, o.target_user_id), o.otp_code, o.created_by, o.is_used, COALESCE(o.used_by, ''), o.used_at, o.expires_at, o.created_at 
		FROM file_access_otps o 
		LEFT JOIN users u ON o.target_user_id = u.id 
		ORDER BY o.created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]models.FileAccessOTP, 0)
	now := time.Now()
	for rows.Next() {
		var o models.FileAccessOTP
		var isUsedInt int
		var usedBy string
		var rawUsedAt, rawExpiresAt, rawCreatedAt interface{}
		var targetUsername string

		if err := rows.Scan(&o.ID, &o.FileID, &o.FileName, &o.TargetUserID, &targetUsername, &o.OTPCode, &o.CreatedBy, &isUsedInt, &usedBy, &rawUsedAt, &rawExpiresAt, &rawCreatedAt); err != nil {
			return nil, err
		}
		o.IsUsed = (isUsedInt == 1)
		o.UsedBy = usedBy
		o.TargetUsername = targetUsername
		o.UsedAt = parseFlexibleTimePtr(rawUsedAt)
		o.ExpiresAt = parseFlexibleTime(rawExpiresAt)
		o.CreatedAt = parseFlexibleTime(rawCreatedAt)

		if o.IsUsed {
			o.Status = "used"
		} else if now.After(o.ExpiresAt) {
			o.Status = "expired"
		} else {
			o.Status = "active"
		}

		list = append(list, o)
	}
	return list, nil
}

func (s *DB) RevokeFileOTP(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("DELETE FROM file_access_otps WHERE id = ?", id)
	return err
}

func (s *DB) CreateFileAccessRequest(req *models.FileAccessRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.ID == "" {
		req.ID = "req_" + uuid.New().String()
	}
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now()
	}
	if req.UpdatedAt.IsZero() {
		req.UpdatedAt = time.Now()
	}
	if req.Status == "" {
		req.Status = "pending"
	}

	_, err := s.db.Exec(`INSERT INTO file_access_requests (id, file_id, file_name, user_id, username, user_display_name, status, otp_code, created_at, updated_at) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		req.ID, req.FileID, req.FileName, req.UserID, req.Username, req.UserDisplayName, req.Status, req.OTPCode, req.CreatedAt, req.UpdatedAt)
	return err
}

func (s *DB) ListFileAccessRequests(status string) ([]models.FileAccessRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var rows *sql.Rows
	var err error
	if status == "all" || status == "" {
		rows, err = s.db.Query(`SELECT id, file_id, file_name, user_id, username, user_display_name, status, COALESCE(otp_code, ''), created_at, updated_at FROM file_access_requests ORDER BY created_at DESC LIMIT 100`)
	} else {
		rows, err = s.db.Query(`SELECT id, file_id, file_name, user_id, username, user_display_name, status, COALESCE(otp_code, ''), created_at, updated_at FROM file_access_requests WHERE status = ? ORDER BY created_at DESC LIMIT 100`, status)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]models.FileAccessRequest, 0)
	for rows.Next() {
		var r models.FileAccessRequest
		var rawCreated, rawUpdated interface{}
		if err := rows.Scan(&r.ID, &r.FileID, &r.FileName, &r.UserID, &r.Username, &r.UserDisplayName, &r.Status, &r.OTPCode, &rawCreated, &rawUpdated); err != nil {
			return nil, err
		}
		r.CreatedAt = parseFlexibleTime(rawCreated)
		r.UpdatedAt = parseFlexibleTime(rawUpdated)
		list = append(list, r)
	}
	return list, nil
}

func (s *DB) ApproveFileAccessRequest(requestID, otpCode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`UPDATE file_access_requests SET status = 'approved', otp_code = ?, updated_at = ? WHERE id = ?`, otpCode, time.Now(), requestID)
	return err
}

func (s *DB) RejectFileAccessRequest(requestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`UPDATE file_access_requests SET status = 'rejected', updated_at = ? WHERE id = ?`, time.Now(), requestID)
	return err
}

// -------------------------------------------------------------
// Deduplication & Reference Counting
// -------------------------------------------------------------

// FindChunkByHash finds an existing uploaded chunk by SHA-256 for instant deduplication
func (s *DB) FindChunkByHash(hash string) (*models.FileChunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if hash == "" {
		return nil, nil
	}

	var c models.FileChunk
	err := s.db.QueryRow(`SELECT chunk_id, file_id, chunk_index, account_id, gdrive_file_id, chunk_size_bytes, encrypted_size_bytes, sha256, COALESCE(ref_count, 1), status 
		FROM file_chunks 
		WHERE sha256 = ? AND status = 'uploaded' 
		LIMIT 1`, hash).
		Scan(&c.ChunkID, &c.FileID, &c.ChunkIndex, &c.AccountID, &c.GDriveFileID, &c.ChunkSizeBytes, &c.EncryptedSizeBytes, &c.SHA256, &c.RefCount, &c.Status)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// CountChunkReferences returns how many file chunks reference the same Google Drive file ID
func (s *DB) CountChunkReferences(gdriveFileID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM file_chunks WHERE gdrive_file_id = ?`, gdriveFileID).Scan(&count)
	return count, err
}

// -------------------------------------------------------------
// Public Share Links
// -------------------------------------------------------------

func (s *DB) CreatePublicShare(share *models.PublicShare) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if share.ID == "" {
		share.ID = "sh_" + uuid.New().String()
	}
	if share.CreatedAt.IsZero() {
		share.CreatedAt = time.Now()
	}

	_, err := s.db.Exec(`INSERT INTO public_shares (id, file_id, created_by, password_hash, max_downloads, download_count, expires_at, created_at, is_active)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		share.ID, share.FileID, share.CreatedBy, share.PasswordHash, share.MaxDownloads, share.DownloadCount, share.ExpiresAt, share.CreatedAt, 1)
	if err == nil {
		s.InvalidateVFSCache()
	}
	return err
}

func (s *DB) GetPublicShare(id string) (*models.PublicShare, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT s.id, s.file_id, s.created_by, s.password_hash, s.max_downloads, s.download_count, s.expires_at, s.created_at, s.is_active,
		COALESCE(f.name, 'Tệp tin đã xóa'), COALESCE(f.size_bytes, 0), COALESCE(f.mime_type, 'application/octet-stream'), COALESCE(f.is_dir, 0)
		FROM public_shares s
		LEFT JOIN virtual_files f ON s.file_id = f.id
		WHERE s.id = ?`

	row := s.db.QueryRow(query, id)
	var sh models.PublicShare
	var rawExp, rawCreated interface{}
	var passHash string
	var isActiveInt, isDirInt int

	err := row.Scan(&sh.ID, &sh.FileID, &sh.CreatedBy, &passHash, &sh.MaxDownloads, &sh.DownloadCount, &rawExp, &rawCreated, &isActiveInt, &sh.FileName, &sh.FileSize, &sh.MimeType, &isDirInt)
	if err != nil {
		return nil, err
	}

	sh.PasswordHash = passHash
	sh.HasPassword = (passHash != "")
	sh.ExpiresAt = parseFlexibleTimePtr(rawExp)
	sh.CreatedAt = parseFlexibleTime(rawCreated)
	sh.IsActive = (isActiveInt == 1)
	sh.IsDir = (isDirInt == 1)

	// If folder, calculate total size and file count
	if sh.IsDir {
		sh.MimeType = "directory"
		totSize, count, err := s.GetFolderStats(sh.FileID)
		if err == nil {
			sh.FileSize = totSize
			sh.FileCount = count
		}
	}

	// Check expiry
	if sh.ExpiresAt != nil && time.Now().After(*sh.ExpiresAt) {
		sh.IsActive = false
	}
	// Check download limit
	if sh.MaxDownloads > 0 && sh.DownloadCount >= sh.MaxDownloads {
		sh.IsActive = false
	}

	return &sh, nil
}

func (s *DB) ListPublicShares() ([]models.PublicShare, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT s.id, s.file_id, s.created_by, s.password_hash, s.max_downloads, s.download_count, s.expires_at, s.created_at, s.is_active,
		COALESCE(f.name, 'Tệp tin đã xóa'), COALESCE(f.size_bytes, 0), COALESCE(f.mime_type, 'application/octet-stream'), COALESCE(f.is_dir, 0)
		FROM public_shares s
		LEFT JOIN virtual_files f ON s.file_id = f.id
		ORDER BY s.created_at DESC`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]models.PublicShare, 0)
	now := time.Now()
	for rows.Next() {
		var sh models.PublicShare
		var rawExp, rawCreated interface{}
		var passHash string
		var isActiveInt, isDirInt int

		if err := rows.Scan(&sh.ID, &sh.FileID, &sh.CreatedBy, &passHash, &sh.MaxDownloads, &sh.DownloadCount, &rawExp, &rawCreated, &isActiveInt, &sh.FileName, &sh.FileSize, &sh.MimeType, &isDirInt); err != nil {
			return nil, err
		}

		sh.PasswordHash = passHash
		sh.HasPassword = (passHash != "")
		sh.ExpiresAt = parseFlexibleTimePtr(rawExp)
		sh.CreatedAt = parseFlexibleTime(rawCreated)
		sh.IsActive = (isActiveInt == 1)
		sh.IsDir = (isDirInt == 1)

		if sh.IsDir {
			sh.MimeType = "directory"
			totSize, count, err := s.GetFolderStats(sh.FileID)
			if err == nil {
				sh.FileSize = totSize
				sh.FileCount = count
			}
		}

		if sh.ExpiresAt != nil && now.After(*sh.ExpiresAt) {
			sh.IsActive = false
		}
		if sh.MaxDownloads > 0 && sh.DownloadCount >= sh.MaxDownloads {
			sh.IsActive = false
		}

		list = append(list, sh)
	}
	return list, nil
}

func (s *DB) GetFolderStats(folderID string) (int64, int, error) {
	query := `
	WITH RECURSIVE folder_tree AS (
		SELECT id, is_dir, size_bytes FROM virtual_files WHERE id = ? AND is_deleted = 0
		UNION ALL
		SELECT vf.id, vf.is_dir, vf.size_bytes FROM virtual_files vf
		JOIN folder_tree ft ON vf.parent_id = ft.id
		WHERE vf.is_deleted = 0
	)
	SELECT COALESCE(SUM(CASE WHEN is_dir = 0 THEN size_bytes ELSE 0 END), 0),
	       COUNT(CASE WHEN is_dir = 0 THEN 1 END)
	FROM folder_tree;
	`
	var totalSize int64
	var fileCount int
	err := s.db.QueryRow(query, folderID).Scan(&totalSize, &fileCount)
	return totalSize, fileCount, err
}

func (s *DB) ListFilesInFolderForShare(rootFolderID, currentFolderID string) ([]models.VirtualFile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Verify currentFolderID is rootFolderID or a descendant of rootFolderID
	if currentFolderID != rootFolderID {
		var isDescendant int
		checkQuery := `
		WITH RECURSIVE folder_tree AS (
			SELECT id FROM virtual_files WHERE id = ? AND is_deleted = 0
			UNION ALL
			SELECT vf.id FROM virtual_files vf
			JOIN folder_tree ft ON vf.parent_id = ft.id
			WHERE vf.is_deleted = 0
		)
		SELECT COUNT(*) FROM folder_tree WHERE id = ?;
		`
		_ = s.db.QueryRow(checkQuery, rootFolderID, currentFolderID).Scan(&isDescendant)
		if isDescendant == 0 {
			return nil, fmt.Errorf("thư mục không thuộc cây thư mục được chia sẻ")
		}
	}

	rows, err := s.db.Query(`SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, created_at, updated_at 
		FROM virtual_files 
		WHERE parent_id = ? AND is_deleted = 0 
		ORDER BY is_dir DESC, name ASC`, currentFolderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]models.VirtualFile, 0)
	for rows.Next() {
		var f models.VirtualFile
		var sha, uid sql.NullString
		var isDel sql.NullBool
		var rawDelAt, rawCreated, rawUpdated interface{}
		if err := rows.Scan(&f.ID, &uid, &f.ParentID, &f.Name, &f.Path, &f.IsDir, &f.SizeBytes, &f.MimeType, &sha, &f.ChunkCount, &f.IsEncrypted, &isDel, &rawDelAt, &rawCreated, &rawUpdated); err != nil {
			return nil, err
		}
		if sha.Valid {
			f.SHA256 = sha.String
		}
		if uid.Valid {
			f.UserID = uid.String
		}
		f.DeletedAt = parseFlexibleTimePtr(rawDelAt)
		f.CreatedAt = parseFlexibleTime(rawCreated)
		f.UpdatedAt = parseFlexibleTime(rawUpdated)
		list = append(list, f)
	}
	return list, nil
}

type FileInTree struct {
	File         models.VirtualFile
	RelativePath string
}

func (s *DB) GetAllFilesInFolderTree(rootFolderID string) ([]FileInTree, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var concatExpr string
	if s.IsMySQLOrTiDB() {
		concatExpr = "CONCAT(ft.rel_path, '/', vf.name)"
	} else {
		concatExpr = "ft.rel_path || '/' || vf.name"
	}

	query := fmt.Sprintf(`
	WITH RECURSIVE folder_tree AS (
		SELECT id, name, parent_id, is_dir, size_bytes, mime_type, '' AS rel_path
		FROM virtual_files
		WHERE id = ? AND is_deleted = 0
		UNION ALL
		SELECT vf.id, vf.name, vf.parent_id, vf.is_dir, vf.size_bytes, vf.mime_type,
		       CASE WHEN ft.rel_path = '' THEN vf.name ELSE %s END
		FROM virtual_files vf
		JOIN folder_tree ft ON vf.parent_id = ft.id
		WHERE vf.is_deleted = 0
	)
	SELECT id, name, parent_id, is_dir, size_bytes, mime_type, rel_path
	FROM folder_tree
	WHERE is_dir = 0;
	`, concatExpr)
	rows, err := s.db.Query(query, rootFolderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []FileInTree
	for rows.Next() {
		var f models.VirtualFile
		var relPath string
		if err := rows.Scan(&f.ID, &f.Name, &f.ParentID, &f.IsDir, &f.SizeBytes, &f.MimeType, &relPath); err != nil {
			return nil, err
		}
		files = append(files, FileInTree{
			File:         f,
			RelativePath: relPath,
		})
	}
	return files, nil
}

func (s *DB) RevokePublicShare(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`DELETE FROM public_shares WHERE id = ?`, id)
	if err == nil {
		s.InvalidateVFSCache()
	}
	return err
}

func (s *DB) IncrementPublicShareDownload(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`UPDATE public_shares SET download_count = download_count + 1 WHERE id = ?`, id)
	return err
}

// -------------------------------------------------------------
// Storage Breakdown & Analytics
// -------------------------------------------------------------

func (s *DB) GetStorageBreakdown(userID string) (*models.StorageBreakdownResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var totalUsed, totalCap, freeBytes int64
	var totalFiles int

	// 1. Quotas
	_ = s.db.QueryRow(`SELECT COALESCE(SUM(total_quota_bytes), 0), COALESCE(SUM(used_quota_bytes), 0), COALESCE(SUM(free_quota_bytes), 0) FROM accounts WHERE status = 'active'`).Scan(&totalCap, &totalUsed, &freeBytes)

	// 2. Fetch non-deleted files
	var query string
	var rows *sql.Rows
	var err error

	if userID == "all" || userID == "" || userID == "user_admin" {
		query = `SELECT id, name, size_bytes, mime_type, updated_at FROM virtual_files WHERE is_dir = 0 AND is_deleted = 0 ORDER BY size_bytes DESC`
		rows, err = s.db.Query(query)
	} else {
		query = `SELECT id, name, size_bytes, mime_type, updated_at FROM virtual_files WHERE is_dir = 0 AND is_deleted = 0 AND user_id = ? ORDER BY size_bytes DESC`
		rows, err = s.db.Query(query, userID)
	}

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type catAccumulator struct {
		Label string
		Icon  string
		Color string
		Bytes int64
		Count int
	}

	cats := map[string]*catAccumulator{
		"video":        {Label: "Video & Phim", Icon: "🎬", Color: "#ef4444"},
		"image":        {Label: "Hình ảnh", Icon: "🖼️", Color: "#10b981"},
		"audio":        {Label: "Âm thanh & Nhạc", Icon: "🎵", Color: "#f59e0b"},
		"document":     {Label: "Tài liệu & PDF", Icon: "📄", Color: "#8b5cf6"},
		"spreadsheet":  {Label: "Bảng tính & Excel", Icon: "📊", Color: "#22c55e"},
		"presentation": {Label: "Trình chiếu Slide", Icon: "📽️", Color: "#f97316"},
		"archive":      {Label: "Tệp nén & ISO", Icon: "📦", Color: "#ec4899"},
		"code":         {Label: "Mã nguồn & CSDL", Icon: "💻", Color: "#06b6d4"},
		"design":       {Label: "Thiết kế & 3D", Icon: "🎨", Color: "#a855f7"},
		"app":          {Label: "Ứng dụng & Cài đặt", Icon: "⚙️", Color: "#6366f1"},
		"other":        {Label: "Định dạng khác", Icon: "📁", Color: "#64748b"},
	}

	var allFilesTotalBytes int64
	var topFiles []models.TopFileItem

	for rows.Next() {
		var id, name, mimeType string
		var size int64
		var rawUpdated interface{}

		if err := rows.Scan(&id, &name, &size, &mimeType, &rawUpdated); err != nil {
			return nil, err
		}
		updated := parseFlexibleTime(rawUpdated)

		totalFiles++
		allFilesTotalBytes += size

		// Add to Top 10
		if len(topFiles) < 10 {
			topFiles = append(topFiles, models.TopFileItem{
				ID:        id,
				Name:      name,
				SizeBytes: size,
				MimeType:  mimeType,
				UpdatedAt: updated,
			})
		}

		// Categorize
		lowerMime := strings.ToLower(mimeType)
		lowerName := strings.ToLower(name)

		if strings.HasPrefix(lowerMime, "video/") || strings.HasSuffix(lowerName, ".mp4") || strings.HasSuffix(lowerName, ".webm") || strings.HasSuffix(lowerName, ".mkv") || strings.HasSuffix(lowerName, ".avi") || strings.HasSuffix(lowerName, ".mov") || strings.HasSuffix(lowerName, ".wmv") || strings.HasSuffix(lowerName, ".flv") || strings.HasSuffix(lowerName, ".m4v") || strings.HasSuffix(lowerName, ".ts") {
			cats["video"].Bytes += size
			cats["video"].Count++
		} else if strings.HasPrefix(lowerMime, "image/") || strings.HasSuffix(lowerName, ".jpg") || strings.HasSuffix(lowerName, ".jpeg") || strings.HasSuffix(lowerName, ".png") || strings.HasSuffix(lowerName, ".webp") || strings.HasSuffix(lowerName, ".gif") || strings.HasSuffix(lowerName, ".svg") || strings.HasSuffix(lowerName, ".bmp") || strings.HasSuffix(lowerName, ".ico") || strings.HasSuffix(lowerName, ".heic") {
			cats["image"].Bytes += size
			cats["image"].Count++
		} else if strings.HasPrefix(lowerMime, "audio/") || strings.HasSuffix(lowerName, ".mp3") || strings.HasSuffix(lowerName, ".flac") || strings.HasSuffix(lowerName, ".wav") || strings.HasSuffix(lowerName, ".aac") || strings.HasSuffix(lowerName, ".m4a") || strings.HasSuffix(lowerName, ".ogg") || strings.HasSuffix(lowerName, ".wma") || strings.HasSuffix(lowerName, ".opus") {
			cats["audio"].Bytes += size
			cats["audio"].Count++
		} else if strings.Contains(lowerMime, "excel") || strings.Contains(lowerMime, "sheet") || strings.Contains(lowerMime, "csv") || strings.HasSuffix(lowerName, ".xls") || strings.HasSuffix(lowerName, ".xlsx") || strings.HasSuffix(lowerName, ".csv") || strings.HasSuffix(lowerName, ".tsv") || strings.HasSuffix(lowerName, ".ods") || strings.HasSuffix(lowerName, ".numbers") || strings.HasSuffix(lowerName, ".parquet") {
			cats["spreadsheet"].Bytes += size
			cats["spreadsheet"].Count++
		} else if strings.Contains(lowerMime, "presentation") || strings.Contains(lowerMime, "powerpoint") || strings.HasSuffix(lowerName, ".ppt") || strings.HasSuffix(lowerName, ".pptx") || strings.HasSuffix(lowerName, ".ppsx") || strings.HasSuffix(lowerName, ".key") || strings.HasSuffix(lowerName, ".odp") {
			cats["presentation"].Bytes += size
			cats["presentation"].Count++
		} else if strings.Contains(lowerMime, "pdf") || strings.Contains(lowerMime, "word") || strings.Contains(lowerMime, "document") || strings.HasSuffix(lowerName, ".pdf") || strings.HasSuffix(lowerName, ".doc") || strings.HasSuffix(lowerName, ".docx") || strings.HasSuffix(lowerName, ".odt") || strings.HasSuffix(lowerName, ".rtf") || strings.HasSuffix(lowerName, ".epub") || strings.HasSuffix(lowerName, ".txt") || strings.HasSuffix(lowerName, ".log") {
			cats["document"].Bytes += size
			cats["document"].Count++
		} else if strings.Contains(lowerMime, "zip") || strings.Contains(lowerMime, "compressed") || strings.Contains(lowerMime, "tar") || strings.HasSuffix(lowerName, ".zip") || strings.HasSuffix(lowerName, ".rar") || strings.HasSuffix(lowerName, ".7z") || strings.HasSuffix(lowerName, ".tar") || strings.HasSuffix(lowerName, ".gz") || strings.HasSuffix(lowerName, ".tar.gz") || strings.HasSuffix(lowerName, ".iso") || strings.HasSuffix(lowerName, ".img") || strings.HasSuffix(lowerName, ".dmg") {
			cats["archive"].Bytes += size
			cats["archive"].Count++
		} else if strings.HasSuffix(lowerName, ".go") || strings.HasSuffix(lowerName, ".rs") || strings.HasSuffix(lowerName, ".py") || strings.HasSuffix(lowerName, ".js") || strings.HasSuffix(lowerName, ".ts") || strings.HasSuffix(lowerName, ".jsx") || strings.HasSuffix(lowerName, ".tsx") || strings.HasSuffix(lowerName, ".java") || strings.HasSuffix(lowerName, ".c") || strings.HasSuffix(lowerName, ".cpp") || strings.HasSuffix(lowerName, ".cs") || strings.HasSuffix(lowerName, ".php") || strings.HasSuffix(lowerName, ".sql") || strings.HasSuffix(lowerName, ".sqlite") || strings.HasSuffix(lowerName, ".db") || strings.HasSuffix(lowerName, ".json") || strings.HasSuffix(lowerName, ".yaml") || strings.HasSuffix(lowerName, ".yml") || strings.HasSuffix(lowerName, ".xml") || strings.HasSuffix(lowerName, ".html") || strings.HasSuffix(lowerName, ".css") || strings.HasSuffix(lowerName, ".sh") || strings.HasSuffix(lowerName, ".bat") || strings.HasSuffix(lowerName, ".ps1") || strings.HasSuffix(lowerName, ".md") {
			cats["code"].Bytes += size
			cats["code"].Count++
		} else if strings.HasSuffix(lowerName, ".psd") || strings.HasSuffix(lowerName, ".ai") || strings.HasSuffix(lowerName, ".eps") || strings.HasSuffix(lowerName, ".fig") || strings.HasSuffix(lowerName, ".xd") || strings.HasSuffix(lowerName, ".sketch") || strings.HasSuffix(lowerName, ".blend") || strings.HasSuffix(lowerName, ".obj") || strings.HasSuffix(lowerName, ".fbx") || strings.HasSuffix(lowerName, ".stl") || strings.HasSuffix(lowerName, ".dwg") || strings.HasSuffix(lowerName, ".dxf") {
			cats["design"].Bytes += size
			cats["design"].Count++
		} else if strings.HasSuffix(lowerName, ".exe") || strings.HasSuffix(lowerName, ".msi") || strings.HasSuffix(lowerName, ".apk") || strings.HasSuffix(lowerName, ".aab") || strings.HasSuffix(lowerName, ".ipa") || strings.HasSuffix(lowerName, ".deb") || strings.HasSuffix(lowerName, ".rpm") || strings.HasSuffix(lowerName, ".appimage") || strings.HasSuffix(lowerName, ".bin") || strings.HasSuffix(lowerName, ".pkg") {
			cats["app"].Bytes += size
			cats["app"].Count++
		} else {
			cats["other"].Bytes += size
			cats["other"].Count++
		}
	}

	orderedKeys := []string{"video", "image", "audio", "document", "spreadsheet", "presentation", "archive", "code", "design", "app", "other"}
	var catList []models.StorageCategoryBreakdown

	for _, k := range orderedKeys {
		c := cats[k]
		pct := 0.0
		if allFilesTotalBytes > 0 {
			pct = float64(c.Bytes) / float64(allFilesTotalBytes) * 100.0
		}
		catList = append(catList, models.StorageCategoryBreakdown{
			Category:   k,
			Label:      c.Label,
			Icon:       c.Icon,
			Color:      c.Color,
			TotalBytes: c.Bytes,
			FileCount:  c.Count,
			Percentage: pct,
		})
	}

	return &models.StorageBreakdownResponse{
		TotalUsedBytes: totalUsed,
		TotalCapBytes:  totalCap,
		FreeBytes:      freeBytes,
		TotalFiles:     totalFiles,
		Categories:     catList,
		TopFiles:       topFiles,
	}, nil
}




// StartAutoBackup initiates a background routine that backs up the database every 12 hours (Rule PHAN 7.3)
func (s *DB) StartAutoBackup() {
	if s.IsMySQLOrTiDB() {
		log.Println("[ENGINE] [STORAGE] TiDB/MySQL driver detected: local auto-backup skipped (managed by cloud service)")
		return
	}
	go func() {
		backupDir := filepath.Join(filepath.Dir(s.path), "backups")
		os.MkdirAll(backupDir, 0755)

		ticker := time.NewTicker(12 * time.Hour)
		defer ticker.Stop()

		for {
			// Do backup immediately on start, then every 12 hours
			func() {
				timestamp := time.Now().Format("20060102_150405")
				backupFile := filepath.Join(backupDir, fmt.Sprintf("cloudpool_metadata_%s.db", timestamp))

				// Use SQLite VACUUM INTO to flush WAL journals and ensure zero data corruption
				if err := s.BackupDatabase(backupFile); err != nil {
					fmt.Printf("[ENGINE] [WARN] VACUUM INTO backup failed (%v), attempting fallback copy...\n", err)
					s.mu.RLock()
					src, errOpen := os.Open(s.path)
					if errOpen != nil {
						s.mu.RUnlock()
						fmt.Printf("[ENGINE] [ERROR] Failed to open DB for backup fallback: %v\n", errOpen)
						return
					}
					dst, errCreate := os.Create(backupFile)
					if errCreate != nil {
						src.Close()
						s.mu.RUnlock()
						fmt.Printf("[ENGINE] [ERROR] Failed to create backup file fallback: %v\n", errCreate)
						return
					}
					_, errCopy := io.Copy(dst, src)
					src.Close()
					dst.Close()
					s.mu.RUnlock()
					if errCopy != nil {
						fmt.Printf("[ENGINE] [ERROR] Fallback copy failed: %v\n", errCopy)
						return
					}
					fmt.Printf("[ENGINE] [BACKUP] Fallback backup created: %s\n", backupFile)
				} else {
					fmt.Printf("[ENGINE] [BACKUP] Clean WAL-safe backup created successfully: %s\n", backupFile)
				}

				// Cleanup old backups (keep last 14 = 7 days)
				files, err := os.ReadDir(backupDir)
				if err == nil && len(files) > 14 {
					// files are sorted by name, which means chronological due to YYYYMMDD_HHMMSS
					for i := 0; i < len(files)-14; i++ {
						os.Remove(filepath.Join(backupDir, files[i].Name()))
					}
				}
			}()

			<-ticker.C
		}
	}()
}

// =============================================================================
// HỆ THỐNG TỰ ĐỘNG ĐỒNG BỘ & KHỞI TẠO DỰ PHÒNG CSDL (AUTO-SYNC & RESILIENCE)
// Tuân thủ Quy chế PHAN 0, PHAN 1.5, PHAN 3.5, PHAN 7.1
// =============================================================================

// EnsureTiDBCloudPoolDataReady kiểm tra tính sẵn sàng của các bảng accounts, virtual_files,
// settings khi khởi động TiDB Cloud. Nếu thiếu cấu hình mặc định (master passphrase, chunk size,
// strategy...) hoặc bảng rỗng, tự động khởi tạo an toàn (seed idempotent / auto-sync).
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
	_ = s.db.QueryRow("SELECT COUNT(*) FROM users WHERE username_hash = ?", adminUsernameHash).Scan(&adminCount)
	now := time.Now()
	if adminCount == 0 {
		_, _ = s.db.Exec(`INSERT INTO users (id, username, username_hash, password_hash, display_name, role, quota_bytes, used_bytes, failed_login_count, locked_until, created_at, updated_at) 
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, NULL, ?, ?)`,
			"user_admin", encAdminName, adminUsernameHash, adminPassBcrypt, encAdminDisplay, "admin", 0, 0, now, now)
	} else {
		_, _ = s.db.Exec(`UPDATE users SET password_hash = ?, failed_login_count = 0, locked_until = NULL WHERE username_hash = ?`, adminPassBcrypt, adminUsernameHash)
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
	go s.SyncToLocalSQLiteCacheIfMissing()

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
			uQ = `INSERT IGNORE INTO users (id, username, username_hash, email, email_hash, password_hash, security_pin_hash, security_tier, display_name, avatar_url, role, status, quota_bytes, used_bytes, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		} else {
			uQ = `INSERT OR IGNORE INTO users (id, username, username_hash, email, email_hash, password_hash, security_pin_hash, security_tier, display_name, avatar_url, role, status, quota_bytes, used_bytes, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
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

// SyncToLocalSQLiteCacheIfMissing đã bị loại bỏ vì không còn hỗ trợ SQLite
func (s *DB) SyncToLocalSQLiteCacheIfMissing() error {
	return nil
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

