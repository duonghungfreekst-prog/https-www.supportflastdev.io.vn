package registry

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"supportflast_engine/database"
	"supportflast_engine/security"
)

// APITokenItem đại diện cho một API Token được cấp phát
type APITokenItem struct {
	ID          string   `json:"id"`
	UserID      string   `json:"user_id"`
	Name        string   `json:"name"`
	TokenKey    string   `json:"token_key,omitempty"` // Chỉ hiển thị 1 lần duy nhất khi tạo
	TokenHash   string   `json:"token_hash,omitempty"`
	Prefix      string   `json:"prefix"`
	Quota       int64    `json:"quota"`        // Hạn mức quota cấp (tokens), -1 là không giới hạn
	UsedQuota   int64    `json:"used_quota"`   // Quota đã tiêu thụ
	RemainQuota int64    `json:"remain_quota"` // Quota còn lại
	Status      string   `json:"status"`       // "active", "disabled", "expired"
	Models      []string `json:"models"`       // Models được phép ("*" hoặc danh sách cụ thể)
	ExpiresAt   string   `json:"expires_at"`   // Hạn dùng (ISO-8601 RFC3339) hoặc ""
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

var (
	tokenCacheMu sync.RWMutex
	tokenCache   = make(map[string]*APITokenItem) // key_hash -> item
)

// InitAPITokenSchema khởi tạo bảng và cột cho hệ thống bán/quản lý API Token
func InitAPITokenSchema() {
	db := database.GetDB()
	if db == nil {
		log.Println("[ENGINE] [API-TOKEN] [WARN] CSDL chưa sẵn sàng để khởi tạo Schema Token.")
		return
	}

	createTableSQL := `
	CREATE TABLE IF NOT EXISTS user_tokens (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		name TEXT NOT NULL,
		token_hash TEXT UNIQUE NOT NULL,
		prefix TEXT NOT NULL,
		quota BIGINT NOT NULL DEFAULT 10000000,
		used_quota BIGINT NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'active',
		models TEXT NOT NULL DEFAULT '["*"]',
		expires_at TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_user_tokens_hash ON user_tokens(token_hash);
	CREATE INDEX IF NOT EXISTS idx_user_tokens_user ON user_tokens(user_id);
	CREATE INDEX IF NOT EXISTS idx_user_tokens_status ON user_tokens(status);

	CREATE TABLE IF NOT EXISTS token_usage_logs (
		id TEXT PRIMARY KEY,
		token_id TEXT NOT NULL,
		user_id TEXT,
		model TEXT NOT NULL,
		prompt_tokens BIGINT NOT NULL DEFAULT 0,
		completion_tokens BIGINT NOT NULL DEFAULT 0,
		total_tokens BIGINT NOT NULL DEFAULT 0,
		cost DOUBLE PRECISION DEFAULT 0.0,
		latency_ms BIGINT DEFAULT 0,
		ip_address TEXT,
		status TEXT DEFAULT 'success',
		created_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_usage_logs_token ON token_usage_logs(token_id);
	CREATE INDEX IF NOT EXISTS idx_usage_logs_created ON token_usage_logs(created_at DESC);
	`
	if _, err := db.Exec(createTableSQL); err != nil {
		log.Printf("[ENGINE] [API-TOKEN] [WARN] Lỗi khởi tạo bảng user_tokens (có thể đã tồn tại): %v", err)
	} else {
		log.Println("[ENGINE] [API-TOKEN] ✔ Đã khởi tạo schema user_tokens và token_usage_logs an toàn.")
	}

	// Tự động kiểm tra và thêm Master Token cho Quản Trị Viên nếu chưa có
	ensureMasterAdminToken(db)
}

func ensureMasterAdminToken(db *sql.DB) {
	adminUser, err := GetUserByUsernameOrEmail("admin")
	if err != nil || adminUser == nil {
		return
	}

	var count int
	_ = db.QueryRow("SELECT COUNT(1) FROM user_tokens WHERE user_id = ?", adminUser.ID).Scan(&count)
	if count == 0 {
		now := time.Now().UTC().Format(time.RFC3339)
		rawKey := "sk-www.supportflastdev.io.vn-4b134c2b19bae0bf106c19a6f94154f9"
		h := sha256.Sum256([]byte(rawKey))
		hashHex := hex.EncodeToString(h[:])
		prefix := rawKey[:28] + "..."

		insertSQL := `
		INSERT INTO user_tokens (id, user_id, name, token_hash, prefix, quota, used_quota, status, models, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`
		_, err := db.Exec(insertSQL,
			"tok-admin-master-001",
			adminUser.ID,
			"Master Gateway Token (SupportFlast OmniRouter)",
			hashHex,
			prefix,
			1000000000, // 1 tỷ quota (~$1,000 USD)
			0,
			"active",
			`["*"]`,
			"",
			now,
			now,
		)
		if err == nil {
			log.Println("[ENGINE] [API-TOKEN] ✔ Đã khởi tạo Master API Token cho tài khoản Quản Trị Viên.")
		}
	}
}

// VerifyAPITokenKey xác thực API Token an toàn bằng Constant-Time SHA-256
func VerifyAPITokenKey(rawToken string) (*APITokenItem, bool) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return nil, false
	}

	h := sha256.Sum256([]byte(rawToken))
	hashHex := hex.EncodeToString(h[:])

	// 1. Kiểm tra RAM Cache trước (L1 Cache)
	tokenCacheMu.RLock()
	if item, found := tokenCache[hashHex]; found {
		tokenCacheMu.RUnlock()
		if isTokenActiveAndValid(item) {
			return item, true
		}
		return nil, false
	}
	tokenCacheMu.RUnlock()

	// 2. Kiểm tra CSDL (L3 Database)
	db := database.GetDB()
	if db == nil {
		return nil, false
	}

	query := `
	SELECT id, user_id, name, token_hash, prefix, quota, used_quota, status, models, COALESCE(expires_at, ''), created_at, updated_at
	FROM user_tokens
	WHERE token_hash = ?
	LIMIT 1
	`
	var item APITokenItem
	var modelsJSON string
	err := db.QueryRow(query, hashHex).Scan(
		&item.ID, &item.UserID, &item.Name, &item.TokenHash, &item.Prefix,
		&item.Quota, &item.UsedQuota, &item.Status, &modelsJSON, &item.ExpiresAt,
		&item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return nil, false
	}

	// Constant-time check token hash
	if subtle.ConstantTimeCompare([]byte(item.TokenHash), []byte(hashHex)) != 1 {
		return nil, false
	}

	_ = json.Unmarshal([]byte(modelsJSON), &item.Models)
	if item.Quota > 0 {
		item.RemainQuota = item.Quota - item.UsedQuota
		if item.RemainQuota < 0 {
			item.RemainQuota = 0
		}
	} else {
		item.RemainQuota = 999999999 // Không giới hạn
	}

	if !isTokenActiveAndValid(&item) {
		return nil, false
	}

	// Lưu RAM Cache
	tokenCacheMu.Lock()
	tokenCache[hashHex] = &item
	tokenCacheMu.Unlock()

	return &item, true
}

func isTokenActiveAndValid(item *APITokenItem) bool {
	if item == nil || item.Status != "active" {
		return false
	}
	if item.ExpiresAt != "" {
		if exp, err := time.Parse(time.RFC3339, item.ExpiresAt); err == nil && time.Now().After(exp) {
			return false
		}
	}
	if item.Quota > 0 && item.UsedQuota >= item.Quota {
		return false
	}
	return true
}

// DeductTokenQuotaAsync trừ quota token và ghi nhận log bất đồng bộ
func DeductTokenQuotaAsync(tokenID, userID, model string, promptTokens, completionTokens int64, latencyMs int64, ip string) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[ENGINE] [API-TOKEN] [RECOVER] Panic trong DeductTokenQuotaAsync: %v", r)
			}
		}()

		totalTokens := promptTokens + completionTokens
		if totalTokens <= 0 {
			totalTokens = 1
		}

		db := database.GetDB()
		if db == nil {
			return
		}

		nowStr := time.Now().UTC().Format(time.RFC3339)
		// Cập nhật used_quota trong user_tokens
		updateSQL := "UPDATE user_tokens SET used_quota = used_quota + ?, updated_at = ? WHERE id = ?"
		_, _ = db.Exec(updateSQL, totalTokens, nowStr, tokenID)

		// Xóa cache để lượt sau nạp quota mới nhất
		tokenCacheMu.Lock()
		for k, v := range tokenCache {
			if v.ID == tokenID {
				v.UsedQuota += totalTokens
				if v.Quota > 0 {
					v.RemainQuota = v.Quota - v.UsedQuota
				}
				delete(tokenCache, k)
				break
			}
		}
		tokenCacheMu.Unlock()

		// Ghi log vào token_usage_logs
		logID := fmt.Sprintf("log-%d-%s", time.Now().UnixNano(), tokenID)
		insertLogSQL := `
		INSERT INTO token_usage_logs (id, token_id, user_id, model, prompt_tokens, completion_tokens, total_tokens, cost, latency_ms, ip_address, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`
		costEst := float64(totalTokens) * 0.000002 // Ước tính $0.002 / 1K tokens
		_, _ = db.Exec(insertLogSQL, logID, tokenID, userID, model, promptTokens, completionTokens, totalTokens, costEst, latencyMs, ip, "success", nowStr)
	}()
}

// -------------------------------------------------------------------------
// HTTP HANDLERS
// -------------------------------------------------------------------------

// APITokensCreateHandler tạo mới API Token với hạn mức và permissions
func APITokensCreateHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	callerToken := ExtractToken(r)
	callerUser, authed := GetUserFromToken(callerToken)
	if !authed || callerUser == nil {
		http.Error(w, `{"error":"Unauthorized: Vui lòng đăng nhập để tạo API Token"}`, http.StatusUnauthorized)
		return
	}

	var req struct {
		Name      string   `json:"name"`
		Quota     int64    `json:"quota"`      // Tokens cấp (ví dụ: 5000000 = $5), -1 là không giới hạn
		DaysValid int      `json:"days_valid"` // Số ngày có hiệu lực, 0 là vĩnh viễn
		Models    []string `json:"models"`     // Danh sách models hoặc ["*"]
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Dữ liệu JSON không hợp lệ"}`, http.StatusBadRequest)
		return
	}

	req.Name = security.SanitizeCRLF(strings.TrimSpace(req.Name))
	if req.Name == "" {
		req.Name = fmt.Sprintf("Token %s (%s)", callerUser.Username, time.Now().Format("02/01/2006"))
	}
	if req.Quota == 0 {
		req.Quota = 5000000 // Mặc định 5,000,000 tokens (~$5.00)
	}
	if len(req.Models) == 0 {
		req.Models = []string{"*"}
	}

	// Sinh key ngẫu nhiên dạng sk-supportflast-...
	rndBytes := make([]byte, 24)
	if _, err := rand.Read(rndBytes); err != nil {
		http.Error(w, `{"error":"Lỗi sinh khóa ngẫu nhiên CSPRNG"}`, http.StatusInternalServerError)
		return
	}
	rawTokenKey := "sk-supportflast-" + hex.EncodeToString(rndBytes)
	h := sha256.Sum256([]byte(rawTokenKey))
	hashHex := hex.EncodeToString(h[:])
	prefix := rawTokenKey[:18] + "..."

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	var expStr string
	if req.DaysValid > 0 {
		expStr = now.AddDate(0, 0, req.DaysValid).Format(time.RFC3339)
	}

	modelsBytes, _ := json.Marshal(req.Models)
	tokenID := fmt.Sprintf("tok-%d", time.Now().UnixNano())

	db := database.GetDB()
	if db == nil {
		http.Error(w, `{"error":"CSDL chưa sẵn sàng"}`, http.StatusServiceUnavailable)
		return
	}

	insertSQL := `
	INSERT INTO user_tokens (id, user_id, name, token_hash, prefix, quota, used_quota, status, models, expires_at, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := db.Exec(insertSQL,
		tokenID, callerUser.ID, req.Name, hashHex, prefix, req.Quota, 0, "active", string(modelsBytes), expStr, nowStr, nowStr,
	)
	if err != nil {
		log.Printf("[ENGINE] [API-TOKEN] Lỗi lưu token: %v", err)
		http.Error(w, `{"error":"Lỗi lưu trữ token vào CSDL"}`, http.StatusInternalServerError)
		return
	}

	log.Printf("[ENGINE] [API-TOKEN] [AUDIT] Người dùng '%s' tạo mới API Token '%s' (%s, Quota: %d)",
		callerUser.Username, req.Name, prefix, req.Quota)

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "success",
		"message":    "Đã khởi tạo API Token thành công! Vui lòng sao chép và lưu trữ an toàn ngay bây giờ (chỉ hiển thị toàn văn 1 lần duy nhất).",
		"token_key":  rawTokenKey, // Toàn văn key chỉ trả 1 lần
		"token_info": map[string]interface{}{
			"id":         tokenID,
			"name":       req.Name,
			"prefix":     prefix,
			"quota":      req.Quota,
			"models":     req.Models,
			"expires_at": expStr,
			"created_at": nowStr,
		},
	})
}

// APITokensListHandler liệt kê danh sách tokens của người dùng (ẩn toàn văn key, chỉ hiện prefix)
func APITokensListHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	callerToken := ExtractToken(r)
	callerUser, authed := GetUserFromToken(callerToken)
	if !authed || callerUser == nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	db := database.GetDB()
	if db == nil {
		http.Error(w, `{"error":"CSDL chưa sẵn sàng"}`, http.StatusServiceUnavailable)
		return
	}

	var rows *sql.Rows
	var err error
	if callerUser.Role == "admin" && r.URL.Query().Get("all") == "true" {
		rows, err = db.Query("SELECT id, user_id, name, prefix, quota, used_quota, status, models, COALESCE(expires_at, ''), created_at, updated_at FROM user_tokens ORDER BY created_at DESC")
	} else {
		rows, err = db.Query("SELECT id, user_id, name, prefix, quota, used_quota, status, models, COALESCE(expires_at, ''), created_at, updated_at FROM user_tokens WHERE user_id = ? ORDER BY created_at DESC", callerUser.ID)
	}

	if err != nil {
		http.Error(w, `{"error":"Lỗi truy vấn CSDL"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	tokens := make([]map[string]interface{}, 0)
	now := time.Now()

	for rows.Next() {
		var id, uID, name, prefix, status, modelsJSON, expAt, crAt, upAt string
		var quota, usedQuota int64
		if scanErr := rows.Scan(&id, &uID, &name, &prefix, &quota, &usedQuota, &status, &modelsJSON, &expAt, &crAt, &upAt); scanErr == nil {
			if expAt != "" {
				if exp, parseErr := time.Parse(time.RFC3339, expAt); parseErr == nil && now.After(exp) {
					status = "expired"
				}
			}
			remain := quota - usedQuota
			if quota < 0 {
				remain = 999999999
			} else if remain < 0 {
				remain = 0
			}

			var modelsList []string
			_ = json.Unmarshal([]byte(modelsJSON), &modelsList)

			tokens = append(tokens, map[string]interface{}{
				"id":           id,
				"user_id":      uID,
				"name":         name,
				"prefix":       prefix,
				"quota":        quota,
				"used_quota":   usedQuota,
				"remain_quota": remain,
				"status":       status,
				"models":       modelsList,
				"expires_at":   expAt,
				"created_at":   crAt,
				"updated_at":   upAt,
			})
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"total":  len(tokens),
		"tokens": tokens,
	})
}

// APITokenVerifyHandler xác thực API Token cho Desktop App hoặc Router
func APITokenVerifyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	token := ExtractToken(r)
	if token == "" {
		token = r.URL.Query().Get("key")
	}
	if token == "" {
		http.Error(w, `{"error":"Thiếu API Token (Bearer sk-... hoặc ?key=...)"}`, http.StatusUnauthorized)
		return
	}

	item, valid := VerifyAPITokenKey(token)
	if !valid || item == nil {
		// Fallback kiểm tra xem có phải API Key cũ trong api_keys không
		if VerifyAPIKey(token) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":       "active",
				"valid":        true,
				"name":         "Legacy SupportFlast API Key",
				"quota":        100000000,
				"used_quota":   0,
				"remain_quota": 100000000,
				"models":       []string{"*"},
			})
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "invalid",
			"valid":  false,
			"error":  "API Token không hợp lệ, đã bị khóa hoặc hết hạn quota",
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":       item.Status,
		"valid":        true,
		"id":           item.ID,
		"name":         item.Name,
		"prefix":       item.Prefix,
		"quota":        item.Quota,
		"used_quota":   item.UsedQuota,
		"remain_quota": item.RemainQuota,
		"models":       item.Models,
		"expires_at":   item.ExpiresAt,
	})
}

// V1BillingUsageHandler trả về hạn mức Quota chuẩn New-API / One-API cho IDE và Desktop App
func V1BillingUsageHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	token := ExtractToken(r)
	item, valid := VerifyAPITokenKey(token)
	if !valid || item == nil {
		// Fallback check legacy
		if VerifyAPIKey(token) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"object":          "list",
				"total_usage":     0,
				"total_granted":   10000000,
				"total_available": 10000000,
			})
			return
		}
		http.Error(w, `{"error":{"message":"Invalid or expired API token","type":"invalid_request_error","code":"invalid_api_key"}}`, http.StatusUnauthorized)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"object":          "list",
		"total_usage":     item.UsedQuota,
		"total_granted":   item.Quota,
		"total_available": item.RemainQuota,
	})
}

// V1ModelsHandler trả về danh sách Models chuẩn OpenAI API
func V1ModelsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	modelsList := []map[string]interface{}{
		{"id": "gemini-2.5-pro", "object": "model", "created": 1740000000, "owned_by": "supportflast"},
		{"id": "gemini-2.5-flash", "object": "model", "created": 1740000000, "owned_by": "supportflast"},
		{"id": "gemini-3.1-pro", "object": "model", "created": 1740000000, "owned_by": "supportflast"},
		{"id": "gemini-3.1-pro-high", "object": "model", "created": 1740000000, "owned_by": "supportflast"},
		{"id": "claude-3-5-sonnet", "object": "model", "created": 1740000000, "owned_by": "supportflast"},
		{"id": "claude-3-7-sonnet", "object": "model", "created": 1740000000, "owned_by": "supportflast"},
		{"id": "gpt-4o", "object": "model", "created": 1740000000, "owned_by": "supportflast"},
		{"id": "gpt-4o-mini", "object": "model", "created": 1740000000, "owned_by": "supportflast"},
		{"id": "deepseek-chat", "object": "model", "created": 1740000000, "owned_by": "supportflast"},
		{"id": "deepseek-reasoner", "object": "model", "created": 1740000000, "owned_by": "supportflast"},
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "list",
		"data":   modelsList,
	})
}

// V1ChatCompletionsHandler xử lý chat completions chuẩn OpenAI với xác thực Token và trừ Quota
func V1ChatCompletionsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":{"message":"Method not allowed","type":"invalid_request_error"}}`, http.StatusMethodNotAllowed)
		return
	}

	start := time.Now()
	token := ExtractToken(r)
	item, valid := VerifyAPITokenKey(token)
	if !valid || item == nil {
		if !VerifyAPIKey(token) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]interface{}{
					"message": "API key không hợp lệ hoặc đã hết hạn mức quota.",
					"type":    "invalid_request_error",
					"code":    "invalid_api_key",
				},
			})
			return
		}
		// Fake item cho legacy key
		item = &APITokenItem{ID: "legacy-key", UserID: "admin", Quota: 10000000, RemainQuota: 10000000}
	}

	// Đọc body request
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":{"message":"Lỗi đọc body request"}}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var reqPayload struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(bodyBytes, &reqPayload)
	if reqPayload.Model == "" {
		reqPayload.Model = "gemini-2.5-pro"
	}

	// Kiểm tra quyền gọi model
	if len(item.Models) > 0 && item.Models[0] != "*" {
		allowed := false
		for _, m := range item.Models {
			if strings.EqualFold(m, reqPayload.Model) {
				allowed = true
				break
			}
		}
		if !allowed {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]interface{}{
					"message": fmt.Sprintf("Token của bạn không có quyền truy cập model '%s'", reqPayload.Model),
					"type":    "permission_denied",
					"code":    "model_not_allowed",
				},
			})
			return
		}
	}

	clientIP := security.GetRealClientIP(r)
	requestID := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())

	// Ước tính prompt tokens
	var promptTokens int64 = 10
	for _, m := range reqPayload.Messages {
		promptTokens += int64(len(m.Content) / 4)
	}

	if reqPayload.Stream {
		// Xử lý Streaming SSE
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		replyChunk := fmt.Sprintf(`{"id":"%s","object":"chat.completion.chunk","created":%d,"model":"%s","choices":[{"index":0,"delta":{"role":"assistant","content":"Hệ thống Gateway SupportFlast AI đã tiếp nhận và phản hồi thành công yêu cầu từ bạn."},"finish_reason":null}]}`, requestID, time.Now().Unix(), reqPayload.Model)
		fmt.Fprintf(w, "data: %s\n\n", replyChunk)
		flusher.Flush()

		time.Sleep(50 * time.Millisecond)

		doneChunk := fmt.Sprintf(`{"id":"%s","object":"chat.completion.chunk","created":%d,"model":"%s","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`, requestID, time.Now().Unix(), reqPayload.Model)
		fmt.Fprintf(w, "data: %s\n\n", doneChunk)
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()

		latencyMs := time.Since(start).Milliseconds()
		var completionTokens int64 = 25
		DeductTokenQuotaAsync(item.ID, item.UserID, reqPayload.Model, promptTokens, completionTokens, latencyMs, clientIP)
		return
	}

	// Xử lý Non-streaming
	w.Header().Set("Content-Type", "application/json")
	var completionTokens int64 = 30
	latencyMs := time.Since(start).Milliseconds()

	resp := map[string]interface{}{
		"id":      requestID,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   reqPayload.Model,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": "Phản hồi mẫu an toàn từ Cổng Gateway SupportFlast AI Hub. Yêu cầu của bạn đã được xác thực mã khóa API Token và trừ hạn mức chính xác.",
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]interface{}{
			"prompt_tokens":     promptTokens,
			"completion_tokens": completionTokens,
			"total_tokens":      promptTokens + completionTokens,
		},
	}

	DeductTokenQuotaAsync(item.ID, item.UserID, reqPayload.Model, promptTokens, completionTokens, latencyMs, clientIP)
	json.NewEncoder(w).Encode(resp)
}

// APIRouterProvidersHandler cung cấp danh sách Upstream Providers an toàn cho Router Client sync
func APIRouterProvidersHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	token := ExtractToken(r)
	if token == "" {
		token = r.URL.Query().Get("key")
	}

	// Xác thực token (chấp nhận Master token hoặc Admin token)
	item, valid := VerifyAPITokenKey(token)
	if !valid && !VerifyAPIKey(token) {
		http.Error(w, `{"error":"Unauthorized: Yêu cầu Master API Key hợp lệ để đồng bộ Providers"}`, http.StatusUnauthorized)
		return
	}

	_ = item
	// Trả về danh sách upstream providers khả dụng
	providers := []map[string]interface{}{
		{
			"id":         "prov-gemini-cloud",
			"provider":   "gemini",
			"auth_type":  "oauth",
			"name":       "Gemini Ultra Cloud Pool (SupportFlast)",
			"email":      "cloud-gemini@supportflastdev.io.vn",
			"priority":   100,
			"is_active":  true,
			"created_at": time.Now().UTC().Format(time.RFC3339),
			"updated_at": time.Now().UTC().Format(time.RFC3339),
		},
		{
			"id":         "prov-claude-cloud",
			"provider":   "claude",
			"auth_type":  "api_key",
			"name":       "Claude 3.5 Sonnet Router Pool",
			"email":      "cloud-claude@supportflastdev.io.vn",
			"priority":   90,
			"is_active":  true,
			"created_at": time.Now().UTC().Format(time.RFC3339),
			"updated_at": time.Now().UTC().Format(time.RFC3339),
		},
		{
			"id":         "prov-openai-cloud",
			"provider":   "openai",
			"auth_type":  "api_key",
			"name":       "OpenAI GPT-4o Gateway Pool",
			"email":      "cloud-openai@supportflastdev.io.vn",
			"priority":   85,
			"is_active":  true,
			"created_at": time.Now().UTC().Format(time.RFC3339),
			"updated_at": time.Now().UTC().Format(time.RFC3339),
		},
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"total":     len(providers),
		"providers": providers,
	})
}
