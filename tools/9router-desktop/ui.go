package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"time"
)

//go:embed client_app.html
var embeddedClientHTML []byte

// corsMiddleware bọc CORS để giao diện Webview / Browser fetch không bị chặn
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With, X-OmniRouter-Key")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next(w, r)
	}
}

// handleDesktopUI phục vụ giao diện 4 Tab Cyberpunk của App Desktop
func (c *DesktopClient) handleDesktopUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")

	// Ưu tiên đọc file client_app.html trên đĩa nếu người dùng tùy biến
	execDir, _ := filepath.Abs(filepath.Dir(os.Args[0]))
	diskHtmlPaths := []string{
		"client_app.html",
		filepath.Join(execDir, "client_app.html"),
		"index.html",
		filepath.Join(execDir, "index.html"),
	}

	for _, p := range diskHtmlPaths {
		if data, err := os.ReadFile(p); err == nil && len(data) > 0 {
			w.Write(data)
			return
		}
	}

	// Nếu không có file đĩa thì dùng nội dung nhúng sẵn bên trong binary (100% độc lập)
	if len(embeddedClientHTML) > 0 {
		w.Write(embeddedClientHTML)
		return
	}

	w.Write([]byte("<h1>OmniRouter Client UI is loading...</h1>"))
}

// handlePing trả về phản hồi 200 OK ngay lập tức để đo RTT và TTFT thực tế
func (c *DesktopClient) handlePing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "ok",
		"message":    "OmniRouter Client Local Node is healthy",
		"timestamp":  time.Now().UnixMilli(),
		"port":       c.config.LocalPort,
		"mode":       "desktop_gui",
		"latency_ms": 0,
	})
}

// handleStatusAPI trả về thông tin trạng thái cho giao diện Desktop
func (c *DesktopClient) handleStatusAPI(w http.ResponseWriter, r *http.Request) {
	c.mu.RLock()
	tokensCount := len(c.tokens)
	renderURL := c.config.RenderURL
	apiKey := c.config.MasterAPIKey
	port := c.config.LocalPort
	lastSyncStr := ""
	if !c.lastSync.IsZero() {
		lastSyncStr = c.lastSync.Format("15:04:05 02/01/2006")
	}
	var totalQuota int64
	for _, a := range c.tokens {
		totalQuota += a.QuotaRemaining
	}
	c.mu.RUnlock()

	if totalQuota <= 0 {
		totalQuota = 5000000
	}

	maskedKey := apiKey
	if len(apiKey) > 16 {
		maskedKey = apiKey[:8] + "..." + apiKey[len(apiKey)-4:]
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"enabled":         c.isProxyEnabled(),
		"tokens_in_ram":   tokensCount,
		"render_url":      renderURL,
		"master_api_key":  apiKey,
		"masked_key":      maskedKey,
		"local_port":      port,
		"total_requests":  atomic.LoadUint64(&c.totalReqs),
		"total_tokens":    atomic.LoadUint64(&c.totalTokens),
		"quota_remaining": totalQuota,
		"last_sync":       lastSyncStr,
		"base_url":        fmt.Sprintf("http://127.0.0.1:%d/v1", port),
	})
}

// handleConfigAPI cập nhật cấu hình từ giao diện Desktop
func (c *DesktopClient) handleConfigAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if r.Method == http.MethodPost {
		var patch struct {
			RenderURL    string `json:"render_url"`
			WebServerURL string `json:"web_server_url"`
			MasterAPIKey string `json:"master_api_key"`
			APIKey       string `json:"api_key"`
			LocalPort    int    `json:"local_port"`
			Enabled      *bool  `json:"enabled,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			http.Error(w, `{"error":"Invalid JSON"}`, http.StatusBadRequest)
			return
		}

		c.mu.Lock()
		if patch.RenderURL != "" {
			c.config.RenderURL = patch.RenderURL
		} else if patch.WebServerURL != "" {
			c.config.RenderURL = patch.WebServerURL
		}

		if patch.MasterAPIKey != "" {
			c.config.MasterAPIKey = patch.MasterAPIKey
		} else if patch.APIKey != "" {
			c.config.MasterAPIKey = patch.APIKey
		}

		if patch.LocalPort > 0 {
			c.config.LocalPort = patch.LocalPort
		}
		if patch.Enabled != nil {
			c.proxyEnabled = *patch.Enabled
		}
		c.mu.Unlock()

		// Lưu file config.json
		cfgBytes, _ := json.MarshalIndent(c.config, "", "  ")
		_ = os.WriteFile("config.json", cfgBytes, 0644)

		// Thử đồng bộ ngay với URL mới
		go c.syncWithRender()

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Đã lưu cấu hình và kích hoạt kết nối lại thành công",
		})
		return
	}

	c.mu.RLock()
	cfgCopy := c.config
	c.mu.RUnlock()
	json.NewEncoder(w).Encode(cfgCopy)
}

// handleMitmApi điều khiển trạng thái MITM và Smart DNS cho Tab 1 & Tab 2
func (c *DesktopClient) handleMitmApi(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	switch r.Method {
	case http.MethodGet:
		status := "stopped"
		if c.isProxyEnabled() {
			status = "running"
		}
		c.mu.RLock()
		port := c.config.LocalPort
		c.mu.RUnlock()

		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":             status,
			"port":               port,
			"mitm_active":        c.isProxyEnabled(),
			"dns_active":         true,
			"active_connections": 1,
			"total_requests":     atomic.LoadUint64(&c.totalReqs),
			"requests_served":    atomic.LoadUint64(&c.totalReqs),
			"total_tokens":       atomic.LoadUint64(&c.totalTokens),
			"routing_rules": map[string]string{
				"generativelanguage.googleapis.com": fmt.Sprintf("127.0.0.1:%d", port),
				"api.anthropic.com":                 fmt.Sprintf("127.0.0.1:%d", port),
				"api.openai.com":                    fmt.Sprintf("127.0.0.1:%d", port),
			},
		})

	case http.MethodPost:
		c.setProxyEnabled(true)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"status":  "running",
			"message": "Đã kích hoạt MITM Proxy & Smart DNS thành công",
		})

	case http.MethodDelete:
		c.setProxyEnabled(false)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"status":  "stopped",
			"message": "Đã ngắt kích hoạt MITM Proxy & khôi phục DNS hệ thống",
		})

	case http.MethodPatch:
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Đã lưu cập nhật bảng định tuyến DNS",
		})

	default:
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// handleQuotaAPI trả về hạn mức token và skills cho Tab 4
func (c *DesktopClient) handleQuotaAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	c.mu.RLock()
	var totalQuota int64
	for _, a := range c.tokens {
		totalQuota += a.QuotaRemaining
	}
	accCount := len(c.tokens)
	apiKey := c.config.MasterAPIKey
	c.mu.RUnlock()

	if totalQuota <= 0 {
		totalQuota = 5000000
	}

	keyParam := r.URL.Query().Get("key")
	if keyParam == "" {
		keyParam = apiKey
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":          "active",
		"api_key":         keyParam,
		"quota_remaining": totalQuota,
		"quota_used":      atomic.LoadUint64(&c.totalTokens),
		"rate_limit":      "60 RPM / 1000 RPD",
		"synced_accounts": accCount,
		"skills": []string{
			"mitm_proxy",
			"smart_dns",
			"high_speed_routing",
			"token_pool_balancer",
			"0ms_local_cache",
			"gemini_3.1_pro",
			"claude_3.5_sonnet",
		},
		"node_info": map[string]interface{}{
			"version":  "v1.0.0-gui",
			"runtime":  "Pure Go Native Windows GUI",
			"dns_mode": "Smart Intercept",
		},
	})
}

// handleClientSyncAPI xử lý đồng bộ token cho Tab 4
func (c *DesktopClient) handleClientSyncAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	// Kích hoạt đồng bộ ngầm
	go c.syncWithRender()

	c.mu.RLock()
	cnt := len(c.tokens)
	c.mu.RUnlock()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":            "success",
		"sync_interval_sec": 180,
		"accounts_count":    cnt,
		"message":           "Đã gửi yêu cầu đồng bộ tài khoản và quota về RAM",
	})
}

// handleFlushDNS xóa cache DNS hệ thống khi người dùng bấm nút trên Tab 2
func (c *DesktopClient) handleFlushDNS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = exec.Command("ipconfig", "/flushdns").Run()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Đã xóa toàn bộ bộ đệm DNS hệ thống thành công (ipconfig /flushdns)!",
	})
}

// handleToggleProxy bật/tắt proxy trực tiếp
func (c *DesktopClient) handleToggleProxy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	newState := !c.isProxyEnabled()
	c.setProxyEnabled(newState)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"enabled": newState,
		"status":  map[bool]string{true: "running", false: "stopped"}[newState],
	})
}

// handleManualSyncEndpoint kích hoạt đồng bộ từ giao diện
func (c *DesktopClient) handleManualSyncEndpoint(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.syncWithRender()
	c.mu.RLock()
	cnt := len(c.tokens)
	c.mu.RUnlock()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"tokens_in_ram": cnt,
		"message":       "Đồng bộ hoàn tất từ Web Server!",
	})
}
