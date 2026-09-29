package database

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

// DefaultDBPath đường dẫn mặc định của cơ sở dữ liệu SQLite
const DefaultDBPath = `f:\supportflast.dev\data\supportflast.db`

// Hằng số tài khoản quản trị viên hệ thống khởi tạo chuẩn
const (
	DefaultAdminID          = "usr-admin-001"
	DefaultAdminUsername    = "admin"
	DefaultAdminEmail       = "admin@supportflastdev.io.vn"
	DefaultAdminPassword    = "Admin@2026!SupportFlast"
	DefaultAdminDisplayName = "Quản Trị Viên Hệ Thống"
	DefaultAdminRole        = "admin"
	DefaultAdminAvatar      = "/avatars/admin.png"
)

var (
	dbInstance   *sql.DB
	dbMutex      sync.RWMutex
	activeDriver atomic.Value
)

func init() {
	activeDriver.Store("sqlite")
}

// SchemaDDL danh sách câu lệnh DDL định nghĩa bảng và chỉ mục (INDEX)
const SchemaDDL = `
-- 1. Bảng users: Quản lý người dùng, phân quyền và xác thực
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    email TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    display_name TEXT,
    role TEXT NOT NULL DEFAULT 'user',
    avatar TEXT,
    created_at TEXT,
    updated_at TEXT,
    last_login TEXT
);

CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
CREATE INDEX IF NOT EXISTS idx_users_role ON users(role);
CREATE INDEX IF NOT EXISTS idx_users_created_at ON users(created_at);

-- 2. Bảng apps: Lưu trữ siêu dữ liệu phần mềm phát hành trên kho SupportFlast
CREATE TABLE IF NOT EXISTS apps (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    version TEXT NOT NULL,
    platform TEXT,
    category TEXT,
    desc TEXT,
    file_name TEXT,
    size_bytes INTEGER,
    size_formatted TEXT,
    sha256 TEXT,
    author TEXT,
    downloads INTEGER DEFAULT 0,
    status TEXT DEFAULT 'published',
    published_at TEXT,
    download_url TEXT,
    video_url TEXT,
    guide TEXT,
    user_id TEXT,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_apps_user_id ON apps(user_id);
CREATE INDEX IF NOT EXISTS idx_apps_status ON apps(status);
CREATE INDEX IF NOT EXISTS idx_apps_category ON apps(category);
CREATE INDEX IF NOT EXISTS idx_apps_platform ON apps(platform);
CREATE INDEX IF NOT EXISTS idx_apps_published_at ON apps(published_at);
CREATE INDEX IF NOT EXISTS idx_apps_downloads ON apps(downloads DESC);

-- 3. Bảng api_keys: Quản lý khóa API phân quyền cho lập trình viên & hệ thống
CREATE TABLE IF NOT EXISTS api_keys (
    id TEXT PRIMARY KEY,
    user_id TEXT,
    name TEXT,
    key_hash TEXT UNIQUE,
    prefix TEXT,
    status TEXT DEFAULT 'active',
    permissions TEXT,
    created_at TEXT,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_api_keys_user_id ON api_keys(user_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_key_hash ON api_keys(key_hash);
CREATE INDEX IF NOT EXISTS idx_api_keys_prefix ON api_keys(prefix);
CREATE INDEX IF NOT EXISTS idx_api_keys_status ON api_keys(status);

-- 4. Bảng reviews: Nhận xét và đánh giá sao ứng dụng từ cộng đồng
CREATE TABLE IF NOT EXISTS reviews (
    id TEXT PRIMARY KEY,
    app_id TEXT,
    user_id TEXT,
    author_name TEXT NOT NULL,
    author_role TEXT,
    stars INTEGER NOT NULL,
    text TEXT NOT NULL,
    status TEXT DEFAULT 'approved',
    created_at TEXT,
    FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_reviews_app_id ON reviews(app_id);
CREATE INDEX IF NOT EXISTS idx_reviews_user_id ON reviews(user_id);
CREATE INDEX IF NOT EXISTS idx_reviews_status ON reviews(status);
CREATE INDEX IF NOT EXISTS idx_reviews_created_at ON reviews(created_at);

-- 5. Bảng audit_logs: Nhật ký kiểm toán bảo mật, lưu vết thao tác hệ thống
CREATE TABLE IF NOT EXISTS audit_logs (
    id TEXT PRIMARY KEY,
    user_id TEXT,
    action TEXT NOT NULL,
    ip_address TEXT,
    user_agent TEXT,
    details TEXT,
    created_at TEXT,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_user_id ON audit_logs(user_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_action ON audit_logs(action);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs(created_at DESC);

-- 6. Bảng system_releases: Phiên bản cập nhật và build hash của nền tảng
CREATE TABLE IF NOT EXISTS system_releases (
    version TEXT PRIMARY KEY,
    title TEXT,
    date TEXT,
    build_hash TEXT,
    notes TEXT,
    published_by TEXT
);

CREATE INDEX IF NOT EXISTS idx_system_releases_date ON system_releases(date DESC);

-- 7. Bảng security_events: Lưu trữ các sự kiện đe dọa an ninh, IP bị chặn, thời gian hết hạn
CREATE TABLE IF NOT EXISTS security_events (
    id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    ip_address TEXT NOT NULL,
    severity TEXT NOT NULL DEFAULT 'warning',
    details TEXT,
    blocked_until TEXT,
    created_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_security_events_ip ON security_events(ip_address);
CREATE INDEX IF NOT EXISTS idx_security_events_type ON security_events(event_type);
CREATE INDEX IF NOT EXISTS idx_security_events_created_at ON security_events(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_security_events_blocked_until ON security_events(blocked_until);
`

// User cấu trúc thực thể người dùng
type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	Email        string `json:"email"`
	PasswordHash string `json:"-"`
	DisplayName  string `json:"display_name"`
	Role         string `json:"role"`
	Avatar       string `json:"avatar"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	LastLogin    string `json:"last_login"`
}

// App cấu trúc thực thể ứng dụng
type App struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Version       string `json:"version"`
	Platform      string `json:"platform"`
	Category      string `json:"category"`
	Desc          string `json:"desc"`
	FileName      string `json:"file_name"`
	SizeBytes     int64  `json:"size_bytes"`
	SizeFormatted string `json:"size_formatted"`
	SHA256        string `json:"sha256"`
	Author        string `json:"author"`
	Downloads     int    `json:"downloads"`
	Status        string `json:"status"`
	PublishedAt   string `json:"published_at"`
	DownloadURL   string `json:"download_url"`
	VideoURL      string `json:"video_url"`
	Guide         string `json:"guide"`
	UserID        string `json:"user_id"`
}

// APIKey cấu trúc khóa API
type APIKey struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	Name        string `json:"name"`
	KeyHash     string `json:"key_hash"`
	Prefix      string `json:"prefix"`
	Status      string `json:"status"`
	Permissions string `json:"permissions"`
	CreatedAt   string `json:"created_at"`
}

// Review cấu trúc nhận xét đánh giá ứng dụng
type Review struct {
	ID         string `json:"id"`
	AppID      string `json:"app_id"`
	UserID     string `json:"user_id"`
	AuthorName string `json:"author_name"`
	AuthorRole string `json:"author_role"`
	Stars      int    `json:"stars"`
	Text       string `json:"text"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
}

// AuditLog cấu trúc nhật ký bảo mật
type AuditLog struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	Action    string `json:"action"`
	IPAddress string `json:"ip_address"`
	UserAgent string `json:"user_agent"`
	Details   string `json:"details"`
	CreatedAt string `json:"created_at"`
}

// SecurityEvent cấu trúc sự kiện an ninh và phòng thủ
type SecurityEvent struct {
	ID           string `json:"id"`
	EventType    string `json:"event_type"`
	IPAddress    string `json:"ip_address"`
	Severity     string `json:"severity"`
	Details      string `json:"details"`
	BlockedUntil string `json:"blocked_until,omitempty"`
	CreatedAt    string `json:"created_at"`
}

// Hằng số định danh hành động ghi nhật ký kiểm toán (Audit Log Actions)
const (
	AuditActionLoginSuccess   = "login_success"
	AuditActionLoginFailure   = "login_failure"
	AuditActionLogout         = "logout"
	AuditActionChangePassword = "change_password"
	AuditActionChangePIN      = "change_pin"
	AuditActionHoneypotAccess = "honeypot_access"
	AuditActionConfigChange   = "config_change"
	AuditActionSettingsUpdate = "settings_update"
)

// SystemRelease cấu trúc bản cập nhật hệ thống
type SystemRelease struct {
	Version     string `json:"version"`
	Title       string `json:"title"`
	Date        string `json:"date"`
	BuildHash   string `json:"build_hash"`
	Notes       string `json:"notes"`
	PublishedBy string `json:"published_by"`
}


// ResolveDBPath xác định đường dẫn file cơ sở dữ liệu SQLite linh hoạt
// Ưu tiên:
// 1. Tham số customPath (nếu được truyền vào)
// 2. Biến môi trường DATA_DIR (filepath.Join(DATA_DIR, "supportflast.db"))
// 3. Các thư mục ứng viên: "../data/supportflast.db", "data/supportflast.db", "f:\supportflast.dev\data\supportflast.db"
func ResolveDBPath(customPath ...string) string {
	if len(customPath) > 0 && strings.TrimSpace(customPath[0]) != "" {
		return customPath[0]
	}

	if envDBPath := strings.TrimSpace(os.Getenv("DB_PATH")); envDBPath != "" {
		return envDBPath
	}

	if envDataDir := strings.TrimSpace(os.Getenv("DATA_DIR")); envDataDir != "" {
		return filepath.Join(envDataDir, "supportflast.db")
	}

	candidates := []string{
		filepath.Join("..", "data", "supportflast.db"),
		filepath.Join("data", "supportflast.db"),
		`f:\supportflast.dev\data\supportflast.db`,
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}

	// Mặc định tạo tại ../data hoặc data
	if info, err := os.Stat(".."); err == nil && info.IsDir() {
		return filepath.Join("..", "data", "supportflast.db")
	}
	return filepath.Join("data", "supportflast.db")
}

// isMySQLOrTiDB kiểm tra xem driver hiện hành, kết nối db hoặc biến môi trường DB_DRIVER có phải là tidb hoặc mysql hay không (Lock-free)
func isMySQLOrTiDB(db ...*sql.DB) bool {
	if len(db) > 0 && db[0] != nil {
		driverType := strings.ToLower(fmt.Sprintf("%T", db[0].Driver()))
		if strings.Contains(driverType, "mysql") || strings.Contains(driverType, "tidb") {
			return true
		}
	}
	drv := ActiveDriver()
	return drv == "tidb" || drv == "mysql"
}

// ActiveDriver trả về loại cơ sở dữ liệu hiện hành ("sqlite", "tidb", "mysql", hoặc "") (Lock-free)
func ActiveDriver() string {
	if v, ok := activeDriver.Load().(string); ok && v != "" {
		return v
	}
	d := strings.ToLower(strings.TrimSpace(os.Getenv("DB_DRIVER")))
	if d == "tidb" || d == "mysql" {
		return d
	}
	return "sqlite"
}

// SetDBInstance gán con trỏ kết nối DB và cập nhật activeDriver phục vụ unit test và tích hợp
func SetDBInstance(db *sql.DB, driver ...string) {
	dbMutex.Lock()
	defer dbMutex.Unlock()
	dbInstance = db
	drv := "sqlite"
	if len(driver) > 0 && strings.TrimSpace(driver[0]) != "" {
		drv = strings.ToLower(strings.TrimSpace(driver[0]))
	}
	activeDriver.Store(drv)
	if drv == "tidb" || drv == "mysql" {
		SetTiDBInstance(db)
	}
}

// InitSQLite khởi tạo kết nối cơ sở dữ liệu SQLite thread-safe
// Nếu truyền customPath thì sử dụng đường dẫn đó (hữu ích cho unit test),
// nếu không truyền thì tự động phân giải qua ResolveDBPath().
func InitSQLite(customPath ...string) (*sql.DB, error) {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	if dbInstance != nil && ActiveDriver() == "sqlite" {
		return dbInstance, nil
	}

	activeDriver.Store("sqlite")
	dbPath := ResolveDBPath(customPath...)

	db, err := openDatabaseLocked(dbPath)
	if err != nil {
		return nil, err
	}

	dbInstance = db
	return dbInstance, nil
}

// InitDB khởi tạo kết nối cơ sở dữ liệu thread-safe theo cấu hình DB_DRIVER:
// - Kiểm tra biến môi trường DB_DRIVER:
//   * Nếu DB_DRIVER=tidb hoặc mysql: gọi InitTiDB()
//   * Nếu DB_DRIVER=sqlite hoặc để trống: gọi InitSQLite(customPath...)
// - Nếu có truyền customPath cụ thể (hữu ích cho unit test cục bộ), ưu tiên gọi InitSQLite(customPath...).
func InitDB(customPath ...string) (*sql.DB, error) {
	// Nếu truyền customPath hợp lệ (thường dùng trong unit test), ưu tiên khởi tạo SQLite theo đường dẫn đó
	if len(customPath) > 0 && strings.TrimSpace(customPath[0]) != "" {
		return InitSQLite(customPath[0])
	}

	driver := strings.ToLower(strings.TrimSpace(os.Getenv("DB_DRIVER")))
	if driver == "tidb" || driver == "mysql" {
		db, err := InitTiDB()
		if err != nil {
			return nil, err
		}
		dbMutex.Lock()
		dbInstance = db
		activeDriver.Store(driver)
		dbMutex.Unlock()
		return db, nil
	}

	return InitSQLite()
}

// GetDB trả về con trỏ kết nối *sql.DB thread-safe cho các module khác truy cập.
// Nếu chưa gọi InitDB, hàm sẽ tự động phân giải biến DB_DRIVER và khởi tạo kết nối thích hợp.
func GetDB() *sql.DB {
	dbMutex.RLock()
	if dbInstance != nil {
		defer dbMutex.RUnlock()
		return dbInstance
	}
	dbMutex.RUnlock()

	// Tự động khởi tạo kết nối dựa theo DB_DRIVER (SQLite hoặc TiDB/MySQL)
	db, err := InitDB()
	if err != nil {
		log.Printf("[ENGINE] [DATABASE] [ERROR] Failed to auto-initialize DB in GetDB: %v", err)
		return nil
	}
	return db
}

// CheckpointWAL thực hiện checkpoint và truncate file journal WAL về database chính (chỉ áp dụng cho SQLite)
func CheckpointWAL() error {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	if dbInstance != nil && ActiveDriver() == "sqlite" {
		_, err := dbInstance.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
		return err
	}
	return nil
}

// CloseDB đóng kết nối cơ sở dữ liệu an toàn khi tắt Engine
func CloseDB() error {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	var lastErr error
	if dbInstance != nil {
		if ActiveDriver() == "sqlite" {
			_, _ = dbInstance.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
		}
		if err := dbInstance.Close(); err != nil {
			lastErr = err
		}
		dbInstance = nil
		activeDriver.Store("")
	}

	// Đóng đồng thời instance TiDB nếu đang mở độc lập
	_ = CloseTiDB()
	return lastErr
}

// SeedInitialData kiểm tra bảng users. Nếu rỗng, tạo sẵn tài khoản Admin chuẩn:
// username='admin', email='admin@supportflastdev.io.vn', password_hash (BCrypt cost 12),
// role='admin', display_name='Quản Trị Viên Hệ Thống'.
// Hỗ trợ đồng nhất cả SQLite và TiDB/MySQL.
// TUYỆT ĐỐI KHÔNG seed app rác hoặc review giả lập (Tuân thủ nghiêm ngặt Rule 9.1).
func SeedInitialData(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}

	stmtCount, err := db.Prepare("SELECT COUNT(*) FROM users")
	if err != nil {
		return fmt.Errorf("failed to prepare users count query: %w", err)
	}
	defer stmtCount.Close()

	var userCount int
	err = stmtCount.QueryRow().Scan(&userCount)
	if err != nil {
		return fmt.Errorf("failed to query users count: %w", err)
	}

	// Kiểm tra trước khi chèn tài khoản admin để tương thích 100% cả SQLite lẫn TiDB MySQL (tránh duplicate key)
	var adminExists int
	_ = db.QueryRow("SELECT COUNT(*) FROM users WHERE id = ? OR username = ?", DefaultAdminID, DefaultAdminUsername).Scan(&adminExists)
	if adminExists > 0 {
		return nil // Đã tồn tại tài khoản admin chuẩn, không chèn lại
	}

	// Băm mật khẩu quản trị viên với BCrypt cost 12
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(DefaultAdminPassword), 12)
	if err != nil {
		return fmt.Errorf("failed to hash default admin password: %w", err)
	}

	var now string
	var lastLoginVal interface{}
	var query string

	if isMySQLOrTiDB(db) {
		now = time.Now().UTC().Format("2006-01-02 15:04:05")
		lastLoginVal = nil // MySQL/TiDB DATETIME không nhận chuỗi rỗng '' trong STRICT mode
		query = `
			INSERT IGNORE INTO users (
				id, username, email, password_hash, display_name, role, avatar, created_at, updated_at, last_login
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`
	} else {
		now = time.Now().UTC().Format(time.RFC3339)
		lastLoginVal = ""
		query = `
			INSERT OR IGNORE INTO users (
				id, username, email, password_hash, display_name, role, avatar, created_at, updated_at, last_login
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`
	}

	stmtInsert, err := db.Prepare(query)
	if err != nil {
		return fmt.Errorf("failed to prepare admin insert query: %w", err)
	}
	defer stmtInsert.Close()

	_, err = stmtInsert.Exec(
		DefaultAdminID,
		DefaultAdminUsername,
		DefaultAdminEmail,
		string(hashedPassword),
		DefaultAdminDisplayName,
		DefaultAdminRole,
		DefaultAdminAvatar,
		now,
		now,
		lastLoginVal,
	)
	if err != nil {
		return fmt.Errorf("failed to insert initial admin user: %w", err)
	}

	log.Printf("[ENGINE] [DATABASE] Initial admin user successfully seeded: username='%s', email='%s', role='%s'",
		DefaultAdminUsername, DefaultAdminEmail, DefaultAdminRole)
	return nil
}

var (
	auditLogSeq      uint64
	securityEventSeq uint64
)

func generateLogID(prefix string, seq *uint64) string {
	cnt := atomic.AddUint64(seq, 1)
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%d-%06d-%s", prefix, time.Now().UnixNano(), cnt%1000000, hex.EncodeToString(b))
}

// RecordAuditLog lưu vết hành động bảo mật vào bảng audit_logs bằng Prepared Statement chống SQL Injection tuyệt đối
// Hỗ trợ lưu vết: đăng nhập, đổi mật khẩu, đổi mã PIN, truy cập Honeypot, thay đổi cấu hình
// Hoạt động đồng nhất cho cả SQLite và TiDB/MySQL
func RecordAuditLog(userID, action, ipAddress, userAgent, details string) error {
	db := GetDB()
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}

	query := `
		INSERT INTO audit_logs (id, user_id, action, ip_address, user_agent, details, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`
	stmt, err := db.Prepare(query)
	if err != nil {
		return fmt.Errorf("failed to prepare audit log insert: %w", err)
	}
	defer stmt.Close()

	id := generateLogID("aud", &auditLogSeq)
	var now string
	if isMySQLOrTiDB() {
		now = time.Now().UTC().Format("2006-01-02 15:04:05")
	} else {
		now = time.Now().UTC().Format(time.RFC3339)
	}

	var uid *string
	if strings.TrimSpace(userID) != "" {
		trimmed := strings.TrimSpace(userID)
		// Kiểm tra user có tồn tại để thỏa mãn ràng buộc khóa ngoại (Foreign Keys ON)
		var userCount int
		_ = db.QueryRow("SELECT COUNT(*) FROM users WHERE id = ?", trimmed).Scan(&userCount)
		if userCount > 0 {
			uid = &trimmed
		}
	}

	cleanIP := strings.ReplaceAll(strings.ReplaceAll(ipAddress, "\n", ""), "\r", "")
	cleanUA := strings.ReplaceAll(strings.ReplaceAll(userAgent, "\n", ""), "\r", "")
	cleanDetails := strings.ReplaceAll(strings.ReplaceAll(details, "\n", " "), "\r", "")

	_, err = stmt.Exec(id, uid, action, cleanIP, cleanUA, cleanDetails, now)
	if err != nil {
		return fmt.Errorf("failed to execute audit log insert: %w", err)
	}
	return nil
}

// GetAuditLogs lấy danh sách nhật ký kiểm toán mới nhất bằng Prepared Statement
func GetAuditLogs(limit, offset int) ([]AuditLog, error) {
	db := GetDB()
	if db == nil {
		return nil, fmt.Errorf("database connection is nil")
	}

	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}

	query := `
		SELECT id, COALESCE(user_id, ''), action, COALESCE(ip_address, ''), 
		       COALESCE(user_agent, ''), COALESCE(details, ''), created_at
		FROM audit_logs
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`
	stmt, err := db.Prepare(query)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare audit logs query: %w", err)
	}
	defer stmt.Close()

	rows, err := stmt.Query(limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query audit logs: %w", err)
	}
	defer rows.Close()

	logs := make([]AuditLog, 0)
	for rows.Next() {
		var l AuditLog
		if err := rows.Scan(&l.ID, &l.UserID, &l.Action, &l.IPAddress, &l.UserAgent, &l.Details, &l.CreatedAt); err != nil {
			continue
		}
		logs = append(logs, l)
	}
	return logs, nil
}

// GetAuditLogsByAction lấy nhật ký kiểm toán theo loại hành động bằng Prepared Statement
func GetAuditLogsByAction(action string, limit, offset int) ([]AuditLog, error) {
	db := GetDB()
	if db == nil {
		return nil, fmt.Errorf("database connection is nil")
	}

	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}

	query := `
		SELECT id, COALESCE(user_id, ''), action, COALESCE(ip_address, ''), 
		       COALESCE(user_agent, ''), COALESCE(details, ''), created_at
		FROM audit_logs
		WHERE action = ?
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`
	stmt, err := db.Prepare(query)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare audit logs by action query: %w", err)
	}
	defer stmt.Close()

	rows, err := stmt.Query(action, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query audit logs by action: %w", err)
	}
	defer rows.Close()

	logs := make([]AuditLog, 0)
	for rows.Next() {
		var l AuditLog
		if err := rows.Scan(&l.ID, &l.UserID, &l.Action, &l.IPAddress, &l.UserAgent, &l.Details, &l.CreatedAt); err != nil {
			continue
		}
		logs = append(logs, l)
	}
	return logs, nil
}

// RecordSecurityEvent lưu trữ sự kiện đe dọa an ninh, IP bị chặn và thời gian hết hạn bằng Prepared Statement
// Hoạt động đồng nhất cho cả SQLite và TiDB/MySQL
func RecordSecurityEvent(eventType, ipAddress, severity, details string, blockedUntil *time.Time) error {
	db := GetDB()
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}

	query := `
		INSERT INTO security_events (id, event_type, ip_address, severity, details, blocked_until, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`
	stmt, err := db.Prepare(query)
	if err != nil {
		return fmt.Errorf("failed to prepare security event insert: %w", err)
	}
	defer stmt.Close()

	id := generateLogID("sec", &securityEventSeq)
	var now string
	var blockedVal interface{}

	if isMySQLOrTiDB() {
		now = time.Now().UTC().Format("2006-01-02 15:04:05")
		if blockedUntil != nil && !blockedUntil.IsZero() {
			blockedVal = blockedUntil.UTC().Format("2006-01-02 15:04:05")
		} else {
			blockedVal = nil
		}
	} else {
		now = time.Now().UTC().Format(time.RFC3339)
		if blockedUntil != nil && !blockedUntil.IsZero() {
			blockedVal = blockedUntil.UTC().Format(time.RFC3339)
		} else {
			blockedVal = nil
		}
	}

	cleanIP := strings.ReplaceAll(strings.ReplaceAll(ipAddress, "\n", ""), "\r", "")
	cleanDetails := strings.ReplaceAll(strings.ReplaceAll(details, "\n", " "), "\r", "")
	if severity == "" {
		severity = "warning"
	}

	_, err = stmt.Exec(id, eventType, cleanIP, severity, cleanDetails, blockedVal, now)
	if err != nil {
		return fmt.Errorf("failed to execute security event insert: %w", err)
	}
	return nil
}

// SaveSecurityEvent lưu trữ sự kiện an ninh và phòng thủ (bí danh đồng nhất của RecordSecurityEvent hỗ trợ cả SQLite và TiDB/MySQL)
func SaveSecurityEvent(eventType, ipAddress, severity, details string, blockedUntil *time.Time) error {
	return RecordSecurityEvent(eventType, ipAddress, severity, details, blockedUntil)
}

// GetSecurityEvents lấy danh sách các sự kiện an ninh mới nhất bằng Prepared Statement
func GetSecurityEvents(limit, offset int) ([]SecurityEvent, error) {
	db := GetDB()
	if db == nil {
		return nil, fmt.Errorf("database connection is nil")
	}

	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}

	query := `
		SELECT id, event_type, ip_address, severity, COALESCE(details, ''), 
		       COALESCE(blocked_until, ''), created_at
		FROM security_events
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`
	stmt, err := db.Prepare(query)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare security events query: %w", err)
	}
	defer stmt.Close()

	rows, err := stmt.Query(limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query security events: %w", err)
	}
	defer rows.Close()

	events := make([]SecurityEvent, 0)
	for rows.Next() {
		var e SecurityEvent
		if err := rows.Scan(&e.ID, &e.EventType, &e.IPAddress, &e.Severity, &e.Details, &e.BlockedUntil, &e.CreatedAt); err != nil {
			continue
		}
		events = append(events, e)
	}
	return events, nil
}

// IsIPBlocked kiểm tra xem địa chỉ IP có đang bị chặn trong bảng security_events hay không bằng Prepared Statement
// Tương thích đồng nhất cho cả SQLite và TiDB/MySQL
func IsIPBlocked(ipAddress string) (bool, time.Time, error) {
	db := GetDB()
	if db == nil {
		return false, time.Time{}, fmt.Errorf("database connection is nil")
	}

	cleanIP := strings.TrimSpace(ipAddress)
	if cleanIP == "" {
		return false, time.Time{}, nil
	}

	nowRFC := time.Now().UTC().Format(time.RFC3339)
	nowSQL := time.Now().UTC().Format("2006-01-02 15:04:05")

	var query string
	var args []interface{}
	if isMySQLOrTiDB() {
		query = `
			SELECT blocked_until
			FROM security_events
			WHERE ip_address = ? AND blocked_until > ?
			ORDER BY blocked_until DESC
			LIMIT 1
		`
		args = []interface{}{cleanIP, nowSQL}
	} else {
		query = `
			SELECT blocked_until
			FROM security_events
			WHERE ip_address = ? AND (
				(blocked_until LIKE '%T%' AND blocked_until > ?) OR
				(blocked_until NOT LIKE '%T%' AND blocked_until > ?)
			)
			ORDER BY blocked_until DESC
			LIMIT 1
		`
		args = []interface{}{cleanIP, nowRFC, nowSQL}
	}

	stmt, err := db.Prepare(query)
	if err != nil {
		return false, time.Time{}, fmt.Errorf("failed to prepare is IP blocked query: %w", err)
	}
	defer stmt.Close()

	var blockedUntilStr string
	err = stmt.QueryRow(args...).Scan(&blockedUntilStr)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, time.Time{}, nil
		}
		return false, time.Time{}, err
	}

	// Hỗ trợ phân tích cả định dạng RFC3339 lẫn định dạng DATETIME chuẩn MySQL
	t, parseErr := time.Parse(time.RFC3339, blockedUntilStr)
	if parseErr != nil {
		if t2, err2 := time.Parse("2006-01-02 15:04:05", blockedUntilStr); err2 == nil {
			return true, t2, nil
		}
		if t3, err3 := time.Parse("2006-01-02T15:04:05", blockedUntilStr); err3 == nil {
			return true, t3, nil
		}
		return true, time.Now().Add(time.Hour), nil
	}
	return true, t, nil
}
