package omnirouter

import (
	"encoding/json"
	"time"
)

// ProviderConnection đại diện cho một tài khoản kết nối AI (Antigravity, Gemini, OpenAI, Claude...)
type ProviderConnection struct {
	ID        string          `json:"id"`
	Provider  string          `json:"provider"`
	AuthType  string          `json:"authType"`
	Name      string          `json:"name"`
	Email     string          `json:"email"`
	Priority  int             `json:"priority"`
	IsActive  int             `json:"isActive"`
	Data      json.RawMessage `json:"data"`
	CreatedAt string          `json:"createdAt"`
	UpdatedAt string          `json:"updatedAt"`
}

// ClientKey đại diện cho API Key do Web sinh ra (dạng sk-9router-...) cấp cho máy tính / Termux
type ClientKey struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Key           string `json:"key"`
	KeyHash       string `json:"keyHash,omitempty"`
	UsedRequests  int64  `json:"usedRequests"`
	TotalTokens   int64  `json:"totalTokens"`
	IsActive      int    `json:"isActive"`
	CreatedAt     string `json:"createdAt"`
	LastUsedAt    string `json:"lastUsedAt"`
}

// ProxyPool đại diện cho cấu hình Proxy trong hệ thống
type ProxyPool struct {
	ID         string          `json:"id"`
	IsActive   int             `json:"isActive"`
	TestStatus string          `json:"testStatus"`
	Data       json.RawMessage `json:"data"`
	CreatedAt  string          `json:"createdAt"`
	UpdatedAt  string          `json:"updatedAt"`
}

// ClientSyncResponse gói dữ liệu trả về cho App Go trên PC/Termux nạp vào RAM
type ClientSyncResponse struct {
	Status          string                 `json:"status"`
	SyncIntervalSec int                    `json:"sync_interval_sec"`
	RoutingDNS      map[string]string      `json:"routing_dns"`
	Accounts        []ClientAccountPayload `json:"accounts"`
}

// ClientAccountPayload thông tin tài khoản kèm token thật nạp thẳng vào RAM client
type ClientAccountPayload struct {
	ID             string `json:"id"`
	Provider       string `json:"provider"`
	AuthType       string `json:"auth_type"`
	Name           string `json:"name"`
	Email          string `json:"email"`
	AccessToken    string `json:"access_token"`
	RefreshToken   string `json:"refresh_token,omitempty"`
	ProjectID      string `json:"project_id"`
	QuotaRemaining int64  `json:"quota_remaining"`
	Priority       int    `json:"priority"`
	Status         int    `json:"status"`
}

// ClientReportPayload gói dữ liệu báo cáo ngầm từ Client gửi lên Web Master
type ClientReportPayload struct {
	ApiKey         string                `json:"api_key"`
	UsedRequests   int64                 `json:"used_requests"`
	TotalTokens    int64                 `json:"total_tokens"`
	AccountUpdates []ClientAccountUpdate `json:"account_updates"`
}

// ClientAccountUpdate cập nhật trạng thái quota và lỗi của tài khoản
type ClientAccountUpdate struct {
	ID            string `json:"id"`
	QuotaDeducted int64  `json:"quota_deducted"`
	ErrorCode     string `json:"error_code,omitempty"`
	IsRateLimited bool   `json:"is_rate_limited"`
}

// UsageRecord ghi nhận lịch sử gọi API
type UsageRecord struct {
	ID               int64   `json:"id"`
	Timestamp        string  `json:"timestamp"`
	Provider         string  `json:"provider"`
	Model            string  `json:"model"`
	ConnectionID     string  `json:"connectionId"`
	ApiKey           string  `json:"apiKey"`
	Endpoint         string  `json:"endpoint"`
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	TotalTokens      int64   `json:"totalTokens"`
	Cost             float64 `json:"cost"`
	Status           string  `json:"status"`
	LatencyMs        int64   `json:"latencyMs"`
	TtftMs           int64   `json:"ttftMs"`
	Tps              float64 `json:"tps"`
	CachedTokens     int64   `json:"cachedTokens"`
	StatusCode       int     `json:"statusCode"`
	ErrorCode        string  `json:"errorCode"`
}

// NowISO trả về thời gian định dạng ISO 8601 chuẩn UTC
func NowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
}
