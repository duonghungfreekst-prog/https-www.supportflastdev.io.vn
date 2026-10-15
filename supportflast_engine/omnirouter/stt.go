package omnirouter

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type STTModelInfo struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Name     string `json:"name"`
}

var availableSTTModels = []STTModelInfo{
	{ID: "gemini/gemini-2.5-flash", Provider: "gemini", Name: "Gemini 2.5 Flash STT"},
	{ID: "gemini/gemini-2.5-pro", Provider: "gemini", Name: "Gemini 2.5 Pro STT"},
	{ID: "openai/whisper-1", Provider: "openai", Name: "Whisper-1"},
	{ID: "groq/whisper-large-v3", Provider: "groq", Name: "Groq Whisper Large v3"},
}

// HandleSTTModels danh sách các mô hình Speech-to-Text khả dụng
func HandleSTTModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "list",
		"data":   availableSTTModels,
	})
}

// HandleAudioTranscriptions chuyển đổi âm thanh thành văn bản chuẩn OpenAI /v1/audio/transcriptions
func HandleAudioTranscriptions(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		// Giới hạn file 30MB
		if err := r.ParseMultipartForm(30 * 1024 * 1024); err != nil {
			http.Error(w, `{"error":"File upload quá lớn hoặc không hợp lệ"}`, http.StatusBadRequest)
			return
		}

		file, handler, err := r.FormFile("file")
		if err != nil {
			http.Error(w, `{"error":"Thiếu file âm thanh trong trường 'file'"}`, http.StatusBadRequest)
			return
		}
		defer file.Close()

		audioBytes, err := io.ReadAll(file)
		if err != nil {
			http.Error(w, `{"error":"Không thể đọc dữ liệu âm thanh"}`, http.StatusBadRequest)
			return
		}

		mimeType := "audio/mp3"
		fn := strings.ToLower(handler.Filename)
		if strings.HasSuffix(fn, ".wav") {
			mimeType = "audio/wav"
		} else if strings.HasSuffix(fn, ".ogg") {
			mimeType = "audio/ogg"
		} else if strings.HasSuffix(fn, ".m4a") {
			mimeType = "audio/m4a"
		} else if strings.HasSuffix(fn, ".webm") {
			mimeType = "audio/webm"
		}

		base64Audio := base64.StdEncoding.EncodeToString(audioBytes)

		// Lấy tài khoản Gemini/Antigravity
		accounts := GetCachedActiveProviders()
		if len(accounts) == 0 {
			http.Error(w, `{"error":"Không có tài khoản AI nào khả dụng"}`, http.StatusServiceUnavailable)
			return
		}

		var usedAcc *ProviderConnection
		for _, a := range accounts {
			if a.Provider == "antigravity" || a.Provider == "google" {
				usedAcc = &a
				break
			}
		}
		if usedAcc == nil {
			usedAcc = &accounts[0]
		}

		creds, err := GetValidCredentials(db, usedAcc)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Lỗi xác thực: %v"}`, err), http.StatusBadGateway)
			return
		}

		upstreamURL := "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse"
		geminiReq := map[string]interface{}{
			"model":     "gemini-2.5-flash",
			"userAgent": "antigravity",
			"requestId": fmt.Sprintf("agent/%s/%d/%s/1", generateUUID(), time.Now().UnixMilli(), generateUUID()),
			"request": map[string]interface{}{
				"contents": []map[string]interface{}{
					{
						"role": "user",
						"parts": []map[string]interface{}{
							{
								"inlineData": map[string]string{
									"mimeType": mimeType,
									"data":     base64Audio,
								},
							},
							{
								"text": "Transcribe the audio accurately verbatim. Output only the transcribed text without explanations or headers.",
							},
						},
					},
				},
			},
		}
		if creds.ProjectId != "" && creds.ProjectId != "aicode-consumers" {
			geminiReq["project"] = creds.ProjectId
		}

		reqBody, _ := json.Marshal(geminiReq)
		fReq, _ := http.NewRequestWithContext(r.Context(), "POST", upstreamURL, bytes.NewReader(reqBody))
		fReq.Header.Set("Content-Type", "application/json")
		fReq.Header.Set("Authorization", "Bearer "+creds.AccessToken)
		fReq.Header.Set("User-Agent", "antigravity/ide/2.11.0 darwin/arm64")

		resp, err := chatHTTPClient.Do(fReq)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Lỗi kết nối upstream STT: %v"}`, err), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			http.Error(w, fmt.Sprintf(`{"error":"Upstream STT trả về lỗi %d: %s"}`, resp.StatusCode, string(b)), http.StatusBadGateway)
			return
		}

		var fullText strings.Builder
		buf := make([]byte, 4096)
		for {
			n, rErr := resp.Body.Read(buf)
			if n > 0 {
				lines := strings.Split(string(buf[:n]), "\n")
				for _, line := range lines {
					lineStr := strings.TrimSpace(line)
					if strings.HasPrefix(lineStr, "data:") {
						d := strings.TrimSpace(strings.TrimPrefix(lineStr, "data:"))
						if d != "" && d != "[DONE]" {
							txt, _, _, _ := ParseGeminiChunk(d)
							fullText.WriteString(txt)
						}
					}
				}
			}
			if rErr != nil {
				break
			}
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"text": strings.TrimSpace(fullText.String()),
		})
	}
}
