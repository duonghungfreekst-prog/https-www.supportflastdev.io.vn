package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	proxyHTTPClient = &http.Client{Timeout: 300 * time.Second}
	cooldownMap     sync.Map // accID -> time.Time
)

func generateRandomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// convertOpenAIMessagesToGemini chuyển đổi tin nhắn OpenAI sang cấu trúc contents của Gemini
func convertOpenAIMessagesToGemini(messages []map[string]interface{}) []map[string]interface{} {
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

// parseDeltaText bóc tách phần text mới từ SSE chunk
func parseDeltaText(chunkJSON string) string {
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

// handleProxy xử lý toàn bộ luồng chat từ Cursor, Antigravity, Claude Code...
func (c *DesktopClient) handleProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.WriteHeader(http.StatusOK)
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")

	// 1. Kiểm tra trạng thái bật/tắt Proxy trên giao diện Desktop
	if !c.isProxyEnabled() {
		http.Error(w, `{"error": "9Router Desktop Proxy đang ở trạng thái TẠM DỪNG. Vui lòng bật lại từ Bảng Điều Khiển Desktop hoặc System Tray."}`, http.StatusServiceUnavailable)
		return
	}

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

	// 2. Lấy danh sách token sẵn có từ RAM
	c.mu.RLock()
	tokens := make([]TokenItem, len(c.tokens))
	copy(tokens, c.tokens)
	renderURL := strings.TrimRight(c.config.RenderURL, "/")
	masterKey := c.config.MasterAPIKey
	c.mu.RUnlock()

	if len(tokens) == 0 {
		// Thử forward trực tiếp lên Render Gateway nếu cấu hình MasterAPIKey hợp lệ
		if renderURL != "" && masterKey != "" && masterKey != "sk-9router-xxxxxxxxxxxxxxxx" {
			c.forwardToRender(w, r, bodyBytes, isStream)
			return
		}
		http.Error(w, `{"error": "Chưa có tài khoản nào được nạp trong RAM Desktop. Hãy mở Bảng Điều Khiển để kiểm tra kết nối với Render Cloud."}`, http.StatusServiceUnavailable)
		return
	}

	// Lọc danh sách tài khoản không trong cooldown
	now := time.Now()
	var readyTokens []TokenItem
	for _, tok := range tokens {
		if coolUntil, ok := cooldownMap.Load(tok.ID); ok {
			if now.Before(coolUntil.(time.Time)) {
				continue
			}
		}
		readyTokens = append(readyTokens, tok)
	}
	if len(readyTokens) == 0 {
		readyTokens = tokens
	}

	numTokens := len(readyTokens)
	startIdx := int(atomic.AddUint64(&c.rrCounter, 1) % uint64(numTokens))

	var resp *http.Response
	var usedToken *TokenItem
	var lastErr error

	// 3. Xoay tua và thử từng tài khoản nếu gặp 429
	for attempt := 0; attempt < numTokens; attempt++ {
		idx := (startIdx + attempt) % numTokens
		tok := readyTokens[idx]

		upstreamURL := "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse"
		reqID := fmt.Sprintf("agent/%s/%d/%s/1", generateRandomHex(16), time.Now().UnixMilli(), generateRandomHex(16))

		contents := convertOpenAIMessagesToGemini(reqPayload.Messages)
		geminiReq := map[string]interface{}{
			"model":     model,
			"userAgent": "antigravity",
			"requestId": reqID,
			"request": map[string]interface{}{
				"contents": contents,
			},
		}
		if tok.ProjectID != "" && tok.ProjectID != "aicode-consumers" {
			geminiReq["project"] = tok.ProjectID
		}

		outBody, _ := json.Marshal(geminiReq)
		fReq, rErr := http.NewRequestWithContext(r.Context(), "POST", upstreamURL, bytes.NewReader(outBody))
		if rErr != nil {
			lastErr = rErr
			continue
		}

		fReq.Header.Set("Content-Type", "application/json")
		fReq.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		fReq.Header.Set("User-Agent", "antigravity/ide/2.11.0 darwin/arm64")

		rResp, doErr := proxyHTTPClient.Do(fReq)
		if doErr != nil {
			lastErr = doErr
			continue
		}

		if rResp.StatusCode == http.StatusTooManyRequests {
			rResp.Body.Close()
			cooldownMap.Store(tok.ID, time.Now().Add(30*time.Second))
			lastErr = fmt.Errorf("rate limited (429) on %s", tok.Name)
			continue
		}

		if rResp.StatusCode != http.StatusOK {
			rResp.Body.Close()
			lastErr = fmt.Errorf("upstream error %d on %s", rResp.StatusCode, tok.Name)
			continue
		}

		resp = rResp
		usedToken = &tok
		break
	}

	if resp == nil {
		if renderURL != "" && masterKey != "" && masterKey != "sk-9router-xxxxxxxxxxxxxxxx" {
			log.Printf("[LOCAL PROXY] [FALLBACK] Tài khoản nội bộ lỗi (%v), chuyển tiếp lên Render Gateway: %s", lastErr, renderURL)
			c.forwardToRender(w, r, bodyBytes, isStream)
			return
		}
		http.Error(w, fmt.Sprintf(`{"error":"Tất cả tài khoản AI đều gặp lỗi: %v"}`, lastErr), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	atomic.AddUint64(&c.totalReqs, 1)
	chatCmplID := "chatcmpl-" + generateRandomHex(16)
	createdTS := time.Now().Unix()

	// 4. Stream phản hồi SSE về máy trạm
	if isStream {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		flusher, isFlusher := w.(http.Flusher)
		reader := bufio.NewReader(resp.Body)
		var estimatedTokens int64 = 0

		for {
			line, rErr := reader.ReadBytes('\n')
			if len(line) > 0 {
				lineStr := strings.TrimSpace(string(line))
				if strings.HasPrefix(lineStr, "data:") {
					d := strings.TrimSpace(strings.TrimPrefix(lineStr, "data:"))
					if d != "" && d != "[DONE]" {
						delta := parseDeltaText(d)
						if delta != "" {
							estimatedTokens += int64(len(delta) / 4)
							if estimatedTokens < 1 {
								estimatedTokens = 1
							}
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

		atomic.AddUint64(&c.totalTokens, uint64(estimatedTokens+int64(len(bodyBytes)/4)))
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
						full.WriteString(parseDeltaText(d))
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

	if usedToken != nil {
		log.Printf("[LOCAL PROXY] ✔ Phản hồi 0ms thành công qua tài khoản: %s (%s)", usedToken.Name, model)
	}
}

// forwardToRender chuyển tiếp trực tiếp request lên Render Gateway khi không có token trong RAM hoặc gặp lỗi
func (c *DesktopClient) forwardToRender(w http.ResponseWriter, r *http.Request, reqBodyBytes []byte, isStream bool) {
	c.mu.RLock()
	renderURL := strings.TrimRight(c.config.RenderURL, "/")
	masterKey := c.config.MasterAPIKey
	c.mu.RUnlock()

	targetURL := renderURL + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(r.Context(), "POST", targetURL, bytes.NewReader(reqBodyBytes))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Không thể khởi tạo request chuyển tiếp: %v"}`, err), http.StatusInternalServerError)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+masterKey)
	req.Header.Set("User-Agent", "9Router-Desktop-Client/2.8.5")

	resp, err := proxyHTTPClient.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Lỗi kết nối tới Render Gateway (%s): %v"}`, renderURL, err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	atomic.AddUint64(&c.totalReqs, 1)

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	if isStream {
		flusher, isFlusher := w.(http.Flusher)
		buf := make([]byte, 4096)
		for {
			n, rErr := resp.Body.Read(buf)
			if n > 0 {
				_, _ = w.Write(buf[:n])
				if isFlusher {
					flusher.Flush()
				}
			}
			if rErr != nil {
				break
			}
		}
	} else {
		_, _ = io.Copy(w, resp.Body)
	}
	log.Printf("[LOCAL PROXY] [FORWARD] ✔ Đã chuyển tiếp request thành công lên Render Gateway: %s", renderURL)
}
