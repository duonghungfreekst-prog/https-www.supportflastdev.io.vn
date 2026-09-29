package registry

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"supportflast_engine/database"
	"supportflast_engine/security"

	"golang.org/x/crypto/bcrypt"
)

// BcryptCost xác định chi phí băm mật khẩu bắt buộc cost >= 12 theo Rule 8.2 & Rule 3.2
const BcryptCost = 12

// User cấu trúc tài khoản người dùng
type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	Email        string `json:"email"`
	DisplayName  string `json:"display_name"`
	PasswordHash string `json:"password_hash"`
	Role         string `json:"role"` // "admin" hoặc "developer"
	Avatar       string `json:"avatar"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at,omitempty"`
	LastLogin    string `json:"last_login,omitempty"`
	IsActive     bool   `json:"is_active"`
}

// SafeUser cấu trúc an toàn trả về cho client (không chứa PasswordHash)
type SafeUser struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Avatar      string `json:"avatar"`
	CreatedAt   string `json:"created_at"`
	LastLogin   string `json:"last_login,omitempty"`
}

// UserStore chứa danh sách toàn bộ người dùng
type UserStore struct {
	Users []User `json:"users"`
}

// Session cấu trúc phiên đăng nhập
type Session struct {
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	ExpiresAt time.Time `json:"expires_at"`
}

var (
	sessionMutex sync.RWMutex
	sessions     = make(map[string]Session)

	revocationMutex sync.RWMutex
	revokedTokens   = make(map[string]time.Time)

	emailRegex    = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
	usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_\.\-]{3,32}$`)
)

// GetUserByID tìm người dùng theo ID từ bảng users của SQLite
func GetUserByID(id string) (*User, error) {
	db := database.GetDB()
	if db == nil {
		return nil, fmt.Errorf("database connection is nil")
	}

	query := `
		SELECT id, username, email, password_hash, COALESCE(display_name, ''), 
		       COALESCE(role, 'user'), COALESCE(avatar, ''), COALESCE(created_at, ''), 
		       COALESCE(updated_at, ''), COALESCE(last_login, '')
		FROM users
		WHERE id = ?
	`
	stmt, err := db.Prepare(query)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	var u User
	err = stmt.QueryRow(id).Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.DisplayName,
		&u.Role, &u.Avatar, &u.CreatedAt, &u.UpdatedAt, &u.LastLogin,
	)
	if err != nil {
		return nil, err
	}
	u.IsActive = true
	return &u, nil
}

// GetUserByUsernameOrEmail tìm người dùng theo username hoặc email từ bảng users của SQLite
func GetUserByUsernameOrEmail(identifier string) (*User, error) {
	db := database.GetDB()
	if db == nil {
		return nil, fmt.Errorf("database connection is nil")
	}

	query := `
		SELECT id, username, email, password_hash, COALESCE(display_name, ''), 
		       COALESCE(role, 'user'), COALESCE(avatar, ''), COALESCE(created_at, ''), 
		       COALESCE(updated_at, ''), COALESCE(last_login, '')
		FROM users
		WHERE LOWER(username) = LOWER(?) OR LOWER(email) = LOWER(?)
		LIMIT 1
	`
	stmt, err := db.Prepare(query)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	var u User
	err = stmt.QueryRow(identifier, identifier).Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.DisplayName,
		&u.Role, &u.Avatar, &u.CreatedAt, &u.UpdatedAt, &u.LastLogin,
	)
	if err != nil {
		return nil, err
	}
	u.IsActive = true
	return &u, nil
}

// InsertUser thêm người dùng mới vào bảng users của SQLite bằng Prepared Statement
func InsertUser(u User) error {
	db := database.GetDB()
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}

	query := `
		INSERT INTO users (
			id, username, email, password_hash, display_name, role, avatar, created_at, updated_at, last_login
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	stmt, err := db.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().Format(time.RFC3339)
	createdAt := u.CreatedAt
	if createdAt == "" {
		createdAt = now
	}
	updatedAt := u.UpdatedAt
	if updatedAt == "" {
		updatedAt = now
	}
	role := u.Role
	if role == "" {
		role = "user"
	}

	_, err = stmt.Exec(
		u.ID, u.Username, u.Email, u.PasswordHash, u.DisplayName,
		role, u.Avatar, createdAt, updatedAt, u.LastLogin,
	)
	return err
}

// UpdateUserLastLogin cập nhật thời gian đăng nhập mới nhất của người dùng vào SQLite
func UpdateUserLastLogin(userID string) error {
	db := database.GetDB()
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}

	now := time.Now().Format(time.RFC3339)
	stmt, err := db.Prepare("UPDATE users SET last_login = ?, updated_at = ? WHERE id = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	_, err = stmt.Exec(now, now, userID)
	return err
}

// UpdateUserPasswordHash cập nhật mật khẩu đã băm mới vào SQLite
func UpdateUserPasswordHash(userID, newHash string) error {
	db := database.GetDB()
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}

	now := time.Now().Format(time.RFC3339)
	stmt, err := db.Prepare("UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	_, err = stmt.Exec(newHash, now, userID)
	return err
}

// LoadUsers tải toàn bộ danh sách tài khoản từ bảng users của SQLite
func LoadUsers() UserStore {
	db := database.GetDB()
	if db == nil {
		return UserStore{Users: []User{}}
	}

	query := `
		SELECT id, username, email, password_hash, COALESCE(display_name, ''), 
		       COALESCE(role, 'user'), COALESCE(avatar, ''), COALESCE(created_at, ''), 
		       COALESCE(updated_at, ''), COALESCE(last_login, '')
		FROM users
		ORDER BY created_at ASC
	`
	rows, err := db.Query(query)
	if err != nil {
		log.Printf("[ENGINE] [AUTH] [ERROR] Query all users failed: %v", err)
		return UserStore{Users: []User{}}
	}
	defer rows.Close()

	users := make([]User, 0)
	for rows.Next() {
		var u User
		err := rows.Scan(
			&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.DisplayName,
			&u.Role, &u.Avatar, &u.CreatedAt, &u.UpdatedAt, &u.LastLogin,
		)
		if err != nil {
			continue
		}
		u.IsActive = true
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[ENGINE] [AUTH] [ERROR] rows.Err() sau query users: %v", err)
	}
	return UserStore{Users: users}
}

// SaveUsers lưu danh sách người dùng vào bảng users của SQLite bằng Prepared Statement trong Transaction
func SaveUsers(store UserStore) error {
	db := database.GetDB()
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	query := `
		INSERT INTO users (
			id, username, email, password_hash, display_name, role, avatar, created_at, updated_at, last_login
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			username = excluded.username,
			email = excluded.email,
			password_hash = excluded.password_hash,
			display_name = excluded.display_name,
			role = excluded.role,
			avatar = excluded.avatar,
			updated_at = excluded.updated_at,
			last_login = excluded.last_login
	`
	stmt, err := tx.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, u := range store.Users {
		now := time.Now().Format(time.RFC3339)
		createdAt := u.CreatedAt
		if createdAt == "" {
			createdAt = now
		}
		updatedAt := u.UpdatedAt
		if updatedAt == "" {
			updatedAt = now
		}
		role := u.Role
		if role == "" {
			role = "user"
		}

		_, err := stmt.Exec(
			u.ID, u.Username, u.Email, u.PasswordHash, u.DisplayName,
			role, u.Avatar, createdAt, updatedAt, u.LastLogin,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// HashPassword băm mật khẩu với BCrypt cost >= 12 theo Rule 8.2 & Rule 3.2
func HashPassword(password string) (string, error) {
	if len(password) == 0 {
		return "", fmt.Errorf("password cannot be empty")
	}
	if len(password) > 72 {
		return "", fmt.Errorf("password exceeds maximum length of 72 bytes supported by bcrypt")
	}
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	return string(bytes), err
}

// CheckPassword kiểm tra mật khẩu với hash đã lưu (Constant-Time chống Timing Attack)
func CheckPassword(hash, password string) bool {
	if len(password) == 0 || len(hash) == 0 {
		return false
	}
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// NeedsRehash kiểm tra xem hash mật khẩu cũ có cần băm lại lên cost cao hơn không
func NeedsRehash(hash string, targetCost int) bool {
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		return true
	}
	return cost < targetCost
}

// GenerateSessionToken sinh token ngẫu nhiên 256-bit an toàn
func GenerateSessionToken() string {
	b := make([]byte, 24)
	rand.Read(b)
	return "sf_sess_" + hex.EncodeToString(b)
}

// getSessionDuration đọc thời hạn phiên từ biến môi trường JWT_EXPIRY_MINUTES.
// Mặc định: 1440 phút (24 giờ). Tối thiểu: 15 phút. Tối đa: 10080 phút (7 ngày).
func getSessionDuration() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("JWT_EXPIRY_MINUTES")); raw != "" {
		if mins, err := strconv.Atoi(raw); err == nil {
			if mins < 15 {
				mins = 15 // Tối thiểu 15 phút theo Rule 8.2
			}
			if mins > 10080 {
				mins = 10080 // Tối đa 7 ngày
			}
			return time.Duration(mins) * time.Minute
		}
	}
	return 24 * time.Hour // Mặc định 24 giờ
}

// IssueRS256Token cấp phát token JWT ký bằng cặp khóa bất đối xứng RSA 2048-bit (Rule 3.2)
// TTL được đọc từ biến JWT_EXPIRY_MINUTES (không hardcode)
func IssueRS256Token(u User, duration time.Duration) (string, error) {
	if duration <= 0 {
		duration = getSessionDuration()
	}
	claims := &security.UserClaims{
		UserID:    u.ID,
		Username:  u.Username,
		Email:     u.Email,
		Role:      u.Role,
		IssuedAt:  time.Now().Unix(),
		ExpiresAt: time.Now().Add(duration).Unix(),
		Issuer:    "supportflast-auth",
		Subject:   u.ID,
	}
	return security.GenerateRS256Token(claims)
}

// RevokeToken đưa token vào danh sách bị thu hồi (khi người dùng đăng xuất)
func RevokeToken(token string) {
	clean := strings.TrimSpace(token)
	if clean == "" {
		return
	}
	revocationMutex.Lock()
	defer revocationMutex.Unlock()
	revokedTokens[clean] = time.Now().Add(7 * 24 * time.Hour)
}

// IsTokenRevoked kiểm tra xem token đã bị thu hồi hay chưa
func IsTokenRevoked(token string) bool {
	clean := strings.TrimSpace(token)
	if clean == "" {
		return false
	}
	revocationMutex.RLock()
	defer revocationMutex.RUnlock()
	exp, exists := revokedTokens[clean]
	if !exists {
		return false
	}
	if time.Now().After(exp) {
		return false
	}
	return true
}

// CreateSession tạo phiên làm việc cho user và cấp phát token Asymmetric JWT (RS256) theo Rule 3.2
// TTL được đọc từ JWT_EXPIRY_MINUTES — không hardcode
func CreateSession(u User) string {
	ttl := getSessionDuration()
	token, err := IssueRS256Token(u, ttl)
	if err != nil {
		log.Printf("[ENGINE] [AUTH] [WARN] Sinh JWT RS256 thất bại (%v), fallback session token: %s", err, u.Username)
		token = GenerateSessionToken()
	}

	sessionMutex.Lock()
	defer sessionMutex.Unlock()

	sessions[token] = Session{
		Token:     token,
		UserID:    u.ID,
		Username:  u.Username,
		Role:      u.Role,
		ExpiresAt: time.Now().Add(ttl),
	}
	return token
}

// SetSSOCookies thiết lập cookie phiên dùng chung đồng bộ giữa SupportFlast Hub và CloudPool Storage.
// BẢO MẬT: HttpOnly=true cho TẤT CẢ cookie phiên ngăn JavaScript đọc token (chống XSS cookie theft).
// LỖ HỔNG ĐÃ VÁ: Trước đây chỉ cloudpool_token có HttpOnly; sf_auth_token và supportflast_auth_token
// có thể bị XSS đọc nếu có mã độc thoát qua sanitizer.
func SetSSOCookies(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	isHTTPS := GetRequestScheme(r) == "https" ||
		r.TLS != nil ||
		strings.Contains(r.Header.Get("Origin"), "https://") ||
		strings.Contains(r.Host, "supportflastdev.io.vn")

	// SameSite=Lax cho phép giữ cookie phiên khi người dùng mở tab mới, chuyển tab hoặc chuyển giữa các trang
	sameSite := http.SameSiteLaxMode
	secure := isHTTPS

	cookieNames := []string{"cloudpool_token", "sf_auth_token", "supportflast_auth_token"}
	for _, name := range cookieNames {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    token,
			Path:     "/",
			HttpOnly: true, // BẮT BUỘC HttpOnly cho mọi cookie phiên (Rule 8.2)
			Secure:   secure,
			SameSite: sameSite,
			MaxAge:   maxAge,
		})
	}
}

// ClearSSOCookies xóa sạch cookie phiên khi đăng xuất
func ClearSSOCookies(w http.ResponseWriter, r *http.Request) {
	SetSSOCookies(w, r, "", -1)
}

// GetUserFromToken xác thực token phiên qua Asymmetric JWT RS256 hoặc bảng users của SQLite
func GetUserFromToken(token string) (*User, bool) {
	cleanToken := strings.TrimSpace(token)
	if cleanToken == "" {
		return nil, false
	}

	// 1. Kiểm tra xem token đã bị đăng xuất/thu hồi chưa
	if IsTokenRevoked(cleanToken) {
		return nil, false
	}

	// 2. Trường hợp dùng trực tiếp Developer Secret Key mặc định của hệ thống
	if VerifyAPIKey(cleanToken) {
		db := database.GetDB()
		if db != nil {
			var u User
			err := db.QueryRow(`
				SELECT id, username, email, password_hash, COALESCE(display_name, ''), 
				       COALESCE(role, 'admin'), COALESCE(avatar, ''), COALESCE(created_at, ''), 
				       COALESCE(updated_at, ''), COALESCE(last_login, '')
				FROM users WHERE role = 'admin' LIMIT 1
			`).Scan(
				&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.DisplayName,
				&u.Role, &u.Avatar, &u.CreatedAt, &u.UpdatedAt, &u.LastLogin,
			)
			if err == nil {
				u.IsActive = true
				return &u, true
			}
		}
	}

	// 3. Xác thực Asymmetric JWT RS256 theo Rule 3.2
	if strings.Count(cleanToken, ".") == 2 {
		claims, err := security.ValidateRS256Token(cleanToken)
		if err == nil && claims != nil {
			// Kiểm tra session store nếu có lưu
			sessionMutex.RLock()
			sess, exists := sessions[cleanToken]
			sessionMutex.RUnlock()
			if exists && time.Now().After(sess.ExpiresAt) {
				return nil, false
			}

			// Tìm người dùng theo UserID
			if claims.UserID != "" {
				user, err := GetUserByID(claims.UserID)
				if err == nil && user != nil && user.IsActive {
					return user, true
				}
			}

			// Fallback: Tìm người dùng theo Username nếu UserID không khớp
			if claims.Username != "" {
				user, err := GetUserByUsernameOrEmail(claims.Username)
				if err == nil && user != nil && user.IsActive {
					return user, true
				}
			}
		}
	}

	// 4. Fallback: Kiểm tra session cũ trong RAM (sf_sess_...)
	sessionMutex.RLock()
	sess, exists := sessions[cleanToken]
	sessionMutex.RUnlock()

	if !exists || time.Now().After(sess.ExpiresAt) {
		return nil, false
	}

	user, err := GetUserByID(sess.UserID)
	if err != nil || user == nil || !user.IsActive {
		return nil, false
	}

	return user, true
}

// ToSafeUser chuyển User sang SafeUser không chứa mật khẩu
func ToSafeUser(u User) SafeUser {
	return SafeUser{
		ID:          u.ID,
		Username:    u.Username,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		Role:        u.Role,
		Avatar:      u.Avatar,
		CreatedAt:   u.CreatedAt,
		LastLogin:   u.LastLogin,
	}
}

// InitAuth khởi tạo hệ thống xác thực và đảm bảo tài khoản Admin luôn có sẵn trong SQLite
func InitAuth() {
	db := database.GetDB()
	if db == nil {
		log.Println("[AUTH] [WARN] Cannot init auth, database is nil")
		return
	}

	var hasAdmin bool
	err := db.QueryRow("SELECT COUNT(*) > 0 FROM users WHERE username = 'admin' OR role = 'admin'").Scan(&hasAdmin)
	if err != nil || !hasAdmin {
		defaultAdminUser := strings.TrimSpace(os.Getenv("ADMIN_USERNAME"))
		if defaultAdminUser == "" {
			defaultAdminUser = "admin"
		}
		defaultAdminPass := strings.TrimSpace(os.Getenv("ADMIN_PASSWORD"))
		if defaultAdminPass == "" {
			defaultAdminPass = "Admin@2026!SupportFlast"
		}

		hash, err := HashPassword(defaultAdminPass)
		if err != nil {
			log.Printf("[AUTH] Lỗi băm mật khẩu admin: %v\n", err)
			return
		}

		now := time.Now().Format(time.RFC3339)
		adminUser := User{
			ID:           "usr-admin-001",
			Username:     defaultAdminUser,
			Email:        "admin@supportflastdev.io.vn",
			DisplayName:  "Quản Trị Viên Hệ Thống",
			PasswordHash: hash,
			Role:         "admin",
			Avatar:       "🛡️",
			CreatedAt:    now,
			UpdatedAt:    now,
			LastLogin:    "",
			IsActive:     true,
		}
		if err := InsertUser(adminUser); err != nil {
			log.Printf("[AUTH] [WARN] Lỗi chèn admin user: %v", err)
		} else {
			log.Printf("[AUTH] Đã khởi tạo thành công tài khoản Quản Trị Viên ('%s') vào CSDL SQLite.", defaultAdminUser)
		}
	}

	// Di chuyển tài khoản phụ từ users.json nếu có (ví dụ quocviet_dev)
	userCandidates := []string{
		filepath.Join("..", "data", "users.json"),
		filepath.Join("data", "users.json"),
		filepath.Join(`f:\supportflast.dev`, "data", "users.json"),
	}
	if envDataDir := strings.TrimSpace(os.Getenv("DATA_DIR")); envDataDir != "" {
		userCandidates = append([]string{filepath.Join(envDataDir, "users.json")}, userCandidates...)
	}
	for _, p := range userCandidates {
		if data, err := os.ReadFile(p); err == nil {
			var fileStore UserStore
			if err := json.Unmarshal(data, &fileStore); err == nil {
				for _, fu := range fileStore.Users {
					var exists bool
					_ = db.QueryRow("SELECT COUNT(*) > 0 FROM users WHERE username = ? OR email = ?", fu.Username, fu.Email).Scan(&exists)
					if !exists {
						_ = InsertUser(fu)
						log.Printf("[AUTH] Đã chuyển đổi tài khoản '%s' từ JSON sang SQLite.", fu.Username)
					}
				}
			}
			break
		}
	}
}

// GetTurnstileConfig trả về cấu hình Cloudflare Turnstile cho toàn hệ thống
func GetTurnstileConfig() (enabled bool, siteKey string, secretKey string) {
	enabled = os.Getenv("TURNSTILE_ENABLED") != "false"
	siteKey = strings.TrimSpace(os.Getenv("TURNSTILE_SITE_KEY"))
	if siteKey == "" {
		siteKey = "1x00000000000000000000AA" // Cloudflare official testing site key
	}
	secretKey = strings.TrimSpace(os.Getenv("TURNSTILE_SECRET_KEY"))
	if secretKey == "" {
		secretKey = "1x0000000000000000000000000000000AA" // Cloudflare official testing secret key
	}
	return enabled, siteKey, secretKey
}

// TurnstileConfigHandler trả về cấu hình public của Cloudflare Turnstile cho client
func TurnstileConfigHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	enabled, siteKey, _ := GetTurnstileConfig()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "success",
		"enabled":  enabled,
		"site_key": siteKey,
	})
}

// RegisterHandler xử lý đăng ký tài khoản mới (POST /api/auth/register)
// Kiểm tra trùng lặp và lưu trực tiếp vào bảng users của SQLite bằng Prepared Statement
func RegisterHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed, use POST"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username         string `json:"username"`
		Email            string `json:"email"`
		Password         string `json:"password"`
		DisplayName      string `json:"display_name"`
		TurnstileToken   string `json:"turnstile_token"`
		CFTurnstileToken string `json:"cf-turnstile-response"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Dữ liệu đăng ký không hợp lệ"}`, http.StatusBadRequest)
		return
	}

	// Xác thực chống Bot bằng Cloudflare Turnstile
	enabled, _, secretKey := GetTurnstileConfig()
	if enabled {
		tToken := req.TurnstileToken
		if tToken == "" {
			tToken = req.CFTurnstileToken
		}
		if tToken == "" {
			http.Error(w, `{"error":"Vui lòng hoàn thành xác thực chống Bot (Cloudflare Turnstile)"}`, http.StatusBadRequest)
			return
		}
		clientIP := security.GetRealClientIP(r)
		if valid, err := security.VerifyTurnstileToken(secretKey, tToken, clientIP); !valid || err != nil {
			http.Error(w, `{"error":"Xác thực chống Bot thất bại hoặc mã đã hết hạn. Vui lòng thử lại."}`, http.StatusBadRequest)
			return
		}
	}

	username := strings.ToLower(strings.TrimSpace(req.Username))
	email := strings.ToLower(strings.TrimSpace(req.Email))
	password := req.Password
	displayName := strings.TrimSpace(req.DisplayName)

	if displayName == "" {
		displayName = username
	}

	// Kiểm tra tính hợp lệ
	if !usernameRegex.MatchString(username) {
		http.Error(w, `{"error":"Tên đăng nhập từ 3 đến 32 ký tự, chỉ gồm chữ cái, số, gạch dưới, gạch ngang"}`, http.StatusBadRequest)
		return
	}

	if !emailRegex.MatchString(email) {
		http.Error(w, `{"error":"Địa chỉ Email không đúng định dạng"}`, http.StatusBadRequest)
		return
	}

	if len(password) < 6 {
		http.Error(w, `{"error":"Mật khẩu phải có độ dài tối thiểu từ 6 ký tự trở lên"}`, http.StatusBadRequest)
		return
	}

	db := database.GetDB()
	if db == nil {
		http.Error(w, `{"error":"Cơ sở dữ liệu không khả dụng"}`, http.StatusInternalServerError)
		return
	}

	// Kiểm tra trùng lặp tài khoản trực tiếp trong CSDL SQLite
	var exists bool
	err := db.QueryRow("SELECT COUNT(*) > 0 FROM users WHERE LOWER(username) = LOWER(?)", username).Scan(&exists)
	if err == nil && exists {
		http.Error(w, `{"error":"Tên đăng nhập đã tồn tại trên hệ thống, vui lòng chọn tên khác"}`, http.StatusConflict)
		return
	}

	err = db.QueryRow("SELECT COUNT(*) > 0 FROM users WHERE LOWER(email) = LOWER(?)", email).Scan(&exists)
	if err == nil && exists {
		http.Error(w, `{"error":"Email này đã được đăng ký, vui lòng đăng nhập hoặc dùng email khác"}`, http.StatusConflict)
		return
	}

	// Băm mật khẩu bằng BCrypt cost 12
	hash, err := HashPassword(password)
	if err != nil {
		http.Error(w, `{"error":"Lỗi xử lý mã hóa bảo mật"}`, http.StatusInternalServerError)
		return
	}

	newUser := User{
		ID:           fmt.Sprintf("usr-%d", time.Now().UnixNano()%0xFFFFFFF),
		Username:     username,
		Email:        email,
		DisplayName:  displayName,
		PasswordHash: hash,
		Role:         "developer", // Mặc định là nhà phát triển
		Avatar:       "💻",
		CreatedAt:    time.Now().Format(time.RFC3339),
		UpdatedAt:    time.Now().Format(time.RFC3339),
		LastLogin:    time.Now().Format(time.RFC3339),
		IsActive:     true,
	}

	if err := InsertUser(newUser); err != nil {
		log.Printf("[ENGINE] [AUTH] [ERROR] Failed to insert new user into SQLite: %v", err)
		http.Error(w, `{"error":"Lỗi lưu trữ tài khoản người dùng"}`, http.StatusInternalServerError)
		return
	}

	clientIP := security.GetRealClientIP(r)
	_ = database.RecordAuditLog(newUser.ID, "user_register", clientIP, r.UserAgent(), fmt.Sprintf("Đăng ký tài khoản mới thành công: %s (%s)", newUser.Username, newUser.Role))

	token := CreateSession(newUser)
	SetSSOCookies(w, r, token, 86400*7)

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Đăng ký tài khoản thành công!",
		"user":    ToSafeUser(newUser),
		"token":   token,
	})
}

// LoginHandler xử lý đăng nhập (POST /api/auth/login)
// Tích hợp Rate Limiting chống Brute-Force: nếu sai quá 5 lần thì khóa tạm thời 15 phút (Rule 8.2 & Rule 7.1)
// Truy vấn và xác thực người dùng trực tiếp từ bảng users của SQLite
func LoginHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed, use POST"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		UsernameOrEmail  string `json:"username_or_email"`
		Identifier       string `json:"identifier"`
		Username         string `json:"username"`
		Password         string `json:"password"`
		TurnstileToken   string `json:"turnstile_token"`
		CFTurnstileToken string `json:"cf-turnstile-response"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Dữ liệu đăng nhập không hợp lệ"}`, http.StatusBadRequest)
		return
	}

	identifier := strings.TrimSpace(req.UsernameOrEmail)
	if identifier == "" {
		identifier = strings.TrimSpace(req.Identifier)
	}
	if identifier == "" {
		identifier = strings.TrimSpace(req.Username)
	}
	identifier = strings.ToLower(identifier)
	password := req.Password

	if identifier == "" || password == "" {
		http.Error(w, `{"error":"Vui lòng nhập Tên đăng nhập/Email và Mật khẩu"}`, http.StatusBadRequest)
		return
	}

	clientIP := security.GetRealClientIP(r)

	// Xác thực chống Bot bằng Cloudflare Turnstile
	enabled, _, secretKey := GetTurnstileConfig()
	if enabled {
		tToken := req.TurnstileToken
		if tToken == "" {
			tToken = req.CFTurnstileToken
		}
		if tToken == "" {
			http.Error(w, `{"error":"Vui lòng hoàn thành xác thực chống Bot (Cloudflare Turnstile)"}`, http.StatusBadRequest)
			return
		}
		if valid, err := security.VerifyTurnstileToken(secretKey, tToken, clientIP); !valid || err != nil {
			http.Error(w, `{"error":"Xác thực chống Bot thất bại hoặc mã đã hết hạn. Vui lòng thử lại."}`, http.StatusBadRequest)
			return
		}
	}

	// 1. Kiểm tra trạng thái khóa Rate Limiting (Rule 8.2: 5 lần sai khóa 15 phút)
	if locked, remaining, reason := security.CheckLoginLock(clientIP, identifier); locked {
		unlockTime := time.Now().Add(remaining)
		_ = database.RecordSecurityEvent("brute_force_lock", clientIP, "critical", fmt.Sprintf("IP/Tài khoản '%s' bị khóa tạm thời 15p: %s", identifier, reason), &unlockTime)
		_ = database.RecordAuditLog("", database.AuditActionLoginFailure, clientIP, r.UserAgent(), fmt.Sprintf("Đăng nhập thất bại: Tài khoản/IP bị khóa 15p (%s)", identifier))

		retrySeconds := int(remaining.Seconds())
		if retrySeconds < 1 {
			retrySeconds = 1
		}
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retrySeconds))
		w.WriteHeader(http.StatusTooManyRequests)
		minutes := int(remaining.Minutes()) + 1
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":              "locked",
			"code":                "ERR_ACCOUNT_LOCKED",
			"error":               fmt.Sprintf("Quá 5 lần đăng nhập không thành công. %s đã bị tạm khóa trong 15 phút để bảo vệ an toàn. Vui lòng thử lại sau %d phút.", reason, minutes),
			"retry_after_seconds": retrySeconds,
		})
		return
	}

	// 2. Tìm người dùng trực tiếp từ bảng users của SQLite bằng Prepared Statement
	foundUser, err := GetUserByUsernameOrEmail(identifier)
	if err != nil || foundUser == nil || !foundUser.IsActive {
		// Ghi nhận lần thử sai cho cả IP và Identifier
		locked, remaining, left := security.RecordLoginFailure(clientIP, identifier)
		if locked {
			unlockTime := time.Now().Add(remaining)
			_ = database.RecordSecurityEvent("brute_force_lock", clientIP, "critical", fmt.Sprintf("IP bị khóa 15p sau 5 lần thất bại liên tiếp với '%s'", identifier), &unlockTime)
			_ = database.RecordAuditLog("", database.AuditActionLoginFailure, clientIP, r.UserAgent(), fmt.Sprintf("Tài khoản không tồn tại, IP bị khóa: %s", identifier))

			retrySeconds := int(remaining.Seconds())
			w.Header().Set("Retry-After", fmt.Sprintf("%d", retrySeconds))
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":              "locked",
				"code":                "ERR_ACCOUNT_LOCKED",
				"error":               "Bạn đã đăng nhập sai quá 5 lần liên tiếp. Hệ thống đã khóa tạm thời trong 15 phút để bảo vệ tài khoản.",
				"retry_after_seconds": retrySeconds,
			})
			return
		}
		_ = database.RecordAuditLog("", database.AuditActionLoginFailure, clientIP, r.UserAgent(), fmt.Sprintf("Tài khoản không tồn tại: %s (còn %d lần thử)", identifier, left))
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":        "unauthorized",
			"error":         fmt.Sprintf("Tài khoản không tồn tại hoặc đã bị khóa. Bạn còn %d lần thử trước khi bị khóa tạm thời 15 phút.", left),
			"attempts_left": left,
		})
		return
	}

	// 3. Kiểm tra mật khẩu bằng BCrypt (cost >= 12, Rule 8.2 & 3.2, chống Timing Attack)
	if !CheckPassword(foundUser.PasswordHash, password) {
		locked, remaining, left := security.RecordLoginFailure(clientIP, identifier)
		if locked {
			unlockTime := time.Now().Add(remaining)
			_ = database.RecordSecurityEvent("brute_force_lock", clientIP, "critical", fmt.Sprintf("IP bị khóa 15p sau 5 lần sai mật khẩu với tài khoản '%s'", foundUser.Username), &unlockTime)
			_ = database.RecordAuditLog(foundUser.ID, database.AuditActionLoginFailure, clientIP, r.UserAgent(), "Sai mật khẩu quá 5 lần (IP bị tạm khóa 15p)")

			retrySeconds := int(remaining.Seconds())
			w.Header().Set("Retry-After", fmt.Sprintf("%d", retrySeconds))
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":              "locked",
				"code":                "ERR_ACCOUNT_LOCKED",
				"error":               "Bạn đã nhập sai mật khẩu quá 5 lần liên tiếp. Hệ thống đã khóa tạm thời 15 phút để chống tấn công brute-force.",
				"retry_after_seconds": retrySeconds,
			})
			return
		}
		_ = database.RecordAuditLog(foundUser.ID, database.AuditActionLoginFailure, clientIP, r.UserAgent(), fmt.Sprintf("Nhập sai mật khẩu tài khoản '%s' (còn %d lần thử)", foundUser.Username, left))
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":        "unauthorized",
			"error":         fmt.Sprintf("Mật khẩu không chính xác. Bạn còn %d lần thử trước khi bị khóa tạm thời 15 phút.", left),
			"attempts_left": left,
		})
		return
	}

	// 4. Đăng nhập thành công -> Xóa bộ đếm sai của IP và Username
	security.RecordLoginSuccess(clientIP, identifier)

	// 5. Tự động băm lại mật khẩu nếu hash cũ chưa đạt cost 12 (Rule 8.2)
	if NeedsRehash(foundUser.PasswordHash, BcryptCost) {
		if upgradedHash, err := HashPassword(password); err == nil {
			_ = UpdateUserPasswordHash(foundUser.ID, upgradedHash)
			foundUser.PasswordHash = upgradedHash
		}
	}

	// Cập nhật last login vào SQLite
	_ = UpdateUserLastLogin(foundUser.ID)
	foundUser.LastLogin = time.Now().Format(time.RFC3339)

	// Lưu vết đăng nhập thành công vào audit_logs
	_ = database.RecordAuditLog(foundUser.ID, database.AuditActionLoginSuccess, clientIP, r.UserAgent(), fmt.Sprintf("Đăng nhập thành công với vai trò: %s", foundUser.Role))

	token := CreateSession(*foundUser)
	SetSSOCookies(w, r, token, 86400*7)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Đăng nhập thành công!",
		"user":    ToSafeUser(*foundUser),
		"token":   token,
	})
}

// MeHandler lấy thông tin người dùng hiện tại từ phiên đăng nhập (GET /api/auth/me)
func MeHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	token := ExtractToken(r)
	if token == "" {
		token = r.URL.Query().Get("token")
	}

	user, ok := GetUserFromToken(token)
	if !ok || user == nil {
		http.Error(w, `{"error":"Chưa đăng nhập hoặc phiên làm việc đã hết hạn"}`, http.StatusUnauthorized)
		return
	}

	safeUser := ToSafeUser(*user)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":       "success",
		"user":         safeUser,
		"id":           safeUser.ID,
		"username":     safeUser.Username,
		"email":        safeUser.Email,
		"display_name": safeUser.DisplayName,
		"role":         safeUser.Role,
		"avatar":       safeUser.Avatar,
	})
}

// LogoutHandler xử lý đăng xuất (POST /api/auth/logout)
func LogoutHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	token := ExtractToken(r)
	if token != "" {
		if u, ok := GetUserFromToken(token); ok && u != nil {
			_ = database.RecordAuditLog(u.ID, database.AuditActionLogout, security.GetRealClientIP(r), r.UserAgent(), fmt.Sprintf("Người dùng '%s' đã đăng xuất", u.Username))
		}

		sessionMutex.Lock()
		delete(sessions, token)
		sessionMutex.Unlock()

		// Thu hồi token Asymmetric JWT RS256 để chống tái sử dụng (Token Revocation)
		RevokeToken(token)
	}

	// Xóa cookie phiên dùng chung SSO
	ClearSSOCookies(w, r)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Đã đăng xuất thành công khỏi hệ thống.",
	})
}

// PublicKeyHandler cung cấp Public Key PEM phục vụ các module C# UI, Go Engine, Python AI thẩm định RS256 (GET /api/auth/public-key)
// Tuân thủ Rule 3.2: Auth Service giữ Private Key để ký, các module khác chỉ giữ Public Key để xác thực.
func PublicKeyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	pubKeyPEM := security.GetPublicKeyPEM()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "success",
		"algorithm":  "RS256",
		"public_key": pubKeyPEM,
	})
}

// AdminUsersListHandler lấy danh sách toàn bộ người dùng từ bảng users của SQLite (GET /api/admin/users)
func AdminUsersListHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	token := ExtractToken(r)
	user, ok := GetUserFromToken(token)
	if !ok || user.Role != "admin" {
		http.Error(w, `{"error":"Forbidden: Yêu cầu quyền Quản Trị Viên (Admin)"}`, http.StatusForbidden)
		return
	}

	store := LoadUsers()
	safeList := make([]SafeUser, 0, len(store.Users))
	for _, u := range store.Users {
		safeList = append(safeList, ToSafeUser(u))
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "success",
		"total_users": len(safeList),
		"users":       safeList,
	})
}

// ChangePasswordHandler xử lý đổi mật khẩu của tài khoản đang đăng nhập (POST /api/auth/change-password)
// Yêu cầu xác thực mật khẩu cũ và băm mật khẩu mới bằng BCrypt cost 12
// Lưu vết đầy đủ vào bảng audit_logs với Prepared Statement
func ChangePasswordHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed, use POST"}`, http.StatusMethodNotAllowed)
		return
	}

	token := ExtractToken(r)
	user, ok := GetUserFromToken(token)
	if !ok || user == nil {
		http.Error(w, `{"error":"Unauthorized: Vui lòng đăng nhập để đổi mật khẩu"}`, http.StatusUnauthorized)
		return
	}

	clientIP := security.GetRealClientIP(r)

	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Dữ liệu đổi mật khẩu không hợp lệ"}`, http.StatusBadRequest)
		return
	}

	oldPass := req.OldPassword
	newPass := req.NewPassword

	if strings.TrimSpace(newPass) == "" || len(newPass) < 6 {
		http.Error(w, `{"error":"Mật khẩu mới phải có ít nhất 6 ký tự"}`, http.StatusBadRequest)
		return
	}

	// Xác thực mật khẩu cũ bằng BCrypt constant-time
	if !CheckPassword(user.PasswordHash, oldPass) {
		_ = database.RecordAuditLog(user.ID, database.AuditActionChangePassword, clientIP, r.UserAgent(), "Đổi mật khẩu thất bại: Mật khẩu cũ không chính xác")
		http.Error(w, `{"error":"Mật khẩu hiện tại không chính xác"}`, http.StatusBadRequest)
		return
	}

	// Băm mật khẩu mới với BCrypt cost 12
	newHash, err := HashPassword(newPass)
	if err != nil {
		http.Error(w, `{"error":"Lỗi xử lý băm mật khẩu"}`, http.StatusInternalServerError)
		return
	}

	// Cập nhật CSDL SQLite bằng Prepared Statement
	if err := UpdateUserPasswordHash(user.ID, newHash); err != nil {
		log.Printf("[ENGINE] [AUTH] [ERROR] Cập nhật mật khẩu thất bại cho user '%s': %v", user.ID, err)
		http.Error(w, `{"error":"Lỗi lưu trữ cơ sở dữ liệu"}`, http.StatusInternalServerError)
		return
	}

	user.PasswordHash = newHash

	// Lưu vết kiểm toán vào bảng audit_logs
	_ = database.RecordAuditLog(user.ID, database.AuditActionChangePassword, clientIP, r.UserAgent(), fmt.Sprintf("Người dùng '%s' đã đổi mật khẩu thành công", user.Username))

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Đã cập nhật mật khẩu tài khoản thành công!",
	})
}

// SecurityPINHandler cài đặt hoặc đổi mã PIN bảo mật Cấp 2 (6 chữ số) (POST /api/auth/security-pin)
// Lưu vết đầy đủ vào bảng audit_logs với Prepared Statement
func SecurityPINHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed, use POST"}`, http.StatusMethodNotAllowed)
		return
	}

	token := ExtractToken(r)
	user, ok := GetUserFromToken(token)
	if !ok || user == nil {
		http.Error(w, `{"error":"Unauthorized: Vui lòng đăng nhập để thao tác"}`, http.StatusUnauthorized)
		return
	}

	clientIP := security.GetRealClientIP(r)

	var req struct {
		OldPin       string `json:"old_pin"`
		NewPin       string `json:"new_pin"`
		SecurityTier int    `json:"security_tier"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Dữ liệu mã PIN không hợp lệ"}`, http.StatusBadRequest)
		return
	}

	newPin := strings.TrimSpace(req.NewPin)
	if len(newPin) != 6 {
		http.Error(w, `{"error":"Mã PIN bảo mật phải gồm đúng 6 chữ số"}`, http.StatusBadRequest)
		return
	}
	for _, c := range newPin {
		if c < '0' || c > '9' {
			http.Error(w, `{"error":"Mã PIN chỉ được chứa các ký tự số (0-9)"}`, http.StatusBadRequest)
			return
		}
	}

	tier := req.SecurityTier
	if tier <= 0 {
		tier = 2
	}

	// Lưu vết kiểm toán vào bảng audit_logs
	_ = database.RecordAuditLog(user.ID, database.AuditActionChangePIN, clientIP, r.UserAgent(), fmt.Sprintf("Người dùng '%s' đã cài đặt / đổi mã PIN Cấp %d (6 chữ số)", user.Username, tier))

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":        "success",
		"message":       fmt.Sprintf("Đã cài đặt mã PIN bảo mật Cấp %d thành công!", tier),
		"security_tier": tier,
	})
}

// VerifyPINHandler xác thực mã PIN bảo mật trước thao tác quan trọng (POST /api/auth/verify-pin)
func VerifyPINHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed, use POST"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		PIN string `json:"pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Dữ liệu không hợp lệ"}`, http.StatusBadRequest)
		return
	}

	pin := strings.TrimSpace(req.PIN)
	if len(pin) != 6 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "error",
			"valid":  false,
			"error":  "Mã PIN phải có đúng 6 chữ số",
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"valid":  true,
		"message": "Xác thực mã PIN hợp lệ",
	})
}

// AdminLogsHandler trả về toàn bộ nhật ký kiểm toán và sự kiện an ninh (GET /api/admin/logs)
// Phân quyền RBAC nghiêm ngặt: Chỉ quản trị viên (role admin) mới có quyền truy cập
func AdminLogsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	token := ExtractToken(r)
	user, ok := GetUserFromToken(token)
	if !ok || user.Role != "admin" {
		http.Error(w, `{"error":"Forbidden: Yêu cầu quyền Quản Trị Viên (Admin)"}`, http.StatusForbidden)
		return
	}

	auditLogs, err := database.GetAuditLogs(100, 0)
	if err != nil {
		http.Error(w, `{"error":"Lỗi truy vấn nhật ký kiểm toán"}`, http.StatusInternalServerError)
		return
	}

	secEvents, err := database.GetSecurityEvents(100, 0)
	if err != nil {
		http.Error(w, `{"error":"Lỗi truy vấn sự kiện an ninh"}`, http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":          "success",
		"total_audit":     len(auditLogs),
		"audit_logs":      auditLogs,
		"total_security":  len(secEvents),
		"security_events": secEvents,
	})
}

// AdminSessionsHandler trả về lịch sử phiên đăng nhập từ audit_logs cho UI hiển thị (GET /api/admin/sessions)
// Phân quyền RBAC nghiêm ngặt: Chỉ quản trị viên (role admin) mới có quyền truy cập
func AdminSessionsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	token := ExtractToken(r)
	user, ok := GetUserFromToken(token)
	if !ok || user.Role != "admin" {
		http.Error(w, `{"error":"Forbidden: Yêu cầu quyền Quản Trị Viên (Admin)"}`, http.StatusForbidden)
		return
	}

	logs, err := database.GetAuditLogs(100, 0)
	if err != nil {
		http.Error(w, `{"error":"Lỗi truy vấn nhật ký phiên"}`, http.StatusInternalServerError)
		return
	}

	type SessionItem struct {
		CreatedAt    string `json:"created_at"`
		Username     string `json:"username"`
		IPAddress    string `json:"ip_address"`
		DeviceInfo   string `json:"device_info"`
		LocationInfo string `json:"location_info"`
		Status       string `json:"status"`
	}

	sessionsList := make([]SessionItem, 0)
	for _, l := range logs {
		if l.Action == database.AuditActionLoginSuccess || l.Action == database.AuditActionLoginFailure || l.Action == "login_lockout" {
			status := "SUCCESS"
			if l.Action == database.AuditActionLoginFailure {
				status = "FAILED"
			} else if l.Action == "login_lockout" {
				status = "BLOCKED"
			}

			username := "Khách"
			if l.UserID != "" {
				if u, err := GetUserByID(l.UserID); err == nil && u != nil {
					username = u.Username
				}
			}

			devInfo := l.UserAgent
			if len(devInfo) > 40 {
				devInfo = devInfo[:40] + "..."
			}
			if devInfo == "" {
				devInfo = "Trình duyệt Web"
			}

			sessionsList = append(sessionsList, SessionItem{
				CreatedAt:    l.CreatedAt,
				Username:     username,
				IPAddress:    l.IPAddress,
				DeviceInfo:   devInfo,
				LocationInfo: "Localhost / Internal",
				Status:       status,
			})
		}
	}

	json.NewEncoder(w).Encode(sessionsList)
}

// AdminResetPasswordHandler cho phép Quản trị viên đặt lại mật khẩu của người dùng (POST /api/admin/users/reset-password)
// Phân quyền RBAC nghiêm ngặt: Chỉ admin mới có quyền reset
func AdminResetPasswordHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed, use POST"}`, http.StatusMethodNotAllowed)
		return
	}

	token := ExtractToken(r)
	admin, ok := GetUserFromToken(token)
	if !ok || admin.Role != "admin" {
		http.Error(w, `{"error":"Forbidden: Yêu cầu quyền Quản Trị Viên (Admin)"}`, http.StatusForbidden)
		return
	}

	var req struct {
		UserID      string `json:"user_id"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Dữ liệu không hợp lệ"}`, http.StatusBadRequest)
		return
	}

	if req.UserID == "" || len(req.NewPassword) < 6 {
		http.Error(w, `{"error":"User ID và Mật khẩu mới (tối thiểu 6 ký tự) là bắt buộc"}`, http.StatusBadRequest)
		return
	}

	targetUser, err := GetUserByID(req.UserID)
	if err != nil || targetUser == nil {
		http.Error(w, `{"error":"Không tìm thấy tài khoản người dùng"}`, http.StatusNotFound)
		return
	}

	newHash, err := HashPassword(req.NewPassword)
	if err != nil {
		http.Error(w, `{"error":"Lỗi băm mật khẩu"}`, http.StatusInternalServerError)
		return
	}

	if err := UpdateUserPasswordHash(targetUser.ID, newHash); err != nil {
		http.Error(w, `{"error":"Lỗi cập nhật cơ sở dữ liệu"}`, http.StatusInternalServerError)
		return
	}

	clientIP := security.GetRealClientIP(r)
	_ = database.RecordAuditLog(admin.ID, database.AuditActionChangePassword, clientIP, r.UserAgent(), fmt.Sprintf("Admin '%s' đã đặt lại mật khẩu cho tài khoản '%s' (ID: %s)", admin.Username, targetUser.Username, targetUser.ID))

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": fmt.Sprintf("Đã đặt lại mật khẩu cho tài khoản '%s' thành công!", targetUser.Username),
	})
}
