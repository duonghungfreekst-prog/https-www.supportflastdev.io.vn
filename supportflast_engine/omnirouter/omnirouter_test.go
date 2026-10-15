package omnirouter

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
