package registry

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"supportflast_engine/database"
)

func setupTestDB(t *testing.T) func() {
	t.Helper()

	testDir := filepath.Join(`f:\supportflast.dev\data`, "test_reviews_db")
	_ = os.MkdirAll(testDir, 0755)
	testDBPath := filepath.Join(testDir, "test_"+t.Name()+".db")
	_ = os.Remove(testDBPath)

	_ = database.CloseDB()
	_, err := database.InitDB(testDBPath)
	if err != nil {
		t.Fatalf("Không thể khởi tạo test database: %v", err)
	}

	return func() {
		_ = database.CloseDB()
		_ = os.Remove(testDBPath)
		_ = os.Remove(testDBPath + "-wal")
		_ = os.Remove(testDBPath + "-shm")
		_ = os.RemoveAll(testDir)
	}
}

func TestReviews_EmptyList(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/reviews", nil)
	rr := httptest.NewRecorder()

	ReviewsHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Kỳ vọng HTTP 200, nhận: %d", rr.Code)
	}

	var resp ReviewsResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("Lỗi decode JSON: %v", err)
	}

	if resp.TotalReviews != 0 {
		t.Errorf("Kỳ vọng TotalReviews = 0, nhận: %d", resp.TotalReviews)
	}
	if resp.AverageStars != 0.0 {
		t.Errorf("Kỳ vọng AverageStars = 0.0, nhận: %f", resp.AverageStars)
	}
	if resp.Reviews == nil || len(resp.Reviews) != 0 {
		t.Errorf("Kỳ vọng mảng reviews rỗng, nhận: %v", resp.Reviews)
	}
}

func TestReviews_CreateAndXSSSanitize(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	// 1. Gửi đánh giá mới có chứa mã độc XSS
	payload := map[string]interface{}{
		"author_name": "<script>alert('hack')</script>Nguyễn Văn Dev",
		"author_role": "Kỹ Sư Phần Mềm",
		"stars":       5,
		"text":        "Hệ thống phân phối rất tuyệt vời! <img src=x onerror=alert(1)> Rất đáng trải nghiệm.",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/reviews", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	ReviewsHandler(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("Kỳ vọng HTTP 201 Created, nhận: %d. Body: %s", rr.Code, rr.Body.String())
	}

	var createResp struct {
		Status  string     `json:"status"`
		Message string     `json:"message"`
		Review  ReviewItem `json:"review"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&createResp); err != nil {
		t.Fatalf("Lỗi decode createResp: %v", err)
	}

	rev := createResp.Review
	if rev.Stars != 5 {
		t.Errorf("Kỳ vọng stars = 5, nhận: %d", rev.Stars)
	}
	// Kiểm tra XSS đã được sanitize triệt để
	if bytes.Contains([]byte(rev.AuthorName), []byte("<script>")) {
		t.Errorf("AuthorName chưa được sanitize chống script tag: %s", rev.AuthorName)
	}
	if bytes.Contains([]byte(rev.Text), []byte("<img")) || bytes.Contains([]byte(rev.Text), []byte("onerror=")) {
		t.Errorf("Text chưa được sanitize chống thẻ và inline event handler: %s", rev.Text)
	}

	// 2. Thêm tiếp 1 đánh giá 4 sao
	payload2 := map[string]interface{}{
		"author_name": "Trần Thị Tester",
		"author_role": "QA Lead",
		"stars":       4,
		"text":        "Tốc độ tải ứng dụng nhanh, giao diện hiện đại.",
	}
	body2, _ := json.Marshal(payload2)
	req2 := httptest.NewRequest(http.MethodPost, "/api/reviews", bytes.NewBuffer(body2))
	req2.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	ReviewsHandler(rr2, req2)
	if rr2.Code != http.StatusCreated {
		t.Fatalf("Kỳ vọng HTTP 201 Created, nhận: %d", rr2.Code)
	}

	// 3. Gọi GET /api/reviews để kiểm tra thống kê
	reqGet := httptest.NewRequest(http.MethodGet, "/api/reviews", nil)
	rrGet := httptest.NewRecorder()
	ReviewsHandler(rrGet, reqGet)

	if rrGet.Code != http.StatusOK {
		t.Fatalf("Kỳ vọng HTTP 200, nhận: %d", rrGet.Code)
	}

	var listResp ReviewsResponse
	if err := json.NewDecoder(rrGet.Body).Decode(&listResp); err != nil {
		t.Fatalf("Lỗi decode listResp: %v", err)
	}

	if listResp.TotalReviews != 2 {
		t.Errorf("Kỳ vọng TotalReviews = 2, nhận: %d", listResp.TotalReviews)
	}
	// Điểm trung bình: (5 + 4) / 2 = 4.5
	if listResp.AverageStars != 4.5 {
		t.Errorf("Kỳ vọng AverageStars = 4.5, nhận: %f", listResp.AverageStars)
	}
	if listResp.StarCounts.Five != 1 || listResp.StarCounts.Four != 1 {
		t.Errorf("Kỳ vọng 1 đánh giá 5 sao và 1 đánh giá 4 sao, nhận: %+v", listResp.StarCounts)
	}
}

func TestReviews_ValidationErrors(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	tests := []struct {
		name    string
		payload map[string]interface{}
	}{
		{
			name: "Thiếu tác giả",
			payload: map[string]interface{}{
				"author_name": "",
				"stars":       5,
				"text":        "Nội dung hợp lệ",
			},
		},
		{
			name: "Thiếu nội dung",
			payload: map[string]interface{}{
				"author_name": "Nguyen Van A",
				"stars":       5,
				"text":        "",
			},
		},
		{
			name: "Số sao nhỏ hơn 1",
			payload: map[string]interface{}{
				"author_name": "Nguyen Van A",
				"stars":       0,
				"text":        "Quá tệ",
			},
		},
		{
			name: "Số sao lớn hơn 5",
			payload: map[string]interface{}{
				"author_name": "Nguyen Van A",
				"stars":       6,
				"text":        "Quá đỉnh",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := json.Marshal(tc.payload)
			req := httptest.NewRequest(http.MethodPost, "/api/reviews", bytes.NewBuffer(b))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()

			ReviewsHandler(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Errorf("[%s] Kỳ vọng HTTP 400 Bad Request, nhận: %d", tc.name, rr.Code)
			}
		})
	}
}

func TestReviews_DeleteByAdmin(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	// 1. Tạo 1 đánh giá
	payload := map[string]interface{}{
		"author_name": "Spam User",
		"stars":       1,
		"text":        "Quảng cáo rác spam lừa đảo xin liên hệ số điện thoại...",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/reviews", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	ReviewsHandler(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("Không thể tạo review mẫu: %d", rr.Code)
	}

	var createResp struct {
		Review ReviewItem `json:"review"`
	}
	json.NewDecoder(rr.Body).Decode(&createResp)
	createdID := createResp.Review.ID

	// 2. Thử xóa không có quyền (không có token) -> 403
	reqDelNoAuth := httptest.NewRequest(http.MethodDelete, "/api/reviews/"+createdID, nil)
	rrDelNoAuth := httptest.NewRecorder()
	ReviewDetailHandler(rrDelNoAuth, reqDelNoAuth)

	if rrDelNoAuth.Code != http.StatusForbidden {
		t.Errorf("Kỳ vọng HTTP 403 Forbidden khi không có quyền admin, nhận: %d", rrDelNoAuth.Code)
	}

	// 3. Xóa với Dynamic Admin API Key -> 200 OK
	adminKey, err := GenerateNewKey("Review Test Admin Key", 1)
	if err != nil {
		t.Fatalf("Không thể sinh dynamic key cho test: %v", err)
	}
	defer RevokeKey(adminKey.ID)

	reqDelAdmin := httptest.NewRequest(http.MethodDelete, "/api/reviews/"+createdID, nil)
	reqDelAdmin.Header.Set("Authorization", "Bearer "+adminKey.Key)
	rrDelAdmin := httptest.NewRecorder()
	ReviewDetailHandler(rrDelAdmin, reqDelAdmin)

	if rrDelAdmin.Code != http.StatusOK {
		t.Fatalf("Kỳ vọng HTTP 200 OK khi Admin xóa, nhận: %d, body: %s", rrDelAdmin.Code, rrDelAdmin.Body.String())
	}

	// 4. Xóa lại lần 2 -> 404 Not Found
	reqDelRepeat := httptest.NewRequest(http.MethodDelete, "/api/reviews/"+createdID, nil)
	reqDelRepeat.Header.Set("Authorization", "Bearer "+adminKey.Key)
	rrDelRepeat := httptest.NewRecorder()
	ReviewDetailHandler(rrDelRepeat, reqDelRepeat)

	if rrDelRepeat.Code != http.StatusNotFound {
		t.Errorf("Kỳ vọng HTTP 404 Not Found khi xóa lại ID đã xóa, nhận: %d", rrDelRepeat.Code)
	}
}
