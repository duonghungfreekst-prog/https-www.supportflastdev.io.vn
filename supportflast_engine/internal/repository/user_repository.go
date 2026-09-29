package repository

import "supportflast_engine/internal/model"

// UserRepository định nghĩa các thao tác truy xuất dữ liệu người dùng
type UserRepository interface {
	FindByID(id string) (*model.User, error)
	FindByUsername(username string) (*model.User, error)
	FindByEmail(email string) (*model.User, error)
	Create(user *model.User) error
	Update(user *model.User) error
	UpdatePassword(userID, passwordHash string) error
	Delete(id string) error
	ListAll() ([]*model.User, error)
	List(limit, offset int) ([]model.User, error)
	Count() (int, error)
}
