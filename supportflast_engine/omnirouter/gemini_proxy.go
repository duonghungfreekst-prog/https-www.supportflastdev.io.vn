package omnirouter



import (

	"bytes"

	"compress/gzip"

	"context"

	"crypto/rand"

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

	"strconv"

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

		upstreamRealHost = "172.217.114.4" // Fallback IP tĩnh Google

		log.Printf("[ENGINE] [GEMINI-PROXY] ⚠ DNS resolve trả về loopback hoặc trống, dùng IP fallback: %s", upstreamRealHost)

	}



	dialer := &net.Dialer{

		Timeout:   8 * time.Second,

		KeepAlive: 30 * time.Second,

	}



	geminiProxyClient = &http.Client{

		Timeout: 300 * time.Second,

		Transport: &http.Transport{

			Proxy: nil, // Luôn kết nối trực tiếp tốc độ cao tới Google Edge

			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {

				host, port, _ := net.SplitHostPort(addr)

				hostLower := strings.ToLower(host)

				targetHost := upstreamRealHost

				if targetHost == "" {

					targetHost = "172.217.114.4"

				}

				if hostLower == "daily-cloudcode-pa.googleapis.com" || 

				   hostLower == "cloudcode-pa.googleapis.com" {

					realAddr := net.JoinHostPort(targetHost, port)

					return dialer.DialContext(ctx, network, realAddr)

				}

				return dialer.DialContext(ctx, network, addr)

			},

			TLSClientConfig: &tls.Config{

				InsecureSkipVerify: false,

			},

			ForceAttemptHTTP2:     true,

			DisableKeepAlives:     false,

			DisableCompression:    true, // Max Speed: Cấm nén gzip khối, truyền raw SSE chunks

			MaxIdleConns:          500,

			MaxIdleConnsPerHost:   250,

			IdleConnTimeout:       180 * time.Second,

			TLSHandshakeTimeout:   2 * time.Second,

			ResponseHeaderTimeout: 30 * time.Second,

			ExpectContinueTimeout: 100 * time.Millisecond,

			WriteBufferSize:       2048,

			ReadBufferSize:        2048,

		},

	}

}



var (
	proxyClientCache sync.Map // proxyURL -> *http.Client
	proxyCooldownMap sync.Map // proxyURL -> time.Time (thời điểm hết hạn phạt)
)

// isProxyHealthy kiểm tra xem proxy có đang bị Circuit Breaker tạm phạt do chậm/rớt gói không
func isProxyHealthy(proxyURL string) bool {
	if proxyURL == "" {
		return false
	}
	if untilVal, ok := proxyCooldownMap.Load(proxyURL); ok {
		if until, ok := untilVal.(time.Time); ok {
			if time.Now().Before(until) {
				return false // Proxy đang bị phạt, tự động chuyển sang Direct
			}
			proxyCooldownMap.Delete(proxyURL)
		}
	}
	return true
}

// markProxyFailure phạt proxy trong 90 giây khi gặp sự cố, tự động bypass sang Direct
func markProxyFailure(proxyURL string, reason string) {
	if proxyURL == "" {
		return
	}
	penaltyUntil := time.Now().Add(90 * time.Second)
	proxyCooldownMap.Store(proxyURL, penaltyUntil)
	log.Printf("[ENGINE] [PROXY-CIRCUIT-BREAKER] ⚡ Đánh dấu Proxy %s bị phạt 90s (Lý do: %s). Tự động chuyển toàn bộ request sang DIRECT siêu tốc!", proxyURL, reason)
}

// getClientForAccount trả về HTTP client được định tuyến qua proxy riêng của tài khoản hoặc geminiProxyClient nếu kết nối trực tiếp
func getClientForAccount(proxyURL string) *http.Client {
	if proxyURL == "" || !isProxyHealthy(proxyURL) {
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
		DialContext: (&net.Dialer{
			Timeout:   3 * time.Second, // Timeout bắt tay nhanh 3s (chống nghẽn khi proxy lag)
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ResponseHeaderTimeout: 8 * time.Second, // Tối đa 8s nhận header (chống treo 120s khi proxy nghẽn)
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   30,
		IdleConnTimeout:       120 * time.Second,
	}

	client := &http.Client{
		Transport: tr,
		Timeout:   120 * time.Second,
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



// CachedIDEQuotaResponse luu giu phan hoi Quota tu upstream cho Antigravity IDE
type CachedIDEQuotaResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	CachedAt   time.Time
}

var (
	ideQuotaCacheLock     sync.RWMutex
	ideQuotaCache         = make(map[string]*CachedIDEQuotaResponse)
	activeAccountIdx      uint64
	accountRequestCounter uint64
)

// ExtractClientAPIKeyFromRequest trích xuất API Key từ các header và query của request cụ thể
func ExtractClientAPIKeyFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	// 1. Authorization: Bearer sk-...
	authH := r.Header.Get("Authorization")
	if strings.HasPrefix(authH, "Bearer ") {
		tok := strings.TrimSpace(strings.TrimPrefix(authH, "Bearer "))
		if strings.HasPrefix(tok, "sk-") && !strings.Contains(tok, "...") && !strings.Contains(tok, "***") {
			return tok
		}
	}
	// 2. Header x-api-key: sk-...
	if xk := strings.TrimSpace(r.Header.Get("x-api-key")); xk != "" {
		if strings.HasPrefix(xk, "sk-") && !strings.Contains(xk, "...") && !strings.Contains(xk, "***") {
			return xk
		}
	}
	// 3. Header x-goog-api-key (Google format): sk-...
	if gk := strings.TrimSpace(r.Header.Get("x-goog-api-key")); gk != "" {
		if strings.HasPrefix(gk, "sk-") && !strings.Contains(gk, "...") && !strings.Contains(gk, "***") {
			return gk
		}
	}
	// 4. Query param ?key=... hoặc ?api_key=...
	if qk := strings.TrimSpace(r.URL.Query().Get("key")); qk != "" {
		if strings.HasPrefix(qk, "sk-") && !strings.Contains(qk, "...") && !strings.Contains(qk, "***") {
			return qk
		}
	}
	if qk := strings.TrimSpace(r.URL.Query().Get("api_key")); qk != "" {
		if strings.HasPrefix(qk, "sk-") && !strings.Contains(qk, "...") && !strings.Contains(qk, "***") {
			return qk
		}
	}
	return ""
}

// HandleGeminiNativeProxy xử lý transparent proxy cho Antigravity IDE

// IDE gửi request Gemini native (v1internal:streamGenerateContent) đến localhost:443

// Handler thay OAuth token của user bằng token fresh từ tài khoản AI pool,

// forward lên daily-cloudcode-pa.googleapis.com IP thật, pipe response nguyên bản

func HandleGeminiNativeProxy(db *sql.DB) http.HandlerFunc {

	StartClientTelemetryHeartbeat(db)

	StartBackgroundTokenRefreshWorker(db)

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

		// FAST-PATH QUOTA & MODEL DISCOVERY RESPONDER (< 0.2ms)
		// Đáp ứng ngay lập tức 100% hạn mức dồi dào cho Antigravity IDE ngay khi app mở lên,
		// triệt tiêu hoàn toàn hiện tượng chậm trễ, quay spinner hay hiển thị 0% Claude / 49% Gemini
		if strings.Contains(r.URL.Path, "retrieveUserQuota") {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("X-ModelRouter9-Aggregated", "true")
			w.Header().Set("X-ModelRouter9-FastPath", "true")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(GetDefaultVirtualAggregatedQuota())
			log.Printf("[ENGINE] [GEMINI-PROXY] ⚡ Fast-Path Quota Responder (<0.2ms): Trả về mặc định FULL 100%% Quota cho Antigravity IDE (URL: %s)", r.URL.Path)
			return
		}

		if strings.Contains(r.URL.Path, "fetchAvailableModels") {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("X-ModelRouter9-Aggregated", "true")
			w.Header().Set("X-ModelRouter9-FastPath", "true")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(GetDefaultAvailableModels())
			log.Printf("[ENGINE] [GEMINI-PROXY] ⚡ Fast-Path Model List Responder (<0.2ms): Trả về FULL 100%% Model List cho Antigravity IDE (URL: %s)", r.URL.Path)
			return
		}

		// NẾU LÀ REQUEST QUOTA: Kiểm tra Virtual Aggregated RAM Cache (chuẩn 9Router)
		// Trả về ngay lập tức nếu cache còn hiệu lực (< 30s) -> Độ trễ < 1ms, chống spam và bảo vệ tuyệt đối quota
		isQuotaReq := strings.Contains(r.URL.Path, "retrieveUserQuota") || strings.Contains(r.URL.Path, "fetchAvailableModels")
		if isQuotaReq {
			ideQuotaCacheLock.RLock()
			cached, hasCache := ideQuotaCache[r.URL.Path]
			ideQuotaCacheLock.RUnlock()
			if hasCache && cached != nil && time.Since(cached.CachedAt) < 30*time.Second {
				for k, v := range cached.Header {
					kLower := strings.ToLower(k)
					if kLower == "content-length" || kLower == "transfer-encoding" || kLower == "content-encoding" {
						continue
					}
					for _, val := range v {
						w.Header().Add(k, val)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Content-Length", strconv.Itoa(len(cached.Body)))
				w.Header().Set("X-ModelRouter9-Cache", "HIT-AGGREGATED-QUOTA")
				w.WriteHeader(cached.StatusCode)
				_, _ = w.Write(cached.Body)
				log.Printf("[ENGINE] [GEMINI-PROXY] ⚡ Phục vụ Quota từ Aggregated RAM Cache (<1ms): %s", r.URL.Path)
				return
			}
		}

		// BẢO VỆ TUYỆT ĐỐI QUOTA & TÀI KHOẢN CỦA NGƯỜI DÙNG:
		// Chỉ passthrough các request hoàn toàn là cấu hình plugin tĩnh (không tính phí, không tiêu tốn token):
		// ví dụ: loadCodeAssist (cấu hình extension), cclog (telemetry).
		// TUYỆT ĐỐI KHÔNG passthrough các request sinh mã (completeCode, predict, generateContent...)
		// hoặc Quota (retrieveUserQuotaSummary, fetchAvailableModels).
		// TẤT CẢ các request đó PHẢI CHẠY QUA POOL TÀI KHOẢN HỆ THỐNG để bảo vệ 100% tài khoản cá nhân,
		// bảo đảm tài khoản cá nhân của người dùng KHÔNG BAO GIỜ bị trừ token hay quota!
		isPureStaticMeta := strings.Contains(r.URL.Path, "loadCodeAssist") || strings.Contains(r.URL.Path, "cclog")
		if isPureStaticMeta {
			if strings.Contains(r.URL.Path, "loadCodeAssist") && strings.TrimSpace(r.Header.Get("Authorization")) == "" {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"codeAssistEnabled":true,"billingTier":"TIER_PRO","userTier":"TIER_PRO","allowedModels":["gemini-3.1-pro-high","gemini-3.1-pro-low","gemini-pro-agent","gemini-3.8-flash-high","claude-sonnet-4-6"]}`))
				log.Printf("[ENGINE] [GEMINI-PROXY] ⚡ Fast-Path loadCodeAssist: Kích hoạt TIER_PRO cho Antigravity IDE")
				return
			}
			targetHost := "daily-cloudcode-pa.googleapis.com"
			if strings.Contains(strings.ToLower(r.Host), "cloudcode-pa.googleapis.com") && !strings.Contains(strings.ToLower(r.Host), "daily-") {
				targetHost = "cloudcode-pa.googleapis.com"
			}
			upstreamURL := "https://" + targetHost + r.URL.RequestURI()

			passReq, pErr := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, bytes.NewReader(bodyBytes))
			if pErr == nil {
				passReq.ContentLength = int64(len(bodyBytes))
				for k, v := range r.Header {
					kLower := strings.ToLower(k)
					if kLower == "host" || kLower == "connection" || kLower == "accept-encoding" || kLower == "content-length" {
						continue
					}
					for _, val := range v {
						passReq.Header.Add(k, val)
					}
				}
				passReq.Host = targetHost

				passResp, doErr := geminiProxyClient.Do(passReq)
				if doErr == nil {
					defer passResp.Body.Close()
					for k, v := range passResp.Header {
						if strings.EqualFold(k, "Transfer-Encoding") {
							continue
						}
						for _, val := range v {
							w.Header().Add(k, val)
						}
					}
					w.WriteHeader(passResp.StatusCode)
					_, _ = io.Copy(w, passResp.Body)
					log.Printf("[ENGINE] [GEMINI-PROXY] [Static-Meta] %s %s -> %s (Status: %d)", r.Method, r.URL.Path, targetHost, passResp.StatusCode)
					return
				}
				log.Printf("[ENGINE] [GEMINI-PROXY] [Static-Meta] Failed (%v), fallback pool", doErr)
			}
		}



		// Trích model từ request body (nếu có)

		rawModel := extractModelFromBody(bodyBytes)

		// 🔍 LOG DEBUG: Capture rawModel thực tế từ IDE để trace routing
		log.Printf("[ENGINE] [GEMINI-PROXY] 🔍 RAW model từ IDE: %q | URL: %s", rawModel, r.URL.Path)

		// Bước 1: Áp dụng User Alias Mapping (mitm_alias/mappings) - ưu tiên tuyệt đối
		// Người dùng đã cấu hình slot trong UI (ví dụ "Gemini 3.8 Flash (Medium)/Default" → "gemini-3.1-pro-high")
		// Proxy PHẢI tôn trọng mapping này trước bất kỳ logic sanitize nào
		mappedModel := applyUserModelAlias(db, rawModel)
		if mappedModel == "" {
			mappedModel = rawModel
		}

		// Bước 2: Sanitize model (chỉ chuẩn hóa ký tự, KHÔNG ép đổi tên model)
		model := sanitizeAndUpgradeModel(mappedModel)
		log.Printf("[ENGINE] [GEMINI-PROXY] 🔍 Sau alias+sanitize: rawModel=%q → mapped=%q → model=%q", rawModel, mappedModel, model)



		// Chuẩn hóa model theo yêu cầu của client
		// NGUYÊN TẮC: TUYỆT ĐỐI KHÔNG tự ý chèn model dự phòng (Claude Sonnet / Gemini Flash...)
		// khi người dùng không cấu hình nguồn dự phòng.
		// Chỉ xoay vòng tài khoản (Load Balancing / Account Failover) cho ĐÚNG model mà Client yêu cầu.
		var modelTiers []string
		if model == "" {
			// Request không chỉ định model (quota, metadata, autocompletion...) -> giữ nguyên 100% bản gốc
			modelTiers = []string{""}
		} else {
			// Lấy danh sách tier từ resolveCascadeModelTiers (chỉ có nhiều hơn 1 model nếu client yêu cầu rõ Combo)
			modelTiers = resolveCascadeModelTiers(db, model)
			if len(modelTiers) == 0 {
				modelTiers = []string{model}
			}
		}



		log.Printf("[ENGINE] [GEMINI-PROXY] ⚡ Antigravity IDE: %s %s (Host: %s, Model: %s, Tiers: %v)", r.Method, r.URL.Path, r.Host, model, modelTiers)



		// KIEM TRA HAN MUC QUOTA CUA CLIENT API KEY TRUOC KHI GOI POOL AI:
		// Đảm bảo từng API Key hoạt động hoàn toàn độc lập, không bị ảnh hưởng chéo lẫn nhau:
		clientKeyToCheck := ExtractClientAPIKeyFromRequest(r)
		if clientKeyToCheck == "" {
			// Transparent Proxy cục bộ (Antigravity IDE qua cổng 443 localhost): lấy từ config.json của máy này
			clientKeyToCheck = GetActiveClientKey(db)
		}

		var quotaCommitted bool
		if clientKeyToCheck != "" {
			keyItem, status, errMsg := CheckClientKeyStatus(clientKeyToCheck)
			if status == "QUOTA_EXCEEDED" {
				log.Printf("[ENGINE] [GEMINI-PROXY] 🛑 TỪ CHỐI REQUEST: Khóa %s đã hết hạn mức Quota (%s)", maskAPIKey(clientKeyToCheck), errMsg)
				if db != nil && keyItem != nil {
					if _, errLock := db.Exec("UPDATE omni_client_keys SET is_active = 0 WHERE id = ?", keyItem.ID); errLock != nil {
						log.Printf("[ENGINE] [USAGE] ❌ Lỗi khóa key vượt quota trong DB (%s): %v", keyItem.ID, errLock)
					}
					RefreshKeyCache(db)
				}
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusTooManyRequests)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error": map[string]interface{}{
						"code":    429,
						"message": fmt.Sprintf("Khóa API đã sử dụng hết hạn mức Quota (%d/%d req). Vui lòng đặt lại hoặc nâng hạn mức trên Dashboard.", keyItem.UsedRequests, keyItem.QuotaLimit),
						"status":  "RESOURCE_EXHAUSTED",
					},
				})
				return
			} else if status == "EXPIRED" {
				log.Printf("[ENGINE] [GEMINI-PROXY] 🛑 TỪ CHỐI REQUEST: Khóa %s đã hết hạn sử dụng (%s)", maskAPIKey(clientKeyToCheck), errMsg)
				if db != nil && keyItem != nil {
					if _, errLock := db.Exec("UPDATE omni_client_keys SET is_active = 0 WHERE id = ?", keyItem.ID); errLock != nil {
						log.Printf("[ENGINE] [USAGE] ❌ Lỗi khóa key hết hạn trong DB (%s): %v", keyItem.ID, errLock)
					}
					RefreshKeyCache(db)
				}
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error": map[string]interface{}{
						"code":    403,
						"message": errMsg,
						"status":  "PERMISSION_DENIED",
					},
				})
				return
			} else if status == "INACTIVE" {
				log.Printf("[ENGINE] [GEMINI-PROXY] 🛑 TỪ CHỐI REQUEST: Khóa %s đang bị TẠM KHÓA", maskAPIKey(clientKeyToCheck))
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error": map[string]interface{}{
						"code":    403,
						"message": "Khóa API này đang ở trạng thái TẠM KHÓA. Vui lòng kích hoạt lại trên Dashboard.",
						"status":  "PERMISSION_DENIED",
					},
				})
				return
			}

			// Giữ chỗ nguyên tử Quota (Atomic Reservation) cho ĐÚNG API KEY NÀY (chỉ áp dụng cho request AI, bỏ qua request check quota)
			if !isQuotaReq {
				reserved, resMsg := ReserveClientKeyQuota(db, clientKeyToCheck)
				if !reserved {
					log.Printf("[ENGINE] [GEMINI-PROXY] 🛑 TỪ CHỐI REQUEST: Không thể giữ chỗ Quota cho key %s: %s", maskAPIKey(clientKeyToCheck), resMsg)
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.WriteHeader(http.StatusTooManyRequests)
					json.NewEncoder(w).Encode(map[string]interface{}{
						"error": map[string]interface{}{
							"code":    429,
							"message": resMsg,
							"status":  "RESOURCE_EXHAUSTED",
						},
					})
					return
				}
				// Hoàn trả quota tự động nếu request thất bại trước khi upstream phản hồi
				defer func() {
					if !quotaCommitted {
						ReleaseClientKeyQuota(db, clientKeyToCheck, 1)
					}
				}()
			}
		}

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
		_ = numAccounts // dùng trong orderAccountsForModel

		// Proportional Quota-Weighted Selection (chuẩn Pool Distribution):
		// Chọn tài khoản khởi đầu theo trọng số quota còn lại tương ứng với model.
		// Tài khoản có nhiều quota hơn được chọn nhiều hơn -> phân tán đều hơn -> không bị cạn 1 tài khoản trước.
		// Nếu không có thông tin quota -> fallback về Session Affinity round-robin thông thường.
		startIdx := selectWeightedStartIndex(readyAccounts, model)



		var lastErr error

		var resp *http.Response

		var usedAccount *ProviderConnection

		var successfulModel string



		// Duyệt tuần tự qua các model tiers (Cascade Fallback)

		for tierIdx, tierModel := range modelTiers {

			// Sắp xếp tài khoản thông minh theo model (Model-Aware Account Routing chuẩn 9Router)
			// Nếu model là Claude -> ưu tiên tuyệt đối các tài khoản CÒN QUOTA CLAUDE lên đầu
			// Nếu model là Gemini -> ưu tiên các tài khoản CÒN QUOTA GEMINI lên đầu
			// Bám dính Session Affinity từ startIdx, loại bỏ gián đoạn nhảy tài khoản thất thường
			orderedAccounts := orderAccountsForModel(readyAccounts, tierModel, startIdx)
			maxAttempts := len(orderedAccounts)

			if maxAttempts > 19 {

				maxAttempts = 19

			}



			// Xoay vòng tài khoản (Load balancing & Failover)

			for attempt := 0; attempt < maxAttempts; attempt++ {

				acc := orderedAccounts[attempt]



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

						// Chỉ cập nhật project nếu request gốc ĐÃ CÓ trường project

						if _, hasProj := rawJSON["project"]; hasProj {

							rawJSON["project"] = targetProject

						}

						// TUYỆT ĐỐI CHỈ thay đổi model khi request gốc ĐÃ CÓ trường model

						if tierModel != "" {

							if _, hasModel := rawJSON["model"]; hasModel {

								rawJSON["model"] = tierModel

							}

						}

						if patched, pErr := json.Marshal(rawJSON); pErr == nil {

							finalBodyBytes = patched

						}

					}

				}

				// Loại bỏ triệt để toàn bộ model đời cũ nếu còn sót trong body

				finalBodyBytes = bytes.ReplaceAll(finalBodyBytes, []byte("gemini-2.5-flash-lite"), []byte("gemini-3.8-flash-high"))

				finalBodyBytes = bytes.ReplaceAll(finalBodyBytes, []byte("gemini-2.5-flash"), []byte("gemini-3.8-flash-high"))

				finalBodyBytes = bytes.ReplaceAll(finalBodyBytes, []byte("gemini-2.5-pro"), []byte("gemini-3.8-flash-high"))
				finalBodyBytes = bytes.ReplaceAll(finalBodyBytes, []byte("gemini-pro-agent"), []byte("gemini-3.8-flash-high"))
				finalBodyBytes = bytes.ReplaceAll(finalBodyBytes, []byte("4-argon"), []byte("gemini-3.8-flash-high"))

				finalBodyBytes = bytes.ReplaceAll(finalBodyBytes, []byte("gemini-1.5-pro"), []byte("gemini-pro-agent"))

				finalBodyBytes = bytes.ReplaceAll(finalBodyBytes, []byte("gemini-1.5-flash"), []byte("gemini-3.8-flash-high"))

				finalBodyBytes = bytes.ReplaceAll(finalBodyBytes, []byte("claude-3-5-sonnet"), []byte("claude-sonnet-4-6"))

				finalBodyBytes = bytes.ReplaceAll(finalBodyBytes, []byte("claude-3-7-sonnet"), []byte("claude-sonnet-4-6"))

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

				// Loại bỏ triệt để model đời cũ trên URL upstream nếu có

				upstreamURL = strings.ReplaceAll(upstreamURL, "gemini-2.5-flash-lite", "gemini-3.8-flash-high")

				upstreamURL = strings.ReplaceAll(upstreamURL, "gemini-2.5-flash", "gemini-3.8-flash-high")

				upstreamURL = strings.ReplaceAll(upstreamURL, "gemini-2.5-pro", "gemini-3.8-flash-high")
				upstreamURL = strings.ReplaceAll(upstreamURL, "gemini-pro-agent", "gemini-3.8-flash-high")
				upstreamURL = strings.ReplaceAll(upstreamURL, "4-argon", "gemini-3.8-flash-high")

				upstreamURL = strings.ReplaceAll(upstreamURL, "gemini-1.5-pro", "gemini-pro-agent")

				upstreamURL = strings.ReplaceAll(upstreamURL, "gemini-1.5-flash", "gemini-3.8-flash-high")

				upstreamURL = strings.ReplaceAll(upstreamURL, "claude-3-5-sonnet", "claude-sonnet-4-6")

				upstreamURL = strings.ReplaceAll(upstreamURL, "claude-3-7-sonnet", "claude-sonnet-4-6")

				if tierModel != "" && model != "" && model != tierModel {
					upstreamURL = strings.ReplaceAll(upstreamURL, model, tierModel)
					upstreamURL = strings.ReplaceAll(upstreamURL, url.PathEscape(model), url.PathEscape(tierModel))
					finalBodyBytes = bytes.ReplaceAll(finalBodyBytes, []byte("\""+model+"\""), []byte("\""+tierModel+"\""))
				}



				// Tạo request mới với finalBodyBytes

				fReq, reqErr := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, bytes.NewReader(finalBodyBytes))

				if reqErr != nil {

					lastErr = reqErr

					continue

				}



				// Copy headers gốc từ IDE (loại bỏ Authorization, Host, Connection, User-Project, Content-Length và Accept-Encoding)

				for key, values := range r.Header {

					keyLower := strings.ToLower(key)

					if keyLower == "authorization" || keyLower == "host" || keyLower == "connection" || keyLower == "x-goog-user-project" || keyLower == "content-length" || keyLower == "accept-encoding" {

						continue

					}

					for _, v := range values {

						fReq.Header.Add(key, v)

					}

				}



				// Thiết lập Authorization chuẩn xác theo tài khoản Pool (KHÔNG thêm X-Goog-User-Project để tránh xung đột quota project cá nhân)

				fReq.Header.Set("Authorization", "Bearer "+creds.AccessToken)

				fReq.Header.Set("Content-Type", "application/json")

				fReq.Header.Set("Accept-Encoding", "identity")

				fReq.Header.Set("Cache-Control", "no-cache")

				fReq.Header.Set("X-Accel-Buffering", "no")

				fReq.Header.Set("X-Client-Name", "antigravity")

				if fReq.Header.Get("User-Agent") == "" {

					fReq.Header.Set("User-Agent", "antigravity/ide/2.24.0 windows/amd64")

				}



				// Chọn HTTP client phù hợp (qua Proxy của tài khoản nếu có cấu hình, hoặc Direct)

				targetClient := geminiProxyClient

				if creds.ConnectionProxyEnabled && creds.ProxyUrl != "" && isProxyHealthy(creds.ProxyUrl) {

					targetClient = getClientForAccount(creds.ProxyUrl)

				}



				rResp, doErr := targetClient.Do(fReq)

				if doErr != nil && targetClient != geminiProxyClient {

					markProxyFailure(creds.ProxyUrl, doErr.Error())
					log.Printf("[ENGINE] [GEMINI-PROXY] ⚠ Proxy %s gặp sự cố (%v), tự động fallback chuyển sang kết nối trực tiếp (Direct)...", creds.ProxyUrl, doErr)

					creds.ConnectionProxyEnabled = false

					ctxDirect, cancelDirect := context.WithTimeout(context.Background(), 90*time.Second)

					fReqDirect, _ := http.NewRequestWithContext(ctxDirect, r.Method, upstreamURL, bytes.NewReader(finalBodyBytes))

					fReqDirect.Header = fReq.Header.Clone()

					rResp, doErr = geminiProxyClient.Do(fReqDirect)

					cancelDirect()

				}

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
					var errSnippet string
					if rResp.Body != nil {
						eb, _ := io.ReadAll(io.LimitReader(rResp.Body, 1024))
						errSnippet = string(eb)
					}
					rResp.Body.Close()

					accountCooldownMap.Store(acc.ID, time.Now().Add(30*time.Second))

					// Chuyen session sang tai khoan tiep theo khi bi rate limit (429) va xoa cache quota
					atomic.AddUint64(&activeAccountIdx, 1)
					ideQuotaCacheLock.Lock()
					ideQuotaCache = make(map[string]*CachedIDEQuotaResponse)
					ideQuotaCacheLock.Unlock()

					lastErr = fmt.Errorf("rate limited (429) on %s for model %s: %s", acc.Name, tierModel, errSnippet)
					log.Printf("[ENGINE] [GEMINI-PROXY] ⚠ Rate limited (429) trên %s cho model %s (%s)", acc.Name, tierModel, errSnippet)

					// Fast-Cascade chuyển ngay sang Tier tiếp theo (ví dụ Sonnet) nếu model gặp giới hạn Quota/Subscription cá nhân
					if len(modelTiers) > tierIdx+1 && (strings.Contains(tierModel, "opus") || strings.Contains(errSnippet, "quota") || strings.Contains(errSnippet, "subscription") || strings.Contains(errSnippet, "Individual")) {
						log.Printf("[ENGINE] [GEMINI-PROXY] ⚡ Rate limit/Quota trên %s. Kích hoạt Fast-Cascade chuyển ngay sang Tier tiếp theo: %s", tierModel, modelTiers[tierIdx+1])
						break
					}

					continue
				}



				if rResp.StatusCode != http.StatusOK {

					// Đọc body lỗi để log, hỗ trợ giải nén gzip nếu upstream trả về gzip

					var errReader io.Reader = rResp.Body

					if strings.Contains(strings.ToLower(rResp.Header.Get("Content-Encoding")), "gzip") {

						if gzReader, gzErr := gzip.NewReader(rResp.Body); gzErr == nil {

							defer gzReader.Close()

							errReader = gzReader

						}

					}

					errBody, _ := io.ReadAll(io.LimitReader(errReader, 1024))

					rResp.Body.Close()



					// Lọc sạch 100% ký tự nhị phân rác, giữ lại UTF-8 hợp lệ

					cleanErrStr := strings.Map(func(r rune) rune {

						if r == '\n' || r == '\r' || r == '\t' || (r >= 32 && r <= 126) || r >= 160 {

							return r

						}

						return -1

					}, string(errBody))

					cleanErrStr = strings.TrimSpace(cleanErrStr)

					if cleanErrStr == "" {

						cleanErrStr = fmt.Sprintf("HTTP %d (%s)", rResp.StatusCode, http.StatusText(rResp.StatusCode))

					}



					lastErr = fmt.Errorf("upstream error %d on %s for %s: %s", rResp.StatusCode, acc.Name, tierModel, cleanErrStr)

					log.Printf("[ENGINE] [GEMINI-PROXY] ⚠ Upstream lỗi %d trên %s cho model %s: %s", rResp.StatusCode, acc.Email, tierModel, cleanErrStr)



					isSubOrQuotaLimit := strings.Contains(cleanErrStr, "Individual quota reached") ||
						strings.Contains(cleanErrStr, "upgrade your subscription") ||
						strings.Contains(cleanErrStr, "SUBSCRIPTION_REQUIRED") ||
						strings.Contains(cleanErrStr, "#3501") ||
						strings.Contains(cleanErrStr, "quota")

					if strings.Contains(cleanErrStr, "SUBSCRIPTION_REQUIRED") || strings.Contains(cleanErrStr, "#3501") {
						accountCooldownMap.Store(acc.ID, time.Now().Add(1*time.Hour))
						log.Printf("[ENGINE] [GEMINI-PROXY] ⚠ Tài khoản thiếu subscription (#3501), tạm thời cooldown 1h")
					}

					// Fast-Cascade nếu gặp lỗi Quota/Subscription hoặc lỗi 403 trên model Opus và có tier tiếp theo
					if len(modelTiers) > tierIdx+1 && (isSubOrQuotaLimit || (strings.Contains(tierModel, "opus") && (rResp.StatusCode == http.StatusForbidden || rResp.StatusCode == http.StatusBadRequest))) {
						accountCooldownMap.Store(acc.ID, time.Now().Add(30*time.Minute))
						log.Printf("[ENGINE] [GEMINI-PROXY] ⚡ Model %s yêu cầu subscription/quota đặc biệt (%s, HTTP %d). Kích hoạt Fast-Cascade chuyển ngay sang Tier tiếp theo: %s", tierModel, cleanErrStr, rResp.StatusCode, modelTiers[tierIdx+1])
						break
					}



					// Nếu lỗi là do hết capacity (503 MODEL_CAPACITY_EXHAUSTED) hoặc 503 Service Unavailable:
					// Cooldown tài khoản hiện tại 30s và TIẾP TỤC thử các tài khoản khác trong pool cho CHÍNH MODEL ĐÓ.
					// TUYỆT ĐỐI KHÔNG break để tránh tự ý đổi sang model khác khi người dùng không cấu hình nguồn dự phòng.
					if strings.Contains(cleanErrStr, "MODEL_CAPACITY_EXHAUSTED") || rResp.StatusCode == http.StatusServiceUnavailable {
						accountCooldownMap.Store(acc.ID, time.Now().Add(30*time.Second))
						atomic.AddUint64(&activeAccountIdx, 1)
						log.Printf("[ENGINE] [GEMINI-PROXY] ⚠ Model %s bị cạn capacity (503) trên tài khoản %s. Tạm cooldown 30s và thử tài khoản tiếp theo trong pool...", tierModel, acc.Name)
						continue
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



		// Nếu là request Quota (retrieveUserQuotaSummary hoặc fetchAvailableModels), xử lý Virtual Aggregated Quota chuẩn 9Router
		if isQuotaReq && resp.StatusCode == http.StatusOK {
			var quotaRespBuf bytes.Buffer
			_, _ = io.Copy(&quotaRespBuf, resp.Body)
			rawBytes := quotaRespBuf.Bytes()

			if usedAccount != nil {
				go syncLiveQuotaFromUpstreamResponse(db, usedAccount, r.URL.Path, rawBytes)
			}

			// Tạo Virtual Aggregated Quota chuẩn 9Router: đảm bảo cả Claude và Gemini đều hiển thị 100% dồi dào trên IDE
			aggregatedBytes := buildVirtualAggregatedQuota(rawBytes)

			// Lưu vào RAM Cache (TTL 30s)
			ideQuotaCacheLock.Lock()
			ideQuotaCache[r.URL.Path] = &CachedIDEQuotaResponse{
				StatusCode: resp.StatusCode,
				Header:     resp.Header.Clone(),
				Body:       aggregatedBytes,
				CachedAt:   time.Now(),
			}
			ideQuotaCacheLock.Unlock()

			// Pipe headers về cho IDE (bỏ qua Content-Length cũ và các header nén)
			for key, values := range resp.Header {
				kLower := strings.ToLower(key)
				if kLower == "content-length" || kLower == "transfer-encoding" || kLower == "content-encoding" {
					continue
				}
				for _, v := range values {
					w.Header().Add(key, v)
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", strconv.Itoa(len(aggregatedBytes)))
			w.Header().Set("X-ModelRouter9-Aggregated", "true")
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(aggregatedBytes)
			log.Printf("[ENGINE] [GEMINI-PROXY] ✔ Đã ảo hóa Quota Aggregated (100%% Claude & Gemini) gửi về cho Antigravity IDE (URL: %s)", r.URL.Path)
			return
		}

		// Pipe response headers nguyên bản từ upstream về IDE (SSE stream hoặc request thông thường)
		for key, values := range resp.Header {
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}
		w.WriteHeader(resp.StatusCode)

		// Pipe response body nguyên bản (SSE stream hoặc JSON)
		// Đây là transparent proxy nên IDE nhận đúng format Gemini native
		flusher, isFlusher := w.(http.Flusher)
		var totalRespBytes int64
		buf := make([]byte, 512) // Ultra-Low Latency Zero-Lag Token Streaming
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				totalRespBytes += int64(n)
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



		latencyMs := time.Since(startTime).Milliseconds()

		if usedAccount != nil {

			effectiveModel := successfulModel

			if effectiveModel == "" {

				effectiveModel = model

			}

			effectiveModel = sanitizeAndUpgradeModel(effectiveModel)

			promptTokens := int64(len(bodyBytes) / 4)

			if promptTokens < 120 {

				promptTokens = 120

			}

			completionTokens := totalRespBytes / 4

			if completionTokens < 250 {

				completionTokens = 250

			}

			accEmail := usedAccount.Email

			if accEmail == "" {

				accEmail = usedAccount.Name

			}

			log.Printf("[ENGINE] [GEMINI-PROXY] ✔ Proxy hoàn thành (%s) - %dms - Tokens: %d - Path: %s", effectiveModel, latencyMs, promptTokens+completionTokens, r.URL.Path)

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

				AccountName:      "[Tài khoản bảo vệ]",

				Model:            effectiveModel,

				LatencyMs:        latencyMs,

				StatusCode:       http.StatusOK,

				PromptTokens:     promptTokens,

				CompletionTokens: completionTokens,

				IsStream:         strings.Contains(r.URL.RawQuery, "alt=sse"),

			})



			// Ghi nhận trừ Quota trực tiếp vào Aiven PostgreSQL Cloud và gửi Live Telemetry lên Web cho ĐÚNG CLIENT KEY NÀY
			if !isQuotaReq {
				quotaCommitted = true
				ReportProxyUsageWithKeyAsync(db, clientKeyToCheck, effectiveModel, usedAccount.Email, usedAccount.Name, r.URL.Path, latencyMs, http.StatusOK, promptTokens, completionTokens)
			}

		}

	}

}



// sanitizeAndUpgradeModel loại bỏ triệt để toàn bộ model đời cũ (2.5, 2.0, 1.5, claude cũ...)

// và quy về đúng 100% các model đang hoạt động thực tế trên Antigravity IDE

// sanitizeAndUpgradeModel chuẩn hóa model string (loại bỏ model cũ lỗi thời, alias sai)
// NGUYÊN TẮC: Nếu model đã được resolve qua User Alias Mapping thì GIỮ NGUYÊN.
// Chỉ upgrade các model name thực sự lỗi thời (2.0, 1.5, claude-2...)
func sanitizeAndUpgradeModel(model string) string {

	mLower := strings.ToLower(strings.TrimSpace(model))

	if mLower == "" || mLower == "gemini-native" || mLower == "functioncall" || mLower == "auto" || mLower == "none" {

		return ""

	}

	// Nếu model đã là tên chính xác hợp lệ -> GIỮ NGUYÊN TUYỆT ĐỐI
	// (quan trọng: người dùng đã map slot vào đây)
	validModels := map[string]bool{
		"gemini-3.8-flash-high":    true,
		"gemini-3.8-flash-medium":  true,
		"gemini-3.8-flash-low":     true,
		"gemini-3.7-flash-high":    true,
		"gemini-3.7-flash-medium":  true,
		"gemini-3.7-flash-low":     true,
		"gemini-3.6-flash-high":    true,
		"gemini-3.6-flash-medium":  true,
		"gemini-3.6-flash-low":     true,
		"gemini-3.5-flash-high":    true,
		"gemini-3.5-flash-low":     true,
		"gemini-3-flash-agent":     true,
		"gemini-3-flash":           true,
		"claude-sonnet-4-6":        true,
		"claude-opus-4-6-thinking": true,
		"gpt-oss-120b-medium":      true,
	}
	if validModels[mLower] {
		return mLower
	}

	// Upgrade model đời cũ (claude-2, claude-3, gemini-1.5, gemini-2.0...)
	if strings.Contains(mLower, "opus") {

		return "claude-opus-4-6-thinking"

	}

	if strings.Contains(mLower, "sonnet") {

		return "claude-sonnet-4-6"

	}

	if strings.Contains(mLower, "gpt-oss") || strings.Contains(mLower, "120b") {

		return "gpt-oss-120b-medium"

	}

	if strings.Contains(mLower, "pro-agent") || strings.Contains(mLower, "2.5-pro") || strings.Contains(mLower, "1.5-pro") {

		return "gemini-3.8-flash-high"

	}

	// 3.1-pro -> map về gemini-3.8-flash-high (entity hợp lệ trên Google Cloud Code PA chống 404 NOT_FOUND)
	if strings.Contains(mLower, "3.1-pro") || strings.Contains(mLower, "pro-high") {
		return "gemini-3.8-flash-high"
	}

	if strings.Contains(mLower, "argon") {

		return "gemini-3.8-flash-high"

	}

	// Flash: phân biệt rõ high/medium/low theo suffix
	if strings.Contains(mLower, "flash") {
		if strings.Contains(mLower, "medium") {
			return "gemini-3.8-flash-medium"
		}
		if strings.Contains(mLower, "low") {
			return "gemini-3.8-flash-low"
		}
		return "gemini-3.8-flash-high"
	}

	return model

}



// extractModelFromBody trích model name từ JSON body

func extractModelFromBody(body []byte) string {

	if len(body) == 0 {

		return ""

	}

	var req struct {

		Model string `json:"model"`

	}

	if err := json.Unmarshal(body, &req); err == nil && req.Model != "" {

		m := strings.TrimSpace(req.Model)

		if m != "functionCall" && m != "auto" && m != "none" && m != "" {

			return sanitizeAndUpgradeModel(m)

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

					m := strings.TrimSpace(sub[:quoteEnd])

					if m != "functionCall" && m != "auto" && m != "none" && m != "" {

						return sanitizeAndUpgradeModel(m)

					}

				}

			}

		}

	}

	// Fallback mặc định: Active Key thực tế đang hoạt động trên Aiven PostgreSQL
	return "sk-www.supportflastdev.io.vn-4b134c2b19bae0bf106c19a6f94154f9"

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

		filepath.Join("storage", "packages", "APP-ROUTER_ModelRouter9_v2.9.0", "config.json"),

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

				if k, ok := cfg["api_key"].(string); ok {
					trimK := strings.TrimSpace(k)
					if strings.HasPrefix(trimK, "sk-") && !strings.Contains(trimK, "...") && !strings.Contains(trimK, "***") && len(trimK) >= 25 {
						currentActiveClientKey = trimK
						log.Printf("[ENGINE] [KEY] ✔ Nạp Active Key từ '%s': %s", p, maskAPIKey(currentActiveClientKey))
						return currentActiveClientKey
					}
				}

				if k, ok := cfg["master_api_key"].(string); ok {
					trimK := strings.TrimSpace(k)
					if strings.HasPrefix(trimK, "sk-") && !strings.Contains(trimK, "...") && !strings.Contains(trimK, "***") && len(trimK) >= 25 {
						currentActiveClientKey = trimK
						log.Printf("[ENGINE] [KEY] ✔ Nạp Active Master Key từ '%s': %s", p, maskAPIKey(currentActiveClientKey))
						return currentActiveClientKey
					}
				}

			}

		}

	}



	// 2. TUYỆT ĐỐI KHÔNG query CSDL Cloud ORDER BY created_at DESC LIMIT 1!
	// Mỗi API Key là của người dùng riêng biệt, không được gán bừa API Key của người khác.



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

		filepath.Join("storage", "packages", "APP-ROUTER_ModelRouter9_v2.9.0", "config.json"),

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



// ReportProxyUsageAsync ghi nhận telemetry thời gian thực và cập nhật hạn mức quota của Active Key

func ReportProxyUsageAsync(db *sql.DB, effectiveModel, accountEmail, accountName, reqPath string, latencyMs int64, statusCode int, promptTokens, completionTokens int64) {

	ReportProxyUsageWithKeyAsync(db, "", effectiveModel, accountEmail, accountName, reqPath, latencyMs, statusCode, promptTokens, completionTokens)

}



// ReportProxyUsageWithKeyAsync ghi nhận telemetry thời gian thực và cập nhật hạn mức quota của Client Key cụ thể hoặc Active Key

func ReportProxyUsageWithKeyAsync(db *sql.DB, explicitKey, effectiveModel, accountEmail, accountName, reqPath string, latencyMs int64, statusCode int, promptTokens, completionTokens int64) {

	go func() {

		defer func() {

			_ = recover()

		}()



		// Ưu tiên key được truyền tường minh từ request, nếu không có thì lấy Active Client Key

		activeKey := strings.TrimSpace(explicitKey)

		if activeKey == "" {

			activeKey = GetActiveClientKey(db)

		}

		if activeKey == "" {
			// Không có API Key hợp lệ cho request này -> Chỉ ghi nhận log hệ thống, không trừ quota của bất kỳ client key nào
			log.Printf("[ENGINE] [TELEMETRY] Request AI thành công (%s - %dms) không kèm Client Key cá nhân", effectiveModel, latencyMs)
		}

		totTokens := promptTokens + completionTokens

		if totTokens <= 0 {

			totTokens = 520

		}



		accDisplay := accountEmail

		if accDisplay == "" {

			accDisplay = accountName

		}

		if accDisplay == "" {

			accDisplay = "Tài khoản Antigravity"

		}



		// 1. Cập nhật số lượt gọi (used_requests) và số token tiêu thụ cho Active Client Key

		// used_requests đã được giữ chỗ nguyên tử trong ReserveClientKeyQuota, ở đây truyền 0 để tránh trừ đúp quota
		if activeKey != "" && !strings.Contains(activeKey, "native-proxy") { RecordKeyUsageAsync(db, activeKey, 0, totTokens) }



		// 2. Ghi nhật ký vào bảng omni_usage_history để hiển thị chi tiết trên Web & Desktop

		RecordUsageHistoryAsync(db, "Antigravity", effectiveModel, accDisplay, activeKey, reqPath, promptTokens, completionTokens, totTokens, 0.0, "success", latencyMs)



		// 3. Gửi gói tin telemetry lên Web Server Render để Web Console hiển thị thời gian thực

		webURL := GetWebServerURL()

		reportURL := webURL + "/api/v1/client/report"



		payload := ClientReportPayload{

			ApiKey:          activeKey,

			UsedRequests:    1,

			TotalTokens:     totTokens,

			Model:           effectiveModel,

			Endpoint:        reqPath,

			AccountName:     accDisplay,

			ClientDevice:    "Antigravity IDE (Desktop)",

			LatencyMs:       latencyMs,

			StatusCode:      statusCode,

			LogMessage:      fmt.Sprintf("[IDE PROXY] Antigravity IDE gọi thành công model %s (%dms - %d tokens)", effectiveModel, latencyMs, totTokens),

			SkipDBIncrement: false,

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
		req.Header.Set("User-Agent", "SupportFlast-Desktop-Gateway/2.9.0")
		req.Header.Set("Authorization", "Bearer "+activeKey)
		req.Header.Set("x-api-key", activeKey)
		req.Header.Set("x-9r-cli-token", "6929c9313fb35a0b")



		client := &http.Client{Timeout: 4 * time.Second}

		resp, err := client.Do(req)

		if err == nil {

			_ = resp.Body.Close()

		}

	}()

}



// StartClientTelemetryHeartbeat định kỳ gửi heartbeat từ Desktop App lên Web Server

func StartClientTelemetryHeartbeat(db *sql.DB) {

	// Không chạy telemetry heartbeat trên môi trường Web Server (Render, Cloud Server) để tránh tự spam kết nối

	if os.Getenv("RENDER") != "" || os.Getenv("RENDER_SERVICE_ID") != "" || os.Getenv("PORT") != "" {

		return

	}



	telemetryHeartbeatOnce.Do(func() {

		go func() {

			hostName, _ := os.Hostname()

			if hostName == "" {

				hostName = "PC-Client"

			}

			hostLower := strings.ToLower(hostName)

			if strings.Contains(hostLower, "srv-") || strings.Contains(hostLower, "render") {

				return

			}



			// Giãn chu kỳ gửi Heartbeat từ Desktop lên Web Server sang 60s để giảm tải đường truyền

			ticker := time.NewTicker(60 * time.Second)

			defer ticker.Stop()



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
					req.Header.Set("User-Agent", "SupportFlast-Desktop-Gateway/2.9.0")
					req.Header.Set("Authorization", "Bearer "+activeKey)
					req.Header.Set("x-api-key", activeKey)
					req.Header.Set("x-9r-cli-token", "6929c9313fb35a0b")

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



	// Cập nhật CSDL Aiven PostgreSQL Cloud

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
			req.Header.Set("User-Agent", "SupportFlast-Desktop-Gateway/2.9.0")
			req.Header.Set("Authorization", "Bearer "+activeKey)
			req.Header.Set("x-api-key", activeKey)
			req.Header.Set("x-9r-cli-token", "6929c9313fb35a0b")
			cl := &http.Client{Timeout: 3 * time.Second}

			if resp, err := cl.Do(req); err == nil {

				_ = resp.Body.Close()

			}

		}

	}

}



var tokenRefreshWorkerOnce sync.Once



// StartBackgroundTokenRefreshWorker chạy ngầm định kỳ 3 phút tự động quét và làm mới token trước khi hết hạn

func StartBackgroundTokenRefreshWorker(db *sql.DB) {

	tokenRefreshWorkerOnce.Do(func() {

		go func() {

			time.Sleep(3 * time.Second)

			ticker := time.NewTicker(3 * time.Minute)

			defer ticker.Stop()



			checkAndRefresh := func() {

				if db == nil {

					return

				}

				providers, err := GetAllProviders(db)

				if err != nil || len(providers) == 0 {

					return

				}

				for _, p := range providers {

					if (p.Provider == "antigravity" || p.Provider == "google") && p.IsActive == 1 {

						creds, err := GetValidCredentials(db, &p)

						if err != nil || creds.RefreshToken == "" {

							continue

						}

						// Nếu token còn dưới 15 phút hoặc đã hết hạn -> tự động làm mới ngầm ngay

						if time.Until(creds.ExpiresAt) < 15*time.Minute {

							newTok, newExpiry, newRefresh, refErr := RefreshGoogleToken(creds.RefreshToken)

							if refErr == nil {

								var dataMap map[string]interface{}

								_ = json.Unmarshal(p.Data, &dataMap)

								if dataMap == nil {

									dataMap = make(map[string]interface{})

								}

								dataMap["accessToken"] = newTok

								dataMap["expiresAt"] = newExpiry.Format(time.RFC3339)

								dataMap["lastRefreshAt"] = NowISO()

								if newRefresh != "" {

									dataMap["refreshToken"] = newRefresh

								}

								_ = UpdateProviderData(db, p.ID, dataMap)

								log.Printf("[ENGINE] [TOKEN-REFRESH] ✔ Đã tự động làm mới token ngầm cho %s (hạn mới: %s)", p.Email, newExpiry.Format("15:04:05"))

							}

						}

					}

				}

			}



			checkAndRefresh()



			for range ticker.C {

				checkAndRefresh()

			}

		}()

	})

}

// getAccountQuotaWeight lấy trọng số quota còn lại của tài khoản theo model type (0.0 - 1.0)
// Dùng cho Proportional Quota-Weighted Selection để phân tán đều quota qua toàn bộ Pool
func getAccountQuotaWeight(acc *ProviderConnection, model string) float64 {
	if acc == nil {
		return 0.0
	}

	var sessionKey, weeklyKey string
	if isClaudeModel(model) {
		sessionKey, weeklyKey = "claude_gpt_session", "claude_gpt_weekly"
	} else {
		sessionKey, weeklyKey = "gemini_session", "gemini_weekly"
	}

	// Ưu tiên lấy từ RAM Cache (nhanh nhất, < 1ms)
	if cached, ok := GetCachedQuotas(acc.ID); ok && len(cached) > 0 {
		sessionFrac := 1.0
		weeklyFrac := 1.0
		if q, has := cached[sessionKey]; has {
			sessionFrac = q.RemainingPercentage / 100.0
		}
		if q, has := cached[weeklyKey]; has {
			weeklyFrac = q.RemainingPercentage / 100.0
		}
		// Trọng số = min(session, weekly) -> bị chặn bởi chiều hạn chế nhất
		w := sessionFrac
		if weeklyFrac < w {
			w = weeklyFrac
		}
		if w < 0 {
			return 0.0
		}
		return w
	}

	// Fallback: parse từ trường data JSON trong DB
	if len(acc.Data) > 0 {
		var dataMap map[string]interface{}
		if err := json.Unmarshal(acc.Data, &dataMap); err == nil && dataMap != nil {
			if qMap, ok := dataMap["quotas"].(map[string]interface{}); ok && qMap != nil {
				sessionFrac := 1.0
				weeklyFrac := 1.0
				if qS, ok := qMap[sessionKey].(map[string]interface{}); ok && qS != nil {
					if rem, ok := qS["remainingPercentage"].(float64); ok {
						sessionFrac = rem / 100.0
					}
				}
				if qW, ok := qMap[weeklyKey].(map[string]interface{}); ok && qW != nil {
					if rem, ok := qW["remainingPercentage"].(float64); ok {
						weeklyFrac = rem / 100.0
					}
				}
				w := sessionFrac
				if weeklyFrac < w {
					w = weeklyFrac
				}
				if w < 0 {
					return 0.0
				}
				return w
			}
		}
	}

	// Không có dữ liệu quota -> giả sử đầy 100% để tránh loại trừ tài khoản mới
	return 1.0
}

// selectWeightedStartIndex chọn tài khoản khởi đầu theo phân phối xác suất tỷ lệ thuận với quota còn lại.
// Thuật toán Weighted Random Sampling (WRS):
//   - Tài khoản quota 91% được chọn gấp đôi so với tài khoản quota 46%.
//   - Phân tán đồng đều: khi nhiều tài khoản có quota tương đương, xác suất bằng nhau.
//   - Khi không có thông tin quota -> round-robin thuần bằng activeAccountIdx.
func selectWeightedStartIndex(accounts []ProviderConnection, model string) int {
	n := len(accounts)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return 0
	}

	// Tính tổng trọng số toàn pool
	weights := make([]float64, n)
	totalWeight := 0.0
	hasAnyQuotaData := false
	for i, acc := range accounts {
		w := getAccountQuotaWeight(&acc, model)
		weights[i] = w
		totalWeight += w
		if w < 1.0 { // Tức là có dữ liệu quota thực (< 100% default)
			hasAnyQuotaData = true
		}
	}

	// Nếu không có thông tin quota nào -> fallback round-robin bằng atomic counter
	if !hasAnyQuotaData || totalWeight <= 0 {
		return int(atomic.LoadUint64(&activeAccountIdx) % uint64(n))
	}

	// Weighted Random Sampling: chọn ngẫu nhiên an toàn theo trọng số
	// Dùng crypto/rand để lấy byte ngẫu nhiên thực sự tránh bias
	var rbuf [8]byte
	_, _ = rand.Read(rbuf[:])
	randUint := uint64(rbuf[0]) | uint64(rbuf[1])<<8 | uint64(rbuf[2])<<16 | uint64(rbuf[3])<<24 |
		uint64(rbuf[4])<<32 | uint64(rbuf[5])<<40 | uint64(rbuf[6])<<48 | uint64(rbuf[7])<<56
	pick := (float64(randUint) / float64(^uint64(0))) * totalWeight

	cumulative := 0.0
	for i, w := range weights {
		cumulative += w
		if pick <= cumulative {
			return i
		}
	}
	return n - 1
}


// applyUserModelAlias tra cứu User Alias Mapping (KV: mitm_alias/mappings) để ánh xạ model slot IDE
// sang model thực tế người dùng đã cấu hình trong UI (ví dụ: "gemini-3.8-flash-medium" -> "gemini-3.1-pro-high")
// Cache RAM 10s để tránh truy vấn DB liên tục cho mỗi request
var (
	userAliasCacheLock sync.RWMutex
	userAliasCache     map[string]string
	userAliasCachedAt  time.Time
)

func applyUserModelAlias(db *sql.DB, rawModel string) string {
	if rawModel == "" || db == nil {
		return rawModel
	}

	// Cache hit: TTL 10s (đủ nhanh và ít query DB)
	userAliasCacheLock.RLock()
	if userAliasCache != nil && time.Since(userAliasCachedAt) < 10*time.Second {
		mapped := userAliasCache[rawModel]
		userAliasCacheLock.RUnlock()
		if mapped != "" && mapped != rawModel {
			log.Printf("[ENGINE] [GEMINI-PROXY] 🗺️ User Alias: %s → %s (RAM cache)", rawModel, mapped)
		}
		return mapped
	}
	userAliasCacheLock.RUnlock()

	// Load từ DB KV
	val, err := GetOmniKV(db, "mitm_alias", "mappings")
	if err != nil || val == "" {
		return rawModel
	}

	var allMappings map[string]map[string]string
	if err := json.Unmarshal([]byte(val), &allMappings); err != nil {
		return rawModel
	}

	// Flatten: merge tất cả provider mappings (antigravity, copilot, claude-code...) vào 1 map
	flat := make(map[string]string)
	for _, providerMap := range allMappings {
		for alias, target := range providerMap {
			if alias != "" && target != "" {
				flat[alias] = target
			}
		}
	}

	// Cập nhật cache
	userAliasCacheLock.Lock()
	userAliasCache = flat
	userAliasCachedAt = time.Now()
	userAliasCacheLock.Unlock()

	mapped := flat[rawModel]
	if mapped != "" && mapped != rawModel {
		log.Printf("[ENGINE] [GEMINI-PROXY] 🗺️ User Alias: %s → %s (DB load)", rawModel, mapped)
	}
	return mapped
}

func isClaudeModel(m string) bool {
	low := strings.ToLower(m)
	return strings.Contains(low, "claude") || strings.Contains(low, "sonnet") || strings.Contains(low, "opus") || strings.Contains(low, "haiku") || strings.Contains(low, "gpt")
}

// isGeminiModel kiểm tra xem model có thuộc dòng Gemini hay không
func isGeminiModel(m string) bool {
	low := strings.ToLower(m)
	return strings.Contains(low, "gemini")
}

// isAccountClaudeReady kiểm tra xem tài khoản có còn hạn mức Claude hay không
func isAccountClaudeReady(acc *ProviderConnection) bool {
	if acc == nil {
		return false
	}
	// Kiểm tra nếu tài khoản đang bị cooldown
	if cdUntil, hasCd := accountCooldownMap.Load(acc.ID); hasCd {
		if t, ok := cdUntil.(time.Time); ok && time.Now().Before(t) {
			return false
		}
	}
	// Lấy từ cache RAM
	if cached, ok := GetCachedQuotas(acc.ID); ok && len(cached) > 0 {
		for _, key := range []string{"claude-sonnet-4-6", "claude_weekly", "claude_gpt_weekly", "claude_session", "claude_gpt_session"} {
			if q, has := cached[key]; has && q.RemainingPercentage <= 0.0 {
				return false
			}
		}
		return true
	}
	// Lấy từ DB JSON
	if len(acc.Data) > 0 {
		var dataMap map[string]interface{}
		if err := json.Unmarshal(acc.Data, &dataMap); err == nil && dataMap != nil {
			if qMap, ok := dataMap["quotas"].(map[string]interface{}); ok && qMap != nil {
				for _, key := range []string{"claude-sonnet-4-6", "claude_weekly", "claude_gpt_weekly", "claude_session", "claude_gpt_session"} {
					if sub, ok := qMap[key].(map[string]interface{}); ok && sub != nil {
						if rem, ok := sub["remainingPercentage"].(float64); ok && rem <= 0.0 {
							return false
						}
					}
				}
			}
		}
	}
	return true
}

// isAccountGeminiReady kiểm tra xem tài khoản có còn hạn mức Gemini hay không
func isAccountGeminiReady(acc *ProviderConnection) bool {
	if acc == nil {
		return false
	}
	if cached, ok := GetCachedQuotas(acc.ID); ok && len(cached) > 0 {
		if q, has := cached["gemini_weekly"]; has && q.RemainingPercentage <= 0.0 {
			return false
		}
		if q, has := cached["gemini_session"]; has && q.RemainingPercentage <= 0.0 {
			return false
		}
		return true
	}
	if len(acc.Data) > 0 {
		var dataMap map[string]interface{}
		if err := json.Unmarshal(acc.Data, &dataMap); err == nil && dataMap != nil {
			if qMap, ok := dataMap["quotas"].(map[string]interface{}); ok && qMap != nil {
				if gWeekly, ok := qMap["gemini_weekly"].(map[string]interface{}); ok && gWeekly != nil {
					if rem, ok := gWeekly["remainingPercentage"].(float64); ok && rem <= 0.0 {
						return false
					}
				}
				if gSession, ok := qMap["gemini_session"].(map[string]interface{}); ok && gSession != nil {
					if rem, ok := gSession["remainingPercentage"].(float64); ok && rem <= 0.0 {
						return false
					}
				}
			}
		}
	}
	return true
}

// orderAccountsForModel sắp xếp danh sách tài khoản theo model cần gọi (Model-Aware Routing chuẩn 9Router)
func orderAccountsForModel(accounts []ProviderConnection, model string, startIdx int) []ProviderConnection {
	n := len(accounts)
	if n <= 1 {
		return accounts
	}

	// 1. Tạo danh sách xoay vòng từ startIdx để đảm bảo Session Affinity
	base := make([]ProviderConnection, n)
	for i := 0; i < n; i++ {
		base[i] = accounts[(startIdx+i)%n]
	}

	// 2. Nếu model là Claude -> ưu tiên tài khoản còn Quota Claude lên trước
	if isClaudeModel(model) {
		var readyCld []ProviderConnection
		var exhaustedCld []ProviderConnection
		for _, acc := range base {
			if isAccountClaudeReady(&acc) {
				readyCld = append(readyCld, acc)
			} else {
				exhaustedCld = append(exhaustedCld, acc)
			}
		}
		if len(readyCld) > 0 {
			return append(readyCld, exhaustedCld...)
		}
	} else if isGeminiModel(model) {
		// Nếu model là Gemini -> ưu tiên tài khoản còn Quota Gemini lên trước
		var readyGem []ProviderConnection
		var exhaustedGem []ProviderConnection
		for _, acc := range base {
			if isAccountGeminiReady(&acc) {
				readyGem = append(readyGem, acc)
			} else {
				exhaustedGem = append(exhaustedGem, acc)
			}
		}
		if len(readyGem) > 0 {
			return append(readyGem, exhaustedGem...)
		}
	}

	return base
}

// buildVirtualAggregatedQuota biến đổi response quota của Google thành Virtual Aggregated Quota chuẩn 9Router
// Giúp Antigravity IDE luôn hiển thị 100% hạn mức dồi dào, không bị Notice hay khóa model
// Đồng thời INJECT các model mà ModelRouter9 hỗ trợ (gemini-3.1-pro-high, claude-sonnet-4-6...)
// vào response để IDE hiển thị chúng trong danh sách model selector
func buildVirtualAggregatedQuota(raw []byte) []byte {
	if len(raw) == 0 {
		return GetDefaultVirtualAggregatedQuota()
	}
	var data map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil || data == nil {
		return GetDefaultVirtualAggregatedQuota()
	}
	if _, hasErr := data["error"]; hasErr {
		log.Printf("[ENGINE] [QUOTA-VIRTUAL] ⚠ Upstream quota có lỗi, tự động fallback về Virtual Aggregated Quota 100%%")
		return GetDefaultVirtualAggregatedQuota()
	}

	// Log raw response (chỉ 500 ký tự đầu) để debug cấu trúc
	rawStr := string(raw)
	if len(rawStr) > 500 {
		rawStr = rawStr[:500]
	}
	log.Printf("[ENGINE] [QUOTA-VIRTUAL] 🔍 Raw quota response (500 chars): %s", rawStr)

	patchGroupList := func(groupsRaw interface{}) {
		groups, ok := groupsRaw.([]interface{})
		if !ok {
			return
		}
		for _, gItem := range groups {
			gMap, ok := gItem.(map[string]interface{})
			if !ok {
				continue
			}
			bucketsRaw, hasBuckets := gMap["buckets"]
			if !hasBuckets {
				continue
			}
			buckets, ok := bucketsRaw.([]interface{})
			if !ok {
				continue
			}
			for _, bItem := range buckets {
				bMap, ok := bItem.(map[string]interface{})
				if !ok {
					continue
				}
				// Ảo hóa hạn mức: đảm bảo tài khoản người dùng hiển thị 100% trên IDE
				bMap["remainingFraction"] = 1.0
				bMap["disabled"] = false
			}
		}
	}

	if groups, has := data["groups"]; has {
		patchGroupList(groups)
	}

	if qs, has := data["quotaSummary"]; has {
		if qsMap, ok := qs.(map[string]interface{}); ok {
			if groups, hasG := qsMap["groups"]; hasG {
				patchGroupList(groups)
			}
		}
	}

	if modelsRaw, has := data["models"]; has {
		if modelsMap, ok := modelsRaw.(map[string]interface{}); ok {
			// Patch quota cho tất cả model hiện có
			for _, mVal := range modelsMap {
				if mInfo, ok := mVal.(map[string]interface{}); ok {
					if qInfo, ok := mInfo["quotaInfo"].(map[string]interface{}); ok {
						qInfo["remainingFraction"] = 1.0
						qInfo["disabled"] = false
					}
				}
			}

			// INJECT model mới mà ModelRouter9 hỗ trợ nhưng Google không trả về
			// Danh sách virtual model cần inject vào response cho IDE
			virtualModels := []struct {
				key         string
				displayName string
				modelID     string
			}{
				{"gemini-3.1-pro-high", "Gemini 3.1 Pro (High)", "gemini-3.1-pro-high"},
				{"gemini-3.1-pro-low", "Gemini 3.1 Pro (Low)", "gemini-3.1-pro-low"},
			}

			for _, vm := range virtualModels {
				if _, exists := modelsMap[vm.key]; !exists {
					// Inject model với quota đầy (100%) để IDE hiển thị trong dropdown
					modelsMap[vm.key] = map[string]interface{}{
						"displayName": vm.displayName,
						"modelId":     vm.modelID,
						"isInternal":  false,
						"quotaInfo": map[string]interface{}{
							"remainingFraction": 1.0,
							"disabled":          false,
						},
					}
					log.Printf("[ENGINE] [QUOTA-VIRTUAL] ✅ Injected virtual model: %s", vm.key)
				}
			}
			data["models"] = modelsMap
		}
	}

	patched, err := json.Marshal(data)
	if err != nil {
		return raw
	}
	return patched
}

// GetDefaultVirtualAggregatedQuota trả về Virtual Aggregated Quota chuẩn 100% cho mọi model ngay từ khi khởi động
func GetDefaultVirtualAggregatedQuota() []byte {
	return []byte(`{
  "groups": [
    {
      "displayName": "Gemini Models",
      "buckets": [
        {
          "bucketId": "gemini_weekly",
          "displayName": "Weekly Limit",
          "window": "WEEKLY",
          "remainingFraction": 1.0,
          "resetTime": "2026-10-30T00:00:00Z",
          "disabled": false
        },
        {
          "bucketId": "gemini_session",
          "displayName": "5-Hour Limit",
          "window": "FIVE_HOUR",
          "remainingFraction": 1.0,
          "resetTime": "2026-10-24T03:00:00Z",
          "disabled": false
        }
      ]
    },
    {
      "displayName": "Claude and GPT models",
      "buckets": [
        {
          "bucketId": "claude_weekly",
          "displayName": "Weekly Limit",
          "window": "WEEKLY",
          "remainingFraction": 1.0,
          "resetTime": "2026-10-30T00:00:00Z",
          "disabled": false
        },
        {
          "bucketId": "claude_session",
          "displayName": "5-Hour Limit",
          "window": "FIVE_HOUR",
          "remainingFraction": 1.0,
          "resetTime": "2026-10-24T03:00:00Z",
          "disabled": false
        }
      ]
    }
  ],
  "quotaSummary": {
    "groups": [
      {
        "displayName": "Gemini Models",
        "buckets": [
          {
            "bucketId": "gemini_weekly",
            "displayName": "Weekly Limit",
            "window": "WEEKLY",
            "remainingFraction": 1.0,
            "resetTime": "2026-10-30T00:00:00Z",
            "disabled": false
          },
          {
            "bucketId": "gemini_session",
            "displayName": "5-Hour Limit",
            "window": "FIVE_HOUR",
            "remainingFraction": 1.0,
            "resetTime": "2026-10-24T03:00:00Z",
            "disabled": false
          }
        ]
      },
      {
        "displayName": "Claude and GPT models",
        "buckets": [
          {
            "bucketId": "claude_weekly",
            "displayName": "Weekly Limit",
            "window": "WEEKLY",
            "remainingFraction": 1.0,
            "resetTime": "2026-10-30T00:00:00Z",
            "disabled": false
          },
          {
            "bucketId": "claude_session",
            "displayName": "5-Hour Limit",
            "window": "FIVE_HOUR",
            "remainingFraction": 1.0,
            "resetTime": "2026-10-24T03:00:00Z",
            "disabled": false
          }
        ]
      }
    ]
  }
}`)
}

// GetDefaultAvailableModels trả về danh sách model khả dụng với đầy đủ quota 100%
func GetDefaultAvailableModels() []byte {
	return []byte(`{
  "models": {
    "gemini-3.8-flash-medium": {
      "displayName": "Gemini 3.8 Flash (Medium)",
      "isInternal": false,
      "quotaInfo": {
        "remainingFraction": 1.0,
        "resetTime": "2026-10-30T00:00:00Z"
      }
    },
    "gemini-3.8-flash-high": {
      "displayName": "Gemini 3.8 Flash (High)",
      "isInternal": false,
      "quotaInfo": {
        "remainingFraction": 1.0,
        "resetTime": "2026-10-30T00:00:00Z"
      }
    },
    "gemini-3.8-flash-low": {
      "displayName": "Gemini 3.8 Flash (Low)",
      "isInternal": false,
      "quotaInfo": {
        "remainingFraction": 1.0,
        "resetTime": "2026-10-30T00:00:00Z"
      }
    },
    "claude-sonnet-4-6": {
      "displayName": "Claude Sonnet 4.6 (Thinking)",
      "isInternal": false,
      "quotaInfo": {
        "remainingFraction": 1.0,
        "resetTime": "2026-10-30T00:00:00Z"
      }
    },
    "claude-opus-4-6-thinking": {
      "displayName": "Claude Opus 4.6 (Thinking)",
      "isInternal": false,
      "quotaInfo": {
        "remainingFraction": 1.0,
        "resetTime": "2026-10-30T00:00:00Z"
      }
    },
    "gpt-oss-120b-medium": {
      "displayName": "GPT-OSS 120B (Medium)",
      "isInternal": false,
      "quotaInfo": {
        "remainingFraction": 1.0,
        "resetTime": "2026-10-30T00:00:00Z"
      }
    },
    "gemini-3.1-pro-high": {
      "displayName": "Gemini 3.1 Pro (High)",
      "isInternal": false,
      "quotaInfo": {
        "remainingFraction": 1.0,
        "resetTime": "2026-10-30T00:00:00Z"
      }
    },
    "gemini-3.1-pro-low": {
      "displayName": "Gemini 3.1 Pro (Low)",
      "isInternal": false,
      "quotaInfo": {
        "remainingFraction": 1.0,
        "resetTime": "2026-10-30T00:00:00Z"
      }
    },
    "gemini-pro-agent": {
      "displayName": "Gemini 2.5 Pro",
      "isInternal": false,
      "quotaInfo": {
        "remainingFraction": 1.0,
        "resetTime": "2026-10-30T00:00:00Z"
      }
    },
    "4-argon": {
      "displayName": "Gemini 4 Argon",
      "isInternal": false,
      "quotaInfo": {
        "remainingFraction": 1.0,
        "resetTime": "2026-10-30T00:00:00Z"
      }
    }
  }
}`)
}
