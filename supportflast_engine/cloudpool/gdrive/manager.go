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

	// Các chiến lược phân bổ và điều phối tài khoản Google Drive
	StrategyLeastUsed   = "least_used"
	StrategyWaterfill   = "waterfill"
	StrategyRoundRobin  = "round_robin"
	StrategyRoundRobin2 = "round-robin" // Hỗ trợ định dạng hyphen
)

type Manager struct {
	db                 *storage.DB
	services           map[string]*drive.Service
	accounts           map[string]*models.Account
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
		accounts:           make(map[string]*models.Account),
		dataDir:            dataDir,
		oauthDir:           oauthDir,
		serviceAccountsDir: saDir,
	}

	// Đảm bảo các thư mục chứng chỉ luôn tồn tại sẵn sàng
	_ = m.EnsureDirectories()

	return m
}

// EnsureDirectories tự động tạo các thư mục chứng chỉ OAuth và Service Account tại thư mục data
func (m *Manager) EnsureDirectories() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	dirs := []string{m.dataDir, m.oauthDir, m.serviceAccountsDir}
	for _, d := range dirs {
		if d == "" {
			continue
		}
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("không thể tạo thư mục '%s': %w", d, err)
		}
	}
	return nil
}

// GetDataDir trả về đường dẫn thư mục data gốc
func (m *Manager) GetDataDir() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.dataDir
}

// GetOAuthDir trả về đường dẫn thư mục chứng chỉ OAuth
func (m *Manager) GetOAuthDir() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.oauthDir
}

// GetServiceAccountsDir trả về đường dẫn thư mục chứng chỉ Service Account
func (m *Manager) GetServiceAccountsDir() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.serviceAccountsDir
}

// SetDataDir cho phép cập nhật thư mục dữ liệu và tự động điều chỉnh thư mục OAuth/SA
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

// GoogleOAuthClientJSON đại diện cho file credentials client JSON tải từ Google Cloud Console
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

// LoadOAuthClientConfig tự động tìm và nạp cấu hình OAuth Client Credentials từ thư mục data
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

	// Fallback từ cấu hình DB nếu có
	if clientID == "" && m.db != nil {
		if settings, err := m.db.GetSettings(); err == nil && settings != nil {
			clientID = settings.GoogleClientID
			clientSecret = settings.GoogleClientSecret
			redirectURL = settings.RedirectURL
		}
	}

	if clientID == "" {
		return nil, fmt.Errorf("không tìm thấy file OAuth credentials tại '%s' và chưa cấu hình trong DB", foundPath)
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

// GetOAuthURL tạo Google OAuth Consent URL
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

// HandleOAuthCallback đổi authorization code lấy token, lưu tài khoản và kích hoạt service
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

	// Tạo Drive client
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
			// Không giới hạn hoặc custom workspace
			totalQuota = 100 * 1024 * 1024 * 1024 * 1024 // 100TB
			freeQuota = totalQuota - usedQuota
		}
	}

	// Đảm bảo thư mục lưu trữ gốc trên Google Drive
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

	// Xóa cache service và account cũ (nếu có) để GetService lần tiếp theo tạo client mới với persistingTokenSource,
	// đảm bảo token refresh được persist vào DB đúng cách.
	m.mu.Lock()
	delete(m.services, acc.ID)
	delete(m.accounts, acc.ID)
	m.mu.Unlock()

	return acc, nil
}

// AddServiceAccount thêm tài khoản Google Drive bằng Service Account JSON
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

	// Lưu bản sao Service Account JSON an toàn vào thư mục f:\supportflast.dev\data\service_accounts\
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
	m.accounts[acc.ID] = acc
	m.mu.Unlock()

	return acc, nil
}

// LoadServiceAccountsFromDir tự động quét và nạp tất cả các file Service Account JSON từ thư mục chứng chỉ
func (m *Manager) LoadServiceAccountsFromDir(optionalDir string) (int, []error) {
	targetDir := optionalDir
	if targetDir == "" {
		m.mu.RLock()
		targetDir = m.serviceAccountsDir
		m.mu.RUnlock()
	}

	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return 0, []error{fmt.Errorf("không thể đọc thư mục Service Accounts '%s': %w", targetDir, err)}
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
			errList = append(errList, fmt.Errorf("lỗi đọc file '%s': %w", entry.Name(), err))
			continue
		}

		// Xác thực sơ bộ file Service Account
		var saCheck struct {
			Type        string `json:"type"`
			ProjectID   string `json:"project_id"`
			ClientEmail string `json:"client_email"`
		}
		if err := json.Unmarshal(data, &saCheck); err != nil || saCheck.Type != "service_account" {
			continue // Không phải file Service Account của Google
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		_, err = m.AddServiceAccount(ctx, data)
		cancel()

		if err != nil {
			errList = append(errList, fmt.Errorf("không thể nạp Service Account '%s': %w", entry.Name(), err))
		} else {
			loadedCount++
		}
	}

	return loadedCount, errList
}

// SelectAccount điều phối và chọn tài khoản Google Drive tối ưu theo các cơ chế:
// 1. "least_used": Sắp xếp các tài khoản theo dung lượng trống khả dụng giảm dần;
//    phân tán luân phiên giữa các tài khoản dồi dào dung lượng nhất để tránh tắc nghẽn I/O.
// 2. "waterfill": Rót đầy tuần tự từng tài khoản; chỉ chuyển sang tài khoản mới khi tài khoản hiện tại đầy.
// 3. "round_robin" / "round-robin": Luân phiên vòng tròn đều đặn giữa các tài khoản đủ dung lượng.
func (m *Manager) SelectAccount(ctx context.Context, strategy string, requiredBytes int64, excludeAccountIDs ...string) (*models.Account, error) {
	if m.db == nil {
		return nil, errors.New("cơ sở dữ liệu lưu trữ tài khoản chưa được khởi tạo")
	}

	accounts, err := m.db.ListAccounts()
	if err != nil {
		return nil, fmt.Errorf("không thể truy vấn danh sách tài khoản: %w", err)
	}

	excludeMap := make(map[string]bool)
	for _, id := range excludeAccountIDs {
		excludeMap[id] = true
	}

	var activeAccounts []models.Account
	for _, a := range accounts {
		if a.Status != "active" || excludeMap[a.ID] || a.IsUploadExcludedAccount() {
			continue
		}
		if a.FreeQuotaBytes >= requiredBytes {
			activeAccounts = append(activeAccounts, a)
		}
	}

	if len(activeAccounts) == 0 {
		return nil, errors.New("không có tài khoản Google Drive nào còn đủ dung lượng khả dụng")
	}

	normStrategy := strings.ToLower(strings.TrimSpace(strategy))
	normStrategy = strings.ReplaceAll(normStrategy, "-", "_")

	switch normStrategy {
	case StrategyLeastUsed:
		// Sắp xếp giảm dần theo dung lượng còn trống (nhiều chỗ trống nhất lên đầu)
		sort.Slice(activeAccounts, func(i, j int) bool {
			return activeAccounts[i].FreeQuotaBytes > activeAccounts[j].FreeQuotaBytes
		})

		// Hỗ trợ phân bổ luân phiên / phân tán đều chunk đa ổ đĩa:
		// Nếu có nhiều tài khoản dồi dào dung lượng khả dụng, luân phiên phân bổ giữa các tài khoản đó
		// để các chunk liên tiếp của một tệp lớn được phân tán đều qua nhiều tài khoản Google Drive khác nhau.
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
		// Rót đầy: Chọn tài khoản đầu tiên còn đủ dung lượng ảo khả dụng
		// Thứ tự ưu tiên giữ nguyên theo danh sách cố định
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
		// Mặc định fallback về least_used
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

// GetService lấy hoặc khởi tạo một drive.Service tương ứng với tài khoản
func (m *Manager) GetService(ctx context.Context, accountID string) (*drive.Service, *models.Account, error) {
	m.mu.RLock()
	srv, srvExists := m.services[accountID]
	acc, accExists := m.accounts[accountID]
	m.mu.RUnlock()

	// FAST PATH (Zero TiDB Query): Nếu cả srv và acc đã có trong cache in-memory, trả về ngay lập tức!
	if srvExists && accExists && srv != nil && acc != nil {
		return srv, acc, nil
	}

	if m.db == nil {
		return nil, nil, errors.New("storage database not configured")
	}

	// Chỉ query DB khi chưa có trong cache
	if !accExists || acc == nil {
		var err error
		acc, err = m.db.GetAccount(accountID)
		if err != nil {
			return nil, nil, fmt.Errorf("account not found: %w", err)
		}
	}

	// Nếu srv đã tồn tại từ trước nhưng acc vừa mới lấy từ DB, lưu acc vào cache rồi trả về ngay
	if srvExists && srv != nil {
		m.mu.Lock()
		m.accounts[accountID] = acc
		m.mu.Unlock()
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
	m.accounts[accountID] = acc
	m.mu.Unlock()

	return newSrv, acc, nil
}

// InvalidateAccount xóa tài khoản khỏi bộ nhớ đệm RAM khi tài khoản bị sửa, đổi hoặc xóa
func (m *Manager) InvalidateAccount(accountID string) {
	m.mu.Lock()
	delete(m.services, accountID)
	delete(m.accounts, accountID)
	m.mu.Unlock()
}

// RefreshAccountQuota cập nhật thông tin dung lượng từ Google Drive
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
		if free <= 100*1024*1024 { // Ít hơn 100MB
			status = "full"
		}

		m.mu.Lock()
		if cachedAcc, ok := m.accounts[accountID]; ok && cachedAcc != nil {
			cachedAcc.TotalQuotaBytes = total
			cachedAcc.UsedQuotaBytes = used
			cachedAcc.FreeQuotaBytes = free
			cachedAcc.Status = status
		}
		m.mu.Unlock()

		return m.db.UpdateAccountQuota(accountID, total, used, free, status, "")
	}

	return nil
}

// RefreshAllQuotas quét và cập nhật dung lượng tất cả các tài khoản
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

// ensureRootFolder kiểm tra hoặc tạo thư mục gốc CloudPool trên Google Drive
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

// UploadChunk tải mẩu dữ liệu đã mã hóa lên tài khoản Google Drive chỉ định
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

	return "", fmt.Errorf("không tải được chunk lên Google Drive: %w", lastErr)
}

// DownloadChunk tải nội dung chunk từ Google Drive
func (m *Manager) DownloadChunk(ctx context.Context, accountID, gdriveFileID string) ([]byte, error) {
	srv, _, err := m.GetService(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get drive service: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		dlCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		resp, err := srv.Files.Get(gdriveFileID).Context(dlCtx).Download()
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				var data []byte
				var readErr error
				if resp.ContentLength > 0 {
					data = make([]byte, resp.ContentLength)
					_, readErr = io.ReadFull(resp.Body, data)
				} else {
					data, readErr = io.ReadAll(resp.Body)
				}
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

	return nil, fmt.Errorf("lỗi tải chunk từ Google Drive: %w", lastErr)
}

// DownloadStream trả về io.ReadCloser cho việc stream tức thì từ Google Drive
func (m *Manager) DownloadStream(ctx context.Context, accountID, gdriveFileID string) (io.ReadCloser, int64, error) {
	srv, _, err := m.GetService(ctx, accountID)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get drive service: %w", err)
	}

	resp, err := srv.Files.Get(gdriveFileID).Context(ctx).Download()
	if err != nil {
		return nil, 0, fmt.Errorf("lỗi tải stream từ Google Drive: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, 0, fmt.Errorf("google drive download status: %d", resp.StatusCode)
	}
	return resp.Body, resp.ContentLength, nil
}

// DownloadRange tải byte range từ Google Drive phục vụ seek/stream media
func (m *Manager) DownloadRange(ctx context.Context, accountID, gdriveFileID string, start, end int64) ([]byte, error) {
	srv, _, err := m.GetService(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get drive service for account %s: %w", accountID, err)
	}

	dlCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
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

	if resp.ContentLength > 0 {
		data := make([]byte, resp.ContentLength)
		_, err := io.ReadFull(resp.Body, data)
		return data, err
	}
	return io.ReadAll(resp.Body)
}

// DeleteChunk xóa file chunk trên Google Drive
func (m *Manager) DeleteChunk(ctx context.Context, accountID, gdriveFileID string) error {
	srv, _, err := m.GetService(ctx, accountID)
	if err != nil {
		return err
	}
	delCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return srv.Files.Delete(gdriveFileID).Context(delCtx).Do()
}

// ScanExistingDriveFiles quét các file sẵn có bên ngoài thư mục ẩn CloudPool
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

// CheckChunkExists kiểm tra xem file chunk có thực sự tồn tại và còn nguyên vẹn trên Google Drive hay không
// Trả về (false, nil) nếu file bị 404 (đã bị xóa/di chuyển), hoặc file nằm trong Thùng rác (trashed = true)
// Trả về (true, nil) nếu file tồn tại bình thường
// Trả về (false, err) nếu gặp lỗi mạng, auth, hoặc quota rate-limit
func (m *Manager) CheckChunkExists(ctx context.Context, accountID, gdriveFileID string) (bool, error) {
	if strings.TrimSpace(gdriveFileID) == "" {
		return false, nil
	}
	srv, _, err := m.GetService(ctx, accountID)
	if err != nil {
		return false, fmt.Errorf("failed to get drive service for account %s: %w", accountID, err)
	}

	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	f, err := srv.Files.Get(gdriveFileID).Fields("id, name, size, trashed").Context(callCtx).Do()
	if err != nil {
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "404") || strings.Contains(errStr, "notfound") || strings.Contains(errStr, "filenotfound") {
			return false, nil // File đã bị xóa trên Google Drive (404)
		}
		return false, err // Lỗi kết nối hoặc chứng chỉ
	}

	if f == nil || f.Trashed {
		return false, nil // Nằm trong thùng rác hoặc không hợp lệ
	}

	return true, nil
}

