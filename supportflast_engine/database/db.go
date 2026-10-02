package database

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// DefaultTiDBDriver driver mặc định là TiDB Cloud
const DefaultTiDBDriverName = "tidb"

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
	d := strings.ToLower(strings.TrimSpace(os.Getenv("DB_DRIVER")))
	if d == "tidb" || d == "mysql" {
		activeDriver.Store("tidb")
	} else {
		activeDriver.Store("sqlite")
	}
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

-- 8. Bảng revoked_tokens: Danh sách token JWT RS256 bị thu hồi / đăng xuất (lưu vết bền vững)
CREATE TABLE IF NOT EXISTS revoked_tokens (
    token_hash TEXT PRIMARY KEY,
    expires_at TEXT NOT NULL,
    revoked_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_revoked_tokens_expires ON revoked_tokens(expires_at);
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


// ResolveDBPath đã loại bỏ — hệ thống chỉ sử dụng TiDB Cloud

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

// ActiveDriver trả về loại cơ sở dữ liệu hiện hành ("sqlite", "tidb", "mysql") (Lock-free)
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

// GetActiveDriver là bí danh (alias) của ActiveDriver đảm bảo tương thích ngược
func GetActiveDriver() string {
	return ActiveDriver()
}

// SetDBInstance gán con trỏ kết nối DB và cập nhật activeDriver phục vụ unit test và tích hợp
func SetDBInstance(db *sql.DB, driver ...string) {
	dbMutex.Lock()
	defer dbMutex.Unlock()
	dbInstance = db
	drv := "tidb"
	if len(driver) > 0 && strings.TrimSpace(driver[0]) != "" {
		drv = strings.ToLower(strings.TrimSpace(driver[0]))
	}
	activeDriver.Store(drv)
	if drv == "tidb" || drv == "mysql" {
		SetTiDBInstance(db)
	}
}

// InitDB khởi tạo kết nối cơ sở dữ liệu thread-safe (TiDB Cloud hoặc SQLite)
func InitDB(customPath ...string) (*sql.DB, error) {
	drv := strings.ToLower(strings.TrimSpace(os.Getenv("DB_DRIVER")))
	if len(customPath) > 0 && strings.TrimSpace(customPath[0]) != "" {
		return InitSQLite(customPath[0])
	}
	if drv == "tidb" || drv == "mysql" {
		db, err := InitTiDB()
		if err != nil {
			return nil, err
		}
		dbMutex.Lock()
		dbInstance = db
		activeDriver.Store("tidb")
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

// CheckpointWAL không áp dụng cho TiDB Cloud — giữ lại để tương thích API
func CheckpointWAL() error {
	return nil
}

// CloseDB đóng kết nối cơ sở dữ liệu an toàn khi tắt Engine
func CloseDB() error {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	var lastErr error
	if dbInstance != nil {
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

func insertIgnoreClause(db ...*sql.DB) string {
	if isMySQLOrTiDB(db...) {
		return "INSERT IGNORE INTO"
	}
	return "INSERT OR IGNORE INTO"
}

// SeedInitialData kiểm tra bảng users. Nếu rỗng, tạo sẵn tài khoản Admin chuẩn:
// username='admin', email='admin@supportflastdev.io.vn', password_hash (BCrypt cost 12),
// role='admin', display_name='Quản Trị Viên Hệ Thống'.
// Hỗ trợ đồng nhất cả SQLite và TiDB/MySQL.
// TUYỆT ĐỐI KHÔNG seed app rác hoặc review giả lập (Tuân thủ nghiêm ngặt Rule 9.1).
func SeedInitialData(db *sql.DB, dataDir ...string) error {
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

	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	var lastLoginVal interface{} = nil
	query := fmt.Sprintf(`
		%s users (
			id, username, email, password_hash, display_name, role, avatar, created_at, updated_at, last_login
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, insertIgnoreClause(db))

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

// SeedInitialApps nạp danh sách ứng dụng chính thức từ apps.json nếu bảng apps rỗng
func SeedInitialApps(db *sql.DB, dataDir ...string) error {
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}

	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM apps").Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to count apps: %w", err)
	}
	if count > 0 {
		return nil // Đã có dữ liệu apps, không nạp đè
	}

	var candidates []string
	if len(dataDir) > 0 && strings.TrimSpace(dataDir[0]) != "" {
		candidates = append(candidates,
			filepath.Join(dataDir[0], "apps.json"),
			filepath.Join(dataDir[0], "backups", "supportflast_snapshot.json"),
			filepath.Join(dataDir[0], "supportflast_snapshot.json"),
		)
	}
	if envDataDir := strings.TrimSpace(os.Getenv("DATA_DIR")); envDataDir != "" {
		candidates = append(candidates,
			filepath.Join(envDataDir, "apps.json"),
			filepath.Join(envDataDir, "backups", "supportflast_snapshot.json"),
			filepath.Join(envDataDir, "supportflast_snapshot.json"),
		)
	}
	candidates = append(candidates,
		filepath.Join("..", "data", "apps.json"),
		filepath.Join("data", "apps.json"),
		filepath.Join("data", "backups", "supportflast_snapshot.json"),
		filepath.Join("..", "data", "backups", "supportflast_snapshot.json"),
		`f:\supportflast.dev\data\apps.json`,
		`f:\supportflast.dev\data\backups\supportflast_snapshot.json`,
	)

	var jsonPath string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			jsonPath = c
			break
		}
	}
	if jsonPath == "" {
		return nil
	}

	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return fmt.Errorf("không thể đọc file '%s': %w", jsonPath, err)
	}

	var wrapper struct {
		Apps []App `json:"apps"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return fmt.Errorf("lỗi parse json '%s': %w", jsonPath, err)
	}
	if len(wrapper.Apps) == 0 {
		return nil
	}

	query := fmt.Sprintf(`
		%s apps (
			id, name, version, platform, category, ` + "`desc`" + `, file_name,
			size_bytes, size_formatted, sha256, author, downloads,
			status, published_at, download_url, video_url, guide, user_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, insertIgnoreClause(db))

	stmt, err := db.Prepare(query)
	if err != nil {
		return fmt.Errorf("failed to prepare insert app statement: %w", err)
	}
	defer stmt.Close()

	inserted := 0
	for _, a := range wrapper.Apps {
		var uid *string
		if strings.TrimSpace(a.UserID) != "" {
			trimmed := strings.TrimSpace(a.UserID)
			uid = &trimmed
		}
		_, errExec := stmt.Exec(
			a.ID, a.Name, a.Version, a.Platform, a.Category, a.Desc, a.FileName,
			a.SizeBytes, a.SizeFormatted, a.SHA256, a.Author, a.Downloads,
			a.Status, a.PublishedAt, a.DownloadURL, a.VideoURL, a.Guide, uid,
		)
		if errExec == nil {
			inserted++
		}
	}

	log.Printf("[ENGINE] [DATABASE] Tự động nạp thành công %d/%d ứng dụng từ '%s' vào CSDL", inserted, len(wrapper.Apps), jsonPath)
	return nil
}

// SeedInitialKeys nạp danh sách khóa API từ keys.json nếu bảng api_keys rỗng
func SeedInitialKeys(db *sql.DB, dataDir ...string) error {
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}

	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM api_keys").Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to count api_keys: %w", err)
	}
	if count > 0 {
		return nil
	}

	var candidates []string
	if len(dataDir) > 0 && strings.TrimSpace(dataDir[0]) != "" {
		candidates = append(candidates, filepath.Join(dataDir[0], "keys.json"))
	}
	if envDataDir := strings.TrimSpace(os.Getenv("DATA_DIR")); envDataDir != "" {
		candidates = append(candidates, filepath.Join(envDataDir, "keys.json"))
	}
	candidates = append(candidates,
		filepath.Join("..", "data", "keys.json"),
		filepath.Join("data", "keys.json"),
		`f:\supportflast.dev\data\keys.json`,
	)

	var jsonPath string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			jsonPath = c
			break
		}
	}
	if jsonPath == "" {
		return nil
	}

	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return fmt.Errorf("không thể đọc file '%s': %w", jsonPath, err)
	}

	type keyRecord struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Key         string   `json:"key"`
		Prefix      string   `json:"prefix"`
		CreatedAt   string   `json:"created_at"`
		Status      string   `json:"status"`
		Permissions []string `json:"permissions"`
	}

	var keys []keyRecord
	if err := json.Unmarshal(data, &keys); err != nil {
		return fmt.Errorf("lỗi parse json '%s': %w", jsonPath, err)
	}

	query := fmt.Sprintf(`
		%s api_keys (id, user_id, name, key_hash, prefix, status, permissions, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, insertIgnoreClause(db))

	stmt, err := db.Prepare(query)
	if err != nil {
		return fmt.Errorf("failed to prepare insert api_key: %w", err)
	}
	defer stmt.Close()

	for _, k := range keys {
		h := sha256.Sum256([]byte(k.Key))
		keyHash := hex.EncodeToString(h[:])
		permBytes, _ := json.Marshal(k.Permissions)
		adminID := DefaultAdminID
		_, _ = stmt.Exec(k.ID, adminID, k.Name, keyHash, k.Prefix, k.Status, string(permBytes), k.CreatedAt)
	}

	log.Printf("[ENGINE] [DATABASE] Tự động nạp thành công %d khóa API từ '%s' vào CSDL", len(keys), jsonPath)
	return nil
}

// SeedInitialReleases nạp bản cập nhật hệ thống từ system_updates.json nếu bảng system_releases rỗng
func SeedInitialReleases(db *sql.DB, dataDir ...string) error {
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}

	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM system_releases").Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to count system_releases: %w", err)
	}
	if count > 0 {
		return nil
	}

	var candidates []string
	if len(dataDir) > 0 && strings.TrimSpace(dataDir[0]) != "" {
		candidates = append(candidates,
			filepath.Join(dataDir[0], "system_updates.json"),
			filepath.Join(dataDir[0], "backups", "supportflast_snapshot.json"),
			filepath.Join(dataDir[0], "supportflast_snapshot.json"),
		)
	}
	if envDataDir := strings.TrimSpace(os.Getenv("DATA_DIR")); envDataDir != "" {
		candidates = append(candidates,
			filepath.Join(envDataDir, "system_updates.json"),
			filepath.Join(envDataDir, "backups", "supportflast_snapshot.json"),
			filepath.Join(envDataDir, "supportflast_snapshot.json"),
		)
	}
	candidates = append(candidates,
		filepath.Join("..", "data", "system_updates.json"),
		filepath.Join("data", "system_updates.json"),
		filepath.Join("data", "backups", "supportflast_snapshot.json"),
		filepath.Join("..", "data", "backups", "supportflast_snapshot.json"),
		`f:\supportflast.dev\data\system_updates.json`,
		`f:\supportflast.dev\data\backups\supportflast_snapshot.json`,
	)

	var jsonPath string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			jsonPath = c
			break
		}
	}
	if jsonPath == "" {
		// Tự động chèn bản ghi phiên bản hệ thống mặc định để đảm bảo bảng system_releases luôn sẵn sàng
		defQuery := fmt.Sprintf(`%s system_releases (version, title, date, build_hash, notes, published_by) VALUES (?, ?, ?, ?, ?, ?)`, insertIgnoreClause(db))
		nowVal := time.Now().UTC().Format("2006-01-02 15:04:05")
		_, _ = db.Exec(defQuery, "v2.1.0", "SupportFlast Polyglot Cloud Architecture", nowVal, "sf-build-2026-cloud", "Phiên bản phát hành hệ thống chính thức tự động khởi tạo", "System Auto-Bootstrap")
		log.Println("[ENGINE] [DATABASE] Đã tự động khởi tạo phiên bản hệ thống mặc định v2.1.0 cho system_releases")
		return nil
	}

	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return fmt.Errorf("không thể đọc file '%s': %w", jsonPath, err)
	}

	var sysUpdate struct {
		System struct {
			Version   string `json:"version"`
			UpdatedAt string `json:"updated_at"`
			BuildHash string `json:"build_hash"`
		} `json:"system"`
		Releases []SystemRelease `json:"releases"`
	}
	if err := json.Unmarshal(data, &sysUpdate); err != nil {
		return fmt.Errorf("lỗi parse json '%s': %w", jsonPath, err)
	}

	query := fmt.Sprintf(`
		%s system_releases (version, title, date, build_hash, notes, published_by)
		VALUES (?, ?, ?, ?, ?, ?)
	`, insertIgnoreClause(db))

	stmt, err := db.Prepare(query)
	if err != nil {
		return fmt.Errorf("failed to prepare insert release statement: %w", err)
	}
	defer stmt.Close()

	if sysUpdate.System.Version != "" {
		_, _ = stmt.Exec(
			sysUpdate.System.Version,
			"SupportFlast Enterprise Production Release",
			sysUpdate.System.UpdatedAt,
			sysUpdate.System.BuildHash,
			"Bản phát hành chính thức tự động khởi tạo",
			"System Auto-Bootstrap",
		)
	}
	for _, r := range sysUpdate.Releases {
		_, _ = stmt.Exec(r.Version, r.Title, r.Date, r.BuildHash, r.Notes, r.PublishedBy)
	}

	log.Printf("[ENGINE] [DATABASE] Tự động nạp bản cập nhật hệ thống từ '%s' vào CSDL", jsonPath)
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
	now := time.Now().UTC().Format("2006-01-02 15:04:05")

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
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	var blockedVal interface{}
	if blockedUntil != nil && !blockedUntil.IsZero() {
		blockedVal = blockedUntil.UTC().Format("2006-01-02 15:04:05")
	} else {
		blockedVal = nil
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

	nowSQL := time.Now().UTC().Format("2006-01-02 15:04:05")

	query := `
		SELECT blocked_until
		FROM security_events
		WHERE ip_address = ? AND blocked_until > ?
		ORDER BY blocked_until DESC
		LIMIT 1
	`
	args := []interface{}{cleanIP, nowSQL}

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

// RecordRevokedToken lưu trữ token hash đã bị thu hồi vào CSDL SQLite / TiDB
func RecordRevokedToken(tokenHash string, expiresAt time.Time) error {
	db := GetDB()
	if db == nil {
		return fmt.Errorf("CSDL chưa được khởi tạo")
	}
	expStr := expiresAt.Format(time.RFC3339)
	nowStr := time.Now().Format(time.RFC3339)

	var query string
	if ActiveDriver() == "tidb" || ActiveDriver() == "mysql" {
		query = `
			INSERT INTO revoked_tokens (token_hash, expires_at, revoked_at)
			VALUES (?, ?, ?)
			ON DUPLICATE KEY UPDATE expires_at = VALUES(expires_at), revoked_at = VALUES(revoked_at)
		`
	} else {
		query = `
			INSERT INTO revoked_tokens (token_hash, expires_at, revoked_at)
			VALUES (?, ?, ?)
			ON CONFLICT(token_hash) DO UPDATE SET expires_at=excluded.expires_at, revoked_at=excluded.revoked_at
		`
	}

	_, err := db.Exec(query, tokenHash, expStr, nowStr)
	return err
}

// IsTokenHashRevoked kiểm tra xem token hash có nằm trong danh sách thu hồi và chưa hết hạn không
func IsTokenHashRevoked(tokenHash string) bool {
	db := GetDB()
	if db == nil {
		return false
	}
	var count int
	nowStr := time.Now().Format(time.RFC3339)
	err := db.QueryRow("SELECT COUNT(*) FROM revoked_tokens WHERE token_hash = ? AND expires_at > ?", tokenHash, nowStr).Scan(&count)
	return err == nil && count > 0
}
