package omnirouter

import (
	"bytes"

	"os"

	"context"

	"crypto/tls"

	"database/sql"

	"encoding/json"

	"fmt"

	"io"

	"log"

	"math"

	"net"

	"net/http"

	"strings"

	"sync"

	"time"
)

const (
	AntigravityQuotaApiUrl = "https://daily-cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels"

	AntigravityQuotaSummaryApiUrl = "https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary"

	AntigravityUserAgent = "antigravity/ide/2.11.0 windows/amd64"

	AntigravityIdeVersion = "2.11.0"

	QuotaCacheTTL = 30 * time.Second
)

// QuotaItem đại diện cho một hạn mức quota hiển thị trên UI và lưu trữ hệ thống
type QuotaItem struct {
	Total               int      `json:"total"`                         // Điểm quy ước giao diện (mặc định 1000)
	Used                int      `json:"used"`                          // Điểm đã tiêu thụ quy ước
	RemainingPercentage float64  `json:"remainingPercentage"`           // Phần trăm còn lại (0.0 - 100.0)
	RemainingFraction   float64  `json:"remainingFraction"`             // Tỷ lệ thực tế từ API (0.0 - 1.0)
	ResetAt             *string  `json:"resetAt"`                       // Thời điểm reset hạn mức (ISO string)
	DisplayName         string   `json:"displayName"`                   // Tên hiển thị (ví dụ: Gemini (5h), Claude (Weekly))
	Unlimited           bool     `json:"unlimited"`                     // Không giới hạn
	BucketID            string   `json:"bucketId,omitempty"`            // ID bucket thực từ Google API
	Window              string   `json:"window,omitempty"`              // Cửa sổ hạn mức: "5h", "daily", "weekly", "model"
	Source              string   `json:"source,omitempty"`              // Nguồn dữ liệu: "live_api", "ram_cache", "db_cache"
	Status              string   `json:"status,omitempty"`              // Trạng thái: "LIVE", "STALE", "PARTIAL", "UNAVAILABLE", "ERROR"
	FetchedAt           string   `json:"fetchedAt,omitempty"`           // Thời điểm lấy dữ liệu (ISO string)
	LastError           string   `json:"lastError,omitempty"`           // Lỗi gần nhất nếu có (đã lọc secrets)
}

// CachedQuota lưu giữ hạn mức trong RAM
type CachedQuota struct {
	Quotas    map[string]QuotaItem
	FetchedAt time.Time
}

// BatchQuotaResult kết quả làm mới hàng loạt hạn mức
type BatchQuotaResult struct {
	Total       int                             `json:"total"`
	Success     int                             `json:"success"`     // Tổng số tài khoản có dữ liệu (Live + Cached)
	LiveSuccess int                             `json:"liveSuccess"` // Số tài khoản lấy LIVE thành công từ Google API
	CachedCount int                             `json:"cachedCount"` // Số tài khoản fallback từ Cache
	Failed      int                             `json:"failed"`      // Số tài khoản lỗi hoàn toàn không có dữ liệu
	DurationMs  int64                           `json:"durationMs"`
	AccountData map[string]map[string]QuotaItem `json:"accountData"`
	Results     map[string]map[string]QuotaItem `json:"results"`
}

var (
	quotaMemCache sync.Map // connId -> *CachedQuota

	quotaFetchMu sync.Map // connId -> *sync.Mutex (chống concurrent duplicate requests)

	quotaClient *http.Client

	quotaClientOnce sync.Once
)

func getQuotaClient() *http.Client {

	quotaClientOnce.Do(func() {

		quotaClient = &http.Client{

			Timeout: 15 * time.Second,

			Transport: &http.Transport{

				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {

					host, port, _ := net.SplitHostPort(addr)

					// Nếu máy khách đã redirect hosts file daily-cloudcode-pa.googleapis.com -> 127.0.0.1,

					// dùng IP Google upstream thật để bypass loopback

					if host == "daily-cloudcode-pa.googleapis.com" && upstreamRealHost != "" {

						realAddr := net.JoinHostPort(upstreamRealHost, port)

						return net.DialTimeout(network, realAddr, 10*time.Second)

					}

					return net.DialTimeout(network, addr, 10*time.Second)

				},

				TLSClientConfig: &tls.Config{

					ServerName: "daily-cloudcode-pa.googleapis.com",
				},

				MaxIdleConns: 50,

				MaxIdleConnsPerHost: 20,

				IdleConnTimeout: 60 * time.Second,
			},
		}

	})

	return quotaClient

}

func getConnMutex(connId string) *sync.Mutex {

	val, _ := quotaFetchMu.LoadOrStore(connId, &sync.Mutex{})

	return val.(*sync.Mutex)

}

// GetCachedQuotas lấy quota từ RAM cache nếu còn hạn TTL 60s

func GetCachedQuotas(connId string) (map[string]QuotaItem, bool) {

	if val, ok := quotaMemCache.Load(connId); ok {

		c := val.(*CachedQuota)

		if time.Since(c.FetchedAt) < QuotaCacheTTL {

			return c.Quotas, true

		}

	}

	return nil, false

}

// SetCachedQuotas ghi đè cache trong RAM

func SetCachedQuotas(connId string, quotas map[string]QuotaItem) {

	quotaMemCache.Store(connId, &CachedQuota{

		Quotas: quotas,

		FetchedAt: time.Now(),
	})

}

// sanitizeQuotaErrorMessage loại bỏ secrets, tokens và giới hạn độ dài của thông báo lỗi
func sanitizeQuotaErrorMessage(msg string) string {
	if msg == "" {
		return ""
	}
	clean := strings.TrimSpace(msg)
	if strings.Contains(clean, "Bearer ") {
		idx := strings.Index(clean, "Bearer ")
		if idx+7 < len(clean) {
			clean = clean[:idx+7] + "[REDACTED]"
		}
	}
	if len(clean) > 250 {
		clean = clean[:250] + "..."
	}
	return clean
}

// FetchAntigravityQuota lấy hạn mức thực tế của tài khoản Antigravity từ Google API
func FetchAntigravityQuota(db *sql.DB, conn *ProviderConnection, force bool) (map[string]QuotaItem, error) {
	if !force {
		if cached, ok := GetCachedQuotas(conn.ID); ok {
			return cached, nil
		}
	}

	mu := getConnMutex(conn.ID)
	mu.Lock()
	defer mu.Unlock()

	// Double-check sau khi acquire lock
	if !force {
		if cached, ok := GetCachedQuotas(conn.ID); ok {
			return cached, nil
		}
	}

	creds, err := GetValidCredentials(db, conn)
	if err != nil {
		cleanErr := sanitizeQuotaErrorMessage(err.Error())
		if cached, ok := GetCachedQuotas(conn.ID); ok && len(cached) > 0 {
			log.Printf("[ENGINE] [OMNIROUTER] ⚠️ Xác thực thất bại cho %s (%v), dùng cache RAM (STALE)", conn.Name, err)
			staleQuotas := make(map[string]QuotaItem, len(cached))
			for k, v := range cached {
				itemCopy := v
				itemCopy.Source = "ram_cache"
				itemCopy.Status = "STALE"
				itemCopy.LastError = cleanErr
				staleQuotas[k] = itemCopy
			}
			return staleQuotas, nil
		}
		var dataMap map[string]interface{}
		if json.Unmarshal(conn.Data, &dataMap) == nil {
			if qRaw, ok := dataMap["quotas"]; ok {
				var dbQuotas map[string]QuotaItem
				qBytes, _ := json.Marshal(qRaw)
				if json.Unmarshal(qBytes, &dbQuotas) == nil && len(dbQuotas) > 0 {
					log.Printf("[ENGINE] [OMNIROUTER] ⚠️ Xác thực thất bại cho %s (%v), dùng cache DB (STALE)", conn.Name, err)
					staleQuotas := make(map[string]QuotaItem, len(dbQuotas))
					for k, v := range dbQuotas {
						itemCopy := v
						itemCopy.Source = "db_cache"
						itemCopy.Status = "STALE"
						itemCopy.LastError = cleanErr
						staleQuotas[k] = itemCopy
					}
					return staleQuotas, nil
				}
			}
		}
		return nil, fmt.Errorf("không thể xác thực tài khoản %s: %w", conn.Name, err)
	}

	client := getQuotaClient()
	quotas, err := executeQuotaFetch(client, creds)
	if err != nil && strings.Contains(err.Error(), "401") && creds.RefreshToken != "" {
		// Token bị lỗi 401 từ Google: Cưỡng chế refresh và thử lại 1 lần
		log.Printf("[ENGINE] [OMNIROUTER] ⚠️ Quota API trả về 401 cho %s, tiến hành làm mới token...", conn.Name)
		newTok, newExpiry, newRefresh, refErr := RefreshGoogleToken(creds.RefreshToken)
		if refErr == nil {
			creds.AccessToken = newTok
			creds.ExpiresAt = newExpiry
			var dataMap map[string]interface{}
			_ = json.Unmarshal(conn.Data, &dataMap)
			if dataMap == nil {
				dataMap = make(map[string]interface{})
			}
			dataMap["accessToken"] = newTok
			dataMap["expiresAt"] = newExpiry.Format(time.RFC3339)
			dataMap["lastRefreshAt"] = NowISO()
			if newRefresh != "" {
				dataMap["refreshToken"] = newRefresh
				creds.RefreshToken = newRefresh
			}
			_ = UpdateProviderData(db, conn.ID, dataMap)
			quotas, err = executeQuotaFetch(client, creds)
		}
	}

	if err != nil {
		cleanErr := sanitizeQuotaErrorMessage(err.Error())
		// Thử lấy từ cache RAM hoặc data JSON trong DB nếu live call thất bại
		if cached, ok := GetCachedQuotas(conn.ID); ok && len(cached) > 0 {
			log.Printf("[ENGINE] [OMNIROUTER] ⚠️ Live quota fetch thất bại cho %s (%v), dùng cache RAM (STALE)", conn.Name, err)
			staleQuotas := make(map[string]QuotaItem, len(cached))
			for k, v := range cached {
				itemCopy := v
				itemCopy.Source = "ram_cache"
				itemCopy.Status = "STALE"
				itemCopy.LastError = cleanErr
				staleQuotas[k] = itemCopy
			}
			return staleQuotas, nil
		}

		var dataMap map[string]interface{}
		if json.Unmarshal(conn.Data, &dataMap) == nil {
			if qRaw, ok := dataMap["quotas"]; ok {
				var dbQuotas map[string]QuotaItem
				qBytes, _ := json.Marshal(qRaw)
				if json.Unmarshal(qBytes, &dbQuotas) == nil && len(dbQuotas) > 0 {
					log.Printf("[ENGINE] [OMNIROUTER] ⚠️ Live quota fetch thất bại cho %s (%v), dùng cache DB (STALE)", conn.Name, err)
					staleQuotas := make(map[string]QuotaItem, len(dbQuotas))
					for k, v := range dbQuotas {
						itemCopy := v
						itemCopy.Source = "db_cache"
						itemCopy.Status = "STALE"
						itemCopy.LastError = cleanErr
						staleQuotas[k] = itemCopy
					}
					return staleQuotas, nil
				}
			}
		}
		return nil, err
	}

	// Đánh dấu toàn bộ item là LIVE và lưu timestamp mới nhất
	nowStr := NowISO()
	for k, v := range quotas {
		itemCopy := v
		itemCopy.Source = "live_api"
		itemCopy.Status = "LIVE"
		itemCopy.FetchedAt = nowStr
		itemCopy.LastError = ""
		quotas[k] = itemCopy
	}

	// Cập nhật Cache vào RAM
	SetCachedQuotas(conn.ID, quotas)

	// Cập nhật Cache vào DB Aiven PostgreSQL an toàn chống Lost Update
	connID := conn.ID
	connData := conn.Data
	go func() {
		var dataMap map[string]interface{}
		_ = json.Unmarshal(connData, &dataMap)
		if dataMap == nil {
			dataMap = make(map[string]interface{})
		}
		dataMap["quotas"] = quotas
		dataMap["lastQuotaAt"] = NowISO()
		_ = UpdateProviderData(db, connID, dataMap)
	}()

	return quotas, nil
}

func executeQuotaFetch(client *http.Client, creds *Credentials) (map[string]QuotaItem, error) {
	reqBody := map[string]string{}
	if creds.ProjectId != "" {
		reqBody["project"] = creds.ProjectId
	}
	bodyBytes, _ := json.Marshal(reqBody)

	quotas := make(map[string]QuotaItem)
	var qMu sync.Mutex
	var wg sync.WaitGroup
	var has401 bool
	var fetchErr error

	// 1. Gọi fetchAvailableModels song song
	wg.Add(1)
	go func() {
		defer wg.Done()
		fetchReq, err := http.NewRequest("POST", AntigravityQuotaApiUrl, bytes.NewReader(bodyBytes))
		if err != nil {
			qMu.Lock()
			if fetchErr == nil {
				fetchErr = fmt.Errorf("tạo request fetchAvailableModels lỗi: %w", err)
			}
			qMu.Unlock()
			return
		}
		fetchReq.Header.Set("Authorization", "Bearer "+creds.AccessToken)
		fetchReq.Header.Set("User-Agent", AntigravityUserAgent)
		fetchReq.Header.Set("Content-Type", "application/json")
		fetchReq.Header.Set("X-Client-Name", "antigravity")
		fetchReq.Header.Set("X-Client-Version", AntigravityIdeVersion)

		resp, err := client.Do(fetchReq)
		if err != nil && (strings.Contains(err.Error(), "unexpected EOF") || strings.Contains(err.Error(), "connection reset") || strings.Contains(err.Error(), "refused")) {
			time.Sleep(300 * time.Millisecond)
			fetchReqRetry, _ := http.NewRequest("POST", AntigravityQuotaApiUrl, bytes.NewReader(bodyBytes))
			fetchReqRetry.Header = fetchReq.Header
			resp, err = client.Do(fetchReqRetry)
		}
		if err != nil {
			qMu.Lock()
			if fetchErr == nil {
				fetchErr = fmt.Errorf("kết nối fetchAvailableModels lỗi: %w", err)
			}
			qMu.Unlock()
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusUnauthorized {
			qMu.Lock()
			has401 = true
			qMu.Unlock()
			return
		}
		if resp.StatusCode != http.StatusOK {
			bodyErr, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			cleanErr := sanitizeQuotaErrorMessage(string(bodyErr))
			qMu.Lock()
			if fetchErr == nil {
				fetchErr = fmt.Errorf("fetchAvailableModels trả về HTTP %d: %s", resp.StatusCode, cleanErr)
			}
			qMu.Unlock()
			return
		}

		var modelsRes struct {
			Models map[string]struct {
				DisplayName string `json:"displayName"`
				QuotaInfo   *struct {
					RemainingFraction float64 `json:"remainingFraction"`
					ResetTime         string  `json:"resetTime"`
				} `json:"quotaInfo"`
				IsInternal bool `json:"isInternal"`
			} `json:"models"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&modelsRes); err != nil {
			qMu.Lock()
			if fetchErr == nil {
				fetchErr = fmt.Errorf("giải mã JSON fetchAvailableModels thất bại: %w", err)
			}
			qMu.Unlock()
			return
		}

		if modelsRes.Models != nil {
			qMu.Lock()
			for modelKey, modelData := range modelsRes.Models {
				if modelData.QuotaInfo == nil || modelData.IsInternal {
					continue
				}

				remFrac := modelData.QuotaInfo.RemainingFraction
				if remFrac < 0 {
					remFrac = 0
				} else if remFrac > 1 {
					remFrac = 1
				}

				remPct := math.Round(remFrac*1000) / 10
				total := 1000
				remaining := int(math.Round(float64(total) * remFrac))
				used := total - remaining

				var resetAtPtr *string
				if modelData.QuotaInfo.ResetTime != "" {
					tStr := modelData.QuotaInfo.ResetTime
					resetAtPtr = &tStr
				}

				dispName := modelData.DisplayName
				if dispName == "" {
					dispName = modelKey
				}

				quotas[modelKey] = QuotaItem{
					Total:               total,
					Used:                used,
					RemainingPercentage: remPct,
					RemainingFraction:   remFrac,
					ResetAt:             resetAtPtr,
					DisplayName:         dispName,
					Unlimited:           false,
					BucketID:            modelKey,
					Window:              "model",
					Source:              "live_api",
					Status:              "LIVE",
				}
			}
			qMu.Unlock()
		}
	}()

	// 2. Gọi retrieveUserQuotaSummary song song
	wg.Add(1)
	go func() {
		defer wg.Done()
		summaryReq, err := http.NewRequest("POST", AntigravityQuotaSummaryApiUrl, bytes.NewReader(bodyBytes))
		if err != nil {
			qMu.Lock()
			if fetchErr == nil {
				fetchErr = fmt.Errorf("tạo request retrieveUserQuotaSummary lỗi: %w", err)
			}
			qMu.Unlock()
			return
		}
		summaryReq.Header.Set("Authorization", "Bearer "+creds.AccessToken)
		summaryReq.Header.Set("User-Agent", AntigravityUserAgent)
		summaryReq.Header.Set("Content-Type", "application/json")
		summaryReq.Header.Set("X-Client-Name", "antigravity")
		summaryReq.Header.Set("X-Client-Version", AntigravityIdeVersion)

		resp, err := client.Do(summaryReq)
		if err != nil && (strings.Contains(err.Error(), "unexpected EOF") || strings.Contains(err.Error(), "connection reset") || strings.Contains(err.Error(), "refused")) {
			time.Sleep(300 * time.Millisecond)
			summaryReqRetry, _ := http.NewRequest("POST", AntigravityQuotaSummaryApiUrl, bytes.NewReader(bodyBytes))
			summaryReqRetry.Header = summaryReq.Header
			resp, err = client.Do(summaryReqRetry)
		}
		if err != nil {
			qMu.Lock()
			if fetchErr == nil {
				fetchErr = fmt.Errorf("kết nối retrieveUserQuotaSummary lỗi: %w", err)
			}
			qMu.Unlock()
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusUnauthorized {
			qMu.Lock()
			has401 = true
			qMu.Unlock()
			return
		}
		if resp.StatusCode != http.StatusOK {
			bodyErr, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			cleanErr := sanitizeQuotaErrorMessage(string(bodyErr))
			qMu.Lock()
			if fetchErr == nil {
				fetchErr = fmt.Errorf("retrieveUserQuotaSummary trả về HTTP %d: %s", resp.StatusCode, cleanErr)
			}
			qMu.Unlock()
			return
		}

		var summaryRes struct {
			Groups []struct {
				DisplayName string `json:"displayName"`
				Buckets     []struct {
					BucketId          string  `json:"bucketId"`
					DisplayName       string  `json:"displayName"`
					Window            string  `json:"window"`
					RemainingFraction float64 `json:"remainingFraction"`
					ResetTime         string  `json:"resetTime"`
					Disabled          bool    `json:"disabled"`
				} `json:"buckets"`
			} `json:"groups"`
			QuotaSummary *struct {
				Groups []struct {
					DisplayName string `json:"displayName"`
					Buckets     []struct {
						BucketId          string  `json:"bucketId"`
						DisplayName       string  `json:"displayName"`
						Window            string  `json:"window"`
						RemainingFraction float64 `json:"remainingFraction"`
						ResetTime         string  `json:"resetTime"`
						Disabled          bool    `json:"disabled"`
					} `json:"buckets"`
				} `json:"groups"`
			} `json:"quotaSummary"`
		}

		dataBytes, rErr := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
		if rErr != nil {
			qMu.Lock()
			if fetchErr == nil {
				fetchErr = fmt.Errorf("đọc dữ liệu retrieveUserQuotaSummary lỗi: %w", rErr)
			}
			qMu.Unlock()
			return
		}

		if err := json.Unmarshal(dataBytes, &summaryRes); err != nil {
			qMu.Lock()
			if fetchErr == nil {
				fetchErr = fmt.Errorf("giải mã JSON retrieveUserQuotaSummary thất bại: %w", err)
			}
			qMu.Unlock()
			return
		}

		groups := summaryRes.Groups
		if len(groups) == 0 && summaryRes.QuotaSummary != nil {
			groups = summaryRes.QuotaSummary.Groups
		}

		qMu.Lock()
		for _, group := range groups {
			groupName := strings.ToLower(group.DisplayName)
			isGemini := strings.Contains(groupName, "gemini")
			isClaudeGpt := strings.Contains(groupName, "claude") || strings.Contains(groupName, "gpt")

			for _, bucket := range group.Buckets {
				win := strings.ToLower(bucket.Window)
				bucketText := strings.ToLower(bucket.BucketId + " " + bucket.DisplayName)

				// Phân loại chính xác 3 loại cửa sổ: Weekly (tuần), Daily (ngày), Session (5 giờ)
				// TUYỆT ĐỐI KHÔNG gom daily vào session/5h
				isWeekly := win == "weekly" || strings.Contains(bucketText, "weekly") || strings.Contains(bucketText, "7d")
				isDaily := win == "daily" || strings.Contains(bucketText, "daily") || strings.Contains(bucketText, "24h") || strings.Contains(bucketText, "per day")
				isSession := !isDaily && !isWeekly && (win == "5h" || win == "session" || strings.Contains(bucketText, "five hour") || strings.Contains(bucketText, "5h") || strings.Contains(bucketText, "5 hour") || strings.Contains(bucketText, "session"))

				if !isWeekly && !isSession && !isDaily {
					continue
				}

				if bucket.Disabled && isWeekly {
					continue
				}

				remFrac := bucket.RemainingFraction
				if bucket.Disabled {
					remFrac = 0
				}
				if remFrac < 0 {
					remFrac = 0
				} else if remFrac > 1 {
					remFrac = 1
				}

				remPct := math.Round(remFrac*1000) / 10
				total := 1000
				remaining := int(math.Round(float64(total) * remFrac))
				used := total - remaining

				var resetAtPtr *string
				if bucket.ResetTime != "" {
					tStr := bucket.ResetTime
					resetAtPtr = &tStr
				}

				var quotaKey string
				var quotaDisplayName string
				var quotaWindow string

				if isGemini {
					if isSession {
						quotaKey = "gemini_session"
						quotaDisplayName = "Gemini (5h)"
						quotaWindow = "5h"
					} else if isDaily {
						quotaKey = "gemini_daily"
						quotaDisplayName = "Gemini (Daily)"
						quotaWindow = "daily"
					} else if isWeekly {
						quotaKey = "gemini_weekly"
						quotaDisplayName = "Gemini (Weekly)"
						quotaWindow = "weekly"
					}
				} else if isClaudeGpt {
					if isSession {
						quotaKey = "claude_gpt_session"
						quotaDisplayName = "Claude & GPT (5h)"
						quotaWindow = "5h"
					} else if isDaily {
						quotaKey = "claude_gpt_daily"
						quotaDisplayName = "Claude & GPT (Daily)"
						quotaWindow = "daily"
					} else if isWeekly {
						quotaKey = "claude_gpt_weekly"
						quotaDisplayName = "Claude & GPT (Weekly)"
						quotaWindow = "weekly"
					}
				}

				if quotaKey != "" {
					if _, exists := quotas[quotaKey]; !exists {
						quotas[quotaKey] = QuotaItem{
							Total:               total,
							Used:                used,
							RemainingPercentage: remPct,
							RemainingFraction:   remFrac,
							ResetAt:             resetAtPtr,
							DisplayName:         quotaDisplayName,
							Unlimited:           false,
							BucketID:            bucket.BucketId,
							Window:              quotaWindow,
							Source:              "live_api",
							Status:              "LIVE",
						}
					}
				}
			}
		}
		qMu.Unlock()
	}()

	wg.Wait()

	if has401 {
		return nil, fmt.Errorf("Google Quota API trả về 401 Unauthorized")
	}

	if len(quotas) == 0 && fetchErr != nil {
		return nil, fetchErr
	}

	if len(quotas) == 0 {
		return nil, fmt.Errorf("Google Quota API không trả về hạn mức nào")
	}

	// 3. Gemini 3.1 Flash Image - Chỉ trích xuất NẾU API THỰC SỰ trả về dữ liệu của model này
	// TUYỆT ĐỐI KHÔNG tự bịa quota 100% khi không có dữ liệu thật (Khắc phục lỗi P0)
	if img, ok := quotas["gemini-3.1-flash-image"]; ok {
		imgCopy := img
		imgCopy.DisplayName = "Gemini 3.1 Flash Image"
		quotas["gemini_flash_image"] = imgCopy
	}

	return quotas, nil
}

// FetchAllAntigravityQuotas làm mới song song toàn bộ hạn mức tài khoản bằng worker pool có điều tiết
func FetchAllAntigravityQuotas(db *sql.DB, force bool) BatchQuotaResult {
	startTime := time.Now()
	allProviders, err := GetAllProviders(db)
	if err != nil || len(allProviders) == 0 {
		allProviders = GetCachedActiveProviders()
	}

	var accounts []ProviderConnection
	for _, p := range allProviders {
		if (p.Provider == "antigravity" || p.Provider == "google") && p.IsActive == 1 {
			accounts = append(accounts, p)
		}
	}

	result := BatchQuotaResult{
		Total:       len(accounts),
		AccountData: make(map[string]map[string]QuotaItem),
		Results:     make(map[string]map[string]QuotaItem),
	}

	if len(accounts) == 0 {
		return result
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 3) // Giới hạn 3 worker đồng thời tránh dồn dập vào Google API

	for i := range accounts {
		wg.Add(1)
		go func(acc ProviderConnection, idx int) {
			defer wg.Done()
			time.Sleep(time.Duration(idx*120) * time.Millisecond) // Stagger tránh thundering herd
			sem <- struct{}{}
			defer func() { <-sem }()

			qMap, qErr := FetchAntigravityQuota(db, &acc, force)
			mu.Lock()
			if qErr == nil && len(qMap) > 0 {
				isLive := false
				for _, it := range qMap {
					if it.Status == "LIVE" && it.Source == "live_api" {
						isLive = true
						break
					}
				}
				if isLive {
					result.LiveSuccess++
				} else {
					result.CachedCount++
				}
				result.Success++
				result.AccountData[acc.ID] = qMap
				result.Results[acc.ID] = qMap
			} else {
				// Nếu live fetch lỗi nhưng có cache cũ trong RAM hoặc DB thì nạp để UI không rỗng
				if cached, ok := GetCachedQuotas(acc.ID); ok && len(cached) > 0 {
					result.CachedCount++
					result.Success++
					result.AccountData[acc.ID] = cached
					result.Results[acc.ID] = cached
				} else {
					result.Failed++
				}
			}
			mu.Unlock()
		}(accounts[i], i)
	}

	wg.Wait()
	result.DurationMs = time.Since(startTime).Milliseconds()
	log.Printf("[ENGINE] [OMNIROUTER] 📊 Hoàn tất làm mới hạn mức: %d live, %d cache cũ, %d lỗi / tổng %d tài khoản (%dms)",
		result.LiveSuccess, result.CachedCount, result.Failed, result.Total, result.DurationMs)

	// Phát sự kiện SSE để UI cập nhật số liệu ngay lập tức với thông tin minh bạch
	if result.Success > 0 {
		msg := fmt.Sprintf("✔ Hạn mức: %d trực tiếp từ Google, %d từ bộ đệm / tổng %d tài khoản (%dms)",
			result.LiveSuccess, result.CachedCount, result.Total, result.DurationMs)
		if result.LiveSuccess == 0 && result.CachedCount > 0 {
			msg = fmt.Sprintf("⚠️ Quota API gặp sự cố, hệ thống bảo lưu %d tài khoản từ bộ đệm (%dms)",
				result.CachedCount, result.DurationMs)
		}
		BroadcastLog("QUOTA", msg)
	}

	return result
}

var backgroundQuotaPollingMu sync.Mutex

// StartBackgroundQuotaPollingWorker chạy ngầm chu kỳ kiểm tra và nạp hạn mức định kỳ 3 phút

func StartBackgroundQuotaPollingWorker(ctx context.Context, db *sql.DB) {

	// Không chạy quét ngầm trên môi trường Web Server Render để tránh cạn kiệt Connection Pool

	if os.Getenv("RENDER") != "" || os.Getenv("RENDER_SERVICE_ID") != "" {

		log.Println("[ENGINE] [OMNIROUTER] Web Server Render: Bỏ qua Background Quota Poller để tối ưu tài nguyên")

		return

	}

	go func() {

		// Chạy lần đầu sau 10 giây khi khởi động server để UI có dữ liệu mà không làm nghẽn quá trình khởi động

		select {

		case <-ctx.Done():

			return

		case <-time.After(10 * time.Second):

			log.Println("[ENGINE] [OMNIROUTER] 🚀 Khởi chạy đợt nạp Quota đầu tiên từ Google Cloud Code PA...")

			_ = FetchAllAntigravityQuotas(db, false)

		}

		// Chu kỳ polling điều độ 3 phút (hạn mức đã được tự động sync tức thì khi có request AI thật)

		ticker := time.NewTicker(3 * time.Minute)

		defer ticker.Stop()

		for {

			select {

			case <-ctx.Done():

				return

			case <-ticker.C:

				if backgroundQuotaPollingMu.TryLock() {

					_ = FetchAllAntigravityQuotas(db, false)

					backgroundQuotaPollingMu.Unlock()

				}

			}

		}

	}()

}
