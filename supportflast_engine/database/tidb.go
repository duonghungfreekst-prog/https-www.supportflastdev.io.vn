package database

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
)

// Hằng số mặc định cho TiDB Cloud
const (
	DefaultTiDBPort            = 4000
	DefaultTiDBDatabase        = "supportflast"
	DefaultTiDBUser            = "root"
	DefaultTiDBTLSConfig       = "tidb"
	DefaultTiDBMaxOpenConns    = 25
	DefaultTiDBMaxIdleConns    = 10
	DefaultTiDBConnMaxLifetime = 5 * time.Minute
	DefaultTiDBConnMaxIdleTime = 3 * time.Minute
	DefaultTiDBConnectTimeout  = 10 * time.Second
	DefaultTiDBReadTimeout     = 30 * time.Second
	DefaultTiDBWriteTimeout    = 30 * time.Second
	DefaultTiDBPingTimeout     = 10 * time.Second
)

// TiDBConfig chứa toàn bộ tham số cấu hình kết nối tới TiDB Cloud Serverless / Dedicated
type TiDBConfig struct {
	Host               string        // Host hoặc Endpoint TiDB Cloud (VD: gateway01.ap-southeast-1.prod.aws.tidbcloud.com)
	Port               int           // Cổng dịch vụ (Mặc định: 4000)
	User               string        // Tên người dùng TiDB Cloud
	Password           string        // Mật khẩu xác thực
	Database           string        // Tên cơ sở dữ liệu (Mặc định: supportflast)
	TLS                string        // Tùy chọn TLS: "tidb", "true", "skip-verify" (Mặc định: "tidb")
	DSN                string        // Chuỗi DSN trực tiếp (nếu có, sẽ được ưu tiên phân tích và cấu hình)
	TLSConfigName      string        // Tên TLS config đăng ký với driver mysql (Mặc định: "tidb")
	MinTLSVersion      uint16        // Bắt buộc TLS 1.2+ (tls.VersionTLS12 hoặc tls.VersionTLS13)
	CustomCAPath       string        // Đường dẫn file CA PEM tùy chỉnh nếu cần xác thực chứng chỉ riêng
	InsecureSkipVerify bool          // Bỏ qua kiểm tra chứng chỉ (chỉ dùng cho môi trường kiểm thử/sandbox nội bộ)
	MaxOpenConns       int           // Số lượng kết nối tối đa mở đồng thời (Connection Pool - Rule PHAN 7.1)
	MaxIdleConns       int           // Số lượng kết nối nhàn rỗi tối đa
	ConnMaxLifetime    time.Duration // Thời gian sống tối đa của một kết nối (tránh bị load balancer ngắt kết nối đột ngột)
	ConnMaxIdleTime    time.Duration // Thời gian chờ tối đa khi kết nối nhàn rỗi
	ConnectTimeout     time.Duration // Thời gian chờ kết nối mạng TCP/TLS
	ReadTimeout        time.Duration // Thời gian chờ đọc dữ liệu
	WriteTimeout       time.Duration // Thời gian chờ ghi dữ liệu
	PingTimeout        time.Duration // Thời gian chờ khi thực hiện Ping check
	AutoMigrate        bool          // Tự động gọi MigrateTiDBSchema sau khi kết nối thành công
}

var (
	tidbInstance      *sql.DB
	tidbMutex         sync.RWMutex
	tidbTLSMutex      sync.Mutex
	tidbTLSRegistered = make(map[string]bool)
)

func init() {
	// Tự động đăng ký cấu hình TLS "tidb" bắt buộc TLS 1.2+ ngay khi nạp module
	_ = RegisterTiDBTLSConfig(DefaultTiDBTLSConfig, tls.VersionTLS12, "", false)
}

// RegisterTiDBTLSConfig đăng ký cấu hình TLS an toàn bắt buộc TLS 1.2+ với go-sql-driver/mysql
func RegisterTiDBTLSConfig(name string, minVersion uint16, caPath string, insecureSkipVerify bool) error {
	tidbTLSMutex.Lock()
	defer tidbTLSMutex.Unlock()

	// Tuân thủ bắt buộc TLS 1.2+ theo chuẩn an ninh TiDB Cloud và Rule 3.4
	if minVersion < tls.VersionTLS12 {
		minVersion = tls.VersionTLS12
	}

	tlsConfig := &tls.Config{
		MinVersion:         minVersion,
		InsecureSkipVerify: insecureSkipVerify,
	}

	if strings.TrimSpace(caPath) != "" {
		caPEM, err := os.ReadFile(caPath)
		if err != nil {
			return fmt.Errorf("không thể đọc file chứng chỉ CA '%s': %w", caPath, err)
		}
		certPool := x509.NewCertPool()
		if !certPool.AppendCertsFromPEM(caPEM) {
			return fmt.Errorf("không thể phân tích chứng chỉ PEM từ file '%s'", caPath)
		}
		tlsConfig.RootCAs = certPool
	}

	if err := mysql.RegisterTLSConfig(name, tlsConfig); err != nil {
		return fmt.Errorf("lỗi đăng ký TLS config '%s' với MySQL driver: %w", name, err)
	}

	tidbTLSRegistered[name] = true
	log.Printf("[ENGINE] [TIDB] Đã đăng ký thành công TLS Config '%s' (MinVersion=0x%04x, InsecureSkipVerify=%v)", name, minVersion, insecureSkipVerify)
	return nil
}

// IsTLSRegistered kiểm tra tên cấu hình TLS đã được đăng ký với driver hay chưa
func IsTLSRegistered(name string) bool {
	tidbTLSMutex.Lock()
	defer tidbTLSMutex.Unlock()
	return tidbTLSRegistered[name]
}

// DefaultTiDBConfig khởi tạo TiDBConfig từ các biến môi trường hệ thống hoặc giá trị chuẩn
func DefaultTiDBConfig() TiDBConfig {
	port := parseIntEnv("TIDB_PORT", DefaultTiDBPort)
	maxOpen := parseIntEnv("TIDB_MAX_OPEN_CONNS", DefaultTiDBMaxOpenConns)
	maxIdle := parseIntEnv("TIDB_MAX_IDLE_CONNS", DefaultTiDBMaxIdleConns)

	connMaxLifetime := parseDurationEnv("TIDB_CONN_MAX_LIFETIME", DefaultTiDBConnMaxLifetime)
	connMaxIdleTime := parseDurationEnv("TIDB_CONN_MAX_IDLE_TIME", DefaultTiDBConnMaxIdleTime)
	connectTimeout := parseDurationEnv("TIDB_CONNECT_TIMEOUT", DefaultTiDBConnectTimeout)
	readTimeout := parseDurationEnv("TIDB_READ_TIMEOUT", DefaultTiDBReadTimeout)
	writeTimeout := parseDurationEnv("TIDB_WRITE_TIMEOUT", DefaultTiDBWriteTimeout)
	pingTimeout := parseDurationEnv("TIDB_PING_TIMEOUT", DefaultTiDBPingTimeout)

	tlsConfigName := strings.TrimSpace(os.Getenv("TIDB_TLS_CONFIG"))
	if tlsConfigName == "" {
		tlsConfigName = DefaultTiDBTLSConfig
	}

	tlsVal := strings.TrimSpace(os.Getenv("TIDB_TLS"))
	if tlsVal == "" {
		tlsVal = tlsConfigName
	}

	autoMigrate := true
	if val := strings.TrimSpace(os.Getenv("TIDB_AUTO_MIGRATE")); val != "" {
		autoMigrate = parseBoolVal(val, true)
	}

	insecureSkip := parseBoolVal(os.Getenv("TIDB_INSECURE_SKIP_VERIFY"), false)

	host := strings.TrimSpace(os.Getenv("TIDB_HOST"))
	if host == "" {
		host = "gateway01.ap-southeast-1.prod.aws.tidbcloud.com"
	}
	user := strings.TrimSpace(os.Getenv("TIDB_USER"))
	if user == "" || user == DefaultTiDBUser {
		if strings.Contains(host, "tidbcloud.com") {
			user = "2KGt5QqixkveQPP.root"
		} else {
			user = DefaultTiDBUser
		}
	}
	pass := strings.TrimSpace(os.Getenv("TIDB_PASSWORD"))
	if pass == "" && strings.Contains(host, "tidbcloud.com") {
		pass = "JIxWb1nGVINnKzap"
	}

	return TiDBConfig{
		Host:               host,
		Port:               port,
		User:               user,
		Password:           pass,
		Database:           getEnvOrDefault("TIDB_DATABASE", DefaultTiDBDatabase),
		TLS:                tlsVal,
		DSN:                strings.TrimSpace(os.Getenv("TIDB_DSN")),
		TLSConfigName:      tlsConfigName,
		MinTLSVersion:      tls.VersionTLS12,
		CustomCAPath:       strings.TrimSpace(os.Getenv("TIDB_CA_PATH")),
		InsecureSkipVerify: insecureSkip,
		MaxOpenConns:       maxOpen,
		MaxIdleConns:       maxIdle,
		ConnMaxLifetime:    connMaxLifetime,
		ConnMaxIdleTime:    connMaxIdleTime,
		ConnectTimeout:     connectTimeout,
		ReadTimeout:        readTimeout,
		WriteTimeout:       writeTimeout,
		PingTimeout:        pingTimeout,
		AutoMigrate:        autoMigrate,
	}
}

// FormatDSN sinh chuỗi kết nối MySQL DSN tương thích TiDB Cloud
func (c *TiDBConfig) FormatDSN() string {
	port := c.Port
	if port <= 0 {
		port = DefaultTiDBPort
	}

	databaseName := c.Database
	if databaseName == "" {
		databaseName = DefaultTiDBDatabase
	}

	tlsParam := c.TLS
	if tlsParam == "" {
		if c.TLSConfigName != "" {
			tlsParam = c.TLSConfigName
		} else {
			tlsParam = DefaultTiDBTLSConfig
		}
	}

	connectTimeout := c.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = DefaultTiDBConnectTimeout
	}
	readTimeout := c.ReadTimeout
	if readTimeout <= 0 {
		readTimeout = DefaultTiDBReadTimeout
	}
	writeTimeout := c.WriteTimeout
	if writeTimeout <= 0 {
		writeTimeout = DefaultTiDBWriteTimeout
	}

	return fmt.Sprintf(
		"%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&collation=utf8mb4_unicode_ci&parseTime=true&loc=UTC&tls=%s&timeout=%s&writeTimeout=%s&readTimeout=%s",
		c.User, c.Password, c.Host, port, databaseName, tlsParam,
		connectTimeout.String(), writeTimeout.String(), readTimeout.String(),
	)
}

// BuildTiDBDSN tạo chuỗi kết nối MySQL DSN an toàn cho TiDB Cloud từ cấu hình TiDBConfig
func BuildTiDBDSN(cfg TiDBConfig) (string, error) {
	tlsName := cfg.TLSConfigName
	if tlsName == "" {
		if cfg.TLS != "" {
			tlsName = cfg.TLS
		} else {
			tlsName = DefaultTiDBTLSConfig
		}
	}

	connectTimeout := cfg.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = DefaultTiDBConnectTimeout
	}
	readTimeout := cfg.ReadTimeout
	if readTimeout <= 0 {
		readTimeout = DefaultTiDBReadTimeout
	}
	writeTimeout := cfg.WriteTimeout
	if writeTimeout <= 0 {
		writeTimeout = DefaultTiDBWriteTimeout
	}

	// Trường hợp người dùng cung cấp sẵn DSN đầy đủ (TIDB_DSN)
	if strings.TrimSpace(cfg.DSN) != "" {
		parsed, err := mysql.ParseDSN(cfg.DSN)
		if err != nil {
			return "", fmt.Errorf("chuỗi DSN TiDB không hợp lệ: %w", err)
		}
		// Đảm bảo bắt buộc TLS nếu DSN chưa thiết lập
		if parsed.TLSConfig == "" && parsed.TLS == nil {
			parsed.TLSConfig = tlsName
		}
		if parsed.Timeout == 0 {
			parsed.Timeout = connectTimeout
		}
		if parsed.ReadTimeout == 0 {
			parsed.ReadTimeout = readTimeout
		}
		if parsed.WriteTimeout == 0 {
			parsed.WriteTimeout = writeTimeout
		}
		if parsed.Params == nil {
			parsed.Params = make(map[string]string)
		}
		if _, ok := parsed.Params["charset"]; !ok {
			parsed.Params["charset"] = "utf8mb4"
		}
		parsed.ParseTime = true
		parsed.Loc = time.UTC
		return parsed.FormatDSN(), nil
	}

	if strings.TrimSpace(cfg.Host) == "" {
		return "", fmt.Errorf("TIDB_HOST không được để trống khi khởi tạo kết nối TiDB Cloud")
	}

	port := cfg.Port
	if port <= 0 {
		port = DefaultTiDBPort
	}

	databaseName := cfg.Database
	if databaseName == "" {
		databaseName = DefaultTiDBDatabase
	}

	mc := mysql.NewConfig()
	mc.User = cfg.User
	mc.Passwd = cfg.Password
	mc.Net = "tcp"
	mc.Addr = fmt.Sprintf("%s:%d", cfg.Host, port)
	mc.DBName = databaseName
	mc.TLSConfig = tlsName
	mc.Timeout = connectTimeout
	mc.ReadTimeout = readTimeout
	mc.WriteTimeout = writeTimeout
	mc.ParseTime = true
	mc.Loc = time.UTC
	mc.Collation = "utf8mb4_unicode_ci"
	mc.Params = map[string]string{
		"charset": "utf8mb4",
	}

	return mc.FormatDSN(), nil
}

// OpenTiDBConnection mở kết nối tới TiDB Cloud với TLS 1.2+, connection pool, ping check và tự động gọi MigrateTiDBSchema
func OpenTiDBConnection(configs ...TiDBConfig) (*sql.DB, error) {
	var cfg TiDBConfig
	if len(configs) > 0 {
		cfg = configs[0]
	} else {
		cfg = DefaultTiDBConfig()
	}

	// 1. Đảm bảo cấu hình TLS 1.2+ đã được đăng ký với driver MySQL
	if cfg.TLSConfigName == "" {
		if cfg.TLS != "" && cfg.TLS != "true" && cfg.TLS != "false" && cfg.TLS != "skip-verify" {
			cfg.TLSConfigName = cfg.TLS
		} else {
			cfg.TLSConfigName = DefaultTiDBTLSConfig
		}
	}
	if cfg.MinTLSVersion < tls.VersionTLS12 {
		cfg.MinTLSVersion = tls.VersionTLS12
	}

	// Đăng ký TLS config nếu chưa có hoặc có cấu hình chứng chỉ riêng
	if cfg.CustomCAPath != "" || cfg.InsecureSkipVerify || !IsTLSRegistered(cfg.TLSConfigName) {
		if err := RegisterTiDBTLSConfig(cfg.TLSConfigName, cfg.MinTLSVersion, cfg.CustomCAPath, cfg.InsecureSkipVerify); err != nil {
			return nil, fmt.Errorf("lỗi khởi tạo cấu hình TLS cho TiDB: %w", err)
		}
	}

	// 2. Tạo chuỗi kết nối DSN an toàn
	dsn, err := BuildTiDBDSN(cfg)
	if err != nil {
		return nil, fmt.Errorf("lỗi tạo DSN TiDB: %w", err)
	}

	// 3. Mở kết nối với driver "mysql"
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("lỗi sql.Open với driver mysql tới TiDB: %w", err)
	}

	// 4. Thiết lập Connection Pool chống cạn kiệt tài nguyên & tràn bộ nhớ (Rule PHAN 7.1)
	maxOpen := cfg.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = DefaultTiDBMaxOpenConns
	}
	maxIdle := cfg.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = DefaultTiDBMaxIdleConns
	}
	connMaxLifetime := cfg.ConnMaxLifetime
	if connMaxLifetime <= 0 {
		connMaxLifetime = DefaultTiDBConnMaxLifetime
	}
	connMaxIdleTime := cfg.ConnMaxIdleTime
	if connMaxIdleTime <= 0 {
		connMaxIdleTime = DefaultTiDBConnMaxIdleTime
	}

	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(connMaxLifetime)
	db.SetConnMaxIdleTime(connMaxIdleTime)

	// 5. Ping check kiểm tra tính sẵn sàng của kết nối mạng và TLS
	pingTimeout := cfg.PingTimeout
	if pingTimeout <= 0 {
		pingTimeout = DefaultTiDBPingTimeout
	}
	pingCtx, pingCancel := context.WithTimeout(context.Background(), pingTimeout)
	defer pingCancel()

	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping check kết nối tới TiDB Cloud (%s:%d) thất bại: %w", cfg.Host, cfg.Port, err)
	}

	log.Printf("[ENGINE] [TIDB] Kết nối TiDB Cloud thành công! (Host=%s:%d, DB=%s, TLS=1.2+, MaxOpen=%d, MaxIdle=%d, Lifetime=%v)",
		cfg.Host, cfg.Port, cfg.Database, maxOpen, maxIdle, connMaxLifetime)

	// 6. Tự động gọi MigrateTiDBSchema để khởi tạo bảng và chỉ mục
	if cfg.AutoMigrate {
		log.Printf("[ENGINE] [TIDB] Tự động kích hoạt MigrateTiDBSchema cho CSDL '%s'...", cfg.Database)
		if err := MigrateTiDBSchema(db); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("tự động thực thi MigrateTiDBSchema thất bại: %w", err)
		}
	}

	return db, nil
}

// InitTiDB khởi tạo đối tượng singleton TiDB cho toàn bộ ứng dụng
func InitTiDB(configs ...TiDBConfig) (*sql.DB, error) {
	tidbMutex.Lock()
	defer tidbMutex.Unlock()

	if tidbInstance != nil {
		dbMutex.Lock()
		dbInstance = tidbInstance
		activeDriver.Store("tidb")
		dbMutex.Unlock()
		return tidbInstance, nil
	}

	var cfg TiDBConfig
	if len(configs) > 0 {
		cfg = configs[0]
	} else {
		cfg = DefaultTiDBConfig()
	}

	db, err := OpenTiDBConnection(cfg)
	if err != nil {
		return nil, err
	}

	tidbInstance = db
	dbMutex.Lock()
	dbInstance = db
	activeDriver.Store("tidb")
	dbMutex.Unlock()
	return tidbInstance, nil
}

// GetTiDB trả về con trỏ kết nối TiDB Cloud hiện hành (thread-safe)
func GetTiDB() *sql.DB {
	tidbMutex.RLock()
	defer tidbMutex.RUnlock()
	return tidbInstance
}

// SetTiDBInstance gán con trỏ kết nối TiDB (hữu ích cho unit test / mocking)
func SetTiDBInstance(db *sql.DB) {
	tidbMutex.Lock()
	defer tidbMutex.Unlock()
	tidbInstance = db
}

// CloseTiDB đóng kết nối TiDB Cloud an toàn và giải phóng pool
func CloseTiDB() error {
	tidbMutex.Lock()
	defer tidbMutex.Unlock()

	if tidbInstance != nil {
		err := tidbInstance.Close()
		tidbInstance = nil
		return err
	}
	return nil
}

// SeedInitialTiDBAdmin kiểm tra bảng users trong TiDB. Nếu rỗng, tạo sẵn tài khoản Admin chuẩn
// (Admin@2026!SupportFlast, BCrypt cost 12). Tuyệt đối không chèn dữ liệu rác/demo theo Rule 9.1.
func SeedInitialTiDBAdmin(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("kết nối database TiDB là nil")
	}

	var userCount int
	err := db.QueryRow("SELECT COUNT(*) FROM users").Scan(&userCount)
	if err != nil {
		return fmt.Errorf("lỗi kiểm tra số lượng người dùng trong bảng users: %w", err)
	}

	// Kiểm tra trước khi chèn tài khoản admin để đảm bảo tính idempotent và tránh duplicate key trên TiDB
	var adminExists int
	_ = db.QueryRow("SELECT COUNT(*) FROM users WHERE id = ? OR username = ?", DefaultAdminID, DefaultAdminUsername).Scan(&adminExists)
	if adminExists > 0 {
		return nil // Đã tồn tại tài khoản admin chuẩn, không chèn lại
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(DefaultAdminPassword), 12)
	if err != nil {
		return fmt.Errorf("lỗi băm mật khẩu quản trị viên mặc định: %w", err)
	}

	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	query := `
		INSERT IGNORE INTO users (
			id, username, email, password_hash, display_name, role, avatar, created_at, updated_at, last_login
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err = db.Exec(query,
		DefaultAdminID,
		DefaultAdminUsername,
		DefaultAdminEmail,
		string(hashedPassword),
		DefaultAdminDisplayName,
		DefaultAdminRole,
		DefaultAdminAvatar,
		now,
		now,
		nil,
	)
	if err != nil {
		return fmt.Errorf("lỗi thêm tài khoản quản trị viên mặc định vào TiDB: %w", err)
	}

	log.Printf("[ENGINE] [TIDB] Khởi tạo thành công tài khoản quản trị viên chuẩn: username='%s', email='%s', role='%s'",
		DefaultAdminUsername, DefaultAdminEmail, DefaultAdminRole)
	return nil
}

// -----------------------------------------------------------------------------
// CÁC HÀM TIỆN ÍCH TRỢ GIÚP (INTERNAL HELPERS)
// -----------------------------------------------------------------------------

func getEnvOrDefault(key, defaultVal string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return defaultVal
}

func parseIntEnv(key string, defaultVal int) int {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return defaultVal
	}
	if n, err := strconv.Atoi(val); err == nil && n > 0 {
		return n
	}
	return defaultVal
}

func parseDurationEnv(key string, defaultVal time.Duration) time.Duration {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return defaultVal
	}
	if d, err := time.ParseDuration(val); err == nil {
		return d
	}
	if sec, err := strconv.Atoi(val); err == nil && sec > 0 {
		return time.Duration(sec) * time.Second
	}
	return defaultVal
}

func parseBoolVal(val string, defaultVal bool) bool {
	clean := strings.ToLower(strings.TrimSpace(val))
	if clean == "" {
		return defaultVal
	}
	return clean == "1" || clean == "true" || clean == "yes" || clean == "on"
}
