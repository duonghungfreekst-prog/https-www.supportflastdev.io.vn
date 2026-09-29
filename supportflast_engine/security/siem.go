package security

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Cấu hình SIEM AI Client theo Rule Phần 5 và Rule 7.1
const (
	DefaultSIEMQueueSize = 2000
	DefaultSIEMWorkers   = 2
	DefaultSIEMTimeout   = 2 * time.Second
)

// SecurityEventType định nghĩa các loại sự kiện an ninh
type SecurityEventType string

const (
	EventAuditLog      SecurityEventType = "AUDIT_LOG"
	EventHoneypotTrap  SecurityEventType = "HONEYPOT_TRAP"
	EventBruteForce    SecurityEventType = "BRUTE_FORCE"
	EventPayloadAttack SecurityEventType = "PAYLOAD_ATTACK"
	EventIPAccess      SecurityEventType = "IP_ACCESS"
)

// SecurityEvent cấu trúc dữ liệu sự kiện an ninh gửi tới SIEM AI
type SecurityEvent struct {
	ID         string                 `json:"event_id,omitempty"`
	Type       SecurityEventType      `json:"event_type"`
	ClientIP   string                 `json:"client_ip"`
	Path       string                 `json:"path,omitempty"`
	Method     string                 `json:"method,omitempty"`
	UserAgent  string                 `json:"user_agent,omitempty"`
	Payload    string                 `json:"payload,omitempty"`
	StatusCode int                    `json:"status_code,omitempty"`
	Timestamp  string                 `json:"timestamp,omitempty"`
	Details    map[string]interface{} `json:"details,omitempty"`
}

// ThreatAnalysisResult kết quả đánh giá phân tích nguy cơ từ SIEM AI
type ThreatAnalysisResult struct {
	EventID            string                 `json:"event_id"`
	EventType          string                 `json:"event_type"`
	ClientIP           string                 `json:"client_ip"`
	Path               string                 `json:"path"`
	Method             string                 `json:"method"`
	ThreatScore        int                    `json:"threat_score"`
	RiskLevel          string                 `json:"risk_level"`
	RiskColor          string                 `json:"risk_color"`
	ActionRecommended  string                 `json:"action_recommended"`
	ThreatIndicators   []string               `json:"threat_indicators"`
	IPReputation       string                 `json:"ip_reputation"`
	Summary            string                 `json:"summary"`
	Timestamp          string                 `json:"timestamp"`
	Details            map[string]interface{} `json:"details,omitempty"`
}

// ThreatAlert cấu trúc bản ghi cảnh báo an ninh được lưu trong SIEM AI
type ThreatAlert struct {
	AlertID            string   `json:"alert_id"`
	EventID            string   `json:"event_id"`
	ClientIP           string   `json:"client_ip"`
	ThreatScore        int      `json:"threat_score"`
	RiskLevel          string   `json:"risk_level"`
	RiskColor          string   `json:"risk_color"`
	Summary            string   `json:"summary"`
	ActionRecommended  string   `json:"action_recommended"`
	ThreatIndicators   []string `json:"threat_indicators"`
	Timestamp          string   `json:"timestamp"`
	Path               string   `json:"path"`
	Method             string   `json:"method"`
}

type ThreatAlertsResponse struct {
	Status             string        `json:"status"`
	TotalAlertsStored  int           `json:"total_alerts_stored"`
	ReturnedCount      int           `json:"returned_count"`
	Alerts             []ThreatAlert `json:"alerts"`
}

// SIEMClient quản lý kết nối và đẩy bất đồng bộ sự kiện từ Go Engine sang SIEM AI
type SIEMClient struct {
	aiURL      string
	httpClient *http.Client
	eventQueue chan SecurityEvent
	stopChan   chan struct{}
	wg         sync.WaitGroup
	mu         sync.RWMutex
	isClosed   bool
}

// NewSIEMClient khởi tạo SIEMClient với worker pool và buffered channel chống block (Rule 7.1)
func NewSIEMClient(aiURL string, queueSize int, workers int) *SIEMClient {
	if aiURL == "" {
		if env := os.Getenv("AI_ENGINE_URL"); env != "" {
			aiURL = strings.TrimRight(env, "/")
		} else {
			aiURL = "http://127.0.0.1:8000"
		}
	}
	if queueSize <= 0 {
		queueSize = DefaultSIEMQueueSize
	}
	if workers <= 0 {
		workers = DefaultSIEMWorkers
	}

	client := &SIEMClient{
		aiURL: aiURL,
		httpClient: &http.Client{
			Timeout: DefaultSIEMTimeout,
		},
		eventQueue: make(chan SecurityEvent, queueSize),
		stopChan:   make(chan struct{}),
	}

	// Khởi động các background worker xử lý sự kiện bất đồng bộ
	client.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go client.workerLoop(i + 1)
	}

	log.Printf("[SIEM] [INIT] SIEM AI Client initialized (Target: %s, Queue: %d, Workers: %d)",
		aiURL, queueSize, workers)
	return client
}

// GlobalSIEMClient singleton quản lý kết nối SIEM AI toàn hệ thống Go Engine
var GlobalSIEMClient = NewSIEMClient("", DefaultSIEMQueueSize, DefaultSIEMWorkers)

// SetAIEngineURL cấu hình lại địa chỉ URL của AI Engine
func (c *SIEMClient) SetAIEngineURL(url string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.aiURL = strings.TrimRight(url, "/")
}

func (c *SIEMClient) getURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.aiURL
}

func getInternalServiceToken() string {
	if s := strings.TrimSpace(os.Getenv("INTERNAL_SERVICE_SECRET")); s != "" {
		return s
	}
	if s := strings.TrimSpace(os.Getenv("JWT_SECRET")); s != "" {
		return s
	}
	return "sf_internal_service_secret_2026"
}

// PushEvent đẩy sự kiện vào hàng đợi xử lý bất đồng bộ không gây nghẽn (Non-blocking theo Rule 7.1)
func (c *SIEMClient) PushEvent(evt SecurityEvent) {
	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return
	}
	c.mu.RUnlock()

	if evt.Timestamp == "" {
		evt.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	select {
	case c.eventQueue <- evt:
		// Đã đưa vào queue thành công
	default:
		// Queue đầy: drop để bảo vệ tài nguyên RAM của Go Engine, tránh OOM (Rule 7.1)
		cleanIP := SanitizeCRLF(evt.ClientIP)
		log.Printf("[SIEM] [WARN] Event queue full (size=%d), dropping event for IP %s to protect RAM",
			cap(c.eventQueue), cleanIP)
	}
}

// workerLoop lắng nghe hàng đợi sự kiện và gửi sang SIEM AI
func (c *SIEMClient) workerLoop(workerID int) {
	defer c.wg.Done()
	for {
		select {
		case <-c.stopChan:
			return
		case evt, ok := <-c.eventQueue:
			if !ok {
				return
			}
			c.dispatchToAI(evt)
		}
	}
}

// dispatchToAI gửi một sự kiện sang endpoint /siem/analyze của AI Engine
func (c *SIEMClient) dispatchToAI(evt SecurityEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultSIEMTimeout)
	defer cancel()

	bodyBytes, err := json.Marshal(evt)
	if err != nil {
		log.Printf("[SIEM] [ERROR] Failed to marshal security event: %v", err)
		return
	}

	url := c.getURL() + "/siem/analyze"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		log.Printf("[SIEM] [ERROR] Failed to create HTTP request to SIEM AI: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "SupportFlast-GoEngine/SIEM-Client")
	req.Header.Set("X-Internal-Token", getInternalServiceToken())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Log nhẹ nếu AI Engine chưa khởi động để không spam log
		cleanIP := SanitizeCRLF(evt.ClientIP)
		log.Printf("[SIEM] [DISPATCH] AI Engine offline or unreachable for IP %s: %v", cleanIP, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var result ThreatAnalysisResult
		if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
			// Nếu phát hiện nguy cơ Critical (>= 80) hoặc Honeypot -> Ghi log an ninh chuẩn [SIEM]
			if result.ThreatScore >= 80 {
				log.Printf("[SIEM] [CRITICAL THREAT] Score=%d/100 | IP=%s | Action=%s | Summary=%s",
					result.ThreatScore, result.ClientIP, result.ActionRecommended, result.Summary)
				
				// Tự động bảo vệ phối hợp: Nếu chưa vào IPJail và hành động là JAIL_IP_24H -> đưa vào IPJail
				if result.ActionRecommended == "JAIL_IP_24H" {
					if jailed, _, _ := GlobalIPJail.IsJailed(result.ClientIP); !jailed {
						GlobalIPJail.Jail(result.ClientIP, JailDuration24H, result.Summary, SeverityHigh)
					}
				}
			} else if result.ThreatScore >= 30 {
				log.Printf("[SIEM] [WARNING] Score=%d/100 | IP=%s | Summary=%s",
					result.ThreatScore, result.ClientIP, result.Summary)
			}
		}
	}
}

// AnalyzeEventSync phân tích sự kiện đồng bộ trực tiếp khi cần kết quả tức thì
func (c *SIEMClient) AnalyzeEventSync(ctx context.Context, evt SecurityEvent) (*ThreatAnalysisResult, error) {
	if evt.Timestamp == "" {
		evt.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	bodyBytes, err := json.Marshal(evt)
	if err != nil {
		return nil, fmt.Errorf("marshal event error: %w", err)
	}

	url := c.getURL() + "/siem/analyze"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request error: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", getInternalServiceToken())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call SIEM AI error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SIEM AI returned status code %d", resp.StatusCode)
	}

	var res ThreatAnalysisResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("decode response error: %w", err)
	}
	return &res, nil
}

// GetRecentAlerts lấy danh sách các cảnh báo an ninh mới nhất từ SIEM AI
func (c *SIEMClient) GetRecentAlerts(limit int, minScore int, riskLevel string) ([]ThreatAlert, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultSIEMTimeout)
	defer cancel()

	url := fmt.Sprintf("%s/siem/alerts?limit=%d&min_score=%d", c.getURL(), limit, minScore)
	if riskLevel != "" {
		url += fmt.Sprintf("&risk_level=%s", riskLevel)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Internal-Token", getInternalServiceToken())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SIEM AI returned status code %d", resp.StatusCode)
	}

	var alertResp ThreatAlertsResponse
	if err := json.NewDecoder(resp.Body).Decode(&alertResp); err != nil {
		return nil, err
	}
	return alertResp.Alerts, nil
}

// GetSIEMStats lấy thống kê tổng quan và chỉ số tài nguyên từ SIEM AI
func (c *SIEMClient) GetSIEMStats() (map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultSIEMTimeout)
	defer cancel()

	url := c.getURL() + "/siem/stats"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Internal-Token", getInternalServiceToken())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var stats map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return nil, err
	}
	return stats, nil
}

// Stop dừng an toàn các worker background
func (c *SIEMClient) Stop() {
	c.mu.Lock()
	if c.isClosed {
		c.mu.Unlock()
		return
	}
	c.isClosed = true
	c.mu.Unlock()

	close(c.stopChan)
	c.wg.Wait()
	log.Printf("[SIEM] SIEM AI Client stopped cleanly")
}

// ==============================================================================
// CÁC HÀM TRỢ GIÚP TIỆN ÍCH CẤP PACKAGE GỌI GLOBAL SIEM CLIENT
// ==============================================================================

// RecordAuditEvent ghi nhận sự kiện nhật ký kiểm toán (Audit Log)
func RecordAuditEvent(userID, username, action, resource, ip, status, details string) {
	GlobalSIEMClient.PushEvent(SecurityEvent{
		Type:       EventAuditLog,
		ClientIP:   ip,
		Path:       resource,
		StatusCode: 200,
		Details: map[string]interface{}{
			"user_id":  userID,
			"username": username,
			"action":   action,
			"resource": resource,
			"status":   status,
			"details":  details,
		},
	})
}

// RecordHoneypotTrap ghi nhận sự kiện bot hoặc hacker kích hoạt bẫy nhử Honeypot
func RecordHoneypotTrap(ip, path, method, userAgent, rawQuery string) {
	GlobalSIEMClient.PushEvent(SecurityEvent{
		Type:      EventHoneypotTrap,
		ClientIP:  ip,
		Path:      path,
		Method:    method,
		UserAgent: userAgent,
		Payload:   rawQuery,
		Details: map[string]interface{}{
			"trigger_type": "decoy_trap",
			"severity":     "CRITICAL",
		},
	})
}

// RecordSIEMLoginFailure ghi nhận sự kiện đăng nhập thất bại / Brute-force
func RecordSIEMLoginFailure(ip, username string, failures int, isLocked bool, left int) {
	GlobalSIEMClient.PushEvent(SecurityEvent{
		Type:     EventBruteForce,
		ClientIP: ip,
		Path:     "/api/auth/login",
		Method:   "POST",
		Details: map[string]interface{}{
			"username":      username,
			"failures":      failures,
			"is_locked":     isLocked,
			"attempts_left": left,
		},
	})
}

// RecordSIEMLoginSuccess ghi nhận đăng nhập thành công
func RecordSIEMLoginSuccess(ip, username string) {
	GlobalSIEMClient.PushEvent(SecurityEvent{
		Type:     EventIPAccess,
		ClientIP: ip,
		Path:     "/api/auth/login",
		Method:   "POST",
		Details: map[string]interface{}{
			"username": username,
			"status":   "success",
		},
	})
}

// RecordPayloadAttack ghi nhận sự kiện gửi payload độc hại (SQLi, XSS, Path Traversal)
func RecordPayloadAttack(ip, attackType, payload, path, method, userAgent string) {
	GlobalSIEMClient.PushEvent(SecurityEvent{
		Type:      EventPayloadAttack,
		ClientIP:  ip,
		Path:      path,
		Method:    method,
		UserAgent: userAgent,
		Payload:   payload,
		Details: map[string]interface{}{
			"attack_type": attackType,
			"severity":    "CRITICAL",
		},
	})
}

// RecordIPAccess ghi nhận nhật ký truy cập IP thông thường
func RecordIPAccess(ip, path, method, userAgent string, statusCode int) {
	GlobalSIEMClient.PushEvent(SecurityEvent{
		Type:       EventIPAccess,
		ClientIP:   ip,
		Path:       path,
		Method:     method,
		UserAgent:  userAgent,
		StatusCode: statusCode,
	})
}

// SIEMAlertsHandler cung cấp API cho admin hoặc dashboard lấy danh sách cảnh báo SIEM AI
func SIEMAlertsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	alerts, err := GlobalSIEMClient.GetRecentAlerts(50, 0, "")
	if err != nil {
		log.Printf("[SIEM] [WARN] Cannot fetch alerts from SIEM AI: %v", err)
		// Trả về cảnh báo WAF cục bộ nếu SIEM AI chưa online
		wafAlerts := GlobalWAFAlarmManager.GetAlerts()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":       "fallback_waf",
			"source":       "go_engine_waf",
			"total_alerts": len(wafAlerts),
			"alerts":       wafAlerts,
			"note":         "SIEM AI offline, fallback to local WAF Alarm history",
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":       "success",
		"source":       "siem_ai",
		"total_alerts": len(alerts),
		"alerts":       alerts,
		"timestamp":    time.Now().Format(time.RFC3339),
	})
}

// SIEMStatsHandler cung cấp API thống kê tình trạng SIEM AI và đo lường bộ nhớ (Rule 7.1 & 7.4)
func SIEMStatsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	stats, err := GlobalSIEMClient.GetSIEMStats()
	if err != nil {
		log.Printf("[SIEM] [WARN] Cannot fetch stats from SIEM AI: %v", err)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":   "warning",
			"service":  "supportflast_engine_siem_proxy",
			"error":    "SIEM AI Engine is currently offline or unreachable",
			"queue_len": len(GlobalSIEMClient.eventQueue),
			"queue_cap": cap(GlobalSIEMClient.eventQueue),
			"timestamp": time.Now().Format(time.RFC3339),
		})
		return
	}

	json.NewEncoder(w).Encode(stats)
}
