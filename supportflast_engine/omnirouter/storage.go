package omnirouter

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	keyCacheMu sync.RWMutex
	keyCache   = make(map[string]*ClientKey)

	providerCacheMu sync.RWMutex
	providerCache   []ProviderConnection
)

// InitStorage khởi tạo các bảng CSDL cho OmniRouter trên Aiven PostgreSQL hoặc SQLite
func InitStorage(db *sql.DB, isCloudDB bool) error {
	if db == nil {
		log.Printf("[ENGINE] [STORAGE] [WARN] db là nil, bỏ qua InitStorage (chế độ in-memory cache)")
		return nil
	}
	var queries []string

	if isCloudDB {
		queries = []string{
			`CREATE TABLE IF NOT EXISTS omni_providers (
				id VARCHAR(64) NOT NULL PRIMARY KEY,
				provider VARCHAR(64) NOT NULL,
				auth_type VARCHAR(32) NOT NULL,
				name VARCHAR(191) NOT NULL,
				email VARCHAR(191) DEFAULT '',
				priority INT DEFAULT 1,
				is_active INT DEFAULT 1,
				data TEXT NOT NULL,
				created_at VARCHAR(64) NOT NULL,
				updated_at VARCHAR(64) NOT NULL
			);`,
			`CREATE INDEX IF NOT EXISTS idx_omni_prov_active ON omni_providers (is_active, priority);`,

			`CREATE TABLE IF NOT EXISTS omni_client_keys (
				id VARCHAR(64) NOT NULL PRIMARY KEY,
				name VARCHAR(128) NOT NULL,
				api_key VARCHAR(191) NOT NULL UNIQUE,
				key_hash VARCHAR(191) DEFAULT '',
				used_requests BIGINT DEFAULT 0,
				total_tokens BIGINT DEFAULT 0,
				quota_limit BIGINT DEFAULT 0,
				expires_at VARCHAR(64) DEFAULT '',
				duration_hours INT DEFAULT 0,
				is_active INT DEFAULT 1,
				created_at VARCHAR(64) NOT NULL,
				last_used_at VARCHAR(64) DEFAULT ''
			);`,
			`CREATE INDEX IF NOT EXISTS idx_omni_key_active ON omni_client_keys (is_active);`,

			`CREATE TABLE IF NOT EXISTS omni_settings (
				id INT NOT NULL PRIMARY KEY,
				data TEXT NOT NULL
			);`,

			`CREATE TABLE IF NOT EXISTS omni_proxy_pools (
				id VARCHAR(64) NOT NULL PRIMARY KEY,
				is_active INT DEFAULT 1,
				test_status VARCHAR(64) DEFAULT '',
				data TEXT NOT NULL,
				created_at VARCHAR(64) NOT NULL,
				updated_at VARCHAR(64) NOT NULL
			);`,

			`CREATE TABLE IF NOT EXISTS omni_kv (
				scope VARCHAR(64) NOT NULL,
				key_name VARCHAR(128) NOT NULL,
				val_data TEXT NOT NULL,
				PRIMARY KEY (scope, key_name)
			);`,

			`CREATE TABLE IF NOT EXISTS omni_usage_history (
				id BIGSERIAL PRIMARY KEY,
				timestamp VARCHAR(64) NOT NULL,
				provider VARCHAR(64) NOT NULL,
				model VARCHAR(128) NOT NULL,
				connection_id VARCHAR(64) NOT NULL,
				api_key VARCHAR(128) DEFAULT '',
				endpoint VARCHAR(128) DEFAULT '',
				prompt_tokens BIGINT DEFAULT 0,
				completion_tokens BIGINT DEFAULT 0,
				total_tokens BIGINT DEFAULT 0,
				cost DOUBLE PRECISION DEFAULT 0,
				status VARCHAR(64) DEFAULT 'success',
				tokens TEXT,
				meta TEXT,
				latency_ms BIGINT DEFAULT 0,
				ttft_ms BIGINT DEFAULT 0,
				tps DOUBLE PRECISION DEFAULT 0,
				cached_tokens BIGINT DEFAULT 0,
				status_code INT DEFAULT 200,
				error_code VARCHAR(64) DEFAULT ''
			);`,
			`CREATE INDEX IF NOT EXISTS idx_omni_uh_ts ON omni_usage_history (timestamp);`,
			`CREATE INDEX IF NOT EXISTS idx_omni_uh_conn ON omni_usage_history (connection_id);`,
		}
	} else {
		queries = []string{
			`CREATE TABLE IF NOT EXISTS omni_providers (
				id TEXT PRIMARY KEY,
				provider TEXT NOT NULL,
				auth_type TEXT NOT NULL,
				name TEXT NOT NULL,
				email TEXT DEFAULT '',
				priority INTEGER DEFAULT 1,
				is_active INTEGER DEFAULT 1,
				data TEXT NOT NULL,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL
			);`,
			`CREATE INDEX IF NOT EXISTS idx_omni_prov_active ON omni_providers(is_active, priority);`,

			`CREATE TABLE IF NOT EXISTS omni_client_keys (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				api_key TEXT UNIQUE NOT NULL,
				key_hash TEXT DEFAULT '',
				used_requests INTEGER DEFAULT 0,
				total_tokens INTEGER DEFAULT 0,
				quota_limit INTEGER DEFAULT 0,
				expires_at TEXT DEFAULT '',
				duration_hours INTEGER DEFAULT 0,
				is_active INTEGER DEFAULT 1,
				created_at TEXT NOT NULL,
				last_used_at TEXT DEFAULT ''
			);`,
			`CREATE INDEX IF NOT EXISTS idx_omni_key_active ON omni_client_keys(is_active);`,

			`CREATE TABLE IF NOT EXISTS omni_settings (
				id INTEGER PRIMARY KEY,
				data TEXT NOT NULL
			);`,

			`CREATE TABLE IF NOT EXISTS omni_proxy_pools (
				id TEXT PRIMARY KEY,
				is_active INTEGER DEFAULT 1,
				test_status TEXT DEFAULT '',
				data TEXT NOT NULL,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL
			);`,

			`CREATE TABLE IF NOT EXISTS omni_kv (
				scope TEXT NOT NULL,
				key_name TEXT NOT NULL,
				val_data TEXT NOT NULL,
				PRIMARY KEY (scope, key_name)
			);`,

			`CREATE TABLE IF NOT EXISTS omni_usage_history (
				id SERIAL PRIMARY KEY,
				timestamp TEXT NOT NULL,
				provider TEXT NOT NULL,
				model TEXT NOT NULL,
				connection_id TEXT NOT NULL,
				api_key TEXT DEFAULT '',
				endpoint TEXT DEFAULT '',
				prompt_tokens INTEGER DEFAULT 0,
				completion_tokens INTEGER DEFAULT 0,
				total_tokens INTEGER DEFAULT 0,
				cost REAL DEFAULT 0,
				status TEXT DEFAULT 'success',
				tokens TEXT,
				meta TEXT,
				latency_ms INTEGER DEFAULT 0,
				ttft_ms INTEGER DEFAULT 0,
				tps REAL DEFAULT 0,
				cached_tokens INTEGER DEFAULT 0,
				status_code INTEGER DEFAULT 200,
				error_code TEXT DEFAULT ''
			);`,
			`CREATE INDEX IF NOT EXISTS idx_omni_uh_ts ON omni_usage_history(timestamp);`,
			`CREATE INDEX IF NOT EXISTS idx_omni_uh_conn ON omni_usage_history(connection_id);`,
		}
	}

	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			// Nếu gặp lỗi cú pháp PostgreSQL SERIAL trên SQLite, fallback về AUTOINCREMENT
			if strings.Contains(q, "id SERIAL PRIMARY KEY") {
				fallbackQ := strings.Replace(q, "id SERIAL PRIMARY KEY", "id INTEGER PRIMARY KEY AUTOINCREMENT", 1)
				if _, fbErr := db.Exec(fallbackQ); fbErr == nil {
					continue
				}
			}
			return fmt.Errorf("lỗi khởi tạo bảng omnirouter: %w (query: %s)", err, q)
		}
	}

	// Tự động thêm cột mới nếu bảng omni_client_keys đã được tạo từ trước (Migration an toàn không gián đoạn)
	var alterCols []string
	if isCloudDB {
		alterCols = []string{
			"ALTER TABLE omni_client_keys ADD COLUMN quota_limit BIGINT DEFAULT 0",
			"ALTER TABLE omni_client_keys ADD COLUMN expires_at VARCHAR(64) DEFAULT ''",
			"ALTER TABLE omni_client_keys ADD COLUMN duration_hours INT DEFAULT 0",
		}
	} else {
		alterCols = []string{
			"ALTER TABLE omni_client_keys ADD COLUMN quota_limit INTEGER DEFAULT 0",
			"ALTER TABLE omni_client_keys ADD COLUMN expires_at TEXT DEFAULT ''",
			"ALTER TABLE omni_client_keys ADD COLUMN duration_hours INTEGER DEFAULT 0",
		}
	}
	for _, alterSQL := range alterCols {
		_, _ = db.Exec(alterSQL)
	}

	// Migration cho PostgreSQL: Đảm bảo cột id của omni_usage_history có sequence tự tăng nếu thiếu
	_, _ = db.Exec("CREATE SEQUENCE IF NOT EXISTS omni_usage_history_id_seq;")
	_, _ = db.Exec("ALTER TABLE omni_usage_history ALTER COLUMN id SET DEFAULT nextval('omni_usage_history_id_seq');")

	// Đảm bảo Master Keys của hệ thống luôn tồn tại trong DB để phục vụ liên kết máy chủ
	now := NowISO()
	seedKeys := []struct {
		id   string
		name string
		key  string
	}{
		{"key_master_domain_cloud", "Master Web Domain Key (" + DefaultDomain + ")", "sk-" + DefaultDomain + "-master-live"},
		{"key_master_gateway_secret", "Master Gateway Secret", "sk-9r-master-live-gateway-secret"},
		{"key_master_legacy", "Legacy Master Key", "sk-9router-master-live"},
	}
	for _, sk := range seedKeys {
		var exists int
		_ = db.QueryRow("SELECT COUNT(*) FROM omni_client_keys WHERE api_key = ?", sk.key).Scan(&exists)
		if exists == 0 {
			_, _ = db.Exec(`
				INSERT INTO omni_client_keys (id, name, api_key, key_hash, used_requests, total_tokens, is_active, created_at, last_used_at)
				VALUES (?, ?, ?, ?, 0, 0, 1, ?, ?)
			`, sk.id, sk.name, sk.key, HashString(sk.key), now, now)
		}
	}

	// Xóa vĩnh viễn và triệt để các khóa API mẫu không liên quan (Default Internal App & VS Code Antigravity IDE)
	_, _ = db.Exec(`
		DELETE FROM omni_client_keys 
		WHERE id IN ('key_default_internal_app', 'key_vscode_antigravity_ide', 'key_legacy_default_internal', 'key_legacy_antigravity_ide') 
		   OR name IN ('Default Internal App', 'VS Code Antigravity IDE', 'Legacy Default Internal App', 'Legacy VS Code Antigravity IDE') 
		   OR api_key IN ('sk-sf-live-default-8f42', 'sk-sf-live-antigravity-b198', 'sk-nx-live-9router-default-8f42', 'sk-nx-live-antigravity-b190')
	`)

	// Tự động nhận diện và đăng ký 4 nhà cung cấp API model miễn phí (OpenRouter, Groq, Mistral, Cloudflare) từ biến môi trường
	SeedEnvProviders(db)

	// Tự động nạp cố định toàn bộ tài khoản AI vào CSDL (Aiven PostgreSQL hoặc SQLite fallback) nếu omni_providers đang trống
	if flag.Lookup("test.v") == nil {
		var provCount int
		_ = db.QueryRow("SELECT COUNT(*) FROM omni_providers").Scan(&provCount)
		if provCount == 0 {
			_ = SeedEmbeddedDataToDB(db)
		}
	}

	log.Println("[ENGINE] [OMNIROUTER] Đã khởi tạo hoàn tất cấu trúc lưu trữ SupportFlast Router trong Database")
	// Nạp sẵn cache
	RefreshKeyCache(db)
	RefreshProviderCache(db)
	return nil
}

// SeedEnvProviders tự động đăng ký hoặc cập nhật 4 nhà cung cấp API model miễn phí (OpenRouter, Groq, Mistral, Cloudflare) nếu có cấu hình trong môi trường/env
func SeedEnvProviders(db *sql.DB) {
	if db == nil {
		return
	}
	now := NowISO()

	type envProv struct {
		id       string
		name     string
		provider string
		authType string
		apiKey   string
		baseURL  string
		extra    map[string]interface{}
		priority int
	}

	var candidates []envProv

	// 1. OpenRouter
	if key := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")); key != "" {
		bURL := strings.TrimSpace(os.Getenv("OPENROUTER_BASE_URL"))
		if bURL == "" {
			bURL = "https://openrouter.ai/api/v1"
		}
		candidates = append(candidates, envProv{
			id:       "prov_openrouter_env",
			name:     "OpenRouter AI (Free/Multi-Model)",
			provider: "openrouter",
			authType: "api_key",
			apiKey:   key,
			baseURL:  bURL,
			priority: 2,
		})
	}

	// 2. Groq
	if key := strings.TrimSpace(os.Getenv("GROQ_API_KEY")); key != "" {
		bURL := strings.TrimSpace(os.Getenv("GROQ_BASE_URL"))
		if bURL == "" {
			bURL = "https://api.groq.com/openai/v1"
		}
		candidates = append(candidates, envProv{
			id:       "prov_groq_env",
			name:     "Groq Cloud (Ultra-Fast Inference)",
			provider: "groq",
			authType: "api_key",
			apiKey:   key,
			baseURL:  bURL,
			priority: 1,
		})
	}

	// 3. Mistral AI
	if key := strings.TrimSpace(os.Getenv("MISTRAL_API_KEY")); key != "" {
		bURL := strings.TrimSpace(os.Getenv("MISTRAL_BASE_URL"))
		if bURL == "" {
			bURL = "https://api.mistral.ai/v1"
		}
		candidates = append(candidates, envProv{
			id:       "prov_mistral_env",
			name:     "Mistral AI (Independent Fallback)",
			provider: "mistral",
			authType: "api_key",
			apiKey:   key,
			baseURL:  bURL,
			priority: 3,
		})
	}

	// 4. Cloudflare Workers AI
	if token := strings.TrimSpace(os.Getenv("CLOUDFLARE_API_TOKEN")); token != "" {
		accID := strings.TrimSpace(os.Getenv("CLOUDFLARE_ACCOUNT_ID"))
		bURL := strings.TrimSpace(os.Getenv("CLOUDFLARE_BASE_URL"))
		if bURL == "" && accID != "" {
			bURL = fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/ai/v1", accID)
		}
		candidates = append(candidates, envProv{
			id:       "prov_cloudflare_env",
			name:     "Cloudflare Workers AI",
			provider: "cloudflare",
			authType: "api_token",
			apiKey:   token,
			baseURL:  bURL,
			extra: map[string]interface{}{
				"projectId": accID,
			},
			priority: 4,
		})
	}

	for _, cp := range candidates {
		dataMap := map[string]interface{}{
			"apiKey":      cp.apiKey,
			"accessToken": cp.apiKey,
			"baseUrl":     cp.baseURL,
			"source":      "env_auto_seed",
		}
		if cp.extra != nil {
			for ek, ev := range cp.extra {
				dataMap[ek] = ev
			}
		}
		dataJSON, _ := json.Marshal(dataMap)

		var existingCount int
		_ = db.QueryRow("SELECT COUNT(*) FROM omni_providers WHERE id = ?", cp.id).Scan(&existingCount)
		if existingCount == 0 {
			_, err := db.Exec(`
				INSERT INTO omni_providers (id, provider, auth_type, name, email, priority, is_active, data, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?, ?)
			`, cp.id, cp.provider, cp.authType, cp.name, cp.provider+"@system.local", cp.priority, string(dataJSON), now, now)
			if err == nil {
				log.Printf("[ENGINE] [OMNIROUTER] ✔ Đã tự động đăng ký Provider từ ENV: %s (%s)", cp.name, cp.provider)
			}
		} else {
			_, _ = db.Exec(`
				UPDATE omni_providers 
				SET data = ?, priority = ?, is_active = 1, updated_at = ? 
				WHERE id = ?
			`, string(dataJSON), cp.priority, now, cp.id)
		}
	}
}

// DefaultDomain là tên miền web chính thức của hệ thống (sử dụng subdomain www để tránh redirect 301 từ Cloudflare)
const DefaultDomain = "www.supportflastdev.io.vn"

// GenerateAPIKey tạo chuỗi API Key duy nhất có chứa tên miền web: sk-supportflastdev.io.vn-xxxxxxxx
func GenerateAPIKey() string {
	return GenerateAPIKeyWithDomain(DefaultDomain)
}

// GenerateAPIKeyWithDomain tạo chuỗi API Key duy nhất có chứa tên miền web chỉ định
func GenerateAPIKeyWithDomain(domain string) string {
	d := strings.TrimSpace(domain)
	if d == "" {
		d = DefaultDomain
	}
	d = strings.TrimPrefix(d, "https://")
	d = strings.TrimPrefix(d, "http://")
	d = strings.Split(d, "/")[0]
	d = strings.Split(d, ":")[0]
	if d == "" {
		d = DefaultDomain
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "sk-" + d + "-" + hex.EncodeToString(b)
}

// HashString băm chuỗi SHA-256
func HashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// RefreshKeyCache nạp toàn bộ API key hợp lệ lên RAM để kiểm tra 0ms
func RefreshKeyCache(db *sql.DB) {
	keyCacheMu.Lock()
	if keyCache == nil {
		keyCache = make(map[string]*ClientKey)
	}
	// Luôn nạp sẵn Master Keys trên RAM để xác thực tức thì 0ms
	systemMasterKeys := []string{
		"sk-" + DefaultDomain + "-master-live",
		"sk-supportflastdev.io.vn-master-live",
		"sk-www.supportflastdev.io.vn-master-live",
		"sk-www.supportflastdev.io.vn-4b134c2b19bae0bf106c19a6f94154f9",
		"sk-9r-master-live-gateway-secret",
		"sk-9router-master-live",
		"sk-" + DefaultDomain + "-admin-live",
		"sk-www.supportflastdev.io.vn-admin-live",
		"6929c9313fb35a0b",
	}
	for _, mk := range systemMasterKeys {
		if entry, exists := keyCache[mk]; !exists {
			keyCache[mk] = &ClientKey{
				ID:         "master_" + HashString(mk)[:12],
				Name:       "Master System Key",
				Key:        mk,
				KeyHash:    HashString(mk),
				QuotaLimit: 0,
				IsActive:   1,
				CreatedAt:  "2026-01-01T00:00:00Z",
			}
		} else if entry != nil {
			entry.IsActive = 1
			entry.QuotaLimit = 0
		}
	}
	keyCacheMu.Unlock()

	if db == nil {
		return
	}
	// Tự động chuyển các key đã vượt quá hạn mức Quota sang TẠM KHÓA (is_active = 0)
	if _, err := db.Exec("UPDATE omni_client_keys SET is_active = 0 WHERE quota_limit > 0 AND used_requests >= quota_limit AND is_active = 1"); err != nil {
		log.Printf("[ENGINE] [USAGE] ❌ Lỗi tự động khóa API key vượt quota trong RefreshKeyCache: %v", err)
	}

	rows, err := db.Query("SELECT id, name, api_key, key_hash, used_requests, total_tokens, quota_limit, expires_at, duration_hours, is_active, created_at, last_used_at FROM omni_client_keys")
	if err != nil {
		return
	}
	defer rows.Close()

	keyCacheMu.Lock()
	defer keyCacheMu.Unlock()
	keyCache = make(map[string]*ClientKey)

	for rows.Next() {
		var k ClientKey
		if err := rows.Scan(&k.ID, &k.Name, &k.Key, &k.KeyHash, &k.UsedRequests, &k.TotalTokens, &k.QuotaLimit, &k.ExpiresAt, &k.DurationHours, &k.IsActive, &k.CreatedAt, &k.LastUsedAt); err == nil {
			keyCopy := k
			keyCache[keyCopy.Key] = &keyCopy
		}
	}

	// Đảm bảo 100% các Master System Key và CLI Token luôn hiện diện trong RAM cache
	systemMasterKeys = []string{
		"sk-supportflastdev.io.vn-master-live",
		"sk-www.supportflastdev.io.vn-master-live",
		"sk-www.supportflastdev.io.vn-4b134c2b19bae0bf106c19a6f94154f9",
		"sk-9r-master-live-gateway-secret",
		"sk-9router-master-live",
		"sk-supportflastdev.io.vn-admin-live",
		"sk-www.supportflastdev.io.vn-admin-live",
		"sk-" + DefaultDomain + "-master-live",
		"sk-" + DefaultDomain + "-admin-live",
		"6929c9313fb35a0b",
	}
	for _, mk := range systemMasterKeys {
		if entry, exists := keyCache[mk]; !exists {
			keyCache[mk] = &ClientKey{
				ID:         "master_" + HashString(mk)[:12],
				Name:       "Master System Key",
				Key:        mk,
				KeyHash:    HashString(mk),
				QuotaLimit: 0,
				IsActive:   1,
				CreatedAt:  "2026-01-01T00:00:00Z",
			}
		} else if entry != nil {
			entry.IsActive = 1
			entry.QuotaLimit = 0
		}
	}
}

// CheckClientKeyStatus kiểm tra chi tiết trạng thái của Client Key (RAM cache 0ms + constant-time comparison)
func CheckClientKeyStatus(rawKey string) (item *ClientKey, status string, errMsg string) {
	keyCacheMu.RLock()
	var matchedKey string
	var kCopy ClientKey
	var found bool
	rawBytes := []byte(rawKey)
	for cachedK, k := range keyCache {
		if subtle.ConstantTimeCompare([]byte(cachedK), rawBytes) == 1 {
			matchedKey = cachedK
			if k != nil {
				kCopy = *k
				found = true
			}
			break
		}
	}
	keyCacheMu.RUnlock()

	if !found {
		return nil, "NOT_FOUND", "API Key không tồn tại hoặc không hợp lệ"
	}

	// 1. Kiểm tra hạn mức Quota requests nếu có cấu hình QuotaLimit > 0 (ưu tiên phát hiện lý do vượt Quota)
	if kCopy.QuotaLimit > 0 && kCopy.UsedRequests >= kCopy.QuotaLimit {
		// Mutate trạng thái IsActive = 0 trong cache: BẮT BUỘC dùng Write Lock (Lock), không sửa khi giữ RLock!
		keyCacheMu.Lock()
		if it, exists := keyCache[matchedKey]; exists && it != nil {
			it.IsActive = 0
		}
		keyCacheMu.Unlock()
		kCopy.IsActive = 0
		return &kCopy, "QUOTA_EXCEEDED", fmt.Sprintf("API Key đã sử dụng hết hạn mức Quota (%d/%d requests)", kCopy.UsedRequests, kCopy.QuotaLimit)
	}

	// 2. Kiểm tra thời hạn hiệu lực theo giờ nếu có ExpiresAt
	if kCopy.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, kCopy.ExpiresAt); err == nil {
			if time.Now().After(t) {
				// Mutate trạng thái IsActive = 0 trong cache: BẮT BUỘC dùng Write Lock (Lock), không sửa khi giữ RLock!
				keyCacheMu.Lock()
				if it, exists := keyCache[matchedKey]; exists && it != nil {
					it.IsActive = 0
				}
				keyCacheMu.Unlock()
				kCopy.IsActive = 0
				return &kCopy, "EXPIRED", fmt.Sprintf("API Key đã hết thời hạn sử dụng (Hết hạn lúc: %s)", t.Format("15:04:05 02/01/2006"))
			}
		}
	}

	// 3. Kiểm tra trạng thái kích hoạt thủ công
	if kCopy.IsActive != 1 {
		return &kCopy, "INACTIVE", "API Key đã bị tạm khóa"
	}

	return &kCopy, "OK", ""
}

var (
	externalKeyValidatorMu sync.RWMutex
	externalKeyValidator   func(rawKey string) (*ClientKey, bool)
)

// SetExternalKeyValidator đăng ký bộ thẩm định khóa bên ngoài (tích hợp với SupportFlast Hub Registry & JWT Auth)
func SetExternalKeyValidator(v func(rawKey string) (*ClientKey, bool)) {
	externalKeyValidatorMu.Lock()
	defer externalKeyValidatorMu.Unlock()
	externalKeyValidator = v
}

// ValidateClientKey kiểm tra xem client key có hợp lệ và còn hiệu lực không
func ValidateClientKey(rawKey string) (*ClientKey, bool) {
	item, status, _ := CheckClientKeyStatus(rawKey)
	if status == "OK" && item != nil {
		return item, true
	}
	externalKeyValidatorMu.RLock()
	v := externalKeyValidator
	externalKeyValidatorMu.RUnlock()
	if v != nil {
		if extItem, ok := v(rawKey); ok && extItem != nil {
			return extItem, true
		}
	}
	return nil, false
}

// CreateNewClientKey sinh mã API Key mới chứa tên miền web và lưu xuống DB + RAM
func CreateNewClientKey(db *sql.DB, name string) (*ClientKey, error) {
	return CreateNewClientKeyWithDomain(db, name, DefaultDomain)
}

// CreateNewClientKeyWithQuotaAndExpiry sinh mã API Key mới chứa tên miền, hạn mức quota và thời hạn theo giờ
func CreateNewClientKeyWithQuotaAndExpiry(db *sql.DB, name string, domain string, quotaLimit int64, durationHours int) (*ClientKey, error) {
	if strings.TrimSpace(name) == "" {
		name = "Antigravity-Device"
	}
	keyStr := GenerateAPIKeyWithDomain(domain)
	rb := make([]byte, 4)
	_, _ = rand.Read(rb)
	id := fmt.Sprintf("key_%d_%s", time.Now().UnixNano(), hex.EncodeToString(rb))
	nowTime := time.Now()
	now := nowTime.Format(time.RFC3339)
	keyHash := HashString(keyStr)

	expiresAt := ""
	if durationHours > 0 {
		expiresAt = nowTime.Add(time.Duration(durationHours) * time.Hour).Format(time.RFC3339)
	}
	if quotaLimit < 0 {
		quotaLimit = 0
	}

	_, err := db.Exec(`
		INSERT INTO omni_client_keys (id, name, api_key, key_hash, used_requests, total_tokens, quota_limit, expires_at, duration_hours, is_active, created_at, last_used_at)
		VALUES (?, ?, ?, ?, 0, 0, ?, ?, ?, 1, ?, ?)
	`, id, name, keyStr, keyHash, quotaLimit, expiresAt, durationHours, now, now)
	if err != nil {
		return nil, err
	}

	item := &ClientKey{
		ID:            id,
		Name:          name,
		Key:           keyStr,
		KeyHash:       keyHash,
		UsedRequests:  0,
		TotalTokens:   0,
		QuotaLimit:    quotaLimit,
		ExpiresAt:     expiresAt,
		DurationHours: durationHours,
		IsActive:      1,
		CreatedAt:     now,
		LastUsedAt:    now,
	}

	keyCacheMu.Lock()
	keyCache[keyStr] = item
	keyCacheMu.Unlock()

	return item, nil
}

// CreateNewClientKeyWithDomain sinh mã API Key mới chứa tên miền chỉ định và lưu xuống DB + RAM
func CreateNewClientKeyWithDomain(db *sql.DB, name string, domain string) (*ClientKey, error) {
	return CreateNewClientKeyWithQuotaAndExpiry(db, name, domain, 0, 0)
}

// ListAllClientKeys lấy danh sách API Key thông thường (ẨN HOÀN TOÀN Master Web Domain Key và các khóa mặc định)
func ListAllClientKeys(db *sql.DB) ([]ClientKey, error) {
	if db == nil {
		keyCacheMu.RLock()
		defer keyCacheMu.RUnlock()
		var list []ClientKey
		for _, k := range keyCache {
			if k != nil {
				list = append(list, *k)
			}
		}
		return list, nil
	}

	rows, err := db.Query(`
		SELECT id, name, api_key, key_hash, used_requests, total_tokens, quota_limit, expires_at, duration_hours, is_active, created_at, last_used_at 
		FROM omni_client_keys 
		WHERE id NOT LIKE 'key_master_%' 
		  AND id NOT LIKE 'key_legacy_%' 
		  AND api_key NOT LIKE '%master-live%' 
		  AND name NOT LIKE '%Master%' 
		  AND id NOT IN ('key_default_internal_app', 'key_vscode_antigravity_ide')
		  AND name NOT IN ('Default Internal App', 'VS Code Antigravity IDE')
		  AND api_key NOT IN ('sk-sf-live-default-8f42', 'sk-sf-live-antigravity-b198')
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []ClientKey
	for rows.Next() {
		var k ClientKey
		if err := rows.Scan(&k.ID, &k.Name, &k.Key, &k.KeyHash, &k.UsedRequests, &k.TotalTokens, &k.QuotaLimit, &k.ExpiresAt, &k.DurationHours, &k.IsActive, &k.CreatedAt, &k.LastUsedAt); err == nil {
			list = append(list, k)
		}
	}
	return list, nil
}

// RevokeClientKey khóa một API Key
func RevokeClientKey(db *sql.DB, id string) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec("UPDATE omni_client_keys SET is_active = 0 WHERE id = ?", id)
	if err == nil {
		RefreshKeyCache(db)
	}
	return err
}

// DeleteClientKey xóa vĩnh viễn một API Key
func DeleteClientKey(db *sql.DB, id string) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec("DELETE FROM omni_client_keys WHERE id = ?", id)
	if err == nil {
		RefreshKeyCache(db)
	}
	return err
}

// RecordKeyUsageAsync cập nhật số lượt và số token tiêu thụ ngầm
func RecordKeyUsageAsync(db *sql.DB, apiKey string, reqCount int64, tokenCount int64) {
	if apiKey == "" {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[ENGINE] [USAGE] ⚠️ Recovered từ panic trong RecordKeyUsageAsync cho key %s: %v", apiKey, r)
			}
		}()

		now := NowISO()
		if db != nil {
			var errExec error
			// Cơ chế retry nhẹ (tối đa 3 lần) cho DB write nếu gặp lock hoặc lỗi tạm thời
			for attempt := 1; attempt <= 3; attempt++ {
				_, errExec = db.Exec(`
				UPDATE omni_client_keys 
				SET used_requests = used_requests + ?, 
				    total_tokens = total_tokens + ?,
				    last_used_at = ?
				WHERE api_key = ?
			`, reqCount, tokenCount, now, apiKey)
				if errExec == nil {
					break
				}
				if attempt < 3 {
					time.Sleep(10 * time.Millisecond)
				}
			}

			if errExec != nil {
				log.Printf("[ENGINE] [USAGE] ❌ Lỗi ghi nhận usage cho key %s: %v", apiKey, errExec)
			}

			// Tự động KHÓA key ngay lập tức nếu đã chạm hoặc vượt hạn mức Quota
			var usedReqs, quotaLim int64
			errQuery := db.QueryRow("SELECT used_requests, quota_limit FROM omni_client_keys WHERE api_key = ?", apiKey).Scan(&usedReqs, &quotaLim)
			if errQuery == nil {
				if quotaLim > 0 && usedReqs >= quotaLim {
					var errLock error
					for attempt := 1; attempt <= 3; attempt++ {
						_, errLock = db.Exec("UPDATE omni_client_keys SET is_active = 0 WHERE api_key = ?", apiKey)
						if errLock == nil {
							break
						}
						if attempt < 3 {
							time.Sleep(10 * time.Millisecond)
						}
					}
					if errLock != nil {
						log.Printf("[ENGINE] [USAGE] ❌ Lỗi khóa API key vượt quota trong DB cho key %s: %v", apiKey, errLock)
					} else {
						log.Printf("[ENGINE] [QUOTA] 🛑 API Key %s đã chạm/vượt hạn mức Quota (%d/%d req). Đã tự động KHÓA key!", apiKey, usedReqs, quotaLim)
					}
				}
			} else if errQuery != sql.ErrNoRows {
				log.Printf("[ENGINE] [USAGE] ❌ Lỗi kiểm tra quota cho key %s: %v", apiKey, errQuery)
			}
		}

		keyCacheMu.Lock()
		if item, ok := keyCache[apiKey]; ok {
			item.UsedRequests += reqCount
			item.TotalTokens += tokenCount
			item.LastUsedAt = now
			if item.QuotaLimit > 0 && item.UsedRequests >= item.QuotaLimit {
				item.IsActive = 0
			}
		}
		keyCacheMu.Unlock()
	}()
}

// ReserveClientKeyQuota thực hiện giữ chỗ (reservation) quota nguyên tử trước khi gọi AI upstream
// Trả về true nếu giữ chỗ thành công; false nếu đã hết quota hoặc key không hợp lệ
// Hoàn toàn chống race condition ở cả chế độ Database lẫn chế độ RAM Cache fallback (Rule 3 & P0)
func ReserveClientKeyQuota(db *sql.DB, apiKey string) (bool, string) {
	if apiKey == "" {
		return false, "Thiếu API Key"
	}

	// 1. Chế độ Database: Atomic Reservation trực tiếp bằng UPDATE nguyên tử
	if db != nil {
		res, err := db.Exec(`
			UPDATE omni_client_keys 
			SET used_requests = used_requests + 1,
			    is_active = CASE WHEN quota_limit > 0 AND (used_requests + 1) >= quota_limit THEN 0 ELSE 1 END,
			    last_used_at = ?
			WHERE api_key = ? 
			  AND is_active = 1 
			  AND (quota_limit = 0 OR used_requests < quota_limit)
		`, NowISO(), apiKey)
		if err != nil {
			log.Printf("[ENGINE] [ERROR] Lỗi Atomic Reservation quota trên DB: %v", err)
			return false, "Lỗi kiểm tra hạn mức cơ sở dữ liệu"
		}
		rows, _ := res.RowsAffected()
		if rows == 0 {
			// Không cập nhật được dòng nào -> Key không tồn tại, bị khóa hoặc đã vượt quota
			keyCacheMu.RLock()
			it, ok := keyCache[apiKey]
			var quotaErr string
			if ok && it != nil {
				if it.IsActive != 1 {
					quotaErr = "API Key đã bị khóa hoặc hết hạn sử dụng"
				} else if it.QuotaLimit > 0 && it.UsedRequests >= it.QuotaLimit {
					quotaErr = fmt.Sprintf("API Key đã sử dụng hết hạn mức Quota (%d/%d requests)", it.UsedRequests, it.QuotaLimit)
				}
			}
			keyCacheMu.RUnlock()
			if quotaErr != "" {
				return false, quotaErr
			}
			return false, "API Key không hợp lệ hoặc đã sử dụng hết hạn mức Quota"
		}

		// Đồng bộ số liệu đã tăng thành công vào RAM Cache
		keyCacheMu.Lock()
		if it, exists := keyCache[apiKey]; exists && it != nil {
			it.UsedRequests++
			it.LastUsedAt = NowISO()
			if it.QuotaLimit > 0 && it.UsedRequests >= it.QuotaLimit {
				it.IsActive = 0
			}
		}
		keyCacheMu.Unlock()
		return true, ""
	}

	// 2. Chế độ RAM Cache fallback (db == nil): Đảm bảo nguyên tử 100% hoàn toàn dưới Write Lock
	keyCacheMu.Lock()
	defer keyCacheMu.Unlock()
	it, ok := keyCache[apiKey]
	if !ok || it == nil {
		return false, "API Key không tồn tại hoặc không hợp lệ"
	}
	if it.IsActive != 1 {
		return false, "API Key đã bị khóa hoặc hết hạn"
	}
	if it.QuotaLimit > 0 && it.UsedRequests >= it.QuotaLimit {
		it.IsActive = 0
		return false, fmt.Sprintf("API Key đã sử dụng hết hạn mức Quota (%d/%d requests)", it.UsedRequests, it.QuotaLimit)
	}
	if it.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, it.ExpiresAt); err == nil && time.Now().After(t) {
			it.IsActive = 0
			return false, "API Key đã hết thời hạn sử dụng"
		}
	}

	// Tăng nguyên tử ngay lập tức dưới Write Lock
	it.UsedRequests++
	it.LastUsedAt = NowISO()
	if it.QuotaLimit > 0 && it.UsedRequests >= it.QuotaLimit {
		it.IsActive = 0
	}
	return true, ""
}

// ReleaseClientKeyQuota giải phóng / hoàn lại quota đã giữ chỗ khi yêu cầu AI thất bại trước khi upstream xử lý
func ReleaseClientKeyQuota(db *sql.DB, apiKey string, amount int64) bool {
	if apiKey == "" || amount <= 0 {
		return false
	}

	// 1. Cập nhật giải phóng trên Database nếu có DB
	if db != nil {
		_, err := db.Exec(`
			UPDATE omni_client_keys 
			SET used_requests = CASE WHEN used_requests >= ? THEN used_requests - ? ELSE 0 END,
			    is_active = CASE WHEN quota_limit > 0 AND (used_requests - ?) < quota_limit THEN 1 ELSE is_active END,
			    last_used_at = ?
			WHERE api_key = ?
		`, amount, amount, amount, NowISO(), apiKey)
		if err != nil {
			log.Printf("[ENGINE] [QUOTA] ❌ Lỗi giải phóng reservation quota trên DB: %v", err)
		}
	}

	// 2. Cập nhật đồng bộ vào RAM Cache dưới Write Lock
	keyCacheMu.Lock()
	if it, exists := keyCache[apiKey]; exists && it != nil {
		if it.UsedRequests >= amount {
			it.UsedRequests -= amount
		} else {
			it.UsedRequests = 0
		}
		if it.QuotaLimit > 0 && it.UsedRequests < it.QuotaLimit {
			it.IsActive = 1
		}
		it.LastUsedAt = NowISO()
	}
	keyCacheMu.Unlock()

	log.Printf("[ENGINE] [QUOTA] ↩️ Đã hoàn lại %d quota giữ chỗ cho API Key (%s)", amount, MaskAPIKey(apiKey))
	return true
}

var (
	allProvidersCacheMu sync.RWMutex
	allProvidersCache   []ProviderConnection
	allProvidersCacheAt time.Time
)

// RefreshProviderCache đọc các tài khoản AI từ DB nạp vào RAM
func RefreshProviderCache(db *sql.DB) {
	allProvidersCacheMu.Lock()
	allProvidersCache = nil
	allProvidersCacheAt = time.Time{}
	allProvidersCacheMu.Unlock()

	InvalidateProvidersCache()

	if db == nil {
		return
	}
	rows, err := db.Query("SELECT id, provider, auth_type, name, email, priority, is_active, data, created_at, updated_at FROM omni_providers WHERE is_active = 1 ORDER BY priority ASC")
	if err != nil {
		return
	}
	defer rows.Close()

	var list []ProviderConnection
	for rows.Next() {
		var p ProviderConnection
		var dataRaw string
		if err := rows.Scan(&p.ID, &p.Provider, &p.AuthType, &p.Name, &p.Email, &p.Priority, &p.IsActive, &dataRaw, &p.CreatedAt, &p.UpdatedAt); err == nil {
			p.Data = json.RawMessage(dataRaw)
			list = append(list, p)
		}
	}

	providerCacheMu.Lock()
	providerCache = list
	providerCacheMu.Unlock()
}

// SyncProvidersFromWebServer kết nối trực tiếp tới Web chủ để nạp danh sách tài khoản AI vào RAM cache (Zero-SQLite)
func SyncProvidersFromWebServer(webServerURL string, apiKey string) ([]ProviderConnection, error) {
	webServerURL = strings.TrimRight(webServerURL, "/")
	if webServerURL == "" {
		webServerURL = "https://www.supportflastdev.io.vn"
	}
	targetURL := fmt.Sprintf("%s/api/router/providers?force=true", webServerURL)
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) SupportFlast-DirectWebClient/3.0")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("x-api-key", apiKey)
	}

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fallbackURL := fmt.Sprintf("%s/api/providers?force=true", webServerURL)
		req2, err2 := http.NewRequest("GET", fallbackURL, nil)
		if err2 != nil {
			return nil, err
		}
		req2.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) SupportFlast-DirectWebClient/3.0")
		if apiKey != "" {
			req2.Header.Set("Authorization", "Bearer "+apiKey)
			req2.Header.Set("x-api-key", apiKey)
		}
		resp, err = client.Do(req2)
		if err != nil {
			return nil, fmt.Errorf("không thể kết nối tới Web chủ: %w", err)
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Web chủ trả về mã HTTP %d", resp.StatusCode)
	}

	var payload struct {
		Success   bool                 `json:"success"`
		Total     int                  `json:"total"`
		Providers []ProviderConnection `json:"providers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("lỗi giải mã JSON từ Web chủ: %w", err)
	}

	if len(payload.Providers) > 0 {
		SetCachedActiveProviders(payload.Providers)
		log.Printf("[ENGINE] [WEB-CLIENT] ✔ Đã đồng bộ trực tiếp %d tài khoản AI từ Web chủ (%s) vào RAM (Zero-SQLite)", len(payload.Providers), webServerURL)
	}
	return payload.Providers, nil
}

// GetEmbeddedProvidersAsConnections chuyển đổi danh sách tài khoản nhúng tĩnh (EmbeddedAIAccounts) sang mảng ProviderConnection
func GetEmbeddedProvidersAsConnections() []ProviderConnection {
	res := make([]ProviderConnection, 0, len(EmbeddedAIAccounts))
	for _, acc := range EmbeddedAIAccounts {
		p := ProviderConnection{
			ID:        acc.ID,
			Provider:  acc.Provider,
			AuthType:  acc.AuthType,
			Name:      acc.Name,
			Email:     acc.Email,
			Priority:  acc.Priority,
			IsActive:  acc.IsActive,
			Data:      json.RawMessage(acc.RawDataJSON),
			CreatedAt: acc.CreatedAt,
			UpdatedAt: acc.UpdatedAt,
		}
		res = append(res, p)
	}
	return res
}

// GetCachedActiveProviders lấy danh sách tài khoản AI từ RAM
func GetCachedActiveProviders() []ProviderConnection {
	providerCacheMu.RLock()
	count := len(providerCache)
	providerCacheMu.RUnlock()

	if count == 0 {
		if flag.Lookup("test.v") != nil {
			return nil
		}
		webURL := GetWebServerURL()
		activeKey := GetActiveClientKey(nil)
		if list, err := SyncProvidersFromWebServer(webURL, activeKey); err == nil && len(list) > 0 {
			return list
		}

		// Fallback an toàn cho Client (Zero-SQLite):
		// Nếu Web chủ chưa có dữ liệu hoặc tạm gián đoạn, nạp ngay từ EmbeddedAIAccounts vào RAM Cache
		embedded := GetEmbeddedProvidersAsConnections()
		if len(embedded) > 0 {
			SetCachedActiveProviders(embedded)
			return embedded
		}
	}

	providerCacheMu.RLock()
	defer providerCacheMu.RUnlock()
	res := make([]ProviderConnection, len(providerCache))
	copy(res, providerCache)
	return res
}

// SetCachedActiveProviders thiết lập danh sách tài khoản AI trong RAM (phục vụ test và mock nội bộ)
func SetCachedActiveProviders(list []ProviderConnection) {
	providerCacheMu.Lock()
	defer providerCacheMu.Unlock()
	providerCache = make([]ProviderConnection, len(list))
	copy(providerCache, list)
}

// UpdateProviderData cập nhật data (token, quota...) của một provider
func UpdateProviderData(db *sql.DB, id string, dataMap map[string]interface{}) error {
	bytes, err := json.Marshal(dataMap)
	if err != nil {
		return err
	}
	now := NowISO()
	if db == nil {
		providerCacheMu.Lock()
		for i := range providerCache {
			if providerCache[i].ID == id {
				providerCache[i].Data = json.RawMessage(bytes)
				providerCache[i].UpdatedAt = now
				break
			}
		}
		providerCacheMu.Unlock()
		return nil
	}
	_, err = db.Exec("UPDATE omni_providers SET data = ?, updated_at = ? WHERE id = ?", string(bytes), now, id)
	if err == nil {
		RefreshProviderCache(db)
	}
	return err
}

// GetAllProviders truy vấn toàn bộ danh sách tài khoản AI (kể cả active lẫn inactive) phục vụ Web Dashboard
// sử dụng In-Memory Cache với TTL 30 giây để giảm latency từ 1600ms (PostgreSQL Cloud) xuống <1ms.
func GetAllProviders(db *sql.DB) ([]ProviderConnection, error) {
	return GetAllProvidersForce(db, false)
}

// GetAllProvidersForce truy vấn toàn bộ danh sách tài khoản AI với cờ force làm mới cache
func GetAllProvidersForce(db *sql.DB, force bool) ([]ProviderConnection, error) {
	if db == nil {
		return GetCachedActiveProviders(), nil
	}

	if !force {
		allProvidersCacheMu.RLock()
		if len(allProvidersCache) > 0 && time.Since(allProvidersCacheAt) < 30*time.Second {
			res := make([]ProviderConnection, len(allProvidersCache))
			copy(res, allProvidersCache)
			allProvidersCacheMu.RUnlock()
			return res, nil
		}
		allProvidersCacheMu.RUnlock()
	}

	rows, err := db.Query("SELECT id, provider, auth_type, name, email, priority, is_active, data, created_at, updated_at FROM omni_providers ORDER BY priority ASC, name ASC")
	if err != nil {
		allProvidersCacheMu.RLock()
		if len(allProvidersCache) > 0 {
			res := make([]ProviderConnection, len(allProvidersCache))
			copy(res, allProvidersCache)
			allProvidersCacheMu.RUnlock()
			return res, nil
		}
		allProvidersCacheMu.RUnlock()
		return nil, err
	}
	defer rows.Close()

	var list []ProviderConnection
	for rows.Next() {
		var p ProviderConnection
		var dataRaw string
		if err := rows.Scan(&p.ID, &p.Provider, &p.AuthType, &p.Name, &p.Email, &p.Priority, &p.IsActive, &dataRaw, &p.CreatedAt, &p.UpdatedAt); err == nil {
			p.Data = json.RawMessage(dataRaw)
			list = append(list, p)
		}
	}

	allProvidersCacheMu.Lock()
	allProvidersCache = list
	allProvidersCacheAt = time.Now()
	allProvidersCacheMu.Unlock()

	res := make([]ProviderConnection, len(list))
	copy(res, list)
	return res, nil
}

// UpdateProviderStatus cập nhật trạng thái hoạt động (bật/tắt) và độ ưu tiên của Provider
func UpdateProviderStatus(db *sql.DB, id string, isActive int, priority int) error {
	now := NowISO()
	if db == nil {
		providerCacheMu.Lock()
		for i := range providerCache {
			if providerCache[i].ID == id {
				providerCache[i].IsActive = isActive
				providerCache[i].Priority = priority
				providerCache[i].UpdatedAt = now
				break
			}
		}
		providerCacheMu.Unlock()
		return nil
	}
	_, err := db.Exec("UPDATE omni_providers SET is_active = ?, priority = ?, updated_at = ? WHERE id = ?", isActive, priority, now, id)
	if err == nil {
		RefreshProviderCache(db)
	}
	return err
}

// SaveProvider thêm mới hoặc cập nhật một Provider vào CSDL và đồng bộ RAM cache
func SaveProvider(db *sql.DB, p *ProviderConnection) error {
	if db == nil {
		providerCacheMu.Lock()
		defer providerCacheMu.Unlock()
		found := false
		for i, existing := range providerCache {
			if existing.ID == p.ID {
				providerCache[i] = *p
				found = true
				break
			}
		}
		if !found {
			providerCache = append(providerCache, *p)
		}
		return nil
	}

	now := NowISO()
	if p.CreatedAt == "" {
		p.CreatedAt = now
	}
	p.UpdatedAt = now

	// 1. Thử cú pháp chuẩn ON CONFLICT (Aiven PostgreSQL & SQLite)
	_, err := db.Exec(`
		INSERT INTO omni_providers (id, provider, auth_type, name, email, priority, is_active, data, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			provider = excluded.provider,
			auth_type = excluded.auth_type,
			name = excluded.name,
			email = excluded.email,
			priority = excluded.priority,
			is_active = excluded.is_active,
			data = excluded.data,
			updated_at = excluded.updated_at`,
		p.ID, p.Provider, p.AuthType, p.Name, p.Email, p.Priority, p.IsActive, string(p.Data), p.CreatedAt, p.UpdatedAt)

	// 2. Fallback sang cú pháp SQLite (ON CONFLICT DO UPDATE) nếu gặp lỗi cú pháp
	if err != nil && (strings.Contains(err.Error(), "syntax error") || strings.Contains(err.Error(), "near \"ON\": syntax error") || strings.Contains(err.Error(), "no such table")) {
		_, err = db.Exec(`
			INSERT INTO omni_providers (id, provider, auth_type, name, email, priority, is_active, data, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				provider = excluded.provider,
				auth_type = excluded.auth_type,
				name = excluded.name,
				email = excluded.email,
				priority = excluded.priority,
				is_active = excluded.is_active,
				data = excluded.data,
				updated_at = excluded.updated_at`,
			p.ID, p.Provider, p.AuthType, p.Name, p.Email, p.Priority, p.IsActive, string(p.Data), p.CreatedAt, p.UpdatedAt)
	}

	if err == nil {
		RefreshProviderCache(db)
	}
	return err
}

// DeleteProvider xóa một Provider khỏi hệ thống
func DeleteProvider(db *sql.DB, id string) error {
	_, err := db.Exec("DELETE FROM omni_providers WHERE id = ?", id)
	if err == nil {
		RefreshProviderCache(db)
	}
	return err
}

// GetOmniKV đọc giá trị cấu hình theo scope và key_name từ bảng omni_kv
func GetOmniKV(db *sql.DB, scope, keyName string) (string, error) {
	if db == nil {
		for _, kv := range EmbeddedKVList {
			if kv.Scope == scope && kv.KeyName == keyName {
				return kv.ValData, nil
			}
		}
		return "", sql.ErrNoRows
	}
	var val string
	err := db.QueryRow("SELECT val_data FROM omni_kv WHERE scope = ? AND key_name = ?", scope, keyName).Scan(&val)
	if err != nil {
		return "", err
	}
	return val, nil
}

// SetOmniKV lưu giá trị cấu hình vào bảng omni_kv (UPSERT)
func SetOmniKV(db *sql.DB, scope, keyName, valData string) error {
	if db == nil {
		return nil
	}
	// Dùng REPLACE INTO tương thích cả MySQL/Aiven PostgreSQL và SQLite
	_, err := db.Exec("REPLACE INTO omni_kv (scope, key_name, val_data) VALUES (?, ?, ?)", scope, keyName, valData)
	return err
}

// GetSettingsRaw đọc JSON settings từ bảng omni_settings
func GetSettingsRaw(db *sql.DB) (string, error) {
	if db == nil {
		return EmbeddedSettingsJSON, nil
	}
	var data string
	err := db.QueryRow("SELECT data FROM omni_settings WHERE id = 1").Scan(&data)
	if err != nil {
		return "", err
	}
	return data, nil
}

// SaveSettingsRaw lưu JSON settings vào bảng omni_settings
func SaveSettingsRaw(db *sql.DB, data string) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec("REPLACE INTO omni_settings (id, data) VALUES (1, ?)", data)
	return err
}

// GetRecentUsageHistory lấy các bản ghi lịch sử sử dụng gần nhất từ omni_usage_history
func GetRecentUsageHistory(db *sql.DB, limit int) []map[string]interface{} {
	if db == nil {
		return []map[string]interface{}{}
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.Query("SELECT id, timestamp, provider, model, endpoint, prompt_tokens, completion_tokens, total_tokens, cost, status, latency_ms FROM omni_usage_history ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return []map[string]interface{}{}
	}
	defer rows.Close()

	var list []map[string]interface{}
	for rows.Next() {
		var id int64
		var ts, prov, model, ep, status string
		var pt, ct, tt, lat int64
		var cost float64
		if err := rows.Scan(&id, &ts, &prov, &model, &ep, &pt, &ct, &tt, &cost, &status, &lat); err == nil {
			list = append(list, map[string]interface{}{
				"id":                id,
				"timestamp":         ts,
				"provider":          prov,
				"model":             model,
				"endpoint":          ep,
				"prompt_tokens":     pt,
				"completion_tokens": ct,
				"total_tokens":      tt,
				"cost":              cost,
				"status":            status,
				"latency_ms":        lat,
			})
		}
	}
	return list
}

// RecordUsageHistoryAsync ghi nhận bản ghi nhật ký yêu cầu vào bảng omni_usage_history
func RecordUsageHistoryAsync(db *sql.DB, provider, model, connectionId, apiKey, endpoint string, promptTokens, completionTokens, totalTokens int64, cost float64, status string, latencyMs int64) {
	if db == nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[ENGINE] [USAGE] ⚠️ Recovered từ panic trong RecordUsageHistoryAsync: %v", r)
			}
		}()
		ts := NowISO()
		var err error
		for attempt := 1; attempt <= 3; attempt++ {
			_, err = db.Exec(`
			INSERT INTO omni_usage_history 
			(timestamp, provider, model, connection_id, api_key, endpoint, prompt_tokens, completion_tokens, total_tokens, cost, status, latency_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, ts, provider, model, connectionId, apiKey, endpoint, promptTokens, completionTokens, totalTokens, cost, status, latencyMs)
			if err == nil {
				break
			}
			if attempt < 3 {
				time.Sleep(10 * time.Millisecond)
			}
		}
		if err != nil {
			log.Printf("[ENGINE] [USAGE] ❌ Lỗi ghi nhận usage cho key %s: %v", apiKey, err)
		}
	}()
}
