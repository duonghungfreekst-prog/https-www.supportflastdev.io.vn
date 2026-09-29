package service

import "supportflast_engine/internal/repository"

// Container chứa tất cả services đã khởi tạo (DI Container thủ công)
type Container struct {
	Auth   AuthService
	App    AppService
	System SystemService
}

// NewContainer khởi tạo tất cả services với dependencies được inject
func NewContainer(userRepo repository.UserRepository, appRepo repository.AppRepository, auditRepo repository.AuditRepository) *Container {
	return &Container{
		Auth:   NewAuthService(userRepo, auditRepo),
		App:    NewAppService(appRepo),
		System: NewSystemService(),
	}
}
