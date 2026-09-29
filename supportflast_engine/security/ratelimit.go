package security

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Cấu hình Rate Limiting phòng thủ đa tầng theo Rule 8.2 & Rule 7.1
const (
	// Tầng 1: Giới hạn toàn cục 1,200 requests/phút cho mỗi IP thông thường (20 req/s, phù hợp web tải tệp phân mảnh)
	DefaultGlobalRateLimit = 1200
	DefaultGlobalWindow    = time.Minute

	// Tầng 2: Giới hạn tối đa 5 lần thử sai trong 15 phút, khóa 15 phút theo Rule 8.2
	DefaultMaxLoginFailures = 5
	DefaultLockoutDuration  = 15 * time.Minute
	DefaultFailureWindow    = 15 * time.Minute

	// Tầng 3: Giới hạn quản trị & thao tác nguy hiểm 30 requests/phút
	DefaultAdminRateLimit = 30
	DefaultAdminWindow    = time.Minute

	// MaxRateLimitEntries: Giới hạn số lượng bản ghi trong RAM chống tràn bộ nhớ (Rule 7.1)
	MaxRateLimitEntries = 10000
)

// ============================================================================
// TẦNG 1 & TẦNG 3: SLIDING WINDOW RATE LIMITER CHO TOÀN CỤC VÀ QUẢN TRỊ
// ============================================================================

// SlidingWindowLimiter triển khai thuật toán cửa sổ trượt (Sliding Window Timestamps)
// Đảm bảo độ chính xác cao, thread-safe và tự động dọn dẹp chống tràn RAM (Rule 7.1)
type SlidingWindowLimiter struct {
	mu          sync.Mutex
	maxRequests int
	window      time.Duration
	requests    map[string][]time.Time
}

// NewSlidingWindowLimiter tạo một bộ giới hạn tốc độ theo cửa sổ trượt
func NewSlidingWindowLimiter(maxRequests int, window time.Duration) *SlidingWindowLimiter {
	if maxRequests <= 0 {
		maxRequests = DefaultGlobalRateLimit
	}
	if window <= 0 {
		window = DefaultGlobalWindow
	}
	return &SlidingWindowLimiter{
		maxRequests: maxRequests,
		window:      window,
		requests:    make(map[string][]time.Time),
	}
}

// Allow kiểm tra và ghi nhận request của một IP.
// Trả về: (cho_phép_truy_cập, số_request_còn_lại, thời_gian_chờ_giây)
func (lim *SlidingWindowLimiter) Allow(ip string) (bool, int, time.Duration) {
	cleanIP := strings.TrimSpace(ip)
	if cleanIP == "" {
		return true, lim.maxRequests, 0
	}

	// Nếu IP nằm trong Whitelist nội bộ -> luôn cho phép
	if GlobalIPJail.IsWhitelisted(cleanIP) {
		return true, lim.maxRequests, 0
	}

	lim.mu.Lock()
	defer lim.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-lim.window)

	timestamps := lim.requests[cleanIP]
	var valid []time.Time
	for _, t := range timestamps {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	currentCount := len(valid)

	// Nếu đã chạm hoặc vượt ngưỡng cho phép
	if currentCount >= lim.maxRequests {
		// Tính toán thời gian phải chờ để request cũ nhất hết hạn
		retryAfter := valid[0].Add(lim.window).Sub(now)
		if retryAfter < time.Second {
			retryAfter = time.Second
		}
		lim.requests[cleanIP] = valid

		// Nếu gửi dồn dập vượt quá gấp đôi giới hạn (DDoS/Spam thô bạo)
		// -> tự động kích hoạt IP Jail đưa vào danh sách đen 15 phút!
		if currentCount >= lim.maxRequests*2 {
			go func(abusiveIP string) {
				GlobalIPJail.RecordAttack(abusiveIP, ReasonRateLimitAbuse, SeverityLow)
			}(cleanIP)
		}

		return false, 0, retryAfter
	}

	// Chấp thuận request và ghi nhận timestamp hiện tại
	valid = append(valid, now)
	lim.requests[cleanIP] = valid
	remaining := lim.maxRequests - len(valid)

	// Dọn dẹp định kỳ nếu map quá lớn (Rule 7.1)
	if len(lim.requests) > MaxRateLimitEntries {
		lim.cleanupUnsafe(now)
	}

	return true, remaining, 0
}

// Reset xóa thông tin theo dõi của một IP cụ thể
func (lim *SlidingWindowLimiter) Reset(ip string) {
	lim.mu.Lock()
	defer lim.mu.Unlock()
	delete(lim.requests, strings.TrimSpace(ip))
}

// ResetAll xóa toàn bộ thông tin theo dõi
func (lim *SlidingWindowLimiter) ResetAll() {
	lim.mu.Lock()
	defer lim.mu.Unlock()
	lim.requests = make(map[string][]time.Time)
}

// cleanupUnsafe dọn dẹp các IP không còn request hợp lệ (phải gọi khi đã giữ Lock)
func (lim *SlidingWindowLimiter) cleanupUnsafe(now time.Time) {
	cutoff := now.Add(-lim.window)
	for ip, list := range lim.requests {
		if len(list) == 0 || list[len(list)-1].Before(cutoff) {
			delete(lim.requests, ip)
		}
	}
}

// ============================================================================
// TẦNG 2: RATE LIMITER XÁC THỰC & ĐĂNG NHẬP (RULE 8.2)
// ============================================================================

// LoginAttemptRecord ghi nhận lịch sử các lần đăng nhập sai của một đối tượng (IP hoặc Username)
type LoginAttemptRecord struct {
	Failures    int       `json:"failures"`
	LockedUntil time.Time `json:"locked_until"`
	LastFailed  time.Time `json:"last_failed"`
}

// LoginRateLimiter quản lý việc giới hạn tần suất đăng nhập theo cả IP và Username
type LoginRateLimiter struct {
	mu            sync.RWMutex
	ipRecords     map[string]*LoginAttemptRecord
	userRecords   map[string]*LoginAttemptRecord
	maxFailures   int
	lockDuration  time.Duration
	failureWindow time.Duration
}

// NewLoginRateLimiter tạo một bộ giới hạn tốc độ đăng nhập mới
func NewLoginRateLimiter(maxFailures int, lockDuration, failureWindow time.Duration) *LoginRateLimiter {
	if maxFailures <= 0 {
		maxFailures = DefaultMaxLoginFailures
	}
	if lockDuration <= 0 {
		lockDuration = DefaultLockoutDuration
	}
	if failureWindow <= 0 {
		failureWindow = DefaultFailureWindow
	}

	return &LoginRateLimiter{
		ipRecords:     make(map[string]*LoginAttemptRecord),
		userRecords:   make(map[string]*LoginAttemptRecord),
		maxFailures:   maxFailures,
		lockDuration:  lockDuration,
		failureWindow: failureWindow,
	}
}

// GlobalLoginRateLimiter singleton quản lý rate limiting đăng nhập toàn hệ thống
var GlobalLoginRateLimiter = NewLoginRateLimiter(DefaultMaxLoginFailures, DefaultLockoutDuration, DefaultFailureWindow)

// IsLocked kiểm tra xem IP hoặc Username hiện có đang bị khóa hay không
// Trả về: (đang_bị_khóa, thời_gian_còn_lại, lý_do_khóa)
func (lim *LoginRateLimiter) IsLocked(ip, username string) (bool, time.Duration, string) {
	lim.mu.RLock()
	defer lim.mu.RUnlock()

	now := time.Now()
	ip = strings.TrimSpace(ip)
	username = strings.ToLower(strings.TrimSpace(username))

	// 1. Kiểm tra khóa theo IP
	if ip != "" {
		if rec, exists := lim.ipRecords[ip]; exists {
			if rec.LockedUntil.After(now) {
				return true, rec.LockedUntil.Sub(now), fmt.Sprintf("Địa chỉ IP (%s)", ip)
			}
		}
	}

	// 2. Kiểm tra khóa theo Username
	if username != "" {
		if rec, exists := lim.userRecords[username]; exists {
			if rec.LockedUntil.After(now) {
				return true, rec.LockedUntil.Sub(now), fmt.Sprintf("Tài khoản (%s)", username)
			}
		}
	}

	return false, 0, ""
}

// RecordFailure ghi nhận một lần đăng nhập thất bại cho cả IP và Username
// Nếu số lần sai đạt ngưỡng (>= 5), sẽ kích hoạt khóa 15 phút theo Rule 8.2.
// Nếu IP tiếp tục brute-force nghiêm trọng (>= 10 lần) -> tự động tống vào IP Jail!
// Trả về: (vừa_bị_khóa, thời_gian_khóa_còn_lại, số_lần_thử_còn_lại)
func (lim *LoginRateLimiter) RecordFailure(ip, username string) (bool, time.Duration, int) {
	lim.mu.Lock()
	defer lim.mu.Unlock()

	now := time.Now()
	ip = strings.TrimSpace(ip)
	username = strings.ToLower(strings.TrimSpace(username))

	isLocked := false
	var maxLockRemaining time.Duration
	maxFailuresCurrent := 0

	// Helper cập nhật bản ghi
	updateRecord := func(records map[string]*LoginAttemptRecord, key string) {
		if key == "" {
			return
		}
		rec, exists := records[key]
		if !exists {
			rec = &LoginAttemptRecord{
				Failures:   1,
				LastFailed: now,
			}
			records[key] = rec
		} else {
			// Nếu thời gian từ lần sai cuối vượt quá cửa sổ failureWindow và không bị khóa -> reset lại từ 1
			if now.Sub(rec.LastFailed) > lim.failureWindow && rec.LockedUntil.Before(now) {
				rec.Failures = 1
			} else {
				rec.Failures++
			}
			rec.LastFailed = now
		}

		if rec.Failures > maxFailuresCurrent {
			maxFailuresCurrent = rec.Failures
		}

		// Nếu đạt hoặc vượt quá ngưỡng sai 5 lần -> khóa 15 phút (Rule 8.2)
		if rec.Failures >= lim.maxFailures {
			rec.LockedUntil = now.Add(lim.lockDuration)
			isLocked = true
			rem := rec.LockedUntil.Sub(now)
			if rem > maxLockRemaining {
				maxLockRemaining = rem
			}
		}
	}

	// Cập nhật theo IP
	if ip != "" {
		updateRecord(lim.ipRecords, ip)
	}

	// Cập nhật theo Username
	if username != "" {
		updateRecord(lim.userRecords, username)
	}

	// Tự động phối hợp Dynamic IP Jail: Nếu IP sai liên tiếp >= 10 lần (cố ý Brute-force)
	// -> Đưa ngay vào Blacklist của IP Jail với thời hạn giam giữ 1 giờ hoặc 24 giờ
	if ip != "" {
		if rec, exists := lim.ipRecords[ip]; exists && rec.Failures >= 10 {
			go func(attackerIP string) {
				cleanIP := SanitizeCRLF(attackerIP)
				log.Printf("[ENGINE] [SECURITY] IP %s brute-force đăng nhập >= 10 lần. Chuyển hồ sơ vào Dynamic IP Jail.", cleanIP)
				GlobalIPJail.RecordAttack(attackerIP, ReasonBruteForce, SeverityMedium)
			}(ip)
		}
	}

	// Dọn dẹp bộ nhớ nếu vượt ngưỡng dung lượng (Rule 7.1)
	if len(lim.ipRecords) > MaxRateLimitEntries || len(lim.userRecords) > MaxRateLimitEntries {
		lim.cleanupUnsafe(now)
	}

	attemptsLeft := lim.maxFailures - maxFailuresCurrent
	if attemptsLeft < 0 {
		attemptsLeft = 0
	}

	return isLocked, maxLockRemaining, attemptsLeft
}

// RecordSuccess ghi nhận đăng nhập thành công và xóa bỏ lịch sử sai của IP và Username
func (lim *LoginRateLimiter) RecordSuccess(ip, username string) {
	lim.mu.Lock()
	defer lim.mu.Unlock()

	ip = strings.TrimSpace(ip)
	username = strings.ToLower(strings.TrimSpace(username))

	if ip != "" {
		delete(lim.ipRecords, ip)
	}
	if username != "" {
		delete(lim.userRecords, username)
	}
}

// Reset xóa thông tin theo dõi của IP và Username cụ thể
func (lim *LoginRateLimiter) Reset(ip, username string) {
	lim.RecordSuccess(ip, username)
}

// ResetAll xóa toàn bộ thông tin rate limiter (phục vụ test)
func (lim *LoginRateLimiter) ResetAll() {
	lim.mu.Lock()
	defer lim.mu.Unlock()

	lim.ipRecords = make(map[string]*LoginAttemptRecord)
	lim.userRecords = make(map[string]*LoginAttemptRecord)
}

// cleanupUnsafe dọn dẹp các bản ghi đã hết hạn (phải gọi khi đã giữ Lock)
func (lim *LoginRateLimiter) cleanupUnsafe(now time.Time) {
	for k, v := range lim.ipRecords {
		if now.After(v.LockedUntil) && now.Sub(v.LastFailed) > lim.failureWindow {
			delete(lim.ipRecords, k)
		}
	}
	for k, v := range lim.userRecords {
		if now.After(v.LockedUntil) && now.Sub(v.LastFailed) > lim.failureWindow {
			delete(lim.userRecords, k)
		}
	}
}

// Cleanup thực hiện dọn dẹp an toàn các bản ghi đã hết hạn
func (lim *LoginRateLimiter) Cleanup() {
	lim.mu.Lock()
	defer lim.mu.Unlock()
	lim.cleanupUnsafe(time.Now())
}

// ============================================================================
// HỆ THỐNG PHÒNG THỦ ĐA TẦNG: MULTI-TIER RATE LIMITER
// ============================================================================

// MultiTierRateLimiter tích hợp cả 3 tầng phòng thủ:
// - Tầng 1: Toàn cục 120 reqs/phút
// - Tầng 2: Xác thực / Đăng nhập (5 lần sai -> khóa 15 phút)
// - Tầng 3: Quản trị & Thao tác nguy hiểm 30 reqs/phút
type MultiTierRateLimiter struct {
	tier1Global *SlidingWindowLimiter
	tier2Auth   *LoginRateLimiter
	tier3Admin  *SlidingWindowLimiter
}

// NewMultiTierRateLimiter khởi tạo hệ thống phòng thủ đa tầng
func NewMultiTierRateLimiter(globalLimit, adminLimit int, authLimiter *LoginRateLimiter) *MultiTierRateLimiter {
	if authLimiter == nil {
		authLimiter = GlobalLoginRateLimiter
	}
	return &MultiTierRateLimiter{
		tier1Global: NewSlidingWindowLimiter(globalLimit, DefaultGlobalWindow),
		tier2Auth:   authLimiter,
		tier3Admin:  NewSlidingWindowLimiter(adminLimit, DefaultAdminWindow),
	}
}

// GlobalMultiTierLimiter singleton phòng thủ đa tầng toàn hệ thống
var GlobalMultiTierLimiter = NewMultiTierRateLimiter(
	DefaultGlobalRateLimit,
	DefaultAdminRateLimit,
	GlobalLoginRateLimiter,
)

// AllowGlobal kiểm tra Tầng 1 (Toàn cục: 120 requests/phút)
func (mtl *MultiTierRateLimiter) AllowGlobal(ip string) (bool, int, time.Duration) {
	return mtl.tier1Global.Allow(ip)
}

// AllowAdmin kiểm tra Tầng 3 (Quản trị: 30 requests/phút)
func (mtl *MultiTierRateLimiter) AllowAdmin(ip string) (bool, int, time.Duration) {
	return mtl.tier3Admin.Allow(ip)
}

// CheckLoginLock kiểm tra Tầng 2 (Khóa đăng nhập)
func (mtl *MultiTierRateLimiter) CheckLoginLock(ip, username string) (bool, time.Duration, string) {
	return mtl.tier2Auth.IsLocked(ip, username)
}

// RecordLoginFailure ghi nhận đăng nhập thất bại Tầng 2
func (mtl *MultiTierRateLimiter) RecordLoginFailure(ip, username string) (bool, time.Duration, int) {
	return mtl.tier2Auth.RecordFailure(ip, username)
}

// RecordLoginSuccess ghi nhận đăng nhập thành công Tầng 2
func (mtl *MultiTierRateLimiter) RecordLoginSuccess(ip, username string) {
	mtl.tier2Auth.RecordSuccess(ip, username)
}

// ResetAll đặt lại toàn bộ các tầng (phục vụ test)
func (mtl *MultiTierRateLimiter) ResetAll() {
	mtl.tier1Global.ResetAll()
	mtl.tier2Auth.ResetAll()
	mtl.tier3Admin.ResetAll()
}

// Danh sách các tiền tố đường dẫn thuộc Tầng 3 (Quản trị & Thao tác nguy hiểm)
var adminSensitiveRoutes = []string{
	"/api/admin/",
	"/api/keys/generate",
	"/api/apps/publish",
	"/api/system/update",
	"/api/system/maintenance",
	"/api/system/hot-reload",
	"/api/system/deploy-ui",
	"/api/device/sync",
}

// IsAdminSensitiveRoute kiểm tra xem đường dẫn có thuộc phạm vi quản trị / thao tác nguy hiểm hay không
func IsAdminSensitiveRoute(path string) bool {
	lowerPath := strings.ToLower(path)
	for _, route := range adminSensitiveRoutes {
		if strings.HasPrefix(lowerPath, route) {
			return true
		}
	}
	return false
}

// IsDataTransferRoute kiểm tra các luồng truyền tải dữ liệu tệp tin khối lớn (Chunk Upload, Streaming, Download)
// Các endpoint này được bảo vệ bởi User Session, Quota dung lượng và Token JWT nên được miễn trừ Rate Limiter API thông thường
func IsDataTransferRoute(path string) bool {
	lowerPath := strings.ToLower(path)
	return lowerPath == "/api/files/upload-chunked" ||
		lowerPath == "/api/files/upload" ||
		lowerPath == "/api/files/stream" ||
		lowerPath == "/api/files/download" ||
		lowerPath == "/api/files/chunks"
}

// ============================================================================
// MIDDLEWARES PHÒNG THỦ RATE LIMIT
// ============================================================================

// MultiTierRateLimitMiddleware middleware phòng thủ đa tầng tự động áp dụng chính sách
// phù hợp với từng tầng dựa trên lộ trình URL (Tier 1: Global 1200/min, Tier 3: Admin 30/min)
func MultiTierRateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientIP := GetRealClientIP(r)

		// Bỏ qua nếu thuộc Whitelist nội bộ đáng tin cậy
		if GlobalIPJail.IsWhitelisted(clientIP) {
			next.ServeHTTP(w, r)
			return
		}

		path := r.URL.Path

		// Bỏ qua kiểm tra Rate Limit đối với các endpoint truyền tải dữ liệu tệp tin khối lớn (Chunk Upload, Streaming, Download)
		if IsDataTransferRoute(path) {
			next.ServeHTTP(w, r)
			return
		}

		// 1. Tầng 3: Kiểm tra các thao tác Quản trị / Nguy hiểm (30 requests/phút)
		if IsAdminSensitiveRoute(path) {
			allowed, remaining, retryAfter := GlobalMultiTierLimiter.AllowAdmin(clientIP)
			if !allowed {
				retrySeconds := int(retryAfter.Seconds())
				if retrySeconds < 1 {
					retrySeconds = 1
				}

				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", fmt.Sprintf("%d", retrySeconds))
				w.Header().Set("X-RateLimit-Tier", "3-Admin")
				w.Header().Set("X-RateLimit-Limit", "30")
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.WriteHeader(http.StatusTooManyRequests)

				json.NewEncoder(w).Encode(map[string]interface{}{
					"status":              "rate_limited",
					"tier":                3,
					"code":                "ERR_ADMIN_RATE_LIMITED",
					"error":               "Vượt quá giới hạn thao tác quản trị (tối đa 30 requests/phút). Vui lòng thử lại sau.",
					"retry_after_seconds": retrySeconds,
				})
				return
			}
			w.Header().Set("X-RateLimit-Tier3-Remaining", fmt.Sprintf("%d", remaining))
		}

		// 2. Tầng 1: Kiểm tra Giới hạn Toàn cục cho tất cả API endpoints (120 requests/phút)
		if strings.HasPrefix(path, "/api/") {
			allowed, remaining, retryAfter := GlobalMultiTierLimiter.AllowGlobal(clientIP)
			if !allowed {
				retrySeconds := int(retryAfter.Seconds())
				if retrySeconds < 1 {
					retrySeconds = 1
				}

				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", fmt.Sprintf("%d", retrySeconds))
				w.Header().Set("X-RateLimit-Tier", "1-Global")
				w.Header().Set("X-RateLimit-Limit", "120")
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.WriteHeader(http.StatusTooManyRequests)

				json.NewEncoder(w).Encode(map[string]interface{}{
					"status":              "rate_limited",
					"tier":                1,
					"code":                "ERR_GLOBAL_RATE_LIMITED",
					"error":               "Vượt quá giới hạn truy cập toàn cục (tối đa 120 requests/phút). Vui lòng đợi trong giây lát.",
					"retry_after_seconds": retrySeconds,
				})
				return
			}
			w.Header().Set("X-RateLimit-Tier1-Remaining", fmt.Sprintf("%d", remaining))
		}

		next.ServeHTTP(w, r)
	})
}

// GlobalRateLimitMiddleware middleware độc lập áp dụng Tầng 1 (1,200 requests/phút)
func GlobalRateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bỏ qua kiểm tra Rate Limit đối với các endpoint truyền tải dữ liệu tệp tin khối lớn
		if IsDataTransferRoute(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		clientIP := GetRealClientIP(r)
		allowed, remaining, retryAfter := GlobalMultiTierLimiter.AllowGlobal(clientIP)
		if !allowed {
			retrySeconds := int(retryAfter.Seconds())
			if retrySeconds < 1 {
				retrySeconds = 1
			}

			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", fmt.Sprintf("%d", retrySeconds))
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":              "rate_limited",
				"tier":                1,
				"code":                "ERR_GLOBAL_RATE_LIMITED",
				"error":               "Vượt quá giới hạn truy cập toàn cục (tối đa 120 requests/phút).",
				"retry_after_seconds": retrySeconds,
			})
			return
		}
		w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		next.ServeHTTP(w, r)
	})
}

// AdminRateLimitMiddleware middleware độc lập áp dụng Tầng 3 (30 requests/phút)
func AdminRateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientIP := GetRealClientIP(r)
		allowed, remaining, retryAfter := GlobalMultiTierLimiter.AllowAdmin(clientIP)
		if !allowed {
			retrySeconds := int(retryAfter.Seconds())
			if retrySeconds < 1 {
				retrySeconds = 1
			}

			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", fmt.Sprintf("%d", retrySeconds))
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":              "rate_limited",
				"tier":                3,
				"code":                "ERR_ADMIN_RATE_LIMITED",
				"error":               "Vượt quá giới hạn thao tác quản trị (tối đa 30 requests/phút).",
				"retry_after_seconds": retrySeconds,
			})
			return
		}
		w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		next.ServeHTTP(w, r)
	})
}

// LoginRateLimitMiddleware middleware kiểm tra khóa trước khi xử lý yêu cầu đăng nhập (Tầng 2)
// Ngăn chặn tấn công Brute-force & giảm tải băm BCrypt khi đang bị khóa
func LoginRateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}

		clientIP := GetRealClientIP(r)

		// Đọc một phần body để trích xuất username/email nếu có (không làm mất body cho handler sau)
		var username string
		if r.Body != nil {
			bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 16*1024)) // Giới hạn 16KB
			if err == nil && len(bodyBytes) > 0 {
				r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

				var req struct {
					UsernameOrEmail string `json:"username_or_email"`
					Username        string `json:"username"`
					Email           string `json:"email"`
				}
				if jsonErr := json.Unmarshal(bodyBytes, &req); jsonErr == nil {
					if req.UsernameOrEmail != "" {
						username = req.UsernameOrEmail
					} else if req.Username != "" {
						username = req.Username
					} else if req.Email != "" {
						username = req.Email
					}
				}
			}
		}

		// Kiểm tra trạng thái khóa (Tầng 2)
		if locked, remaining, reason := GlobalLoginRateLimiter.IsLocked(clientIP, username); locked {
			retrySeconds := int(remaining.Seconds())
			if retrySeconds < 1 {
				retrySeconds = 1
			}

			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", fmt.Sprintf("%d", retrySeconds))
			w.WriteHeader(http.StatusTooManyRequests)

			minutes := int(remaining.Minutes()) + 1
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":              "locked",
				"code":                "ERR_ACCOUNT_LOCKED",
				"error":               fmt.Sprintf("Quá 5 lần đăng nhập không thành công. %s đã bị tạm khóa trong 15 phút để bảo vệ an toàn hệ thống. Vui lòng thử lại sau %d phút.", reason, minutes),
				"retry_after_seconds": retrySeconds,
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}

// ============================================================================
// CÁC HÀM TIỆN ÍCH CẤP PACKAGE
// ============================================================================

func CheckLoginLock(ip, username string) (bool, time.Duration, string) {
	return GlobalMultiTierLimiter.CheckLoginLock(ip, username)
}

func RecordLoginFailure(ip, username string) (bool, time.Duration, int) {
	locked, dur, left := GlobalMultiTierLimiter.RecordLoginFailure(ip, username)
	failures := DefaultMaxLoginFailures - left
	if failures < 1 {
		failures = 1
	}
	RecordSIEMLoginFailure(ip, username, failures, locked, left)
	return locked, dur, left
}

func RecordLoginSuccess(ip, username string) {
	GlobalMultiTierLimiter.RecordLoginSuccess(ip, username)
	RecordSIEMLoginSuccess(ip, username)
}

func ResetLoginLimits(ip, username string) {
	GlobalLoginRateLimiter.Reset(ip, username)
	GlobalMultiTierLimiter.tier2Auth.Reset(ip, username)
}

func ResetAllRateLimits() {
	GlobalLoginRateLimiter.ResetAll()
	GlobalMultiTierLimiter.ResetAll()
	GlobalIPJail.ClearJail()
}

func AllowGlobalRequest(ip string) (bool, int, time.Duration) {
	return GlobalMultiTierLimiter.AllowGlobal(ip)
}

func AllowAdminRequest(ip string) (bool, int, time.Duration) {
	return GlobalMultiTierLimiter.AllowAdmin(ip)
}
