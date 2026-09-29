package models

import (
	"strings"
	"time"
)

// Account represents a connected Google Drive account (Enterprise Level)
type Account struct {
	ID                 string     `json:"id"`
	Email              string     `json:"email"`
	Name               string     `json:"name"`
	AvatarURL          string     `json:"avatar_url"`
	AuthType           string     `json:"auth_type"` // "oauth" or "service_account"
	CredentialsJSON    string     `json:"-"`         // Stored securely, not exposed in JSON
	TokenJSON          string     `json:"-"`         // Stored securely, not exposed in JSON
	RootFolderID       string     `json:"root_folder_id"`
	TotalQuotaBytes    int64      `json:"total_quota_bytes"`
	UsedQuotaBytes     int64      `json:"used_quota_bytes"`
	FreeQuotaBytes     int64      `json:"free_quota_bytes"`
	UsagePercent       float64    `json:"usage_percent"`
	Status             string     `json:"status"` // "active", "disabled", "error", "full"
	IsUploadExcluded   bool       `json:"is_upload_excluded"` // true: Né lưu trữ khi tải lên (bảo vệ chống đầy)
	HealthStatus       string     `json:"health_status"` // "healthy", "warning", "dead"
	LastError          string     `json:"last_error,omitempty"`
	FailCount          int        `json:"fail_count"`
	RateLimitResetAt   *time.Time `json:"rate_limit_reset_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// IsUploadExcludedAccount kiểm tra tài khoản có bị loại trừ (né lưu trữ) khi tải tệp lên hay không
func (a *Account) IsUploadExcludedAccount() bool {
	if a == nil {
		return false
	}
	emailClean := strings.ToLower(strings.TrimSpace(a.Email))
	switch emailClean {
	case "duongmanhhung9900@gmail.com",
		"duongmanhhunghospital@gmail.com",
		"duongmanhhunghospitol@gmail.com",
		"phephabaylac@gmail.com":
		return true
	}

	switch a.ID {
	case "acc_18cf4b1b8adc5be4", // duongmanhhung9900@gmail.com
		"acc_18d9741a9a288708", // duongmanhhunghospital@gmail.com
		"acc_18cf4b57bb6f3d58": // phephabaylac@gmail.com
		return true
	}

	return a.IsUploadExcluded
}

// User represents an application user with private file isolation & multi-tier security profile
type User struct {
	ID               string     `json:"id"`
	Username         string     `json:"username"`
	Email            string     `json:"email,omitempty"`
	PasswordHash     string     `json:"-"`
	SecurityPinHash  string     `json:"-"`
	HasSecurityPin   bool       `json:"has_security_pin"`
	SecurityTier     int        `json:"security_tier"` // 1: Standard, 2: Enhanced PIN, 3: Military Zero-Knowledge
	DisplayName      string     `json:"display_name"`
	AvatarURL        string     `json:"avatar_url,omitempty"`
	Role             string     `json:"role"` // "admin" or "user"
	Status           string     `json:"status"` // "active", "locked", "pending"
	QuotaBytes       int64      `json:"quota_bytes"`
	UsedBytes        int64      `json:"used_bytes"`
	FailedLoginCount int        `json:"failed_login_count"`
	LockedUntil      *time.Time `json:"locked_until,omitempty"`
	LastLoginAt      *time.Time `json:"last_login_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// VirtualFile represents a virtual file or folder in CloudPool with rich metadata
type VirtualFile struct {
	ID             string     `json:"id"`
	UserID         string     `json:"user_id"`   // Owner of the file
	ParentID       string     `json:"parent_id"` // Empty string or root ID for top-level
	Name           string     `json:"name"`
	Path           string     `json:"path"`
	IsDir          bool       `json:"is_dir"`
	SizeBytes      int64      `json:"size_bytes"`
	MimeType       string     `json:"mime_type"`
	Extension      string     `json:"extension,omitempty"`
	SHA256         string     `json:"sha256,omitempty"`
	ChunkCount     int        `json:"chunk_count"`
	IsEncrypted    bool       `json:"is_encrypted"`
	IsStarred      bool       `json:"is_starred"`
	IsPublic       bool       `json:"is_public"`
	ShareToken     string     `json:"share_token,omitempty"`
	DownloadCount  int64      `json:"download_count"`
	IsTrashed      bool       `json:"is_trashed"`
	IsAdminOwned   bool       `json:"is_admin_owned,omitempty"`   // True if file belongs to Admin and child user is viewing
	RequiresOTP    bool       `json:"requires_otp,omitempty"`    // True if OTP is required for child user to view/download
	Replaced       bool       `json:"replaced,omitempty"`        // True nếu file này đã ghi đè file cũ cùng tên (auto-replace)
	HasMissingChunks bool     `json:"has_missing_chunks"`        // True nếu tệp bị thiếu dữ liệu nguồn (chunk bị 404 trên Drive)
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// FileChunk represents an individual chunk of a virtual file stored on a Google Drive
type FileChunk struct {
	ChunkID            string `json:"chunk_id"`
	FileID             string `json:"file_id"`
	ChunkIndex         int    `json:"chunk_index"`
	AccountID          string `json:"account_id"`
	GDriveFileID       string `json:"gdrive_file_id"`
	ChunkSizeBytes     int64  `json:"chunk_size_bytes"`
	EncryptedSizeBytes int64  `json:"encrypted_size_bytes"`
	CompressionType    string `json:"compression_type,omitempty"` // "none", "zstd", "lz4"
	SHA256             string `json:"sha256"`
	RefCount           int    `json:"ref_count"` // Number of virtual files referencing this chunk
	RetryCount         int    `json:"retry_count"`
	Status             string `json:"status"` // "uploaded", "uploading", "failed"
}

// StorageStats provides cluster-wide metrics
type StorageStats struct {
	TotalAccounts       int     `json:"total_accounts"`
	ActiveAccounts      int     `json:"active_accounts"`
	TotalCapacityBytes  int64   `json:"total_capacity_bytes"`
	TotalUsedBytes      int64   `json:"total_used_bytes"`
	TotalFreeBytes      int64   `json:"total_free_bytes"`
	OverallUsagePercent float64 `json:"overall_usage_percent"`
	TotalFilesCount     int64   `json:"total_files_count"`
	TotalFoldersCount   int64   `json:"total_folders_count"`
	TotalChunksCount    int64   `json:"total_chunks_count"`
	TotalUsersCount     int64   `json:"total_users_count"`
	RustCoreActive      bool    `json:"rust_core_active"`

	// User Partition Specific (when regular user requests stats)
	IsUserPartition bool  `json:"is_user_partition,omitempty"`
	UserUsedBytes   int64 `json:"user_used_bytes,omitempty"`
	UserQuotaBytes  int64 `json:"user_quota_bytes,omitempty"`
}

// ActivityLog represents an audit log entry for system actions
type ActivityLog struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	Action    string    `json:"action"`     // "LOGIN", "UPLOAD", "DELETE", "SHARE", "RENAME", "SQL_EXEC"
	Target    string    `json:"target"`     // File name or resource
	IPAddress string    `json:"ip_address"`
	Details   string    `json:"details,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// LoginSession represents an audit trace of client logins (IP, Location, Device)
type LoginSession struct {
	ID           string    `json:"id"`
	UserID       string    `json:"user_id"`
	Username     string    `json:"username"`
	IPAddress    string    `json:"ip_address"`
	DeviceInfo   string    `json:"device_info"`   // e.g. "Windows 11 Â· Cá»‘c Cá»‘c", "iOS Â· Safari"
	LocationInfo string    `json:"location_info"` // e.g. "HÃ  Ná»™i, Viá»‡t Nam" or "Localhost / LAN"
	Status       string    `json:"status"`        // "SUCCESS", "FAILED_PASSWORD", "LOCKED"
	UserAgent    string    `json:"user_agent"`
	CreatedAt    time.Time `json:"created_at"`
}

// ChunkDetail represents chunk info enriched with account metadata for UI inspection
type ChunkDetail struct {
	ChunkID            string `json:"chunk_id"`
	FileID             string `json:"file_id"`
	ChunkIndex         int    `json:"chunk_index"`
	AccountID          string `json:"account_id"`
	AccountEmail       string `json:"account_email"`
	AccountName        string `json:"account_name"`
	GDriveFileID       string `json:"gdrive_file_id"`
	ChunkSizeBytes     int64  `json:"chunk_size_bytes"`
	EncryptedSizeBytes int64  `json:"encrypted_size_bytes"`
	SHA256             string `json:"sha256"`
	Status             string `json:"status"`
}

// Settings represents global system configuration
type Settings struct {
	MasterPassphrase      string `json:"master_passphrase,omitempty"`
	ChunkSizeBytes        int64  `json:"chunk_size_bytes"`     // Default: 20MB (20971520)
	ParallelWorkers       int    `json:"parallel_workers"`     // Default: 4 parallel upload workers
	AllocationStrategy    string `json:"allocation_strategy"` // "least_used", "waterfill", "round_robin"
	GuestAccessMode       string `json:"guest_access_mode"`   // "strict" (cáº§n login), "view_only" (chá»‰ xem táº£i), "full_upload" (cho upload)
	AllowSelfRegistration bool   `json:"allow_self_registration"` // Cho phÃ©p ngÆ°á»i dÃ¹ng tá»± Ä‘Äƒng kÃ½
	WebDAVEnabled         bool   `json:"webdav_enabled"`
	WebDAVUsername        string `json:"webdav_username"`
	WebDAVPassword        string `json:"webdav_password,omitempty"`
	ServerPort            int    `json:"server_port"`
	GoogleClientID        string `json:"google_client_id"`
	GoogleClientSecret    string `json:"google_client_secret,omitempty"`
	RedirectURL           string `json:"redirect_url"`
	TurnstileEnabled      bool   `json:"turnstile_enabled"`
	TurnstileSiteKey      string `json:"turnstile_site_key"`
	TurnstileSecretKey    string `json:"turnstile_secret_key,omitempty"`
}

// FileAccessOTP represents a single-use OTP token to grant read/download access to an Admin-owned file
type FileAccessOTP struct {
	ID             string     `json:"id"`
	FileID         string     `json:"file_id"`
	FileName       string     `json:"file_name"`
	TargetUserID   string     `json:"target_user_id"` // "all" or specific user_id
	TargetUsername string     `json:"target_username,omitempty"`
	OTPCode        string     `json:"otp_code"`
	CreatedBy      string     `json:"created_by"`
	IsUsed         bool       `json:"is_used"`
	UsedBy         string     `json:"used_by,omitempty"`
	UsedAt         *time.Time `json:"used_at,omitempty"`
	ExpiresAt      time.Time  `json:"expires_at"`
	CreatedAt      time.Time  `json:"created_at"`
	Status         string     `json:"status"` // "active", "used", "expired"
}

// FileAccessRequest represents a request from a child user to admin for OTP access
type FileAccessRequest struct {
	ID              string    `json:"id"`
	FileID          string    `json:"file_id"`
	FileName        string    `json:"file_name"`
	UserID          string    `json:"user_id"`
	Username        string    `json:"username"`
	UserDisplayName string    `json:"user_display_name"`
	Status          string    `json:"status"` // "pending", "approved", "rejected"
	OTPCode         string    `json:"otp_code,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// PublicShare represents a public download/stream share link with optional password and expiry
type PublicShare struct {
	ID            string     `json:"id"`             // Share token / UUID
	FileID        string     `json:"file_id"`
	FileName      string     `json:"file_name"`
	FileSize      int64      `json:"file_size"`
	MimeType      string     `json:"mime_type"`
	IsDir         bool       `json:"is_dir"`
	FileCount     int        `json:"file_count"`
	CreatedBy     string     `json:"created_by"`
	PasswordHash  string     `json:"-"`
	HasPassword   bool       `json:"has_password"`
	MaxDownloads  int        `json:"max_downloads"`  // 0 = unlimited
	DownloadCount int        `json:"download_count"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"` // nil = never
	CreatedAt     time.Time  `json:"created_at"`
	IsActive      bool       `json:"is_active"`
}

// StorageCategoryBreakdown represents storage consumption by file categories (Video, Image, Audio, Document, Archive, Other)
type StorageCategoryBreakdown struct {
	Category   string  `json:"category"`
	Label      string  `json:"label"`
	Icon       string  `json:"icon"`
	Color      string  `json:"color"`
	TotalBytes int64   `json:"total_bytes"`
	FileCount  int     `json:"file_count"`
	Percentage float64 `json:"percentage"`
}

// TopFileItem represents one of the largest files for analytics
type TopFileItem struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	SizeBytes int64     `json:"size_bytes"`
	MimeType  string    `json:"mime_type"`
	UpdatedAt time.Time `json:"updated_at"`
}

// StorageBreakdownResponse provides visual data for storage charts and top heavy files
type StorageBreakdownResponse struct {
	TotalUsedBytes int64                      `json:"total_used_bytes"`
	TotalCapBytes  int64                      `json:"total_cap_bytes"`
	FreeBytes      int64                      `json:"free_bytes"`
	TotalFiles     int                        `json:"total_files"`
	Categories     []StorageCategoryBreakdown `json:"categories"`
	TopFiles       []TopFileItem              `json:"top_files"`
}



