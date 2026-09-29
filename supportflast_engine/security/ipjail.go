package security

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Các cấu hình mặc định cho Dynamic IP Jail (Rule 7.1 & Rule 8.2)
const (
	JailDuration15M = 15 * time.Minute // Khóa 15 phút (mức độ nhẹ / brute-force lần đầu)
	JailDuration1H  = 1 * time.Hour   // Khóa 1 giờ (mức độ trung bình / quét lỗ hổng thông thường)
	JailDuration24H = 24 * time.Hour  // Khóa 24 giờ (mức độ nghiêm trọng / tấn công khai thác lỗ hổng)
	MaxJailEntries  = 10000           // Giới hạn bộ nhớ RAM chống OOM (Rule 7.1)
)

// JailSeverity cấp độ nghiêm trọng của hành vi vi phạm
type JailSeverity int

const (
	SeverityLow JailSeverity = iota
	SeverityMedium
	SeverityHigh
)

// Các lý do phổ biến đưa IP vào Jail
const (
	ReasonBruteForce        = "Liên tục đăng nhập sai hoặc dò mật khẩu (Brute-force)"
	ReasonVulnerabilityScan = "Thăm dò và quét lỗ hổng hệ thống (Vulnerability Scanning)"
	ReasonMaliciousPayload  = "Gửi payload độc hại (SQLi, XSS, Path Traversal, Null Byte)"
	ReasonRateLimitAbuse    = "Lạm dụng tần suất truy cập vượt ngưỡng nghiêm trọng (DDoS/Spam)"
	ReasonManualBan         = "Quản trị viên hệ thống chủ động đưa vào danh sách đen"
)

// JailEntry lưu trữ thông tin một IP bị giam giữ
type JailEntry struct {
	IP           string        `json:"ip"`
	Reason       string        `json:"reason"`
	Severity     JailSeverity  `json:"severity"`
	JailedAt     time.Time     `json:"jailed_at"`
	ExpiresAt    time.Time     `json:"expires_at"`
	OffenseCount int           `json:"offense_count"`
	Duration     time.Duration `json:"duration"`
}

// IPJail phân hệ Dynamic IP Jail quản lý danh sách đen động và danh sách trắng
type IPJail struct {
	mu             sync.RWMutex
	jailed         map[string]*JailEntry
	offenseHistory map[string]int // Đếm số lần vi phạm để tự động leo thang hình phạt
	whitelist      map[string]bool
	whitelistCIDRs []*net.IPNet
	maxEntries     int
}

// NewIPJail khởi tạo một thực thể IP Jail mới với cấu hình Whitelist an toàn mặc định
func NewIPJail(maxEntries int) *IPJail {
	if maxEntries <= 0 {
		maxEntries = MaxJailEntries
	}

	jail := &IPJail{
		jailed:         make(map[string]*JailEntry),
		offenseHistory: make(map[string]int),
		whitelist:      make(map[string]bool),
		whitelistCIDRs: make([]*net.IPNet, 0),
		maxEntries:     maxEntries,
	}

	// 1. Thêm các IP nội bộ đáng tin cậy vào Whitelist mặc định
	defaultTrustedIPs := []string{
		"127.0.0.1",
		"::1",
		"localhost",
		"0.0.0.0",
	}
	for _, ip := range defaultTrustedIPs {
		jail.whitelist[ip] = true
	}

	// 2. Thêm các dải mạng nội bộ an toàn (Private IP Ranges)
	privateCIDRs := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"fc00::/7",
	}
	for _, cidr := range privateCIDRs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err == nil {
			jail.whitelistCIDRs = append(jail.whitelistCIDRs, ipNet)
		}
	}

	return jail
}

// GlobalIPJail singleton quản lý IP Jail trên toàn bộ hệ thống
var GlobalIPJail = NewIPJail(MaxJailEntries)

// AddWhitelist thêm một IP hoặc CIDR vào danh sách Whitelist đáng tin cậy
func (j *IPJail) AddWhitelist(ipOrCIDR string) {
	j.mu.Lock()
	defer j.mu.Unlock()

	clean := strings.TrimSpace(ipOrCIDR)
	if clean == "" {
		return
	}

	if strings.Contains(clean, "/") {
		_, ipNet, err := net.ParseCIDR(clean)
		if err == nil {
			j.whitelistCIDRs = append(j.whitelistCIDRs, ipNet)
			return
		}
	}

	host, _, err := net.SplitHostPort(clean)
	if err == nil {
		clean = host
	}
	j.whitelist[clean] = true
}

// RemoveWhitelist xóa IP hoặc CIDR khỏi danh sách Whitelist
func (j *IPJail) RemoveWhitelist(ipOrCIDR string) {
	j.mu.Lock()
	defer j.mu.Unlock()

	clean := strings.TrimSpace(ipOrCIDR)
	delete(j.whitelist, clean)

	var updated []*net.IPNet
	for _, netObj := range j.whitelistCIDRs {
		if netObj.String() != clean {
			updated = append(updated, netObj)
		}
	}
	j.whitelistCIDRs = updated
}

// IsWhitelisted kiểm tra xem một IP có thuộc Whitelist hay không (bỏ qua mọi hình thức Jail)
func (j *IPJail) IsWhitelisted(ipStr string) bool {
	j.mu.RLock()
	defer j.mu.RUnlock()

	clean := strings.TrimSpace(ipStr)
	host, _, err := net.SplitHostPort(clean)
	if err == nil {
		clean = host
	}

	// 1. Kiểm tra Exact Match
	if j.whitelist[clean] {
		return true
	}

	// 2. Phân tích IP và kiểm tra Loopback / Private / CIDRs
	parsedIP := net.ParseIP(clean)
	if parsedIP == nil {
		return false
	}

	if parsedIP.IsLoopback() {
		return true
	}

	for _, cidr := range j.whitelistCIDRs {
		if cidr.Contains(parsedIP) {
			return true
		}
	}

	return false
}

// GetWhitelist trả về danh sách toàn bộ IP và CIDR trong Whitelist
func (j *IPJail) GetWhitelist() []string {
	j.mu.RLock()
	defer j.mu.RUnlock()

	var list []string
	for k := range j.whitelist {
		list = append(list, k)
	}
	for _, cidr := range j.whitelistCIDRs {
		list = append(list, cidr.String())
	}
	return list
}

// Jail đưa một IP vào danh sách đen (Blacklist) với thời hạn cụ thể
func (j *IPJail) Jail(ip string, duration time.Duration, reason string, severity JailSeverity) (bool, time.Duration) {
	cleanIP := strings.TrimSpace(ip)
	host, _, err := net.SplitHostPort(cleanIP)
	if err == nil {
		cleanIP = host
	}

	if cleanIP == "" || j.IsWhitelisted(cleanIP) {
		return false, 0
	}

	if duration <= 0 {
		duration = JailDuration15M
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	now := time.Now()
	j.offenseHistory[cleanIP]++
	offenses := j.offenseHistory[cleanIP]

	entry := &JailEntry{
		IP:           cleanIP,
		Reason:       reason,
		Severity:     severity,
		JailedAt:     now,
		ExpiresAt:    now.Add(duration),
		OffenseCount: offenses,
		Duration:     duration,
	}

	j.jailed[cleanIP] = entry

	// Kiểm tra dọn dẹp dung lượng RAM (Rule 7.1)
	if len(j.jailed) > j.maxEntries {
		j.cleanupUnsafe(now)
	}

	cleanLogReason := SanitizeCRLF(reason)
	log.Printf("[ENGINE] [IP_JAIL] IP %s đã bị đưa vào Jail trong %v (Lần vi phạm: %d). Lý do: %s",
		SanitizeCRLF(cleanIP), duration, offenses, cleanLogReason)

	return true, duration
}

// RecordAttack tự động ghi nhận hành vi tấn công, tính toán leo thang án phạt và tống vào Jail
func (j *IPJail) RecordAttack(ip string, reason string, severity JailSeverity) (bool, time.Duration) {
	cleanIP := strings.TrimSpace(ip)
	host, _, err := net.SplitHostPort(cleanIP)
	if err == nil {
		cleanIP = host
	}

	if cleanIP == "" || j.IsWhitelisted(cleanIP) {
		return false, 0
	}

	j.mu.RLock()
	priorOffenses := j.offenseHistory[cleanIP]
	j.mu.RUnlock()

	var duration time.Duration

	// Thuật toán xác định thời hạn giam giữ và tự động leo thang:
	// - Severity High: Tấn công trực tiếp vào bảo mật (SQLi, XSS, Path Traversal, Null byte) -> 24 giờ
	// - Tái phạm lần 3 trở lên -> 24 giờ
	// - Tái phạm lần 2 -> 1 giờ
	// - Vi phạm lần đầu:
	//   * Severity Low -> 15 phút
	//   * Severity Medium -> 1 giờ
	//   * Severity High -> 24 giờ
	if severity == SeverityHigh || priorOffenses >= 2 {
		duration = JailDuration24H
	} else if severity == SeverityMedium || priorOffenses == 1 {
		duration = JailDuration1H
	} else {
		duration = JailDuration15M
	}

	return j.Jail(cleanIP, duration, reason, severity)
}

// Unjail gỡ bỏ một IP khỏi danh sách đen
func (j *IPJail) Unjail(ip string) bool {
	cleanIP := strings.TrimSpace(ip)
	host, _, err := net.SplitHostPort(cleanIP)
	if err == nil {
		cleanIP = host
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	if _, exists := j.jailed[cleanIP]; exists {
		delete(j.jailed, cleanIP)
		log.Printf("[ENGINE] [IP_JAIL] IP %s đã được gỡ khỏi Jail", SanitizeCRLF(cleanIP))
		return true
	}
	return false
}

// IsJailed kiểm tra xem IP có đang bị giam giữ hay không.
// Trả về: (đang_bị_giam, thời_gian_còn_lại, lý_do)
func (j *IPJail) IsJailed(ip string) (bool, time.Duration, string) {
	cleanIP := strings.TrimSpace(ip)
	host, _, err := net.SplitHostPort(cleanIP)
	if err == nil {
		cleanIP = host
	}

	if cleanIP == "" || j.IsWhitelisted(cleanIP) {
		return false, 0, ""
	}

	now := time.Now()

	j.mu.RLock()
	entry, exists := j.jailed[cleanIP]
	if !exists {
		j.mu.RUnlock()
		return false, 0, ""
	}

	// Nếu đã hết hạn giam giữ
	if now.After(entry.ExpiresAt) {
		j.mu.RUnlock()
		// Nâng lên Write Lock để xóa entry hết hạn
		j.mu.Lock()
		delete(j.jailed, cleanIP)
		j.mu.Unlock()
		return false, 0, ""
	}

	remaining := entry.ExpiresAt.Sub(now)
	reason := entry.Reason
	j.mu.RUnlock()

	return true, remaining, reason
}

// GetJailList trả về danh sách tất cả các IP đang bị giam giữ còn hiệu lực
func (j *IPJail) GetJailList() []JailEntry {
	j.mu.RLock()
	defer j.mu.RUnlock()

	now := time.Now()
	var list []JailEntry
	for _, entry := range j.jailed {
		if now.Before(entry.ExpiresAt) {
			list = append(list, *entry)
		}
	}
	return list
}

// ClearJail xóa sạch toàn bộ danh sách giam giữ và lịch sử vi phạm (phục vụ test)
func (j *IPJail) ClearJail() {
	j.mu.Lock()
	defer j.mu.Unlock()

	j.jailed = make(map[string]*JailEntry)
	j.offenseHistory = make(map[string]int)
}

// cleanupUnsafe dọn dẹp các entry đã hết hạn (chỉ gọi khi đã giữ Lock)
func (j *IPJail) cleanupUnsafe(now time.Time) {
	for ip, entry := range j.jailed {
		if now.After(entry.ExpiresAt) {
			delete(j.jailed, ip)
		}
	}
}

// Cleanup dọn dẹp an toàn các bản ghi đã hết hạn
func (j *IPJail) Cleanup() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.cleanupUnsafe(time.Now())
}

// Danh sách các mẫu đường dẫn URL thường gặp trong các đợt quét lỗ hổng tự động
var suspiciousScanPatterns = []string{
	"/.env",
	"/.git",
	"/.svn",
	"/.htaccess",
	"/.htpasswd",
	"/web.config",
	"/config.json",
	"/wp-admin",
	"/wp-login",
	"/wp-includes",
	"/xmlrpc.php",
	"/phpmyadmin",
	"/pma",
	"/adminer",
	"/mysqladmin",
	"/etc/passwd",
	"/etc/shadow",
	"/proc/self",
	"/windows/system32",
	"/boot.ini",
	"/cgi-bin/",
	"/shell.php",
	"/eval-stdin.php",
	"/.aws/credentials",
	"/.ssh/",
	"/actuator",
}

// DetectVulnerabilityScan phân tích đường dẫn URL để phát hiện hành vi quét lỗ hổng
func DetectVulnerabilityScan(path string) (bool, string) {
	lowerPath := strings.ToLower(path)

	// 1. Kiểm tra Directory Traversal
	if strings.Contains(path, "../") || strings.Contains(path, "..\\") {
		return true, "Directory traversal attack detected in request path"
	}

	// 2. Kiểm tra Null Byte
	if strings.Contains(path, "\x00") {
		return true, "Null byte detected in request path"
	}

	// 3. Đối chiếu các mẫu quét lỗ hổng phổ biến
	for _, pattern := range suspiciousScanPatterns {
		if strings.Contains(lowerPath, pattern) {
			return true, fmt.Sprintf("Vulnerability scanning detected targeting pattern: %s", pattern)
		}
	}

	return false, ""
}

// DetectMaliciousRequest phân tích request bao gồm path và raw query xem có dấu hiệu tấn công hay không
func DetectMaliciousRequest(r *http.Request) (bool, string, JailSeverity) {
	// 1. Kiểm tra path
	if isScan, reason := DetectVulnerabilityScan(r.URL.Path); isScan {
		// Nếu là traversal hoặc system files -> Severity High (24h)
		if strings.Contains(r.URL.Path, "..") || strings.Contains(r.URL.Path, "passwd") || strings.Contains(r.URL.Path, "\x00") {
			return true, reason, SeverityHigh
		}
		return true, reason, SeverityMedium
	}

	// 2. Kiểm tra RawQuery sử dụng ValidateInput
	rawQuery := r.URL.RawQuery
	if rawQuery != "" {
		valid, reason := ValidateInput(rawQuery)
		if !valid {
			return true, fmt.Sprintf("Malicious query payload: %s", reason), SeverityHigh
		}
	}

	return false, "", SeverityLow
}

// IPJailMiddleware middleware chặn đứng mọi request từ các IP nằm trong Blacklist
// Lập tức trả về HTTP 403 Forbidden với thông báo ngắn gọn tuân thủ Rule 3.5
func IPJailMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientIP := GetRealClientIP(r)

		// 1. Nếu thuộc danh sách Whitelist -> Bỏ qua kiểm tra Jail
		if GlobalIPJail.IsWhitelisted(clientIP) {
			next.ServeHTTP(w, r)
			return
		}

		// 2. Kiểm tra xem IP hiện có đang bị giam giữ hay không
		if isJailed, remaining, _ := GlobalIPJail.IsJailed(clientIP); isJailed {
			retrySeconds := int(remaining.Seconds())
			if retrySeconds < 1 {
				retrySeconds = 1
			}

			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", fmt.Sprintf("%d", retrySeconds))
			w.WriteHeader(http.StatusForbidden)

			// Trả về thông báo lỗi ngắn gọn, generic theo Rule 3.5 (không để lộ chi tiết nội bộ)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":              "forbidden",
				"code":                "ERR_IP_BANNED",
				"error":               "Truy cập bị từ chối do địa chỉ IP vi phạm chính sách bảo mật hệ thống.",
				"retry_after_seconds": retrySeconds,
			})
			return
		}

		// 3. Tự động phát hiện hành vi quét lỗ hổng hoặc gửi payload độc hại ngay trong request
		if isAttack, attackReason, severity := DetectMaliciousRequest(r); isAttack {
			cleanIP := SanitizeCRLF(clientIP)
			cleanReason := SanitizeCRLF(attackReason)
			log.Printf("[ENGINE] [SECURITY_ALERT] Phát hiện tấn công từ IP %s: %s", cleanIP, cleanReason)

			// Tự động tống IP vào Jail với án phạt thích đáng
			_, remaining := GlobalIPJail.RecordAttack(clientIP, attackReason, severity)
			retrySeconds := int(remaining.Seconds())
			if retrySeconds < 1 {
				retrySeconds = 1
			}

			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", fmt.Sprintf("%d", retrySeconds))
			w.WriteHeader(http.StatusForbidden)

			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":              "forbidden",
				"code":                "ERR_IP_BANNED",
				"error":               "Yêu cầu chứa mẫu tấn công nguy hiểm. Địa chỉ IP của bạn đã bị đưa vào danh sách đen.",
				"retry_after_seconds": retrySeconds,
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Các hàm tiện ích cấp package gọi GlobalIPJail
func JailIP(ip string, duration time.Duration, reason string, severity JailSeverity) (bool, time.Duration) {
	return GlobalIPJail.Jail(ip, duration, reason, severity)
}

func UnjailIP(ip string) bool {
	return GlobalIPJail.Unjail(ip)
}

func IsIPJailed(ip string) (bool, time.Duration, string) {
	return GlobalIPJail.IsJailed(ip)
}

func RecordIPAttack(ip string, reason string, severity JailSeverity) (bool, time.Duration) {
	return GlobalIPJail.RecordAttack(ip, reason, severity)
}

func AddIPWhitelist(ipOrCIDR string) {
	GlobalIPJail.AddWhitelist(ipOrCIDR)
}

func RemoveIPWhitelist(ipOrCIDR string) {
	GlobalIPJail.RemoveWhitelist(ipOrCIDR)
}

func IsIPWhitelisted(ip string) bool {
	return GlobalIPJail.IsWhitelisted(ip)
}
