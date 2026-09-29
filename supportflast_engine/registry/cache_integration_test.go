package registry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"supportflast_engine/cache"
	"testing"
	"time"
)

// TestListHandlerCache kiểm tra cơ chế Cache MISS lần đầu, Cache HIT lần tiếp theo trên /api/apps
func TestListHandlerCache(t *testing.T) {
	// Xóa sạch cache trước khi test
	cache.DefaultCache.Clear()

	// 1. Lần gọi đầu tiên: Phải là Cache MISS
	req1 := httptest.NewRequest(http.MethodGet, "/api/apps", nil)
	rec1 := httptest.NewRecorder()
	ListHandler(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("Mong đợi mã HTTP 200, nhận được: %d", rec1.Code)
	}

	xCache1 := rec1.Header().Get("X-Cache")
	if xCache1 != "MISS" {
		t.Fatalf("Lần gọi đầu tiên phải có header X-Cache là MISS, nhận được: %s", xCache1)
	}

	var resp1 map[string]interface{}
	if err := json.Unmarshal(rec1.Body.Bytes(), &resp1); err != nil {
		t.Fatalf("Không thể parse JSON từ response 1: %v", err)
	}
	if resp1["status"] != "success" {
		t.Fatalf("Status phải là success, nhận được: %v", resp1["status"])
	}

	// 2. Lần gọi thứ hai: Phải là Cache HIT từ bộ nhớ RAM
	req2 := httptest.NewRequest(http.MethodGet, "/api/apps", nil)
	rec2 := httptest.NewRecorder()
	ListHandler(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("Mong đợi mã HTTP 200, nhận được: %d", rec2.Code)
	}

	xCache2 := rec2.Header().Get("X-Cache")
	if xCache2 != "HIT" {
		t.Fatalf("Lần gọi thứ hai phải có header X-Cache là HIT, nhận được: %s", xCache2)
	}

	var resp2 map[string]interface{}
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("Không thể parse JSON từ response 2: %v", err)
	}
	if resp2["status"] != "success" {
		t.Fatalf("Status lần 2 phải là success")
	}
}

// TestPublishInvalidatesCache kiểm tra việc tự động Invalidate cache khi có app mới
func TestPublishInvalidatesCache(t *testing.T) {
	cache.DefaultCache.Clear()

	// 1. Gọi ListHandler để nạp cache
	req1 := httptest.NewRequest(http.MethodGet, "/api/apps", nil)
	rec1 := httptest.NewRecorder()
	ListHandler(rec1, req1)
	if rec1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("Mong đợi MISS lần 1")
	}

	// 2. Kiểm tra cache đã được lưu
	if _, ok := cache.DefaultCache.Get(CacheKeyAppsList); !ok {
		t.Fatalf("CacheKeyAppsList phải tồn tại trong cache")
	}

	// 3. Kích hoạt Invalidate (tương tự như khi PublishHandler hoàn tất xuất bản)
	InvalidateAppsCache()

	// 4. Kiểm tra cache đã bị xóa
	if _, ok := cache.DefaultCache.Get(CacheKeyAppsList); ok {
		t.Fatalf("CacheKeyAppsList lẽ ra phải bị xóa sau khi Invalidate")
	}

	// 5. Lần gọi tiếp theo phải là Cache MISS để làm mới dữ liệu
	req3 := httptest.NewRequest(http.MethodGet, "/api/apps", nil)
	rec3 := httptest.NewRecorder()
	ListHandler(rec3, req3)

	if rec3.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("Mong đợi X-Cache là MISS sau khi cache bị invalidate")
	}
}

// TestSystemStatusHandlerCache kiểm tra cache cho /api/system/status và invalidate khi cập nhật
func TestSystemStatusHandlerCache(t *testing.T) {
	cache.DefaultCache.Clear()

	// 1. Lần gọi đầu tiên: Cache MISS
	req1 := httptest.NewRequest(http.MethodGet, "/api/system/status", nil)
	rec1 := httptest.NewRecorder()
	SystemStatusHandler(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("Mong đợi HTTP 200, nhận được: %d", rec1.Code)
	}
	if rec1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("Mong đợi X-Cache MISS ở lần gọi đầu")
	}

	// 2. Lần gọi thứ hai: Cache HIT
	req2 := httptest.NewRequest(http.MethodGet, "/api/system/status", nil)
	rec2 := httptest.NewRecorder()
	SystemStatusHandler(rec2, req2)

	if rec2.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("Mong đợi X-Cache HIT ở lần gọi thứ hai")
	}

	// 3. Invalidate cache hệ thống
	InvalidateSystemStatusCache()

	// 4. Lần gọi thứ ba: Phải là Cache MISS
	req3 := httptest.NewRequest(http.MethodGet, "/api/system/status", nil)
	rec3 := httptest.NewRecorder()
	SystemStatusHandler(rec3, req3)

	if rec3.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("Mong đợi X-Cache MISS sau khi Invalidate")
	}
}

// TestCacheTTLExpirationInHandlers kiểm tra cache hết hạn tự động sau khoảng thời gian TTL
func TestCacheTTLExpirationInHandlers(t *testing.T) {
	testCache := cache.NewLRUCache(10, 0)
	defer testCache.Close()

	// Đặt dữ liệu với TTL ngắn 50ms
	testCache.Set("test_endpoint", []byte(`{"data":"ok"}`), 50*time.Millisecond)

	// Lấy ngay: thành công
	if _, ok := testCache.Get("test_endpoint"); !ok {
		t.Fatalf("Dữ liệu phải có sẵn trước khi hết hạn")
	}

	// Chờ hết hạn
	time.Sleep(80 * time.Millisecond)

	// Lấy lại: phải hết hạn (evicted)
	if _, ok := testCache.Get("test_endpoint"); ok {
		t.Fatalf("Dữ liệu phải bị loại bỏ sau khi hết hạn")
	}
}
