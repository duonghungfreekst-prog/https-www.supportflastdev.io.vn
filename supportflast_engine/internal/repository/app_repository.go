package repository

import "supportflast_engine/internal/model"

// AppRepository định nghĩa các thao tác truy xuất dữ liệu ứng dụng
type AppRepository interface {
	FindByID(id string) (*model.App, error)
	Create(app *model.App) error
	Update(app *model.App) error
	Delete(id string) error
	ListAll() ([]*model.App, error)
	List(limit, offset int) ([]model.App, error)
	ListByUserID(userID string) ([]*model.App, error)
	ListByCategory(category string, limit, offset int) ([]model.App, error)
	IncrementDownloads(id string) error
}
