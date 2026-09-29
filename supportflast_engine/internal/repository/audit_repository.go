package repository

import "supportflast_engine/internal/model"

// AuditRepository định nghĩa các thao tác ghi nhật ký kiểm toán
type AuditRepository interface {
	Create(log *model.AuditLog) error
	Record(log *model.AuditLog) error
	FindByUserID(userID string) ([]*model.AuditLog, error)
	ListAll(limit, offset int) ([]*model.AuditLog, error)
	List(limit, offset int) ([]model.AuditLog, error)
	ListByAction(action string, limit, offset int) ([]model.AuditLog, error)
}
