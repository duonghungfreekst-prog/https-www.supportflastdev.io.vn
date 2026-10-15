package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// TokenItem đại diện cho tài khoản nạp sẵn trong RAM máy tính
type TokenItem struct {
	ID             string `json:"id"`
	Provider       string `json:"provider"`
	AuthType       string `json:"auth_type"`
	Name           string `json:"name"`
	Email          string `json:"email"`
	AccessToken    string `json:"access_token"`
	ProjectID      string `json:"project_id"`
	QuotaRemaining int64  `json:"quota_remaining"`
	Priority       int    `json:"priority"`
	Status         int    `json:"status"`
}

// ClientSyncResponse phản hồi từ Web Render
type ClientSyncResponse struct {
	Status          string            `json:"status"`
	SyncIntervalSec int               `json:"sync_interval_sec"`
	RoutingDNS      map[string]string `json:"routing_dns"`
	Accounts        []TokenItem       `json:"accounts"`
}

var syncHTTPClient = &http.Client{Timeout: 20 * time.Second}

// startSyncWorker chạy luồng ngầm đồng bộ token và giữ Render thức 24/24
func (c *DesktopClient) startSyncWorker() {
	// Đồng bộ lần đầu tiên
	c.syncWithRender()

	ticker := time.NewTicker(3 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		c.reportToRender()
		c.syncWithRender()
	}
}

// syncWithRender gọi API lên Render + TiDB Cloud kéo danh sách token mới nhất về RAM
func (c *DesktopClient) syncWithRender() {
	c.mu.RLock()
	renderURL := strings.TrimRight(c.config.RenderURL, "/")
	masterKey := c.config.MasterAPIKey
	c.mu.RUnlock()

	if renderURL == "" || masterKey == "" || masterKey == "sk-9router-xxxxxxxxxxxxxxxx" {
		return
	}

	req, err := http.NewRequest("GET", renderURL+"/api/v1/client/sync", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+masterKey)

	resp, err := syncHTTPClient.Do(req)
	if err != nil {
		log.Printf("[SYNC] [CẢNH BÁO] Không thể kết nối tới Render (%s): %v", renderURL, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		log.Printf("[SYNC] [CẢNH BÁO] Render từ chối xác thực (HTTP %d): %s", resp.StatusCode, string(b))
		return
	}

	var syncResp ClientSyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&syncResp); err != nil {
		log.Printf("[SYNC] Lỗi parse JSON phản hồi: %v", err)
		return
	}

	c.mu.Lock()
	c.tokens = syncResp.Accounts
	c.lastSync = time.Now()
	c.mu.Unlock()

	log.Printf("[SYNC] ✔ Đã đồng bộ thành công %d tài khoản AI từ Render Cloud vào RAM Desktop!", len(syncResp.Accounts))
}

// reportToRender báo cáo số request và token tiêu thụ, đồng thời ping giữ Render thức 24/24
func (c *DesktopClient) reportToRender() {
	c.mu.RLock()
	renderURL := strings.TrimRight(c.config.RenderURL, "/")
	masterKey := c.config.MasterAPIKey
	c.mu.RUnlock()

	if renderURL == "" || masterKey == "" || masterKey == "sk-9router-xxxxxxxxxxxxxxxx" {
		return
	}

	currReqs := atomic.LoadUint64(&c.totalReqs)
	currTokens := atomic.LoadUint64(&c.totalTokens)

	diffReqs := currReqs - c.reportedReqs
	diffTokens := currTokens - c.reportedTokens

	if diffReqs == 0 && diffTokens == 0 {
		// Ping nhẹ 0 RU để giữ Render thức
		pingURL := renderURL + "/ping"
		r, err := syncHTTPClient.Get(pingURL)
		if err == nil {
			r.Body.Close()
		}
		return
	}

	payload := map[string]interface{}{
		"api_key":         masterKey,
		"used_requests":   diffReqs,
		"total_tokens":    diffTokens,
		"account_updates": []interface{}{},
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", renderURL+"/api/v1/client/report", bytes.NewReader(body))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+masterKey)
		resp, errDo := syncHTTPClient.Do(req)
		if errDo == nil {
			resp.Body.Close()
			c.reportedReqs = currReqs
			c.reportedTokens = currTokens
			log.Printf("[REPORT] 📤 Đã báo cáo ngầm lên Render: +%d requests, +%d tokens", diffReqs, diffTokens)
		}
	}
}

// TriggerSyncManual gọi đồng bộ thủ công từ giao diện Desktop
func (c *DesktopClient) TriggerSyncManual() error {
	c.syncWithRender()
	c.mu.RLock()
	count := len(c.tokens)
	c.mu.RUnlock()
	if count == 0 {
		return fmt.Errorf("không thể đồng bộ hoặc chưa có tài khoản nào được trả về từ Render")
	}
	return nil
}
