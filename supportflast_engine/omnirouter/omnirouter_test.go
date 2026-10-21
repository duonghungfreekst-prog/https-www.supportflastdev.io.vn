package omnirouter

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite", "file:memtest?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("Không thể mở CSDL in-memory: %v", err)
	}
	db.SetMaxOpenConns(1)

	if err := InitStorage(db, false); err != nil {
		t.Fatalf("InitStorage thất bại: %v", err)
	}
	return db
}

func TestClientKeyLifecycle(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// 1. Tạo API Key mới
	key, err := CreateNewClientKey(db, "Test-Antigravity-PC")
	if err != nil {
		t.Fatalf("CreateNewClientKey thất bại: %v", err)
	}
	if key.Key == "" || key.Name != "Test-Antigravity-PC" {
		t.Fatalf("Dữ liệu key không đúng: %+v", key)
	}

	// 2. Xác thực API Key
	cachedKey, ok := ValidateClientKey(key.Key)
	if !ok || cachedKey == nil {
		t.Fatalf("ValidateClientKey thất bại cho key vừa tạo")
	}

	// 3. Khóa API Key
	if err := RevokeClientKey(db, key.ID); err != nil {
		t.Fatalf("RevokeClientKey thất bại: %v", err)
	}
	_, okAfterRevoke := ValidateClientKey(key.Key)
	if okAfterRevoke {
		t.Fatalf("Key bị khóa vẫn trả về hợp lệ!")
	}

	// 4. Liệt kê danh sách
	list, err := ListAllClientKeys(db)
	if err != nil || len(list) < 1 {
		t.Fatalf("ListAllClientKeys thất bại: %v, count=%d", err, len(list))
	}
	found := false
	for _, item := range list {
		if item.ID == key.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Không tìm thấy key vừa tạo trong danh sách!")
	}
}

func TestSyncFromLibSQL(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Kiểm tra xem data.libsql thật có tồn tại không
	if _, err := os.Stat(DefaultLibSQLPath); err != nil {
		t.Skip("Tệp data.libsql không tồn tại tại đường dẫn mặc định, bỏ qua test đồng bộ thật")
		return
	}

	res, err := SyncFromLibSQL(db, DefaultLibSQLPath)
	if err != nil {
		t.Fatalf("SyncFromLibSQL thất bại: %v", err)
	}

	if !res.Success {
		t.Fatalf("SyncFromLibSQL trả về success=false: %s", res.Message)
	}

	if res.ProvidersCount < 10 {
		t.Fatalf("Kỳ vọng đồng bộ >= 10 providers nhưng chỉ được %d", res.ProvidersCount)
	}

	activeProvs := GetCachedActiveProviders()
	if len(activeProvs) == 0 || len(activeProvs) > res.ProvidersCount {
		t.Fatalf("Bộ đệm RAM providers không hợp lệ: %d (tổng DB: %d)", len(activeProvs), res.ProvidersCount)
	}
}

func TestClientSyncAndReportEndpoints(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Tạo 1 key hợp lệ
	key, err := CreateNewClientKey(db, "Test-Client-Device")
	if err != nil {
		t.Fatalf("Tạo key thất bại: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/client/sync", HandleClientSync(db))
	mux.HandleFunc("/api/v1/client/report", HandleClientReport(db))

	// 1. Test Client Sync
	req := httptest.NewRequest("GET", "/api/v1/client/sync", nil)
	req.Header.Set("Authorization", "Bearer "+key.Key)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Client Sync trả về status %d: %s", rec.Code, rec.Body.String())
	}

	var syncResp ClientSyncResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &syncResp); err != nil {
		t.Fatalf("Parse syncResp thất bại: %v", err)
	}
	if syncResp.Status != "ok" {
		t.Fatalf("Status syncResp không phải 'ok': %s", syncResp.Status)
	}

	// 2. Test Client Report
	reportPayload := ClientReportPayload{
		ApiKey:       key.Key,
		UsedRequests: 5,
		TotalTokens:  1250,
	}
	reportBytes, _ := json.Marshal(reportPayload)
	reqRep := httptest.NewRequest("POST", "/api/v1/client/report", bytes.NewReader(reportBytes))
	reqRep.Header.Set("Content-Type", "application/json")
	recRep := httptest.NewRecorder()
	mux.ServeHTTP(recRep, reqRep)

	if recRep.Code != http.StatusOK {
		t.Fatalf("Client Report trả về status %d: %s", recRep.Code, recRep.Body.String())
	}
}

func TestEmbeddedDataSeed(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Kiểm tra nhúng hạt nhân 19 tài khoản cố định
	err := SeedEmbeddedDataToDB(db)
	if err != nil {
		t.Fatalf("SeedEmbeddedDataToDB thất bại: %v", err)
	}

	providers := GetCachedActiveProviders()
	if len(providers) != 19 {
		t.Fatalf("Kỳ vọng 19 providers sau khi seed, nhận được: %d", len(providers))
	}
}

func TestClientConfigEndpoint(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// 1. Tạo API Key có chứa tên miền web
	key, err := CreateNewClientKeyWithDomain(db, "CMD-Test-Device", "supportflastdev.io.vn")
	if err != nil {
		t.Fatalf("Tạo key thất bại: %v", err)
	}
	if !strings.Contains(key.Key, "supportflastdev.io.vn") {
		t.Fatalf("API Key không chứa tên miền web: %s", key.Key)
	}

	mux := http.NewServeMux()
	cfgHandler := HandleClientConfig(db)
	mux.HandleFunc("/api/v1/client/config", cfgHandler)
	mux.HandleFunc("/api/v1/client/setup.bat", cfgHandler)

	// 2. Test lấy cấu hình JSON qua Header
	req := httptest.NewRequest("GET", "/api/v1/client/config", nil)
	req.Header.Set("Authorization", "Bearer "+key.Key)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Client Config trả về status %d: %s", rec.Code, rec.Body.String())
	}

	var cfgResp ClientConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &cfgResp); err != nil {
		t.Fatalf("Parse ClientConfigResponse thất bại: %v", err)
	}
	if !cfgResp.Success {
		t.Fatalf("ClientConfigResponse success = false")
	}
	if cfgResp.Server.Domain != "supportflastdev.io.vn" {
		t.Fatalf("Domain không khớp: %s", cfgResp.Server.Domain)
	}
	if !cfgResp.Mitm.Enabled || cfgResp.Mitm.Port != 443 {
		t.Fatalf("Mitm config không đúng: %+v", cfgResp.Mitm)
	}
	if !cfgResp.DNS.Enabled || len(cfgResp.DNS.Domains) == 0 {
		t.Fatalf("DNS config không đúng: %+v", cfgResp.DNS)
	}

	// 3. Test lấy file script .bat qua query param ?key=...
	reqBat := httptest.NewRequest("GET", "/api/v1/client/setup.bat?key="+key.Key, nil)
	recBat := httptest.NewRecorder()
	mux.ServeHTTP(recBat, reqBat)

	if recBat.Code != http.StatusOK {
		t.Fatalf("Setup.bat trả về status %d: %s", recBat.Code, recBat.Body.String())
	}
	batContent := recBat.Body.String()
	if !strings.Contains(batContent, "set OPENAI_BASE_URL=") || !strings.Contains(batContent, key.Key) {
		t.Fatalf("Nội dung setup.bat thiếu OPENAI_BASE_URL hoặc key: %s", batContent)
	}
}

func TestClientQuotaCLIEndpoint(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	_ = SeedEmbeddedDataToDB(db)

	key, err := CreateNewClientKeyWithDomain(db, "Terminal-Ping-Key", "supportflastdev.io.vn")
	if err != nil {
		t.Fatalf("Tạo key thất bại: %v", err)
	}

	mux := http.NewServeMux()
	quotaHandler := HandleClientQuotaCLI(db)
	mux.HandleFunc("/api/v1/client/quota", quotaHandler)
	mux.HandleFunc("/api/v1/client/quota.bat", quotaHandler)
	mux.HandleFunc("/api/v1/client/ping", quotaHandler)

	// 1. Test Text ASCII Console CLI Output
	req := httptest.NewRequest("GET", "/api/v1/client/quota?key="+key.Key, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Status %d: %s", rec.Code, rec.Body.String())
	}
	output := rec.Body.String()
	if !strings.Contains(output, "SUPPORTFLAST GATEWAY") || !strings.Contains(output, "DNS Domain") {
		t.Fatalf("Output không chứa tiêu đề CLI: %s", output)
	}
	if !strings.Contains(output, "DANH SACH API KEYS DA TAO") || !strings.Contains(output, "QUOTA DA DUNG") {
		t.Fatalf("Output thiếu bảng API Keys và Quota đã dùng: %s", output)
	}
	if !strings.Contains(output, "Key Dang Dung") {
		t.Fatalf("Output thiếu khối thông tin Key Dang Dung: %s", output)
	}

	// 2. Test JSON format
	reqJSON := httptest.NewRequest("GET", "/api/v1/client/quota?format=json&key="+key.Key, nil)
	recJSON := httptest.NewRecorder()
	mux.ServeHTTP(recJSON, reqJSON)

	if recJSON.Code != http.StatusOK {
		t.Fatalf("Status JSON %d: %s", recJSON.Code, recJSON.Body.String())
	}
	var jsonResp map[string]interface{}
	if err := json.Unmarshal(recJSON.Body.Bytes(), &jsonResp); err != nil {
		t.Fatalf("Unmarshal JSON thất bại: %v", err)
	}
	if jsonResp["status"] != "online" {
		t.Fatalf("Kỳ vọng status online, nhận được: %v", jsonResp["status"])
	}

	// 3. Test script quota.bat
	reqBat := httptest.NewRequest("GET", "/api/v1/client/quota.bat?key="+key.Key, nil)
	recBat := httptest.NewRecorder()
	mux.ServeHTTP(recBat, reqBat)

	if recBat.Code != http.StatusOK {
		t.Fatalf("Status quota.bat %d: %s", recBat.Code, recBat.Body.String())
	}
	batText := recBat.Body.String()
	if !strings.Contains(batText, "ping -n 2") || !strings.Contains(batText, key.Key) {
		t.Fatalf("Nội dung quota.bat không đúng: %s", batText)
	}
}

func TestProviderModelsEndpoint(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	mux := http.NewServeMux()
	modelsHandler := HandleProviderModels(db)
	mux.HandleFunc("/api/models/provider", modelsHandler)

	// 1. Kiểm tra GET mặc định trả về 15 model của Antigravity
	reqGet := httptest.NewRequest("GET", "/api/models/provider?id=antigravity", nil)
	recGet := httptest.NewRecorder()
	mux.ServeHTTP(recGet, reqGet)

	if recGet.Code != http.StatusOK {
		t.Fatalf("Kỳ vọng status 200, nhận được: %d: %s", recGet.Code, recGet.Body.String())
	}

	var resp struct {
		Provider        string              `json:"provider"`
		AvailableModels []ProviderModelItem `json:"availableModels"`
		DisabledModels  []ProviderModelItem `json:"disabledModels"`
		Total           int                 `json:"total"`
	}
	if err := json.Unmarshal(recGet.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal JSON thất bại: %v", err)
	}

	expectedTotal := len(GetDefaultModelsForProvider("antigravity"))
	if len(resp.AvailableModels) != expectedTotal {
		t.Fatalf("Kỳ vọng %d available models, nhận được %d", expectedTotal, len(resp.AvailableModels))
	}
	if len(resp.DisabledModels) != 0 {
		t.Fatalf("Kỳ vọng 0 disabled models, nhận được %d", len(resp.DisabledModels))
	}

	// 2. Tắt 1 model (4-argon)
	activeFalse := false
	payloadDisable, _ := json.Marshal(map[string]interface{}{
		"provider": "antigravity",
		"modelId":  "4-argon",
		"active":   &activeFalse,
	})
	reqPostDisable := httptest.NewRequest("POST", "/api/models/provider", bytes.NewReader(payloadDisable))
	recPostDisable := httptest.NewRecorder()
	mux.ServeHTTP(recPostDisable, reqPostDisable)

	if recPostDisable.Code != http.StatusOK {
		t.Fatalf("POST disable thất bại: %d: %s", recPostDisable.Code, recPostDisable.Body.String())
	}

	// 3. GET lại xác minh model đã chuyển sang disabledModels
	recGet2 := httptest.NewRecorder()
	mux.ServeHTTP(recGet2, reqGet)
	_ = json.Unmarshal(recGet2.Body.Bytes(), &resp)

	if len(resp.AvailableModels) != expectedTotal-1 || len(resp.DisabledModels) != 1 {
		t.Fatalf("Kỳ vọng %d available và 1 disabled, nhận được: %d / %d", expectedTotal-1, len(resp.AvailableModels), len(resp.DisabledModels))
	}
	if resp.DisabledModels[0].ID != "4-argon" {
		t.Fatalf("Disabled model ID không đúng: %s", resp.DisabledModels[0].ID)
	}

	// 4. Kích hoạt lại (active-all)
	payloadActiveAll, _ := json.Marshal(map[string]interface{}{
		"provider": "antigravity",
		"action":   "active-all",
	})
	reqPostActiveAll := httptest.NewRequest("POST", "/api/models/provider", bytes.NewReader(payloadActiveAll))
	recPostActiveAll := httptest.NewRecorder()
	mux.ServeHTTP(recPostActiveAll, reqPostActiveAll)

	recGet3 := httptest.NewRecorder()
	mux.ServeHTTP(recGet3, reqGet)
	_ = json.Unmarshal(recGet3.Body.Bytes(), &resp)

	if len(resp.AvailableModels) != expectedTotal || len(resp.DisabledModels) != 0 {
		t.Fatalf("Sau active-all kỳ vọng %d available và 0 disabled, nhận được: %d / %d", expectedTotal, len(resp.AvailableModels), len(resp.DisabledModels))
	}

	// 5. Thêm custom model
	payloadCustom, _ := json.Marshal(map[string]interface{}{
		"provider":    "antigravity",
		"customModel": "gemini-3.9-flash",
	})
	reqPostCustom := httptest.NewRequest("POST", "/api/models/provider", bytes.NewReader(payloadCustom))
	recPostCustom := httptest.NewRecorder()
	mux.ServeHTTP(recPostCustom, reqPostCustom)

	recGet4 := httptest.NewRecorder()
	mux.ServeHTTP(recGet4, reqGet)
	_ = json.Unmarshal(recGet4.Body.Bytes(), &resp)

	if len(resp.AvailableModels) != expectedTotal+1 {
		t.Fatalf("Sau khi thêm custom model kỳ vọng %d available models, nhận được: %d", expectedTotal+1, len(resp.AvailableModels))
	}
}

func TestProviderLogoStaticRoute(t *testing.T) {
	mux := http.NewServeMux()
	uiDir := filepath.Join("..", "supportflast_ui")
	db := setupTestDB(t)
	defer db.Close()

	RegisterOmniRouterRoutes(mux, db, false, uiDir)

	// 1. Test GET /providers/antigravity.png
	req := httptest.NewRequest("GET", "/providers/antigravity.png", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Phục vụ tĩnh /providers/antigravity.png thất bại: status=%d", rec.Code)
	}

	// 2. Test GET /router/providers/claude.png
	req2 := httptest.NewRequest("GET", "/router/providers/claude.png", nil)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("Phục vụ tĩnh /router/providers/claude.png thất bại: status=%d", rec2.Code)
	}
}

func TestClientKeyHourlyAndQuotaLimit(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// 1. Tạo Key có hạn mức Quota = 2 và thời hạn 1 giờ
	key, err := CreateNewClientKeyWithQuotaAndExpiry(db, "Test-Quota-Key", "supportflastdev.io.vn", 2, 1)
	if err != nil {
		t.Fatalf("Tạo key thất bại: %v", err)
	}
	if key.QuotaLimit != 2 || key.DurationHours != 1 || key.ExpiresAt == "" {
		t.Fatalf("Key khởi tạo không đúng tham số: %+v", key)
	}

	// Lần đầu kiểm tra: hợp lệ
	_, valid := ValidateClientKey(key.Key)
	if !valid {
		t.Fatalf("Kỳ vọng key mới tạo hợp lệ")
	}

	// Giả lập dùng 2 requests -> chạm hạn mức
	RecordKeyUsageAsync(db, key.Key, 2, 100)
	time.Sleep(50 * time.Millisecond) // chờ async ghi nhận

	// Kiểm tra lại: phải bị từ chối do chạm quota
	_, status, errMsg := CheckClientKeyStatus(key.Key)
	if status != "QUOTA_EXCEEDED" {
		t.Fatalf("Kỳ vọng QUOTA_EXCEEDED, nhận được: %s (%s)", status, errMsg)
	}
	_, validAfterQuota := ValidateClientKey(key.Key)
	if validAfterQuota {
		t.Fatalf("Kỳ vọng ValidateClientKey trả về false khi đã vượt quota")
	}

	// 2. Tạo Key hết hạn thời gian
	pastTime := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	expiredKey, err := CreateNewClientKeyWithQuotaAndExpiry(db, "Expired-Key", "supportflastdev.io.vn", 0, 0)
	if err != nil {
		t.Fatalf("Tạo expired key thất bại: %v", err)
	}
	_, _ = db.Exec("UPDATE omni_client_keys SET expires_at = ? WHERE id = ?", pastTime, expiredKey.ID)
	RefreshKeyCache(db)

	_, expStatus, expErrMsg := CheckClientKeyStatus(expiredKey.Key)
	if expStatus != "EXPIRED" {
		t.Fatalf("Kỳ vọng EXPIRED, nhận được: %s (%s)", expStatus, expErrMsg)
	}
	_, expValid := ValidateClientKey(expiredKey.Key)
	if expValid {
		t.Fatalf("Kỳ vọng ValidateClientKey trả về false khi key đã hết hạn theo giờ")
	}

	// 3. Test HTTP POST /api/router/keys tạo key theo giờ và quota
	handler := HandleManageKeys(db)
	postBody := `{"name":"Device-Hourly","quotaLimit":100,"durationHours":48}`
	req := httptest.NewRequest("POST", "/api/router/keys", strings.NewReader(postBody))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/router/keys thất bại: %d - %s", rec.Code, rec.Body.String())
	}
	var res map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("Unmarshal response thất bại: %v", err)
	}
	if res["quotaLimit"] != float64(100) || res["durationHours"] != float64(48) {
		t.Fatalf("Response không chứa đúng quotaLimit hoặc durationHours: %+v", res)
	}
}

func TestUpgradedRouterEndpoints(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	mux := http.NewServeMux()
	RegisterOmniRouterRoutes(mux, db, false, ".")

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{"Ping", "GET", "/ping", http.StatusOK},
		{"Health", "GET", "/api/health", http.StatusOK},
		{"SettingsGet", "GET", "/api/settings", http.StatusOK},
		{"SettingsDB", "GET", "/api/settings/database", http.StatusOK},
		{"KeysList", "GET", "/api/keys", http.StatusOK},
		{"ProvidersList", "GET", "/api/providers", http.StatusOK},
		{"ProvidersTestBatch", "GET", "/api/providers/test-batch", http.StatusOK},
		{"ProxyPools", "GET", "/api/proxy-pools", http.StatusOK},
		{"Combos", "GET", "/api/combos", http.StatusOK},
		{"OptimizerStats", "GET", "/api/optimizer/stats", http.StatusOK},
		{"TunnelStatus", "GET", "/api/tunnel/status", http.StatusOK},
		{"SkillsCore", "GET", "/api/skills/core", http.StatusOK},
		{"SkillsActive", "GET", "/api/skills/active", http.StatusOK},
		{"RouterDashboard", "GET", "/router", http.StatusOK},
		{"ClientApp", "GET", "/client", http.StatusOK},
		{"ClientAppSlash", "GET", "/client/", http.StatusOK},
		{"AppRoute", "GET", "/app", http.StatusOK},
		{"ShareAppRoute", "GET", "/share-app", http.StatusOK},
		{"ShareRoute", "GET", "/share", http.StatusOK},
		{"ClientSyncAlias", "GET", "/api/client/sync", http.StatusUnauthorized},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.RemoteAddr = "127.0.0.1:20128"
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Errorf("[%s %s] status = %d, want %d (body: %s)", tc.method, tc.path, rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestClientTelemetryAndQuotaReport(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// 1. Tạo một key thử nghiệm với Quota = 100
	testKey, err := CreateNewClientKeyWithQuotaAndExpiry(db, "Test-Device-v12", "supportflastdev.io.vn", 100, 24)
	if err != nil {
		t.Fatalf("Không thể tạo test key: %v", err)
	}

	// 2. SetActiveClientKey
	SetActiveClientKey(testKey.Key)
	retrievedKey := GetActiveClientKey(db)
	if retrievedKey != testKey.Key {
		t.Fatalf("Kỳ vọng retrievedKey = %s, nhận được: %s", testKey.Key, retrievedKey)
	}

	// 3. Test API /api/v1/client/set_active_key
	mux := http.NewServeMux()
	RegisterOmniRouterRoutes(mux, db, false, ".")

	bodyStr := fmt.Sprintf(`{"key":"%s"}`, testKey.Key)
	req := httptest.NewRequest("POST", "/api/v1/client/set_active_key", strings.NewReader(bodyStr))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/client/set_active_key thất bại: %d", rec.Code)
	}

	// 4. Test API /api/v1/client/report với telemetry log và tăng Quota
	reportPayload := fmt.Sprintf(`{
		"api_key": "%s",
		"used_requests": 3,
		"total_tokens": 150,
		"model": "gemini-3.8-flash-high",
		"endpoint": "/v1internal:streamGenerateContent",
		"account_name": "ai-master@google.com",
		"client_device": "Antigravity IDE (Desktop)",
		"latency_ms": 1150,
		"status_code": 200,
		"log_message": "Antigravity IDE test telemetry",
		"skip_db_increment": false
	}`, testKey.Key)

	reqReport := httptest.NewRequest("POST", "/api/v1/client/report", strings.NewReader(reportPayload))
	reqReport.Header.Set("Content-Type", "application/json")
	recReport := httptest.NewRecorder()
	mux.ServeHTTP(recReport, reqReport)

	if recReport.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/client/report thất bại: %d - %s", recReport.Code, recReport.Body.String())
	}

	time.Sleep(50 * time.Millisecond) // chờ async ghi nhận vào SQLite/TiDB

	// 5. Kiểm tra Quota đã tăng lên 3 trong DB
	var usedReqs int64
	err = db.QueryRow("SELECT used_requests FROM omni_client_keys WHERE api_key = ?", testKey.Key).Scan(&usedReqs)
	if err != nil {
		t.Fatalf("Lỗi truy vấn used_requests: %v", err)
	}
	if usedReqs != 3 {
		t.Fatalf("Kỳ vọng used_requests = 3, nhận được: %d", usedReqs)
	}
}

func TestProxyConfigurationAndBinding(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// 1. T?o 2 t?i kho?n antigravity m?u
	accData := `{"accessToken":"ya29.test","connectionProxyEnabled":false,"proxyUrl":""}`
	_, _ = db.Exec(`INSERT INTO omni_providers (id, provider, auth_type, name, email, priority, is_active, data, created_at, updated_at)
		VALUES ('acc_test_1', 'antigravity', 'oauth', 'Acc 1', 'acc1@gmail.com', 1, 1, ?, '2026-10-19', '2026-10-19'),
		       ('acc_test_2', 'antigravity', 'oauth', 'Acc 2', 'acc2@gmail.com', 2, 1, ?, '2026-10-19', '2026-10-19')`,
		accData, accData)

	// 2. Test POST /api/proxy-pools: T?o proxy pool m?u
	createPayload := `{"id":"42db42b6-7136-4c0c-8feb-20759c8e0946","name":"VNPT Test","proxyUrl":"http://pyYXdt:QWfItD@58.186.63.192:18753/","type":"http"}`
	reqCreate := httptest.NewRequest("POST", "/api/proxy-pools", strings.NewReader(createPayload))
	reqCreate.Header.Set("Content-Type", "application/json")
	recCreate := httptest.NewRecorder()
	HandleProxyPools(db)(recCreate, reqCreate)
	if recCreate.Code != http.StatusOK {
		t.Fatalf("POST /api/proxy-pools t?o proxy th?t b?i: %d", recCreate.Code)
	}

	// 3. Test GET /api/proxy-pools
	reqPools := httptest.NewRequest("GET", "/api/proxy-pools", nil)
	recPools := httptest.NewRecorder()
	HandleProxyPools(db)(recPools, reqPools)
	if recPools.Code != http.StatusOK {
		t.Fatalf("GET /api/proxy-pools th?t b?i: %d", recPools.Code)
	}
	var poolsResp map[string]interface{}
	_ = json.Unmarshal(recPools.Body.Bytes(), &poolsResp)
	pList, _ := poolsResp["proxyPools"].([]interface{})
	if len(pList) == 0 {
		t.Fatalf("K? v?ng proxyPools c? danh s?ch")
	}

	// 4. Test PUT /api/proxy-pools/{id}: C?p nh?t proxy pool
	putPayload := `{"name":"VNPT Clean Strict","strictProxy":true}`
	reqPut := httptest.NewRequest("PUT", "/api/proxy-pools/42db42b6-7136-4c0c-8feb-20759c8e0946", strings.NewReader(putPayload))
	reqPut.Header.Set("Content-Type", "application/json")
	recPut := httptest.NewRecorder()
	HandleProxyPools(db)(recPut, reqPut)
	if recPut.Code != http.StatusOK {
		t.Fatalf("PUT /api/proxy-pools c?p nh?t th?t b?i: %d", recPut.Code)
	}

	// 5. Test POST /api/proxy-pools/bind: G?n proxy pool cho antigravity
	bindPayload := `{"proxyPoolId":"42db42b6-7136-4c0c-8feb-20759c8e0946","provider":"antigravity","action":"bind"}`
	reqBind := httptest.NewRequest("POST", "/api/proxy-pools/bind", strings.NewReader(bindPayload))
	reqBind.Header.Set("Content-Type", "application/json")
	recBind := httptest.NewRecorder()
	HandleBindProxyPool(db)(recBind, reqBind)
	if recBind.Code != http.StatusOK {
		t.Fatalf("POST /api/proxy-pools/bind th?t b?i: %d", recBind.Code)
	}
	var bindResp map[string]interface{}
	_ = json.Unmarshal(recBind.Body.Bytes(), &bindResp)
	if count, ok := bindResp["count"].(float64); !ok || count != 2 {
		t.Fatalf("K? v?ng bind th?nh c?ng cho 2 t?i kho?n, nh?n ???c: %v", bindResp["count"])
	}

	// 6. Test GET /api/providers: Ki?m tra badge proxy ?? c?p nh?t
	reqProviders := httptest.NewRequest("GET", "/api/providers", nil)
	recProviders := httptest.NewRecorder()
	HandleListProviders(db)(recProviders, reqProviders)
	if recProviders.Code != http.StatusOK {
		t.Fatalf("GET /api/providers th?t b?i: %d", recProviders.Code)
	}
	var provResp map[string]interface{}
	_ = json.Unmarshal(recProviders.Body.Bytes(), &provResp)
	pArr, _ := provResp["providers"].([]interface{})
	if len(pArr) != 2 {
		t.Fatalf("K? v?ng 2 providers, nh?n ???c: %d", len(pArr))
	}
	p0 := pArr[0].(map[string]interface{})
	if en, ok := p0["connectionProxyEnabled"].(bool); !ok || !en {
		t.Fatalf("K? v?ng connectionProxyEnabled = true sau khi bind, nh?n ???c: %v", p0["connectionProxyEnabled"])
	}

	// 7. Test DELETE /api/proxy-pools/{id}: X?a v?nh vi?n proxy v? cascade unbind
	reqDelete := httptest.NewRequest("DELETE", "/api/proxy-pools/42db42b6-7136-4c0c-8feb-20759c8e0946", nil)
	recDelete := httptest.NewRecorder()
	HandleProxyPools(db)(recDelete, reqDelete)
	if recDelete.Code != http.StatusOK {
		t.Fatalf("DELETE /api/proxy-pools x?a th?t b?i: %d", recDelete.Code)
	}

	// 8. Test GET /api/proxy-pools sau khi x?a: Ph?i r?ng (0 pool)
	reqPoolsAfter := httptest.NewRequest("GET", "/api/proxy-pools", nil)
	recPoolsAfter := httptest.NewRecorder()
	HandleProxyPools(db)(recPoolsAfter, reqPoolsAfter)
	var poolsRespAfter map[string]interface{}
	_ = json.Unmarshal(recPoolsAfter.Body.Bytes(), &poolsRespAfter)
	pListAfter, _ := poolsRespAfter["proxyPools"].([]interface{})
	if len(pListAfter) != 0 {
		t.Fatalf("K? v?ng danh s?ch proxyPools r?ng sau khi x?a, nh?n ???c: %d", len(pListAfter))
	}

	// 9. Test GET /api/providers sau khi x?a proxy: Ph?i t? ??ng cascade unbind v? Direct
	reqProvAfter := httptest.NewRequest("GET", "/api/providers", nil)
	recProvAfter := httptest.NewRecorder()
	HandleListProviders(db)(recProvAfter, reqProvAfter)
	var provRespAfter map[string]interface{}
	_ = json.Unmarshal(recProvAfter.Body.Bytes(), &provRespAfter)
	pArrAfter, _ := provRespAfter["providers"].([]interface{})
	p0After := pArrAfter[0].(map[string]interface{})
	if en, ok := p0After["connectionProxyEnabled"].(bool); ok && en {
		t.Fatalf("K? v?ng connectionProxyEnabled = false sau khi x?a proxy pool, nh?ng v?n c?n b?t!")
	}
	if pUrl, _ := p0After["proxyUrl"].(string); pUrl != "" {
		t.Fatalf("K? v?ng proxyUrl = '' sau khi x?a proxy pool, nh?n ???c: %s", pUrl)
	}
}

func TestResolveCascadeModelTiers_NoAutomaticFallback(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// 1. Kiểm tra khi client gọi gemini-pro-agent (hoặc 3.1-pro)
	tiersPro := resolveCascadeModelTiers(db, "gemini-pro-agent")
	if len(tiersPro) != 1 || tiersPro[0] != "gemini-pro-agent" {
		t.Fatalf("Kỳ vọng chỉ duy nhất 1 tier gemini-pro-agent, nhận được: %v", tiersPro)
	}

	tiers31 := resolveCascadeModelTiers(db, "gemini-3.1-pro")
	if len(tiers31) != 1 || tiers31[0] != "gemini-3.1-pro-high" && tiers31[0] != "gemini-pro-agent" {
		t.Fatalf("Kỳ vọng chỉ duy nhất 1 tier gemini-pro-agent cho gemini-3.1-pro, nhận được: %v", tiers31)
	}

	// 2. Kiểm tra khi client gọi flash
	tiersFlash := resolveCascadeModelTiers(db, "gemini-3.8-flash-high")
	if len(tiersFlash) != 1 || tiersFlash[0] != "gemini-3.8-flash-high" {
		t.Fatalf("Kỳ vọng chỉ duy nhất 1 tier gemini-3.8-flash-high, nhận được: %v", tiersFlash)
	}

	// 3. Kiểm tra khi client gọi claude sonnet
	tiersSonnet := resolveCascadeModelTiers(db, "claude-sonnet-4-6")
	if len(tiersSonnet) != 1 || tiersSonnet[0] != "claude-sonnet-4-6" {
		t.Fatalf("Kỳ vọng chỉ duy nhất 1 tier claude-sonnet-4-6, nhận được: %v", tiersSonnet)
	}
}






func TestChatCompletionsAuthMatrix(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	handler := HandleChatCompletions(db)
	dummyBody := `{"model":"gemini-3.8-flash-high","messages":[{"role":"user","content":"hi"}]}`

	// 1. Trường hợp thiếu key -> 401 missing_api_key
	reqMissing := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(dummyBody))
	recMissing := httptest.NewRecorder()
	handler(recMissing, reqMissing)
	if recMissing.Code != http.StatusUnauthorized {
		t.Errorf("Thiếu key: kỳ vọng 401, nhận: %d", recMissing.Code)
	}

	// 2. Trường hợp sai key -> 401 invalid_api_key
	reqInvalid := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(dummyBody))
	reqInvalid.Header.Set("Authorization", "Bearer sk-invalid-key-xyz-123")
	recInvalid := httptest.NewRecorder()
	handler(recInvalid, reqInvalid)
	if recInvalid.Code != http.StatusUnauthorized {
		t.Errorf("Sai key: kỳ vọng 401, nhận: %d", recInvalid.Code)
	}

	// 3. Trường hợp key hết hạn -> 403 key_expired
	pastTime := time.Now().Add(-5 * time.Hour).Format(time.RFC3339)
	expKey, err := CreateNewClientKeyWithQuotaAndExpiry(db, "Test-Expired", "supportflastdev.io.vn", 0, 0)
	if err != nil {
		t.Fatalf("Tạo expired key lỗi: %v", err)
	}
	_, _ = db.Exec("UPDATE omni_client_keys SET expires_at = ? WHERE id = ?", pastTime, expKey.ID)
	RefreshKeyCache(db)

	reqExp := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(dummyBody))
	reqExp.Header.Set("Authorization", "Bearer "+expKey.Key)
	recExp := httptest.NewRecorder()
	handler(recExp, reqExp)
	if recExp.Code != http.StatusForbidden {
		t.Errorf("Key hết hạn: kỳ vọng 403, nhận: %d", recExp.Code)
	}

	// 4. Trường hợp key vượt Quota -> 429 quota_exceeded
	quotaKey, err := CreateNewClientKeyWithQuotaAndExpiry(db, "Test-Quota-Exceeded", "supportflastdev.io.vn", 1, 24)
	if err != nil {
		t.Fatalf("Tạo quota key lỗi: %v", err)
	}
	RecordKeyUsageAsync(db, quotaKey.Key, 1, 50)
	time.Sleep(50 * time.Millisecond)

	reqQuota := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(dummyBody))
	reqQuota.Header.Set("Authorization", "Bearer "+quotaKey.Key)
	recQuota := httptest.NewRecorder()
	handler(recQuota, reqQuota)
	if recQuota.Code != http.StatusTooManyRequests {
		t.Errorf("Key vượt quota: kỳ vọng 429, nhận: %d", recQuota.Code)
	}

	// 5. Trường hợp key hợp lệ -> vượt qua tầng xác thực (không trả 401/403/429 vì auth)
	validKey, err := CreateNewClientKeyWithQuotaAndExpiry(db, "Test-Valid", "supportflastdev.io.vn", 100, 24)
	if err != nil {
		t.Fatalf("Tạo valid key lỗi: %v", err)
	}
	reqValid := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(dummyBody))
	reqValid.Header.Set("Authorization", "Bearer "+validKey.Key)
	recValid := httptest.NewRecorder()
	handler(recValid, reqValid)
	if recValid.Code == http.StatusUnauthorized || recValid.Code == http.StatusForbidden {
		t.Errorf("Key hợp lệ không được bị từ chối xác thực: %d", recValid.Code)
	}
}

// TestAtomicQuotaReservationConcurrent kiểm chứng phép thử nghiệm thu bắt buộc:
// Đặt quota còn đúng 1 lượt, gửi 10 request đồng thời. Chỉ DUY NHẤT 1 request được chấp nhận.
func TestAtomicQuotaReservationConcurrent(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	key, err := CreateNewClientKeyWithQuotaAndExpiry(db, "Test-Concurrency", "supportflastdev.io.vn", 1, 24)
	if err != nil {
		t.Fatalf("Tạo key thất bại: %v", err)
	}

	concurrentRequests := 10
	var wg sync.WaitGroup
	var successCount int32
	var failCount int32

	for i := 0; i < concurrentRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reserved, _ := ReserveClientKeyQuota(db, key.Key)
			if reserved {
				atomic.AddInt32(&successCount, 1)
			} else {
				atomic.AddInt32(&failCount, 1)
			}
		}()
	}

	wg.Wait()

	if successCount != 1 {
		t.Fatalf("Atomic Reservation thất bại: Kỳ vọng ĐÚNG 1 request thành công, thực tế có %d thành công (fail=%d)", successCount, failCount)
	}
	if failCount != int32(concurrentRequests-1) {
		t.Fatalf("Kỳ vọng %d request bị từ chối, thực tế có %d", concurrentRequests-1, failCount)
	}

	// Gọi thêm 1 lần nữa sau khi đã hết quota
	reservedAfter, msg := ReserveClientKeyQuota(db, key.Key)
	if reservedAfter {
		t.Fatalf("Kỳ vọng bị từ chối sau khi hết quota, nhưng lại thành công")
	}
	if !strings.Contains(msg, "hết hạn mức Quota") && !strings.Contains(msg, "bị khóa") {
		t.Fatalf("Thông báo lỗi không đúng kỳ vọng: %s", msg)
	}
}