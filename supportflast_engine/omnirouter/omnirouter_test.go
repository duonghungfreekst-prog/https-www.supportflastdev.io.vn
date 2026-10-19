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

	// 2. Tắt 1 model (gemini-3.8-flash-low)
	activeFalse := false
	payloadDisable, _ := json.Marshal(map[string]interface{}{
		"provider": "antigravity",
		"modelId":  "gemini-3.8-flash-low",
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
	if resp.DisabledModels[0].ID != "gemini-3.8-flash-low" {
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

	// 1. Tạo 2 tài khoản antigravity mẫu
	accData := `{"accessToken":"ya29.test","connectionProxyEnabled":false,"proxyUrl":""}`
	_, _ = db.Exec(`INSERT INTO omni_providers (id, provider, auth_type, name, email, priority, is_active, data, created_at, updated_at)
		VALUES ('acc_test_1', 'antigravity', 'oauth', 'Acc 1', 'acc1@gmail.com', 1, 1, ?, '2026-10-19', '2026-10-19'),
		       ('acc_test_2', 'antigravity', 'oauth', 'Acc 2', 'acc2@gmail.com', 2, 1, ?, '2026-10-19', '2026-10-19')`,
		accData, accData)

	// 2. Test GET /api/proxy-pools
	reqPools := httptest.NewRequest("GET", "/api/proxy-pools", nil)
	recPools := httptest.NewRecorder()
	HandleProxyPools(db)(recPools, reqPools)
	if recPools.Code != http.StatusOK {
		t.Fatalf("GET /api/proxy-pools thất bại: %d", recPools.Code)
	}
	var poolsResp map[string]interface{}
	_ = json.Unmarshal(recPools.Body.Bytes(), &poolsResp)
	pList, _ := poolsResp["proxyPools"].([]interface{})
	if len(pList) == 0 {
		t.Fatalf("Kỳ vọng proxyPools có danh sách")
	}

	// 3. Test POST /api/proxy-pools/bind (Gán proxy pool)
	bindPayload := `{"proxyPoolId":"42db42b6-7136-4c0c-8feb-20759c8e0946","provider":"antigravity","action":"bind"}`
	reqBind := httptest.NewRequest("POST", "/api/proxy-pools/bind", strings.NewReader(bindPayload))
	reqBind.Header.Set("Content-Type", "application/json")
	recBind := httptest.NewRecorder()
	HandleBindProxyPool(db)(recBind, reqBind)
	if recBind.Code != http.StatusOK {
		t.Fatalf("POST /api/proxy-pools/bind thất bại: %d", recBind.Code)
	}
	var bindResp map[string]interface{}
	_ = json.Unmarshal(recBind.Body.Bytes(), &bindResp)
	if count, ok := bindResp["count"].(float64); !ok || count != 2 {
		t.Fatalf("Kỳ vọng bind thành công cho 2 tài khoản, nhận được: %v", bindResp["count"])
	}

	// 4. Test GET /api/providers để kiểm tra badge proxy đã cập nhật
	reqProviders := httptest.NewRequest("GET", "/api/providers", nil)
	recProviders := httptest.NewRecorder()
	HandleListProviders(db)(recProviders, reqProviders)
	if recProviders.Code != http.StatusOK {
		t.Fatalf("GET /api/providers thất bại: %d", recProviders.Code)
	}
	var provResp map[string]interface{}
	_ = json.Unmarshal(recProviders.Body.Bytes(), &provResp)
	pArr, _ := provResp["providers"].([]interface{})
	if len(pArr) != 2 {
		t.Fatalf("Kỳ vọng 2 providers, nhận được: %d", len(pArr))
	}
	p0 := pArr[0].(map[string]interface{})
	if en, ok := p0["connectionProxyEnabled"].(bool); !ok || !en {
		t.Fatalf("Kỳ vọng connectionProxyEnabled = true sau khi bind, nhận được: %v", p0["connectionProxyEnabled"])
	}
	if pUrl, ok := p0["proxyUrl"].(string); !ok || !strings.Contains(pUrl, "58.186.63.192") {
		t.Fatalf("Kỳ vọng proxyUrl chứa 58.186.63.192, nhận được: %v", p0["proxyUrl"])
	}

	// 5. Test POST /api/proxy-pools/bind với action=unbind (chuyển về Direct)
	unbindPayload := `{"provider":"antigravity","action":"unbind"}`
	reqUnbind := httptest.NewRequest("POST", "/api/proxy-pools/bind", strings.NewReader(unbindPayload))
	reqUnbind.Header.Set("Content-Type", "application/json")
	recUnbind := httptest.NewRecorder()
	HandleBindProxyPool(db)(recUnbind, reqUnbind)
	if recUnbind.Code != http.StatusOK {
		t.Fatalf("POST /api/proxy-pools/bind unbind thất bại: %d", recUnbind.Code)
	}
	var unbindResp map[string]interface{}
	_ = json.Unmarshal(recUnbind.Body.Bytes(), &unbindResp)
	if count, ok := unbindResp["count"].(float64); !ok || count != 2 {
		t.Fatalf("Kỳ vọng unbind thành công cho 2 tài khoản, nhận được: %v", unbindResp["count"])
	}
}



