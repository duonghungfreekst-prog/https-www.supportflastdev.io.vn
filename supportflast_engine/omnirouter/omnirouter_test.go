package omnirouter

import (
	"bytes"
	"context"
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

	"supportflast_engine/security"

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
	reqRep.Header.Set("Authorization", "Bearer "+key.Key)
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

	// 4. Test từ chối khi không gửi API key -> BẮT BUỘC 401 {"error":"Yêu cầu API key hợp lệ"}
	reqNoKey := httptest.NewRequest("GET", "/api/v1/client/config", nil)
	recNoKey := httptest.NewRecorder()
	mux.ServeHTTP(recNoKey, reqNoKey)
	if recNoKey.Code != http.StatusUnauthorized {
		t.Fatalf("Không gửi key kỳ vọng 401, nhận: %d", recNoKey.Code)
	}
	var errRespNoKey map[string]interface{}
	if err := json.Unmarshal(recNoKey.Body.Bytes(), &errRespNoKey); err != nil {
		t.Fatalf("Unmarshal error response thất bại: %v", err)
	}
	if errRespNoKey["error"] != "Yêu cầu API key hợp lệ" {
		t.Fatalf("Kỳ vọng error 'Yêu cầu API key hợp lệ', nhận: %v", errRespNoKey["error"])
	}

	// 5. Test từ chối khi gửi API key không hợp lệ -> BẮT BUỘC 401 {"error":"Yêu cầu API key hợp lệ"}
	reqBadKey := httptest.NewRequest("GET", "/api/v1/client/config?key=sk-invalid-random-key", nil)
	recBadKey := httptest.NewRecorder()
	mux.ServeHTTP(recBadKey, reqBadKey)
	if recBadKey.Code != http.StatusUnauthorized {
		t.Fatalf("Key không hợp lệ kỳ vọng 401, nhận: %d", recBadKey.Code)
	}
	var errRespBadKey map[string]interface{}
	if err := json.Unmarshal(recBadKey.Body.Bytes(), &errRespBadKey); err != nil {
		t.Fatalf("Unmarshal error response thất bại: %v", err)
	}
	if errRespBadKey["error"] != "Yêu cầu API key hợp lệ" {
		t.Fatalf("Kỳ vọng error 'Yêu cầu API key hợp lệ', nhận: %v", errRespBadKey["error"])
	}

	// 6. Test từ chối khi gửi API key đã hết hạn -> BẮT BUỘC 401 {"error":"Yêu cầu API key hợp lệ"}
	expKey, err := CreateNewClientKeyWithQuotaAndExpiry(db, "Expired-Config-Key", "supportflastdev.io.vn", 0, 0)
	if err != nil {
		t.Fatalf("Tạo expired key lỗi: %v", err)
	}
	pastTime := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	_, _ = db.Exec("UPDATE omni_client_keys SET expires_at = ? WHERE id = ?", pastTime, expKey.ID)
	RefreshKeyCache(db)

	reqExp := httptest.NewRequest("GET", "/api/v1/client/config", nil)
	reqExp.Header.Set("Authorization", "Bearer "+expKey.Key)
	recExp := httptest.NewRecorder()
	mux.ServeHTTP(recExp, reqExp)
	if recExp.Code != http.StatusUnauthorized {
		t.Fatalf("Key hết hạn kỳ vọng 401, nhận: %d", recExp.Code)
	}
	var errRespExp map[string]interface{}
	if err := json.Unmarshal(recExp.Body.Bytes(), &errRespExp); err != nil {
		t.Fatalf("Unmarshal error response thất bại: %v", err)
	}
	if errRespExp["error"] != "Yêu cầu API key hợp lệ" {
		t.Fatalf("Kỳ vọng error 'Yêu cầu API key hợp lệ', nhận: %v", errRespExp["error"])
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
	testHourlyAdminKey := "test_master_admin_key_hourly_12345"
	t.Setenv("MASTER_ADMIN_KEY", testHourlyAdminKey)
	handler := HandleManageKeys(db)
	postBody := `{"name":"Device-Hourly","quotaLimit":100,"durationHours":48}`
	req := httptest.NewRequest("POST", "/api/router/keys", strings.NewReader(postBody))
	req.Header.Set("Authorization", "Bearer "+testHourlyAdminKey)
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

	testUpgradedAdminKey := "test_master_admin_key_upgraded_12345"
	t.Setenv("MASTER_ADMIN_KEY", testUpgradedAdminKey)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.wantStatus == http.StatusOK {
				req.Header.Set("Authorization", "Bearer "+testUpgradedAdminKey)
			}
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
	reqReport.Header.Set("Authorization", "Bearer "+testKey.Key)
	recReport := httptest.NewRecorder()
	mux.ServeHTTP(recReport, reqReport)

	if recReport.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/client/report thất bại: %d - %s", recReport.Code, recReport.Body.String())
	}

	time.Sleep(50 * time.Millisecond) // chờ async ghi nhận vào SQLite/Aiven PostgreSQL

	// 5. P1 Security: Client report chỉ là telemetry/audit log, KHÔNG làm tăng used_requests của key
	var usedReqs int64
	err = db.QueryRow("SELECT used_requests FROM omni_client_keys WHERE api_key = ?", testKey.Key).Scan(&usedReqs)
	if err != nil {
		t.Fatalf("Lỗi truy vấn used_requests: %v", err)
	}
	if usedReqs != 0 {
		t.Fatalf("P1 VIOLATION: used_requests bị client report tăng (%d), kỳ vọng giữ nguyên 0!", usedReqs)
	}
}

func TestProxyConfigurationAndBinding(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	testAdminKey := "test-admin-secret-key-32-chars-long!"
	t.Setenv("MASTER_ADMIN_KEY", testAdminKey)

	// 1. Tạo 2 tài khoản antigravity mẫu
	accData := `{"accessToken":"ya29.test","connectionProxyEnabled":false,"proxyUrl":""}`
	_, _ = db.Exec(`INSERT INTO omni_providers (id, provider, auth_type, name, email, priority, is_active, data, created_at, updated_at)
		VALUES ('acc_test_1', 'antigravity', 'oauth', 'Acc 1', 'acc1@gmail.com', 1, 1, ?, '2026-10-19', '2026-10-19'),
		       ('acc_test_2', 'antigravity', 'oauth', 'Acc 2', 'acc2@gmail.com', 2, 1, ?, '2026-10-19', '2026-10-19')`,
		accData, accData)

	// 2. Test POST /api/proxy-pools: Tạo proxy pool mẫu
	createPayload := `{"id":"42db42b6-7136-4c0c-8feb-20759c8e0946","name":"VNPT Test","proxyUrl":"http://pyYXdt:QWfItD@58.186.63.192:18753/","type":"http"}`
	reqCreate := httptest.NewRequest("POST", "/api/proxy-pools", strings.NewReader(createPayload))
	reqCreate.Header.Set("Content-Type", "application/json")
	reqCreate.Header.Set("X-Admin-Key", testAdminKey)
	recCreate := httptest.NewRecorder()
	HandleProxyPools(db)(recCreate, reqCreate)
	if recCreate.Code != http.StatusOK {
		t.Fatalf("POST /api/proxy-pools tạo proxy thất bại: %d", recCreate.Code)
	}

	// 3. Test GET /api/proxy-pools
	reqPools := httptest.NewRequest("GET", "/api/proxy-pools", nil)
	reqPools.Header.Set("X-Admin-Key", testAdminKey)
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

	// 4. Test PUT /api/proxy-pools/{id}: Cập nhật proxy pool
	putPayload := `{"name":"VNPT Clean Strict","strictProxy":true}`
	reqPut := httptest.NewRequest("PUT", "/api/proxy-pools/42db42b6-7136-4c0c-8feb-20759c8e0946", strings.NewReader(putPayload))
	reqPut.Header.Set("Content-Type", "application/json")
	reqPut.Header.Set("X-Admin-Key", testAdminKey)
	recPut := httptest.NewRecorder()
	HandleProxyPools(db)(recPut, reqPut)
	if recPut.Code != http.StatusOK {
		t.Fatalf("PUT /api/proxy-pools cập nhật thất bại: %d", recPut.Code)
	}

	// 5. Test POST /api/proxy-pools/bind: Gắn proxy pool cho antigravity
	bindPayload := `{"proxyPoolId":"42db42b6-7136-4c0c-8feb-20759c8e0946","provider":"antigravity","action":"bind"}`
	reqBind := httptest.NewRequest("POST", "/api/proxy-pools/bind", strings.NewReader(bindPayload))
	reqBind.Header.Set("Content-Type", "application/json")
	reqBind.Header.Set("X-Admin-Key", testAdminKey)
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

	// 6. Test GET /api/providers: Kiểm tra badge proxy đã cập nhật (yêu cầu Admin auth)
	reqProviders := httptest.NewRequest("GET", "/api/providers", nil)
	reqProviders.Header.Set("X-Admin-Key", testAdminKey)
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

	// 7. Test DELETE /api/proxy-pools/{id}: Xóa vĩnh viễn proxy và cascade unbind
	reqDelete := httptest.NewRequest("DELETE", "/api/proxy-pools/42db42b6-7136-4c0c-8feb-20759c8e0946", nil)
	reqDelete.Header.Set("X-Admin-Key", testAdminKey)
	recDelete := httptest.NewRecorder()
	HandleProxyPools(db)(recDelete, reqDelete)
	if recDelete.Code != http.StatusOK {
		t.Fatalf("DELETE /api/proxy-pools xóa thất bại: %d", recDelete.Code)
	}

	// 8. Test GET /api/proxy-pools sau khi xóa: Phải rỗng (0 pool)
	reqPoolsAfter := httptest.NewRequest("GET", "/api/proxy-pools", nil)
	reqPoolsAfter.Header.Set("X-Admin-Key", testAdminKey)
	recPoolsAfter := httptest.NewRecorder()
	HandleProxyPools(db)(recPoolsAfter, reqPoolsAfter)
	var poolsRespAfter map[string]interface{}
	_ = json.Unmarshal(recPoolsAfter.Body.Bytes(), &poolsRespAfter)
	pListAfter, _ := poolsRespAfter["proxyPools"].([]interface{})
	if len(pListAfter) != 0 {
		t.Fatalf("Kỳ vọng danh sách proxyPools rỗng sau khi xóa, nhận được: %d", len(pListAfter))
	}

	// 9. Test GET /api/providers sau khi xóa proxy: Phải tự động cascade unbind về Direct
	reqProvAfter := httptest.NewRequest("GET", "/api/providers", nil)
	reqProvAfter.Header.Set("X-Admin-Key", testAdminKey)
	recProvAfter := httptest.NewRecorder()
	HandleListProviders(db)(recProvAfter, reqProvAfter)
	var provRespAfter map[string]interface{}
	_ = json.Unmarshal(recProvAfter.Body.Bytes(), &provRespAfter)
	pArrAfter, _ := provRespAfter["providers"].([]interface{})
	p0After := pArrAfter[0].(map[string]interface{})
	if en, ok := p0After["connectionProxyEnabled"].(bool); ok && en {
		t.Fatalf("Kỳ vọng connectionProxyEnabled = false sau khi xóa proxy pool, nhưng vẫn còn bật!")
	}
	if pUrl, _ := p0After["proxyUrl"].(string); pUrl != "" {
		t.Fatalf("Kỳ vọng proxyUrl = '' sau khi xóa proxy pool, nhận được: %s", pUrl)
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

func TestHandleClientSync_IsolateUpstreamAccessToken(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// 1. Seed tài khoản AI có upstream AccessToken và RefreshToken thật trong DB
	secretUpstreamToken := "ya29.TOP_SECRET_UPSTREAM_ACCESS_TOKEN_ABC12345"
	secretRefreshToken := "1//REFRESH_TOKEN_UPSTREAM_SECRET_67890"
	futureExpiry := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	accData := fmt.Sprintf(`{"accessToken":"%s","refreshToken":"%s","expiresAt":"%s","projectId":"proj-test-secure","quotas":{"gemini_weekly":{"total":1000,"used":50}}}`, secretUpstreamToken, secretRefreshToken, futureExpiry)

	_, err := db.Exec(`INSERT INTO omni_providers (id, provider, auth_type, name, email, priority, is_active, data, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?, ?)`, "acc_secure_test_1", "antigravity", "oauth", "Secure Account", "secure@supportflastdev.io.vn", 1, accData, NowISO(), NowISO())
	if err != nil {
		t.Fatalf("Thêm provider test thất bại: %v", err)
	}
	RefreshProviderCache(db)

	// 2. Tạo Client Key hợp lệ
	clientKey, err := CreateNewClientKey(db, "Test-Desktop-Client")
	if err != nil {
		t.Fatalf("Tạo client key thất bại: %v", err)
	}

	handler := HandleClientSync(db)

	// 3. Test Unauthorized: Không có Bearer token
	reqNoAuth := httptest.NewRequest("GET", "/api/v1/client/sync", nil)
	recNoAuth := httptest.NewRecorder()
	handler.ServeHTTP(recNoAuth, reqNoAuth)
	if recNoAuth.Code != http.StatusUnauthorized {
		t.Fatalf("Kỳ vọng 401 khi thiếu auth, thực tế: %d", recNoAuth.Code)
	}

	// 4. Test Authorized Client Sync
	req := httptest.NewRequest("GET", "/api/v1/client/sync", nil)
	req.Header.Set("Authorization", "Bearer "+clientKey.Key)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleClientSync thất bại: status=%d, body=%s", rec.Code, rec.Body.String())
	}

	rawBody := rec.Body.String()

	// P0 SECURITY VERIFICATION: TUYỆT ĐỐI KHÔNG chứa Access Token hoặc Refresh Token upstream trong raw response!
	if strings.Contains(rawBody, secretUpstreamToken) {
		t.Fatalf("LỖI BẢO MẬT NGHIÊM TRỌNG: Upstream AccessToken bị rò rỉ trong response gửi về client! body: %s", rawBody)
	}
	if strings.Contains(rawBody, secretRefreshToken) {
		t.Fatalf("LỖI BẢO MẬT NGHIÊM TRỌNG: Upstream RefreshToken bị rò rỉ trong response gửi về client! body: %s", rawBody)
	}

	var syncResp ClientSyncResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &syncResp); err != nil {
		t.Fatalf("Unmarshal ClientSyncResponse thất bại: %v", err)
	}

	if len(syncResp.Accounts) == 0 {
		t.Fatalf("Kỳ vọng có ít nhất 1 tài khoản được đồng bộ, thực tế: 0")
	}

	foundTestAcc := false
	for _, acc := range syncResp.Accounts {
		if acc.ID == "acc_secure_test_1" {
			foundTestAcc = true
			if acc.AccessToken != "" {
				t.Fatalf("LỖI BẢO MẬT: Account.AccessToken không rỗng: %q", acc.AccessToken)
			}
			if acc.RefreshToken != "" {
				t.Fatalf("LỖI BẢO MẬT: Account.RefreshToken không rỗng: %q", acc.RefreshToken)
			}
			if acc.ProjectID != "proj-test-secure" {
				t.Fatalf("Account.ProjectID không đúng: %s", acc.ProjectID)
			}
		}
	}

	if !foundTestAcc {
		t.Fatalf("Không tìm thấy tài khoản acc_secure_test_1 trong syncResp.Accounts")
	}
}

// TestRecordKeyUsageAsync_ErrorHandlingAndRetry kiểm chứng xử lý lỗi và tính bền vững của RecordKeyUsageAsync
func TestRecordKeyUsageAsync_ErrorHandlingAndRetry(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	key, err := CreateNewClientKeyWithQuotaAndExpiry(db, "Test-Usage-Retry", "supportflastdev.io.vn", 10, 24)
	if err != nil {
		t.Fatalf("Tạo key thất bại: %v", err)
	}

	// 1. Ghi nhận bình thường
	RecordKeyUsageAsync(db, key.Key, 2, 150)
	time.Sleep(50 * time.Millisecond)

	var usedReqs, totalTokens int64
	err = db.QueryRow("SELECT used_requests, total_tokens FROM omni_client_keys WHERE api_key = ?", key.Key).Scan(&usedReqs, &totalTokens)
	if err != nil {
		t.Fatalf("Truy vấn used_requests thất bại: %v", err)
	}
	if usedReqs != 2 || totalTokens != 150 {
		t.Fatalf("Kỳ vọng used_requests=2, tokens=150; thực tế: used=%d, tokens=%d", usedReqs, totalTokens)
	}

	// 2. Gọi với key rỗng -> không lỗi, không panic
	RecordKeyUsageAsync(db, "", 5, 500)

	// 3. Gọi với db = nil -> cập nhật RAM cache an toàn không panic
	RecordKeyUsageAsync(nil, key.Key, 1, 50)
	time.Sleep(20 * time.Millisecond)

	keyCacheMu.RLock()
	cachedItem := keyCache[key.Key]
	keyCacheMu.RUnlock()
	if cachedItem == nil || cachedItem.UsedRequests < 3 {
		t.Fatalf("Kỳ vọng RAM cache được cập nhật ngay cả khi db = nil")
	}

	// 4. Test với database đã đóng -> kích hoạt cơ chế retry & log lỗi mà không panic
	closedDB := setupTestDB(t)
	closedKey, err := CreateNewClientKeyWithQuotaAndExpiry(closedDB, "Test-Closed-DB", "supportflastdev.io.vn", 5, 24)
	if err != nil {
		t.Fatalf("Tạo key cho closedDB thất bại: %v", err)
	}
	_ = closedDB.Close()

	// Gọi RecordKeyUsageAsync trên DB đã đóng: phải retry nhẹ, log lỗi và không crash
	RecordKeyUsageAsync(closedDB, closedKey.Key, 1, 100)
	// Gọi RecordUsageHistoryAsync trên DB đã đóng: phải retry nhẹ, log lỗi và không crash
	RecordUsageHistoryAsync(closedDB, "Antigravity", "gemini-pro-agent", "acc-1", closedKey.Key, "/test", 10, 20, 30, 0.0, "success", 120)
	time.Sleep(80 * time.Millisecond)
}

// TestCheckClientKeyStatusConcurrency kiểm tra độ an toàn đa luồng (Thread-Safety),
// chống Data Race khi nhiều goroutines đồng thời đọc, kiểm tra trạng thái và cập nhật key trong cache.
func TestCheckClientKeyStatusConcurrency(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// 1. Tạo 3 keys: hợp lệ, chạm quota, hết hạn
	validKey, err := CreateNewClientKeyWithQuotaAndExpiry(db, "Test-Valid-Concurrent", "supportflastdev.io.vn", 1000, 24)
	if err != nil {
		t.Fatalf("Tạo valid key lỗi: %v", err)
	}

	quotaKey, err := CreateNewClientKeyWithQuotaAndExpiry(db, "Test-Quota-Concurrent", "supportflastdev.io.vn", 5, 24)
	if err != nil {
		t.Fatalf("Tạo quota key lỗi: %v", err)
	}
	// Cập nhật used_requests = 5 để chạm quota
	_, _ = db.Exec("UPDATE omni_client_keys SET used_requests = 5 WHERE id = ?", quotaKey.ID)

	pastTime := time.Now().Add(-1 * time.Hour).Format(time.RFC3339)
	expiredKey, err := CreateNewClientKeyWithQuotaAndExpiry(db, "Test-Expired-Concurrent", "supportflastdev.io.vn", 100, 0)
	if err != nil {
		t.Fatalf("Tạo expired key lỗi: %v", err)
	}
	_, _ = db.Exec("UPDATE omni_client_keys SET expires_at = ? WHERE id = ?", pastTime, expiredKey.ID)

	RefreshKeyCache(db)

	// 2. Chạy 120 goroutines đồng thời: kiểm tra, mutate cache qua Lock(), ghi nhận usage
	const numGoroutines = 120
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		idx := i
		go func() {
			defer wg.Done()
			switch idx % 4 {
			case 0:
				// Đọc key hợp lệ
				item, status, _ := CheckClientKeyStatus(validKey.Key)
				if status != "OK" || item == nil {
					t.Errorf("Kỳ vọng key hợp lệ trả về OK, nhận: %s", status)
				}
				if _, ok := ValidateClientKey(validKey.Key); !ok {
					t.Errorf("ValidateClientKey kỳ vọng true cho validKey")
				}
			case 1:
				// Kiểm tra key chạm quota -> kích hoạt lock Write để cập nhật IsActive = 0
				item, status, _ := CheckClientKeyStatus(quotaKey.Key)
				if status != "QUOTA_EXCEEDED" {
					t.Errorf("Kỳ vọng QUOTA_EXCEEDED, nhận: %s", status)
				}
				if item != nil && item.IsActive != 0 {
					t.Errorf("Kỳ vọng IsActive = 0 sau khi chạm quota")
				}
			case 2:
				// Kiểm tra key hết hạn -> kích hoạt lock Write để cập nhật IsActive = 0
				item, status, _ := CheckClientKeyStatus(expiredKey.Key)
				if status != "EXPIRED" {
					t.Errorf("Kỳ vọng EXPIRED, nhận: %s", status)
				}
				if item != nil && item.IsActive != 0 {
					t.Errorf("Kỳ vọng IsActive = 0 sau khi hết hạn")
				}
			case 3:
				// Đồng thời ghi nhận usage ngầm (cũng dùng keyCacheMu.Lock)
				RecordKeyUsageAsync(db, validKey.Key, 1, 10)
			}
		}()
	}

	wg.Wait()
	time.Sleep(50 * time.Millisecond) // Đợi async DB flush

	// 3. Xác minh trạng thái cuối cùng
	_, qStatus, _ := CheckClientKeyStatus(quotaKey.Key)
	if qStatus != "QUOTA_EXCEEDED" {
		t.Errorf("Trạng thái cuối cùng của quotaKey kỳ vọng QUOTA_EXCEEDED, nhận: %s", qStatus)
	}

	_, expStatus, _ := CheckClientKeyStatus(expiredKey.Key)
	if expStatus != "EXPIRED" {
		t.Errorf("Trạng thái cuối cùng của expiredKey kỳ vọng EXPIRED, nhận: %s", expStatus)
	}
}

func TestClientReportAuthAndSanitization(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	activeKey, err := CreateNewClientKeyWithQuotaAndExpiry(db, "Test-Report-Active", DefaultDomain, 100, 24)
	if err != nil {
		t.Fatalf("Tạo key thất bại: %v", err)
	}

	lockedKey, err := CreateNewClientKeyWithQuotaAndExpiry(db, "Test-Report-Locked", DefaultDomain, 100, 24)
	if err != nil {
		t.Fatalf("Tạo locked key thất bại: %v", err)
	}
	_, _ = db.Exec("UPDATE omni_client_keys SET is_active = 0 WHERE id = ?", lockedKey.ID)
	keyCacheMu.Lock()
	if it, ok := keyCache[lockedKey.Key]; ok {
		it.IsActive = 0
	}
	keyCacheMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/client/report", HandleClientReport(db))

	// 1. Request thiếu Header Authorization -> Phải trả về 401 Unauthorized
	{
		payload := `{"api_key":"` + activeKey.Key + `","used_requests":1,"total_tokens":100}`
		req := httptest.NewRequest("POST", "/api/v1/client/report", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Kỳ vọng 401 khi thiếu Authorization header, nhận: %d", rec.Code)
		}
	}

	// 2. Request có Header Authorization sai định dạng (không phải Bearer) -> Phải trả về 401 Unauthorized
	{
		payload := `{"used_requests":1,"total_tokens":100}`
		req := httptest.NewRequest("POST", "/api/v1/client/report", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+activeKey.Key)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Kỳ vọng 401 khi Authorization không phải Bearer, nhận: %d", rec.Code)
		}
	}

	// 3. Request với API Key không tồn tại -> Phải trả về 401 Unauthorized
	{
		payload := `{"used_requests":1,"total_tokens":100}`
		req := httptest.NewRequest("POST", "/api/v1/client/report", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer sk-supportflastdev.io.vn-nonexistent-key-12345")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Kỳ vọng 401 khi API key không tồn tại, nhận: %d", rec.Code)
		}
	}

	// 4. Request với API Key đã bị khóa (is_active = 0) -> Phải trả về 401 Unauthorized
	{
		payload := `{"used_requests":1,"total_tokens":100}`
		req := httptest.NewRequest("POST", "/api/v1/client/report", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+lockedKey.Key)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Kỳ vọng 401 khi API key đã bị khóa, nhận: %d", rec.Code)
		}
	}

	// 5. Request với report.ApiKey KHÁC với Bearer Key đã xác thực (mạo danh key khác) -> Phải trả về 401 Unauthorized
	{
		payload := `{"api_key":"sk-supportflastdev.io.vn-different-key","used_requests":1,"total_tokens":100}`
		req := httptest.NewRequest("POST", "/api/v1/client/report", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+activeKey.Key)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Kỳ vọng 401 khi report.api_key không khớp với Bearer token, nhận: %d", rec.Code)
		}
	}

	// 6. Request với used_requests âm -> Phải trả về 400 Bad Request
	{
		payload := `{"used_requests":-5,"total_tokens":100}`
		req := httptest.NewRequest("POST", "/api/v1/client/report", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+activeKey.Key)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("Kỳ vọng 400 khi used_requests âm, nhận: %d", rec.Code)
		}
	}

	// 7. Request với total_tokens âm -> Phải trả về 400 Bad Request
	{
		payload := `{"used_requests":1,"total_tokens":-100}`
		req := httptest.NewRequest("POST", "/api/v1/client/report", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+activeKey.Key)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("Kỳ vọng 400 khi total_tokens âm, nhận: %d", rec.Code)
		}
	}

	// 8. Request hợp lệ có report.ApiKey trùng khớp với Bearer key -> 200 OK và tăng usage
	{
		payload := `{"api_key":"` + activeKey.Key + `","used_requests":2,"total_tokens":500,"model":"gemini-3.8-flash-high"}`
		req := httptest.NewRequest("POST", "/api/v1/client/report", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+activeKey.Key)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("Kỳ vọng 200 cho request hợp lệ, nhận: %d - %s", rec.Code, rec.Body.String())
		}
	}

	// 9. Request hợp lệ không có report.ApiKey (bỏ trống trong JSON, chỉ có Bearer key) -> 200 OK và tăng usage
	{
		payload := `{"used_requests":3,"total_tokens":750,"model":"gemini-3.8-flash-high"}`
		req := httptest.NewRequest("POST", "/api/v1/client/report", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+activeKey.Key)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("Kỳ vọng 200 khi report.ApiKey bỏ trống nhưng có Bearer key hợp lệ, nhận: %d", rec.Code)
		}
	}

	time.Sleep(50 * time.Millisecond) // Đợi async ghi DB

	// P1 Security: Xác nhận quota Client Key không bị client report tự ý ghi đè/tăng bừa bãi
	var usedRequests int64
	err = db.QueryRow("SELECT used_requests FROM omni_client_keys WHERE api_key = ?", activeKey.Key).Scan(&usedRequests)
	if err != nil {
		t.Fatalf("Lỗi truy vấn usage trong DB: %v", err)
	}
	if usedRequests != 0 {
		t.Errorf("P1 VIOLATION: used_requests bị client report tăng (%d), kỳ vọng giữ nguyên 0!", usedRequests)
	}
}

// TestManageKeys_AdminAuthentication kiểm tra toàn diện bước xác thực Admin cho các route quản lý key:
// /api/keys, /api/router/keys, /v1/user/keys
func TestManageKeys_AdminAuthentication(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	mux := http.NewServeMux()
	RegisterOmniRouterRoutes(mux, db, false, ".")

	testMasterKey := "test-master-admin-key-2026-secret"
	os.Setenv("MASTER_ADMIN_KEY", testMasterKey)
	defer os.Unsetenv("MASTER_ADMIN_KEY")

	// Sinh Admin JWT Bearer Token hợp lệ (role: admin)
	adminJWT, err := security.GenerateRS256Token(&security.UserClaims{
		UserID:    "usr-admin-001",
		Username:  "admin",
		Role:      "admin",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("Không thể sinh admin JWT: %v", err)
	}

	// Sinh User JWT Bearer Token thông thường (role: user)
	userJWT, err := security.GenerateRS256Token(&security.UserClaims{
		UserID:    "usr-regular-002",
		Username:  "regular_user",
		Role:      "user",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("Không thể sinh user JWT: %v", err)
	}

	remoteIP := "198.51.100.55:45678"

	t.Run("GET /api/keys không có auth từ remote IP -> 401", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/keys", nil)
		req.RemoteAddr = remoteIP
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("Kỳ vọng 401, nhận: %d (body: %s)", rec.Code, rec.Body.String())
		}
		var res map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatalf("Unmarshal thất bại: %v", err)
		}
		if res["error"] != "Yêu cầu quyền quản trị viên" {
			t.Fatalf("Thông báo lỗi không đúng kỳ vọng: %v", res["error"])
		}
	})

	t.Run("POST /api/router/keys không có auth từ remote IP -> 401", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/router/keys", strings.NewReader(`{"name":"HackerKey"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remoteIP
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("Kỳ vọng 401, nhận: %d (body: %s)", rec.Code, rec.Body.String())
		}
		var res map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["error"] != "Yêu cầu quyền quản trị viên" {
			t.Fatalf("Thông báo lỗi không đúng kỳ vọng: %v", res["error"])
		}
	})

	t.Run("GET /v1/user/keys không có auth từ remote IP -> 401", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/user/keys", nil)
		req.RemoteAddr = remoteIP
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("Kỳ vọng 401, nhận: %d (body: %s)", rec.Code, rec.Body.String())
		}
		var res map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["error"] != "Yêu cầu quyền quản trị viên" {
			t.Fatalf("Thông báo lỗi không đúng kỳ vọng: %v", res["error"])
		}
	})

	t.Run("GET /api/keys với Bearer token sai -> 401", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/keys", nil)
		req.RemoteAddr = remoteIP
		req.Header.Set("Authorization", "Bearer invalid-wrong-token-abc")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("Kỳ vọng 401, nhận: %d", rec.Code)
		}
		var res map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["error"] != "Yêu cầu quyền quản trị viên" {
			t.Fatalf("Thông báo lỗi không đúng kỳ vọng: %v", res["error"])
		}
	})

	t.Run("GET /api/keys với User JWT (role user) -> 401", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/keys", nil)
		req.RemoteAddr = remoteIP
		req.Header.Set("Authorization", "Bearer "+userJWT)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("Kỳ vọng 401 với regular user, nhận: %d", rec.Code)
		}
		var res map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["error"] != "Yêu cầu quyền quản trị viên" {
			t.Fatalf("Thông báo lỗi không đúng kỳ vọng: %v", res["error"])
		}
	})

	t.Run("GET /api/keys với Admin JWT hợp lệ -> 200", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/keys", nil)
		req.RemoteAddr = remoteIP
		req.Header.Set("Authorization", "Bearer "+adminJWT)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Kỳ vọng 200 với Admin JWT, nhận: %d (body: %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("GET /api/router/keys với Master Admin Key từ env -> 200", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/router/keys", nil)
		req.RemoteAddr = remoteIP
		req.Header.Set("Authorization", "Bearer "+testMasterKey)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Kỳ vọng 200 với Master Admin Key, nhận: %d (body: %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("POST /api/router/keys với Master Domain Key mặc định cố định -> BẮT BUỘC 401 (Chặn P0)", func(t *testing.T) {
		body := `{"name":"Device-Admin-Test","quotaLimit":50}`
		req := httptest.NewRequest("POST", "/api/router/keys", strings.NewReader(body))
		req.RemoteAddr = remoteIP
		req.Header.Set("Authorization", "Bearer sk-supportflastdev.io.vn-master-live")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("P0 SECURITY: Kỳ vọng 401 từ chối Master Key mặc định cố định, nhận: %d (body: %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("GET /api/router/keys từ localhost không có auth -> BẮT BUỘC 401 (Chặn P0)", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/router/keys", nil)
		req.RemoteAddr = "127.0.0.1:54321"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("P0 SECURITY: Kỳ vọng 401 từ chối localhost không có auth, nhận: %d", rec.Code)
		}
	})

	t.Run("GET /api/router/keys với header X-Admin-Key hợp lệ -> 200", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/router/keys", nil)
		req.RemoteAddr = remoteIP
		req.Header.Set("X-Admin-Key", testMasterKey)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Kỳ vọng 200 với X-Admin-Key, nhận: %d", rec.Code)
		}
	})
}

// ==============================================================================
// TEST MULTI-PROVIDER ADAPTER (Task 8: OpenAI, Anthropic, Google, Antigravity)
// ==============================================================================

func TestDetermineProvider(t *testing.T) {
	tests := []struct {
		accProvider string
		model       string
		expected    string
	}{
		{"openai", "custom-model", "openai"},
		{"azure", "custom-model", "openai"},
		{"codex", "custom-model", "openai"},
		{"anthropic", "custom-model", "anthropic"},
		{"claude", "custom-model", "anthropic"},
		{"google", "custom-model", "google"},
		{"gemini", "custom-model", "google"},
		{"antigravity", "custom-model", "antigravity"},
		{"", "gpt-4o", "openai"},
		{"", "openai/gpt-4o-mini", "openai"},
		{"", "claude-3-5-sonnet", "anthropic"},
		{"", "anthropic/claude-3-7-sonnet", "anthropic"},
		{"", "gemini-3.8-flash-high", "google"},
		{"", "google/gemini-2.5-pro", "google"},
		{"", "unknown-model", "antigravity"},
	}

	for _, tt := range tests {
		var acc *ProviderConnection
		if tt.accProvider != "" {
			acc = &ProviderConnection{Provider: tt.accProvider}
		}
		got := DetermineProvider(acc, tt.model)
		if got != tt.expected {
			t.Errorf("DetermineProvider(acc=%s, model=%s) = %s; kỳ vọng: %s", tt.accProvider, tt.model, got, tt.expected)
		}
	}
}

func TestTargetProviderForModel(t *testing.T) {
	if got := TargetProviderForModel("gpt-4o"); got != "openai" {
		t.Errorf("TargetProviderForModel(gpt-4o) = %s; kỳ vọng openai", got)
	}
	if got := TargetProviderForModel("claude-sonnet-4-6"); got != "anthropic" {
		t.Errorf("TargetProviderForModel(claude-sonnet-4-6) = %s; kỳ vọng anthropic", got)
	}
	if got := TargetProviderForModel("gemini-3.8-flash-high"); got != "antigravity" {
		t.Errorf("TargetProviderForModel(gemini-3.8-flash-high) = %s; kỳ vọng antigravity", got)
	}
}

func TestOrderAccountsForChat(t *testing.T) {
	accounts := []ProviderConnection{
		{ID: "acc-anti", Provider: "antigravity", Name: "Antigravity 1"},
		{ID: "acc-oai", Provider: "openai", Name: "OpenAI 1"},
		{ID: "acc-anth", Provider: "anthropic", Name: "Anthropic 1"},
	}

	ordered := orderAccountsForChat(accounts, "gpt-4o", 0)
	if len(ordered) != 3 || ordered[0].Provider != "openai" {
		t.Errorf("Kỳ vọng account OpenAI lên đầu cho model gpt-4o, nhận: %s", ordered[0].Provider)
	}

	orderedClaude := orderAccountsForChat(accounts, "claude-3-5-sonnet", 0)
	if len(orderedClaude) != 3 || (orderedClaude[0].Provider != "anthropic" && orderedClaude[0].Provider != "antigravity") {
		t.Errorf("Kỳ vọng account Anthropic/Antigravity lên đầu cho Claude, nhận: %s", orderedClaude[0].Provider)
	}
}

func TestConvertMessagesToAnthropic(t *testing.T) {
	messages := []map[string]interface{}{
		{"role": "system", "content": "You are a helpful assistant."},
		{"role": "user", "content": "Hello world"},
		{"role": "assistant", "content": "Hi there!"},
		{"role": "developer", "content": "Always be concise."},
		{"role": "user", "content": "How are you?"},
	}

	sysPrompt, converted := ConvertMessagesToAnthropic(messages)
	if !strings.Contains(sysPrompt, "You are a helpful assistant.") || !strings.Contains(sysPrompt, "Always be concise.") {
		t.Errorf("System prompt chưa trích xuất đủ: %s", sysPrompt)
	}

	if len(converted) != 3 {
		t.Fatalf("Kỳ vọng 3 message sau khi tách system, nhận: %d", len(converted))
	}
	if converted[0]["role"] != "user" || converted[1]["role"] != "assistant" || converted[2]["role"] != "user" {
		t.Errorf("Thứ tự role converted không đúng: %+v", converted)
	}
}

func TestParseOpenAIChunk(t *testing.T) {
	chunkJSON := `{"id":"chatcmpl-123","choices":[{"index":0,"delta":{"content":"Hello Antigravity!"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5}}`
	text, pTok, cTok, finishReason := ParseOpenAIChunk(chunkJSON)
	if text != "Hello Antigravity!" {
		t.Errorf("ParseOpenAIChunk text = %s; kỳ vọng Hello Antigravity!", text)
	}
	if pTok != 12 || cTok != 5 {
		t.Errorf("ParseOpenAIChunk tokens: prompt=%d, compl=%d; kỳ vọng 12 và 5", pTok, cTok)
	}
	if finishReason != "stop" {
		t.Errorf("ParseOpenAIChunk finishReason = %s; kỳ vọng stop", finishReason)
	}
}

func TestParseAnthropicChunk(t *testing.T) {
	chunkDelta := `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Claude response"}}`
	text, _, _, _ := ParseAnthropicChunk(chunkDelta)
	if text != "Claude response" {
		t.Errorf("ParseAnthropicChunk delta text = %s; kỳ vọng 'Claude response'", text)
	}

	chunkStart := `{"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":42}}}`
	_, pTok, _, _ := ParseAnthropicChunk(chunkStart)
	if pTok != 42 {
		t.Errorf("ParseAnthropicChunk prompt tokens = %d; kỳ vọng 42", pTok)
	}

	chunkEnd := `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":88}}`
	_, _, cTok, finishReason := ParseAnthropicChunk(chunkEnd)
	if cTok != 88 || finishReason != "end_turn" {
		t.Errorf("ParseAnthropicChunk compl tokens=%d, finishReason=%s; kỳ vọng 88 và end_turn", cTok, finishReason)
	}
}

func TestParseNonStreamingResponse(t *testing.T) {
	oaiJSON := `{"choices":[{"message":{"role":"assistant","content":"OpenAI Full Content"}}],"usage":{"prompt_tokens":10,"completion_tokens":20}}`
	txtOai, pOai, cOai, err := ParseNonStreamingResponse("openai", []byte(oaiJSON))
	if err != nil || txtOai != "OpenAI Full Content" || pOai != 10 || cOai != 20 {
		t.Errorf("ParseNonStreamingResponse(openai) thất bại: txt=%s, p=%d, c=%d, err=%v", txtOai, pOai, cOai, err)
	}

	anthJSON := `{"content":[{"type":"text","text":"Anthropic Full Content"}],"usage":{"input_tokens":15,"output_tokens":35}}`
	txtAnth, pAnth, cAnth, err := ParseNonStreamingResponse("anthropic", []byte(anthJSON))
	if err != nil || txtAnth != "Anthropic Full Content" || pAnth != 15 || cAnth != 35 {
		t.Errorf("ParseNonStreamingResponse(anthropic) thất bại: txt=%s, p=%d, c=%d, err=%v", txtAnth, pAnth, cAnth, err)
	}

	geminiSSE := "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Gemini \"}]}}],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":3}}}\n\ndata: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Stream\"}]}}],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":6}}}\n\n"
	txtGem, pGem, cGem, err := ParseNonStreamingResponse("antigravity", []byte(geminiSSE))
	if err != nil || txtGem != "Gemini Stream" || pGem != 5 || cGem != 6 {
		t.Errorf("ParseNonStreamingResponse(gemini SSE) thất bại: txt=%s, p=%d, c=%d, err=%v", txtGem, pGem, cGem, err)
	}
}

func TestBuildOutboundRequestMultiProvider(t *testing.T) {
	ctx := context.Background()
	req := ChatCompletionRequest{
		Model: "test-model",
		Messages: []map[string]interface{}{
			{"role": "user", "content": "Hello test"},
		},
	}

	// 1. Test OpenAI
	oaiCreds := &Credentials{AccessToken: "sk-openai-test-token"}
	httpReqOai, err := BuildOutboundRequest(ctx, "openai", "gpt-4o", oaiCreds, req, true)
	if err != nil {
		t.Fatalf("BuildOutboundRequest(openai) lỗi: %v", err)
	}
	if httpReqOai.URL.String() != "https://api.openai.com/v1/chat/completions" {
		t.Errorf("OpenAI URL sai: %s", httpReqOai.URL.String())
	}
	if httpReqOai.Header.Get("Authorization") != "Bearer sk-openai-test-token" {
		t.Errorf("OpenAI Authorization header sai: %s", httpReqOai.Header.Get("Authorization"))
	}

	// 2. Test Anthropic
	anthCreds := &Credentials{AccessToken: "sk-ant-test-token"}
	httpReqAnth, err := BuildOutboundRequest(ctx, "anthropic", "claude-3-7-sonnet", anthCreds, req, true)
	if err != nil {
		t.Fatalf("BuildOutboundRequest(anthropic) lỗi: %v", err)
	}
	if httpReqAnth.URL.String() != "https://api.anthropic.com/v1/messages" {
		t.Errorf("Anthropic URL sai: %s", httpReqAnth.URL.String())
	}
	if httpReqAnth.Header.Get("x-api-key") != "sk-ant-test-token" {
		t.Errorf("Anthropic x-api-key sai: %s", httpReqAnth.Header.Get("x-api-key"))
	}
	if httpReqAnth.Header.Get("anthropic-version") != "2023-06-01" {
		t.Errorf("Anthropic version header sai: %s", httpReqAnth.Header.Get("anthropic-version"))
	}

	// 3. Test Google AI Studio (API Key)
	googleKeyCreds := &Credentials{AccessToken: "AIzaSyTestApiKeyGoogle123"}
	httpReqGoogleKey, err := BuildOutboundRequest(ctx, "google", "gemini-2.5-flash", googleKeyCreds, req, true)
	if err != nil {
		t.Fatalf("BuildOutboundRequest(google key) lỗi: %v", err)
	}
	if !strings.Contains(httpReqGoogleKey.URL.String(), "generativelanguage.googleapis.com") || !strings.Contains(httpReqGoogleKey.URL.String(), "AIzaSyTestApiKeyGoogle123") {
		t.Errorf("Google AI Studio URL sai: %s", httpReqGoogleKey.URL.String())
	}

	// 4. Test Antigravity / Google Cloud Code
	antiCreds := &Credentials{AccessToken: "ya29.google-oauth-token", ProjectId: "test-proj-123"}
	httpReqAnti, err := BuildOutboundRequest(ctx, "antigravity", "gemini-3.8-flash-high", antiCreds, req, true)
	if err != nil {
		t.Fatalf("BuildOutboundRequest(antigravity) lỗi: %v", err)
	}
	if httpReqAnti.URL.String() != "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse" {
		t.Errorf("Antigravity URL sai: %s", httpReqAnti.URL.String())
	}
	if httpReqAnti.Header.Get("Authorization") != "Bearer ya29.google-oauth-token" {
		t.Errorf("Antigravity Authorization header sai: %s", httpReqAnti.Header.Get("Authorization"))
	}
	if !strings.Contains(httpReqAnti.Header.Get("User-Agent"), "antigravity") {
		t.Errorf("Antigravity User-Agent sai: %s", httpReqAnti.Header.Get("User-Agent"))
	}
}

// TestHandleClientSync_RealQuotaCalculation_NoHardcodedFallback kiểm tra loại bỏ hoàn toàn hạn mức cố định 100000
// và tính toán QuotaRemaining thực tế của Client Key từ omni_client_keys, đồng thời phân biệt rõ với Quota Upstream.
func TestHandleClientSync_RealQuotaCalculation_NoHardcodedFallback(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// 1. Thêm 1 Provider có hạn mức Quota cụ thể trong DB (gemini_weekly: total=5000, used=1200 -> remaining=3800)
	futureExpiry := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	accData := fmt.Sprintf(`{"accessToken":"ya29.test_token_upstream","refreshToken":"1//test_refresh","expiresAt":"%s","projectId":"proj-test-sync","quotas":{"gemini_weekly":{"total":5000,"used":1200}},"lastQuotaAt":"2026-10-21T07:00:00Z"}`, futureExpiry)
	_, err := db.Exec(`INSERT INTO omni_providers (id, provider, auth_type, name, email, priority, is_active, data, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?, ?)`, "acc_prov_sync_test", "antigravity", "oauth", "Test Upstream Provider", "provider@supportflastdev.io.vn", 1, accData, NowISO(), NowISO())
	if err != nil {
		t.Fatalf("Thêm provider test thất bại: %v", err)
	}
	RefreshProviderCache(db)

	// 2. Tạo Client Key có hạn mức Quota rõ ràng: QuotaLimit = 1000, UsedRequests = 250 -> Còn lại 750
	clientKey, err := CreateNewClientKeyWithQuotaAndExpiry(db, "Test-Device-Specific-Quota", "supportflastdev.io.vn", 1000, 24)
	if err != nil {
		t.Fatalf("Tạo Client Key thất bại: %v", err)
	}

	// Cập nhật used_requests = 250 trong omni_client_keys
	_, err = db.Exec("UPDATE omni_client_keys SET used_requests = 250, total_tokens = 5000 WHERE id = ?", clientKey.ID)
	if err != nil {
		t.Fatalf("Cập nhật used_requests thất bại: %v", err)
	}
	RefreshKeyCache(db)

	handler := HandleClientSync(db)

	// 3. Gửi request Client Sync
	req := httptest.NewRequest("GET", "/api/v1/client/sync", nil)
	req.Header.Set("Authorization", "Bearer "+clientKey.Key)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleClientSync thất bại: status=%d, body=%s", rec.Code, rec.Body.String())
	}

	var syncResp ClientSyncResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &syncResp); err != nil {
		t.Fatalf("Unmarshal ClientSyncResponse thất bại: %v", err)
	}

	// 4. KIỂM CHỨNG: Quota của Client Key PHẢI là 750 (1000 - 250), TUYỆT ĐỐI KHÔNG PHẢI 100000
	if syncResp.QuotaRemaining == 100000 {
		t.Fatalf("LỖI NGHIÊM TRỌNG: QuotaRemaining của Client Key vẫn bị gán cứng là 100000!")
	}
	if syncResp.QuotaRemaining != 750 {
		t.Errorf("Kỳ vọng QuotaRemaining = 750 (1000 - 250), nhận được: %d", syncResp.QuotaRemaining)
	}
	if syncResp.QuotaLimit != 1000 {
		t.Errorf("Kỳ vọng QuotaLimit = 1000, nhận được: %d", syncResp.QuotaLimit)
	}
	if syncResp.UsedRequests != 250 {
		t.Errorf("Kỳ vọng UsedRequests = 250, nhận được: %d", syncResp.UsedRequests)
	}
	if syncResp.TotalTokens != 5000 {
		t.Errorf("Kỳ vọng TotalTokens = 5000, nhận được: %d", syncResp.TotalTokens)
	}
	if syncResp.LastQuotaAt == "" {
		t.Errorf("Kỳ vọng LastQuotaAt không rỗng cho Client Key")
	}
	if syncResp.UpdatedAt == "" {
		t.Errorf("Kỳ vọng UpdatedAt không rỗng cho Client Key")
	}

	// Kiểm tra ClientQuota struct
	if syncResp.ClientQuota == nil {
		t.Fatalf("Kỳ vọng ClientQuota struct không nil")
	}
	if syncResp.ClientQuota.QuotaRemaining != 750 {
		t.Errorf("Kỳ vọng ClientQuota.QuotaRemaining = 750, nhận được: %d", syncResp.ClientQuota.QuotaRemaining)
	}
	if syncResp.ClientQuota.QuotaLimit != 1000 {
		t.Errorf("Kỳ vọng ClientQuota.QuotaLimit = 1000, nhận được: %d", syncResp.ClientQuota.QuotaLimit)
	}
	if syncResp.ClientQuota.IsUnlimited {
		t.Errorf("Kỳ vọng ClientQuota.IsUnlimited = false khi QuotaLimit > 0")
	}

	// 5. KIỂM CHỨNG: Quota của Provider Upstream PHẢI phân biệt rõ ràng với Quota của Client Key
	foundProv := false
	for _, acc := range syncResp.Accounts {
		if acc.ID == "acc_prov_sync_test" {
			foundProv = true
			if acc.QuotaRemaining == 100000 {
				t.Fatalf("LỖI: Upstream Account QuotaRemaining bị gán cứng 100000!")
			}
			if acc.QuotaRemaining != 3800 {
				t.Errorf("Kỳ vọng Account QuotaRemaining = 3800 (5000 - 1200), nhận được: %d", acc.QuotaRemaining)
			}
			if acc.LastQuotaAt == "" {
				t.Errorf("Kỳ vọng Account LastQuotaAt không rỗng")
			}
			if acc.UpdatedAt == "" {
				t.Errorf("Kỳ vọng Account UpdatedAt không rỗng")
			}
		}
	}
	if !foundProv {
		t.Fatalf("Không tìm thấy account acc_prov_sync_test trong syncResp.Accounts")
	}

	// 6. KIỂM CHỨNG Key Vô Hạn (QuotaLimit = 0): QuotaRemaining trả về -1 (không phải 100000)
	unlimitedKey, err := CreateNewClientKeyWithQuotaAndExpiry(db, "Test-Device-Unlimited", "supportflastdev.io.vn", 0, 0)
	if err != nil {
		t.Fatalf("Tạo unlimited key thất bại: %v", err)
	}
	reqUnlim := httptest.NewRequest("GET", "/api/v1/client/sync", nil)
	reqUnlim.Header.Set("Authorization", "Bearer "+unlimitedKey.Key)
	recUnlim := httptest.NewRecorder()
	handler.ServeHTTP(recUnlim, reqUnlim)

	var unlimResp ClientSyncResponse
	if err := json.Unmarshal(recUnlim.Body.Bytes(), &unlimResp); err != nil {
		t.Fatalf("Unmarshal unlimResp thất bại: %v", err)
	}
	if unlimResp.QuotaRemaining == 100000 {
		t.Fatalf("LỖI: Unlimited key QuotaRemaining bị gán cứng là 100000!")
	}
	if unlimResp.QuotaRemaining != -1 {
		t.Errorf("Kỳ vọng Unlimited key QuotaRemaining = -1, nhận được: %d", unlimResp.QuotaRemaining)
	}
	if unlimResp.ClientQuota == nil || !unlimResp.ClientQuota.IsUnlimited {
		t.Errorf("Kỳ vọng ClientQuota.IsUnlimited = true cho key không giới hạn")
	}
}

// TestRouterAdminSecurityAndAuthentication kiểm tra bảo vệ tuyệt đối tất cả API quản trị Router
// Chống việc người dùng không đăng nhập truy cập vào xem danh sách tài khoản, quota, cài đặt (P0)
func TestRouterAdminSecurityAndAuthentication(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	testAdminSecret := "super-secure-admin-secret-key-32b-length!"
	t.Setenv("MASTER_ADMIN_KEY", testAdminSecret)

	// Tạo JWT Admin hợp lệ (RS256)
	adminToken, err := security.GenerateRS256Token(&security.UserClaims{
		UserID:   "usr-admin-001",
		Username: "admin",
		Role:     "admin",
	})
	if err != nil {
		t.Fatalf("Không thể sinh token Admin RS256: %v", err)
	}

	// 1. GET /api/providers không có auth -> BẮT BUỘC HTTP 401
	t.Run("GET /api/providers không có auth -> 401", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/providers", nil)
		rec := httptest.NewRecorder()
		HandleListProviders(db)(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("Kỳ vọng HTTP 401 Unauthorized khi không đăng nhập, nhận được: %d", rec.Code)
		}
	})

	// 2. GET /api/router/providers không có auth -> BẮT BUỘC HTTP 401
	t.Run("GET /api/router/providers không có auth -> 401", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/router/providers", nil)
		rec := httptest.NewRecorder()
		HandleListProviders(db)(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("Kỳ vọng HTTP 401 Unauthorized khi không đăng nhập, nhận được: %d", rec.Code)
		}
	})

	// 3. GET /api/settings không có auth -> BẮT BUỘC HTTP 401
	t.Run("GET /api/settings không có auth -> 401", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/settings", nil)
		rec := httptest.NewRecorder()
		HandleSettings(db)(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("Kỳ vọng HTTP 401 cho Settings, nhận được: %d", rec.Code)
		}
	})

	// 4. GET /api/usage/request-logs không có auth -> BẮT BUỘC HTTP 401
	t.Run("GET /api/usage/request-logs không có auth -> 401", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/usage/request-logs", nil)
		rec := httptest.NewRecorder()
		HandleRequestDetails(db)(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("Kỳ vọng HTTP 401 cho RequestDetails, nhận được: %d", rec.Code)
		}
	})

	// 5. GET /api/proxy-pools không có auth -> BẮT BUỘC HTTP 401
	t.Run("GET /api/proxy-pools không có auth -> 401", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/proxy-pools", nil)
		rec := httptest.NewRecorder()
		HandleProxyPools(db)(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("Kỳ vọng HTTP 401 cho ProxyPools, nhận được: %d", rec.Code)
		}
	})

	// 6. GET /api/router/providers với Master Admin Key -> HTTP 200 OK
	t.Run("GET /api/router/providers với Master Admin Key -> 200", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/router/providers", nil)
		req.Header.Set("X-Admin-Key", testAdminSecret)
		rec := httptest.NewRecorder()
		HandleListProviders(db)(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("Kỳ vọng HTTP 200 với Master Admin Key, nhận được: %d", rec.Code)
		}
	})

	// 7. GET /api/router/providers với Admin JWT RS256 Bearer Token -> HTTP 200 OK
	t.Run("GET /api/router/providers với Admin JWT RS256 -> 200", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/router/providers", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()
		HandleListProviders(db)(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("Kỳ vọng HTTP 200 với Admin JWT, nhận được: %d", rec.Code)
		}
	})

	// 8. GET /api/providers với Cookie cloudpool_token của Admin -> HTTP 200 OK (Tích hợp Web Browser SSO)
	t.Run("GET /api/providers với Cookie cloudpool_token -> 200", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/providers", nil)
		req.AddCookie(&http.Cookie{
			Name:  "cloudpool_token",
			Value: adminToken,
		})
		rec := httptest.NewRecorder()
		HandleListProviders(db)(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("Kỳ vọng HTTP 200 với Cookie cloudpool_token của Admin, nhận được: %d", rec.Code)
		}
	})
}

// TestOAuthAndProviderManagement kiểm thử toàn diện luồng OAuth 2.0 PKCE và thêm Provider qua API Key
func TestOAuthAndProviderManagement(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Không thể mở CSDL in-memory: %v", err)
	}
	defer db.Close()

	if err := InitStorage(db, false); err != nil {
		t.Fatalf("InitStorage thất bại: %v", err)
	}

	// 1. GET /api/oauth/antigravity/authorize
	t.Run("GET /api/oauth/antigravity/authorize -> Sinh authUrl hợp lệ", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/oauth/antigravity/authorize?redirect_uri=http://localhost:20128/callback", nil)
		rec := httptest.NewRecorder()
		HandleOAuth(db)(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("Kỳ vọng 200, nhận được: %d", rec.Code)
		}
		var res map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		authURL, _ := res["authUrl"].(string)
		if !strings.Contains(authURL, "accounts.google.com") || !strings.Contains(authURL, AntigravityClientID) {
			t.Fatalf("authUrl không chứa Google OAuth URL hoặc AntigravityClientID: %s", authURL)
		}
		if res["provider"] != "antigravity" {
			t.Fatalf("Kỳ vọng provider = antigravity, nhận được: %v", res["provider"])
		}
	})

	// 2. GET /api/oauth/codex/authorize -> Báo requiresApiKey
	t.Run("GET /api/oauth/codex/authorize -> requiresApiKey", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/oauth/codex/authorize", nil)
		rec := httptest.NewRecorder()
		HandleOAuth(db)(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("Kỳ vọng 200, nhận được: %d", rec.Code)
		}
		var res map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["requiresApiKey"] != true {
			t.Fatalf("Kỳ vọng requiresApiKey = true, nhận được: %v", res["requiresApiKey"])
		}
	})

	// 3. GET /callback -> Trả về HTML callback có postMessage / BroadcastChannel
	t.Run("GET /callback -> Trả về HTML", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/callback?code=test-code-123&state=xyz", nil)
		rec := httptest.NewRecorder()
		HandleOAuthCallbackHTML(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("Kỳ vọng 200, nhận được: %d", rec.Code)
		}
		bodyStr := rec.Body.String()
		if !strings.Contains(bodyStr, "oauth_callback") || !strings.Contains(bodyStr, "postMessage") {
			t.Fatalf("HTML thiếu logic callback postMessage: %s", bodyStr)
		}
	})

	// 4. POST /api/providers với API Key -> Tạo thành công HTTP 201
	t.Run("POST /api/providers -> Tạo kết nối thành công 201", func(t *testing.T) {
		payload := map[string]interface{}{
			"provider": "openai",
			"name":     "OpenAI Primary Key",
			"apiKey":   "sk-test-live-key-12345",
			"priority": 1,
			"isActive": true,
		}
		bodyBytes, _ := json.Marshal(payload)
		req := httptest.NewRequest("POST", "/api/providers", bytes.NewReader(bodyBytes))
		req.Header.Set("x-9r-cli-token", "6929c9313fb35a0b")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		HandleListProviders(db)(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("Kỳ vọng HTTP 201 Created, nhận được: %d, body: %s", rec.Code, rec.Body.String())
		}
		var res map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["success"] != true {
			t.Fatalf("Kỳ vọng success = true, nhận được: %v", res["success"])
		}

		// Xác minh lưu vào DB
		all, err := GetAllProviders(db)
		if err != nil || len(all) == 0 {
			t.Fatalf("Không tìm thấy provider đã lưu trong DB: %v", err)
		}
		if all[0].Name != "OpenAI Primary Key" || all[0].Provider != "openai" {
			t.Fatalf("Dữ liệu provider trong DB không khớp: %+v", all[0])
		}
	})
}

// TestSecureCORSOAuth kiểm tra triệt để tính an toàn của CORS cho các endpoint OAuth (Rule PHẦN 3)
func TestSecureCORSOAuth(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Không thể mở in-memory sqlite db: %v", err)
	}
	defer db.Close()
	_ = InitStorage(db, false)

	// 1. Allowed origin: https://supportflastdev.io.vn
	t.Run("Allowed Origin -> CORS headers set", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/api/oauth/antigravity/authorize", nil)
		req.Header.Set("Origin", "https://supportflastdev.io.vn")
		rec := httptest.NewRecorder()
		HandleOAuth(db)(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("OPTIONS kỳ vọng 200, nhận: %d", rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://supportflastdev.io.vn" {
			t.Fatalf("Kỳ vọng Access-Control-Allow-Origin = https://supportflastdev.io.vn, nhận: %s", got)
		}
		if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
			t.Fatalf("Kỳ vọng Access-Control-Allow-Credentials = true, nhận: %s", got)
		}
	})

	// 2. Allowed origin: http://127.0.0.1:20128
	t.Run("Local Gateway Origin -> CORS headers set", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/oauth/poll?state=test", nil)
		req.Header.Set("Origin", "http://127.0.0.1:20128")
		rec := httptest.NewRecorder()
		HandleOAuthPoll(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:20128" {
			t.Fatalf("Kỳ vọng Access-Control-Allow-Origin = http://127.0.0.1:20128, nhận: %s", got)
		}
		if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
			t.Fatalf("Kỳ vọng Access-Control-Allow-Credentials = true, nhận: %s", got)
		}
	})

	// 3. ALLOWED_ORIGINS env
	t.Run("Custom ALLOWED_ORIGINS env -> Exact match allowed", func(t *testing.T) {
		origEnv := os.Getenv("ALLOWED_ORIGINS")
		defer os.Setenv("ALLOWED_ORIGINS", origEnv)
		os.Setenv("ALLOWED_ORIGINS", "https://partner.supportflastdev.io.vn,https://preview.app")

		req := httptest.NewRequest("POST", "/api/oauth/antigravity/exchange", nil)
		req.Header.Set("Origin", "https://partner.supportflastdev.io.vn")
		rec := httptest.NewRecorder()
		HandleOAuthExchange(db)(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://partner.supportflastdev.io.vn" {
			t.Fatalf("Kỳ vọng Access-Control-Allow-Origin = https://partner.supportflastdev.io.vn, nhận: %s", got)
		}
	})

	// 4. Malicious / untrusted Origin -> CẤM TUYỆT ĐỐI (không set Allow-Origin)
	t.Run("Untrusted Origin -> No CORS header", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/api/oauth/antigravity/authorize", nil)
		req.Header.Set("Origin", "https://evil-attacker.com")
		rec := httptest.NewRecorder()
		HandleOAuth(db)(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("LỖI BẢO MẬT: Origin độc hại nhận được Access-Control-Allow-Origin: %s", got)
		}
	})

	// 5. Wildcard Origin '*' -> CẤM TUYỆT ĐỐI (không set Allow-Origin)
	t.Run("Wildcard Origin '*' -> Rejected", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/callback?code=abc&state=123", nil)
		req.Header.Set("Origin", "*")
		rec := httptest.NewRecorder()
		HandleOAuthCallbackHTML(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("LỖI BẢO MẬT: Wildcard origin '*' nhận được Access-Control-Allow-Origin: %s", got)
		}
	})

	// 6. No Origin header (same-origin / server-to-server) -> No Allow-Origin header
	t.Run("No Origin header -> No Allow-Origin header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/oauth/poll?state=none", nil)
		rec := httptest.NewRecorder()
		HandleOAuthPoll(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("Kỳ vọng rỗng khi không có Origin header, nhận được: %s", got)
		}
	})
}


// TestServerLinkAuthentication kiểm tra liên kết giữa Desktop App, Antigravity IDE và Server Chủ
func TestServerLinkAuthentication(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	mux := http.NewServeMux()
	RegisterOmniRouterRoutes(mux, db, false, ".")

	// 1. GET /api/router/providers với sk-supportflastdev.io.vn-master-live -> HTTP 200 OK
	t.Run("GET /api/router/providers với master domain key -> 200", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/router/providers", nil)
		req.Header.Set("Authorization", "Bearer sk-supportflastdev.io.vn-master-live")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("Kỳ vọng HTTP 200 cho master domain key, nhận: %d (body: %s)", rec.Code, rec.Body.String())
		}
	})

	// 2. GET /api/router/providers với sk-9r-master-live-gateway-secret -> HTTP 200 OK
	t.Run("GET /api/router/providers với gateway secret key -> 200", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/router/providers", nil)
		req.Header.Set("Authorization", "Bearer sk-9r-master-live-gateway-secret")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("Kỳ vọng HTTP 200 cho gateway secret key, nhận: %d", rec.Code)
		}
	})

	// 3. GET /api/router/providers với Client Key sk-9r-... do web cấp -> HTTP 200 OK
	t.Run("GET /api/router/providers với client key hợp lệ -> 200", func(t *testing.T) {
		clientKey, err := CreateNewClientKey(db, "Test-Desktop-User")
		if err != nil {
			t.Fatalf("CreateNewClientKey thất bại: %v", err)
		}
		req := httptest.NewRequest("GET", "/api/router/providers", nil)
		req.Header.Set("Authorization", "Bearer " + clientKey.Key)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("Kỳ vọng HTTP 200 cho client key hợp lệ, nhận: %d (body: %s)", rec.Code, rec.Body.String())
		}
	})

	// 4. GET /api/usage/stats (Endpoint kiểm tra kết nối từ Desktop App) -> HTTP 200 OK
	t.Run("GET /api/usage/stats -> 200", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/usage/stats", nil)
		req.Header.Set("Authorization", "Bearer sk-supportflastdev.io.vn-master-live")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("Kỳ vọng HTTP 200 cho /api/usage/stats, nhận: %d", rec.Code)
		}
	})

	// 5. GET /api/quotas với x-api-key -> HTTP 200 OK
	t.Run("GET /api/quotas với x-api-key -> 200", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/quotas", nil)
		req.Header.Set("x-api-key", "sk-supportflastdev.io.vn-master-live")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("Kỳ vọng HTTP 200 cho /api/quotas, nhận: %d", rec.Code)
		}
	})
}
