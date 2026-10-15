package omnirouter

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Không thể mở CSDL in-memory: %v", err)
	}

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
	if !strings.Contains(output, "GEMINI (5h)") {
		t.Fatalf("Output thiếu cột GEMINI (5h): %s", output)
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

	if len(resp.AvailableModels) != 15 {
		t.Fatalf("Kỳ vọng 15 available models, nhận được %d", len(resp.AvailableModels))
	}
	if len(resp.DisabledModels) != 0 {
		t.Fatalf("Kỳ vọng 0 disabled models, nhận được %d", len(resp.DisabledModels))
	}

	// 2. Tắt 1 model (gemini-2.5-flash)
	activeFalse := false
	payloadDisable, _ := json.Marshal(map[string]interface{}{
		"provider": "antigravity",
		"modelId":  "gemini-2.5-flash",
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

	if len(resp.AvailableModels) != 14 || len(resp.DisabledModels) != 1 {
		t.Fatalf("Kỳ vọng 14 available và 1 disabled, nhận được: %d / %d", len(resp.AvailableModels), len(resp.DisabledModels))
	}
	if resp.DisabledModels[0].ID != "gemini-2.5-flash" {
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

	if len(resp.AvailableModels) != 15 || len(resp.DisabledModels) != 0 {
		t.Fatalf("Sau active-all kỳ vọng 15 available và 0 disabled, nhận được: %d / %d", len(resp.AvailableModels), len(resp.DisabledModels))
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

	if len(resp.AvailableModels) != 16 {
		t.Fatalf("Sau khi thêm custom model kỳ vọng 16 available models, nhận được: %d", len(resp.AvailableModels))
	}
}

