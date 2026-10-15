package omnirouter

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
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
				// Khi request đi đến daily-cloudcode-pa.googleapis.com:443,
				// thay bằng IP thật để bypass hosts file redirect 127.0.0.1
				host, port, _ := net.SplitHostPort(addr)
				if host == "daily-cloudcode-pa.googleapis.com" {
					realAddr := net.JoinHostPort(upstreamRealHost, port)
					return net.DialTimeout(network, realAddr, 15*time.Second)
				}
				return net.DialTimeout(network, addr, 15*time.Second)
			},
			TLSClientConfig: &tls.Config{
				ServerName: "daily-cloudcode-pa.googleapis.com",
			},
			MaxIdleConns:        50,
			MaxIdleConnsPerHost: 20,
			IdleConnTimeout:     120 * time.Second,
		},
	}
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
		maxAttempts := 5
		if maxAttempts > numAccounts {
			maxAttempts = numAccounts
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

			// Xây dựng URL upstream thật
			upstreamURL := "https://daily-cloudcode-pa.googleapis.com" + r.URL.Path
			if r.URL.RawQuery != "" {
				upstreamURL += "?" + r.URL.RawQuery
			}

			// Tạo request mới với body gốc
			fReq, reqErr := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, bytes.NewReader(bodyBytes))
			if reqErr != nil {
				lastErr = reqErr
				continue
			}

			// Copy headers gốc từ IDE (trừ Authorization và Host)
			for key, values := range r.Header {
				keyLower := strings.ToLower(key)
				if keyLower == "authorization" || keyLower == "host" || keyLower == "connection" {
					continue
				}
				for _, v := range values {
					fReq.Header.Add(key, v)
				}
			}

			// Thay Authorization bằng token fresh từ tài khoản AI pool
			fReq.Header.Set("Authorization", "Bearer "+creds.AccessToken)
			fReq.Header.Set("Content-Type", "application/json")
			if fReq.Header.Get("User-Agent") == "" {
				fReq.Header.Set("User-Agent", "antigravity/ide/2.24.0 windows/amd64")
			}

			rResp, doErr := geminiProxyClient.Do(fReq)
			if doErr != nil {
				lastErr = doErr
				continue
			}

			if rResp.StatusCode == http.StatusTooManyRequests {
				rResp.Body.Close()
				accountCooldownMap.Store(acc.ID, time.Now().Add(30*time.Second))
				lastErr = fmt.Errorf("rate limited (429) on %s", acc.Name)
				log.Printf("[ENGINE] [GEMINI-PROXY] ⚠ Rate limited (429) trên %s, chuyển tài khoản tiếp...", acc.Name)
				continue
			}

			if rResp.StatusCode != http.StatusOK {
				// Đọc body lỗi để log
				errBody, _ := io.ReadAll(io.LimitReader(rResp.Body, 512))
				rResp.Body.Close()
				lastErr = fmt.Errorf("upstream error %d on %s: %s", rResp.StatusCode, acc.Name, string(errBody))
				log.Printf("[ENGINE] [GEMINI-PROXY] ⚠ Upstream lỗi %d trên %s: %s", rResp.StatusCode, acc.Name, string(errBody))
				continue
			}

			resp = rResp
			usedAccount = &acc
			break
		}

		if resp == nil {
			errMsg := fmt.Sprintf(`{"error":{"message":"Toàn bộ tài khoản và fallback tiers đều bận hoặc lỗi: %v","code":"gateway_error"}}`, lastErr)
			log.Printf("[ENGINE] [GEMINI-PROXY] ✗ Tất cả tài khoản đều thất bại: %v", lastErr)
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

		buf := make([]byte, 4096)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
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
			log.Printf("[ENGINE] [GEMINI-PROXY] ✔ Proxy hoàn thành qua %s (%s) - %dms - Path: %s", usedAccount.Name, model, latencyMs, r.URL.Path)
			GlobalLogHub.RecordLog(RouterLogEntry{
				ClientDevice: r.RemoteAddr,
				APIKeyName:   "Antigravity IDE (Native Gemini Proxy)",
				Endpoint:     r.URL.Path,
				Method:       r.Method,
				Provider:     usedAccount.Provider,
				AccountID:    usedAccount.ID,
				AccountName:  usedAccount.Email,
				Model:        model,
				LatencyMs:    latencyMs,
				StatusCode:   http.StatusOK,
				IsStream:     strings.Contains(r.URL.RawQuery, "alt=sse"),
			})
		}
	}
}

// extractModelFromBody trích model name từ JSON body (nếu có trường "model")
func extractModelFromBody(body []byte) string {
	// Parse nhanh không dùng json.Unmarshal full để giữ hiệu năng
	s := string(body)
	idx := strings.Index(s, `"model"`)
	if idx == -1 {
		return "gemini-native"
	}
	sub := s[idx+7:]
	// Tìm dấu : rồi tìm giá trị trong dấu "
	colonIdx := strings.Index(sub, ":")
	if colonIdx == -1 {
		return "gemini-native"
	}
	sub = sub[colonIdx+1:]
	quoteStart := strings.Index(sub, `"`)
	if quoteStart == -1 {
		return "gemini-native"
	}
	sub = sub[quoteStart+1:]
	quoteEnd := strings.Index(sub, `"`)
	if quoteEnd == -1 {
		return "gemini-native"
	}
	return sub[:quoteEnd]
}
