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

// QuotaItem đại diện cho một hạn mức quota hiển thị trên UI

type QuotaItem struct {
	Total int `json:"total"`

	Used int `json:"used"`

	RemainingPercentage float64 `json:"remainingPercentage"`

	ResetAt *string `json:"resetAt"`

	DisplayName string `json:"displayName"`

	Unlimited bool `json:"unlimited"`
}

// CachedQuota lưu giữ hạn mức trong RAM

type CachedQuota struct {
	Quotas map[string]QuotaItem

	FetchedAt time.Time
}

// BatchQuotaResult kết quả làm mới hàng loạt hạn mức

type BatchQuotaResult struct {
	Total int `json:"total"`

	Success int `json:"success"`

	Failed int `json:"failed"`

	DurationMs int64 `json:"durationMs"`

	AccountData map[string]map[string]QuotaItem `json:"accountData"`

	Results map[string]map[string]QuotaItem `json:"results"`
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

		// Thử lấy từ cache RAM hoặc data JSON trong DB nếu live call thất bại

		if cached, ok := GetCachedQuotas(conn.ID); ok && len(cached) > 0 {

			log.Printf("[ENGINE] [OMNIROUTER] ⚠️ Live quota fetch thất bại cho %s (%v), dùng cache RAM", conn.Name, err)

			return cached, nil

		}

		var dataMap map[string]interface{}

		if json.Unmarshal(conn.Data, &dataMap) == nil {

			if qRaw, ok := dataMap["quotas"]; ok {

				var dbQuotas map[string]QuotaItem

				qBytes, _ := json.Marshal(qRaw)

				if json.Unmarshal(qBytes, &dbQuotas) == nil && len(dbQuotas) > 0 {

					log.Printf("[ENGINE] [OMNIROUTER] ⚠️ Live quota fetch thất bại cho %s (%v), dùng cache DB", conn.Name, err)

					return dbQuotas, nil

				}

			}

		}

		return nil, err

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

				fetchErr = err

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

		if resp.StatusCode == http.StatusOK {

			var modelsRes struct {
				Models map[string]struct {
					DisplayName string `json:"displayName"`

					QuotaInfo *struct {
						RemainingFraction float64 `json:"remainingFraction"`

						ResetTime string `json:"resetTime"`
					} `json:"quotaInfo"`

					IsInternal bool `json:"isInternal"`
				} `json:"models"`
			}

			if err := json.NewDecoder(resp.Body).Decode(&modelsRes); err == nil && modelsRes.Models != nil {

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

						Total: total,

						Used: used,

						RemainingPercentage: remPct,

						ResetAt: resetAtPtr,

						DisplayName: dispName,

						Unlimited: false,
					}

				}

				qMu.Unlock()

			}

		}

	}()

	// 2. Gọi retrieveUserQuotaSummary song song

	wg.Add(1)

	go func() {

		defer wg.Done()

		summaryReq, err := http.NewRequest("POST", AntigravityQuotaSummaryApiUrl, bytes.NewReader(bodyBytes))

		if err != nil {

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

				fetchErr = err

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

		if resp.StatusCode == http.StatusOK {

			var summaryRes struct {
				Groups []struct {
					DisplayName string `json:"displayName"`

					Buckets []struct {
						BucketId string `json:"bucketId"`

						DisplayName string `json:"displayName"`

						Window string `json:"window"`

						RemainingFraction float64 `json:"remainingFraction"`

						ResetTime string `json:"resetTime"`

						Disabled bool `json:"disabled"`
					} `json:"buckets"`
				} `json:"groups"`

				QuotaSummary *struct {
					Groups []struct {
						DisplayName string `json:"displayName"`

						Buckets []struct {
							BucketId string `json:"bucketId"`

							DisplayName string `json:"displayName"`

							Window string `json:"window"`

							RemainingFraction float64 `json:"remainingFraction"`

							ResetTime string `json:"resetTime"`

							Disabled bool `json:"disabled"`
						} `json:"buckets"`
					} `json:"groups"`
				} `json:"quotaSummary"`
			}

			dataBytes, _ := io.ReadAll(resp.Body)

			_ = json.Unmarshal(dataBytes, &summaryRes)

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

					isWeekly := win == "weekly" || strings.Contains(bucketText, "weekly")

					isSession := win == "5h" || strings.Contains(bucketText, "five hour") || strings.Contains(bucketText, "5h") || win == "daily" || strings.Contains(bucketText, "daily")

					if !isWeekly && !isSession {

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

					if isGemini {

						if isSession {

							if _, exists := quotas["gemini_session"]; !exists {

								quotas["gemini_session"] = QuotaItem{

									Total: total,

									Used: used,

									RemainingPercentage: remPct,

									ResetAt: resetAtPtr,

									DisplayName: "Gemini (5h)",

									Unlimited: false,
								}

							}

						} else if isWeekly {

							if _, exists := quotas["gemini_weekly"]; !exists {

								quotas["gemini_weekly"] = QuotaItem{

									Total: total,

									Used: used,

									RemainingPercentage: remPct,

									ResetAt: resetAtPtr,

									DisplayName: "Gemini (Weekly)",

									Unlimited: false,
								}

							}

						}

					} else if isClaudeGpt {

						if isSession {

							if _, exists := quotas["claude_gpt_session"]; !exists {

								quotas["claude_gpt_session"] = QuotaItem{

									Total: total,

									Used: used,

									RemainingPercentage: remPct,

									ResetAt: resetAtPtr,

									DisplayName: "Claude & GPT (5h)",

									Unlimited: false,
								}

							}

						} else if isWeekly {

							if _, exists := quotas["claude_gpt_weekly"]; !exists {

								quotas["claude_gpt_weekly"] = QuotaItem{

									Total: total,

									Used: used,

									RemainingPercentage: remPct,

									ResetAt: resetAtPtr,

									DisplayName: "Claude & GPT (Weekly)",

									Unlimited: false,
								}

							}

						}

					}

				}

			}

			qMu.Unlock()

		}

	}()

	wg.Wait()

	if has401 {

		return nil, fmt.Errorf("Google Quota API trả về 401 Unauthorized")

	}

	if len(quotas) == 0 && fetchErr != nil {

		return nil, fetchErr

	}

	// 3. Reconcile: Nếu QuotaSummary thiếu gemini_session hoặc claude_gpt_session, tính từ models

	if _, ok := quotas["gemini_session"]; !ok {

		var minRem float64 = 100.0

		var bestItem *QuotaItem

		for k, v := range quotas {

			if strings.HasPrefix(k, "gemini-") && !strings.Contains(k, "image") {

				if bestItem == nil || v.RemainingPercentage < minRem {

					minRem = v.RemainingPercentage

					itemCopy := v

					itemCopy.DisplayName = "Gemini (5h)"

					bestItem = &itemCopy

				}

			}

		}

		if bestItem != nil {

			quotas["gemini_session"] = *bestItem

		}

	}

	if _, ok := quotas["claude_gpt_session"]; !ok {

		var minRem float64 = 100.0

		var bestItem *QuotaItem

		for k, v := range quotas {

			if strings.HasPrefix(k, "claude-") || strings.HasPrefix(k, "gpt-") {

				if bestItem == nil || v.RemainingPercentage < minRem {

					minRem = v.RemainingPercentage

					itemCopy := v

					itemCopy.DisplayName = "Claude & GPT (5h)"

					bestItem = &itemCopy

				}

			}

		}

		if bestItem != nil {

			quotas["claude_gpt_session"] = *bestItem

		}

	}

	// 4. Gemini 3.1 Flash Image

	if img, ok := quotas["gemini-3.1-flash-image"]; ok {

		imgCopy := img

		imgCopy.DisplayName = "Gemini 3.1 Flash Image"

		quotas["gemini_flash_image"] = imgCopy

	} else if _, ok := quotas["gemini_flash_image"]; !ok {

		quotas["gemini_flash_image"] = QuotaItem{

			Total: 1000,

			Used: 0,

			RemainingPercentage: 100.0,

			DisplayName: "Gemini 3.1 Flash Image",

			Unlimited: false,
		}

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

		Total: len(accounts),

		AccountData: make(map[string]map[string]QuotaItem),

		Results: make(map[string]map[string]QuotaItem),
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

				result.Success++

				result.AccountData[acc.ID] = qMap

				result.Results[acc.ID] = qMap

			} else {

				result.Failed++

				// Nếu live fetch lỗi nhưng có cache cũ trong RAM hoặc DB thì vẫn nạp để UI không bị rỗng

				if cached, ok := GetCachedQuotas(acc.ID); ok && len(cached) > 0 {

					result.AccountData[acc.ID] = cached

					result.Results[acc.ID] = cached

				}

			}

			mu.Unlock()

		}(accounts[i], i)

	}

	wg.Wait()

	result.DurationMs = time.Since(startTime).Milliseconds()

	log.Printf("[ENGINE] [OMNIROUTER] 📊 Hoàn tất làm mới hạn mức: %d/%d tài khoản thành công (%dms)", result.Success, result.Total, result.DurationMs)

	// Phát sự kiện SSE để UI cập nhật số liệu ngay lập tức (Zero Latency)

	if result.Success > 0 {

		BroadcastLog("QUOTA", fmt.Sprintf("✔ Đã đồng bộ thời gian thực %d/%d tài khoản AI (%dms)", result.Success, result.Total, result.DurationMs))

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
