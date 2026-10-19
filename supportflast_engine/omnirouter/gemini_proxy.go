package omnirouter

import (
	"bytes"
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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// upstreamRealHost lưu IP thật của daily-cloudcode-pa.googleapis.com (bypass hosts file)
var upstreamRealHost string

// geminiProxyClient HTTP client kết nối trực tiếp IP thật (bypass hosts redirect 127.0.0.1)
var geminiProxyClient *http.Client

func init() {
	// Resolve IP thật qua DNS 8.8.8.8 (bỏ qua hosts file)
	resolvedIP := resolveRealIP("daily-cloudcode-pa.googleapis.com")
	if resolvedIP != "" && resolvedIP != "127.0.0.1" && resolvedIP != "::1" && resolvedIP != "0.0.0.0" {
		upstreamRealHost = resolvedIP
	} else {
		upstreamRealHost = "172.217.113.4" // Fallback IP tĩnh Google
		log.Printf("[ENGINE] [GEMINI-PROXY] ⚠ DNS resolve trả về loopback hoặc trống, dùng IP fallback: %s", upstreamRealHost)
	}

	geminiProxyClient = &http.Client{
		Timeout: 300 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				// Khi request đi đến daily-cloudcode-pa.googleapis.com:443 hoặc cloudcode-pa.googleapis.com:443,
				// thay bằng IP thật để bypass hosts file redirect 127.0.0.1
				host, port, _ := net.SplitHostPort(addr)
				hostLower := strings.ToLower(host)
				if hostLower == "daily-cloudcode-pa.googleapis.com" || 
				   hostLower == "cloudcode-pa.googleapis.com" || 
				   hostLower == "generativelanguage.googleapis.com" || 
				   strings.HasSuffix(hostLower, ".googleapis.com") {
					realAddr := net.JoinHostPort(upstreamRealHost, port)
					return net.DialTimeout(network, realAddr, 15*time.Second)
				}
				return net.DialTimeout(network, addr, 15*time.Second)
			},
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: false,
			},
			MaxIdleConns:        50,
			MaxIdleConnsPerHost: 20,
			IdleConnTimeout:     120 * time.Second,
		},
	}
}

var proxyClientCache sync.Map // proxyURL -> *http.Client

// getClientForAccount trả về HTTP client được định tuyến qua proxy riêng của tài khoản hoặc geminiProxyClient nếu kết nối trực tiếp
func getClientForAccount(proxyURL string) *http.Client {
	if proxyURL == "" {
		return geminiProxyClient
	}
	if clientVal, ok := proxyClientCache.Load(proxyURL); ok {
		return clientVal.(*http.Client)
	}
	pParsed, err := url.Parse(proxyURL)
	if err != nil {
		return geminiProxyClient
	}
	tr := &http.Transport{
		Proxy: http.ProxyURL(pParsed),
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: false,
		},
		MaxIdleConns:        50,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     120 * time.Second,
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   300 * time.Second,
	}
	proxyClientCache.Store(proxyURL, client)
	return client
}

// resolveRealIP dùng DNS 8.8.8.8 để lấy IP thật (bypass hosts file)
func resolveRealIP(hostname string) string {
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "udp", "8.8.8.8:53")
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	addrs, err := resolver.LookupHost(ctx, hostname)
	if err != nil || len(addrs) == 0 {
		log.Printf("[ENGINE] [GEMINI-PROXY] ⚠ Không thể resolve DNS cho %s: %v, dùng IP fallback", hostname, err)
		return ""
	}
	// Ưu tiên IPv4
	for _, addr := range addrs {
		if net.ParseIP(addr).To4() != nil {
			log.Printf("[ENGINE] [GEMINI-PROXY] ✔ Đã resolve IP thật cho %s: %s", hostname, addr)
			return addr
		}
	}
	log.Printf("[ENGINE] [GEMINI-PROXY] ✔ Đã resolve IP (IPv6) cho %s: %s", hostname, addrs[0])
	return addrs[0]
}

// HandleGeminiNativeProxy xử lý transparent proxy cho Antigravity IDE
// IDE gửi request Gemini native (v1internal:streamGenerateContent) đến localhost:443
// Handler thay OAuth token của user bằng token fresh từ tài khoản AI pool,
// forward lên daily-cloudcode-pa.googleapis.com IP thật, pipe response nguyên bản
func HandleGeminiNativeProxy(db *sql.DB) http.HandlerFunc {
	StartClientTelemetryHeartbeat(db)
	return func(w http.ResponseWriter, r *http.Request) {
		startTime := time.Now()

		// CORS preflight
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Goog-Api-Key, X-Goog-Api-Client")
			w.WriteHeader(http.StatusOK)
			return
		}

		// Đọc body request gốc từ IDE
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("[ENGINE] [GEMINI-PROXY] ✗ Lỗi đọc body: %v", err)
			http.Error(w, `{"error":"Cannot read request body"}`, http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		// Trích model từ request body (nếu có)
		model := extractModelFromBody(bodyBytes)

		// Chuẩn hóa model và xây dựng cascade fallback tiers - TUYỆT ĐỐI CHỈ DÙNG CÁC MODEL CAO CẤP
		var modelTiers []string
		if model == "gemini-pro-agent" || model == "gemini-3.1-pro-high" || model == "gemini-3.1-pro" || model == "gemini-3.1-pro-low" {
			// Model cao cấp của Google Cloud Code PA hỗ trợ reasoning: ưu tiên 3.8-flash-high và 2.5-pro
			modelTiers = []string{"gemini-3.8-flash-high", "gemini-2.5-pro", "gemini-3-flash-agent"}
		} else if strings.HasPrefix(model, "gemini-3.7") || strings.HasPrefix(model, "gemini-3.6") {
			modelTiers = []string{"gemini-3.8-flash-high", "gemini-2.5-pro"}
		} else if model == "gemini-2.5-pro" {
			modelTiers = []string{"gemini-2.5-pro", "gemini-3.8-flash-high"}
		} else if model == "gemini-3.8-flash-high" {
			modelTiers = []string{"gemini-3.8-flash-high", "gemini-2.5-pro"}
		} else if model != "" && model != "gemini-native" && model != "functionCall" {
			// Thử model người dùng chọn, nếu upstream lỗi thì fallback sang các model cao cấp
			modelTiers = []string{model, "gemini-3.8-flash-high", "gemini-2.5-pro"}
		} else {
			modelTiers = []string{"gemini-3.8-flash-high", "gemini-2.5-pro"}
		}

		log.Printf("[ENGINE] [GEMINI-PROXY] ⚡ Antigravity IDE: %s %s (Host: %s, Model: %s, Tiers: %v)", r.Method, r.URL.Path, r.Host, model, modelTiers)

		// Lấy danh sách tài khoản AI khả dụng
		accounts := GetCachedActiveProviders()
		if len(accounts) == 0 {
			log.Printf("[ENGINE] [GEMINI-PROXY] ✗ Không có tài khoản AI nào khả dụng")
			http.Error(w, `{"error":"No AI accounts available"}`, http.StatusServiceUnavailable)
			return
		}

		// Lọc tài khoản không bị cooldown
		var readyAccounts []ProviderConnection
		now := time.Now()
		for _, acc := range accounts {
			if coolUntil, ok := accountCooldownMap.Load(acc.ID); ok {
				if now.Before(coolUntil.(time.Time)) {
					continue
				}
			}
			readyAccounts = append(readyAccounts, acc)
		}
		if len(readyAccounts) == 0 {
			readyAccounts = accounts
		}

		numAccounts := len(readyAccounts)
		startIdx := int(atomic.AddUint64(&rrCounter, 1) % uint64(numAccounts))

		var lastErr error
		var resp *http.Response
		var usedAccount *ProviderConnection
		var successfulModel string

		// Duyệt tuần tự qua các model tiers (Cascade Fallback)
		for tierIdx, tierModel := range modelTiers {
			maxAttempts := numAccounts
			if maxAttempts > 19 {
				maxAttempts = 19
			}

			// Xoay vòng tài khoản (Load balancing & Failover)
			for attempt := 0; attempt < maxAttempts; attempt++ {
				idx := (startIdx + attempt) % numAccounts
				acc := readyAccounts[idx]

				creds, cErr := GetValidCredentials(db, &acc)
				if cErr != nil {
					lastErr = cErr
					continue
				}

				// Xác định Project ID chuẩn mực: Antigravity IDE mặc định sử dụng "aicode-consumers"
				targetProject := "aicode-consumers"
				if strings.TrimSpace(creds.ProjectId) != "" && strings.Contains(strings.TrimSpace(creds.ProjectId), "aicode") {
					targetProject = strings.TrimSpace(creds.ProjectId)
				}

				// Chuẩn hóa JSON body an toàn tuyệt đối
				finalBodyBytes := bodyBytes
				if len(finalBodyBytes) > 0 && finalBodyBytes[0] == '{' {
					var rawJSON map[string]interface{}
					if jErr := json.Unmarshal(finalBodyBytes, &rawJSON); jErr == nil && rawJSON != nil {
						rawJSON["project"] = targetProject
						if tierModel != "" && rawJSON["model"] != nil {
							rawJSON["model"] = tierModel
						}
						if patched, pErr := json.Marshal(rawJSON); pErr == nil {
							finalBodyBytes = patched
						}
					}
				} else if model != "" && tierModel != model && model != "gemini-native" && tierModel != "gemini-native" {
					if bytes.Contains(bodyBytes, []byte(`"`+model+`"`)) {
						finalBodyBytes = bytes.Replace(bodyBytes, []byte(`"`+model+`"`), []byte(`"`+tierModel+`"`), 1)
					}
				}

				// Xây dựng URL upstream thật
				targetHost := "daily-cloudcode-pa.googleapis.com"
				reqHostLower := strings.ToLower(r.Host)
				if strings.HasPrefix(r.URL.Path, "/v1internal") {
					// Tất cả PredictionService nội bộ của Antigravity đều nằm trên daily-cloudcode-pa
					targetHost = "daily-cloudcode-pa.googleapis.com"
				} else if strings.Contains(reqHostLower, "generativelanguage.googleapis.com") {
					targetHost = "generativelanguage.googleapis.com"
				} else if strings.Contains(reqHostLower, "cloudcode-pa.googleapis.com") && !strings.Contains(reqHostLower, "daily-") {
					targetHost = "cloudcode-pa.googleapis.com"
				} else if r.Host != "" && !strings.Contains(reqHostLower, "127.0.0.1") && !strings.Contains(reqHostLower, "localhost") {
					targetHost = r.Host
				}
				upstreamURL := "https://" + targetHost + r.URL.Path
				if r.URL.RawQuery != "" {
					upstreamURL += "?" + r.URL.RawQuery
				}

				// Tạo request mới với finalBodyBytes
				fReq, reqErr := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, bytes.NewReader(finalBodyBytes))
				if reqErr != nil {
					lastErr = reqErr
					continue
				}

				// Copy headers gốc từ IDE (loại bỏ Authorization, Host, Connection và User-Project cũ)
				for key, values := range r.Header {
					keyLower := strings.ToLower(key)
					if keyLower == "authorization" || keyLower == "host" || keyLower == "connection" || keyLower == "x-goog-user-project" {
						continue
					}
					for _, v := range values {
						fReq.Header.Add(key, v)
					}
				}

				// Thiết lập Authorization chuẩn xác theo tài khoản Pool (KHÔNG thêm X-Goog-User-Project để tránh xung đột quota project cá nhân)
				fReq.Header.Set("Authorization", "Bearer "+creds.AccessToken)
				fReq.Header.Set("Content-Type", "application/json")
				fReq.Header.Set("X-Client-Name", "antigravity")
				if fReq.Header.Get("User-Agent") == "" {
					fReq.Header.Set("User-Agent", "antigravity/ide/2.24.0 windows/amd64")
				}

				// Chọn HTTP client phù hợp (qua Proxy của tài khoản nếu có cấu hình, hoặc Direct)
				targetClient := geminiProxyClient
				if creds.ConnectionProxyEnabled && creds.ProxyUrl != "" {
					targetClient = getClientForAccount(creds.ProxyUrl)
				}

				rResp, doErr := targetClient.Do(fReq)
				if doErr != nil {
					lastErr = doErr
					continue
				}

				// Tự động Refresh Token nếu gặp 401 Unauthorized
				if rResp.StatusCode == http.StatusUnauthorized && creds.RefreshToken != "" {
					rResp.Body.Close()
					log.Printf("[ENGINE] [GEMINI-PROXY] ⚠ Token hết hạn (401) trên %s, tự động làm mới token...", acc.Name)
					newTok, newExpiry, newRefresh, refErr := RefreshGoogleToken(creds.RefreshToken)
					if refErr == nil {
						creds.AccessToken = newTok
						creds.ExpiresAt = newExpiry
						var dataMap map[string]interface{}
						_ = json.Unmarshal(acc.Data, &dataMap)
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
						_ = UpdateProviderData(db, acc.ID, dataMap)

						// Thử lại ngay với token mới
						fReqRetry, _ := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, bytes.NewReader(finalBodyBytes))
						fReqRetry.Header = fReq.Header.Clone()
						fReqRetry.Header.Set("Authorization", "Bearer "+newTok)
						if rRespRetry, retryErr := targetClient.Do(fReqRetry); retryErr == nil {
							rResp = rRespRetry
						}
					}
				}

				if rResp.StatusCode == http.StatusTooManyRequests {
					rResp.Body.Close()
					accountCooldownMap.Store(acc.ID, time.Now().Add(30*time.Second))
					lastErr = fmt.Errorf("rate limited (429) on %s for model %s", acc.Name, tierModel)
					log.Printf("[ENGINE] [GEMINI-PROXY] ⚠ Rate limited (429) trên %s cho model %s, chuyển tài khoản tiếp...", acc.Name, tierModel)
					continue
				}

				if rResp.StatusCode != http.StatusOK {
					// Đọc body lỗi để log
					errBody, _ := io.ReadAll(io.LimitReader(rResp.Body, 512))
					rResp.Body.Close()
					errStr := string(errBody)
					lastErr = fmt.Errorf("upstream error %d on %s for %s: %s", rResp.StatusCode, acc.Name, tierModel, errStr)
					log.Printf("[ENGINE] [GEMINI-PROXY] ⚠ Upstream lỗi %d trên %s cho model %s: %s", rResp.StatusCode, acc.Name, tierModel, errStr)

					if strings.Contains(errStr, "SUBSCRIPTION_REQUIRED") || strings.Contains(errStr, "#3501") {
						accountCooldownMap.Store(acc.ID, time.Now().Add(1*time.Hour))
						log.Printf("[ENGINE] [GEMINI-PROXY] ⚠ Tài khoản %s thiếu subscription (#3501), tạm thời cooldown 1h", acc.Name)
					}

					// Nếu lỗi là do hết capacity (503 MODEL_CAPACITY_EXHAUSTED) hoặc model không hỗ trợ,
					// ngắt vòng lặp tài khoản ngay để kích hoạt fallback sang Tier tiếp theo
					if strings.Contains(errStr, "MODEL_CAPACITY_EXHAUSTED") || rResp.StatusCode == http.StatusServiceUnavailable {
						log.Printf("[ENGINE] [GEMINI-PROXY] ⚡ Model %s bị cạn capacity (503). Chuyển mạch tầng dự phòng...", tierModel)
						break
					}
					continue
				}

				resp = rResp
				usedAccount = &acc
				successfulModel = tierModel
				break
			}

			if resp != nil {
				if tierIdx > 0 {
					log.Printf("[ENGINE] [GEMINI-PROXY] 🔀 Cascade Fallback thành công: %s -> %s", model, successfulModel)
				}
				break
			}
		}

		if resp == nil {
			errMsg := fmt.Sprintf(`{"error":{"message":"Toàn bộ tài khoản và fallback tiers đều bận hoặc lỗi: %v","code":"gateway_error"}}`, lastErr)
			log.Printf("[ENGINE] [GEMINI-PROXY] ✗ Tất cả tài khoản và tiers đều thất bại: %v", lastErr)
			now := time.Now().UTC()
			GlobalLogHub.RecordLog(RouterLogEntry{
				Timestamp:    now.Format("15:04:05.000"),
				TimestampISO: now.Format(time.RFC3339Nano),
				ClientDevice: r.RemoteAddr,
				APIKeyName:   "Antigravity IDE (Native Gemini Proxy)",
				Endpoint:     r.URL.Path,
				Method:       r.Method,
				Provider:     "FAILOVER",
				Model:        model,
				LatencyMs:    time.Since(startTime).Milliseconds(),
				StatusCode:   http.StatusBadGateway,
				ErrorMessage: fmt.Sprintf("%v", lastErr),
			})
			http.Error(w, errMsg, http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		// Pipe response headers nguyên bản từ upstream về IDE
		for key, values := range resp.Header {
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}
		w.WriteHeader(resp.StatusCode)

		// Pipe response body nguyên bản (SSE stream hoặc JSON)
		// Đây là transparent proxy nên IDE nhận đúng format Gemini native
		flusher, isFlusher := w.(http.Flusher)

		// Nếu là request Quota (retrieveUserQuotaSummary hoặc fetchAvailableModels), hứng lại body để sync số liệu thật
		isQuotaReq := strings.Contains(r.URL.Path, "retrieveUserQuotaSummary") || strings.Contains(r.URL.Path, "fetchAvailableModels")
		var quotaRespBuf bytes.Buffer
		var totalRespBytes int64

		buf := make([]byte, 4096)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				totalRespBytes += int64(n)
				if isQuotaReq {
					quotaRespBuf.Write(buf[:n])
				}
				_, writeErr := w.Write(buf[:n])
				if writeErr != nil {
					break
				}
				if isFlusher {
					flusher.Flush()
				}
			}
			if readErr != nil {
				break
			}
		}

		if isQuotaReq && quotaRespBuf.Len() > 0 && usedAccount != nil {
			go syncLiveQuotaFromUpstreamResponse(db, usedAccount, r.URL.Path, quotaRespBuf.Bytes())
		}

		latencyMs := time.Since(startTime).Milliseconds()
		if usedAccount != nil {
			effectiveModel := successfulModel
			if effectiveModel == "" {
				effectiveModel = model
			}
			promptTokens := int64(len(bodyBytes) / 4)
			if promptTokens < 120 {
				promptTokens = 120
			}
			completionTokens := totalRespBytes / 4
			if completionTokens < 250 {
				completionTokens = 250
			}
			log.Printf("[ENGINE] [GEMINI-PROXY] ✔ Proxy hoàn thành qua %s (%s) - %dms - Tokens: %d - Path: %s", usedAccount.Name, effectiveModel, latencyMs, promptTokens+completionTokens, r.URL.Path)
			now := time.Now().UTC()
			GlobalLogHub.RecordLog(RouterLogEntry{
				Timestamp:        now.Format("15:04:05.000"),
				TimestampISO:     now.Format(time.RFC3339Nano),
				ClientDevice:     r.RemoteAddr,
				APIKeyName:       "Antigravity IDE (Native Gemini Proxy)",
				Endpoint:         r.URL.Path,
				Method:           r.Method,
				Provider:         usedAccount.Provider,
				AccountID:        usedAccount.ID,
				AccountName:      usedAccount.Email,
				Model:            effectiveModel,
				LatencyMs:        latencyMs,
				StatusCode:       http.StatusOK,
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				IsStream:         strings.Contains(r.URL.RawQuery, "alt=sse"),
			})

			// Ghi nhận trừ Quota trực tiếp vào TiDB Cloud và gửi Live Telemetry lên Web
			ReportProxyUsageAsync(db, effectiveModel, usedAccount.Email, usedAccount.Name, r.URL.Path, latencyMs, http.StatusOK, promptTokens, completionTokens)
		}
	}
}

// extractModelFromBody trích model name từ JSON body
func extractModelFromBody(body []byte) string {
	if len(body) == 0 {
		return "gemini-3.8-flash-high"
	}
	var req struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &req); err == nil && req.Model != "" {
		m := strings.TrimSpace(req.Model)
		if m != "functionCall" && m != "auto" && m != "none" && m != "" {
			return m
		}
	}
	s := string(body)
	idx := strings.Index(s, `"model"`)
	if idx != -1 {
		sub := s[idx+7:]
		colonIdx := strings.Index(sub, ":")
		if colonIdx != -1 {
			sub = sub[colonIdx+1:]
			quoteStart := strings.Index(sub, `"`)
			if quoteStart != -1 {
				sub = sub[quoteStart+1:]
				quoteEnd := strings.Index(sub, `"`)
				if quoteEnd != -1 {
					m := sub[:quoteEnd]
					if m != "functionCall" && m != "auto" && m != "none" && m != "" {
						return m
					}
				}
			}
		}
	}
	return "gemini-3.8-flash-high"
}

var (
	currentActiveClientKey   string
	currentActiveClientKeyMu sync.RWMutex
	telemetryHeartbeatOnce   sync.Once
)

// SetActiveClientKey cập nhật API Key đang hoạt động trong RAM
func SetActiveClientKey(key string) {
	currentActiveClientKeyMu.Lock()
	currentActiveClientKey = strings.TrimSpace(key)
	currentActiveClientKeyMu.Unlock()
	log.Printf("[ENGINE] [KEY] ✔ Đã cập nhật Active Client Key: %s", maskAPIKey(key))
}

// GetActiveClientKey tìm kiếm và trả về API Key hợp lệ đang được client sử dụng
func GetActiveClientKey(db *sql.DB) string {
	currentActiveClientKeyMu.RLock()
	key := currentActiveClientKey
	currentActiveClientKeyMu.RUnlock()
	if key != "" {
		return key
	}

	currentActiveClientKeyMu.Lock()
	defer currentActiveClientKeyMu.Unlock()
	if currentActiveClientKey != "" {
		return currentActiveClientKey
	}

	// 1. Thử đọc từ config.json ở các vị trí khả dụng
	candidatePaths := []string{
		"config.json",
		filepath.Join("storage", "packages", "APP-ROUTER_OmniRouter_TiDB_v2.9.0", "config.json"),
	}
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidatePaths = append(candidatePaths, filepath.Join(exeDir, "config.json"))
		candidatePaths = append(candidatePaths, filepath.Join(filepath.Dir(exeDir), "config.json"))
	}

	for _, p := range candidatePaths {
		if data, err := os.ReadFile(p); err == nil && len(data) > 0 {
			var cfg map[string]interface{}
			if err := json.Unmarshal(data, &cfg); err == nil {
				if k, ok := cfg["api_key"].(string); ok && strings.HasPrefix(strings.TrimSpace(k), "sk-") {
					currentActiveClientKey = strings.TrimSpace(k)
					log.Printf("[ENGINE] [KEY] ✔ Nạp Active Key từ '%s': %s", p, maskAPIKey(currentActiveClientKey))
					return currentActiveClientKey
				}
				if k, ok := cfg["master_api_key"].(string); ok && strings.HasPrefix(strings.TrimSpace(k), "sk-") {
					currentActiveClientKey = strings.TrimSpace(k)
					log.Printf("[ENGINE] [KEY] ✔ Nạp Active Master Key từ '%s': %s", p, maskAPIKey(currentActiveClientKey))
					return currentActiveClientKey
				}
			}
		}
	}

	// 2. Nếu chưa có trong config.json, truy vấn TiDB Cloud lấy khóa người dùng gần nhất
	if db != nil {
		var dbKey string
		err := db.QueryRow("SELECT api_key FROM omni_client_keys WHERE is_active = 1 AND api_key LIKE 'sk-%' AND name != 'Master Web Domain Key' ORDER BY created_at DESC LIMIT 1").Scan(&dbKey)
		if err == nil && dbKey != "" {
			currentActiveClientKey = dbKey
			log.Printf("[ENGINE] [KEY] ✔ Nạp Active Key từ TiDB Cloud: %s", maskAPIKey(currentActiveClientKey))
			return currentActiveClientKey
		}

		// Fallback sang bất kỳ active key nào
		err = db.QueryRow("SELECT api_key FROM omni_client_keys WHERE is_active = 1 ORDER BY created_at DESC LIMIT 1").Scan(&dbKey)
		if err == nil && dbKey != "" {
			currentActiveClientKey = dbKey
			log.Printf("[ENGINE] [KEY] ✔ Nạp Active Fallback Key từ TiDB Cloud: %s", maskAPIKey(currentActiveClientKey))
			return currentActiveClientKey
		}
	}

	return ""
}

var (
	cachedWebServerURL   string
	cachedWebServerURLMu sync.RWMutex
)

// SetCachedWebServerURL cho phép cập nhật URL máy chủ web Render
func SetCachedWebServerURL(u string) {
	cachedWebServerURLMu.Lock()
	defer cachedWebServerURLMu.Unlock()
	cachedWebServerURL = strings.TrimRight(u, "/")
}

// GetWebServerURL lấy URL máy chủ Web Render để gửi báo cáo telemetry (có cache RAM an toàn)
func GetWebServerURL() string {
	cachedWebServerURLMu.RLock()
	if cachedWebServerURL != "" {
		u := cachedWebServerURL
		cachedWebServerURLMu.RUnlock()
		return u
	}
	cachedWebServerURLMu.RUnlock()

	cachedWebServerURLMu.Lock()
	defer cachedWebServerURLMu.Unlock()
	if cachedWebServerURL != "" {
		return cachedWebServerURL
	}

	candidatePaths := []string{
		"config.json",
		filepath.Join("storage", "packages", "APP-ROUTER_OmniRouter_TiDB_v2.9.0", "config.json"),
	}
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidatePaths = append(candidatePaths, filepath.Join(exeDir, "config.json"))
		candidatePaths = append(candidatePaths, filepath.Join(filepath.Dir(exeDir), "config.json"))
	}
	for _, p := range candidatePaths {
		if data, err := os.ReadFile(p); err == nil && len(data) > 0 {
			var cfg map[string]interface{}
			if err := json.Unmarshal(data, &cfg); err == nil {
				if u, ok := cfg["web_server_url"].(string); ok && u != "" {
					cachedWebServerURL = strings.TrimRight(u, "/")
					return cachedWebServerURL
				}
				if u, ok := cfg["render_url"].(string); ok && u != "" {
					cachedWebServerURL = strings.TrimRight(u, "/")
					return cachedWebServerURL
				}
			}
		}
	}
	cachedWebServerURL = "https://www.supportflastdev.io.vn"
	return cachedWebServerURL
}

// ReportProxyUsageAsync ghi nhận Quota trên TiDB Cloud và gửi báo cáo telemetry thời gian thực lên Web Server
func ReportProxyUsageAsync(db *sql.DB, effectiveModel, accountEmail, accountName, reqPath string, latencyMs int64, statusCode int, promptTokens, completionTokens int64) {
	go func() {
		activeKey := GetActiveClientKey(db)
		totTokens := promptTokens + completionTokens
		if totTokens <= 0 {
			totTokens = 520
		}

		// 1. Ghi nhận trừ Quota trực tiếp trên CSDL TiDB Cloud Serverless (nếu có key thật)
		if db != nil && activeKey != "" {
			RecordKeyUsageAsync(db, activeKey, 1, totTokens)
			log.Printf("[ENGINE] [QUOTA] ✔ Đã ghi nhận +1 request, %d tokens vào Quota của Key: %s trên TiDB Cloud", totTokens, maskAPIKey(activeKey))
		}

		if activeKey == "" {
			activeKey = "sk-desktop-native-proxy"
		}

		// 2. Gửi gói tin telemetry lên Web Server Render để Web Console hiển thị thời gian thực
		webURL := GetWebServerURL()
		reportURL := webURL + "/api/v1/client/report"

		payload := ClientReportPayload{
			ApiKey:          activeKey,
			UsedRequests:    1,
			TotalTokens:     totTokens,
			Model:           effectiveModel,
			Endpoint:        reqPath,
			AccountName:     accountEmail,
			ClientDevice:    "Antigravity IDE (Desktop)",
			LatencyMs:       latencyMs,
			StatusCode:      statusCode,
			LogMessage:      fmt.Sprintf("[IDE PROXY] Antigravity IDE gọi thành công model %s qua %s (%dms - %d tokens)", effectiveModel, accountName, latencyMs, totTokens),
			SkipDBIncrement: (db != nil), // Nếu đã ghi trực tiếp TiDB Cloud thì Web server chỉ việc nhận log, không tăng Quota lần 2
		}

		bodyBytes, err := json.Marshal(payload)
		if err != nil {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reportURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		req.Header.Set("Authorization", "Bearer "+activeKey)
		req.Header.Set("x-api-key", activeKey)

		client := &http.Client{Timeout: 4 * time.Second}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
}

// StartClientTelemetryHeartbeat định kỳ gửi heartbeat từ Desktop App lên Web Server
func StartClientTelemetryHeartbeat(db *sql.DB) {
	telemetryHeartbeatOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(45 * time.Second)
			defer ticker.Stop()

			hostName, _ := os.Hostname()
			if hostName == "" {
				hostName = "PC-Client"
			}

			for range ticker.C {
				activeKey := GetActiveClientKey(db)
				if activeKey == "" {
					continue
				}

				webURL := GetWebServerURL()
				hbURL := webURL + "/api/v1/client/heartbeat"

				payload := map[string]interface{}{
					"event":    "active",
					"client":   "Antigravity IDE (Desktop)",
					"hostname": hostName,
					"api_key":  activeKey,
				}

				b, _ := json.Marshal(payload)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, hbURL, bytes.NewReader(b))
				if err == nil {
					req.Header.Set("Content-Type", "application/json; charset=utf-8")
					client := &http.Client{Timeout: 3 * time.Second}
					if resp, rErr := client.Do(req); rErr == nil {
						_ = resp.Body.Close()
					}
				}
				cancel()
			}
		}()
	})
}

// syncLiveQuotaFromUpstreamResponse trích xuất hạn mức chính xác 100% từ phản hồi của Google Cloud Code PA mà Antigravity IDE vừa nhận
func syncLiveQuotaFromUpstreamResponse(db *sql.DB, acc *ProviderConnection, reqPath string, respBytes []byte) {
	if len(respBytes) == 0 || acc == nil {
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
		Models map[string]struct {
			DisplayName string `json:"displayName"`
			QuotaInfo   *struct {
				RemainingFraction float64 `json:"remainingFraction"`
				ResetTime         string  `json:"resetTime"`
			} `json:"quotaInfo"`
		} `json:"models"`
	}

	if err := json.Unmarshal(respBytes, &summaryRes); err != nil {
		return
	}

	parsedQuotas := make(map[string]QuotaItem)
	groups := summaryRes.Groups
	if len(groups) == 0 && summaryRes.QuotaSummary != nil {
		groups = summaryRes.QuotaSummary.Groups
	}

	for _, g := range groups {
		gName := strings.ToLower(g.DisplayName)
		isGem := strings.Contains(gName, "gemini")
		isCld := strings.Contains(gName, "claude") || strings.Contains(gName, "gpt")

		for _, b := range g.Buckets {
			win := strings.ToLower(b.Window)
			bText := strings.ToLower(b.BucketId + " " + b.DisplayName)
			isWeekly := win == "weekly" || strings.Contains(bText, "weekly")
			isSession := win == "5h" || strings.Contains(bText, "five hour") || strings.Contains(bText, "5h") || win == "daily"

			if !isWeekly && !isSession {
				continue
			}

			remFrac := b.RemainingFraction
			if b.Disabled {
				remFrac = 0
			}
			if remFrac < 0 {
				remFrac = 0
			} else if remFrac > 1 {
				remFrac = 1
			}

			remPct := math.Round(remFrac*1000) / 10
			total := 1000
			remCount := int(math.Round(float64(total) * remFrac))
			used := total - remCount

			var rPtr *string
			if b.ResetTime != "" {
				rTime := b.ResetTime
				rPtr = &rTime
			}

			if isGem {
				if isSession {
					parsedQuotas["gemini_session"] = QuotaItem{
						Total: total, Used: used, RemainingPercentage: remPct,
						ResetAt: rPtr, DisplayName: "Gemini (5h)", Unlimited: false,
					}
				} else if isWeekly {
					parsedQuotas["gemini_weekly"] = QuotaItem{
						Total: total, Used: used, RemainingPercentage: remPct,
						ResetAt: rPtr, DisplayName: "Gemini (Weekly)", Unlimited: false,
					}
				}
			} else if isCld {
				if isSession {
					parsedQuotas["claude_gpt_session"] = QuotaItem{
						Total: total, Used: used, RemainingPercentage: remPct,
						ResetAt: rPtr, DisplayName: "Claude & GPT (5h)", Unlimited: false,
					}
				} else if isWeekly {
					parsedQuotas["claude_gpt_weekly"] = QuotaItem{
						Total: total, Used: used, RemainingPercentage: remPct,
						ResetAt: rPtr, DisplayName: "Claude & GPT (Weekly)", Unlimited: false,
					}
				}
			}
		}
	}

	// Nếu là fetchAvailableModels, trích quota các model con
	for mKey, mVal := range summaryRes.Models {
		if mVal.QuotaInfo != nil {
			remFrac := mVal.QuotaInfo.RemainingFraction
			if remFrac < 0 {
				remFrac = 0
			} else if remFrac > 1 {
				remFrac = 1
			}
			remPct := math.Round(remFrac*1000) / 10
			total := 1000
			remCount := int(math.Round(float64(total) * remFrac))
			var rPtr *string
			if mVal.QuotaInfo.ResetTime != "" {
				t := mVal.QuotaInfo.ResetTime
				rPtr = &t
			}
			disp := mVal.DisplayName
			if disp == "" {
				disp = mKey
			}
			parsedQuotas[mKey] = QuotaItem{
				Total: total, Used: total - remCount, RemainingPercentage: remPct,
				ResetAt: rPtr, DisplayName: disp, Unlimited: false,
			}
		}
	}

	if len(parsedQuotas) == 0 {
		return
	}

	// Lưu ngay vào RAM Cache của Go Engine
	SetCachedQuotas(acc.ID, parsedQuotas)

	// Cập nhật CSDL TiDB Cloud
	var dataMap map[string]interface{}
	if err := json.Unmarshal(acc.Data, &dataMap); err == nil {
		if dataMap == nil {
			dataMap = make(map[string]interface{})
		}
		dataMap["quotas"] = parsedQuotas
		dataMap["lastQuotaAt"] = NowISO()
		_ = UpdateProviderData(db, acc.ID, dataMap)
		log.Printf("[ENGINE] [QUOTA-SYNC] ✔ Đã đồng bộ Quota thực tế từ IDE cho tài khoản %s (%d hạn mức: Gemini 5h=%.1f%%, Weekly=%.1f%%)", 
			acc.Email, len(parsedQuotas), 
			parsedQuotas["gemini_session"].RemainingPercentage, 
			parsedQuotas["gemini_weekly"].RemainingPercentage)
	}

	// Gửi Live Telemetry về Web Render để Web UI cập nhật ngay lập tức
	activeKey := GetActiveClientKey(db)
	if activeKey != "" {
		webURL := GetWebServerURL()
		reportURL := webURL + "/api/v1/client/report"
		payload := ClientReportPayload{
			ApiKey:       activeKey,
			Model:        "QuotaSync",
			Endpoint:     reqPath,
			AccountName:  acc.Email,
			ClientDevice: "Antigravity IDE (Live Quota Sync)",
			StatusCode:   200,
			LogMessage: fmt.Sprintf("[LIVE QUOTA] Đồng bộ hạn mức chuẩn xác từ IDE cho %s: Gemini 5h=%.0f%%, Weekly=%.0f%%, Claude Weekly=%.0f%%", 
				acc.Email, 
				parsedQuotas["gemini_session"].RemainingPercentage,
				parsedQuotas["gemini_weekly"].RemainingPercentage,
				parsedQuotas["claude_gpt_weekly"].RemainingPercentage),
			SkipDBIncrement: true,
		}
		b, _ := json.Marshal(payload)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, reportURL, bytes.NewReader(b))
		if req != nil {
			req.Header.Set("Content-Type", "application/json; charset=utf-8")
			req.Header.Set("Authorization", "Bearer "+activeKey)
			cl := &http.Client{Timeout: 3 * time.Second}
			if resp, err := cl.Do(req); err == nil {
				_ = resp.Body.Close()
			}
		}
	}
}
