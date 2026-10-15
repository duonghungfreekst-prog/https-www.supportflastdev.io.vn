package omnirouter

import (
	"context"
	"database/sql"
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
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	ProjectId    string    `json:"projectId"`
	ExpiresAt    time.Time `json:"-"`
	RawExpiresAt string    `json:"expiresAt"`
}

// ParseCredentialsFromMap trích xuất thông tin xác thực từ map data
func ParseCredentialsFromMap(dataMap map[string]interface{}) *Credentials {
	creds := &Credentials{}
	if tok, ok := dataMap["accessToken"].(string); ok {
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
