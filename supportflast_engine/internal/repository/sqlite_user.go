package repository

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"supportflast_engine/database"
	"supportflast_engine/internal/model"
)

// SQLiteUserRepository triển khai UserRepository cho SQLite
type SQLiteUserRepository struct{}

// NewSQLiteUserRepository khởi tạo SQLiteUserRepository
func NewSQLiteUserRepository() *SQLiteUserRepository {
	return &SQLiteUserRepository{}
}

// sanitize loại bỏ ký tự CRLF khỏi chuỗi đầu vào để chống log injection
func sanitize(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return strings.TrimSpace(s)
}

// getDB lấy kết nối database thread-safe
func getDB() (*sql.DB, error) {
	db := database.GetDB()
	if db == nil {
		return nil, fmt.Errorf("database connection is nil")
	}
	return db, nil
}

// FindByID tìm người dùng theo ID bằng Prepared Statement
func (r *SQLiteUserRepository) FindByID(id string) (*model.User, error) {
	db, err := getDB()
	if err != nil {
		return nil, err
	}

	query := `
		SELECT id, username, email, password_hash, COALESCE(display_name, ''),
		       role, COALESCE(avatar, ''), COALESCE(created_at, ''),
		       COALESCE(updated_at, ''), COALESCE(last_login, '')
		FROM users WHERE id = ?
	`
	stmt, err := db.Prepare(query)
	if err != nil {
		log.Printf("[REPO] [USER] [ERROR] FindByID prepare failed: %v", err)
		return nil, fmt.Errorf("failed to prepare FindByID query: %w", err)
	}
	defer stmt.Close()

	cleanID := sanitize(id)
	var u model.User
	err = stmt.QueryRow(cleanID).Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.DisplayName,
		&u.Role, &u.Avatar, &u.CreatedAt, &u.UpdatedAt, &u.LastLogin,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		log.Printf("[REPO] [USER] [ERROR] FindByID scan failed for id=%s: %v", cleanID, err)
		return nil, fmt.Errorf("failed to scan user by id: %w", err)
	}
	return &u, nil
}

// FindByUsername tìm người dùng theo username bằng Prepared Statement
func (r *SQLiteUserRepository) FindByUsername(username string) (*model.User, error) {
	db, err := getDB()
	if err != nil {
		return nil, err
	}

	query := `
		SELECT id, username, email, password_hash, COALESCE(display_name, ''),
		       role, COALESCE(avatar, ''), COALESCE(created_at, ''),
		       COALESCE(updated_at, ''), COALESCE(last_login, '')
		FROM users WHERE username = ?
	`
	stmt, err := db.Prepare(query)
	if err != nil {
		log.Printf("[REPO] [USER] [ERROR] FindByUsername prepare failed: %v", err)
		return nil, fmt.Errorf("failed to prepare FindByUsername query: %w", err)
	}
	defer stmt.Close()

	cleanUsername := sanitize(username)
	var u model.User
	err = stmt.QueryRow(cleanUsername).Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.DisplayName,
		&u.Role, &u.Avatar, &u.CreatedAt, &u.UpdatedAt, &u.LastLogin,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		log.Printf("[REPO] [USER] [ERROR] FindByUsername scan failed for username=%s: %v", cleanUsername, err)
		return nil, fmt.Errorf("failed to scan user by username: %w", err)
	}
	return &u, nil
}

// FindByEmail tìm người dùng theo email bằng Prepared Statement
func (r *SQLiteUserRepository) FindByEmail(email string) (*model.User, error) {
	db, err := getDB()
	if err != nil {
		return nil, err
	}

	query := `
		SELECT id, username, email, password_hash, COALESCE(display_name, ''),
		       role, COALESCE(avatar, ''), COALESCE(created_at, ''),
		       COALESCE(updated_at, ''), COALESCE(last_login, '')
		FROM users WHERE email = ?
	`
	stmt, err := db.Prepare(query)
	if err != nil {
		log.Printf("[REPO] [USER] [ERROR] FindByEmail prepare failed: %v", err)
		return nil, fmt.Errorf("failed to prepare FindByEmail query: %w", err)
	}
	defer stmt.Close()

	cleanEmail := sanitize(email)
	var u model.User
	err = stmt.QueryRow(cleanEmail).Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.DisplayName,
		&u.Role, &u.Avatar, &u.CreatedAt, &u.UpdatedAt, &u.LastLogin,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		log.Printf("[REPO] [USER] [ERROR] FindByEmail scan failed for email=%s: %v", cleanEmail, err)
		return nil, fmt.Errorf("failed to scan user by email: %w", err)
	}
	return &u, nil
}

// Create tạo mới người dùng bằng Prepared Statement
func (r *SQLiteUserRepository) Create(user *model.User) error {
	db, err := getDB()
	if err != nil {
		return err
	}

	query := `
		INSERT INTO users (id, username, email, password_hash, display_name, role, avatar, created_at, updated_at, last_login)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	stmt, err := db.Prepare(query)
	if err != nil {
		log.Printf("[REPO] [USER] [ERROR] Create prepare failed: %v", err)
		return fmt.Errorf("failed to prepare Create query: %w", err)
	}
	defer stmt.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	if user.CreatedAt == "" {
		user.CreatedAt = now
	}
	if user.UpdatedAt == "" {
		user.UpdatedAt = now
	}

	_, err = stmt.Exec(
		sanitize(user.ID),
		sanitize(user.Username),
		sanitize(user.Email),
		user.PasswordHash,
		sanitize(user.DisplayName),
		sanitize(user.Role),
		sanitize(user.Avatar),
		user.CreatedAt,
		user.UpdatedAt,
		user.LastLogin,
	)
	if err != nil {
		log.Printf("[REPO] [USER] [ERROR] Create exec failed for username=%s: %v", user.Username, err)
		return fmt.Errorf("failed to create user: %w", err)
	}
	return nil
}

// Update cập nhật thông tin người dùng bằng Prepared Statement
func (r *SQLiteUserRepository) Update(user *model.User) error {
	db, err := getDB()
	if err != nil {
		return err
	}

	query := `
		UPDATE users
		SET username = ?, email = ?, password_hash = ?, display_name = ?,
		    role = ?, avatar = ?, updated_at = ?, last_login = ?
		WHERE id = ?
	`
	stmt, err := db.Prepare(query)
	if err != nil {
		log.Printf("[REPO] [USER] [ERROR] Update prepare failed: %v", err)
		return fmt.Errorf("failed to prepare Update query: %w", err)
	}
	defer stmt.Close()

	user.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	result, err := stmt.Exec(
		sanitize(user.Username),
		sanitize(user.Email),
		user.PasswordHash,
		sanitize(user.DisplayName),
		sanitize(user.Role),
		sanitize(user.Avatar),
		user.UpdatedAt,
		user.LastLogin,
		sanitize(user.ID),
	)
	if err != nil {
		log.Printf("[REPO] [USER] [ERROR] Update exec failed for id=%s: %v", user.ID, err)
		return fmt.Errorf("failed to update user: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("user not found: %s", user.ID)
	}
	return nil
}

// Delete xóa người dùng theo ID bằng Prepared Statement
func (r *SQLiteUserRepository) Delete(id string) error {
	db, err := getDB()
	if err != nil {
		return err
	}

	query := `DELETE FROM users WHERE id = ?`
	stmt, err := db.Prepare(query)
	if err != nil {
		log.Printf("[REPO] [USER] [ERROR] Delete prepare failed: %v", err)
		return fmt.Errorf("failed to prepare Delete query: %w", err)
	}
	defer stmt.Close()

	cleanID := sanitize(id)
	result, err := stmt.Exec(cleanID)
	if err != nil {
		log.Printf("[REPO] [USER] [ERROR] Delete exec failed for id=%s: %v", cleanID, err)
		return fmt.Errorf("failed to delete user: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("user not found: %s", cleanID)
	}
	return nil
}

// List lấy danh sách người dùng có phân trang bằng Prepared Statement
func (r *SQLiteUserRepository) List(limit, offset int) ([]model.User, error) {
	db, err := getDB()
	if err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}

	query := `
		SELECT id, username, email, password_hash, COALESCE(display_name, ''),
		       role, COALESCE(avatar, ''), COALESCE(created_at, ''),
		       COALESCE(updated_at, ''), COALESCE(last_login, '')
		FROM users
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`
	stmt, err := db.Prepare(query)
	if err != nil {
		log.Printf("[REPO] [USER] [ERROR] List prepare failed: %v", err)
		return nil, fmt.Errorf("failed to prepare List query: %w", err)
	}
	defer stmt.Close()

	rows, err := stmt.Query(limit, offset)
	if err != nil {
		log.Printf("[REPO] [USER] [ERROR] List query failed: %v", err)
		return nil, fmt.Errorf("failed to query users list: %w", err)
	}
	defer rows.Close()

	users := make([]model.User, 0)
	for rows.Next() {
		var u model.User
		if err := rows.Scan(
			&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.DisplayName,
			&u.Role, &u.Avatar, &u.CreatedAt, &u.UpdatedAt, &u.LastLogin,
		); err != nil {
			log.Printf("[REPO] [USER] [WARN] List scan row skipped: %v", err)
			continue
		}
		users = append(users, u)
	}
	return users, nil
}

// Count đếm tổng số người dùng bằng Prepared Statement
func (r *SQLiteUserRepository) Count() (int, error) {
	db, err := getDB()
	if err != nil {
		return 0, err
	}

	query := `SELECT COUNT(*) FROM users`
	stmt, err := db.Prepare(query)
	if err != nil {
		log.Printf("[REPO] [USER] [ERROR] Count prepare failed: %v", err)
		return 0, fmt.Errorf("failed to prepare Count query: %w", err)
	}
	defer stmt.Close()

	var count int
	err = stmt.QueryRow().Scan(&count)
	if err != nil {
		log.Printf("[REPO] [USER] [ERROR] Count scan failed: %v", err)
		return 0, fmt.Errorf("failed to count users: %w", err)
	}
	return count, nil
}
