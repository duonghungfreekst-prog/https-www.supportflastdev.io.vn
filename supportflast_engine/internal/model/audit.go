package model

// Hằng số định danh hành động ghi nhật ký kiểm toán (Audit Log Actions)
const (
	AuditActionLoginSuccess   = "login_success"
	AuditActionLoginFailure   = "login_failure"
	AuditActionLogout         = "logout"
	AuditActionChangePassword = "change_password"
	AuditActionChangePIN      = "change_pin"
	AuditActionHoneypotAccess = "honeypot_access"
	AuditActionConfigChange   = "config_change"
	AuditActionSettingsUpdate = "settings_update"
)

// AuditLog cấu trúc nhật ký bảo mật (model layer)
type AuditLog struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	Action    string `json:"action"`
	IPAddress string `json:"ip_address"`
	UserAgent string `json:"user_agent"`
	Details   string `json:"details"`
	CreatedAt string `json:"created_at"`
}

// SecurityEvent cấu trúc sự kiện an ninh và phòng thủ (model layer)
type SecurityEvent struct {
	ID           string `json:"id"`
	EventType    string `json:"event_type"`
	IPAddress    string `json:"ip_address"`
	Severity     string `json:"severity"`
	Details      string `json:"details"`
	BlockedUntil string `json:"blocked_until,omitempty"`
	CreatedAt    string `json:"created_at"`
}
