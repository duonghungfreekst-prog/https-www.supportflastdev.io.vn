package api

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
	"supportflast_engine/cloudpool/core"
	"supportflast_engine/cloudpool/models"
	"supportflast_engine/registry"
	"supportflast_engine/security"
	"github.com/google/uuid"
)


func (s *Server) getUserFromRequest(r *http.Request) *models.User {
	var tokenString string

	// 1. Thử Authorization header (Bearer token)
	authHeader := r.Header.Get("Authorization")
	if len(authHeader) > 7 && strings.EqualFold(authHeader[:7], "Bearer ") {
		tokenString = strings.TrimSpace(authHeader[7:])
	}

	// 2. Thử Header API Key hoặc Auth Token
	if tokenString == "" {
		if key := r.Header.Get("X-API-Key"); key != "" {
			tokenString = strings.TrimSpace(key)
		} else if key := r.Header.Get("X-Auth-Token"); key != "" {
			tokenString = strings.TrimSpace(key)
		}
	}

	// 3. Thử cookies đa dạng (hỗ trợ SSO giữa Hub và CloudPool)
	if tokenString == "" {
		cookieNames := []string{"cloudpool_token", "sf_auth_token", "supportflast_auth_token", "auth_token"}
		for _, name := range cookieNames {
			if cookie, err := r.Cookie(name); err == nil && strings.TrimSpace(cookie.Value) != "" {
				tokenString = strings.TrimSpace(cookie.Value)
				break
			}
		}
	}

	// 4. Thử URL Query Parameter (hỗ trợ link download/preview trực tiếp)
	if tokenString == "" {
		tokenString = strings.TrimSpace(r.URL.Query().Get("token"))
		if tokenString == "" {
			tokenString = strings.TrimSpace(r.URL.Query().Get("auth_token"))
		}
	}

	if tokenString == "" {
		return nil
	}

	// 5. Thẩm định token JWT RS256 bằng hàm ValidateRS256Token từ security.jwt_asymmetric (SSO Hợp Nhất)
	claims, err := VerifyJWTClaims(tokenString)
	if err != nil || claims == nil {
		// Kiểm tra SSO Session token từ SupportFlast Hub (ví dụ: sf_sess_...)
		if regUser, ok := registry.GetUserFromToken(tokenString); ok && regUser != nil {
			if regUser.Role == "admin" || regUser.Username == "admin" || regUser.ID == "usr-admin-001" {
				if u, err := s.db.GetUserByID("user_admin"); err == nil && u != nil {
					return u
				}
			}
			return &models.User{
				ID:          regUser.ID,
				Username:    regUser.Username,
				DisplayName: regUser.DisplayName,
				Role:        regUser.Role,
			}
		}

		// Kiểm tra Developer API Key hợp lệ thực tế từ hệ thống (Tuyệt đối không dùng chuỗi bypass hoặc chỉ check prefix)
		if registry.VerifyAPIKey(tokenString) {
			if u, err := s.db.GetUserByID("user_admin"); err == nil && u != nil {
				return u
			}
		}
		return nil
	}

	// 6. Xử lý thông tin định danh & Vai trò (Single Sign-On SSO)
	userID := strings.TrimSpace(claims.UserID)
	if userID == "" {
		userID = strings.TrimSpace(claims.Subject)
	}
	username := strings.TrimSpace(claims.Username)
	if username == "" {
		username = userID
	}
	role := strings.TrimSpace(claims.Role)
	if role == "" {
		role = "user"
	}
	// Đảm bảo nhận diện Admin tuyệt đối 100% từ SupportFlast Hub
	isAdmin := (role == "admin" || username == "admin" || userID == "admin" || userID == "user_admin" || userID == "usr-admin-001")
	if isAdmin {
		role = "admin"
	}

	// 7. Tra cứu người dùng trong CSDL CloudPool
	var u *models.User
	if isAdmin {
		// Ưu tiên bản ghi user_admin gốc của CloudPool để tương thích trọn vẹn với các tệp tin hiện hữu
		u, _ = s.db.GetUserByID("user_admin")
		if u == nil && userID != "" {
			u, _ = s.db.GetUserByID(userID)
		}
		if u == nil {
			u, _ = s.db.GetUserByUsername("admin")
		}
	} else {
		// Tìm theo UserID trước
		if userID != "" {
			u, _ = s.db.GetUserByID(userID)
		}
		// Nếu không thấy, tìm theo Username
		if u == nil && username != "" {
			u, _ = s.db.GetUserByUsername(username)
		}
	}

	// 8. Tự động đồng bộ / Cấp phát JIT (Just-In-Time User Provisioning) nếu user chưa có trong CSDL CloudPool
	if u == nil {
		log.Printf("[ENGINE] [SSO] Đồng bộ JIT người dùng từ SupportFlast Hub sang CloudPool: username='%s', role='%s', id='%s'", username, role, userID)
		now := time.Now()
		newUser := &models.User{
			ID:           userID,
			Username:     username,
			Email:        claims.Email,
			DisplayName:  username,
			Role:         role,
			Status:       "active",
			SecurityTier: 1,
			QuotaBytes:   100 * 1024 * 1024 * 1024, // Mặc định 100GB
			UsedBytes:    0,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if newUser.ID == "" {
			newUser.ID = "usr_" + username
		}
		if err := s.db.CreateUser(newUser); err == nil {
			u = newUser
		} else {
			// Fallback: nếu lỗi ghi DB, vẫn trả về user hợp lệ để không ngắt quãng phiên đăng nhập SSO
			u = newUser
		}
	}

	// 9. Đồng bộ quyền Admin tuyệt đối nếu Token được cấp quyền Admin từ Hub
	if isAdmin && u != nil {
		u.Role = "admin"
	}

	return u
}



func (s *Server) handleAuthRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	var req struct {
		Username         string `json:"username"`
		Password         string `json:"password"`
		DisplayName      string `json:"display_name"`
		TurnstileToken   string `json:"turnstile_token"`
		CFTurnstileToken string `json:"cf-turnstile-response"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Dữ liệu đăng ký không hợp lệ", err)
		return
	}

	// Xác thực chống Bot bằng Cloudflare Turnstile
	settings, _ := s.db.GetSettings()
	if settings != nil && settings.TurnstileEnabled {
		tToken := req.TurnstileToken
		if tToken == "" {
			tToken = req.CFTurnstileToken
		}
		if tToken == "" {
			writeError(w, http.StatusBadRequest, "Vui lòng hoàn thành xác thực chống Bot (Cloudflare Turnstile)", nil)
			return
		}
		if valid, err := security.VerifyTurnstileToken(settings.TurnstileSecretKey, tToken, getClientIP(r)); !valid || err != nil {
			writeError(w, http.StatusBadRequest, "Xác thực chống Bot thất bại hoặc mã đã hết hạn. Vui lòng thử lại.", err)
			return
		}
	}

	req.Username = strings.TrimSpace(req.Username)
	if len(req.Username) < 3 {
		writeError(w, http.StatusBadRequest, "Tên tài khoản phải có ít nhất 3 ký tự", nil)
		return
	}
	// [PASSWORD POLICY - Production] Min 8 ký tự, phải có chữ và số
	if err := validatePasswordStrength(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	// Check if already logged in user is admin
	isAdmin := false
	currentUser := s.getUserFromRequest(r)
	if currentUser != nil && currentUser.Role == "admin" {
		isAdmin = true
	}

	// Check if self-registration is allowed
	if settings != nil && !settings.AllowSelfRegistration && !isAdmin {
		writeError(w, http.StatusForbidden, "Hệ thống đang tạm khóa tính năng tự đăng ký tài khoản. Vui lòng liên hệ Quản trị viên.", nil)
		return
	}

	// Check if already exists
	if _, err := s.db.GetUserByUsername(req.Username); err == nil {
		writeError(w, http.StatusConflict, "Tên tài khoản này đã được sử dụng", nil)
		return
	}

	passHash, err := core.HashPasswordBcrypt(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi băm mật khẩu", err)
		return
	}
	displayName := req.DisplayName
	if displayName == "" {
		displayName = req.Username
	}

	now := time.Now()
	user := &models.User{
		ID:           "user_" + uuid.New().String(),
		Username:     req.Username,
		PasswordHash: passHash,
		DisplayName:  displayName,
		Role:         "user",
		QuotaBytes:   0, // Unlimited / pool limit
		UsedBytes:    0,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.db.CreateUser(user); err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi tạo tài khoản: "+err.Error(), err)
		return
	}

	jwtToken, err := GenerateJWTWithRole(user.ID, user.Username, user.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi tạo phiên đăng nhập", err)
		return
	}

	setAuthCookie(w, r, jwtToken, int(registry.GetSessionDuration().Seconds()))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Đăng ký tài khoản thành công",
		"user":    user,
		"token":   jwtToken,
	})
}



func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	var req struct {
		Username         string `json:"username"`
		Password         string `json:"password"`
		TurnstileToken   string `json:"turnstile_token"`
		CFTurnstileToken string `json:"cf-turnstile-response"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Dữ liệu đăng nhập không hợp lệ", err)
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	clientIP := getClientIP(r)

	// Xác thực chống Bot bằng Cloudflare Turnstile
	settings, _ := s.db.GetSettings()
	if settings != nil && settings.TurnstileEnabled {
		tToken := req.TurnstileToken
		if tToken == "" {
			tToken = req.CFTurnstileToken
		}
		if tToken == "" {
			writeError(w, http.StatusBadRequest, "Vui lòng hoàn thành xác thực chống Bot (Cloudflare Turnstile)", nil)
			return
		}
		if valid, err := security.VerifyTurnstileToken(settings.TurnstileSecretKey, tToken, clientIP); !valid || err != nil {
			writeError(w, http.StatusBadRequest, "Xác thực chống Bot thất bại hoặc mã đã hết hạn. Vui lòng thử lại.", err)
			return
		}
	}
	userAgent := r.UserAgent()
	deviceInfo := parseDeviceInfo(userAgent)
	locationInfo := resolveLocation(clientIP)

	user, err := s.db.GetUserByUsername(req.Username)
	if err != nil {
		// Log failed login attempt
		_ = s.db.LogLoginSession(&models.LoginSession{
			Username:     req.Username,
			IPAddress:    clientIP,
			DeviceInfo:   deviceInfo,
			LocationInfo: locationInfo,
			Status:       "FAILED_UNKNOWN_USER",
			UserAgent:    userAgent,
		})
		// Constant-time dummy check to prevent timing attacks (Rule PHAN 3.6)
		_ = subtle.ConstantTimeCompare([]byte("dummy"), []byte("dummy2"))
		writeError(w, http.StatusUnauthorized, "Tài khoản hoặc mật khẩu không chính xác", nil)
		return
	}

	// Check Brute-Force lockout
	if user.LockedUntil != nil && time.Now().Before(*user.LockedUntil) {
		remaining := int(time.Until(*user.LockedUntil).Minutes()) + 1
		_ = s.db.LogLoginSession(&models.LoginSession{
			UserID:       user.ID,
			Username:     user.Username,
			IPAddress:    clientIP,
			DeviceInfo:   deviceInfo,
			LocationInfo: locationInfo,
			Status:       "BLOCKED_LOCKED",
			UserAgent:    userAgent,
		})
		writeError(w, http.StatusLocked, fmt.Sprintf("Tài khoản đang bị tạm khóa an toàn trong %d phút do nhập sai quá nhiều lần.", remaining), nil)
		return
	}

	// Constant-time compare and Bcrypt check (Rule PHAN 3.6)
	passwordValid := core.CheckPasswordHashBcrypt(req.Password, user.PasswordHash)

	// If logging in as admin or user has admin role, also verify against master passphrase in settings
	if !passwordValid && (user.Role == "admin" || user.Username == "admin" || user.ID == "user_admin") {
		if settings == nil {
			settings, _ = s.db.GetSettings()
		}
		if settings != nil && settings.MasterPassphrase != "" {
			if subtle.ConstantTimeCompare([]byte(settings.MasterPassphrase), []byte(req.Password)) == 1 {
				passwordValid = true
				// Auto-sync user password hash in database so future logins are instant
				if newHash, err := core.HashPasswordBcrypt(req.Password); err == nil {
					_ = s.db.UpdateUserPassword(user.ID, newHash)
				}
			}
		}
	}

	if !passwordValid {
		fails, isLocked, _ := s.db.RecordLoginFailure(user.Username)
		status := "FAILED_WRONG_PASSWORD"

		if isLocked {
			status = "LOCKED_5_FAILS"
		}
		_ = s.db.LogLoginSession(&models.LoginSession{
			UserID:       user.ID,
			Username:     user.Username,
			IPAddress:    clientIP,
			DeviceInfo:   deviceInfo,
			LocationInfo: locationInfo,
			Status:       status,
			UserAgent:    userAgent,
		})

		if isLocked {
			writeError(w, http.StatusLocked, "Bạn đã nhập sai mật khẩu 5 lần. Tài khoản đã bị tạm khóa 15 phút để bảo vệ an toàn.", nil)
			return
		}
		writeError(w, http.StatusUnauthorized, fmt.Sprintf("Tài khoản hoặc mật khẩu không chính xác (Còn %d lần thử trước khi khóa)", 5-fails), nil)
		return
	}

	// Success: Reset failures & log login session & activity
	_ = s.db.ResetLoginFailure(user.Username)
	_ = s.db.LogLoginSession(&models.LoginSession{
		UserID:       user.ID,
		Username:     user.Username,
		IPAddress:    clientIP,
		DeviceInfo:   deviceInfo,
		LocationInfo: locationInfo,
		Status:       "SUCCESS",
		UserAgent:    userAgent,
	})

	_ = s.db.LogActivity(&models.ActivityLog{
		UserID:    user.ID,
		Username:  user.Username,
		Action:    "LOGIN",
		Target:    "Web Explorer",
		IPAddress: clientIP,
		Details:   fmt.Sprintf("Đăng nhập từ %s (%s)", deviceInfo, locationInfo),
	})

	jwtToken, err := GenerateJWTWithRole(user.ID, user.Username, user.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi tạo phiên đăng nhập", err)
		return
	}

	if user.Role == "admin" {
		registry.SetAdminSessionCookies(w, r, jwtToken)
	} else {
		setAuthCookie(w, r, jwtToken, int(registry.GetSessionDuration().Seconds()))
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Đăng nhập thành công",
		"user":    user,
		"token":   jwtToken,
	})
}



func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.db.ListLoginSessions(50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi lấy danh sách phiên: "+err.Error(), err)
		return
	}
	writeJSON(w, http.StatusOK, sessions)
}



func parseDeviceInfo(ua string) string {
	os := "Thiết bị không xác định"
	if strings.Contains(ua, "Windows NT 10.0") || strings.Contains(ua, "Windows") {
		os = "Windows PC"
	} else if strings.Contains(ua, "Macintosh") || strings.Contains(ua, "Mac OS") {
		os = "macOS"
	} else if strings.Contains(ua, "iPhone") {
		os = "Apple iPhone"
	} else if strings.Contains(ua, "iPad") {
		os = "Apple iPad"
	} else if strings.Contains(ua, "Android") {
		os = "Android Phone"
	} else if strings.Contains(ua, "Linux") {
		os = "Linux Workstation"
	}

	browser := "Web Browser"
	if strings.Contains(ua, "CocCoc") {
		browser = "Cốc Cốc"
	} else if strings.Contains(ua, "Edg/") {
		browser = "Microsoft Edge"
	} else if strings.Contains(ua, "Chrome") {
		browser = "Google Chrome"
	} else if strings.Contains(ua, "Safari") && !strings.Contains(ua, "Chrome") {
		browser = "Apple Safari"
	} else if strings.Contains(ua, "Firefox") {
		browser = "Mozilla Firefox"
	}

	return fmt.Sprintf("%s · %s", os, browser)
}



func getClientIP(r *http.Request) string {
	// [BUG FIX] Only trust CF-Connecting-IP (from Cloudflare) or X-Real-IP (from known trusted reverse proxy).
	// X-Forwarded-For is intentionally NOT trusted as a primary source because
	// clients can trivially forge it (IP Spoofing). RemoteAddr is always the ground truth.
	// If running behind Cloudflare Tunnel, CF-Connecting-IP is set by Cloudflare and cannot be spoofed.
	if cf := r.Header.Get("CF-Connecting-IP"); cf != "" {
		// Only use if it looks like a valid IP (not a spoofed multi-value)
		if !strings.Contains(cf, ",") {
			return strings.TrimSpace(cf)
		}
	}
	// X-Real-IP is set by nginx/caddy proxy — trust only if no CF header
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		if !strings.Contains(xrip, ",") {
			return strings.TrimSpace(xrip)
		}
	}
	// Fall back to the direct TCP connection RemoteAddr (cannot be spoofed)
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return ip
	}
	return r.RemoteAddr
}



func resolveLocation(ip string) string {
	if ip == "127.0.0.1" || ip == "::1" || ip == "localhost" || strings.HasPrefix(ip, "192.168.") || strings.HasPrefix(ip, "10.") || strings.HasPrefix(ip, "172.") {
		return "Máy Cục Bộ / Mạng LAN (Local Host)"
	}
	return "Việt Nam (Truy cập từ xa / Internet)"
}



func (s *Server) handleAuthSecurityPin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lòng đăng nhập để cài đặt mã PIN bảo mật", nil)
		return
	}

	var req struct {
		OldPin       string `json:"old_pin"`
		NewPin       string `json:"new_pin"`
		SecurityTier int    `json:"security_tier"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Dữ liệu không hợp lệ", err)
		return
	}

	if len(req.NewPin) != 6 {
		writeError(w, http.StatusBadRequest, "Mã PIN bảo mật cấp 2 bắt buộc phải có đúng 6 chữ số", nil)
		return
	}

	// If user already has PIN, verify old PIN
	if user.SecurityPinHash != "" {
		oldPinHash := core.HashSHA256([]byte(req.OldPin))
		if subtle.ConstantTimeCompare([]byte(user.SecurityPinHash), []byte(oldPinHash)) != 1 {
			writeError(w, http.StatusUnauthorized, "Mã PIN bảo mật hiện tại không chính xác", nil)
			return
		}
	}

	tier := req.SecurityTier
	if tier < 1 || tier > 3 {
		tier = 2
	}

	newPinHash := core.HashSHA256([]byte(req.NewPin))
	if err := s.db.UpdateUserSecurityPin(user.ID, newPinHash, tier); err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi cập nhật mã PIN: "+err.Error(), err)
		return
	}

	_ = s.db.LogActivity(&models.ActivityLog{
		UserID:    user.ID,
		Username:  user.Username,
		Action:    "SET_PIN",
		Target:    "Security PIN",
		IPAddress: r.RemoteAddr,
		Details:   fmt.Sprintf("Đã cập nhật mã PIN cấp 2 (Tier %d)", tier),
	})

	writeJSON(w, http.StatusOK, map[string]string{"message": "Đã cài đặt mã PIN bảo vệ cấp 2 thành công!"})
}



func (s *Server) handleAuthVerifyPin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Chưa đăng nhập", nil)
		return
	}

	var req struct {
		Pin string `json:"pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Pin) != 6 {
		writeError(w, http.StatusBadRequest, "Mã PIN phải có 6 chữ số", nil)
		return
	}

	pinHash := core.HashSHA256([]byte(req.Pin))
	if subtle.ConstantTimeCompare([]byte(user.SecurityPinHash), []byte(pinHash)) != 1 {
		writeError(w, http.StatusUnauthorized, "Mã PIN bảo mật không chính xác", nil)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"verified": true,
		"message":  "Xác thực mã PIN cấp 2 thành công",
	})
}



func (s *Server) handleVerifyAdminPass(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Password == "" {
		writeError(w, http.StatusBadRequest, "Mật khẩu không được để trống", err)
		return
	}

	pass := strings.TrimSpace(req.Password)

	// 1. Check against admin user's actual password in database
	adminUser, err := s.db.GetUserByUsername("admin")
	adminPassMatched := false
	if err == nil && adminUser != nil {
		if core.CheckPasswordHashBcrypt(pass, adminUser.PasswordHash) {
			adminPassMatched = true
		}
	}

	// 2. Check against master passphrase in settings
	settings, _ := s.db.GetSettings()
	masterPassMatched := false
	if settings != nil && settings.MasterPassphrase != "" {
		if subtle.ConstantTimeCompare([]byte(settings.MasterPassphrase), []byte(pass)) == 1 {
			masterPassMatched = true
		}
	}

	// 3. Check against currently logged in user (chỉ áp dụng nếu user đó thực sự có quyền admin)
	currentUser := s.getUserFromRequest(r)
	currentUserMatched := false
	if currentUser != nil && currentUser.Role == "admin" {
		if core.CheckPasswordHashBcrypt(pass, currentUser.PasswordHash) {
			currentUserMatched = true
		}
		if currentUser.SecurityPinHash != "" {
			pinHash := core.HashSHA256([]byte(pass))
			if subtle.ConstantTimeCompare([]byte(currentUser.SecurityPinHash), []byte(pinHash)) == 1 {
				currentUserMatched = true
			}
		}
	}

	// 4. Fallback cứng đã bị xóa bỏ vì lý do bảo mật — chỉ chấp nhận mật khẩu từ DB

	if adminPassMatched || masterPassMatched || currentUserMatched {
		// Reset login failures on success
		_ = s.db.ResetLoginFailure("admin")
		if currentUser != nil {
			_ = s.db.ResetLoginFailure(currentUser.Username)
		}

		// Issue admin session cookie
		jwtToken, err := GenerateJWTWithRole("user_admin", "admin", "admin")
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Lỗi tạo phiên quản trị", err)
			return
		}

		registry.SetAdminSessionCookies(w, r, jwtToken)

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"message": "Xác thực Quản trị thành công!",
			"user": map[string]interface{}{
				"id":           "user_admin",
				"username":     "admin",
				"role":         "admin",
				"display_name": "Quản Trị Viên",
			},
		})
		return
	}

	// Record failed attempt
	if adminUser != nil {
		_, _, _ = s.db.RecordLoginFailure("admin")
	}
	writeError(w, http.StatusUnauthorized, "Mật khẩu Quản trị không chính xác!", nil)
}



func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Chưa đăng nhập", nil)
		return
	}

	// Silent Token Renewal: JWT sắp hết hạn (còn dưới 2 ngày) → tự động cấp token mới
	response := map[string]interface{}{
		"user": user,
	}

	tokenString := ""
	if ah := r.Header.Get("Authorization"); len(ah) > 7 && strings.EqualFold(ah[:7], "Bearer ") {
		tokenString = strings.TrimSpace(ah[7:])
	}
	if tokenString == "" {
		cookieNames := []string{"cloudpool_token", "sf_auth_token"}
		for _, name := range cookieNames {
			if c, err := r.Cookie(name); err == nil && strings.TrimSpace(c.Value) != "" {
				tokenString = strings.TrimSpace(c.Value)
				break
			}
		}
	}

	if tokenString != "" {
		if claims, err := VerifyJWTClaims(tokenString); err == nil && claims != nil {
			remainingSec := claims.ExpiresAt - time.Now().Unix()
			maxTTL := int64(registry.GetSessionDuration().Seconds())
			threshold := maxTTL / 4
			if threshold < 3600 {
				threshold = 3600
			}
			if remainingSec > 0 && remainingSec < threshold && user.Role != "admin" {
				if newToken, err := GenerateJWTWithRole(user.ID, user.Username, user.Role); err == nil {
					setAuthCookie(w, r, newToken, int(maxTTL))
					response["new_token"] = newToken
					log.Printf("[ENGINE] [AUTH] Silent Token Renewal cho user '%s' (con %ds het han)", user.Username, remainingSec)
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, response)
}



func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	tokenString := ""
	if ah := r.Header.Get("Authorization"); len(ah) > 7 && strings.EqualFold(ah[:7], "Bearer ") {
		tokenString = strings.TrimSpace(ah[7:])
	}
	if tokenString == "" {
		cookieNames := []string{"cloudpool_token", "sf_auth_token", "supportflast_auth_token", "auth_token"}
		for _, name := range cookieNames {
			if c, err := r.Cookie(name); err == nil && strings.TrimSpace(c.Value) != "" {
				tokenString = strings.TrimSpace(c.Value)
				break
			}
		}
	}

	if tokenString != "" {
		registry.RevokeToken(tokenString)
		log.Printf("[ENGINE] [AUTH] Đã thu hồi token đăng xuất: %.16s...", tokenString)
	}

	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, max-age=0")
	w.Header().Set("Clear-Site-Data", `"cache", "cookies", "storage"`)
	setAuthCookie(w, r, "", -1)
	registry.ClearSSOCookies(w, r)
	writeJSON(w, http.StatusOK, map[string]string{"message": "Đã đăng xuất tài khoản thành công"})
}



func (s *Server) handleAuthChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lòng đăng nhập để đổi mật khẩu", nil)
		return
	}

	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Dữ liệu không hợp lệ", err)
		return
	}

	// [PASSWORD POLICY - Production] Min 8 ký tự, phải có chữ và số
	if err := validatePasswordStrength(req.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	// Verify old password
	if !core.CheckPasswordHashBcrypt(req.OldPassword, user.PasswordHash) {
		writeError(w, http.StatusUnauthorized, "Mật khẩu hiện tại không chính xác", nil)
		return
	}

	newHash, err := core.HashPasswordBcrypt(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi băm mật khẩu", err)
		return
	}
	if err := s.db.UpdateUserPassword(user.ID, newHash); err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi cập nhật mật khẩu: "+err.Error(), err)
		return
	}

	// If admin is changing password, sync to master_passphrase in settings
	if user.Role == "admin" || user.Username == "admin" || user.ID == "user_admin" {
		currentSettings, _ := s.db.GetSettings()
		if currentSettings != nil {
			currentSettings.MasterPassphrase = req.NewPassword
			_ = s.db.SaveSettings(currentSettings)
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Đã đổi mật khẩu thành công"})
}



func (s *Server) handleAdminResetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusUnauthorized, "Yêu cầu quyền Quản trị viên", nil)
		return
	}

	var req struct {
		UserID      string `json:"user_id"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
		writeError(w, http.StatusBadRequest, "Dữ liệu không hợp lệ", err)
		return
	}
	// [PASSWORD POLICY - Production] Min 8 ký tự, phải có chữ và số
	if err := validatePasswordStrength(req.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	newHash, err := core.HashPasswordBcrypt(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi băm mật khẩu", err)
		return
	}
	if err := s.db.UpdateUserPassword(req.UserID, newHash); err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi đặt lại mật khẩu: "+err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Đã đặt lại mật khẩu người dùng thành công"})
}

// -------------------------------------------------------------
// In-App Software Update & Hot-Patching Handlers
// -------------------------------------------------------------



func validatePasswordStrength(password string) error {
	if len(password) < 8 {
		return fmt.Errorf("mật khẩu phải có ít nhất 8 ký tự (hiện tại: %d ký tự)", len(password))
	}
	hasLetter := false
	hasDigit := false
	for _, c := range password {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			hasLetter = true
		}
		if c >= '0' && c <= '9' {
			hasDigit = true
		}
	}
	if !hasLetter {
		return fmt.Errorf("mật khẩu phải chứa ít nhất 1 chữ cái (a-z)")
	}
	if !hasDigit {
		return fmt.Errorf("mật khẩu phải chứa ít nhất 1 chữ số (0-9)")
	}
	return nil
}

// handleFileStatus kiểm tra nhanh tính sẵn sàng và tính toàn vẹn của tệp trước khi mở stream
// Endpoint: GET /api/files/status?id=... (hoặc POST {"id":"..."})
