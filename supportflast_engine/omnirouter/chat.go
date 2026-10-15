package omnirouter

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"database/sql"
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
	rrCounter          uint64
	accountCooldownMap sync.Map // connId -> time.Time
	chatHTTPClient     = &http.Client{
		Timeout: 300 * time.Second,
	}
)

type ChatCompletionRequest struct {
	Model     string                   `json:"model"`
	Messages  []map[string]interface{} `json:"messages"`
	Stream    *bool                    `json:"stream,omitempty"`
	UserAgent string                   `json:"userAgent,omitempty"`
	Request   map[string]interface{}   `json:"request,omitempty"`
	Project   string                   `json:"project,omitempty"`
}

func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// attachRateLimitHeaders đính kèm các Header hạn mức Quota chuẩn OpenAI/Antigravity vào HTTP Response
func attachRateLimitHeaders(w http.ResponseWriter, keyItem *ClientKey) {
	if keyItem == nil {
		return
	}
	limitReq := int64(100000)
	remainingReq := int64(100000)
	if keyItem.QuotaLimit > 0 {
		limitReq = keyItem.QuotaLimit
		remainingReq = limitReq - keyItem.UsedRequests - 1
		if remainingReq < 0 {
			remainingReq = 0
		}
	}
	resetSec := int64(3600)
	if keyItem.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, keyItem.ExpiresAt); err == nil {
			diff := int64(time.Until(t).Seconds())
			if diff > 0 {
				resetSec = diff
			} else {
				resetSec = 0
			}
		}
	}
	w.Header().Set("x-ratelimit-limit-requests", fmt.Sprintf("%d", limitReq))
	w.Header().Set("x-ratelimit-remaining-requests", fmt.Sprintf("%d", remainingReq))
	w.Header().Set("x-ratelimit-reset-requests", fmt.Sprintf("%ds", resetSec))
	w.Header().Set("x-ratelimit-used-requests", fmt.Sprintf("%d", keyItem.UsedRequests+1))
	w.Header().Set("x-ratelimit-total-tokens", fmt.Sprintf("%d", keyItem.TotalTokens))
}

// ConvertMessagesToGeminiContents chuyển đổi messages OpenAI sang contents của Gemini
func ConvertMessagesToGeminiContents(messages []map[string]interface{}) []map[string]interface{} {
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

// ParseGeminiChunk bóc tách delta text từ SSE chunk của Gemini
func ParseGeminiChunk(chunkJSON string) (string, int64, int64, int64) {
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(chunkJSON), &obj); err != nil {
		return "", 0, 0, 0
	}

	var deltaText string
	var pTok, cTok, caTok int64

	if uMeta, ok := obj["usageMetadata"].(map[string]interface{}); ok {
		if p, ok := uMeta["promptTokenCount"].(float64); ok {
			pTok = int64(p)
		}
		if c, ok := uMeta["candidatesTokenCount"].(float64); ok {
			cTok = int64(c)
		}
		if ca, ok := uMeta["cachedContentTokenCount"].(float64); ok {
			caTok = int64(ca)
		}
	}

	// Antigravity PA wrapper
	if respMap, ok := obj["response"].(map[string]interface{}); ok {
		if uMeta, ok := respMap["usageMetadata"].(map[string]interface{}); ok {
			if p, ok := uMeta["promptTokenCount"].(float64); ok {
				pTok = int64(p)
			}
			if c, ok := uMeta["candidatesTokenCount"].(float64); ok {
				cTok = int64(c)
			}
			if ca, ok := uMeta["cachedContentTokenCount"].(float64); ok {
				caTok = int64(ca)
			}
		}
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

	return deltaText, pTok, cTok, caTok
}

// HandleChatCompletions xử lý request /v1/chat/completions chuẩn OpenAI
func HandleChatCompletions(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		startTime := time.Now()

		// 1. Kiểm tra xác thực API Key
		authHeader := r.Header.Get("Authorization")
		apiKey := strings.TrimPrefix(authHeader, "Bearer ")
		apiKey = strings.TrimSpace(apiKey)
		if apiKey == "" {
			apiKey = strings.TrimSpace(r.Header.Get("x-api-key"))
		}

		var clientKeyItem *ClientKey
		if apiKey != "" {
			item, status, errMsg := CheckClientKeyStatus(apiKey)
			switch status {
			case "EXPIRED":
				log.Printf("[ENGINE] [OMNIROUTER] 🛑 Từ chối request vì API Key đã hết hạn: %s", errMsg)
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error": map[string]string{
						"message": errMsg,
						"type":    "quota_error",
						"code":    "key_expired",
					},
				})
				return
			case "QUOTA_EXCEEDED":
				log.Printf("[ENGINE] [OMNIROUTER] 🛑 Từ chối request vì API Key đã vượt quá hạn mức Quota: %s", errMsg)
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusTooManyRequests)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error": map[string]string{
						"message": errMsg,
						"type":    "quota_error",
						"code":    "quota_exceeded",
					},
				})
				return
			case "INACTIVE":
				log.Printf("[ENGINE] [OMNIROUTER] 🛑 Từ chối request vì API Key đã bị vô hiệu hóa")
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error": map[string]string{
						"message": "API Key đã bị tạm khóa bởi quản trị viên",
						"type":    "auth_error",
						"code":    "key_inactive",
					},
				})
				return
			case "OK":
				clientKeyItem = item
			}
		}

		// Đọc payload
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, `{"error":"Cannot read body"}`, http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		var req ChatCompletionRequest
		_ = json.Unmarshal(bodyBytes, &req)

		model := req.Model
		if model == "" {
			model = "gemini-3.1-pro-high"
		}

		isStream := true
		if req.Stream != nil && !*req.Stream {
			isStream = false
		}

		// 2. Lấy danh sách tài khoản AI từ RAM cache
		accounts := GetCachedActiveProviders()
		if len(accounts) == 0 {
			http.Error(w, `{"error":"Không có tài khoản AI nào khả dụng trong hệ thống"}`, http.StatusServiceUnavailable)
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
		var creds *Credentials
		maxAttempts := 5
		if maxAttempts > numAccounts {
			maxAttempts = numAccounts
		}

		// 3. Vòng lặp xoay vòng tài khoản (Load balancing & Failover)
		for attempt := 0; attempt < maxAttempts; attempt++ {
			idx := (startIdx + attempt) % numAccounts
			acc := readyAccounts[idx]

			c, cErr := GetValidCredentials(db, &acc)
			if cErr != nil {
				lastErr = cErr
				continue
			}

			creds = c
			usedAccount = &acc

			upstreamURL := "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse"
			generatedReqID := fmt.Sprintf("agent/%s/%d/%s/1", generateUUID(), time.Now().UnixMilli(), generateUUID())

			contents := ConvertMessagesToGeminiContents(req.Messages)
			geminiReq := map[string]interface{}{
				"model":     model,
				"userAgent": "antigravity",
				"requestId": generatedReqID,
				"request": map[string]interface{}{
					"contents": contents,
				},
			}
			if creds.ProjectId != "" && creds.ProjectId != "aicode-consumers" {
				geminiReq["project"] = creds.ProjectId
			}

			outboundBody, _ := json.Marshal(geminiReq)
			fReq, reqErr := http.NewRequestWithContext(r.Context(), "POST", upstreamURL, bytes.NewReader(outboundBody))
			if reqErr != nil {
				lastErr = reqErr
				continue
			}

			fReq.Header.Set("Content-Type", "application/json")
			fReq.Header.Set("Authorization", "Bearer "+creds.AccessToken)
			fReq.Header.Set("User-Agent", "antigravity/ide/2.11.0 darwin/arm64")

			rResp, doErr := chatHTTPClient.Do(fReq)
			if doErr != nil {
				lastErr = doErr
				continue
			}

			if rResp.StatusCode == http.StatusTooManyRequests {
				rResp.Body.Close()
				accountCooldownMap.Store(acc.ID, time.Now().Add(30*time.Second))
				lastErr = fmt.Errorf("rate limited (429) on %s", acc.Name)
				continue
			}

			if rResp.StatusCode != http.StatusOK {
				rResp.Body.Close()
				lastErr = fmt.Errorf("upstream error %d on %s", rResp.StatusCode, acc.Name)
				continue
			}

			resp = rResp
			break
		}

		if resp == nil {
			http.Error(w, fmt.Sprintf(`{"error":"Tất cả tài khoản AI đều bận hoặc gặp lỗi: %v"}`, lastErr), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		chatCmplID := "chatcmpl-" + generateUUID()
		createdTimestamp := time.Now().Unix()
		var promptTokens, completionTokens int64

		// 4. Xử lý Stream SSE
		if isStream {
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			attachRateLimitHeaders(w, clientKeyItem)
			w.WriteHeader(http.StatusOK)

			flusher, isFlusher := w.(http.Flusher)
			reader := bufio.NewReader(resp.Body)

			for {
				line, readErr := reader.ReadBytes('\n')
				if len(line) > 0 {
					lineStr := strings.TrimSpace(string(line))
					if strings.HasPrefix(lineStr, "data:") {
						dataJSON := strings.TrimSpace(strings.TrimPrefix(lineStr, "data:"))
						if dataJSON != "" && dataJSON != "[DONE]" {
							deltaText, pTok, cTok, _ := ParseGeminiChunk(dataJSON)
							if pTok > 0 {
								promptTokens = pTok
							}
							if cTok > 0 {
								completionTokens = cTok
							}
							if deltaText != "" {
								chunkObj := map[string]interface{}{
									"id":      chatCmplID,
									"object":  "chat.completion.chunk",
									"created": createdTimestamp,
									"model":   model,
									"choices": []map[string]interface{}{
										{
											"index": 0,
											"delta": map[string]string{
												"content": deltaText,
											},
											"finish_reason": nil,
										},
									},
								}
								chunkBytes, _ := json.Marshal(chunkObj)
								_, _ = fmt.Fprintf(w, "data: %s\n\n", string(chunkBytes))
								if isFlusher {
									flusher.Flush()
								}
							}
						}
					}
				}
				if readErr != nil {
					break
				}
			}

			// Chunk kết thúc chuẩn OpenAI
			doneChunk := map[string]interface{}{
				"id":      chatCmplID,
				"object":  "chat.completion.chunk",
				"created": createdTimestamp,
				"model":   model,
				"choices": []map[string]interface{}{
					{
						"index":         0,
						"delta":         map[string]string{},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]interface{}{
					"prompt_tokens":     promptTokens,
					"completion_tokens": completionTokens,
					"total_tokens":      promptTokens + completionTokens,
				},
			}
			doneBytes, _ := json.Marshal(doneChunk)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", string(doneBytes))
			_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
			if isFlusher {
				flusher.Flush()
			}

			if clientKeyItem != nil {
				RecordKeyUsageAsync(db, clientKeyItem.Key, 1, promptTokens+completionTokens)
			}
		} else {
			// Non-streaming: Gom toàn bộ phản hồi trả về JSON chuẩn OpenAI
			reader := bufio.NewReader(resp.Body)
			var fullContent strings.Builder

			for {
				line, readErr := reader.ReadBytes('\n')
				if len(line) > 0 {
					lineStr := strings.TrimSpace(string(line))
					if strings.HasPrefix(lineStr, "data:") {
						dataJSON := strings.TrimSpace(strings.TrimPrefix(lineStr, "data:"))
						if dataJSON != "" && dataJSON != "[DONE]" {
							deltaText, pTok, cTok, _ := ParseGeminiChunk(dataJSON)
							if pTok > 0 {
								promptTokens = pTok
							}
							if cTok > 0 {
								completionTokens = cTok
							}
							fullContent.WriteString(deltaText)
						}
					}
				}
				if readErr != nil {
					break
				}
			}

			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			attachRateLimitHeaders(w, clientKeyItem)
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":      chatCmplID,
				"object":  "chat.completion",
				"created": createdTimestamp,
				"model":   model,
				"choices": []map[string]interface{}{
					{
						"index": 0,
						"message": map[string]string{
							"role":    "assistant",
							"content": fullContent.String(),
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]interface{}{
					"prompt_tokens":     promptTokens,
					"completion_tokens": completionTokens,
					"total_tokens":      promptTokens + completionTokens,
				},
			})

			if clientKeyItem != nil {
				RecordKeyUsageAsync(db, clientKeyItem.Key, 1, promptTokens+completionTokens)
			}
		}

		if usedAccount != nil {
			log.Printf("[ENGINE] [OMNIROUTER] ✔ Chat hoàn thành qua %s (%s)", usedAccount.Name, model)
			keyName := "Direct API"
			if clientKeyItem != nil {
				keyName = clientKeyItem.Name
			}
			GlobalLogHub.RecordLog(RouterLogEntry{
				ClientDevice:     r.RemoteAddr,
				APIKeyName:       keyName,
				Endpoint:         "/v1/chat/completions",
				Method:           "POST",
				Provider:         usedAccount.Provider,
				AccountID:        usedAccount.ID,
				AccountName:      usedAccount.Email,
				Model:            model,
				LatencyMs:        time.Since(startTime).Milliseconds(),
				StatusCode:       http.StatusOK,
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				IsStream:         isStream,
			})
		}
	}
}
