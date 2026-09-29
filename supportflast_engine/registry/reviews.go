package registry

import (
	"encoding/json"
	"fmt"
	"html"
	"log"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"supportflast_engine/database"
)

var (
	stripScriptRegex = regexp.MustCompile(`(?i)<\s*script[^>]*>[\s\S]*?<\s*/\s*script\s*>`)
	stripEventRegex  = regexp.MustCompile(`(?i)\bon\w+\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
)

// ReviewItem cấu trúc nhận xét đánh giá trả về cho API
type ReviewItem struct {
	ID         string `json:"id"`
	AppID      string `json:"app_id,omitempty"`
	UserID     string `json:"user_id,omitempty"`
	AuthorName string `json:"author_name"`
	Name       string `json:"name"` // Alias tương thích UI
	AuthorRole string `json:"author_role"`
	Role       string `json:"role"` // Alias tương thích UI
	Stars      int    `json:"stars"`
	Text       string `json:"text"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
	Date       string `json:"date"` // Format ngày thân thiện UI
}

// StarCounts thống kê số lượng đánh giá theo từng mức sao
type StarCounts struct {
	One   int `json:"1"`
	Two   int `json:"2"`
	Three int `json:"3"`
	Four  int `json:"4"`
	Five  int `json:"5"`
}

// StarPercentages thống kê tỷ lệ phần trăm theo từng mức sao
type StarPercentages struct {
	One   int `json:"1"`
	Two   int `json:"2"`
	Three int `json:"3"`
	Four  int `json:"4"`
	Five  int `json:"5"`
}

// ReviewsResponse cấu trúc phản hồi danh sách đánh giá kèm thống kê
type ReviewsResponse struct {
	Status       string          `json:"status"`
	TotalReviews int             `json:"total_reviews"`
	AverageStars float64         `json:"average_stars"`
	StarCounts   StarCounts      `json:"star_counts"`
	Percentages  StarPercentages `json:"percentages"`
	Reviews      []ReviewItem    `json:"reviews"`
}

// CreateReviewRequest dữ liệu tiếp nhận khi người dùng gửi đánh giá mới
type CreateReviewRequest struct {
	AppID      string `json:"app_id,omitempty"`
	AuthorName string `json:"author_name"`
	Name       string `json:"name,omitempty"` // Fallback alias
	AuthorRole string `json:"author_role"`
	Role       string `json:"role,omitempty"` // Fallback alias
	Stars      int    `json:"stars"`
	Text       string `json:"text"`
	Content    string `json:"content,omitempty"` // Fallback alias
}

// SanitizeReviewText loại bỏ mã độc script và escape HTML chống tấn công XSS
func SanitizeReviewText(input string) string {
	s := stripScriptRegex.ReplaceAllString(input, "")
	s = stripEventRegex.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	return html.EscapeString(s)
}

// formatFriendlyDate chuyển đổi timestamp RFC3339 sang ngày định dạng dễ đọc
func formatFriendlyDate(rfc3339Str string) string {
	t, err := time.Parse(time.RFC3339, rfc3339Str)
	if err != nil {
		return rfc3339Str
	}
	return t.Local().Format("02/01/2006 15:04")
}

// extractReviewID trích xuất ID đánh giá từ r.PathValue, URL path hoặc Query parameter
func extractReviewID(r *http.Request) string {
	// 1. Thử lấy từ Go 1.22+ PathValue nếu có
	if id := r.PathValue("id"); id != "" {
		return strings.TrimSpace(id)
	}

	// 2. Trích xuất từ URL Path (/api/reviews/{id})
	path := strings.Trim(r.URL.Path, "/")
	parts := strings.Split(path, "/")
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "reviews" {
		id := strings.TrimSpace(parts[2])
		if id != "" {
			return id
		}
	}

	// 3. Fallback lấy từ Query parameter ?id=...
	return strings.TrimSpace(r.URL.Query().Get("id"))
}

// ReviewsHandler điều phối các request tới /api/reviews (GET, POST)
func ReviewsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		GetReviewsHandler(w, r)
	case http.MethodPost:
		CreateReviewHandler(w, r)
	case http.MethodOptions:
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, `{"error":"Phương thức không được hỗ trợ, vui lòng sử dụng GET hoặc POST"}`, http.StatusMethodNotAllowed)
	}
}

// ReviewDetailHandler điều phối các request tới /api/reviews/{id} (DELETE, GET)
func ReviewDetailHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Nếu path là /api/reviews hoặc /api/reviews/ thì chuyển sang ReviewsHandler
	cleanPath := strings.Trim(r.URL.Path, "/")
	if cleanPath == "api/reviews" {
		ReviewsHandler(w, r)
		return
	}

	switch r.Method {
	case http.MethodDelete:
		DeleteReviewHandler(w, r)
	case http.MethodGet:
		GetReviewByIDHandler(w, r)
	case http.MethodOptions:
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, `{"error":"Phương thức không được hỗ trợ, vui lòng sử dụng DELETE hoặc GET"}`, http.StatusMethodNotAllowed)
	}
}

// GetReviewsHandler trả về danh sách đánh giá từ bảng 'reviews' trong SQLite DB
// kèm thống kê tổng số lượt đánh giá, điểm trung bình và số lượng từng mức sao (GET /api/reviews)
func GetReviewsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	db := database.GetDB()
	if db == nil {
		log.Printf("[ENGINE] [REVIEWS] [ERROR] Không thể kết nối cơ sở dữ liệu SQLite")
		http.Error(w, `{"error":"Không thể kết nối cơ sở dữ liệu hệ thống"}`, http.StatusInternalServerError)
		return
	}

	appIDFilter := strings.TrimSpace(r.URL.Query().Get("app_id"))

	var query string
	var args []interface{}

	if appIDFilter != "" {
		query = `
			SELECT id, COALESCE(app_id, ''), COALESCE(user_id, ''), author_name, COALESCE(author_role, 'Người dùng'),
			       stars, text, COALESCE(status, 'approved'), COALESCE(created_at, '')
			FROM reviews
			WHERE (status = 'approved' OR status IS NULL OR status = '') AND app_id = ?
			ORDER BY datetime(created_at) DESC, rowid DESC
		`
		args = append(args, appIDFilter)
	} else {
		query = `
			SELECT id, COALESCE(app_id, ''), COALESCE(user_id, ''), author_name, COALESCE(author_role, 'Người dùng'),
			       stars, text, COALESCE(status, 'approved'), COALESCE(created_at, '')
			FROM reviews
			WHERE status = 'approved' OR status IS NULL OR status = ''
			ORDER BY datetime(created_at) DESC, rowid DESC
		`
	}

	rows, err := db.QueryContext(r.Context(), query, args...)
	if err != nil {
		log.Printf("[ENGINE] [REVIEWS] [ERROR] Lỗi truy vấn bảng reviews: %v", err)
		http.Error(w, `{"error":"Lỗi truy vấn dữ liệu đánh giá từ cơ sở dữ liệu"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	reviews := make([]ReviewItem, 0)
	counts := StarCounts{}
	sumStars := 0

	for rows.Next() {
		var item ReviewItem
		err := rows.Scan(
			&item.ID,
			&item.AppID,
			&item.UserID,
			&item.AuthorName,
			&item.AuthorRole,
			&item.Stars,
			&item.Text,
			&item.Status,
			&item.CreatedAt,
		)
		if err != nil {
			log.Printf("[ENGINE] [REVIEWS] [WARN] Lỗi scan dòng dữ liệu review: %v", err)
			continue
		}

		// Thiết lập các trường tương thích
		item.Name = item.AuthorName
		item.Role = item.AuthorRole
		item.Date = formatFriendlyDate(item.CreatedAt)

		// Thống kê điểm sao
		switch item.Stars {
		case 5:
			counts.Five++
		case 4:
			counts.Four++
		case 3:
			counts.Three++
		case 2:
			counts.Two++
		case 1:
			counts.One++
		}
		sumStars += item.Stars

		reviews = append(reviews, item)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[ENGINE] [REVIEWS] [ERROR] rows.Err() sau query reviews: %v", err)
	}

	total := len(reviews)
	avgStars := 0.0
	percentages := StarPercentages{}

	if total > 0 {
		avgStars = float64(sumStars) / float64(total)
		avgStars = math.Round(avgStars*10) / 10 // Làm tròn 1 chữ số thập phân, ví dụ: 4.8 hoặc 5.0

		percentages.Five = int(math.Round(float64(counts.Five) / float64(total) * 100))
		percentages.Four = int(math.Round(float64(counts.Four) / float64(total) * 100))
		percentages.Three = int(math.Round(float64(counts.Three) / float64(total) * 100))
		percentages.Two = int(math.Round(float64(counts.Two) / float64(total) * 100))
		percentages.One = int(math.Round(float64(counts.One) / float64(total) * 100))
	}

	resp := ReviewsResponse{
		Status:       "success",
		TotalReviews: total,
		AverageStars: avgStars,
		StarCounts:   counts,
		Percentages:  percentages,
		Reviews:      reviews,
	}

	json.NewEncoder(w).Encode(resp)
}

// CreateReviewHandler tiếp nhận đánh giá mới của người dùng (POST /api/reviews)
// Thực hiện validate dữ liệu, sanitize text chống XSS, tạo UUID duy nhất và lưu vào SQLite DB
func CreateReviewHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed, use POST"}`, http.StatusMethodNotAllowed)
		return
	}

	// Giới hạn kích thước body tối đa 64KB chống tràn bộ nhớ (Phần 7.1)
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

	var req CreateReviewRequest
	contentType := r.Header.Get("Content-Type")

	if strings.Contains(contentType, "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"Dữ liệu JSON đánh giá không hợp lệ"}`, http.StatusBadRequest)
			return
		}
	} else {
		// Hỗ trợ form submissions
		if err := r.ParseForm(); err != nil {
			http.Error(w, `{"error":"Không thể xử lý form dữ liệu"}`, http.StatusBadRequest)
			return
		}
		req.AuthorName = r.FormValue("author_name")
		req.Name = r.FormValue("name")
		req.AuthorRole = r.FormValue("author_role")
		req.Role = r.FormValue("role")
		req.Text = r.FormValue("text")
		req.Content = r.FormValue("content")
		req.AppID = r.FormValue("app_id")
		starsVal := r.FormValue("stars")
		if starsVal != "" {
			var s int
			if _, err := fmt.Sscanf(starsVal, "%d", &s); err == nil {
				req.Stars = s
			}
		}
	}

	// 1. Chuẩn hóa các trường thông tin
	authorName := strings.TrimSpace(req.AuthorName)
	if authorName == "" {
		authorName = strings.TrimSpace(req.Name)
	}

	authorRole := strings.TrimSpace(req.AuthorRole)
	if authorRole == "" {
		authorRole = strings.TrimSpace(req.Role)
	}
	if authorRole == "" {
		authorRole = "Thành viên cộng đồng"
	}

	text := strings.TrimSpace(req.Text)
	if text == "" {
		text = strings.TrimSpace(req.Content)
	}

	stars := req.Stars

	// 2. Validate dữ liệu đầu vào nghiêm ngặt
	if authorName == "" {
		http.Error(w, `{"error":"Vui lòng nhập tên người đánh giá (author_name)"}`, http.StatusBadRequest)
		return
	}
	if len(authorName) > 100 {
		http.Error(w, `{"error":"Tên người đánh giá không được vượt quá 100 ký tự"}`, http.StatusBadRequest)
		return
	}
	if strings.Contains(authorName, "\x00") {
		http.Error(w, `{"error":"Tên người đánh giá chứa ký tự không an toàn"}`, http.StatusBadRequest)
		return
	}

	if text == "" {
		http.Error(w, `{"error":"Vui lòng nhập nội dung đánh giá (text)"}`, http.StatusBadRequest)
		return
	}
	if len(text) < 3 {
		http.Error(w, `{"error":"Nội dung đánh giá phải có độ dài tối thiểu từ 3 ký tự trở lên"}`, http.StatusBadRequest)
		return
	}
	if len(text) > 2000 {
		http.Error(w, `{"error":"Nội dung đánh giá không được vượt quá 2000 ký tự"}`, http.StatusBadRequest)
		return
	}
	if strings.Contains(text, "\x00") {
		http.Error(w, `{"error":"Nội dung đánh giá chứa ký tự không an toàn"}`, http.StatusBadRequest)
		return
	}

	// Stars phải nằm trong khoảng từ 1 đến 5
	if stars < 1 || stars > 5 {
		http.Error(w, `{"error":"Số sao đánh giá phải từ 1 đến 5 sao (stars: 1-5)"}`, http.StatusBadRequest)
		return
	}

	// 3. Sanitize dữ liệu chống tấn công XSS
	cleanAuthorName := SanitizeReviewText(authorName)
	cleanAuthorRole := SanitizeReviewText(authorRole)
	cleanText := SanitizeReviewText(text)

	// 4. Tạo ID duy nhất (uuid v4 chuẩn)
	reviewID := fmt.Sprintf("rev-%s", uuid.New().String())
	createdAt := time.Now().UTC().Format(time.RFC3339)

	// Kiểm tra xem người dùng có đăng nhập hay không (nếu có thì gắn user_id)
	var userIDVal interface{}
	token := ExtractToken(r)
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	if token != "" {
		if u, ok := GetUserFromToken(token); ok && u != nil {
			userIDVal = u.ID
		}
	}

	var appIDVal interface{}
	cleanAppID := strings.TrimSpace(req.AppID)
	if cleanAppID != "" {
		appIDVal = cleanAppID
	}

	// 5. Lưu vào cơ sở dữ liệu SQLite bảng 'reviews'
	db := database.GetDB()
	if db == nil {
		log.Printf("[ENGINE] [REVIEWS] [ERROR] Không thể kết nối cơ sở dữ liệu SQLite để lưu đánh giá")
		http.Error(w, `{"error":"Không thể kết nối cơ sở dữ liệu hệ thống"}`, http.StatusInternalServerError)
		return
	}

	insertSQL := `
		INSERT INTO reviews (id, app_id, user_id, author_name, author_role, stars, text, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'approved', ?)
	`

	_, err := db.ExecContext(
		r.Context(),
		insertSQL,
		reviewID,
		appIDVal,
		userIDVal,
		cleanAuthorName,
		cleanAuthorRole,
		stars,
		cleanText,
		createdAt,
	)
	if err != nil {
		log.Printf("[ENGINE] [REVIEWS] [ERROR] Lỗi chèn đánh giá vào SQLite DB: %v", err)
		http.Error(w, `{"error":"Lỗi lưu trữ đánh giá vào cơ sở dữ liệu"}`, http.StatusInternalServerError)
		return
	}

	var assignedUserID string
	if userIDVal != nil {
		assignedUserID, _ = userIDVal.(string)
	}

	createdItem := ReviewItem{
		ID:         reviewID,
		AppID:      cleanAppID,
		UserID:     assignedUserID,
		AuthorName: cleanAuthorName,
		Name:       cleanAuthorName,
		AuthorRole: cleanAuthorRole,
		Role:       cleanAuthorRole,
		Stars:      stars,
		Text:       cleanText,
		Status:     "approved",
		CreatedAt:  createdAt,
		Date:       formatFriendlyDate(createdAt),
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Đánh giá của bạn đã được ghi nhận và xuất bản thành công lên hệ thống!",
		"review":  createdItem,
	})
}

// DeleteReviewHandler cho phép Quản Trị Viên (role admin) xóa một đánh giá spam hoặc vi phạm (DELETE /api/reviews/{id})
func DeleteReviewHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodDelete {
		http.Error(w, `{"error":"Method not allowed, use DELETE"}`, http.StatusMethodNotAllowed)
		return
	}

	// 1. Xác thực quyền Quản Trị Viên (role admin)
	token := ExtractToken(r)
	if token == "" {
		token = r.URL.Query().Get("token")
	}

	isAdmin := false
	if token != "" {
		if u, ok := GetUserFromToken(token); ok && u != nil && u.Role == "admin" {
			isAdmin = true
		} else if VerifyAPIKey(token) {
			// Master Developer / CI/CD API Key được cấp quyền quản trị
			isAdmin = true
		}
	}

	if !isAdmin {
		http.Error(w, `{"error":"Forbidden: Yêu cầu quyền Quản Trị Viên (role admin) để xóa đánh giá"}`, http.StatusForbidden)
		return
	}

	// 2. Trích xuất ID đánh giá cần xóa
	reviewID := extractReviewID(r)
	if reviewID == "" {
		http.Error(w, `{"error":"Mã định danh đánh giá (id) không được để trống"}`, http.StatusBadRequest)
		return
	}

	// 3. Thực thi xóa từ bảng 'reviews' trong SQLite DB
	db := database.GetDB()
	if db == nil {
		log.Printf("[ENGINE] [REVIEWS] [ERROR] Không thể kết nối cơ sở dữ liệu SQLite")
		http.Error(w, `{"error":"Không thể kết nối cơ sở dữ liệu hệ thống"}`, http.StatusInternalServerError)
		return
	}

	res, err := db.ExecContext(r.Context(), "DELETE FROM reviews WHERE id = ?", reviewID)
	if err != nil {
		log.Printf("[ENGINE] [REVIEWS] [ERROR] Lỗi thực thi xóa đánh giá %s: %v", reviewID, err)
		http.Error(w, `{"error":"Lỗi thực thi xóa đánh giá trong cơ sở dữ liệu"}`, http.StatusInternalServerError)
		return
	}

	rows, err := res.RowsAffected()
	if err != nil || rows == 0 {
		http.Error(w, `{"error":"Không tìm thấy đánh giá với ID đã cho hoặc đánh giá đã bị xóa"}`, http.StatusNotFound)
		return
	}

	log.Printf("[ENGINE] [REVIEWS] [ADMIN] Quản trị viên đã xóa thành công đánh giá vi phạm: %s", reviewID)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Đã xóa đánh giá vi phạm thành công khỏi hệ thống.",
		"id":      reviewID,
	})
}

// GetReviewByIDHandler trả về chi tiết một đánh giá cụ thể (GET /api/reviews/{id})
func GetReviewByIDHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	reviewID := extractReviewID(r)
	if reviewID == "" {
		http.Error(w, `{"error":"ID đánh giá không được để trống"}`, http.StatusBadRequest)
		return
	}

	db := database.GetDB()
	if db == nil {
		http.Error(w, `{"error":"Không thể kết nối cơ sở dữ liệu hệ thống"}`, http.StatusInternalServerError)
		return
	}

	query := `
		SELECT id, COALESCE(app_id, ''), COALESCE(user_id, ''), author_name, COALESCE(author_role, 'Người dùng'),
		       stars, text, COALESCE(status, 'approved'), COALESCE(created_at, '')
		FROM reviews
		WHERE id = ?
	`

	var item ReviewItem
	err := db.QueryRowContext(r.Context(), query, reviewID).Scan(
		&item.ID,
		&item.AppID,
		&item.UserID,
		&item.AuthorName,
		&item.AuthorRole,
		&item.Stars,
		&item.Text,
		&item.Status,
		&item.CreatedAt,
	)
	if err != nil {
		http.Error(w, `{"error":"Không tìm thấy đánh giá với ID yêu cầu"}`, http.StatusNotFound)
		return
	}

	item.Name = item.AuthorName
	item.Role = item.AuthorRole
	item.Date = formatFriendlyDate(item.CreatedAt)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"review": item,
	})
}
