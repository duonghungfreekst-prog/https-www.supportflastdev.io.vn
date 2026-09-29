package main

import (
	"bufio"
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"
)

// TableDef định nghĩa bảng dữ liệu và DDL tương thích TiDB Cloud Serverless
type TableDef struct {
	SourceDB   string // "supportflast" hoặc "cloudpool"
	SourceTbl  string // Tên bảng nguồn trong SQLite
	Name       string // Tên bảng đích trên TiDB Cloud
	PrimaryKey []string
	DDL        string
}

// Danh sách tất cả các bảng của toàn bộ nền tảng SupportFlast (Web Portal + CloudPool Storage)
var allTables = []TableDef{
	// -------------------------------------------------------------
	// NHÓM 1: CỔNG THÔNG TIN WEB VÀ PHẦN MỀM (data/supportflast.db)
	// -------------------------------------------------------------
	{
		SourceDB:   "supportflast",
		SourceTbl:  "users",
		Name:       "users",
		PrimaryKey: []string{"id"},
		DDL: `CREATE TABLE IF NOT EXISTS ` + "`users`" + ` (
    ` + "`id`" + ` VARCHAR(64) PRIMARY KEY,
    ` + "`username`" + ` VARCHAR(100) UNIQUE NOT NULL,
    ` + "`email`" + ` VARCHAR(255) UNIQUE NOT NULL,
    ` + "`password_hash`" + ` VARCHAR(255) NOT NULL,
    ` + "`display_name`" + ` VARCHAR(255),
    ` + "`role`" + ` VARCHAR(50) NOT NULL DEFAULT 'user',
    ` + "`avatar`" + ` VARCHAR(500),
    ` + "`created_at`" + ` VARCHAR(64),
    ` + "`updated_at`" + ` VARCHAR(64),
    ` + "`last_login`" + ` VARCHAR(64),
    INDEX idx_users_username (username),
    INDEX idx_users_email (email),
    INDEX idx_users_role (role),
    INDEX idx_users_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},
	{
		SourceDB:   "supportflast",
		SourceTbl:  "apps",
		Name:       "apps",
		PrimaryKey: []string{"id"},
		DDL: `CREATE TABLE IF NOT EXISTS ` + "`apps`" + ` (
    ` + "`id`" + ` VARCHAR(64) PRIMARY KEY,
    ` + "`name`" + ` VARCHAR(255) NOT NULL,
    ` + "`version`" + ` VARCHAR(50) NOT NULL,
    ` + "`platform`" + ` VARCHAR(100),
    ` + "`category`" + ` VARCHAR(100),
    ` + "`desc`" + ` TEXT,
    ` + "`file_name`" + ` VARCHAR(255),
    ` + "`size_bytes`" + ` BIGINT DEFAULT 0,
    ` + "`size_formatted`" + ` VARCHAR(50),
    ` + "`sha256`" + ` VARCHAR(128),
    ` + "`author`" + ` VARCHAR(255),
    ` + "`downloads`" + ` INT DEFAULT 0,
    ` + "`status`" + ` VARCHAR(50) DEFAULT 'published',
    ` + "`published_at`" + ` VARCHAR(64),
    ` + "`download_url`" + ` TEXT,
    ` + "`video_url`" + ` TEXT,
    ` + "`guide`" + ` TEXT,
    ` + "`user_id`" + ` VARCHAR(64),
    INDEX idx_apps_user_id (user_id),
    INDEX idx_apps_status (status),
    INDEX idx_apps_category (category),
    INDEX idx_apps_platform (platform),
    INDEX idx_apps_published_at (published_at),
    INDEX idx_apps_downloads (downloads)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},
	{
		SourceDB:   "supportflast",
		SourceTbl:  "api_keys",
		Name:       "api_keys",
		PrimaryKey: []string{"id"},
		DDL: `CREATE TABLE IF NOT EXISTS ` + "`api_keys`" + ` (
    ` + "`id`" + ` VARCHAR(64) PRIMARY KEY,
    ` + "`user_id`" + ` VARCHAR(64),
    ` + "`name`" + ` VARCHAR(255),
    ` + "`key_hash`" + ` VARCHAR(128) UNIQUE,
    ` + "`prefix`" + ` VARCHAR(32),
    ` + "`status`" + ` VARCHAR(50) DEFAULT 'active',
    ` + "`permissions`" + ` TEXT,
    ` + "`created_at`" + ` VARCHAR(64),
    INDEX idx_api_keys_user_id (user_id),
    INDEX idx_api_keys_key_hash (key_hash),
    INDEX idx_api_keys_prefix (prefix),
    INDEX idx_api_keys_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},
	{
		SourceDB:   "supportflast",
		SourceTbl:  "system_releases",
		Name:       "system_releases",
		PrimaryKey: []string{"version"},
		DDL: `CREATE TABLE IF NOT EXISTS ` + "`system_releases`" + ` (
    ` + "`version`" + ` VARCHAR(64) PRIMARY KEY,
    ` + "`title`" + ` VARCHAR(255),
    ` + "`date`" + ` VARCHAR(64),
    ` + "`build_hash`" + ` VARCHAR(128),
    ` + "`notes`" + ` TEXT,
    ` + "`published_by`" + ` VARCHAR(255),
    INDEX idx_system_releases_date (date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},
	{
		SourceDB:   "supportflast",
		SourceTbl:  "reviews",
		Name:       "reviews",
		PrimaryKey: []string{"id"},
		DDL: `CREATE TABLE IF NOT EXISTS ` + "`reviews`" + ` (
    ` + "`id`" + ` VARCHAR(64) PRIMARY KEY,
    ` + "`app_id`" + ` VARCHAR(64),
    ` + "`user_id`" + ` VARCHAR(64),
    ` + "`author_name`" + ` VARCHAR(255) NOT NULL,
    ` + "`author_role`" + ` VARCHAR(100),
    ` + "`stars`" + ` INT NOT NULL,
    ` + "`text`" + ` TEXT NOT NULL,
    ` + "`status`" + ` VARCHAR(50) DEFAULT 'approved',
    ` + "`created_at`" + ` VARCHAR(64),
    INDEX idx_reviews_app_id (app_id),
    INDEX idx_reviews_user_id (user_id),
    INDEX idx_reviews_status (status),
    INDEX idx_reviews_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},
	{
		SourceDB:   "supportflast",
		SourceTbl:  "audit_logs",
		Name:       "audit_logs",
		PrimaryKey: []string{"id"},
		DDL: `CREATE TABLE IF NOT EXISTS ` + "`audit_logs`" + ` (
    ` + "`id`" + ` VARCHAR(64) PRIMARY KEY,
    ` + "`user_id`" + ` VARCHAR(64),
    ` + "`action`" + ` VARCHAR(100) NOT NULL,
    ` + "`ip_address`" + ` VARCHAR(100),
    ` + "`user_agent`" + ` VARCHAR(500),
    ` + "`details`" + ` TEXT,
    ` + "`created_at`" + ` VARCHAR(64),
    INDEX idx_audit_logs_user_id (user_id),
    INDEX idx_audit_logs_action (action),
    INDEX idx_audit_logs_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},
	{
		SourceDB:   "supportflast",
		SourceTbl:  "security_events",
		Name:       "security_events",
		PrimaryKey: []string{"id"},
		DDL: `CREATE TABLE IF NOT EXISTS ` + "`security_events`" + ` (
    ` + "`id`" + ` VARCHAR(64) PRIMARY KEY,
    ` + "`event_type`" + ` VARCHAR(100) NOT NULL,
    ` + "`ip_address`" + ` VARCHAR(100) NOT NULL,
    ` + "`severity`" + ` VARCHAR(50) NOT NULL DEFAULT 'warning',
    ` + "`details`" + ` TEXT,
    ` + "`blocked_until`" + ` VARCHAR(64),
    ` + "`created_at`" + ` VARCHAR(64),
    INDEX idx_security_events_ip (ip_address),
    INDEX idx_security_events_type (event_type),
    INDEX idx_security_events_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},

	// -------------------------------------------------------------
	// NHÓM 2: CLOUDPOOL STORAGE ENGINE (data/cloudpool_metadata.db)
	// -------------------------------------------------------------
	{
		SourceDB:   "cloudpool",
		SourceTbl:  "accounts",
		Name:       "accounts",
		PrimaryKey: []string{"id"},
		DDL: `CREATE TABLE IF NOT EXISTS ` + "`accounts`" + ` (
    ` + "`id`" + ` VARCHAR(64) PRIMARY KEY,
    ` + "`email`" + ` VARCHAR(255) NOT NULL,
    ` + "`name`" + ` VARCHAR(255),
    ` + "`avatar_url`" + ` TEXT,
    ` + "`auth_type`" + ` VARCHAR(50) NOT NULL,
    ` + "`credentials_json`" + ` LONGTEXT,
    ` + "`token_json`" + ` LONGTEXT,
    ` + "`root_folder_id`" + ` VARCHAR(128),
    ` + "`total_quota_bytes`" + ` BIGINT DEFAULT 0,
    ` + "`used_quota_bytes`" + ` BIGINT DEFAULT 0,
    ` + "`free_quota_bytes`" + ` BIGINT DEFAULT 0,
    ` + "`status`" + ` VARCHAR(50) DEFAULT 'active',
    ` + "`last_error`" + ` TEXT,
    ` + "`created_at`" + ` VARCHAR(64),
    ` + "`updated_at`" + ` VARCHAR(64),
    ` + "`email_hash`" + ` VARCHAR(128) DEFAULT '',
    ` + "`name_hash`" + ` VARCHAR(128) DEFAULT '',
    INDEX idx_accounts_email (email(191)),
    INDEX idx_accounts_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},
	{
		SourceDB:   "cloudpool",
		SourceTbl:  "virtual_files",
		Name:       "virtual_files",
		PrimaryKey: []string{"id"},
		DDL: `CREATE TABLE IF NOT EXISTS ` + "`virtual_files`" + ` (
    ` + "`id`" + ` VARCHAR(64) PRIMARY KEY,
    ` + "`parent_id`" + ` VARCHAR(64) DEFAULT '',
    ` + "`name`" + ` VARCHAR(500) NOT NULL,
    ` + "`path`" + ` VARCHAR(1000) NOT NULL,
    ` + "`is_dir`" + ` TINYINT(1) DEFAULT 0,
    ` + "`size_bytes`" + ` BIGINT DEFAULT 0,
    ` + "`mime_type`" + ` VARCHAR(150),
    ` + "`sha256`" + ` VARCHAR(128),
    ` + "`chunk_count`" + ` INT DEFAULT 0,
    ` + "`is_encrypted`" + ` TINYINT(1) DEFAULT 1,
    ` + "`created_at`" + ` VARCHAR(64),
    ` + "`updated_at`" + ` VARCHAR(64),
    ` + "`user_id`" + ` VARCHAR(64) DEFAULT 'user_admin',
    ` + "`is_deleted`" + ` TINYINT(1) DEFAULT 0,
    ` + "`deleted_at`" + ` VARCHAR(64),
    ` + "`has_missing_chunks`" + ` TINYINT(1) DEFAULT 0,
    INDEX idx_vfiles_parent_id (parent_id),
    INDEX idx_vfiles_path (path(255)),
    INDEX idx_vfiles_user_id (user_id),
    INDEX idx_vfiles_is_deleted (is_deleted)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},
	{
		SourceDB:   "cloudpool",
		SourceTbl:  "file_chunks",
		Name:       "file_chunks",
		PrimaryKey: []string{"chunk_id"},
		DDL: `CREATE TABLE IF NOT EXISTS ` + "`file_chunks`" + ` (
    ` + "`chunk_id`" + ` VARCHAR(64) PRIMARY KEY,
    ` + "`file_id`" + ` VARCHAR(64) NOT NULL,
    ` + "`chunk_index`" + ` INT NOT NULL,
    ` + "`account_id`" + ` VARCHAR(64) NOT NULL,
    ` + "`gdrive_file_id`" + ` VARCHAR(128) NOT NULL,
    ` + "`chunk_size_bytes`" + ` BIGINT DEFAULT 0,
    ` + "`encrypted_size_bytes`" + ` BIGINT DEFAULT 0,
    ` + "`sha256`" + ` VARCHAR(128),
    ` + "`status`" + ` VARCHAR(50) DEFAULT 'uploaded',
    ` + "`ref_count`" + ` INT DEFAULT 1,
    INDEX idx_fchunks_file_id (file_id),
    INDEX idx_fchunks_account_id (account_id),
    INDEX idx_fchunks_gdrive_id (gdrive_file_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},
	{
		SourceDB:   "cloudpool",
		SourceTbl:  "settings",
		Name:       "settings",
		PrimaryKey: []string{"key"},
		DDL: `CREATE TABLE IF NOT EXISTS ` + "`settings`" + ` (
    ` + "`key`" + ` VARCHAR(100) PRIMARY KEY,
    ` + "`value`" + ` LONGTEXT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},
	{
		SourceDB:   "cloudpool",
		SourceTbl:  "users",
		Name:       "cloudpool_users",
		PrimaryKey: []string{"id"},
		DDL: `CREATE TABLE IF NOT EXISTS ` + "`cloudpool_users`" + ` (
    ` + "`id`" + ` VARCHAR(64) PRIMARY KEY,
    ` + "`username`" + ` VARCHAR(255) NOT NULL,
    ` + "`password_hash`" + ` VARCHAR(255) NOT NULL,
    ` + "`display_name`" + ` VARCHAR(255),
    ` + "`role`" + ` VARCHAR(50) DEFAULT 'user',
    ` + "`quota_bytes`" + ` BIGINT DEFAULT 0,
    ` + "`used_bytes`" + ` BIGINT DEFAULT 0,
    ` + "`created_at`" + ` VARCHAR(64),
    ` + "`updated_at`" + ` VARCHAR(64),
    ` + "`email`" + ` VARCHAR(255) DEFAULT '',
    ` + "`security_pin_hash`" + ` VARCHAR(255) DEFAULT '',
    ` + "`security_tier`" + ` INT DEFAULT 1,
    ` + "`avatar_url`" + ` TEXT,
    ` + "`status`" + ` VARCHAR(50) DEFAULT 'active',
    ` + "`failed_login_count`" + ` INT DEFAULT 0,
    ` + "`locked_until`" + ` VARCHAR(64),
    ` + "`last_login_at`" + ` VARCHAR(64),
    ` + "`username_hash`" + ` VARCHAR(128) DEFAULT '',
    ` + "`email_hash`" + ` VARCHAR(128) DEFAULT ''
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},
	{
		SourceDB:   "cloudpool",
		SourceTbl:  "activity_logs",
		Name:       "activity_logs",
		PrimaryKey: []string{"id"},
		DDL: `CREATE TABLE IF NOT EXISTS ` + "`activity_logs`" + ` (
    ` + "`id`" + ` VARCHAR(64) PRIMARY KEY,
    ` + "`user_id`" + ` VARCHAR(64),
    ` + "`username`" + ` VARCHAR(255),
    ` + "`action`" + ` VARCHAR(100),
    ` + "`target`" + ` VARCHAR(255),
    ` + "`ip_address`" + ` VARCHAR(100),
    ` + "`details`" + ` LONGTEXT,
    ` + "`created_at`" + ` VARCHAR(64),
    INDEX idx_act_user_id (user_id),
    INDEX idx_act_action (action)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},
	{
		SourceDB:   "cloudpool",
		SourceTbl:  "login_sessions",
		Name:       "login_sessions",
		PrimaryKey: []string{"id"},
		DDL: `CREATE TABLE IF NOT EXISTS ` + "`login_sessions`" + ` (
    ` + "`id`" + ` VARCHAR(64) PRIMARY KEY,
    ` + "`user_id`" + ` VARCHAR(64),
    ` + "`username`" + ` VARCHAR(255),
    ` + "`ip_address`" + ` VARCHAR(100),
    ` + "`device_info`" + ` TEXT,
    ` + "`location_info`" + ` TEXT,
    ` + "`status`" + ` VARCHAR(50),
    ` + "`user_agent`" + ` TEXT,
    ` + "`created_at`" + ` VARCHAR(64),
    INDEX idx_loginsess_user_id (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},
	{
		SourceDB:   "cloudpool",
		SourceTbl:  "file_access_otps",
		Name:       "file_access_otps",
		PrimaryKey: []string{"id"},
		DDL: `CREATE TABLE IF NOT EXISTS ` + "`file_access_otps`" + ` (
    ` + "`id`" + ` VARCHAR(64) PRIMARY KEY,
    ` + "`file_id`" + ` VARCHAR(64) NOT NULL,
    ` + "`file_name`" + ` VARCHAR(255) NOT NULL,
    ` + "`target_user_id`" + ` VARCHAR(64) NOT NULL DEFAULT 'all',
    ` + "`otp_code`" + ` VARCHAR(32) NOT NULL,
    ` + "`created_by`" + ` VARCHAR(64) NOT NULL DEFAULT 'user_admin',
    ` + "`is_used`" + ` TINYINT DEFAULT 0,
    ` + "`used_by`" + ` VARCHAR(64) DEFAULT '',
    ` + "`used_at`" + ` VARCHAR(64),
    ` + "`expires_at`" + ` VARCHAR(64) NOT NULL,
    ` + "`created_at`" + ` VARCHAR(64) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},
	{
		SourceDB:   "cloudpool",
		SourceTbl:  "file_access_requests",
		Name:       "file_access_requests",
		PrimaryKey: []string{"id"},
		DDL: `CREATE TABLE IF NOT EXISTS ` + "`file_access_requests`" + ` (
    ` + "`id`" + ` VARCHAR(64) PRIMARY KEY,
    ` + "`file_id`" + ` VARCHAR(64) NOT NULL,
    ` + "`file_name`" + ` VARCHAR(255) NOT NULL,
    ` + "`user_id`" + ` VARCHAR(64) NOT NULL,
    ` + "`username`" + ` VARCHAR(255) NOT NULL,
    ` + "`user_display_name`" + ` VARCHAR(255) NOT NULL,
    ` + "`status`" + ` VARCHAR(50) DEFAULT 'pending',
    ` + "`otp_code`" + ` VARCHAR(32) DEFAULT '',
    ` + "`created_at`" + ` VARCHAR(64) NOT NULL,
    ` + "`updated_at`" + ` VARCHAR(64) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},
	{
		SourceDB:   "cloudpool",
		SourceTbl:  "public_shares",
		Name:       "public_shares",
		PrimaryKey: []string{"id"},
		DDL: `CREATE TABLE IF NOT EXISTS ` + "`public_shares`" + ` (
    ` + "`id`" + ` VARCHAR(64) PRIMARY KEY,
    ` + "`file_id`" + ` VARCHAR(64) NOT NULL,
    ` + "`created_by`" + ` VARCHAR(64) NOT NULL DEFAULT 'user_admin',
    ` + "`password_hash`" + ` VARCHAR(255) DEFAULT '',
    ` + "`max_downloads`" + ` INT DEFAULT 0,
    ` + "`download_count`" + ` INT DEFAULT 0,
    ` + "`expires_at`" + ` VARCHAR(64),
    ` + "`created_at`" + ` VARCHAR(64) NOT NULL,
    ` + "`is_active`" + ` TINYINT DEFAULT 1
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},
	{
		SourceDB:   "cloudpool",
		SourceTbl:  "gdrive_backups",
		Name:       "gdrive_backups",
		PrimaryKey: []string{"id"},
		DDL: `CREATE TABLE IF NOT EXISTS ` + "`gdrive_backups`" + ` (
    ` + "`id`" + ` VARCHAR(64) PRIMARY KEY,
    ` + "`filename`" + ` VARCHAR(255) NOT NULL,
    ` + "`size_bytes`" + ` BIGINT NOT NULL,
    ` + "`sha256`" + ` VARCHAR(128) NOT NULL,
    ` + "`gdrive_file_id`" + ` VARCHAR(128) NOT NULL,
    ` + "`gdrive_web_link`" + ` TEXT NOT NULL,
    ` + "`target_email`" + ` VARCHAR(255) NOT NULL,
    ` + "`manifest_json`" + ` LONGTEXT,
    ` + "`created_at`" + ` VARCHAR(64) NOT NULL,
    INDEX idx_gdbk_email (target_email(191))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},
}

// Config chứa cấu hình kết nối
type Config struct {
	SupportflastDBPath string
	CloudpoolDBPath    string
	TiDBHost           string
	TiDBPort           string
	TiDBUser           string
	TiDBPassword       string
	TiDBDatabase       string
	TiDBTLS            string
	BatchSize          int
	Mode               string
	Verbose            bool
}

func main() {
	var (
		flagEnv     = flag.String("env", "", "Đường dẫn file .env (mặc định: tự động tìm .env)")
		flagHost    = flag.String("host", "", "TiDB Cloud Host")
		flagPort    = flag.String("port", "", "TiDB Cloud Port (mặc định: 4000)")
		flagUser    = flag.String("user", "", "TiDB Cloud User")
		flagPass    = flag.String("password", "", "TiDB Cloud Password")
		flagDB      = flag.String("database", "", "TiDB Cloud Database (mặc định: supportflast)")
		flagTLS     = flag.String("tls", "", "Bật TLS cho TiDB (true/false, mặc định: true)")
		flagBatch   = flag.Int("batch", 100, "Kích thước mỗi lô chèn dữ liệu")
		flagMode    = flag.String("mode", "upsert", "Chế độ chèn: upsert / ignore / replace")
		flagVerbose = flag.Bool("verbose", false, "Hiển thị chi tiết từng bản ghi xử lý")
	)
	flag.Parse()

	fmt.Println(`
╔═══════════════════════════════════════════════════════════════════════════╗
║    SUPPORTFLAST - ĐỒNG BỘ TOÀN DIỆN TẤT CẢ DỮ LIỆU LÊN TIDB CLOUD         ║
║          (Web Portal, Kho Lưu Trữ CloudPool, Khóa API, Phiên Bản)         ║
╚═══════════════════════════════════════════════════════════════════════════╝`)

	// 1. Nạp file .env
	envPath := resolveEnvPath(*flagEnv)
	if envPath != "" {
		_ = loadEnvFile(envPath)
		fmt.Printf("📄 [CẤU HÌNH] Đã nạp cấu hình từ: %s\n", envPath)
	}

	cfg := Config{
		SupportflastDBPath: resolvePath("data/supportflast.db"),
		CloudpoolDBPath:    resolvePath("data/cloudpool_metadata.db"),
		TiDBHost:           getEnvOrDefault("TIDB_HOST", *flagHost, "gateway01.ap-southeast-1.prod.aws.tidbcloud.com"),
		TiDBPort:           getEnvOrDefault("TIDB_PORT", *flagPort, "4000"),
		TiDBUser:           getEnvOrDefault("TIDB_USER", *flagUser, "2KGt5QqixkveQPP.root"),
		TiDBPassword:       getEnvOrDefault("TIDB_PASSWORD", *flagPass, "JIxWb1nGVINnKzap"),
		TiDBDatabase:       getEnvOrDefault("TIDB_DATABASE", *flagDB, "supportflast"),
		TiDBTLS:            getEnvOrDefault("TIDB_TLS", *flagTLS, "true"),
		BatchSize:          *flagBatch,
		Mode:               strings.ToLower(strings.TrimSpace(*flagMode)),
		Verbose:            *flagVerbose,
	}

	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}

	// 2. Mở kết nối SQLite các nguồn
	sqlitePortal, err := sql.Open("sqlite", cfg.SupportflastDBPath)
	if err != nil {
		log.Fatalf("❌ [LỖI] Không thể mở SQLite portal '%s': %v", cfg.SupportflastDBPath, err)
	}
	defer sqlitePortal.Close()

	sqliteCloudPool, err := sql.Open("sqlite", cfg.CloudpoolDBPath)
	if err != nil {
		log.Fatalf("❌ [LỖI] Không thể mở SQLite CloudPool '%s': %v", cfg.CloudpoolDBPath, err)
	}
	defer sqliteCloudPool.Close()

	fmt.Println("✅ [SQLITE] Đã kết nối thành công 2 cơ sở dữ liệu nguồn:")
	fmt.Printf("   ├─ Portal & Web: %s\n", cfg.SupportflastDBPath)
	fmt.Printf("   └─ CloudPool Storage: %s\n", cfg.CloudpoolDBPath)

	// 3. Kết nối TiDB Cloud
	ensureDatabaseExistsOnTiDB(cfg)
	tlsParam := "false"
	if strings.ToLower(cfg.TiDBTLS) == "true" || cfg.TiDBTLS == "1" {
		tlsParam = "true"
	}
	tidbDSN := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?tls=%s&charset=utf8mb4&parseTime=True&loc=Local&interpolateParams=true&multiStatements=true",
		cfg.TiDBUser, cfg.TiDBPassword, cfg.TiDBHost, cfg.TiDBPort, cfg.TiDBDatabase, tlsParam)

	tidbDB, err := sql.Open("mysql", tidbDSN)
	if err != nil {
		log.Fatalf("❌ [LỖI] Kết nối TiDB driver thất bại: %v", err)
	}
	defer tidbDB.Close()

	if err := tidbDB.Ping(); err != nil {
		log.Fatalf("❌ [LỖI] Không thể ping tới TiDB Cloud: %v", err)
	}
	fmt.Printf("✅ [TIDB CLOUD] Kết nối thành công tới %s (Database: %s)!\n", cfg.TiDBHost, cfg.TiDBDatabase)

	// 4. XOÁ TÀI KHOẢN MẪU THEO YÊU CẦU CỦA NGƯỜI DÙNG
	fmt.Println("\n🧹 [DỌN DẸP] Đang tiến hành xóa tài khoản mẫu (quocviet_dev, devlanuser) khỏi TiDB Cloud...")
	resDel, err := tidbDB.Exec("DELETE FROM users WHERE username IN ('quocviet_dev', 'devlanuser');")
	if err != nil {
		log.Printf("⚠️ Cảnh báo xóa tài khoản mẫu trên TiDB: %v", err)
	} else {
		affected, _ := resDel.RowsAffected()
		fmt.Printf("   └─ Đã xóa thành công %d tài khoản mẫu trên TiDB Cloud.\n", affected)
	}

	// 5. Tắt foreign_key_checks tạm thời để nạp dữ liệu mượt mà
	_, _ = tidbDB.Exec("SET foreign_key_checks = 0;")
	defer tidbDB.Exec("SET foreign_key_checks = 1;")

	// 6. Di chuyển & đồng bộ từng bảng
	fmt.Println("\n======================= TIẾN HÀNH ĐỒNG BỘ TOÀN DIỆN =======================")
	startTime := time.Now()
	totalMigrated := 0

	for _, tbl := range allTables {
		var srcDB *sql.DB
		if tbl.SourceDB == "supportflast" {
			srcDB = sqlitePortal
		} else {
			srcDB = sqliteCloudPool
		}

		// Kiểm tra bảng nguồn có tồn tại không
		exists, err := checkTableExistsSQLite(srcDB, tbl.SourceTbl)
		if err != nil || !exists {
			if cfg.Verbose {
				fmt.Printf("ℹ️ [BỎ QUA] Bảng nguồn '%s' không tồn tại trong %s.\n", tbl.SourceTbl, tbl.SourceDB)
			}
			continue
		}

		// Tạo bảng trên TiDB Cloud nếu chưa có
		if _, err := tidbDB.Exec(tbl.DDL); err != nil {
			log.Fatalf("❌ [LỖI] Tạo bảng '%s' trên TiDB thất bại: %v\nDDL: %s", tbl.Name, err, tbl.DDL)
		}

		// Di chuyển dữ liệu
		count, err := migrateTableData(srcDB, tidbDB, tbl, cfg)
		if err != nil {
			log.Fatalf("❌ [LỖI] Đồng bộ bảng '%s' thất bại: %v", tbl.Name, err)
		}
		totalMigrated += count
	}

	duration := time.Since(startTime)
	fmt.Println("===========================================================================")
	fmt.Printf("🎉 [HOÀN TẤT] Đồng bộ tổng cộng %d bản ghi lên TiDB Cloud trong %s!\n", totalMigrated, duration.Round(time.Millisecond))
	fmt.Println("===========================================================================\n")

	// 7. Báo cáo đối chiếu và thống kê toàn bộ
	printFinalVerification(sqlitePortal, sqliteCloudPool, tidbDB)
}

// migrateTableData đọc từ bảng nguồn SQLite và ghi vào bảng đích TiDB
func migrateTableData(srcDB, targetDB *sql.DB, tbl TableDef, cfg Config) (int, error) {
	rows, err := srcDB.Query(fmt.Sprintf("SELECT * FROM `%s`", tbl.SourceTbl))
	if err != nil {
		return 0, fmt.Errorf("truy vấn nguồn thất bại: %w", err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return 0, fmt.Errorf("không lấy được tên cột: %w", err)
	}

	colCount := len(columns)
	if colCount == 0 {
		return 0, nil
	}

	var batchRecords [][]interface{}
	totalRows := 0

	for rows.Next() {
		values := make([]interface{}, colCount)
		valuePtrs := make([]interface{}, colCount)
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return 0, fmt.Errorf("scan hàng thất bại: %w", err)
		}

		normalizedRow := make([]interface{}, colCount)
		for i, val := range values {
			if b, ok := val.([]byte); ok {
				normalizedRow[i] = string(b)
			} else {
				normalizedRow[i] = val
			}
		}

		// Nếu là bảng users của portal, tuyệt đối không chèn tài khoản mẫu
		if tbl.Name == "users" {
			usernameIdx := -1
			for ci, cn := range columns {
				if strings.ToLower(cn) == "username" {
					usernameIdx = ci
					break
				}
			}
			if usernameIdx >= 0 {
				uVal := fmt.Sprintf("%v", normalizedRow[usernameIdx])
				if uVal == "quocviet_dev" || uVal == "devlanuser" {
					continue // Bỏ qua tài khoản mẫu
				}
			}
		}

		batchRecords = append(batchRecords, normalizedRow)
		totalRows++

		if len(batchRecords) >= cfg.BatchSize {
			if err := insertBatchData(targetDB, tbl, columns, batchRecords, cfg.Mode); err != nil {
				return 0, fmt.Errorf("chèn lô vào '%s' thất bại: %w", tbl.Name, err)
			}
			batchRecords = batchRecords[:0]
		}
	}

	if len(batchRecords) > 0 {
		if err := insertBatchData(targetDB, tbl, columns, batchRecords, cfg.Mode); err != nil {
			return 0, fmt.Errorf("chèn lô cuối vào '%s' thất bại: %w", tbl.Name, err)
		}
	}

	fmt.Printf("📦 [BẢNG '%-18s'] Nguồn: %-12s ➔ Đích: %-18s: %4d bản ghi\n", tbl.Name, tbl.SourceTbl, tbl.Name, totalRows)
	return totalRows, nil
}

// insertBatchData chèn dữ liệu theo lô với cơ chế Upsert / On Duplicate Key Update
func insertBatchData(db *sql.DB, tbl TableDef, columns []string, records [][]interface{}, mode string) error {
	if len(records) == 0 {
		return nil
	}

	quotedCols := make([]string, len(columns))
	for i, c := range columns {
		quotedCols[i] = fmt.Sprintf("`%s`", c)
	}

	numCols := len(columns)
	rowPlaceholder := "(" + strings.Repeat("?, ", numCols-1) + "?)"

	placeholders := make([]string, len(records))
	var allArgs []interface{}
	allArgs = make([]interface{}, 0, len(records)*numCols)

	for i, r := range records {
		placeholders[i] = rowPlaceholder
		allArgs = append(allArgs, r...)
	}

	var query string
	switch mode {
	case "replace":
		query = fmt.Sprintf("REPLACE INTO `%s` (%s) VALUES %s", tbl.Name, strings.Join(quotedCols, ", "), strings.Join(placeholders, ", "))
	case "ignore":
		query = fmt.Sprintf("INSERT IGNORE INTO `%s` (%s) VALUES %s", tbl.Name, strings.Join(quotedCols, ", "), strings.Join(placeholders, ", "))
	default: // "upsert"
		var updateClauses []string
		pkMap := make(map[string]bool)
		for _, pk := range tbl.PrimaryKey {
			pkMap[strings.ToLower(pk)] = true
		}

		for _, col := range columns {
			if !pkMap[strings.ToLower(col)] {
				updateClauses = append(updateClauses, fmt.Sprintf("`%s` = VALUES(`%s`)", col, col))
			}
		}

		if len(updateClauses) > 0 {
			query = fmt.Sprintf("INSERT INTO `%s` (%s) VALUES %s ON DUPLICATE KEY UPDATE %s",
				tbl.Name, strings.Join(quotedCols, ", "), strings.Join(placeholders, ", "), strings.Join(updateClauses, ", "))
		} else {
			query = fmt.Sprintf("INSERT IGNORE INTO `%s` (%s) VALUES %s", tbl.Name, strings.Join(quotedCols, ", "), strings.Join(placeholders, ", "))
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, err := db.ExecContext(ctx, query, allArgs...)
	return err
}

// printFinalVerification đối chiếu số lượng bản ghi giữa SQLite và TiDB Cloud
func printFinalVerification(portalDB, cpDB, tidbDB *sql.DB) {
	fmt.Printf("\n%-24s | %-12s | %-12s | %-15s\n", "Tên Bảng (TiDB Cloud)", "Nguồn SQLite", "TiDB Cloud", "Đối Chiếu")
	fmt.Println(strings.Repeat("-", 72))

	totalSQLite := 0
	totalTiDB := 0

	for _, tbl := range allTables {
		var srcDB *sql.DB
		if tbl.SourceDB == "supportflast" {
			srcDB = portalDB
		} else {
			srcDB = cpDB
		}

		var countSQLite int
		_ = srcDB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM `%s`", tbl.SourceTbl)).Scan(&countSQLite)

		// Với users, SQLite chỉ còn 1
		var countTiDB int
		_ = tidbDB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM `%s`", tbl.Name)).Scan(&countTiDB)

		status := "✅ KHỚP 100%"
		if countSQLite != countTiDB {
			status = fmt.Sprintf("⚠️ KHÁC (%d vs %d)", countSQLite, countTiDB)
		}

		totalSQLite += countSQLite
		totalTiDB += countTiDB
		fmt.Printf("%-24s | %-12d | %-12d | %-15s\n", tbl.Name, countSQLite, countTiDB, status)
	}

	fmt.Println(strings.Repeat("-", 72))
	fmt.Printf("%-24s | %-12d | %-12d | ✨ ĐỒNG BỘ XONG\n", "TỔNG CỘNG TOÀN HỆ THỐNG", totalSQLite, totalTiDB)
}

func checkTableExistsSQLite(db *sql.DB, tableName string) (bool, error) {
	var count int
	query := "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?;"
	err := db.QueryRow(query, tableName).Scan(&count)
	return count > 0, err
}

func resolvePath(rel string) string {
	candidates := []string{
		rel,
		filepath.Join("..", rel),
		filepath.Join("..", "..", rel),
		filepath.Join(`f:\supportflast.dev`, rel),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return rel
}

func resolveEnvPath(custom string) string {
	if custom != "" {
		if _, err := os.Stat(custom); err == nil {
			return custom
		}
	}
	candidates := []string{
		".env",
		filepath.Join("..", ".env"),
		filepath.Join("..", "..", ".env"),
		`f:\supportflast.dev\.env`,
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func loadEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
	return scanner.Err()
}

func getEnvOrDefault(envKey, flagVal, defaultVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return defaultVal
}

func ensureDatabaseExistsOnTiDB(cfg Config) {
	if cfg.TiDBDatabase == "" || cfg.TiDBDatabase == "test" || cfg.TiDBDatabase == "sys" {
		return
	}
	tlsParam := "false"
	if strings.ToLower(cfg.TiDBTLS) == "true" || cfg.TiDBTLS == "1" {
		tlsParam = "true"
	}
	defaultDBs := []string{"test", "sys", ""}
	for _, defaultDB := range defaultDBs {
		rootDSN := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?tls=%s&charset=utf8mb4&timeout=15s",
			cfg.TiDBUser, cfg.TiDBPassword, cfg.TiDBHost, cfg.TiDBPort, defaultDB, tlsParam)
		db, err := sql.Open("mysql", rootDSN)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = db.PingContext(ctx)
		if err == nil {
			query := fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;", cfg.TiDBDatabase)
			_, _ = db.ExecContext(ctx, query)
			cancel()
			db.Close()
			return
		}
		cancel()
		db.Close()
	}
}
