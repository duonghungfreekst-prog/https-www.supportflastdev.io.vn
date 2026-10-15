package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Config cấu hình cho 9Router Local Client
type Config struct {
	WebServerURL string `json:"web_server_url"`
	APIKey       string `json:"api_key"`
	LocalPort    int    `json:"local_port"`
}

// ClientAccountPayload thông tin tài khoản nạp trong RAM
type ClientAccountPayload struct {
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

// ClientSyncResponse phản hồi đồng bộ từ Web Master
type ClientSyncResponse struct {
	Status          string                 `json:"status"`
	SyncIntervalSec int                    `json:"sync_interval_sec"`
	RoutingDNS      map[string]string      `json:"routing_dns"`
	Accounts        []ClientAccountPayload `json:"accounts"`
}

var (
	cfg            Config
	accountsMu     sync.RWMutex
	accountsList   []ClientAccountPayload
	cooldownMap    sync.Map // accID -> time.Time
	rrCounter      uint64
	upstreamClient = &http.Client{Timeout: 300 * time.Second}
	syncClient     = &http.Client{Timeout: 20 * time.Second}

	// Bộ đếm thống kê cục bộ
	localReqsServed  uint64
	localTokensUsed  uint64
	reportedReqs     uint64
	reportedTokens   uint64
)

func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func loadConfig(path string) error {
	f, err := os.Open(path)
	if err != nil {
		// Tạo file mẫu nếu chưa có
		cfg = Config{
			WebServerURL: "https://supportflastdev.io.vn",
			APIKey:       "sk-9router-xxxxxxxxxxxxxxxx",
			LocalPort:    8080,
		}
		data, _ := json.MarshalIndent(cfg, "", "  ")
		_ = os.WriteFile(path, data, 0644)
		return fmt.Errorf("đã tạo tệp cấu hình mẫu tại %s, hãy điền api_key và chạy lại", path)
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(&cfg)
}

// syncFromWeb gọi lên Web Master kéo Token và Quota về RAM
func syncFromWeb() error {
	url := strings.TrimRight(cfg.WebServerURL, "/") + "/api/v1/client/sync"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	resp, err := syncClient.Do(req)
	if err != nil {
		return fmt.Errorf("kết nối tới Web Master thất bại: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Web Master từ chối (HTTP %d): %s", resp.StatusCode, string(b))
	}

	var syncResp ClientSyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&syncResp); err != nil {
		return fmt.Errorf("parse JSON từ Web Master thất bại: %w", err)
	}

	accountsMu.Lock()
	accountsList = syncResp.Accounts
	accountsMu.Unlock()

	log.Printf("[CLIENT] ✔ Đã đồng bộ thành công %d tài khoản AI từ Web Master về RAM!", len(syncResp.Accounts))
	return nil
}

// reportToWeb gửi báo cáo lượng token đã dùng lên Web Master
func reportToWeb() {
	currReqs := atomic.LoadUint64(&localReqsServed)
	currTokens := atomic.LoadUint64(&localTokensUsed)

	diffReqs := currReqs - reportedReqs
	diffTokens := currTokens - reportedTokens

	if diffReqs == 0 && diffTokens == 0 {
		// Vẫn ping nhẹ để giữ Web thức 24/24
		pingURL := strings.TrimRight(cfg.WebServerURL, "/") + "/ping"
		r, err := syncClient.Get(pingURL)
		if err == nil {
			r.Body.Close()
		}
		return
	}

	payload := map[string]interface{}{
		"api_key":         cfg.APIKey,
		"used_requests":   diffReqs,
		"total_tokens":    diffTokens,
		"account_updates": []interface{}{},
	}

	body, _ := json.Marshal(payload)
	url := strings.TrimRight(cfg.WebServerURL, "/") + "/api/v1/client/report"
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		resp, errDo := syncClient.Do(req)
		if errDo == nil {
			resp.Body.Close()
			reportedReqs = currReqs
			reportedTokens = currTokens
			log.Printf("[CLIENT] 📤 Đã báo cáo ngầm lên Web Master: +%d requests, +%d tokens", diffReqs, diffTokens)
		}
	}
}

// convertMessagesToGemini chuẩn hóa tin nhắn OpenAI sang Gemini
func convertMessagesToGemini(messages []map[string]interface{}) []map[string]interface{} {
	var contents []map[string]interface{}
	for _, m := range messages {
		role, _ := m["role"].(string)
		if role == "assistant" {
			role = "model"
		} else if role == "system" {
			role = "user"
		}
		rawContent := m["content"]
		var parts []map[string]interface{}

		switch c := rawContent.(type) {
		case string:
			parts = append(parts, map[string]interface{}{"text": c})
		case []interface{}:
			for _, item := range c {
				if itemMap, ok := item.(map[string]interface{}); ok {
					if t, ok := itemMap["type"].(string); ok && t == "text" {
						if txt, ok := itemMap["text"].(string); ok {
							parts = append(parts, map[string]interface{}{"text": txt})
						}
					}
				}
			}
		}

		if len(parts) > 0 {
			contents = append(contents, map[string]interface{}{
				"role":  role,
				"parts": parts,
			})
		}
	}
	return contents
}

// parseGeminiDelta tách text từ Gemini chunk
func parseGeminiDelta(chunkJSON string) string {
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(chunkJSON), &obj); err != nil {
		return ""
	}
	var deltaText string
	if respMap, ok := obj["response"].(map[string]interface{}); ok {
		if candidates, ok := respMap["candidates"].([]interface{}); ok && len(candidates) > 0 {
			if cand, ok := candidates[0].(map[string]interface{}); ok {
				if content, ok := cand["content"].(map[string]interface{}); ok {
					if parts, ok := content["parts"].([]interface{}); ok {
						for _, p := range parts {
							if pMap, ok := p.(map[string]interface{}); ok {
								if t, ok := pMap["text"].(string); ok {
									deltaText += t
								}
							}
						}
					}
				}
			}
		}
	} else if candidates, ok := obj["candidates"].([]interface{}); ok && len(candidates) > 0 {
		if cand, ok := candidates[0].(map[string]interface{}); ok {
			if content, ok := cand["content"].(map[string]interface{}); ok {
				if parts, ok := content["parts"].([]interface{}); ok {
					for _, p := range parts {
						if pMap, ok := p.(map[string]interface{}); ok {
							if t, ok := pMap["text"].(string); ok {
								deltaText += t
							}
						}
					}
				}
			}
		}
	}
	return deltaText
}

// handleLocalChatCompletions xử lý request từ Antigravity/Cursor/Claude Code
func handleLocalChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.WriteHeader(http.StatusOK)
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"Cannot read body"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var reqPayload struct {
		Model    string                   `json:"model"`
		Messages []map[string]interface{} `json:"messages"`
		Stream   *bool                    `json:"stream"`
	}
	_ = json.Unmarshal(bodyBytes, &reqPayload)

	model := reqPayload.Model
	if model == "" {
		model = "gemini-3.1-pro-high"
	}
	isStream := true
	if reqPayload.Stream != nil && !*reqPayload.Stream {
		isStream = false
	}

	accountsMu.RLock()
	accounts := make([]ClientAccountPayload, len(accountsList))
	copy(accounts, accountsList)
	accountsMu.RUnlock()

	if len(accounts) == 0 {
		http.Error(w, `{"error":"Chưa có tài khoản nào được đồng bộ từ Web Master"}`, http.StatusServiceUnavailable)
		return
	}

	// Lọc tài khoản không bị cooldown
	now := time.Now()
	var readyAccounts []ClientAccountPayload
	for _, acc := range accounts {
		if coolUntil, ok := cooldownMap.Load(acc.ID); ok {
			if now.Before(coolUntil.(time.Time)) {
				continue
			}
		}
		readyAccounts = append(readyAccounts, acc)
	}
	if len(readyAccounts) == 0 {
		readyAccounts = accounts
	}

	numAccs := len(readyAccounts)
	startIdx := int(atomic.AddUint64(&rrCounter, 1) % uint64(numAccs))

	var resp *http.Response
	var usedAcc *ClientAccountPayload
	var lastErr error

	for attempt := 0; attempt < numAccs; attempt++ {
		idx := (startIdx + attempt) % numAccs
		acc := readyAccounts[idx]

		upstreamURL := "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse"
		reqID := fmt.Sprintf("agent/%s/%d/%s/1", generateUUID(), time.Now().UnixMilli(), generateUUID())

		contents := convertMessagesToGemini(reqPayload.Messages)
		geminiReq := map[string]interface{}{
			"model":     model,
			"userAgent": "antigravity",
			"requestId": reqID,
			"request": map[string]interface{}{
				"contents": contents,
			},
		}
		if acc.ProjectID != "" && acc.ProjectID != "aicode-consumers" {
			geminiReq["project"] = acc.ProjectID
		}

		outBody, _ := json.Marshal(geminiReq)
		fReq, rErr := http.NewRequestWithContext(r.Context(), "POST", upstreamURL, bytes.NewReader(outBody))
		if rErr != nil {
			lastErr = rErr
			continue
		}

		fReq.Header.Set("Content-Type", "application/json")
		fReq.Header.Set("Authorization", "Bearer "+acc.AccessToken)
		fReq.Header.Set("User-Agent", "antigravity/ide/2.11.0 darwin/arm64")

		rResp, doErr := upstreamClient.Do(fReq)
		if doErr != nil {
			lastErr = doErr
			continue
		}

		if rResp.StatusCode == http.StatusTooManyRequests {
			rResp.Body.Close()
			cooldownMap.Store(acc.ID, time.Now().Add(30*time.Second))
			lastErr = fmt.Errorf("rate limited (429) on %s", acc.Name)
			continue
		}

		if rResp.StatusCode != http.StatusOK {
			rResp.Body.Close()
			lastErr = fmt.Errorf("upstream error %d on %s", rResp.StatusCode, acc.Name)
			continue
		}

		resp = rResp
		usedAcc = &acc
		break
	}

	if resp == nil {
		http.Error(w, fmt.Sprintf(`{"error":"Tất cả tài khoản AI đều gặp lỗi: %v"}`, lastErr), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	atomic.AddUint64(&localReqsServed, 1)
	chatCmplID := "chatcmpl-" + generateUUID()
	createdTS := time.Now().Unix()

	if isStream {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		flusher, isFlusher := w.(http.Flusher)
		reader := bufio.NewReader(resp.Body)

		for {
			line, rErr := reader.ReadBytes('\n')
			if len(line) > 0 {
				lineStr := strings.TrimSpace(string(line))
				if strings.HasPrefix(lineStr, "data:") {
					d := strings.TrimSpace(strings.TrimPrefix(lineStr, "data:"))
					if d != "" && d != "[DONE]" {
						delta := parseGeminiDelta(d)
						if delta != "" {
							chunk := map[string]interface{}{
								"id":      chatCmplID,
								"object":  "chat.completion.chunk",
								"created": createdTS,
								"model":   model,
								"choices": []map[string]interface{}{
									{
										"index":         0,
										"delta":         map[string]string{"content": delta},
										"finish_reason": nil,
									},
								},
							}
							b, _ := json.Marshal(chunk)
							_, _ = fmt.Fprintf(w, "data: %s\n\n", string(b))
							if isFlusher {
								flusher.Flush()
							}
						}
					}
				}
			}
			if rErr != nil {
				break
			}
		}

		doneChunk := map[string]interface{}{
			"id":      chatCmplID,
			"object":  "chat.completion.chunk",
			"created": createdTS,
			"model":   model,
			"choices": []map[string]interface{}{
				{"index": 0, "delta": map[string]string{}, "finish_reason": "stop"},
			},
		}
		b, _ := json.Marshal(doneChunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", string(b))
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		if isFlusher {
			flusher.Flush()
		}
	} else {
		reader := bufio.NewReader(resp.Body)
		var full strings.Builder
		for {
			line, rErr := reader.ReadBytes('\n')
			if len(line) > 0 {
				lineStr := strings.TrimSpace(string(line))
				if strings.HasPrefix(lineStr, "data:") {
					d := strings.TrimSpace(strings.TrimPrefix(lineStr, "data:"))
					if d != "" && d != "[DONE]" {
						full.WriteString(parseGeminiDelta(d))
					}
				}
			}
			if rErr != nil {
				break
			}
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":      chatCmplID,
			"object":  "chat.completion",
			"created": createdTS,
			"model":   model,
			"choices": []map[string]interface{}{
				{
					"index":   0,
					"message": map[string]string{"role": "assistant", "content": full.String()},
				},
			},
		})
	}

	if usedAcc != nil {
		log.Printf("[LOCAL PROXY] ✔ Phản hồi 0ms thành công qua tài khoản %s (%s)", usedAcc.Name, model)
	}
}

func main() {
	configPath := flag.String("config", "config.json", "Đường dẫn tệp cấu hình JSON")
	portFlag := flag.Int("port", 0, "Ghi đè cổng lắng nghe local")
	flag.Parse()

	log.Println("=====================================================================")
	log.Println("       9ROUTER LOCAL PROXY & LLM DNS (PURE GO / 0ms LATENCY)         ")
	log.Println("=====================================================================")

	execDir, _ := filepath.Abs(filepath.Dir(os.Args[0]))
	cfgFile := *configPath
	if !filepath.IsAbs(cfgFile) {
		cfgFile = filepath.Join(execDir, cfgFile)
	}

	if err := loadConfig(cfgFile); err != nil {
		log.Fatalf("[FATAL] %v", err)
	}

	if *portFlag > 0 {
		cfg.LocalPort = *portFlag
	}
	if cfg.LocalPort == 0 {
		cfg.LocalPort = 8080
	}

	log.Printf("[CLIENT] Web Master URL: %s", cfg.WebServerURL)
	log.Printf("[CLIENT] Đang kết nối xác thực bằng API Key: %s...", cfg.APIKey[:min(len(cfg.APIKey), 14)]+"...")

	// 1. Đồng bộ ban đầu
	if err := syncFromWeb(); err != nil {
		log.Printf("[CLIENT] [CẢNH BÁO] Chưa thể đồng bộ tài khoản lúc khởi động: %v", err)
		log.Println("[CLIENT] Sẽ thử lại định kỳ trong tiến trình ngầm...")
	}

	// 2. Tác vụ ngầm đồng bộ và báo cáo định kỳ mỗi 5 phút
	ticker := time.NewTicker(5 * time.Minute)
	go func() {
		defer ticker.Stop()
		for range ticker.C {
			reportToWeb()
			_ = syncFromWeb()
		}
	}()

	// 3. Khởi tạo ServeMux
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", handleLocalChatCompletions)
	mux.HandleFunc("/chat/completions", handleLocalChatCompletions)
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "list",
			"data": []map[string]string{
				{"id": "gemini-3.1-pro-high"},
				{"id": "gemini-2.5-pro"},
				{"id": "gemini-2.5-flash"},
				{"id": "claude-3-5-sonnet"},
				{"id": "claude-3-opus"},
			},
		})
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		accountsMu.RLock()
		cnt := len(accountsList)
		accountsMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":          "ok",
			"accounts_in_ram": cnt,
			"requests_served": atomic.LoadUint64(&localReqsServed),
			"web_master":      cfg.WebServerURL,
		})
	})

	addr := fmt.Sprintf("127.0.0.1:%d", cfg.LocalPort)
	log.Printf("🚀 9Router Local Proxy đang lắng nghe tại: http://%s", addr)
	log.Printf("🔌 Cấu hình Base URL cho Antigravity / Cursor / Claude Code:")
	log.Printf("   👉 Base URL: http://%s/v1", addr)
	log.Printf("   👉 API Key:  %s", cfg.APIKey)

	// Bắt tín hiệu tắt an toàn
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("\n[CLIENT] Đang gửi báo cáo cuối cùng trước khi thoát...")
		reportToWeb()
		log.Println("[CLIENT] Thoát an toàn.")
		os.Exit(0)
	}()

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("[FATAL] Lỗi chạy server: %v", err)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
