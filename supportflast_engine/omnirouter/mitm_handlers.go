package omnirouter

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"
)

// HandleMitmAliases xử lý GET/POST cho model aliases (/api/cli-tools/antigravity-mitm/alias)
func HandleMitmAliases(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")

		switch r.Method {
		case http.MethodGet:
			val, err := GetOmniKV(db, "mitm_alias", "mappings")
			var mappings map[string]interface{}
			if err == nil && val != "" {
				_ = json.Unmarshal([]byte(val), &mappings)
			}
			if mappings == nil {
				mappings = map[string]interface{}{
					"antigravity": map[string]string{
						"gemini-3.1-pro-high": "gemini-3.1-pro-high",
						"gemini-2.5-pro":      "gemini-2.5-pro",
						"claude-3-5-sonnet":   "claude-3-5-sonnet",
					},
					"copilot": map[string]string{
						"gpt-4o": "gemini-3.1-pro-high",
					},
					"claude-code": map[string]string{
						"claude-3-5-sonnet": "claude-3-5-sonnet",
					},
				}
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success":  true,
				"mappings": mappings,
			})

		case http.MethodPost:
			var body struct {
				Tool  string `json:"tool"`
				Alias string `json:"alias"`
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"Invalid JSON body"}`, http.StatusBadRequest)
				return
			}
			val, _ := GetOmniKV(db, "mitm_alias", "mappings")
			var mappings map[string]map[string]string
			if val != "" {
				_ = json.Unmarshal([]byte(val), &mappings)
			}
			if mappings == nil {
				mappings = make(map[string]map[string]string)
			}
			if mappings[body.Tool] == nil {
				mappings[body.Tool] = make(map[string]string)
			}
			if body.Model == "" {
				delete(mappings[body.Tool], body.Alias)
			} else {
				mappings[body.Tool][body.Alias] = body.Model
			}

			bytes, _ := json.Marshal(mappings)
			_ = SetOmniKV(db, "mitm_alias", "mappings", string(bytes))

			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"message": "Đã lưu ánh xạ mô hình thành công",
			})

		default:
			http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		}
	}
}

// HandleMitmStatus trả về thông tin trạng thái CLI tools & MITM Gateway
func HandleMitmStatus(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		activeProviders := GetCachedActiveProviders()

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":         true,
			"gateway_status":  "online",
			"cloud_provider":  "Render + TiDB Serverless",
			"version":         "v2.8.5-production",
			"active_accounts": len(activeProviders),
			"dns_redirection": true,
			"timestamp":       time.Now().Format(time.RFC3339),
			"supported_tools": []string{
				"antigravity-mitm",
				"claude-code",
				"codex-cli",
				"openclaw",
				"droid-agent",
				"hermes-cli",
				"opencode",
			},
		})
	}
}
