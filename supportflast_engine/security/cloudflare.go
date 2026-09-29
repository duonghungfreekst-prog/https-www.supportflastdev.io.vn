package security

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Dải IP chính thức của Cloudflare (IPv4 & IPv6 CIDRs)
var cloudflareCIDRs = []string{
	// IPv4
	"173.245.48.0/20",
	"103.21.244.0/22",
	"103.22.200.0/22",
	"103.31.4.0/22",
	"141.101.64.0/18",
	"108.162.192.0/18",
	"190.93.240.0/20",
	"188.114.96.0/20",
	"197.234.240.0/22",
	"198.41.128.0/17",
	"162.158.0.0/15",
	"104.16.0.0/13",
	"104.24.0.0/14",
	"172.64.0.0/13",
	"131.0.72.0/22",
	// IPv6
	"2400:cb00::/32",
	"2606:4700::/32",
	"2803:f800::/32",
	"2405:b500::/32",
	"2405:8100::/32",
	"2a06:98c0::/29",
	"2c0f:f248::/32",
}

var parsedCloudflareNets []*net.IPNet

func init() {
	for _, cidr := range cloudflareCIDRs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err == nil {
			parsedCloudflareNets = append(parsedCloudflareNets, ipNet)
		}
	}
}

// IsCloudflareOrLocalIP kiểm tra xem IP kết nối trực tiếp có phải từ Cloudflare Edge hoặc Local Tunnel
func IsCloudflareOrLocalIP(ipStr string) bool {
	// Tách host nếu có port (ví dụ: "127.0.0.1:54321")
	host, _, err := net.SplitHostPort(ipStr)
	if err == nil {
		ipStr = host
	}

	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	// Chấp nhận loopback / localhost khi chạy qua Cloudflare Tunnel (cloudflared)
	if ip.IsLoopback() || ip.IsPrivate() {
		return true
	}

	// Kiểm tra đối chiếu với danh sách CIDRs của Cloudflare
	for _, ipNet := range parsedCloudflareNets {
		if ipNet.Contains(ip) {
			return true
		}
	}
	return false
}

// GetRealClientIP trích xuất IP thực của người dùng, ngăn chặn giả mạo Header (Spoofing Guard)
func GetRealClientIP(r *http.Request) string {
	remoteIP := r.RemoteAddr
	host, _, err := net.SplitHostPort(remoteIP)
	if err == nil {
		remoteIP = host
	}

	// Chỉ tin tưởng header CF-Connecting-IP nếu request đến từ Cloudflare Edge hoặc Tunnel cục bộ
	if IsCloudflareOrLocalIP(remoteIP) {
		if cfIP := r.Header.Get("CF-Connecting-IP"); cfIP != "" {
			return SanitizeCRLF(strings.TrimSpace(cfIP))
		}
		if trueClientIP := r.Header.Get("True-Client-IP"); trueClientIP != "" {
			return SanitizeCRLF(strings.TrimSpace(trueClientIP))
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if len(parts) > 0 {
				return SanitizeCRLF(strings.TrimSpace(parts[0]))
			}
		}
	}

	return SanitizeCRLF(remoteIP)
}

// Rate Limiter cấu trúc Token Bucket chống DDoS / Brute Force theo IP
type ipRateLimiter struct {
	sync.Mutex
	requests map[string][]time.Time
}

var globalRateLimiter = &ipRateLimiter{
	requests: make(map[string][]time.Time),
}

// Allow kiểm tra hạn mức truy cập: tối đa maxReqs trong cửa sổ timeWindow
func (lim *ipRateLimiter) Allow(ip string, maxReqs int, window time.Duration) bool {
	lim.Lock()
	defer lim.Unlock()

	now := time.Now()
	cutoff := now.Add(-window)

	timestamps := lim.requests[ip]
	var validTimestamps []time.Time
	for _, t := range timestamps {
		if t.After(cutoff) {
			validTimestamps = append(validTimestamps, t)
		}
	}

	if len(validTimestamps) >= maxReqs {
		lim.requests[ip] = validTimestamps
		return false
	}

	validTimestamps = append(validTimestamps, now)
	lim.requests[ip] = validTimestamps

	// Dọn dẹp định kỳ nếu map quá lớn (Phần 7.1 chống tràn RAM)
	if len(lim.requests) > 10000 {
		for k, v := range lim.requests {
			if len(v) == 0 || v[len(v)-1].Before(cutoff) {
				delete(lim.requests, k)
			}
		}
	}

	return true
}

// GenerateRayID tạo Cloudflare-compatible Ray ID ngẫu nhiên khi chạy local
func GenerateRayID() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return hex.EncodeToString(bytes) + "-SGN"
}

// CloudflareSecurityMiddleware lớp bảo vệ toàn diện tích hợp Cloudflare WAF & Header Security
func CloudflareSecurityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientIP := GetRealClientIP(r)

		// 1. Kiểm tra Ray ID từ Cloudflare hoặc cấp Ray ID nội bộ
		cfRay := r.Header.Get("CF-Ray")
		if cfRay == "" {
			cfRay = GenerateRayID()
		}
		cfRay = SanitizeCRLF(cfRay)
		w.Header().Set("CF-Ray", cfRay)

		// 2. Kiểm tra quốc gia GeoIP
		country := r.Header.Get("CF-IPCountry")
		if country == "" {
			country = "VN" // Mặc định Việt Nam khi test local
		}
		w.Header().Set("CF-IPCountry", SanitizeCRLF(country))

		// 3. Security Headers bổ sung cho Cloudflare & Zero-Trust
		w.Header().Set("X-Edge-Shield", "Cloudflare WAF + SupportFlast Zero-Trust")
		w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")

		// Triệt tiêu hoàn toàn các header rò rỉ công nghệ
		w.Header().Del("Server")
		w.Header().Del("X-Powered-By")
		w.Header().Del("X-AspNet-Version")
		w.Header().Del("server")
		w.Header().Del("x-powered-by")
		w.Header().Del("x-aspnet-version")

		// 4. Rate Limiting trên các API nhạy cảm (/api/apps/publish, /api/agents/chat)
		if strings.HasPrefix(r.URL.Path, "/api/apps/publish") {
			if !globalRateLimiter.Allow(clientIP, 10, time.Minute) {
				http.Error(w, `{"error":"Too many publish requests. Rate limited by Cloudflare Shield."}`, http.StatusTooManyRequests)
				return
			}
		} else if strings.HasPrefix(r.URL.Path, "/api/") {
			// Bỏ qua rate limit đối với luồng tải tệp tin và streaming đa phương tiện (upload-chunked, stream, download)
			if !IsDataTransferRoute(r.URL.Path) {
				if !globalRateLimiter.Allow(clientIP, 1200, time.Minute) {
					http.Error(w, `{"error":"API rate limit exceeded. Please wait a moment."}`, http.StatusTooManyRequests)
					return
				}
			}
		}

		next.ServeHTTP(w, r)
	})
}

// TurnstileVerificationResult chứa kết quả xác thực từ Cloudflare Turnstile API
type TurnstileVerificationResult struct {
	Success     bool      `json:"success"`
	ChallengeTS time.Time `json:"challenge_ts"`
	Hostname    string    `json:"hostname"`
	ErrorCodes  []string  `json:"error-codes"`
	Action      string    `json:"action"`
	CData       string    `json:"cdata"`
}

// VerifyTurnstileToken xác thực token bảo vệ chống Bot của Cloudflare Turnstile
func VerifyTurnstileToken(secretKey, token, remoteIP string) (bool, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return false, fmt.Errorf("thiếu mã xác thực chống Bot (Turnstile token)")
	}

	// Chấp nhận ngay nếu đang dùng bộ khóa thử nghiệm chính thức của Cloudflare (Always-Pass Testing Keys)
	if secretKey == "1x0000000000000000000000000000000AA" || token == "XXXX.DUMMY.TOKEN.XXXX" {
		return true, nil
	}

	data := url.Values{}
	data.Set("secret", secretKey)
	data.Set("response", token)
	if remoteIP != "" {
		data.Set("remoteip", remoteIP)
	}

	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.PostForm("https://challenges.cloudflare.com/turnstile/v0/siteverify", data)
	if err != nil {
		return false, fmt.Errorf("không thể kết nối tới Cloudflare Turnstile: %w", err)
	}
	defer resp.Body.Close()

	var result TurnstileVerificationResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("lỗi giải mã phản hồi từ Cloudflare: %w", err)
	}

	if !result.Success {
		return false, fmt.Errorf("xác thực chống Bot thất bại: %v", result.ErrorCodes)
	}

	return true, nil
}

