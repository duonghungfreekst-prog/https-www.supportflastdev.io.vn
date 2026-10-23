package omnirouter

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	AntigravityClientID     = "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com"
	AntigravityClientSecret = "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"
	GoogleTokenURL          = "https://oauth2.googleapis.com/token"
)

var (
	accountTokenMu sync.Map // connId -> *sync.Mutex
	httpClient     = &http.Client{
		Timeout: 20 * time.Second,
	}
)

func getAccountMutex(connId string) *sync.Mutex {
	val, _ := accountTokenMu.LoadOrStore(connId, &sync.Mutex{})
	return val.(*sync.Mutex)
}

// Credentials chứa token và project ID của account
type Credentials struct {
	AccessToken            string    `json:"accessToken"`
	RefreshToken           string    `json:"refreshToken"`
	ProjectId              string    `json:"projectId"`
	ExpiresAt              time.Time `json:"-"`
	RawExpiresAt           string    `json:"expiresAt"`
	ProxyUrl               string    `json:"proxyUrl,omitempty"`
	ConnectionProxyEnabled bool      `json:"connectionProxyEnabled,omitempty"`
}

// ParseCredentialsFromMap trích xuất thông tin xác thực từ map data
func ParseCredentialsFromMap(dataMap map[string]interface{}) *Credentials {
	creds := &Credentials{}
	if tok, ok := dataMap["accessToken"].(string); ok && tok != "" {
		creds.AccessToken = tok
	} else if tok, ok := dataMap["apiKey"].(string); ok && tok != "" {
		creds.AccessToken = tok
	} else if tok, ok := dataMap["api_key"].(string); ok && tok != "" {
		creds.AccessToken = tok
	} else if tok, ok := dataMap["token"].(string); ok && tok != "" {
		creds.AccessToken = tok
	}
	if ref, ok := dataMap["refreshToken"].(string); ok {
		creds.RefreshToken = ref
	}
	if p, ok := dataMap["projectId"].(string); ok {
		creds.ProjectId = p
	}
	if expStr, ok := dataMap["expiresAt"].(string); ok && expStr != "" {
		creds.RawExpiresAt = expStr
		if t, err := time.Parse(time.RFC3339, expStr); err == nil {
			creds.ExpiresAt = t
		}
	}
	if en, ok := dataMap["connectionProxyEnabled"].(bool); ok && en {
		if pUrl, ok := dataMap["connectionProxyUrl"].(string); ok && pUrl != "" {
			creds.ProxyUrl = pUrl
		} else if pUrl, ok := dataMap["proxyUrl"].(string); ok && pUrl != "" {
			creds.ProxyUrl = pUrl
		} else if pUrl, ok := dataMap["proxy"].(string); ok && pUrl != "" {
			creds.ProxyUrl = pUrl
		}
		// Loại bỏ triệt để proxy chết cũ
		if strings.Contains(creds.ProxyUrl, "36.50.175.113") || strings.Contains(creds.ProxyUrl, "58.186.63.192") {
			creds.ProxyUrl = ""
			creds.ConnectionProxyEnabled = false
		} else if creds.ProxyUrl != "" {
			creds.ConnectionProxyEnabled = true
		}
	} else {
		creds.ConnectionProxyEnabled = false
		creds.ProxyUrl = ""
	}
	return creds
}

// RefreshGoogleToken thực hiện gọi sang Google OAuth API để lấy Access Token mới
func RefreshGoogleToken(refreshToken string) (string, time.Time, string, error) {
	data := url.Values{}
	data.Set("client_id", AntigravityClientID)
	data.Set("client_secret", AntigravityClientSecret)
	data.Set("refresh_token", refreshToken)
	data.Set("grant_type", "refresh_token")

	req, err := http.NewRequest("POST", GoogleTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", time.Time{}, "", fmt.Errorf("tạo request refresh token thất bại: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", time.Time{}, "", fmt.Errorf("lỗi mạng khi gọi Google OAuth: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", time.Time{}, "", fmt.Errorf("đọc phản hồi Google OAuth thất bại: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, "", fmt.Errorf("Google OAuth trả về mã lỗi HTTP %d: %s", resp.StatusCode, string(body))
	}

	var res struct {
		AccessToken  string `json:"access_token"`
		ExpiresIn    int    `json:"expires_in"`
		RefreshToken string `json:"refresh_token,omitempty"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return "", time.Time{}, "", fmt.Errorf("parse JSON phản hồi Google OAuth thất bại: %w", err)
	}

	expiresIn := res.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	newExpiry := time.Now().Add(time.Duration(expiresIn) * time.Second)

	return res.AccessToken, newExpiry, res.RefreshToken, nil
}

// GetValidCredentials lấy access token còn hạn của account, tự động refresh nếu sắp hết hạn
func GetValidCredentials(db *sql.DB, conn *ProviderConnection) (*Credentials, error) {
	var dataMap map[string]interface{}
	if err := json.Unmarshal(conn.Data, &dataMap); err != nil {
		return nil, fmt.Errorf("không thể đọc data tài khoản %s: %w", conn.Name, err)
	}

	creds := ParseCredentialsFromMap(dataMap)
	now := time.Now()

	// Nếu accessToken còn hạn hơn 3 phút thì dùng tiếp ngay lập tức
	if creds.AccessToken != "" && !creds.ExpiresAt.IsZero() && creds.ExpiresAt.After(now.Add(3*time.Minute)) {
		return creds, nil
	}

	accMu := getAccountMutex(conn.ID)
	accMu.Lock()
	defer accMu.Unlock()

	// Kiểm tra lại sau khi lock
	if creds.RefreshToken == "" {
		if creds.AccessToken != "" {
			return creds, nil
		}
		return nil, fmt.Errorf("tài khoản %s thiếu refresh token", conn.Name)
	}

	log.Printf("[ENGINE] [OMNIROUTER] 🔄 Tự động làm mới OAuth Token cho tài khoản: %s", conn.Name)
	newTok, newExpiry, newRefresh, err := RefreshGoogleToken(creds.RefreshToken)
	if err != nil {
		log.Printf("[ENGINE] [OMNIROUTER] ✗ Lỗi refresh token tài khoản %s: %v", conn.Name, err)
		if creds.AccessToken != "" {
			return creds, nil
		}
		return nil, err
	}

	creds.AccessToken = newTok
	creds.ExpiresAt = newExpiry
	creds.RawExpiresAt = newExpiry.Format(time.RFC3339)

	dataMap["accessToken"] = newTok
	dataMap["expiresAt"] = creds.RawExpiresAt
	dataMap["lastRefreshAt"] = NowISO()
	if newRefresh != "" {
		dataMap["refreshToken"] = newRefresh
		creds.RefreshToken = newRefresh
	}

	if err := UpdateProviderData(db, conn.ID, dataMap); err != nil {
		log.Printf("[ENGINE] [OMNIROUTER] [WARN] Lỗi cập nhật token mới vào DB: %v", err)
	}

	log.Printf("[ENGINE] [OMNIROUTER] ✔ Đã làm mới token thành công cho tài khoản: %s (Hết hạn lúc: %s)", conn.Name, creds.RawExpiresAt)
	return creds, nil
}

// StartAutoTokenRefreshWorker chạy ngầm chu kỳ kiểm tra và làm mới token định kỳ
func StartAutoTokenRefreshWorker(ctx context.Context, db *sql.DB) {
	ticker := time.NewTicker(5 * time.Minute)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				providers := GetCachedActiveProviders()
				for _, p := range providers {
					if p.Provider == "antigravity" || p.Provider == "google" {
						_, _ = GetValidCredentials(db, &p)
					}
				}
			}
		}
	}()
}

// extractProviderFromOAuthPath trích xuất provider từ đường dẫn URL (/api/oauth/:provider/authorize, /api/router/oauth/:provider/exchange...)
func extractProviderFromOAuthPath(path string) string {
	clean := strings.Trim(path, "/")
	parts := strings.Split(clean, "/")
	for i := 0; i < len(parts); i++ {
		if parts[i] == "oauth" && i+1 < len(parts) {
			candidate := parts[i+1]
			if candidate != "authorize" && candidate != "exchange" {
				return strings.ToLower(candidate)
			}
		}
	}
	return ""
}

// HandleOAuthAuthorize khởi tạo phiên đăng nhập OAuth 2.0 PKCE cho Provider
func HandleOAuthAuthorize(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, x-api-key, x-9r-cli-token")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		provider := extractProviderFromOAuthPath(r.URL.Path)
		if provider == "" {
			provider = strings.ToLower(strings.TrimSpace(r.URL.Query().Get("provider")))
		}
		if provider == "" {
			provider = "antigravity"
		}

		redirectURI := r.URL.Query().Get("redirect_uri")
		if redirectURI == "" {
			redirectURI = "http://localhost:20128/callback"
		}

		stateBytes := make([]byte, 16)
		_, _ = rand.Read(stateBytes)
		state := hex.EncodeToString(stateBytes)
		if qState := r.URL.Query().Get("state"); qState != "" {
			state = qState
		}

		switch provider {
		case "antigravity", "google", "gemini-cli":
			scopes := "openid https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/cloud-platform"
			authURL := fmt.Sprintf("https://accounts.google.com/o/oauth2/v2/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=%s&access_type=offline&prompt=consent&state=%s",
				AntigravityClientID,
				url.QueryEscape(redirectURI),
				url.QueryEscape(scopes),
				state,
			)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success":     true,
				"authUrl":     authURL,
				"provider":    provider,
				"state":       state,
				"redirectUri": redirectURI,
			})
			return

		case "copilot", "github":
			authURL := fmt.Sprintf("https://github.com/login/oauth/authorize?client_id=Iv1.b507a08c87ecfe81&scope=%s&state=%s",
				url.QueryEscape("read:user copilot"),
				state,
			)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success":     true,
				"authUrl":     authURL,
				"provider":    provider,
				"state":       state,
				"redirectUri": redirectURI,
			})
			return

		default:
			// Với các nhà cung cấp sử dụng API Key
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success":        true,
				"requiresApiKey": true,
				"provider":       provider,
				"error":          fmt.Sprintf("Nhà cung cấp [%s] xác thực bằng API Key.", strings.ToUpper(provider)),
			})
			return
		}
	}
}

// HandleOAuthExchange đổi OAuth code lấy access/refresh token và lưu vào CSDL
func HandleOAuthExchange(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
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

		provider := extractProviderFromOAuthPath(r.URL.Path)
		if provider == "" {
			provider = strings.ToLower(strings.TrimSpace(r.URL.Query().Get("provider")))
		}

		var req struct {
			Code         string `json:"code"`
			RedirectURI  string `json:"redirectUri"`
			CodeVerifier string `json:"codeVerifier"`
			State        string `json:"state"`
			Provider     string `json:"provider"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"Dữ liệu JSON không hợp lệ"}`, http.StatusBadRequest)
			return
		}

		if provider == "" && req.Provider != "" {
			provider = strings.ToLower(strings.TrimSpace(req.Provider))
		}
		if provider == "" {
			provider = "antigravity"
		}

		code := strings.TrimSpace(req.Code)
		if code == "" {
			http.Error(w, `{"error":"Thiếu mã xác thực OAuth code"}`, http.StatusBadRequest)
			return
		}

		redirectURI := strings.TrimSpace(req.RedirectURI)
		if redirectURI == "" {
			redirectURI = "http://localhost:20128/callback"
		}

		switch provider {
		case "antigravity", "google", "gemini-cli":
			conn, err := exchangeGoogleOAuthCode(db, provider, code, redirectURI, req.CodeVerifier)
			if err != nil {
				log.Printf("[ENGINE] [OAUTH] ✗ Lỗi đổi mã OAuth Google (%s): %v", provider, err)
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"error": fmt.Sprintf("Xác thực Google OAuth thất bại: %v", err),
				})
				return
			}

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"ok":      true,
				"success": true,
				"connection": map[string]interface{}{
					"id":          conn.ID,
					"provider":    conn.Provider,
					"name":        conn.Name,
					"email":       conn.Email,
					"displayName": conn.Name,
				},
			})
			return

		default:
			http.Error(w, fmt.Sprintf(`{"error":"Nhà cung cấp [%s] không hỗ trợ đổi mã OAuth code trực tiếp."}`, provider), http.StatusBadRequest)
			return
		}
	}
}

// HandleOAuth điều phối các endpoint OAuth (/api/oauth/ và /api/router/oauth/)
func HandleOAuth(db *sql.DB) http.HandlerFunc {
	authHandler := HandleOAuthAuthorize(db)
	exchHandler := HandleOAuthExchange(db)

	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasSuffix(path, "/authorize") {
			authHandler(w, r)
			return
		}
		if strings.HasSuffix(path, "/exchange") {
			exchHandler(w, r)
			return
		}

		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, x-api-key, x-9r-cli-token")
			w.WriteHeader(http.StatusOK)
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Không tìm thấy endpoint OAuth tương ứng",
			"path":  path,
		})
	}
}

// exchangeGoogleOAuthCode thực hiện gọi Google Token API để đổi code lấy tokens
func exchangeGoogleOAuthCode(db *sql.DB, provider, code, redirectURI, codeVerifier string) (*ProviderConnection, error) {
	data := url.Values{}
	data.Set("client_id", AntigravityClientID)
	data.Set("client_secret", AntigravityClientSecret)
	data.Set("code", code)
	data.Set("grant_type", "authorization_code")
	data.Set("redirect_uri", redirectURI)
	if codeVerifier != "" {
		data.Set("code_verifier", codeVerifier)
	}

	req, err := http.NewRequest("POST", GoogleTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("tạo request đổi token thất bại: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lỗi kết nối tới máy chủ Google OAuth: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("đọc phản hồi Google OAuth thất bại: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errObj struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		_ = json.Unmarshal(bodyBytes, &errObj)
		errMsg := errObj.ErrorDescription
		if errMsg == "" {
			errMsg = errObj.Error
		}
		if errMsg == "" {
			errMsg = string(bodyBytes)
		}
		return nil, fmt.Errorf("Google phản hồi HTTP %d: %s", resp.StatusCode, errMsg)
	}

	var res struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		IdToken      string `json:"id_token"`
		TokenType    string `json:"token_type"`
	}
	if err := json.Unmarshal(bodyBytes, &res); err != nil {
		return nil, fmt.Errorf("phân tích JSON token thất bại: %w", err)
	}

	if res.AccessToken == "" {
		return nil, fmt.Errorf("Google không trả về accessToken")
	}

	email := extractEmailFromIDToken(res.IdToken)
	if email == "" {
		email = fetchGoogleUserEmail(res.AccessToken)
	}
	if email == "" {
		email = fmt.Sprintf("antigravity_%d@google.com", time.Now().Unix()%100000)
	}

	expiresIn := res.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	expiresAt := time.Now().Add(time.Duration(expiresIn) * time.Second)

	connID := fmt.Sprintf("acc_%s_%d", provider, time.Now().UnixNano()%10000000)
	name := fmt.Sprintf("%s (%s)", email, strings.ToUpper(provider))

	dataMap := map[string]interface{}{
		"accessToken":  res.AccessToken,
		"refreshToken": res.RefreshToken,
		"token":        res.AccessToken,
		"expiresAt":    expiresAt.Format(time.RFC3339),
		"email":        email,
		"lastRefresh":  NowISO(),
	}
	dataBytes, _ := json.Marshal(dataMap)

	now := NowISO()
	newConn := ProviderConnection{
		ID:        connID,
		Provider:  provider,
		AuthType:  "oauth",
		Name:      name,
		Email:     email,
		Priority:  1,
		IsActive:  1,
		Data:      dataBytes,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := SaveProvider(db, &newConn); err != nil {
		return nil, fmt.Errorf("lưu tài khoản vào CSDL thất bại: %w", err)
	}

	log.Printf("[ENGINE] [OAUTH] ✔ Đã kết nối thành công tài khoản Google Antigravity: %s (%s)", email, connID)
	return &newConn, nil
}

// extractEmailFromIDToken trích xuất trường email từ JWT ID Token của Google
func extractEmailFromIDToken(idToken string) string {
	if idToken == "" {
		return ""
	}
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return ""
	}
	payloadB64 := parts[1]
	switch len(payloadB64) % 4 {
	case 2:
		payloadB64 += "=="
	case 3:
		payloadB64 += "="
	}
	decoded, err := base64.URLEncoding.DecodeString(payloadB64)
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := json.Unmarshal(decoded, &claims); err == nil && claims.Email != "" {
		return claims.Email
	}
	return ""
}

// fetchGoogleUserEmail gọi Google UserInfo API dự phòng khi ID Token không có email
func fetchGoogleUserEmail(accessToken string) string {
	req, err := http.NewRequest("GET", "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var info struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err == nil {
		return info.Email
	}
	return ""
}

// HandleOAuthCallbackHTML phục vụ trang callback HTML trả về khi hoàn tất đăng nhập Google OAuth
func HandleOAuthCallbackHTML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	html := `<!DOCTYPE html>
<html lang="vi">
<head>
  <meta charset="utf-8">
  <title>OAuth Callback - ModelRouter9</title>
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <style>
    body {
      background: #090d16;
      color: #e2e8f0;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      display: flex;
      align-items: center;
      justify-content: center;
      height: 100vh;
      margin: 0;
      padding: 16px;
      box-sizing: border-box;
    }
    .card {
      background: rgba(15, 23, 42, 0.85);
      border: 1px solid rgba(6, 182, 212, 0.4);
      border-radius: 20px;
      padding: 32px 24px;
      text-align: center;
      max-width: 420px;
      width: 100%;
      box-shadow: 0 0 50px rgba(6, 182, 212, 0.25);
    }
    .spinner {
      width: 48px;
      height: 48px;
      border: 3px solid rgba(6, 182, 212, 0.2);
      border-top-color: #06b6d4;
      border-radius: 50%;
      animation: spin 0.8s linear infinite;
      margin: 0 auto 20px;
    }
    @keyframes spin { to { transform: rotate(360deg); } }
    h2 { margin: 0 0 8px; font-size: 18px; color: #fff; }
    p { margin: 0; font-size: 13px; color: #94a3b8; line-height: 1.5; }
    .status { margin-top: 16px; font-size: 11px; font-family: monospace; color: #22d3ee; }
  </style>
</head>
<body>
  <div class="card">
    <div class="spinner"></div>
    <h2>Xác thực tài khoản thành công!</h2>
    <p>Đang hoàn tất đồng bộ kết nối vào ModelRouter9. Cửa sổ này sẽ tự động đóng lại trong giây lát...</p>
    <div class="status" id="status-text">Đang phát tín hiệu callback...</div>
  </div>
  <script>
    const params = new URLSearchParams(window.location.search);
    const code = params.get('code');
    const state = params.get('state');
    const token = params.get('token');
    const error = params.get('error');
    const payload = { code, state, token, error };

    if (window.opener) {
      try {
        window.opener.postMessage({ type: 'oauth_callback', data: payload }, '*');
      } catch (e) {}
    }

    try {
      const bc = new BroadcastChannel('oauth_callback');
      bc.postMessage(payload);
    } catch (e) {}

    try {
      localStorage.setItem('oauth_callback', JSON.stringify(payload));
    } catch (e) {}

    const statusEl = document.getElementById('status-text');
    if (statusEl) statusEl.textContent = '✔ Đã gửi tín hiệu callback thành công!';

    setTimeout(() => {
      try { window.close(); } catch (_) {}
    }, 1200);
  </script>
</body>
</html>`
	_, _ = w.Write([]byte(html))
}
