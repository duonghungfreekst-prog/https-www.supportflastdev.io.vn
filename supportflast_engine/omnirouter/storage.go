package omnirouter

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
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

// InitStorage khởi tạo các bảng CSDL cho OmniRouter trên TiDB hoặc SQLite
func InitStorage(db *sql.DB, isTiDB bool) error {
	if db == nil {
		log.Printf("[ENGINE] [STORAGE] [WARN] db là nil, bỏ qua InitStorage (chế độ in-memory cache)")
		return nil
	}
	var queries []string

	if isTiDB {
		queries = []string{
			`CREATE TABLE IF NOT EXISTS omni_providers (
				id VARCHAR(64) NOT NULL PRIMARY KEY,
				provider VARCHAR(64) NOT NULL,
				auth_type VARCHAR(32) NOT NULL,
				name VARCHAR(191) NOT NULL,
				email VARCHAR(191) DEFAULT '',
				priority INT DEFAULT 1,
				is_active INT DEFAULT 1,
				data LONGTEXT NOT NULL,
				created_at VARCHAR(64) NOT NULL,
				updated_at VARCHAR(64) NOT NULL,
				INDEX idx_omni_prov_active (is_active, priority)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

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
				last_used_at VARCHAR(64) DEFAULT '',
				INDEX idx_omni_key_active (is_active)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

			`CREATE TABLE IF NOT EXISTS omni_settings (
				id INT NOT NULL PRIMARY KEY,
				data LONGTEXT NOT NULL
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

			`CREATE TABLE IF NOT EXISTS omni_proxy_pools (
				id VARCHAR(64) NOT NULL PRIMARY KEY,
				is_active INT DEFAULT 1,
				test_status VARCHAR(64) DEFAULT '',
				data LONGTEXT NOT NULL,
				created_at VARCHAR(64) NOT NULL,
				updated_at VARCHAR(64) NOT NULL
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

			`CREATE TABLE IF NOT EXISTS omni_kv (
				scope VARCHAR(64) NOT NULL,
				key_name VARCHAR(128) NOT NULL,
				val_data LONGTEXT NOT NULL,
				PRIMARY KEY (scope, key_name)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

			`CREATE TABLE IF NOT EXISTS omni_usage_history (
				id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
				timestamp VARCHAR(64) NOT NULL,
				provider VARCHAR(64) NOT NULL,
				model VARCHAR(128) NOT NULL,
				connection_id VARCHAR(64) NOT NULL,
				api_key VARCHAR(128) DEFAULT '',
				endpoint VARCHAR(128) DEFAULT '',
				prompt_tokens BIGINT DEFAULT 0,
				completion_tokens BIGINT DEFAULT 0,
				total_tokens BIGINT DEFAULT 0,
				cost DOUBLE DEFAULT 0,
				status VARCHAR(64) DEFAULT 'success',
				tokens TEXT,
				meta LONGTEXT,
				latency_ms BIGINT DEFAULT 0,
				ttft_ms BIGINT DEFAULT 0,
				tps DOUBLE DEFAULT 0,
				cached_tokens BIGINT DEFAULT 0,
				status_code INT DEFAULT 200,
				error_code VARCHAR(64) DEFAULT '',
				INDEX idx_omni_uh_ts (timestamp),
				INDEX idx_omni_uh_conn (connection_id)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
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
	if isTiDB {
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

	// Dọn dẹp dứt điểm các key mặc định cố định thừa thãi (chặn đứng lỗ hổng P0)
	if _, errDel := db.Exec("DELETE FROM omni_client_keys WHERE id IN ('key_master_legacy', 'key_master_domain_cloud') OR api_key LIKE '%master-live%'"); errDel != nil {
		log.Printf("[ENGINE] [STORAGE] ⚠️ Lỗi dọn dẹp key mặc định cũ: %v", errDel)
	}

	log.Println("[ENGINE] [OMNIROUTER] Đã khởi tạo hoàn tất cấu trúc lưu trữ SupportFlast Router trong Database")
	// Nạp sẵn cache
	RefreshKeyCache(db)
	RefreshProviderCache(db)
	return nil
}

// DefaultDomain là tên miền web chính thức của hệ thống
const DefaultDomain = "supportflastdev.io.vn"

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
			keyCache[k.Key] = &k
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

// ValidateClientKey kiểm tra xem client key có hợp lệ và còn hiệu lực không
func ValidateClientKey(rawKey string) (*ClientKey, bool) {
	item, status, _ := CheckClientKeyStatus(rawKey)
	if status == "OK" && item != nil {
		return item, true
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

// ListAllClientKeys lấy danh sách API Key thông thường (ẨN HOÀN TOÀN Master Web Domain Key không bao giờ hiện)
func ListAllClientKeys(db *sql.DB) ([]ClientKey, error) {
	rows, err := db.Query(`
		SELECT id, name, api_key, key_hash, used_requests, total_tokens, quota_limit, expires_at, duration_hours, is_active, created_at, last_used_at 
		FROM omni_client_keys 
		WHERE id != 'key_master_domain_cloud' 
		  AND id != 'key_master_legacy' 
		  AND api_key NOT LIKE '%master-live%' 
		  AND name NOT LIKE '%Master%' 
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
	_, err := db.Exec("UPDATE omni_client_keys SET is_active = 0 WHERE id = ?", id)
	if err == nil {
		RefreshKeyCache(db)
	}
	return err
}

// DeleteClientKey xóa vĩnh viễn một API Key
func DeleteClientKey(db *sql.DB, id string) error {
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

// RefreshProviderCache đọc các tài khoản AI từ DB nạp vào RAM
func RefreshProviderCache(db *sql.DB) {
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

// GetCachedActiveProviders lấy danh sách tài khoản AI từ RAM
func GetCachedActiveProviders() []ProviderConnection {
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
	_, err = db.Exec("UPDATE omni_providers SET data = ?, updated_at = ? WHERE id = ?", string(bytes), now, id)
	if err == nil {
		RefreshProviderCache(db)
	}
	return err
}

// GetAllProviders truy vấn toàn bộ danh sách tài khoản AI (kể cả active lẫn inactive) phục vụ Web Dashboard
func GetAllProviders(db *sql.DB) ([]ProviderConnection, error) {
	rows, err := db.Query("SELECT id, provider, auth_type, name, email, priority, is_active, data, created_at, updated_at FROM omni_providers ORDER BY priority ASC, name ASC")
	if err != nil {
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
	return list, nil
}

// UpdateProviderStatus cập nhật trạng thái hoạt động (bật/tắt) và độ ưu tiên của Provider
func UpdateProviderStatus(db *sql.DB, id string, isActive int, priority int) error {
	now := NowISO()
	_, err := db.Exec("UPDATE omni_providers SET is_active = ?, priority = ?, updated_at = ? WHERE id = ?", isActive, priority, now, id)
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
	var val string
	err := db.QueryRow("SELECT val_data FROM omni_kv WHERE scope = ? AND key_name = ?", scope, keyName).Scan(&val)
	if err != nil {
		return "", err
	}
	return val, nil
}

// SetOmniKV lưu giá trị cấu hình vào bảng omni_kv (UPSERT)
func SetOmniKV(db *sql.DB, scope, keyName, valData string) error {
	// Dùng REPLACE INTO tương thích cả MySQL/TiDB và SQLite
	_, err := db.Exec("REPLACE INTO omni_kv (scope, key_name, val_data) VALUES (?, ?, ?)", scope, keyName, valData)
	return err
}

// GetSettingsRaw đọc JSON settings từ bảng omni_settings
func GetSettingsRaw(db *sql.DB) (string, error) {
	var data string
	err := db.QueryRow("SELECT data FROM omni_settings WHERE id = 1").Scan(&data)
	if err != nil {
		return "", err
	}
	return data, nil
}

// SaveSettingsRaw lưu JSON settings vào bảng omni_settings
func SaveSettingsRaw(db *sql.DB, data string) error {
	_, err := db.Exec("REPLACE INTO omni_settings (id, data) VALUES (1, ?)", data)
	return err
}

// GetRecentUsageHistory lấy các bản ghi lịch sử sử dụng gần nhất từ omni_usage_history
func GetRecentUsageHistory(db *sql.DB, limit int) []map[string]interface{} {
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
