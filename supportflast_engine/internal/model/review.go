package model

// Review cấu trúc nhận xét đánh giá ứng dụng (model layer)
type Review struct {
	ID         string `json:"id"`
	AppID      string `json:"app_id"`
	UserID     string `json:"user_id"`
	AuthorName string `json:"author_name"`
	AuthorRole string `json:"author_role"`
	Stars      int    `json:"stars"`
	Text       string `json:"text"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
}
