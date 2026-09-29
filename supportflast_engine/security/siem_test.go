package security

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSIEMClient_LifecycleAndPush(t *testing.T) {
	client := NewSIEMClient("http://127.0.0.1:9999", 100, 2)
	defer client.Stop()

	// Push các loại sự kiện an ninh khác nhau
	client.PushEvent(SecurityEvent{
		Type:       EventAuditLog,
		ClientIP:   "10.0.0.1",
		Path:       "/api/admin/users",
		StatusCode: 200,
	})

	client.PushEvent(SecurityEvent{
		Type:      EventHoneypotTrap,
		ClientIP:  "198.51.100.99",
		Path:      "/.env",
		Method:    "GET",
		UserAgent: "curl/7.68.0",
	})

	client.PushEvent(SecurityEvent{
		Type:     EventBruteForce,
		ClientIP: "203.0.113.5",
		Path:     "/api/auth/login",
		Method:   "POST",
	})

	// Kiểm tra queue không bị block
	if cap(client.eventQueue) != 100 {
		t.Fatalf("expected queue capacity 100, got %d", cap(client.eventQueue))
	}
}

func TestSIEMClient_SyncAnalysisWithMockServer(t *testing.T) {
	// Giả lập server SIEM AI để test tích hợp đồng bộ
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/siem/analyze":
			var req SecurityEvent
			_ = json.NewDecoder(r.Body).Decode(&req)

			score := 10
			level := "SAFE"
			color := "green"
			action := "ALLOW"

			if req.Type == EventHoneypotTrap {
				score = 95
				level = "CRITICAL"
				color = "red"
				action = "JAIL_IP_24H"
			}

			json.NewEncoder(w).Encode(ThreatAnalysisResult{
				EventID:           "test-evt-001",
				EventType:         string(req.Type),
				ClientIP:          req.ClientIP,
				ThreatScore:       score,
				RiskLevel:         level,
				RiskColor:         color,
				ActionRecommended: action,
				Summary:           "Mock analysis for testing",
				Timestamp:         time.Now().UTC().Format(time.RFC3339),
			})

		case "/siem/alerts":
			json.NewEncoder(w).Encode(ThreatAlertsResponse{
				Status:             "success",
				TotalAlertsStored:  1,
				ReturnedCount:      1,
				Alerts: []ThreatAlert{
					{
						AlertID:           "alt-001",
						ClientIP:          "198.51.100.99",
						ThreatScore:       95,
						RiskLevel:         "CRITICAL",
						RiskColor:         "red",
						Summary:           "Honeypot triggered",
						ActionRecommended: "JAIL_IP_24H",
					},
				},
			})

		case "/siem/stats":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":         "active",
				"service":        "supportflast_siem_ai",
				"memory_rss_mb":  12.5,
				"metrics": map[string]interface{}{
					"total_events": 10,
					"critical":     1,
				},
			})

		default:
			http.NotFound(w, r)
		}
	}))
	defer mockServer.Close()

	client := NewSIEMClient(mockServer.URL, 50, 1)
	defer client.Stop()

	// 1. Test AnalyzeEventSync với sự kiện an toàn
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	safeRes, err := client.AnalyzeEventSync(ctx, SecurityEvent{
		Type:     EventIPAccess,
		ClientIP: "127.0.0.1",
		Path:     "/api/apps",
	})
	if err != nil {
		t.Fatalf("AnalyzeEventSync failed: %v", err)
	}
	if safeRes.ThreatScore > 20 || safeRes.RiskLevel != "SAFE" {
		t.Errorf("expected safe threat score <= 20, got %d (%s)", safeRes.ThreatScore, safeRes.RiskLevel)
	}

	// 2. Test AnalyzeEventSync với sự kiện bẫy Honeypot
	criticalRes, err := client.AnalyzeEventSync(ctx, SecurityEvent{
		Type:     EventHoneypotTrap,
		ClientIP: "198.51.100.99",
		Path:     "/.env",
	})
	if err != nil {
		t.Fatalf("AnalyzeEventSync for Honeypot failed: %v", err)
	}
	if criticalRes.ThreatScore < 80 || criticalRes.RiskLevel != "CRITICAL" {
		t.Errorf("expected critical threat score >= 80, got %d (%s)", criticalRes.ThreatScore, criticalRes.RiskLevel)
	}

	// 3. Test GetRecentAlerts
	alerts, err := client.GetRecentAlerts(10, 0, "")
	if err != nil {
		t.Fatalf("GetRecentAlerts failed: %v", err)
	}
	if len(alerts) != 1 || alerts[0].ClientIP != "198.51.100.99" {
		t.Errorf("unexpected alerts response: %+v", alerts)
	}

	// 4. Test GetSIEMStats
	stats, err := client.GetSIEMStats()
	if err != nil {
		t.Fatalf("GetSIEMStats failed: %v", err)
	}
	if stats["service"] != "supportflast_siem_ai" {
		t.Errorf("unexpected service name in stats: %v", stats["service"])
	}
}

func TestSIEM_PackageHelpers(t *testing.T) {
	// Kiểm tra các helper functions cấp package không bị panic khi gọi
	RecordAuditEvent("usr-1", "admin", "UPDATE_CONFIG", "/api/config", "10.0.0.2", "success", "Updated rate limits")
	RecordHoneypotTrap("198.51.100.11", "/wp-admin", "GET", "curl/7.81", "")
	RecordSIEMLoginFailure("198.51.100.22", "admin", 3, false, 2)
	RecordSIEMLoginSuccess("10.0.0.5", "admin")
	RecordPayloadAttack("198.51.100.33", "SQLi", "UNION SELECT", "/api/chat", "POST", "python-requests")
	RecordIPAccess("10.0.0.6", "/api/apps", "GET", "Mozilla", 200)

	// Test SIEMAlertsHandler fallback khi offline
	req := httptest.NewRequest(http.MethodGet, "/api/admin/siem/alerts", nil)
	rr := httptest.NewRecorder()
	SIEMAlertsHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200 from SIEMAlertsHandler, got %d", rr.Code)
	}

	// Test SIEMStatsHandler fallback khi offline
	reqStats := httptest.NewRequest(http.MethodGet, "/api/admin/siem/stats", nil)
	rrStats := httptest.NewRecorder()
	SIEMStatsHandler(rrStats, reqStats)

	if rrStats.Code != http.StatusOK {
		t.Errorf("expected status 200 from SIEMStatsHandler, got %d", rrStats.Code)
	}
}
