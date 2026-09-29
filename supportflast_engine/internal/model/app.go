package model

// AppStatus kiểu định danh trạng thái ứng dụng
type AppStatus string

const (
	StatusPublished AppStatus = "published"
	StatusDraft     AppStatus = "draft"
	StatusArchived  AppStatus = "archived"
)

// App cấu trúc thực thể ứng dụng (model layer)
type App struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Version       string `json:"version"`
	Platform      string `json:"platform"`
	Category      string `json:"category"`
	Desc          string `json:"desc"`
	FileName      string `json:"file_name"`
	SizeBytes     int64  `json:"size_bytes"`
	SizeFormatted string `json:"size_formatted"`
	SHA256        string `json:"sha256"`
	Author        string `json:"author"`
	Downloads     int    `json:"downloads"`
	Status        string `json:"status"`
	PublishedAt   string `json:"published_at"`
	DownloadURL   string `json:"download_url"`
	VideoURL      string `json:"video_url"`
	Guide         string `json:"guide"`
	UserID        string `json:"user_id"`
}
