package registry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"supportflast_engine/database"
)

func TestPerformDiagnostics(t *testing.T) {
	// Khởi tạo thư mục tạm cho test
	tempDir := t.TempDir()
	dataDir := filepath.Join(tempDir, "data")
	storageDir := filepath.Join(tempDir, "storage")
	_ = os.MkdirAll(dataDir, 0755)
	_ = os.MkdirAll(storageDir, 0755)

	t.Setenv("DATA_DIR", dataDir)
	t.Setenv("STORAGE_DIR", storageDir)
	defer database.CloseDB()

	resp := PerformDiagnostics()

	if resp.Status != "success" {
		t.Fatalf("kỳ vọng resp.Status là 'success', nhận được: %s", resp.Status)
	}

	if resp.Domain != "supportflastdev.io.vn" {
		t.Errorf("kỳ vọng domain 'supportflastdev.io.vn', nhận được: %s", resp.Domain)
	}

	// 1. Kiểm tra Write Permissions
	if !resp.Diagnostics.WritePermissions.DataDir.Writable {
		t.Errorf("kỳ vọng DataDir có quyền ghi writable=true")
	}
	if !resp.Diagnostics.WritePermissions.StorageDir.Writable {
		t.Errorf("kỳ vọng StorageDir có quyền ghi writable=true")
	}

	// 2. Kiểm tra Memory
	if resp.Diagnostics.Memory.AllocBytes == 0 {
		t.Errorf("kỳ vọng AllocBytes > 0")
	}
	if resp.Diagnostics.Memory.SysBytes == 0 {
		t.Errorf("kỳ vọng SysBytes > 0")
	}

	// 3. Kiểm tra Disk
	if resp.Diagnostics.Disk.TotalBytes == 0 {
		t.Logf("Lưu ý: Không lấy được dung lượng đĩa trên môi trường kiểm thử (có thể là virtual fs)")
	}

	// 4. Kiểm tra RSA Keys
	if resp.Diagnostics.RSAKeys.Algorithm != "RS256" {
		t.Errorf("kỳ vọng thuật toán RSA là 'RS256', nhận: %s", resp.Diagnostics.RSAKeys.Algorithm)
	}

	// 5. Kiểm tra checks summary
	if resp.ChecksSummary["total"] != 6 {
		t.Errorf("kỳ vọng total checks là 6, nhận: %d", resp.ChecksSummary["total"])
	}
}

func TestSystemDiagnosticsHandler(t *testing.T) {
	defer database.CloseDB()
	req := httptest.NewRequest(http.MethodGet, "/api/system/diagnostics?nocache=1", nil)
	rr := httptest.NewRecorder()

	SystemDiagnosticsHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("kỳ vọng status 200 OK, nhận được: %d, body: %s", rr.Code, rr.Body.String())
	}

	contentType := rr.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("kỳ vọng Content-Type là application/json, nhận: %s", contentType)
	}

	var data DiagnosticsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("không thể unmarshal phản hồi diagnostics JSON: %v", err)
	}

	if data.Status != "success" {
		t.Errorf("kỳ vọng data.Status là success, nhận: %s", data.Status)
	}

	if data.Diagnostics.WritePermissions.Title == "" {
		t.Errorf("kỳ vọng WritePermissions.Title không rỗng")
	}

	if data.Diagnostics.Database.Title == "" {
		t.Errorf("kỳ vọng Database.Title không rỗng")
	}

	if data.Diagnostics.Memory.Title == "" {
		t.Errorf("kỳ vọng Memory.Title không rỗng")
	}
}

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		bytes    uint64
		expected string
	}{
		{500, "500 B"},
		{1024, "1.00 KB"},
		{1024 * 1024, "1.00 MB"},
		{1024 * 1024 * 1024, "1.00 GB"},
		{1536 * 1024 * 1024, "1.50 GB"},
	}

	for _, c := range cases {
		result := formatBytes(c.bytes)
		if result != c.expected {
			t.Errorf("formatBytes(%d) = %s, kỳ vọng %s", c.bytes, result, c.expected)
		}
	}
}

func TestIsTiDBOrMySQL(t *testing.T) {
	_ = database.CloseDB()
	defer database.CloseDB()

	// 1. Kiểm tra qua DB_DRIVER=tidb
	t.Setenv("DB_DRIVER", "tidb")
	if !isTiDBOrMySQL(nil) {
		t.Errorf("kỳ vọng isTiDBOrMySQL trả về true khi DB_DRIVER=tidb")
	}

	// 2. Kiểm tra qua DB_DRIVER=mysql
	t.Setenv("DB_DRIVER", "mysql")
	if !isTiDBOrMySQL(nil) {
		t.Errorf("kỳ vọng isTiDBOrMySQL trả về true khi DB_DRIVER=mysql")
	}

	// 3. Kiểm tra qua DB_DRIVER=sqlite
	t.Setenv("DB_DRIVER", "sqlite")
	if isTiDBOrMySQL(nil) {
		t.Errorf("kỳ vọng isTiDBOrMySQL trả về false khi DB_DRIVER=sqlite")
	}

	// 4. Kiểm tra qua TIDB_HOST khi DB_DRIVER không đặt
	t.Setenv("DB_DRIVER", "")
	t.Setenv("TIDB_HOST", "gateway01.ap-southeast-1.prod.aws.tidbcloud.com")
	if !isTiDBOrMySQL(nil) {
		t.Errorf("kỳ vọng isTiDBOrMySQL trả về true khi TIDB_HOST được cấu hình")
	}

	// 5. Kiểm tra chế độ fallback: DB_DRIVER=tidb nhưng database.ActiveDriver()="sqlite" -> isTiDBOrMySQL trả về false
	t.Setenv("DB_DRIVER", "tidb")
	database.SetDBInstance(nil, "sqlite")
	if isTiDBOrMySQL(nil) {
		t.Errorf("kỳ vọng isTiDBOrMySQL trả về false khi database.ActiveDriver() là sqlite dù DB_DRIVER=tidb (chế độ fallback)")
	}
}

func TestPerformDiagnostics_TiDB_Config(t *testing.T) {
	_ = database.CloseDB()
	defer database.CloseDB()
	tempDir := t.TempDir()
	dataDir := filepath.Join(tempDir, "data")
	storageDir := filepath.Join(tempDir, "storage")
	_ = os.MkdirAll(dataDir, 0755)
	_ = os.MkdirAll(storageDir, 0755)

	t.Setenv("DATA_DIR", dataDir)
	t.Setenv("STORAGE_DIR", storageDir)
	t.Setenv("DB_DRIVER", "tidb")
	t.Setenv("TIDB_HOST", "gateway01.ap-southeast-1.prod.aws.tidbcloud.com")
	t.Setenv("TIDB_PORT", "4000")
	t.Setenv("TIDB_DATABASE", "supportflast")

	resp := PerformDiagnostics()

	if resp.Status != "success" {
		t.Fatalf("kỳ vọng resp.Status là success, nhận: %s", resp.Status)
	}

	dbCheck := resp.Diagnostics.Database
	if dbCheck.Driver != "tidb" {
		t.Errorf("kỳ vọng dbCheck.Driver là 'tidb', nhận: %s", dbCheck.Driver)
	}

	if dbCheck.Title != "Cơ Sở Dữ Liệu TiDB Cloud (Database Engine)" {
		t.Errorf("kỳ vọng Title TiDB Cloud, nhận: %s", dbCheck.Title)
	}

	expectedPath := "gateway01.ap-southeast-1.prod.aws.tidbcloud.com:4000/supportflast"
	if dbCheck.DBPath != expectedPath {
		t.Errorf("kỳ vọng DBPath '%s', nhận: '%s'", expectedPath, dbCheck.DBPath)
	}

	if !dbCheck.WALModeActive {
		t.Errorf("kỳ vọng WALModeActive là true cho TiDB")
	}
	if !dbCheck.ForeignKeysEnabled {
		t.Errorf("kỳ vọng ForeignKeysEnabled là true cho TiDB")
	}
}
