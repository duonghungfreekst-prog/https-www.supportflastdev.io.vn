package security

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"supportflast_engine/database"
)

// Cấu hình Honeypot theo Rule Phần 5 & Phần 7.1
const (
	// HoneypotJailDuration: Thời hạn khóa 24 giờ cho kẻ tấn công xâm phạm Honeypot
	HoneypotJailDuration = 24 * time.Hour

	// MaxWAFAlertHistory: Giới hạn lịch sử cảnh báo WAF lưu trong RAM chống OOM (Rule 7.1)
	MaxWAFAlertHistory = 1000

	// DefaultHoneypotDelay: Độ trễ phản hồi giả lập để làm chậm và tiêu hao tài nguyên scanner/bot
	DefaultHoneypotDelay = 50 * time.Millisecond
)

// DecoyTraps chứa danh sách các route bẫy nhử mà bot và hacker hay quét (Rule Phần 5)
var DecoyTraps = []string{
	"/.env",
	"/wp-admin",
	"/wp-admin/",
	"/phpmyadmin",
	"/phpmyadmin/",
	"/api/admin/shell",
	"/.git/config",
	"/.git/",
	"/config.json",
	"/actuator/health",
	"/actuator/",
}

var decoyTrapMap = map[string]bool{
	"/.env":            true,
	"/wp-admin":        true,
	"/wp-admin/":       true,
	"/phpmyadmin":      true,
	"/phpmyadmin/":     true,
	"/api/admin/shell": true,
	"/.git/config":     true,
	"/.git/":           true,
	"/config.json":     true,
	"/actuator/health": true,
	"/actuator/":       true,
}

var currentHoneypotDelay int64 = int64(DefaultHoneypotDelay)

// SetHoneypotDelay cho phép cấu hình độ trễ giả lập (set 0 trong unit test để test chạy nhanh)
func SetHoneypotDelay(d time.Duration) {
	atomic.StoreInt64(&currentHoneypotDelay, int64(d))
}

// GetHoneypotDelay lấy độ trễ phản hồi bẫy hiện tại
func GetHoneypotDelay() time.Duration {
	return time.Duration(atomic.LoadInt64(&currentHoneypotDelay))
}

// IsDecoyTrapPath kiểm tra xem một đường dẫn URL có thuộc danh sách bẫy nhử hay không
func IsDecoyTrapPath(path string) bool {
	clean := strings.ToLower(strings.TrimSpace(path))
	if decoyTrapMap[clean] {
		return true
	}
	// Kiểm tra các biến thể hay gặp của bẫy nhử
	if strings.HasPrefix(clean, "/wp-admin") ||
		strings.HasPrefix(clean, "/phpmyadmin") ||
		strings.HasPrefix(clean, "/.git/") ||
		strings.HasPrefix(clean, "/actuator/") ||
		strings.HasPrefix(clean, "/api/admin/shell") ||
		strings.HasPrefix(clean, "/.env") {
		return true
	}
	return false
}

// WAFAlert cấu trúc lưu trữ thông tin cảnh báo an ninh WAF
type WAFAlert struct {
	ID        string    `json:"id"`
	IP        string    `json:"ip"`
	Path      string    `json:"path"`
	UserAgent string    `json:"user_agent"`
	Timestamp time.Time `json:"timestamp"`
	Severity  string    `json:"severity"`
	Action    string    `json:"action"`
	Message   string    `json:"message"`
}

// WAFAlarmManager quản lý kích hoạt báo động WAF toàn cục
type WAFAlarmManager struct {
	mu        sync.RWMutex
	alerts    []WAFAlert
	maxAlerts int
}

// NewWAFAlarmManager khởi tạo bộ quản lý cảnh báo WAF mới
func NewWAFAlarmManager(maxAlerts int) *WAFAlarmManager {
	if maxAlerts <= 0 {
		maxAlerts = MaxWAFAlertHistory
	}
	return &WAFAlarmManager{
		alerts:    make([]WAFAlert, 0, maxAlerts),
		maxAlerts: maxAlerts,
	}
}

// GlobalWAFAlarmManager singleton quản lý báo động WAF toàn hệ thống
var GlobalWAFAlarmManager = NewWAFAlarmManager(MaxWAFAlertHistory)

// TriggerAlarm kích hoạt cơ chế báo động WAF khi bẫy Honeypot bị xâm phạm
func (m *WAFAlarmManager) TriggerAlarm(ip, path, userAgent, message string) WAFAlert {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	cleanIP := SanitizeCRLF(ip)
	cleanPath := SanitizeCRLF(path)
	cleanUA := SanitizeCRLF(userAgent)
	cleanMsg := SanitizeCRLF(message)

	alert := WAFAlert{
		ID:        fmt.Sprintf("WAF-TRAP-%d", now.UnixNano()),
		IP:        cleanIP,
		Path:      cleanPath,
		UserAgent: cleanUA,
		Timestamp: now,
		Severity:  "CRITICAL",
		Action:    "IP_JAILED_24H",
		Message:   cleanMsg,
	}

	// Ghi log báo động WAF chuẩn tiền tố [TRAP] [WAF] theo Rule 4.4
	log.Printf("[TRAP] [WAF] ALARM ACTIVATED: IP %s blacklisted in IP Jail for 24h | Cause=%s | Decoy=%s",
		cleanIP, cleanMsg, cleanPath)

	if len(m.alerts) >= m.maxAlerts {
		// Trượt danh sách để giữ maxAlerts bản ghi mới nhất chống tràn RAM (Rule 7.1)
		m.alerts = append(m.alerts[1:], alert)
	} else {
		m.alerts = append(m.alerts, alert)
	}

	return alert
}

// GetAlerts trả về danh sách lịch sử cảnh báo WAF gần nhất
func (m *WAFAlarmManager) GetAlerts() []WAFAlert {
	m.mu.RLock()
	defer m.mu.RUnlock()
	res := make([]WAFAlert, len(m.alerts))
	copy(res, m.alerts)
	return res
}

// Reset xóa sạch danh sách cảnh báo WAF (dùng cho test)
func (m *WAFAlarmManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alerts = make([]WAFAlert, 0, m.maxAlerts)
}

// HoneypotTrapHandler xử lý yêu cầu gửi tới các route bẫy nhử (Decoy Traps)
// 1. Sanitize CRLF các trường IP, User-Agent, Path, Time (Rule 3.6)
// 2. Ghi log an ninh chuẩn tiền tố [TRAP] [HONEYPOT] (Rule 4.4)
// 3. Tự động kích hoạt cơ chế báo động WAF
// 4. Lập tức chuyển IP vi phạm vào danh sách Blacklist của IP Jail (khóa 24 giờ)
// 5. Trả về phản hồi giả lập chậm trễ hoặc HTTP 404/403 chung chung, không để lộ bẫy (Rule 3.5)
func HoneypotTrapHandler(w http.ResponseWriter, r *http.Request) {
	clientIP := GetRealClientIP(r)
	ua := r.UserAgent()
	path := r.URL.Path
	now := time.Now()

	// 1. Sanitize CRLF toàn bộ thông tin đầu vào chống Log Injection (Rule 3.6)
	cleanIP := SanitizeCRLF(clientIP)
	cleanUA := SanitizeCRLF(ua)
	cleanPath := SanitizeCRLF(path)
	cleanTime := SanitizeCRLF(now.Format(time.RFC3339))

	// a) Ghi log an ninh đặc biệt với tiền tố chuẩn [TRAP] [HONEYPOT] (Rule 4.4)
	log.Printf("[TRAP] [HONEYPOT] Intrusion detected! IP=%s, UA=%s, Path=%s, Time=%s",
		cleanIP, cleanUA, cleanPath, cleanTime)

	// b) Tự động kích hoạt cơ chế báo động WAF
	GlobalWAFAlarmManager.TriggerAlarm(clientIP, path, ua, "Probing honeypot decoy trap: "+cleanPath)

	// c) Lập tức chuyển IP vi phạm vào danh sách Blacklist của IP Jail (khóa 24 giờ)
	GlobalIPJail.Jail(clientIP, HoneypotJailDuration, "Triggered honeypot decoy trap "+cleanPath, SeverityHigh)

	// d) Tự động đẩy sự kiện đáng ngờ sang SIEM AI để phân tích mối đe dọa (Rule Phần 5)
	RecordHoneypotTrap(clientIP, path, r.Method, ua, r.URL.RawQuery)

	// e) Lưu vết an ninh vĩnh viễn vào SQLite (audit_logs & security_events) bằng Prepared Statement
	expiresAt := now.Add(HoneypotJailDuration)
	_ = database.RecordAuditLog("", database.AuditActionHoneypotAccess, clientIP, ua, fmt.Sprintf("Honeypot decoy trap triggered on path: %s", cleanPath))
	_ = database.RecordSecurityEvent("honeypot_trap", clientIP, "critical", fmt.Sprintf("IP jailed for 24h: Triggered honeypot decoy trap '%s'", cleanPath), &expiresAt)

	// d) Trả về phản hồi giả lập chậm trễ để làm chậm bot/scanner
	if delay := GetHoneypotDelay(); delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}

	// Phản hồi lỗi chung chung giả lập máy chủ bình thường theo Rule 3.5 (không để lộ Honeypot)
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Not Found","code":"ERR_NOT_FOUND"}`))
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("404 page not found\n"))
	}
}

// RegisterHoneypotRoutes đăng ký toàn bộ các route bẫy nhử vào ServeMux
func RegisterHoneypotRoutes(mux *http.ServeMux) {
	traps := []string{
		"/.env",
		"/wp-admin",
		"/wp-admin/",
		"/phpmyadmin",
		"/phpmyadmin/",
		"/api/admin/shell",
		"/.git/config",
		"/.git/",
		"/config.json",
		"/actuator/health",
		"/actuator/",
	}

	for _, trap := range traps {
		mux.HandleFunc(trap, HoneypotTrapHandler)
	}

	log.Printf("[TRAP] [INIT] Successfully registered %d decoy honeypot trap routes", len(traps))
}

// HoneypotStatusHandler cung cấp API thống kê tình trạng bẫy Honeypot & IP Jail
func HoneypotStatusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	jailedList := GlobalIPJail.GetJailList()
	alerts := GlobalWAFAlarmManager.GetAlerts()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":          "active",
		"system":          "SupportFlast Honeypot & WAF Defense Trap",
		"jailed_ip_count": len(jailedList),
		"jailed_ips":      jailedList,
		"waf_alert_count": len(alerts),
		"recent_alerts":   alerts,
		"decoy_traps":     DecoyTraps,
		"timestamp":       time.Now().Format(time.RFC3339),
	})
}
