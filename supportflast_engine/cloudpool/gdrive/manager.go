package gdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"supportflast_engine/cloudpool/core"
	"supportflast_engine/cloudpool/models"
	"supportflast_engine/cloudpool/storage"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

const (
	CloudPoolFolderName        = ".cloudpool_storage_data"
	DriveScopeFull             = "https://www.googleapis.com/auth/drive"
	DefaultDataDir             = `f:\supportflast.dev\data`
	DefaultOAuthDir            = `f:\supportflast.dev\data\oauth`
	DefaultServiceAccountsDir  = `f:\supportflast.dev\data\service_accounts`
	DefaultOAuthCredentialsFile = `f:\supportflast.dev\data\oauth\credentials.json`

	// CÃ¡c chiáº¿n lÆ°á»£c phÃ¢n bá»• vÃ  Ä‘iá»u phá»‘i tÃ i khoáº£n Google Drive
	StrategyLeastUsed   = "least_used"
	StrategyWaterfill   = "waterfill"
	StrategyRoundRobin  = "round_robin"
	StrategyRoundRobin2 = "round-robin" // Há»— trá»£ Ä‘á»‹nh dáº¡ng hyphen
)

type Manager struct {
	db                 *storage.DB
	services           map[string]*drive.Service
	mu                 sync.RWMutex
	rrIndex            int
	dataDir            string
	oauthDir           string
	serviceAccountsDir string
}

func NewManager(db *storage.DB) *Manager {
	dataDir := strings.TrimSpace(os.Getenv("DATA_DIR"))
	if dataDir == "" {
		dataDir = strings.TrimSpace(os.Getenv("SUPPORTFLAST_DATA_DIR"))
	}
	if dataDir == "" {
		dataDir = strings.TrimSpace(os.Getenv("CLOUDPool_DATA_DIR"))
	}
	if dataDir == "" {
		dataDir = DefaultDataDir
	}

	oauthDir := filepath.Join(dataDir, "oauth")
	saDir := filepath.Join(dataDir, "service_accounts")

	m := &Manager{
		db:                 db,
		services:           make(map[string]*drive.Service),
		dataDir:            dataDir,
		oauthDir:           oauthDir,
		serviceAccountsDir: saDir,
	}

	// Äáº£m báº£o cÃ¡c thÆ° má»¥c chá»©ng chá»‰ luÃ´n tá»“n táº¡i sáºµn sÃ ng
	_ = m.EnsureDirectories()

	return m
}

// EnsureDirectories tá»± Ä‘á»™ng táº¡o cÃ¡c thÆ° má»¥c chá»©ng chá»‰ OAuth vÃ  Service Account táº¡i thÆ° má»¥c data
func (m *Manager) EnsureDirectories() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	dirs := []string{m.dataDir, m.oauthDir, m.serviceAccountsDir}
	for _, d := range dirs {
		if d == "" {
			continue
		}
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("khÃ´ng thá»ƒ táº¡o thÆ° má»¥c '%s': %w", d, err)
		}
	}
	return nil
}

// GetDataDir tráº£ vá» Ä‘Æ°á»ng dáº«n thÆ° má»¥c data gá»‘c
func (m *Manager) GetDataDir() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.dataDir
}

// GetOAuthDir tráº£ vá» Ä‘Æ°á»ng dáº«n thÆ° má»¥c chá»©ng chá»‰ OAuth
func (m *Manager) GetOAuthDir() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.oauthDir
}

// GetServiceAccountsDir tráº£ vá» Ä‘Æ°á»ng dáº«n thÆ° má»¥c chá»©ng chá»‰ Service Account
func (m *Manager) GetServiceAccountsDir() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.serviceAccountsDir
}

// SetDataDir cho phÃ©p cáº­p nháº­t thÆ° má»¥c dá»¯ liá»‡u vÃ  tá»± Ä‘á»™ng Ä‘iá»u chá»‰nh thÆ° má»¥c OAuth/SA
func (m *Manager) SetDataDir(dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dataDir = dir
	m.oauthDir = filepath.Join(dir, "oauth")
	m.serviceAccountsDir = filepath.Join(dir, "service_accounts")
	_ = os.MkdirAll(m.dataDir, 0755)
	_ = os.MkdirAll(m.oauthDir, 0755)
	_ = os.MkdirAll(m.serviceAccountsDir, 0755)
}

// GoogleOAuthClientJSON Ä‘áº¡i diá»‡n cho file credentials client JSON táº£i tá»« Google Cloud Console
type GoogleOAuthClientJSON struct {
	Installed *struct {
		ClientID     string   `json:"client_id"`
		ClientSecret string   `json:"client_secret"`
		RedirectURIs []string `json:"redirect_uris"`
	} `json:"installed"`
	Web *struct {
		ClientID     string   `json:"client_id"`
		ClientSecret string   `json:"client_secret"`
		RedirectURIs []string `json:"redirect_uris"`
	} `json:"web"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RedirectURI  string `json:"redirect_uri"`
}

// LoadOAuthClientConfig tá»± Ä‘á»™ng tÃ¬m vÃ  náº¡p cáº¥u hÃ¬nh OAuth Client Credentials tá»« thÆ° má»¥c data
func (m *Manager) LoadOAuthClientConfig(optionalPath string) (*oauth2.Config, error) {
	candidatePaths := []string{}
	if optionalPath != "" {
		candidatePaths = append(candidatePaths, optionalPath)
	}

	m.mu.RLock()
	oauthDir := m.oauthDir
	dataDir := m.dataDir
	m.mu.RUnlock()

	candidatePaths = append(candidatePaths,
		filepath.Join(oauthDir, "credentials.json"),
		filepath.Join(oauthDir, "client_secret.json"),
		filepath.Join(dataDir, "oauth_credentials.json"),
		filepath.Join(dataDir, "credentials.json"),
		DefaultOAuthCredentialsFile,
	)

	var fileBytes []byte
	var foundPath string
	for _, p := range candidatePaths {
		if data, err := os.ReadFile(p); err == nil && len(data) > 0 {
			fileBytes = data
			foundPath = p
			break
		}
	}

	var clientID, clientSecret, redirectURL string
	if len(fileBytes) > 0 {
		var cred GoogleOAuthClientJSON
		if err := json.Unmarshal(fileBytes, &cred); err == nil {
			if cred.Installed != nil {
				clientID = cred.Installed.ClientID
				clientSecret = cred.Installed.ClientSecret
				if len(cred.Installed.RedirectURIs) > 0 {
					redirectURL = cred.Installed.RedirectURIs[0]
				}
			} else if cred.Web != nil {
				clientID = cred.Web.ClientID
				clientSecret = cred.Web.ClientSecret
				if len(cred.Web.RedirectURIs) > 0 {
					redirectURL = cred.Web.RedirectURIs[0]
				}
			} else {
				clientID = cred.ClientID
				clientSecret = cred.ClientSecret
				redirectURL = cred.RedirectURI
			}
		}
	}

	// Fallback tá»« cáº¥u hÃ¬nh DB náº¿u cÃ³
	if clientID == "" && m.db != nil {
		if settings, err := m.db.GetSettings(); err == nil && settings != nil {
			clientID = settings.GoogleClientID
			clientSecret = settings.GoogleClientSecret
			redirectURL = settings.RedirectURL
		}
	}

	if clientID == "" {
		return nil, fmt.Errorf("khÃ´ng tÃ¬m tháº¥y file OAuth credentials táº¡i '%s' vÃ  chÆ°a cáº¥u hÃ¬nh trong DB", foundPath)
	}

	if redirectURL == "" {
		redirectURL = "http://localhost:8080/api/storage/oauth/callback"
	}

	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes: []string{
			DriveScopeFull,
			"https://www.googleapis.com/auth/userinfo.email",
			"https://www.googleapis.com/auth/userinfo.profile",
		},
		Endpoint: google.Endpoint,
	}, nil
}

// GetOAuthURL táº¡o Google OAuth Consent URL
func (m *Manager) GetOAuthURL(clientID, clientSecret, redirectURL, state string) string {
	if clientID == "" || clientSecret == "" {
		if cfg, err := m.LoadOAuthClientConfig(""); err == nil {
			if clientID == "" {
				clientID = cfg.ClientID
			}
			if clientSecret == "" {
				clientSecret = cfg.ClientSecret
			}
			if redirectURL == "" {
				redirectURL = cfg.RedirectURL
			}
		}
	}

	config := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes: []string{
			DriveScopeFull,
			"https://www.googleapis.com/auth/userinfo.email",
			"https://www.googleapis.com/auth/userinfo.profile",
		},
		Endpoint: google.Endpoint,
	}
	return config.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent select_account"))
}

// HandleOAuthCallback Ä‘á»•i authorization code láº¥y token, lÆ°u tÃ i khoáº£n vÃ  kÃ­ch hoáº¡t service
func (m *Manager) HandleOAuthCallback(ctx context.Context, clientID, clientSecret, redirectURL, code string) (*models.Account, error) {
	if clientID == "" || clientSecret == "" {
		if cfg, err := m.LoadOAuthClientConfig(""); err == nil {
			if clientID == "" {
				clientID = cfg.ClientID
			}
			if clientSecret == "" {
				clientSecret = cfg.ClientSecret
			}
			if redirectURL == "" {
				redirectURL = cfg.RedirectURL
			}
		}
	}

	config := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes: []string{
			DriveScopeFull,
			"https://www.googleapis.com/auth/userinfo.email",
			"https://www.googleapis.com/auth/userinfo.profile",
		},
		Endpoint: google.Endpoint,
	}

	token, err := config.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange auth code: %w", err)
	}

	tokenBytes, err := json.Marshal(token)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize token: %w", err)
	}

	// Táº¡o Drive client
	client := config.Client(ctx, token)
	srv, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("failed to create drive service: %w", err)
	}

	aCtx, aCancel := context.WithTimeout(ctx, 10*time.Second)
	defer aCancel()
	about, err := srv.About.Get().Fields("user,storageQuota").Context(aCtx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user info from drive: %w", err)
	}

	email := about.User.EmailAddress
	name := about.User.DisplayName
	avatar := about.User.PhotoLink

	var totalQuota, usedQuota, freeQuota int64
	if about.StorageQuota != nil {
		totalQuota = about.StorageQuota.Limit
		usedQuota = about.StorageQuota.Usage
		if totalQuota > 0 {
			freeQuota = totalQuota - usedQuota
		} else {
			// KhÃ´ng giá»›i háº¡n hoáº·c custom workspace
			totalQuota = 100 * 1024 * 1024 * 1024 * 1024 // 100TB
			freeQuota = totalQuota - usedQuota
		}
	}

	// Äáº£m báº£o thÆ° má»¥c lÆ°u trá»¯ gá»‘c trÃªn Google Drive
	rootFolderID, err := m.ensureRootFolder(srv)
	if err != nil || rootFolderID == "" {
		rootFolderID = "root"
	}

	// Kiểm tra tài khoản đã tồn tại chưa để bảo toàn Refresh Token và tránh lỗi UNIQUE constraint accounts.email_hash
	var existingAcc *models.Account
	if m.db != nil {
		existingAcc, _ = m.db.GetAccountByEmail(email)
	}

	accID := ""
	if existingAcc != nil {
		accID = existingAcc.ID
		// Nếu Google không trả về refresh_token mới (do tái kết nối), bảo tồn refresh_token cũ
		if token.RefreshToken == "" && existingAcc.TokenJSON != "" {
			var oldToken oauth2.Token
			if err := json.Unmarshal([]byte(existingAcc.TokenJSON), &oldToken); err == nil && oldToken.RefreshToken != "" {
				token.RefreshToken = oldToken.RefreshToken
				if reBytes, err := json.Marshal(token); err == nil {
					tokenBytes = reBytes
				}
			}
		}
	} else {
		accID = "acc_" + fmt.Sprintf("%x", time.Now().UnixNano())
		// Tài khoản mới kết nối lần đầu: Bắt buộc phải có refresh_token
		if token.RefreshToken == "" {
			return nil, fmt.Errorf("không nhận được refresh_token từ Google (tài khoản đã từng cấp quyền trước đó). Vui lòng vào https://myaccount.google.com/permissions hủy liên kết CloudPool rồi thử kết nối lại")
		}
	}

	acc := &models.Account{
		ID:              accID,
		Email:           email,
		Name:            name,
		AvatarURL:       avatar,
		AuthType:        "oauth",
		TokenJSON:       string(tokenBytes),
		RootFolderID:    rootFolderID,
		TotalQuotaBytes: totalQuota,
		UsedQuotaBytes:  usedQuota,
		FreeQuotaBytes:  freeQuota,
		Status:          "active",
		HealthStatus:    "healthy",
	}

	if m.db != nil {
		if err := m.db.SaveAccount(acc); err != nil {
			return nil, fmt.Errorf("failed to save account to db: %w", err)
		}
	}

	// Lưu bản sao chứng chỉ token an toàn vào thư mục data/oauth (Mã hóa an toàn theo Rule PHAN 3.2)
	m.mu.RLock()
	oauthDir := m.oauthDir
	m.mu.RUnlock()
	if oauthDir != "" {
		safeEmail := strings.ReplaceAll(email, "@", "_at_")
		safeEmail = strings.ReplaceAll(safeEmail, ".", "_")
		tokenFilePath := filepath.Join(oauthDir, fmt.Sprintf("token_%s_%s.json", safeEmail, accID))
		var encData []byte
		if m.db != nil {
			masterKey := m.db.GetMasterKey()
			encSecret := core.EncryptSecret(masterKey, string(tokenBytes))
			encData = []byte(encSecret)
		} else {
			encData = tokenBytes
		}
		_ = os.WriteFile(tokenFilePath, encData, 0600)
	}

	// Xóa cache service cũ (nếu có) để GetService lần tiếp theo tạo client mới với persistingTokenSource,
	// đảm bảo token refresh được persist vào DB đúng cách.
	m.mu.Lock()
	delete(m.services, acc.ID)
	m.mu.Unlock()

	return acc, nil
}

// AddServiceAccount thÃªm tÃ i khoáº£n Google Drive báº±ng Service Account JSON
func (m *Manager) AddServiceAccount(ctx context.Context, saJSON []byte) (*models.Account, error) {
	creds, err := google.CredentialsFromJSON(ctx, saJSON, DriveScopeFull)
	if err != nil {
		return nil, fmt.Errorf("invalid service account JSON: %w", err)
	}

	srv, err := drive.NewService(ctx, option.WithCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("failed to create drive service with SA: %w", err)
	}

	about, err := srv.About.Get().Fields("user,storageQuota").Do()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user info with SA: %w", err)
	}

	email := about.User.EmailAddress
	name := about.User.DisplayName
	if name == "" {
		name = "Service Account"
	}

	var totalQuota, usedQuota, freeQuota int64
	if about.StorageQuota != nil {
		totalQuota = about.StorageQuota.Limit
		usedQuota = about.StorageQuota.Usage
		if totalQuota > 0 {
			freeQuota = totalQuota - usedQuota
		} else {
			totalQuota = 100 * 1024 * 1024 * 1024 * 1024 // 100TB
			freeQuota = totalQuota - usedQuota
		}
	}

	rootFolderID, err := m.ensureRootFolder(srv)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure CloudPool folder: %w", err)
	}

	accID := "sa_" + fmt.Sprintf("%x", time.Now().UnixNano())
	acc := &models.Account{
		ID:              accID,
		Email:           email,
		Name:            name,
		AvatarURL:       "",
		AuthType:        "service_account",
		CredentialsJSON: string(saJSON),
		RootFolderID:    rootFolderID,
		TotalQuotaBytes: totalQuota,
		UsedQuotaBytes:  usedQuota,
		FreeQuotaBytes:  freeQuota,
		Status:          "active",
		HealthStatus:    "healthy",
	}

	if m.db != nil {
		if err := m.db.SaveAccount(acc); err != nil {
			return nil, fmt.Errorf("failed to save account to db: %w", err)
		}
	}

	// LÆ°u báº£n sao Service Account JSON an toÃ n vÃ o thÆ° má»¥c f:\supportflast.dev\data\service_accounts\
	m.mu.RLock()
	saDir := m.serviceAccountsDir
	m.mu.RUnlock()
	if saDir != "" {
		safeEmail := strings.ReplaceAll(email, "@", "_at_")
		safeEmail = strings.ReplaceAll(safeEmail, ".", "_")
		saFilePath := filepath.Join(saDir, fmt.Sprintf("sa_%s_%s.json", safeEmail, accID))
		_ = os.WriteFile(saFilePath, saJSON, 0600)
	}

	m.mu.Lock()
	m.services[acc.ID] = srv
	m.mu.Unlock()

	return acc, nil
}

// LoadServiceAccountsFromDir tá»± Ä‘á»™ng quÃ©t vÃ  náº¡p táº¥t cáº£ cÃ¡c file Service Account JSON tá»« thÆ° má»¥c chá»©ng chá»‰
func (m *Manager) LoadServiceAccountsFromDir(optionalDir string) (int, []error) {
	targetDir := optionalDir
	if targetDir == "" {
		m.mu.RLock()
		targetDir = m.serviceAccountsDir
		m.mu.RUnlock()
	}

	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return 0, []error{fmt.Errorf("khÃ´ng thá»ƒ Ä‘á»c thÆ° má»¥c Service Accounts '%s': %w", targetDir, err)}
	}

	var loadedCount int
	var errList []error

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}

		filePath := filepath.Join(targetDir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			errList = append(errList, fmt.Errorf("lá»—i Ä‘á»c file '%s': %w", entry.Name(), err))
			continue
		}

		// XÃ¡c thá»±c sÆ¡ bá»™ file Service Account
		var saCheck struct {
			Type        string `json:"type"`
			ProjectID   string `json:"project_id"`
			ClientEmail string `json:"client_email"`
		}
		if err := json.Unmarshal(data, &saCheck); err != nil || saCheck.Type != "service_account" {
			continue // KhÃ´ng pháº£i file Service Account cá»§a Google
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		_, err = m.AddServiceAccount(ctx, data)
		cancel()

		if err != nil {
			errList = append(errList, fmt.Errorf("khÃ´ng thá»ƒ náº¡p Service Account '%s': %w", entry.Name(), err))
		} else {
			loadedCount++
		}
	}

	return loadedCount, errList
}

// SelectAccount Ä‘iá»u phá»‘i vÃ  chá»n tÃ i khoáº£n Google Drive tá»‘i Æ°u theo cÃ¡c cÆ¡ cháº¿:
// 1. "least_used": Sáº¯p xáº¿p cÃ¡c tÃ i khoáº£n theo dung lÆ°á»£ng trá»‘ng kháº£ dá»¥ng giáº£m dáº§n;
//    phÃ¢n tÃ¡n luÃ¢n phiÃªn giá»¯a cÃ¡c tÃ i khoáº£n dá»“i dÃ o dung lÆ°á»£ng nháº¥t Ä‘á»ƒ trÃ¡nh táº¯c ngháº½n I/O.
// 2. "waterfill": RÃ³t Ä‘áº§y tuáº§n tá»± tá»«ng tÃ i khoáº£n; chá»‰ chuyá»ƒn sang tÃ i khoáº£n má»›i khi tÃ i khoáº£n hiá»‡n táº¡i Ä‘áº§y.
// 3. "round_robin" / "round-robin": LuÃ¢n phiÃªn vÃ²ng trÃ²n Ä‘á»u Ä‘áº·n giá»¯a cÃ¡c tÃ i khoáº£n Ä‘á»§ dung lÆ°á»£ng.
func (m *Manager) SelectAccount(ctx context.Context, strategy string, requiredBytes int64, excludeAccountIDs ...string) (*models.Account, error) {
	if m.db == nil {
		return nil, errors.New("cÆ¡ sá»Ÿ dá»¯ liá»‡u lÆ°u trá»¯ tÃ i khoáº£n chÆ°a Ä‘Æ°á»£c khá»Ÿi táº¡o")
	}

	accounts, err := m.db.ListAccounts()
	if err != nil {
		return nil, fmt.Errorf("khÃ´ng thá»ƒ truy váº¥n danh sÃ¡ch tÃ i khoáº£n: %w", err)
	}

	excludeMap := make(map[string]bool)
	for _, id := range excludeAccountIDs {
		excludeMap[id] = true
	}

	var activeAccounts []models.Account
	for _, a := range accounts {
		if a.Status != "active" || excludeMap[a.ID] {
			continue
		}
		if a.FreeQuotaBytes >= requiredBytes {
			activeAccounts = append(activeAccounts, a)
		}
	}

	if len(activeAccounts) == 0 {
		return nil, errors.New("khÃ´ng cÃ³ tÃ i khoáº£n Google Drive nÃ o cÃ²n Ä‘á»§ dung lÆ°á»£ng kháº£ dá»¥ng")
	}

	normStrategy := strings.ToLower(strings.TrimSpace(strategy))
	normStrategy = strings.ReplaceAll(normStrategy, "-", "_")

	switch normStrategy {
	case StrategyLeastUsed:
		// Sáº¯p xáº¿p giáº£m dáº§n theo dung lÆ°á»£ng cÃ²n trá»‘ng (nhiá»u chá»— trá»‘ng nháº¥t lÃªn Ä‘áº§u)
		sort.Slice(activeAccounts, func(i, j int) bool {
			return activeAccounts[i].FreeQuotaBytes > activeAccounts[j].FreeQuotaBytes
		})

		// Há»— trá»£ phÃ¢n bá»• luÃ¢n phiÃªn / phÃ¢n tÃ¡n Ä‘á»u chunk Ä‘a á»• Ä‘Ä©a:
		// Náº¿u cÃ³ nhiá»u tÃ i khoáº£n dá»“i dÃ o dung lÆ°á»£ng kháº£ dá»¥ng, luÃ¢n phiÃªn phÃ¢n bá»• giá»¯a cÃ¡c tÃ i khoáº£n Ä‘Ã³
		// Ä‘á»ƒ cÃ¡c chunk liÃªn tiáº¿p cá»§a má»™t tá»‡p lá»›n Ä‘Æ°á»£c phÃ¢n tÃ¡n Ä‘á»u qua nhiá»u tÃ i khoáº£n Google Drive khÃ¡c nhau.
		if len(activeAccounts) > 1 {
			maxFree := activeAccounts[0].FreeQuotaBytes
			var candidateCount int
			for _, a := range activeAccounts {
				if a.FreeQuotaBytes >= requiredBytes*5 && a.FreeQuotaBytes >= maxFree/5 {
					candidateCount++
				} else {
					break
				}
			}
			if candidateCount > 1 {
				m.mu.Lock()
				idx := m.rrIndex % candidateCount
				m.rrIndex = (m.rrIndex + 1) % candidateCount
				m.mu.Unlock()
				chosen := activeAccounts[idx]
				return &chosen, nil
			}
		}
		chosen := activeAccounts[0]
		return &chosen, nil

	case StrategyWaterfill:
		// RÃ³t Ä‘áº§y: Chá»n tÃ i khoáº£n Ä‘áº§u tiÃªn cÃ²n Ä‘á»§ dung lÆ°á»£ng áº£o kháº£ dá»¥ng
		// Thá»© tá»± Æ°u tiÃªn giá»¯ nguyÃªn theo danh sÃ¡ch cá»‘ Ä‘á»‹nh
		chosen := activeAccounts[0]
		return &chosen, nil

	case StrategyRoundRobin:
		m.mu.Lock()
		if m.rrIndex >= len(activeAccounts) {
			m.rrIndex = 0
		}
		idx := m.rrIndex % len(activeAccounts)
		m.rrIndex = (m.rrIndex + 1) % len(activeAccounts)
		m.mu.Unlock()
		chosen := activeAccounts[idx]
		return &chosen, nil

	default:
		// Máº·c Ä‘á»‹nh fallback vá» least_used
		sort.Slice(activeAccounts, func(i, j int) bool {
			return activeAccounts[i].FreeQuotaBytes > activeAccounts[j].FreeQuotaBytes
		})
		if len(activeAccounts) > 1 {
			maxFree := activeAccounts[0].FreeQuotaBytes
			var candidateCount int
			for _, a := range activeAccounts {
				if a.FreeQuotaBytes >= requiredBytes*5 && a.FreeQuotaBytes >= maxFree/5 {
					candidateCount++
				} else {
					break
				}
			}
			if candidateCount > 1 {
				m.mu.Lock()
				idx := m.rrIndex % candidateCount
				m.rrIndex = (m.rrIndex + 1) % candidateCount
				m.mu.Unlock()
				chosen := activeAccounts[idx]
				return &chosen, nil
			}
		}
		chosen := activeAccounts[0]
		return &chosen, nil
	}
}

type persistingTokenSource struct {
	base    oauth2.TokenSource
	account *models.Account
	db      *storage.DB
	mu      sync.Mutex
}

func (pts *persistingTokenSource) Token() (*oauth2.Token, error) {
	pts.mu.Lock()
	defer pts.mu.Unlock()

	tok, err := pts.base.Token()
	if err != nil {
		return nil, err
	}

	// Bảo toàn Refresh Token khi Google cấp phát access token mới nhưng không gửi lại refresh_token
	if tok.RefreshToken == "" && pts.account.TokenJSON != "" {
		var oldToken oauth2.Token
		if err := json.Unmarshal([]byte(pts.account.TokenJSON), &oldToken); err == nil && oldToken.RefreshToken != "" {
			tok.RefreshToken = oldToken.RefreshToken
		}
	}

	tokBytes, _ := json.Marshal(tok)
	if string(tokBytes) != pts.account.TokenJSON {
		pts.account.TokenJSON = string(tokBytes)
		if pts.db != nil {
			_ = pts.db.UpdateAccountToken(pts.account.ID, string(tokBytes))
		}
	}
	return tok, nil
}

// GetService láº¥y hoáº·c khá»Ÿi táº¡o má»™t drive.Service tÆ°Æ¡ng á»©ng vá»›i tÃ i khoáº£n
func (m *Manager) GetService(ctx context.Context, accountID string) (*drive.Service, *models.Account, error) {
	m.mu.RLock()
	srv, exists := m.services[accountID]
	m.mu.RUnlock()

	if m.db == nil {
		return nil, nil, errors.New("storage database not configured")
	}

	acc, err := m.db.GetAccount(accountID)
	if err != nil {
		return nil, nil, fmt.Errorf("account not found: %w", err)
	}

	if exists && srv != nil {
		return srv, acc, nil
	}

	// Tái tạo drive.Service - KHÔNG giữ m.mu.Lock() ở đây để tránh deadlock khi gọi LoadOAuthClientConfig
	var newSrv *drive.Service

	if acc.AuthType == "service_account" && acc.CredentialsJSON != "" {
		creds, err := google.CredentialsFromJSON(context.Background(), []byte(acc.CredentialsJSON), DriveScopeFull)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load SA creds: %w", err)
		}
		var errSrv error
		newSrv, errSrv = drive.NewService(context.Background(), option.WithCredentials(creds))
		if errSrv != nil {
			return nil, nil, fmt.Errorf("failed to create SA drive service: %w", errSrv)
		}
	} else if acc.AuthType == "oauth" && acc.TokenJSON != "" {
		clientID := ""
		clientSecret := ""

		if cfg, err := m.LoadOAuthClientConfig(""); err == nil && cfg != nil {
			clientID = cfg.ClientID
			clientSecret = cfg.ClientSecret
		} else if settings, _ := m.db.GetSettings(); settings != nil {
			clientID = settings.GoogleClientID
			clientSecret = settings.GoogleClientSecret
		}

		var token oauth2.Token
		if err := json.Unmarshal([]byte(acc.TokenJSON), &token); err != nil {
			return nil, nil, fmt.Errorf("invalid token JSON: %w", err)
		}

		config := &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Scopes:       []string{DriveScopeFull},
			Endpoint:     google.Endpoint,
		}

		baseSource := config.TokenSource(context.Background(), &token)
		pts := &persistingTokenSource{
			base:    baseSource,
			account: acc,
			db:      m.db,
		}

		transport := &oauth2.Transport{
			Source: pts,
			Base: &http.Transport{
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   100,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
				DisableCompression:    false,
				ForceAttemptHTTP2:     true,
			},
		}
		client := &http.Client{Transport: transport}
		var errSrv error
		newSrv, errSrv = drive.NewService(context.Background(), option.WithHTTPClient(client))
		if errSrv != nil {
			return nil, nil, fmt.Errorf("failed to create OAuth drive service: %w", errSrv)
		}
	} else {
		return nil, nil, errors.New("no valid credentials found for account")
	}

	m.mu.Lock()
	m.services[accountID] = newSrv
	m.mu.Unlock()

	return newSrv, acc, nil
}

// RefreshAccountQuota cáº­p nháº­t thÃ´ng tin dung lÆ°á»£ng tá»« Google Drive
func (m *Manager) RefreshAccountQuota(ctx context.Context, accountID string) error {
	srv, acc, err := m.GetService(context.Background(), accountID)
	if err != nil {
		if m.db != nil {
			_ = m.db.UpdateAccountQuota(accountID, 0, 0, 0, "error", err.Error())
		}
		return err
	}

	qCtx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	about, err := srv.About.Get().Fields("storageQuota").Context(qCtx).Do()
	if err != nil {
		if m.db != nil {
			_ = m.db.UpdateAccountQuota(accountID, acc.TotalQuotaBytes, acc.UsedQuotaBytes, acc.FreeQuotaBytes, "error", err.Error())
		}
		return err
	}

	if about.StorageQuota != nil && m.db != nil {
		total := about.StorageQuota.Limit
		used := about.StorageQuota.Usage
		free := int64(0)
		if total > 0 {
			free = total - used
		} else {
			total = 100 * 1024 * 1024 * 1024 * 1024
			free = total - used
		}
		status := "active"
		if free <= 100*1024*1024 { // Ãt hÆ¡n 100MB
			status = "full"
		}
		return m.db.UpdateAccountQuota(accountID, total, used, free, status, "")
	}

	return nil
}

// RefreshAllQuotas quÃ©t vÃ  cáº­p nháº­t dung lÆ°á»£ng táº¥t cáº£ cÃ¡c tÃ i khoáº£n
func (m *Manager) RefreshAllQuotas(ctx context.Context) {
	if m.db == nil {
		return
	}
	accounts, err := m.db.ListAccounts()
	if err != nil {
		return
	}

	for _, acc := range accounts {
		_ = m.RefreshAccountQuota(context.Background(), acc.ID)
	}
}

// ensureRootFolder kiá»ƒm tra hoáº·c táº¡o thÆ° má»¥c gá»‘c CloudPool trÃªn Google Drive
func (m *Manager) ensureRootFolder(srv *drive.Service) (string, error) {
	q := fmt.Sprintf("name = '%s' and mimeType = 'application/vnd.google-apps.folder' and trashed = false", CloudPoolFolderName)
	fCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	list, err := srv.Files.List().Q(q).Spaces("drive").Fields("files(id, name)").Context(fCtx).Do()
	if err != nil {
		return "", err
	}

	if len(list.Files) > 0 {
		return list.Files[0].Id, nil
	}

	// Tạo thư mục mới
	folderMeta := &drive.File{
		Name:     CloudPoolFolderName,
		MimeType: "application/vnd.google-apps.folder",
	}
	cCtx, cancel2 := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel2()

	file, err := srv.Files.Create(folderMeta).Fields("id").Context(cCtx).Do()
	if err != nil {
		return "", err
	}
	return file.Id, nil
}

// UploadChunk táº£i máº©u dá»¯ liá»‡u Ä‘Ã£ mÃ£ hÃ³a lÃªn tÃ i khoáº£n Google Drive chá»‰ Ä‘á»‹nh
func (m *Manager) UploadChunk(ctx context.Context, accountID, chunkFileName string, data []byte) (string, error) {
	srv, acc, err := m.GetService(ctx, accountID)
	if err != nil {
		return "", fmt.Errorf("failed to get drive service for account %s: %w", accountID, err)
	}

	fileMeta := &drive.File{
		Name:    chunkFileName,
		Parents: []string{acc.RootFolderID},
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		uploadCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		reader := bytes.NewReader(data)
		createdFile, err := srv.Files.Create(fileMeta).Media(reader).Fields("id, size").Context(uploadCtx).Do()
		cancel()
		if err == nil && createdFile != nil {
			return createdFile.Id, nil
		}
		lastErr = err
		time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
	}

	return "", fmt.Errorf("khÃ´ng táº£i Ä‘Æ°á»£c chunk lÃªn Google Drive: %w", lastErr)
}

// DownloadChunk táº£i ná»™i dung chunk tá»« Google Drive
func (m *Manager) DownloadChunk(ctx context.Context, accountID, gdriveFileID string) ([]byte, error) {
	srv, _, err := m.GetService(context.Background(), accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get drive service: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		dlCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		resp, err := srv.Files.Get(gdriveFileID).Context(dlCtx).Download()
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				data, readErr := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				cancel()
				if readErr == nil {
					return data, nil
				}
				lastErr = readErr
			} else {
				_ = resp.Body.Close()
				lastErr = fmt.Errorf("Google Drive download returned status: %d", resp.StatusCode)
			}
		} else {
			lastErr = err
			// Nếu file không tồn tại trên Drive (404 / notFound), dừng ngay lập tức không thử lại vô ích
			errStr := err.Error()
			if strings.Contains(errStr, "404") || strings.Contains(errStr, "notFound") {
				cancel()
				return nil, fmt.Errorf("chunk not found on Google Drive (404): %w", err)
			}
		}
		cancel()
		time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
	}

	return nil, fmt.Errorf("lá»—i táº£i chunk tá»« Google Drive: %w", lastErr)
}

// DownloadStream tráº£ vá» io.ReadCloser cho viá»‡c stream tá»©c thÃ¬ tá»« Google Drive
func (m *Manager) DownloadStream(ctx context.Context, accountID, gdriveFileID string) (io.ReadCloser, int64, error) {
	srv, _, err := m.GetService(context.Background(), accountID)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get drive service: %w", err)
	}

	resp, err := srv.Files.Get(gdriveFileID).Context(ctx).Download()
	if err != nil {
		return nil, 0, fmt.Errorf("lá»—i táº£i stream tá»« Google Drive: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, 0, fmt.Errorf("google drive download status: %d", resp.StatusCode)
	}
	return resp.Body, resp.ContentLength, nil
}

// DownloadRange táº£i byte range tá»« Google Drive phá»¥c vá»¥ seek/stream media
func (m *Manager) DownloadRange(ctx context.Context, accountID, gdriveFileID string, start, end int64) ([]byte, error) {
	srv, _, err := m.GetService(context.Background(), accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get drive service for account %s: %w", accountID, err)
	}

	dlCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	call := srv.Files.Get(gdriveFileID).Context(dlCtx)
	if end >= start && end > 0 {
		call.Header().Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	} else if start >= 0 {
		call.Header().Set("Range", fmt.Sprintf("bytes=%d-", start))
	}

	resp, err := call.Download()
	if err != nil {
		return nil, fmt.Errorf("failed to download range from Google Drive: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("Google Drive download returned status: %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// DeleteChunk xÃ³a file chunk trÃªn Google Drive
func (m *Manager) DeleteChunk(ctx context.Context, accountID, gdriveFileID string) error {
	srv, _, err := m.GetService(context.Background(), accountID)
	if err != nil {
		return err
	}
	delCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return srv.Files.Delete(gdriveFileID).Context(delCtx).Do()
}

// ScanExistingDriveFiles quÃ©t cÃ¡c file sáºµn cÃ³ bÃªn ngoÃ i thÆ° má»¥c áº©n CloudPool
func (m *Manager) ScanExistingDriveFiles(ctx context.Context, accountID string) ([]*drive.File, error) {
	srv, _, err := m.GetService(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get drive service for account %s: %w", accountID, err)
	}

	var results []*drive.File
	pageToken := ""

	q := "trashed = false and mimeType != 'application/vnd.google-apps.folder' and not name contains '.cloudpool_storage_data' and not name contains 'chk_'"

	for {
		call := srv.Files.List().Q(q).PageSize(100).Fields("nextPageToken, files(id, name, size, mimeType, md5Checksum, modifiedTime, createdTime, parents)")
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}

		res, err := call.Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("failed to list drive files: %w", err)
		}

		for _, f := range res.Files {
			if f.Size > 0 {
				results = append(results, f)
			}
		}

		pageToken = res.NextPageToken
		if pageToken == "" || len(results) >= 500 {
			break
		}
	}

	return results, nil
}

