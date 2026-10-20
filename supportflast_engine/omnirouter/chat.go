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

// resolveCascadeModelTiers phân giải danh sách model tiers theo thứ tự ưu tiên (Tier 1 Primary -> Tier 2 Fallback 1 -> Tier 3 Fallback 2...)
// TUÂN THỦ NGUYÊN TẮC: Luôn luôn giữ Model chính ở vị trí đầu tiên (Tier 1). Chỉ chuyển sang Tier tiếp theo khi Tier 1 thực sự chết.
func resolveCascadeModelTiers(db *sql.DB, requestedModel string) []string {
	reqTrim := strings.TrimSpace(requestedModel)
	if reqTrim == "" {
		return []string{"gemini-3.8-flash-high"}
	}

	// 1. Kiểm tra nếu requestedModel là ID của Combo (ví dụ: combo/smart-code, combo/fast-flash, ...)
	if db != nil {
		var valData string
		err := db.QueryRow("SELECT val_data FROM omni_kv WHERE scope = 'combos' AND key_name = ?", reqTrim).Scan(&valData)
		if err == nil && valData != "" {
			var combo struct {
				Models []string `json:"models"`
			}
			if err := json.Unmarshal([]byte(valData), &combo); err == nil && len(combo.Models) > 0 {
				return combo.Models
			}
		}
	}

	// 2. requestedModel chính là Model TIER 1 (Primary Model)
	normPrimary := reqTrim
	lowerNorm := strings.ToLower(normPrimary)
	switch {
	case strings.Contains(lowerNorm, "opus"):
		normPrimary = "claude-opus-4-6-thinking"
	case strings.Contains(lowerNorm, "sonnet"):
		normPrimary = "claude-sonnet-4-6"
	case strings.Contains(lowerNorm, "gpt-oss") || strings.Contains(lowerNorm, "120b"):
		normPrimary = "gpt-oss-120b-medium"
	case strings.Contains(lowerNorm, "3.1-pro") || strings.Contains(lowerNorm, "pro-agent") || strings.Contains(lowerNorm, "2.5-pro") || strings.Contains(lowerNorm, "1.5-pro"):
		normPrimary = "gemini-pro-agent"
	case strings.Contains(lowerNorm, "argon"):
		normPrimary = "4-argon"
	case strings.Contains(lowerNorm, "flash"):
		normPrimary = "gemini-3.8-flash-high"
	default:
		normPrimary = reqTrim
	}

	tiers := []string{normPrimary}

	// TUYỆT ĐỐI KHÔNG tự ý nhồi thêm fallback model của hãng khác vào request khi người dùng không chọn combo!
	// Chỉ chạy ĐÚNG DUY NHẤT model do client yêu cầu.
	return tiers
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
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, x-api-key, x-9r-cli-token")
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
				SetActiveClientKey(item.Key)
			}
		}

		// N?u khng c API Key trong header ho?c key ngoi h? th?ng, t? d?ng fallback v? Active Client Key c?a ngu?i dng
				// Neu khong co API Key trong header hoac key ngoai he thong, tu dong fallback ve Active Client Key con hieu luc
		if clientKeyItem == nil {
			activeKey := GetActiveClientKey(db)
			if activeKey != "" {
				if item, status, _ := CheckClientKeyStatus(activeKey); status == "OK" && item != nil {
					clientKeyItem = item
					SetActiveClientKey(item.Key)
				}
			}
		}

		// CHAN TUYET DOI: Neu khong co API Key hop le hoac khoa da het han muc Quota -> Khong dung de cung cap duoc nua!
		if clientKeyItem == nil {
			log.Printf("[ENGINE] [OMNIROUTER] 🛑 Tu choi request vi khong co API Key hop le hoac khoa da het han muc")
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]string{
					"message": "Khoa API da dat han muc toi da va bi KHOA HOAN TOAN. Khong the tiep tuc cung cap yeu cau.",
					"type":    "quota_exhausted_error",
					"code":    "key_exhausted",
				},
			})
			return
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

		// 1. Phân giải danh sách Tầng Ưu Tiên (Tier 1: Model chính -> Tier 2, Tier 3: Dự phòng)
		modelTiers := resolveCascadeModelTiers(db, req.Model)

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
		var usedModel string
		maxAttempts := 5
		if maxAttempts > numAccounts {
			maxAttempts = numAccounts
		}

		// 3. THUẬT TOÁN CASCADE FALLBACK TIERS:
		// Duyệt tuần tự qua từng Tier (Tier 1 là Model chính do client chọn).
		// LUÔN LUÔN thử Model chính trước 100%.
		// CHỈ KHI Model chính gặp lỗi thực sự (429 Quota Exceeded, 503 Overloaded, 500, Timeout) trên toàn bộ tài khoản
		// thì mới tự động chuyển mạch (cascade fallback) sang tầng dự phòng tiếp theo.
		for tierIdx, currentTierModel := range modelTiers {
			tierSucceeded := false
			tierLabel := fmt.Sprintf("TIER %d (Model: %s)", tierIdx+1, currentTierModel)
			if tierIdx == 0 {
				tierLabel = fmt.Sprintf("TIER 1 CHÍNH (Model: %s)", currentTierModel)
			}

			for attempt := 0; attempt < maxAttempts; attempt++ {
				idx := (startIdx + attempt) % numAccounts
				acc := readyAccounts[idx]

				c, cErr := GetValidCredentials(db, &acc)
				if cErr != nil {
					lastErr = cErr
					continue
				}

				upstreamURL := "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse"
				generatedReqID := fmt.Sprintf("agent/%s/%d/%s/1", generateUUID(), time.Now().UnixMilli(), generateUUID())

				contents := ConvertMessagesToGeminiContents(req.Messages)
				geminiReq := map[string]interface{}{
					"model":     currentTierModel,
					"userAgent": "antigravity",
					"requestId": generatedReqID,
					"request": map[string]interface{}{
						"contents": contents,
					},
				}

				if c.ProjectId != "" && c.ProjectId != "aicode-consumers" {
					geminiReq["project"] = c.ProjectId
				}

				if len(contents) == 0 && req.Request != nil {
					geminiReq["request"] = req.Request
				}

				outboundBody, _ := json.Marshal(geminiReq)
				fReq, reqErr := http.NewRequestWithContext(r.Context(), "POST", upstreamURL, bytes.NewReader(outboundBody))
				if reqErr != nil {
					lastErr = reqErr
					continue
				}

				fReq.Header.Set("Content-Type", "application/json")
				fReq.Header.Set("Authorization", "Bearer "+c.AccessToken)
				fReq.Header.Set("User-Agent", "antigravity/ide/2.11.0 darwin/arm64")

				rResp, doErr := chatHTTPClient.Do(fReq)
				if doErr != nil {
					lastErr = doErr
					continue
				}

				if rResp.StatusCode == http.StatusTooManyRequests {
					rResp.Body.Close()
					accountCooldownMap.Store(acc.ID, time.Now().Add(30*time.Second))
					lastErr = fmt.Errorf("rate limited (429 Quota) trên %s cho model %s", acc.Name, currentTierModel)
					continue
				}

				if rResp.StatusCode != http.StatusOK {
					errBody, _ := io.ReadAll(io.LimitReader(rResp.Body, 1024))
					rResp.Body.Close()
					lastErr = fmt.Errorf("upstream error %d trên %s cho model %s: %s", rResp.StatusCode, acc.Name, currentTierModel, strings.TrimSpace(string(errBody)))
					continue
				}

				// Tầng hiện tại đã phục vụ thành công!
				resp = rResp
				usedAccount = &acc
				usedModel = currentTierModel
				tierSucceeded = true
				break
			}

			if tierSucceeded {
				if tierIdx > 0 {
					log.Printf("[ENGINE] [CASCADE] ⚡ Model chính gặp sự cố, đã chuyển mạch sang %s thành công qua tài khoản %s", tierLabel, usedAccount.Name)
				} else {
					log.Printf("[ENGINE] [CASCADE] ✔ Model chính %s phản hồi thành công qua tài khoản %s", tierLabel, usedAccount.Name)
				}
				break
			}

			log.Printf("[ENGINE] [CASCADE] ⚠ %s gặp lỗi không thể phục vụ trên tất cả tài khoản (%v). Kích hoạt cascade fallback sang tầng tiếp theo...", tierLabel, lastErr)
		}

		if resp == nil {
			http.Error(w, fmt.Sprintf(`{"error":"Tất cả tầng phân luồng và tài khoản AI đều bận hoặc gặp lỗi: %v"}`, lastErr), http.StatusBadGateway)
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
									"model":   usedModel,
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
				"model":   usedModel,
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

			accEmail := ""
			accName := "Ti kho?n Antigravity"
			if usedAccount != nil {
				accEmail = usedAccount.Email
				accName = usedAccount.Name
			}
			reqKey := ""
			if clientKeyItem != nil {
				reqKey = clientKeyItem.Key
			}
			ReportProxyUsageWithKeyAsync(db, reqKey, usedModel, accEmail, accName, r.URL.Path, time.Since(startTime).Milliseconds(), http.StatusOK, promptTokens, completionTokens)
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
				"model":   usedModel,
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

			accEmail := ""
			accName := "Ti kho?n Antigravity"
			if usedAccount != nil {
				accEmail = usedAccount.Email
				accName = usedAccount.Name
			}
			reqKey := ""
			if clientKeyItem != nil {
				reqKey = clientKeyItem.Key
			}
			ReportProxyUsageWithKeyAsync(db, reqKey, usedModel, accEmail, accName, r.URL.Path, time.Since(startTime).Milliseconds(), http.StatusOK, promptTokens, completionTokens)
		}

		if usedAccount != nil {
			log.Printf("[ENGINE] [OMNIROUTER] ✔ Chat hoàn thành qua %s (Model: %s)", usedAccount.Name, usedModel)
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
				Model:            usedModel,
				LatencyMs:        time.Since(startTime).Milliseconds(),
				StatusCode:       http.StatusOK,
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				IsStream:         isStream,
			})
		}
	}
}
