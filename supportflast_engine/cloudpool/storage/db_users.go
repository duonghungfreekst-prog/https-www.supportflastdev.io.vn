package storage

import (
	"time"
	"supportflast_engine/cloudpool/core"
	"supportflast_engine/cloudpool/models"
	"github.com/google/uuid"
)


func (s *DB) CreateUser(u *models.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	masterKey := s.getMasterKey()
	encUsername := core.EncryptSecret(masterKey, u.Username)
	encEmail := core.EncryptSecret(masterKey, u.Email)
	encDisplay := core.EncryptSecret(masterKey, u.DisplayName)
	encAvatar := core.EncryptSecret(masterKey, u.AvatarURL)

	usernameHash := core.BlindIndexHash(masterKey, u.Username)
	emailHash := core.BlindIndexHash(masterKey, u.Email)

	_, err := s.db.Exec(`INSERT INTO cloudpool_users (id, username, username_hash, email, email_hash, password_hash, security_pin_hash, security_tier, display_name, avatar_url, role, quota_bytes, used_bytes, created_at, updated_at) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, encUsername, usernameHash, encEmail, emailHash, u.PasswordHash, u.SecurityPinHash, u.SecurityTier, encDisplay, encAvatar, u.Role, u.QuotaBytes, u.UsedBytes, u.CreatedAt, u.UpdatedAt)
	if err == nil {
		s.InvalidateStatsCache()
	}
	return err
}



func (s *DB) GetUserByUsername(username string) (*models.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	masterKey := s.getMasterKey()
	usernameHash := core.BlindIndexHash(masterKey, username)

	row := s.db.QueryRow(`SELECT id, username, email, password_hash, COALESCE(security_pin_hash, ''), COALESCE(security_tier, 1), display_name, avatar_url, role, quota_bytes, used_bytes, failed_login_count, locked_until, created_at, updated_at FROM cloudpool_users WHERE username_hash = ?`, usernameHash)
	var u models.User
	var rawLockedUntil, rawCreated, rawUpdated interface{}
	if err := row.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.SecurityPinHash, &u.SecurityTier, &u.DisplayName, &u.AvatarURL, &u.Role, &u.QuotaBytes, &u.UsedBytes, &u.FailedLoginCount, &rawLockedUntil, &rawCreated, &rawUpdated); err != nil {
		return nil, err
	}
	u.LockedUntil = parseFlexibleTimePtr(rawLockedUntil)
	u.CreatedAt = parseFlexibleTime(rawCreated)
	u.UpdatedAt = parseFlexibleTime(rawUpdated)
	u.HasSecurityPin = (u.SecurityPinHash != "")
	u.Username = core.DecryptSecret(masterKey, u.Username)
	u.Email = core.DecryptSecret(masterKey, u.Email)
	u.DisplayName = core.DecryptSecret(masterKey, u.DisplayName)
	u.AvatarURL = core.DecryptSecret(masterKey, u.AvatarURL)
	return &u, nil
}



func (s *DB) GetUserByID(id string) (*models.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := s.db.QueryRow(`SELECT id, username, email, password_hash, COALESCE(security_pin_hash, ''), COALESCE(security_tier, 1), display_name, avatar_url, role, quota_bytes, used_bytes, failed_login_count, locked_until, created_at, updated_at FROM cloudpool_users WHERE id = ?`, id)
	var u models.User
	var rawLockedUntil, rawCreated, rawUpdated interface{}
	if err := row.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.SecurityPinHash, &u.SecurityTier, &u.DisplayName, &u.AvatarURL, &u.Role, &u.QuotaBytes, &u.UsedBytes, &u.FailedLoginCount, &rawLockedUntil, &rawCreated, &rawUpdated); err != nil {
		return nil, err
	}
	masterKey := s.getMasterKey()
	u.LockedUntil = parseFlexibleTimePtr(rawLockedUntil)
	u.CreatedAt = parseFlexibleTime(rawCreated)
	u.UpdatedAt = parseFlexibleTime(rawUpdated)
	u.HasSecurityPin = (u.SecurityPinHash != "")
	u.Username = core.DecryptSecret(masterKey, u.Username)
	u.Email = core.DecryptSecret(masterKey, u.Email)
	u.DisplayName = core.DecryptSecret(masterKey, u.DisplayName)
	u.AvatarURL = core.DecryptSecret(masterKey, u.AvatarURL)
	return &u, nil
}



func (s *DB) ListUsers() ([]models.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT id, username, email, COALESCE(security_pin_hash, ''), COALESCE(security_tier, 1), display_name, avatar_url, role, quota_bytes, used_bytes, failed_login_count, locked_until, created_at, updated_at FROM cloudpool_users ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]models.User, 0)
	masterKey := s.getMasterKey()
	for rows.Next() {
		var u models.User
		var rawLockedUntil, rawCreated, rawUpdated interface{}
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.SecurityPinHash, &u.SecurityTier, &u.DisplayName, &u.AvatarURL, &u.Role, &u.QuotaBytes, &u.UsedBytes, &u.FailedLoginCount, &rawLockedUntil, &rawCreated, &rawUpdated); err == nil {
			u.LockedUntil = parseFlexibleTimePtr(rawLockedUntil)
			u.CreatedAt = parseFlexibleTime(rawCreated)
			u.UpdatedAt = parseFlexibleTime(rawUpdated)
			u.HasSecurityPin = (u.SecurityPinHash != "")
			u.Username = core.DecryptSecret(masterKey, u.Username)
			u.Email = core.DecryptSecret(masterKey, u.Email)
			u.DisplayName = core.DecryptSecret(masterKey, u.DisplayName)
			u.AvatarURL = core.DecryptSecret(masterKey, u.AvatarURL)
			list = append(list, u)
		}
	}
	return list, nil
}



func (s *DB) DeleteUser(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	masterKey := s.getMasterKey()
	adminHash := core.BlindIndexHash(masterKey, "admin")
	_, err := s.db.Exec("DELETE FROM cloudpool_users WHERE id = ? AND username_hash != ?", id, adminHash)
	if err == nil {
		s.InvalidateStatsCache()
	}
	return err
}



func (s *DB) UpdateUserQuota(id string, quotaBytes int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("UPDATE cloudpool_users SET quota_bytes = ?, updated_at = ? WHERE id = ?", quotaBytes, time.Now(), id)
	if err == nil {
		s.InvalidateStatsCache()
	}
	return err
}



func (s *DB) UpdateUserPassword(id, passwordHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("UPDATE cloudpool_users SET password_hash = ?, updated_at = ? WHERE id = ?", passwordHash, time.Now(), id)
	return err
}



func (s *DB) UpdateUserSecurityPin(id, pinHash string, tier int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("UPDATE cloudpool_users SET security_pin_hash = ?, security_tier = ?, updated_at = ? WHERE id = ?", pinHash, tier, time.Now(), id)
	return err
}



func (s *DB) RecordLoginFailure(username string) (int, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	masterKey := s.getMasterKey()
	usernameHash := core.BlindIndexHash(masterKey, username)

	var fails int
	_ = s.db.QueryRow("SELECT failed_login_count FROM cloudpool_users WHERE username_hash = ?", usernameHash).Scan(&fails)
	fails++

	var lockedUntil *time.Time
	isLocked := false
	if fails >= 5 {
		lockTime := time.Now().Add(15 * time.Minute)
		lockedUntil = &lockTime
		isLocked = true
	}

	_, err := s.db.Exec("UPDATE cloudpool_users SET failed_login_count = ?, locked_until = ?, updated_at = ? WHERE username_hash = ?",
		fails, lockedUntil, time.Now(), usernameHash)
	return fails, isLocked, err
}



func (s *DB) ResetLoginFailure(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	masterKey := s.getMasterKey()
	usernameHash := core.BlindIndexHash(masterKey, username)
	
	_, err := s.db.Exec("UPDATE cloudpool_users SET failed_login_count = 0, locked_until = NULL, last_login_at = ?, updated_at = ? WHERE username_hash = ?",
		time.Now(), time.Now(), usernameHash)
	return err
}



func (s *DB) UpdateUserDisplayName(id, displayName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	masterKey := s.getMasterKey()
	encDisplay := core.EncryptSecret(masterKey, displayName)
	_, err := s.db.Exec("UPDATE cloudpool_users SET display_name = ?, updated_at = ? WHERE id = ?", encDisplay, time.Now(), id)
	return err
}



func (s *DB) UpdateUserUsage(id string, usedDelta int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("UPDATE cloudpool_users SET used_bytes = used_bytes + ?, updated_at = ? WHERE id = ?", usedDelta, time.Now(), id)
	if err == nil {
		s.InvalidateStatsCache()
	}
	return err
}

// -------------------------------------------------------------
// SQL Studio Database Inspector
// -------------------------------------------------------------



func (s *DB) LogLoginSession(sess *models.LoginSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sess.ID == "" {
		sess.ID = "sess_" + uuid.New().String()
	}
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = time.Now()
	}

	_, err := s.db.Exec(`INSERT INTO login_sessions (id, user_id, username, ip_address, device_info, location_info, status, user_agent, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.UserID, sess.Username, sess.IPAddress, sess.DeviceInfo, sess.LocationInfo, sess.Status, sess.UserAgent, sess.CreatedAt)
	return err
}



func (s *DB) ListLoginSessions(limit int) ([]models.LoginSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.Query(`SELECT id, COALESCE(user_id, ''), COALESCE(username, ''), COALESCE(ip_address, ''), COALESCE(device_info, ''), COALESCE(location_info, ''), COALESCE(status, ''), COALESCE(user_agent, ''), created_at 
		FROM login_sessions ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]models.LoginSession, 0)
	for rows.Next() {
		var s models.LoginSession
		var rawCreatedAt interface{}
		if err := rows.Scan(&s.ID, &s.UserID, &s.Username, &s.IPAddress, &s.DeviceInfo, &s.LocationInfo, &s.Status, &s.UserAgent, &rawCreatedAt); err == nil {
			s.CreatedAt = parseFlexibleTime(rawCreatedAt)
			list = append(list, s)
		}
	}
	return list, nil
}

// -------------------------------------------------------------
// Single-Use OTP & File Access Management
// -------------------------------------------------------------

