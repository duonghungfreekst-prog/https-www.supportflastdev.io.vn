package model

// UserRole kiểu định danh phân quyền người dùng
type UserRole string

const (
	RoleAdmin     UserRole = "admin"
	RoleUser      UserRole = "user"
	RoleDeveloper UserRole = "developer"
)

// User cấu trúc thực thể người dùng (model layer)
type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	Email        string `json:"email"`
	PasswordHash string `json:"-"`
	DisplayName  string `json:"display_name"`
	Role         string `json:"role"`
	Avatar       string `json:"avatar"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	LastLogin    string `json:"last_login"`
}
