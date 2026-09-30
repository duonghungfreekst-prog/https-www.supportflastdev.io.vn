package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// =========================================================================================
// TiDB Cloud / MySQL 8.0 / 5.7 Wire Protocol DDL Schema
// Tương thích chuẩn hệ quản trị cơ sở dữ liệu phân tán TiDB Cloud & MySQL
// Mã hóa: utf8mb4 | Collation: utf8mb4_unicode_ci | Storage Engine: InnoDB / TiKV
// =========================================================================================

// TiDBCreateUsersTable DDL tạo bảng users: Quản lý người dùng, định danh, phân quyền và xác thực
const TiDBCreateUsersTable = `
CREATE TABLE IF NOT EXISTS ` + "`users`" + ` (
    ` + "`id`" + ` VARCHAR(64) NOT NULL,
    ` + "`username`" + ` VARCHAR(64) NOT NULL,
    ` + "`email`" + ` VARCHAR(191) NOT NULL,
    ` + "`password_hash`" + ` VARCHAR(255) NOT NULL,
    ` + "`display_name`" + ` VARCHAR(128) DEFAULT NULL,
    ` + "`role`" + ` VARCHAR(32) NOT NULL DEFAULT 'user',
    ` + "`avatar`" + ` VARCHAR(512) DEFAULT NULL,
    ` + "`created_at`" + ` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ` + "`updated_at`" + ` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    ` + "`last_login`" + ` DATETIME DEFAULT NULL,
    PRIMARY KEY (` + "`id`" + `),
    UNIQUE KEY ` + "`uk_users_username`" + ` (` + "`username`" + `),
    UNIQUE KEY ` + "`uk_users_email`" + ` (` + "`email`" + `),
    INDEX ` + "`idx_users_role`" + ` (` + "`role`" + `),
    INDEX ` + "`idx_users_created_at`" + ` (` + "`created_at`" + `)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
`

// TiDBCreateAppsTable DDL tạo bảng apps: Lưu trữ siêu dữ liệu phần mềm phát hành trên kho SupportFlast
const TiDBCreateAppsTable = `
CREATE TABLE IF NOT EXISTS ` + "`apps`" + ` (
    ` + "`id`" + ` VARCHAR(64) NOT NULL,
    ` + "`name`" + ` VARCHAR(191) NOT NULL,
    ` + "`version`" + ` VARCHAR(64) NOT NULL,
    ` + "`platform`" + ` VARCHAR(64) DEFAULT NULL,
    ` + "`category`" + ` VARCHAR(64) DEFAULT NULL,
    ` + "`desc`" + ` TEXT DEFAULT NULL,
    ` + "`file_name`" + ` VARCHAR(255) DEFAULT NULL,
    ` + "`size_bytes`" + ` BIGINT NOT NULL DEFAULT 0,
    ` + "`size_formatted`" + ` VARCHAR(64) DEFAULT NULL,
    ` + "`sha256`" + ` VARCHAR(64) DEFAULT NULL,
    ` + "`author`" + ` VARCHAR(128) DEFAULT NULL,
    ` + "`downloads`" + ` INT NOT NULL DEFAULT 0,
    ` + "`status`" + ` VARCHAR(32) NOT NULL DEFAULT 'published',
    ` + "`published_at`" + ` DATETIME DEFAULT NULL,
    ` + "`download_url`" + ` VARCHAR(512) DEFAULT NULL,
    ` + "`video_url`" + ` VARCHAR(512) DEFAULT NULL,
    ` + "`guide`" + ` TEXT DEFAULT NULL,
    ` + "`user_id`" + ` VARCHAR(64) DEFAULT NULL,
    PRIMARY KEY (` + "`id`" + `),
    INDEX ` + "`idx_apps_user_id`" + ` (` + "`user_id`" + `),
    INDEX ` + "`idx_apps_status`" + ` (` + "`status`" + `),
    INDEX ` + "`idx_apps_category`" + ` (` + "`category`" + `),
    INDEX ` + "`idx_apps_platform`" + ` (` + "`platform`" + `),
    INDEX ` + "`idx_apps_published_at`" + ` (` + "`published_at`" + `),
    INDEX ` + "`idx_apps_downloads`" + ` (` + "`downloads`" + `),
    CONSTRAINT ` + "`fk_apps_user_id`" + ` FOREIGN KEY (` + "`user_id`" + `) REFERENCES ` + "`users`" + ` (` + "`id`" + `) ON DELETE SET NULL ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
`

// TiDBCreateAPIKeysTable DDL tạo bảng api_keys: Quản lý khóa API phân quyền lập trình viên & hệ thống
const TiDBCreateAPIKeysTable = `
CREATE TABLE IF NOT EXISTS ` + "`api_keys`" + ` (
    ` + "`id`" + ` VARCHAR(64) NOT NULL,
    ` + "`user_id`" + ` VARCHAR(64) DEFAULT NULL,
    ` + "`name`" + ` VARCHAR(128) DEFAULT NULL,
    ` + "`key_hash`" + ` VARCHAR(191) NOT NULL,
    ` + "`prefix`" + ` VARCHAR(32) DEFAULT NULL,
    ` + "`status`" + ` VARCHAR(32) NOT NULL DEFAULT 'active',
    ` + "`permissions`" + ` TEXT DEFAULT NULL,
    ` + "`created_at`" + ` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (` + "`id`" + `),
    UNIQUE KEY ` + "`uk_api_keys_key_hash`" + ` (` + "`key_hash`" + `),
    INDEX ` + "`idx_api_keys_user_id`" + ` (` + "`user_id`" + `),
    INDEX ` + "`idx_api_keys_prefix`" + ` (` + "`prefix`" + `),
    INDEX ` + "`idx_api_keys_status`" + ` (` + "`status`" + `),
    INDEX ` + "`idx_api_keys_created_at`" + ` (` + "`created_at`" + `),
    CONSTRAINT ` + "`fk_api_keys_user_id`" + ` FOREIGN KEY (` + "`user_id`" + `) REFERENCES ` + "`users`" + ` (` + "`id`" + `) ON DELETE CASCADE ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
`

// TiDBCreateReviewsTable DDL tạo bảng reviews: Nhận xét và đánh giá sao ứng dụng từ cộng đồng
const TiDBCreateReviewsTable = `
CREATE TABLE IF NOT EXISTS ` + "`reviews`" + ` (
    ` + "`id`" + ` VARCHAR(64) NOT NULL,
    ` + "`app_id`" + ` VARCHAR(64) NOT NULL,
    ` + "`user_id`" + ` VARCHAR(64) DEFAULT NULL,
    ` + "`author_name`" + ` VARCHAR(128) NOT NULL,
    ` + "`author_role`" + ` VARCHAR(64) DEFAULT NULL,
    ` + "`stars`" + ` INT NOT NULL DEFAULT 5,
    ` + "`text`" + ` TEXT NOT NULL,
    ` + "`status`" + ` VARCHAR(32) NOT NULL DEFAULT 'approved',
    ` + "`created_at`" + ` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (` + "`id`" + `),
    INDEX ` + "`idx_reviews_app_id`" + ` (` + "`app_id`" + `),
    INDEX ` + "`idx_reviews_user_id`" + ` (` + "`user_id`" + `),
    INDEX ` + "`idx_reviews_status`" + ` (` + "`status`" + `),
    INDEX ` + "`idx_reviews_created_at`" + ` (` + "`created_at`" + `),
    CONSTRAINT ` + "`fk_reviews_app_id`" + ` FOREIGN KEY (` + "`app_id`" + `) REFERENCES ` + "`apps`" + ` (` + "`id`" + `) ON DELETE CASCADE ON UPDATE CASCADE,
    CONSTRAINT ` + "`fk_reviews_user_id`" + ` FOREIGN KEY (` + "`user_id`" + `) REFERENCES ` + "`users`" + ` (` + "`id`" + `) ON DELETE SET NULL ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
`

// TiDBCreateAuditLogsTable DDL tạo bảng audit_logs: Nhật ký kiểm toán bảo mật, lưu vết thao tác hệ thống
const TiDBCreateAuditLogsTable = `
CREATE TABLE IF NOT EXISTS ` + "`audit_logs`" + ` (
    ` + "`id`" + ` VARCHAR(64) NOT NULL,
    ` + "`user_id`" + ` VARCHAR(64) DEFAULT NULL,
    ` + "`action`" + ` VARCHAR(64) NOT NULL,
    ` + "`ip_address`" + ` VARCHAR(45) DEFAULT NULL,
    ` + "`user_agent`" + ` VARCHAR(512) DEFAULT NULL,
    ` + "`details`" + ` TEXT DEFAULT NULL,
    ` + "`created_at`" + ` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (` + "`id`" + `),
    INDEX ` + "`idx_audit_logs_user_id`" + ` (` + "`user_id`" + `),
    INDEX ` + "`idx_audit_logs_action`" + ` (` + "`action`" + `),
    INDEX ` + "`idx_audit_logs_created_at`" + ` (` + "`created_at`" + `),
    CONSTRAINT ` + "`fk_audit_logs_user_id`" + ` FOREIGN KEY (` + "`user_id`" + `) REFERENCES ` + "`users`" + ` (` + "`id`" + `) ON DELETE SET NULL ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
`

// TiDBCreateSystemReleasesTable DDL tạo bảng system_releases: Phiên bản cập nhật và build hash của nền tảng
const TiDBCreateSystemReleasesTable = `
CREATE TABLE IF NOT EXISTS ` + "`system_releases`" + ` (
    ` + "`version`" + ` VARCHAR(64) NOT NULL,
    ` + "`title`" + ` VARCHAR(191) NOT NULL,
    ` + "`date`" + ` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ` + "`build_hash`" + ` VARCHAR(128) DEFAULT NULL,
    ` + "`notes`" + ` TEXT DEFAULT NULL,
    ` + "`published_by`" + ` VARCHAR(128) DEFAULT NULL,
    PRIMARY KEY (` + "`version`" + `),
    INDEX ` + "`idx_system_releases_date`" + ` (` + "`date`" + `)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
`

// TiDBCreateSecurityEventsTable DDL tạo bảng security_events: Lưu trữ sự kiện an ninh, IP bị chặn và thời hạn
const TiDBCreateSecurityEventsTable = `
CREATE TABLE IF NOT EXISTS ` + "`security_events`" + ` (
    ` + "`id`" + ` VARCHAR(64) NOT NULL,
    ` + "`event_type`" + ` VARCHAR(64) NOT NULL,
    ` + "`ip_address`" + ` VARCHAR(45) NOT NULL,
    ` + "`severity`" + ` VARCHAR(32) NOT NULL DEFAULT 'warning',
    ` + "`details`" + ` TEXT DEFAULT NULL,
    ` + "`blocked_until`" + ` DATETIME DEFAULT NULL,
    ` + "`created_at`" + ` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (` + "`id`" + `),
    INDEX ` + "`idx_security_events_ip`" + ` (` + "`ip_address`" + `),
    INDEX ` + "`idx_security_events_type`" + ` (` + "`event_type`" + `),
    INDEX ` + "`idx_security_events_severity`" + ` (` + "`severity`" + `),
    INDEX ` + "`idx_security_events_created_at`" + ` (` + "`created_at`" + `),
    INDEX ` + "`idx_security_events_blocked_until`" + ` (` + "`blocked_until`" + `)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
`

// TiDBCreateRevokedTokensTable DDL tạo bảng revoked_tokens: Danh sách token JWT RS256 bị thu hồi
const TiDBCreateRevokedTokensTable = `
CREATE TABLE IF NOT EXISTS ` + "`revoked_tokens`" + ` (
    ` + "`token_hash`" + ` VARCHAR(191) NOT NULL,
    ` + "`expires_at`" + ` DATETIME NOT NULL,
    ` + "`revoked_at`" + ` DATETIME NOT NULL,
    PRIMARY KEY (` + "`token_hash`" + `),
    INDEX ` + "`idx_revoked_tokens_expires`" + ` (` + "`expires_at`" + `)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
`

// TableMigration định nghĩa một bước khởi tạo bảng trong tiến trình di trú
type TableMigration struct {
	Name string
	DDL  string
}

// TiDBTableMigrations danh sách tuần tự các bảng cần khởi tạo theo thứ tự phụ thuộc khóa ngoại
var TiDBTableMigrations = []TableMigration{
	{Name: "users", DDL: TiDBCreateUsersTable},
	{Name: "apps", DDL: TiDBCreateAppsTable},
	{Name: "api_keys", DDL: TiDBCreateAPIKeysTable},
	{Name: "reviews", DDL: TiDBCreateReviewsTable},
	{Name: "audit_logs", DDL: TiDBCreateAuditLogsTable},
	{Name: "system_releases", DDL: TiDBCreateSystemReleasesTable},
	{Name: "security_events", DDL: TiDBCreateSecurityEventsTable},
	{Name: "revoked_tokens", DDL: TiDBCreateRevokedTokensTable},
}

// TiDBFullSchemaDDL toàn bộ DDL hợp nhất tương thích MySQL/TiDB
var TiDBFullSchemaDDL = strings.Join([]string{
	TiDBCreateUsersTable,
	TiDBCreateAppsTable,
	TiDBCreateAPIKeysTable,
	TiDBCreateReviewsTable,
	TiDBCreateAuditLogsTable,
	TiDBCreateSystemReleasesTable,
	TiDBCreateSecurityEventsTable,
	TiDBCreateRevokedTokensTable,
}, "\n\n")

// MigrateTiDBSchema thực thi di trú Schema DDL tương thích chuẩn TiDB Cloud (MySQL 8.0 / 5.7 wire protocol).
// Thiết lập cấu hình charset utf8mb4, kiểm tra kết nối và tạo tuần tự 7 bảng:
// users, apps, api_keys, reviews, audit_logs, system_releases, security_events.
func MigrateTiDBSchema(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("kết nối database TiDB là nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1. Kiểm tra liveness kết nối tới cluster TiDB
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("không thể ping tới cụm TiDB Cloud: %w", err)
	}

	// 2. Thiết lập session charset utf8mb4 chuẩn
	if _, err := db.ExecContext(ctx, "SET NAMES utf8mb4;"); err != nil {
		log.Printf("[ENGINE] [DATABASE] [TIDB] [WARN] Cảnh báo khi đặt SET NAMES utf8mb4: %v", err)
	}

	// 3. Khởi tạo tuần tự từng bảng theo thứ tự phụ thuộc khóa ngoại
	for _, m := range TiDBTableMigrations {
		start := time.Now()
		cleanDDL := strings.TrimSpace(m.DDL)
		if _, err := db.ExecContext(ctx, cleanDDL); err != nil {
			return fmt.Errorf("thất bại khi tạo bảng '%s' trên TiDB: %w", m.Name, err)
		}
		duration := time.Since(start)
		log.Printf("[ENGINE] [DATABASE] [TIDB] Khởi tạo thành công bảng '%s' (thời gian: %v, charset: utf8mb4, engine: InnoDB/TiKV)", m.Name, duration)
	}

	// 4. Khởi tạo tài khoản admin chuẩn nếu bảng users rỗng
	if err := SeedInitialTiDBAdmin(db); err != nil {
		log.Printf("[ENGINE] [DATABASE] [TIDB] [WARN] SeedInitialTiDBAdmin gặp cảnh báo: %v", err)
	}

	log.Printf("[ENGINE] [DATABASE] [TIDB] Hoàn tất di trú toàn bộ 7 bảng TiDB Cloud Schema thành công")
	return nil
}

// VerifyTiDBSchema kiểm tra sự tồn tại của đầy đủ 7 bảng trong cơ sở dữ liệu hiện hành
func VerifyTiDBSchema(db *sql.DB, databaseName string) ([]string, error) {
	if db == nil {
		return nil, fmt.Errorf("kết nối database TiDB là nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	query := `
		SELECT table_name 
		FROM information_schema.tables 
		WHERE table_schema = ? AND table_type = 'BASE TABLE'
	`
	rows, err := db.QueryContext(ctx, query, databaseName)
	if err != nil {
		return nil, fmt.Errorf("thất bại khi truy vấn information_schema.tables: %w", err)
	}
	defer rows.Close()

	existingTables := make(map[string]bool)
	for rows.Next() {
		var tbl string
		if err := rows.Scan(&tbl); err != nil {
			return nil, err
		}
		existingTables[strings.ToLower(tbl)] = true
	}

	missing := make([]string, 0)
	for _, m := range TiDBTableMigrations {
		if !existingTables[strings.ToLower(m.Name)] {
			missing = append(missing, m.Name)
		}
	}

	if len(missing) > 0 {
		return missing, fmt.Errorf("các bảng TiDB chưa tồn tại: %s", strings.Join(missing, ", "))
	}

	return nil, nil
}

