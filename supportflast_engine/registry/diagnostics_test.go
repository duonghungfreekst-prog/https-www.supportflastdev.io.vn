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
