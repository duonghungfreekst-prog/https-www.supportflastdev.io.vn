package omnirouter

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	rrCounter          uint64
	accountCooldownMap sync.Map // connId -> time.Time
	chatTransport      = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 60 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		DisableKeepAlives:     false,
		MaxIdleConns:          500,
		MaxIdleConnsPerHost:   100, // Tối ưu Keep-Alive: tái sử dụng kết nối TLS sẵn có cho mỗi provider AI
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 120 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	chatHTTPClient = &http.Client{
		Transport: chatTransport,
		Timeout:   300 * time.Second,
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
	// NGUYÊN TẮC: Nếu model đã là tên chính xác hợp lệ (đã được user alias mapping hoặc sanitize đúng)
	// → GIỮ NGUYÊN TUYỆT ĐỐI. Chỉ normalize các model alias/lỗi thời chưa được resolve.
	normPrimary := reqTrim
	lowerNorm := strings.ToLower(normPrimary)

	// Danh sách model hợp lệ đã được chuẩn hóa - nếu trùng khớp → KHÔNG map lại
	knownValidModels := map[string]bool{
		"gemini-3.8-flash-high":    true,
		"gemini-3.8-flash-medium":  true,
		"gemini-3.8-flash-low":     true,
		"gemini-pro-agent":         true,
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
		"4-argon":                  true,
	}
	if knownValidModels[lowerNorm] {
		// Model đã hợp lệ (ví dụ: do user alias mapping từ UI) → giữ nguyên 100%
		log.Printf("[ENGINE] [GEMINI-PROXY] ✅ Model đã valid từ alias mapping: %q → giữ nguyên", normPrimary)
		return []string{normPrimary}
	}

	// Chỉ normalize khi model là alias/lỗi thời chưa được resolve (ví dụ: "gemini-2.5-pro", "claude-3-5-sonnet"...)
	switch {
	case strings.Contains(lowerNorm, "opus"):
		normPrimary = "claude-opus-4-6-thinking"
	case strings.Contains(lowerNorm, "sonnet"):
		normPrimary = "claude-sonnet-4-6"
	case strings.Contains(lowerNorm, "gpt-oss") || strings.Contains(lowerNorm, "120b"):
		normPrimary = "gpt-oss-120b-medium"
	case strings.Contains(lowerNorm, "3.1-pro") || strings.Contains(lowerNorm, "pro-high"):
		normPrimary = "gemini-3.8-flash-high"
	case strings.Contains(lowerNorm, "pro-agent") || strings.Contains(lowerNorm, "2.5-pro") || strings.Contains(lowerNorm, "1.5-pro"):
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

// DetermineProvider xác định provider upstream dựa trên ProviderConnection và model
func DetermineProvider(acc *ProviderConnection, model string) string {
	if acc != nil {
		p := strings.ToLower(strings.TrimSpace(acc.Provider))
		switch {
		case p == "openai" || p == "azure" || p == "codex":
			return "openai"
		case p == "anthropic" || p == "claude":
			return "anthropic"
		case p == "google" || p == "gemini":
			return "google"
		case p == "antigravity":
			return "antigravity"
		}
	}
	// Fallback dựa trên model nếu account provider chưa xác định
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(m, "openai/") || strings.HasPrefix(m, "gpt-") || strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") || strings.Contains(m, "chatgpt"):
		return "openai"
	case strings.HasPrefix(m, "anthropic/") || strings.Contains(m, "claude") || strings.Contains(m, "sonnet") || strings.Contains(m, "opus"):
		return "anthropic"
	case strings.HasPrefix(m, "google/") || strings.Contains(m, "gemini"):
		return "google"
	default:
		return "antigravity"
	}
}

// TargetProviderForModel xác định provider mục tiêu của một model để ưu tiên sắp xếp tài khoản
func TargetProviderForModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(m, "openai/") || strings.HasPrefix(m, "gpt-") || strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") || strings.Contains(m, "chatgpt"):
		return "openai"
	case strings.HasPrefix(m, "anthropic/") || strings.Contains(m, "claude") || strings.Contains(m, "sonnet") || strings.Contains(m, "opus"):
		return "anthropic"
	case strings.HasPrefix(m, "google/") || strings.Contains(m, "gemini"):
		return "antigravity"
	default:
		return ""
	}
}

// orderAccountsForChat sắp xếp tài khoản ưu tiên theo Provider phù hợp với model
func orderAccountsForChat(accounts []ProviderConnection, currentTierModel string, startIdx int) []ProviderConnection {
	n := len(accounts)
	if n <= 1 {
		return accounts
	}
	base := make([]ProviderConnection, n)
	for i := 0; i < n; i++ {
		base[i] = accounts[(startIdx+i)%n]
	}

	targetProv := TargetProviderForModel(currentTierModel)
	if targetProv == "" {
		return base
	}

	var matched []ProviderConnection
	var others []ProviderConnection

	for _, acc := range base {
		p := strings.ToLower(strings.TrimSpace(acc.Provider))
		isMatch := false
		switch targetProv {
		case "openai":
			isMatch = (p == "openai" || p == "azure" || p == "codex")
		case "anthropic":
			isMatch = (p == "anthropic" || p == "claude" || p == "antigravity")
		case "google", "antigravity":
			isMatch = (p == "google" || p == "gemini" || p == "antigravity")
		}
		if isMatch {
			matched = append(matched, acc)
		} else {
			others = append(others, acc)
		}
	}

	if len(matched) > 0 {
		return append(matched, others...)
	}
	return base
}

// ConvertMessagesToAnthropic chuyển đổi OpenAI messages sang format Anthropic Messages API
func ConvertMessagesToAnthropic(messages []map[string]interface{}) (string, []map[string]interface{}) {
	var systemParts []string
	var anthropicMsgs []map[string]interface{}

	for _, m := range messages {
		role, _ := m["role"].(string)
		content := m["content"]
		if role == "system" || role == "developer" {
			switch c := content.(type) {
			case string:
				if strings.TrimSpace(c) != "" {
					systemParts = append(systemParts, strings.TrimSpace(c))
				}
			case []interface{}:
				for _, item := range c {
					if im, ok := item.(map[string]interface{}); ok {
						if txt, ok := im["text"].(string); ok && strings.TrimSpace(txt) != "" {
							systemParts = append(systemParts, strings.TrimSpace(txt))
						}
					}
				}
			}
			continue
		}

		if role == "model" {
			role = "assistant"
		} else if role != "user" && role != "assistant" {
			role = "user"
		}

		anthropicMsgs = append(anthropicMsgs, map[string]interface{}{
			"role":    role,
			"content": content,
		})
	}

	if len(anthropicMsgs) == 0 {
		anthropicMsgs = append(anthropicMsgs, map[string]interface{}{
			"role":    "user",
			"content": "Hello",
		})
	}

	return strings.Join(systemParts, "\n\n"), anthropicMsgs
}

// ParseOpenAIChunk bóc tách delta text và tokens từ SSE chunk của OpenAI
func ParseOpenAIChunk(chunkJSON string) (string, int64, int64, string) {
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(chunkJSON), &obj); err != nil {
		return "", 0, 0, ""
	}

	var deltaText, finishReason string
	var pTok, cTok int64

	if usage, ok := obj["usage"].(map[string]interface{}); ok && usage != nil {
		if p, ok := usage["prompt_tokens"].(float64); ok {
			pTok = int64(p)
		}
		if c, ok := usage["completion_tokens"].(float64); ok {
			cTok = int64(c)
		}
	}

	if choices, ok := obj["choices"].([]interface{}); ok && len(choices) > 0 {
		if ch, ok := choices[0].(map[string]interface{}); ok {
			if fr, ok := ch["finish_reason"].(string); ok {
				finishReason = fr
			}
			if delta, ok := ch["delta"].(map[string]interface{}); ok {
				if c, ok := delta["content"].(string); ok {
					deltaText = c
				}
			}
		}
	}

	return deltaText, pTok, cTok, finishReason
}

// ParseAnthropicChunk bóc tách delta text và tokens từ SSE chunk của Anthropic
func ParseAnthropicChunk(chunkJSON string) (string, int64, int64, string) {
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(chunkJSON), &obj); err != nil {
		return "", 0, 0, ""
	}

	var deltaText, finishReason string
	var pTok, cTok int64

	evtType, _ := obj["type"].(string)
	switch evtType {
	case "content_block_delta":
		if delta, ok := obj["delta"].(map[string]interface{}); ok {
			if t, ok := delta["text"].(string); ok {
				deltaText = t
			}
		}
	case "message_start":
		if msg, ok := obj["message"].(map[string]interface{}); ok {
			if usage, ok := msg["usage"].(map[string]interface{}); ok {
				if inp, ok := usage["input_tokens"].(float64); ok {
					pTok = int64(inp)
				}
			}
		}
	case "message_delta":
		if delta, ok := obj["delta"].(map[string]interface{}); ok {
			if sr, ok := delta["stop_reason"].(string); ok {
				finishReason = sr
			}
		}
		if usage, ok := obj["usage"].(map[string]interface{}); ok {
			if out, ok := usage["output_tokens"].(float64); ok {
				cTok = int64(out)
			}
		}
	}

	return deltaText, pTok, cTok, finishReason
}

// BuildOutboundRequest khởi tạo HTTP Request gửi lên Upstream phân nhánh động theo Provider
func BuildOutboundRequest(
	ctx context.Context,
	provider string,
	model string,
	creds *Credentials,
	req ChatCompletionRequest,
	isStream bool,
) (*http.Request, error) {
	switch provider {
	case "openai":
		cleanModel := strings.TrimPrefix(model, "openai/")
		upstreamURL := "https://api.openai.com/v1/chat/completions"
		payload := map[string]interface{}{
			"model":    cleanModel,
			"messages": req.Messages,
			"stream":   isStream,
		}
		if req.Request != nil {
			for k, v := range req.Request {
				if k != "messages" && k != "model" && k != "stream" && k != "contents" {
					payload[k] = v
				}
			}
		}
		outboundBody, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("lỗi encode payload OpenAI: %w", err)
		}
		fReq, err := http.NewRequestWithContext(ctx, "POST", upstreamURL, bytes.NewReader(outboundBody))
		if err != nil {
			return nil, fmt.Errorf("lỗi tạo request OpenAI: %w", err)
		}
		fReq.Header.Set("Content-Type", "application/json")
		fReq.Header.Set("Authorization", "Bearer "+creds.AccessToken)
		return fReq, nil

	case "anthropic":
		cleanModel := strings.TrimPrefix(model, "anthropic/")
		upstreamURL := "https://api.anthropic.com/v1/messages"
		systemPrompt, anthropicMsgs := ConvertMessagesToAnthropic(req.Messages)
		maxTokens := 4096
		if req.Request != nil {
			if mt, ok := req.Request["max_tokens"].(float64); ok && mt > 0 {
				maxTokens = int(mt)
			}
		}
		payload := map[string]interface{}{
			"model":      cleanModel,
			"messages":   anthropicMsgs,
			"max_tokens": maxTokens,
			"stream":     isStream,
		}
		if systemPrompt != "" {
			payload["system"] = systemPrompt
		}
		if req.Request != nil {
			for k, v := range req.Request {
				if k != "messages" && k != "model" && k != "stream" && k != "contents" && k != "system" && k != "max_tokens" {
					payload[k] = v
				}
			}
		}
		outboundBody, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("lỗi encode payload Anthropic: %w", err)
		}
		fReq, err := http.NewRequestWithContext(ctx, "POST", upstreamURL, bytes.NewReader(outboundBody))
		if err != nil {
			return nil, fmt.Errorf("lỗi tạo request Anthropic: %w", err)
		}
		fReq.Header.Set("Content-Type", "application/json")
		fReq.Header.Set("x-api-key", creds.AccessToken)
		fReq.Header.Set("anthropic-version", "2023-06-01")
		return fReq, nil

	case "google":
		cleanModel := strings.TrimPrefix(model, "google/")
		contents := ConvertMessagesToGeminiContents(req.Messages)
		if strings.HasPrefix(creds.AccessToken, "AIza") {
			upstreamURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:streamGenerateContent?alt=sse&key=%s", cleanModel, creds.AccessToken)
			payload := map[string]interface{}{
				"contents": contents,
			}
			outboundBody, err := json.Marshal(payload)
			if err != nil {
				return nil, fmt.Errorf("lỗi encode payload Google AI Studio: %w", err)
			}
			fReq, err := http.NewRequestWithContext(ctx, "POST", upstreamURL, bytes.NewReader(outboundBody))
			if err != nil {
				return nil, fmt.Errorf("lỗi tạo request Google AI Studio: %w", err)
			}
			fReq.Header.Set("Content-Type", "application/json")
			return fReq, nil
		}

		// Google Cloud Code PA
		upstreamURL := "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse"
		generatedReqID := fmt.Sprintf("agent/%s/%d/%s/1", generateUUID(), time.Now().UnixMilli(), generateUUID())
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
		if len(contents) == 0 && req.Request != nil {
			geminiReq["request"] = req.Request
		}
		outboundBody, err := json.Marshal(geminiReq)
		if err != nil {
			return nil, fmt.Errorf("lỗi encode payload Google Cloud Code: %w", err)
		}
		fReq, err := http.NewRequestWithContext(ctx, "POST", upstreamURL, bytes.NewReader(outboundBody))
		if err != nil {
			return nil, fmt.Errorf("lỗi tạo request Google Cloud Code: %w", err)
		}
		fReq.Header.Set("Content-Type", "application/json")
		fReq.Header.Set("Authorization", "Bearer "+creds.AccessToken)
		fReq.Header.Set("User-Agent", "antigravity/ide/2.11.0 darwin/arm64")
		return fReq, nil

	default: // "antigravity"
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
		if len(contents) == 0 && req.Request != nil {
			geminiReq["request"] = req.Request
		}
		outboundBody, err := json.Marshal(geminiReq)
		if err != nil {
			return nil, fmt.Errorf("lỗi encode payload Antigravity: %w", err)
		}
		fReq, err := http.NewRequestWithContext(ctx, "POST", upstreamURL, bytes.NewReader(outboundBody))
		if err != nil {
			return nil, fmt.Errorf("lỗi tạo request Antigravity: %w", err)
		}
		fReq.Header.Set("Content-Type", "application/json")
		fReq.Header.Set("Authorization", "Bearer "+creds.AccessToken)
		fReq.Header.Set("User-Agent", "antigravity/ide/2.11.0 darwin/arm64")
		return fReq, nil
	}
}

// ParseNonStreamingResponse bóc tách kết quả non-streaming từ phản hồi của upstream
func ParseNonStreamingResponse(provider string, body []byte) (string, int64, int64, error) {
	trimmed := bytes.TrimSpace(body)
	if bytes.HasPrefix(trimmed, []byte("data:")) {
		var fullText strings.Builder
		var pTok, cTok int64
		scanner := bufio.NewScanner(bytes.NewReader(trimmed))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "data:") {
				dataJSON := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if dataJSON != "" && dataJSON != "[DONE]" {
					var delta string
					var p, c int64
					switch provider {
					case "openai":
						delta, p, c, _ = ParseOpenAIChunk(dataJSON)
					case "anthropic":
						delta, p, c, _ = ParseAnthropicChunk(dataJSON)
					default:
						delta, p, c, _ = ParseGeminiChunk(dataJSON)
					}
					if p > 0 {
						pTok = p
					}
					if c > 0 {
						cTok = c
					}
					fullText.WriteString(delta)
				}
			}
		}
		return fullText.String(), pTok, cTok, nil
	}

	switch provider {
	case "openai":
		var oaiResp struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
			Usage struct {
				PromptTokens     int64 `json:"prompt_tokens"`
				CompletionTokens int64 `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(body, &oaiResp); err == nil && len(oaiResp.Choices) > 0 {
			return oaiResp.Choices[0].Message.Content, oaiResp.Usage.PromptTokens, oaiResp.Usage.CompletionTokens, nil
		}

	case "anthropic":
		var anthResp struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Usage struct {
				InputTokens  int64 `json:"input_tokens"`
				OutputTokens int64 `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(body, &anthResp); err == nil && len(anthResp.Content) > 0 {
			var sb strings.Builder
			for _, item := range anthResp.Content {
				if item.Type == "text" {
					sb.WriteString(item.Text)
				}
			}
			return sb.String(), anthResp.Usage.InputTokens, anthResp.Usage.OutputTokens, nil
		}
	}

	// Fallback Gemini non-streaming JSON:
	var gemResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokens     int64 `json:"promptTokenCount"`
			CandidatesTokens int64 `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(body, &gemResp); err == nil && len(gemResp.Candidates) > 0 {
		var sb strings.Builder
		for _, part := range gemResp.Candidates[0].Content.Parts {
			sb.WriteString(part.Text)
		}
		return sb.String(), gemResp.UsageMetadata.PromptTokens, gemResp.UsageMetadata.CandidatesTokens, nil
	}

	return string(body), 0, 0, nil
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

		// 1. Kiểm tra xác thực API Key bắt buộc (P0 Security: KHÔNG fallback sang key chủ server)
		authHeader := r.Header.Get("Authorization")
		apiKey := strings.TrimPrefix(authHeader, "Bearer ")
		apiKey = strings.TrimSpace(apiKey)
		if apiKey == "" {
			apiKey = strings.TrimSpace(r.Header.Get("x-api-key"))
		}

		if apiKey == "" {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]string{
					"message": "Thiếu API Key xác thực. Vui lòng gửi kèm header 'Authorization: Bearer <sk_api_key>' hoặc 'x-api-key'.",
					"type":    "authentication_error",
					"code":    "missing_api_key",
				},
			})
			return
		}

		var clientKeyItem *ClientKey
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
		default:
			log.Printf("[ENGINE] [OMNIROUTER] 🛑 Từ chối request vì API Key không hợp lệ: %s", MaskAPIKey(apiKey))
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]string{
					"message": "API Key không hợp lệ hoặc không tồn tại trên hệ thống",
					"type":    "authentication_error",
					"code":    "invalid_api_key",
				},
			})
			return
		}

		if clientKeyItem == nil {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]string{
					"message": "Không thể xác thực API Key",
					"type":    "authentication_error",
					"code":    "auth_failed",
				},
			})
			return
		}

		// Giới hạn kích thước body bằng http.MaxBytesReader 10MB trước khi đọc qua io.ReadAll
		r.Body = http.MaxBytesReader(w, r.Body, 10*1024*1024)
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "Dữ liệu yêu cầu vượt quá giới hạn cho phép hoặc không thể đọc",
			})
			return
		}
		defer r.Body.Close()

		var req ChatCompletionRequest
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "Dữ liệu JSON không hợp lệ",
			})
			return
		}

		if len(req.Messages) == 0 {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]string{
					"message": "Trường 'messages' không được để trống",
					"type":    "invalid_request_error",
					"code":    "missing_messages",
				},
			})
			return
		}

		// Giữ chỗ Quota nguyên tử (Atomic Quota Reservation) chống race condition khi tải đồng thời
		reserved, resMsg := ReserveClientKeyQuota(db, clientKeyItem.Key)
		if !reserved {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]string{
					"message": resMsg,
					"type":    "quota_error",
					"code":    "quota_reservation_failed",
				},
			})
			return
		}

		// P0: Quản lý vòng đời Reservation (Reserved -> Committed / Released)
		// Chỉ khi upstream AI phản hồi thành công và bắt đầu stream / trả kết quả thì mới Commit.
		// Mọi trường hợp lỗi trước đó (không có tài khoản, toàn bộ cooldown, upstream lỗi...) đều tự động hoàn lại quota.
		quotaCommitted := false
		defer func() {
			if !quotaCommitted {
				ReleaseClientKeyQuota(db, clientKeyItem.Key, 1)
			}
		}()

		// 1. Phân giải danh sách Tầng Ưu Tiên (Tier 1: Model chính -> Tier 2, Tier 3: Dự phòng)
		modelTiers := resolveCascadeModelTiers(db, req.Model)

		isStream := false
		if req.Stream != nil && *req.Stream {
			isStream = true
		}

		// 2. Lấy danh sách tài khoản AI từ RAM cache
		accounts := GetCachedActiveProviders()
		if len(accounts) == 0 {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]string{
					"message": "Không có tài khoản AI nào khả dụng trong hệ thống",
					"type":    "service_unavailable",
					"code":    "no_available_accounts",
				},
			})
			return
		}

		// Lọc tài khoản không bị cooldown
		var readyAccounts []ProviderConnection
		now := time.Now()
		var earliestCoolUntil time.Time
		for _, acc := range accounts {
			if coolUntilVal, ok := accountCooldownMap.Load(acc.ID); ok {
				if coolUntil, isTime := coolUntilVal.(time.Time); isTime && now.Before(coolUntil) {
					if earliestCoolUntil.IsZero() || coolUntil.Before(earliestCoolUntil) {
						earliestCoolUntil = coolUntil
					}
					continue
				}
			}
			readyAccounts = append(readyAccounts, acc)
		}
		if len(readyAccounts) == 0 {
			// TUYỆT ĐỐI KHÔNG gán lại readyAccounts = accounts bừa bãi!
			// Trả về lỗi 503 Service Unavailable kèm Retry-After tính theo tài khoản có cooldown sớm nhất
			retryAfterSec := 30
			if !earliestCoolUntil.IsZero() {
				diff := int(time.Until(earliestCoolUntil).Seconds())
				if diff > 0 {
					retryAfterSec = diff
				} else {
					retryAfterSec = 1
				}
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfterSec))
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]interface{}{
					"message":     fmt.Sprintf("Toàn bộ tài khoản AI upstream đang trong thời gian hồi phục (Cooldown). Vui lòng thử lại sau %d giây.", retryAfterSec),
					"type":        "service_unavailable",
					"code":        "all_accounts_in_cooldown",
					"retry_after": retryAfterSec,
				},
			})
			return
		}

		numAccounts := len(readyAccounts)
		startIdx := int(atomic.AddUint64(&rrCounter, 1) % uint64(numAccounts))

		var lastErr error
		var resp *http.Response
		var usedAccount *ProviderConnection
		var usedModel string
		var usedProvider string
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

			orderedAccounts := orderAccountsForChat(readyAccounts, currentTierModel, startIdx)
			attemptLimit := maxAttempts
			if attemptLimit > len(orderedAccounts) {
				attemptLimit = len(orderedAccounts)
			}

			for attempt := 0; attempt < attemptLimit; attempt++ {
				acc := orderedAccounts[attempt]
				provider := DetermineProvider(&acc, currentTierModel)

				c, cErr := GetValidCredentials(db, &acc)
				if cErr != nil {
					lastErr = cErr
					continue
				}

				fReq, reqErr := BuildOutboundRequest(r.Context(), provider, currentTierModel, c, req, isStream)
				if reqErr != nil {
					lastErr = reqErr
					continue
				}

				targetClient := chatHTTPClient
				if c.ConnectionProxyEnabled && c.ProxyUrl != "" && isProxyHealthy(c.ProxyUrl) {
					targetClient = getClientForAccount(c.ProxyUrl)
				}

				rResp, doErr := targetClient.Do(fReq)
				if doErr != nil {
					if c.ConnectionProxyEnabled && c.ProxyUrl != "" {
						markProxyFailure(c.ProxyUrl, doErr.Error())
					}
					lastErr = doErr
					continue
				}

				if rResp.StatusCode == http.StatusTooManyRequests {
					rResp.Body.Close()
					accountCooldownMap.Store(acc.ID, time.Now().Add(30*time.Second))
					lastErr = fmt.Errorf("rate limited (429 Quota) trên %s (%s) cho model %s", acc.Name, provider, currentTierModel)
					continue
				}

				if rResp.StatusCode != http.StatusOK {
					errBody, _ := io.ReadAll(io.LimitReader(rResp.Body, 1024))
					rResp.Body.Close()
					lastErr = fmt.Errorf("upstream error %d trên %s (%s) cho model %s: %s", rResp.StatusCode, acc.Name, provider, currentTierModel, strings.TrimSpace(string(errBody)))
					continue
				}

				// Tầng hiện tại đã phục vụ thành công!
				resp = rResp
				usedAccount = &acc
				usedModel = currentTierModel
				usedProvider = provider
				tierSucceeded = true
				break
			}

			if tierSucceeded {
				if tierIdx > 0 {
					log.Printf("[ENGINE] [CASCADE] ⚡ Model chính gặp sự cố, đã chuyển mạch sang %s (Provider: %s) thành công qua tài khoản %s", tierLabel, usedProvider, usedAccount.Name)
				} else {
					log.Printf("[ENGINE] [CASCADE] ✔ Model chính %s (Provider: %s) phản hồi thành công qua tài khoản %s", tierLabel, usedProvider, usedAccount.Name)
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
			quotaCommitted = true // Upstream stream bắt đầu thành công -> Xác nhận trừ Quota

			flusher, isFlusher := w.(http.Flusher)
			if isFlusher {
				flusher.Flush() // Fast-Flush ngay lập tức gửi header SSE xuống client
			}
			reader := bufio.NewReader(resp.Body)

			for {
				line, readErr := reader.ReadBytes('\n')
				if len(line) > 0 {
					lineStr := strings.TrimSpace(string(line))
					if strings.HasPrefix(lineStr, "data:") {
						dataJSON := strings.TrimSpace(strings.TrimPrefix(lineStr, "data:"))
						if dataJSON != "" && dataJSON != "[DONE]" {
							var deltaText string
							var pTok, cTok int64

							switch usedProvider {
							case "openai":
								deltaText, pTok, cTok, _ = ParseOpenAIChunk(dataJSON)
							case "anthropic":
								deltaText, pTok, cTok, _ = ParseAnthropicChunk(dataJSON)
							default:
								deltaText, pTok, cTok, _ = ParseGeminiChunk(dataJSON)
							}

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
			accName := "Tài khoản Antigravity"
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
			// Non-streaming: Gom toàn bộ phản hồi từ các chunk SSE hoặc body JSON trả về JSON chuẩn OpenAI
			respBytes, rErr := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024))
			if rErr != nil {
				log.Printf("[ENGINE] [OMNIROUTER] ⚠ Lỗi khi đọc dữ liệu phản hồi từ upstream: %v", rErr)
			}
			fullContentText, pTok, cTok, parseErr := ParseNonStreamingResponse(usedProvider, respBytes)
			if parseErr != nil {
				log.Printf("[ENGINE] [OMNIROUTER] ⚠ Lỗi bóc tách response non-streaming: %v", parseErr)
			}
			if pTok > 0 {
				promptTokens = pTok
			}
			if cTok > 0 {
				completionTokens = cTok
			}

			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			attachRateLimitHeaders(w, clientKeyItem)
			w.WriteHeader(http.StatusOK)
			quotaCommitted = true // Upstream stream bắt đầu thành công -> Xác nhận trừ Quota
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":      chatCmplID,
				"object":  "chat.completion",
				"created": createdTimestamp,
				"model":   usedModel,
				"choices": []map[string]interface{}{
					{
						"index": 0,
						"message": map[string]string{
							"role":    "assistant",
							"content": fullContentText,
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
			accName := "Tài khoản Antigravity"
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
			log.Printf("[ENGINE] [OMNIROUTER] ✔ Chat hoàn thành qua %s (%s, Model: %s)", usedAccount.Name, usedProvider, usedModel)
			keyName := "Direct API"
			if clientKeyItem != nil {
				keyName = clientKeyItem.Name
			}
			GlobalLogHub.RecordLog(RouterLogEntry{
				ClientDevice:     r.RemoteAddr,
				APIKeyName:       keyName,
				Endpoint:         "/v1/chat/completions",
				Method:           "POST",
				Provider:         usedProvider,
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
