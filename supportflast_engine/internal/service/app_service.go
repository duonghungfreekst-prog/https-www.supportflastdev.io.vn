package service

import (
	"fmt"

	"supportflast_engine/internal/model"
	"supportflast_engine/internal/repository"
)

// AppService định nghĩa các operations quản lý ứng dụng
type AppService interface {
	Publish(app *model.App) (*model.App, error)
	List() ([]*model.App, error)
	Download(id string) (*model.App, error)
	GetByID(id string) (*model.App, error)
}

// appServiceImpl là implementation chính
type appServiceImpl struct {
	appRepo repository.AppRepository
}

// NewAppService tạo app service với dependencies inject
func NewAppService(appRepo repository.AppRepository) AppService {
	return &appServiceImpl{
		appRepo: appRepo,
	}
}

// Publish đăng tải ứng dụng mới (stub Phase 1)
func (s *appServiceImpl) Publish(app *model.App) (*model.App, error) {
	return nil, fmt.Errorf("app: Publish chưa được triển khai")
}

// List liệt kê toàn bộ ứng dụng (stub Phase 1)
func (s *appServiceImpl) List() ([]*model.App, error) {
	return nil, fmt.Errorf("app: List chưa được triển khai")
}

// Download tải ứng dụng theo ID (stub Phase 1)
func (s *appServiceImpl) Download(id string) (*model.App, error) {
	return nil, fmt.Errorf("app: Download chưa được triển khai")
}

// GetByID tìm ứng dụng theo ID (stub Phase 1)
func (s *appServiceImpl) GetByID(id string) (*model.App, error) {
	return nil, fmt.Errorf("app: GetByID chưa được triển khai")
}
