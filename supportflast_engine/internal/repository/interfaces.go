package repository

import "supportflast_engine/internal/model"

// SecurityEventRepository định nghĩa các thao tác truy cập sự kiện an ninh
type SecurityEventRepository interface {
	Record(event *model.SecurityEvent) error
	List(limit, offset int) ([]model.SecurityEvent, error)
	IsIPBlocked(ip string) (bool, error)
}

// ReviewRepository định nghĩa các thao tác truy cập nhận xét đánh giá
type ReviewRepository interface {
	FindByID(id string) (*model.Review, error)
	Create(review *model.Review) error
	Delete(id string) error
	ListByApp(appID string, limit, offset int) ([]model.Review, error)
}
