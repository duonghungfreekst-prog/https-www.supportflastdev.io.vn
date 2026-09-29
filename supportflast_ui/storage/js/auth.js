// ==========================================================================
// CloudPool User Authentication & Account Management Controller
// ==========================================================================

const AuthManager = {
  currentUser: null,
  turnstileWidgetId: null,
  turnstileToken: '',
  turnstileSiteKey: '1x00000000000000000000AA',
  turnstileEnabled: true,

  async fetchTurnstileConfig() {
    try {
      const res = await fetch('/api/auth/turnstile-config');
      if (res.ok) {
        const data = await res.json();
        if (data.site_key) this.turnstileSiteKey = data.site_key;
        this.turnstileEnabled = (data.enabled !== false);
      }
    } catch (_) {}
  },

  renderTurnstile() {
    if (!this.turnstileEnabled) return;
    if (typeof turnstile === 'undefined') {
      setTimeout(() => this.renderTurnstile(), 300);
      return;
    }
    const container = document.getElementById('cf-turnstile-storage-login');
    if (!container) return;
    if (this.turnstileWidgetId !== null) {
      try { turnstile.reset(this.turnstileWidgetId); } catch (_) {}
      this.turnstileToken = '';
      return;
    }
    try {
      this.turnstileWidgetId = turnstile.render('#cf-turnstile-storage-login', {
        sitekey: this.turnstileSiteKey,
        theme: 'dark',
        size: 'flexible',
        callback: (token) => {
          this.turnstileToken = token;
        },
        'expired-callback': () => {
          this.turnstileToken = '';
        },
        'error-callback': () => {
          this.turnstileToken = '';
        }
      });
    } catch (e) {
      console.warn('[TURNSTILE] Storage login render err:', e);
    }
  },

  async init() {
    this.bindEvents();
    this.fetchTurnstileConfig();

    // 1. Phục hồi ngay lập tức trạng thái đăng nhập từ localStorage để chống nhấp nháy UI (Zero-flicker on F5)
    const cachedUser = API.getCurrentUser();
    if (cachedUser && (cachedUser.id || cachedUser.username)) {
      this.currentUser = cachedUser;
      if (cachedUser.role === 'admin' || cachedUser.username === 'admin') {
        App.isAdminUnlocked = true;
        sessionStorage.setItem('cloudpool_admin_session', 'true');
      }
      this.updateUserUI();
    }

    // 2. Xác thực phiên làm việc trực tiếp với backend (Server validation)
    try {
      const res = await API.getMe();
      const me = (res && res.user) ? res.user : res;
      if (me && (me.id || me.username) && me.role) {
        this.currentUser = me;
        API.setCurrentUser(me);
        if (me.role === 'admin' || me.username === 'admin') {
          App.isAdminUnlocked = true;
          sessionStorage.setItem('cloudpool_admin_session', 'true');
        } else {
          App.isAdminUnlocked = false;
          sessionStorage.removeItem('cloudpool_admin_session');
        }
      } else {
        this.currentUser = null;
        API.setCurrentUser(null);
        App.isAdminUnlocked = false;
        sessionStorage.removeItem('cloudpool_admin_session');
      }
    } catch (err) {
      console.warn('[AUTH] Kiểm tra phiên /api/auth/me:', err);
      const errMsg = (err && err.message) ? err.message.toLowerCase() : '';
      if (errMsg.includes('401') || errMsg.includes('chưa đăng nhập') || errMsg.includes('hết hạn') || errMsg.includes('unauthorized') || errMsg.includes('403')) {
        this.currentUser = null;
        API.setCurrentUser(null);
        API.setToken(null);
        App.isAdminUnlocked = false;
        sessionStorage.removeItem('cloudpool_admin_session');
      } else {
        console.log('[AUTH] Duy trì phiên đăng nhập từ bộ nhớ đệm (Chống văng khi mạng chậm hoặc reload)');
      }
    }

    this.updateUserUI();
  },

  bindEvents() {
    // Topbar Auth Modal Trigger
    const btnOpen = document.getElementById('btn-open-auth-modal');
    const avatarBadge = document.getElementById('user-avatar-badge');

    if (btnOpen) {
      btnOpen.addEventListener('click', () => {
        if (this.currentUser) {
          this.logout();
        } else {
          this.openLoginModal();
        }
      });
    }

    if (avatarBadge) {
      avatarBadge.addEventListener('click', () => {
        if (this.currentUser) {
          if (confirm(`Bạn đang đăng nhập với tài khoản: ${this.currentUser.username} (${this.currentUser.display_name}). Bạn có muốn đăng xuất không?`)) {
            this.logout();
          }
        } else {
          this.openLoginModal();
        }
      });
    }

    // Login Form Submit (Chỉ duy nhất Đăng Nhập Tài Khoản)
    const formLogin = document.getElementById('form-login');
    if (formLogin) {
      formLogin.addEventListener('submit', async (e) => {
        e.preventDefault();
        const username = document.getElementById('login-username').value.trim();
        const password = document.getElementById('login-password').value;

        let logToken = this.turnstileToken;
        if (!logToken && typeof turnstile !== 'undefined' && this.turnstileWidgetId !== null) {
          logToken = turnstile.getResponse(this.turnstileWidgetId);
        }
        if (this.turnstileEnabled && !logToken) {
          Toast.error('Vui lòng xác thực mã chống Bot (Cloudflare Turnstile) trước khi đăng nhập!');
          return;
        }

        try {
          const res = await API.login(username, password, logToken);
          const userData = (res && res.user) ? res.user : res;
          this.currentUser = userData;
          API.setCurrentUser(userData);
          if (res.token) {
            API.setToken(res.token);
          }
          this.closeAuthModal();
          this.updateUserUI();

          // If admin, auto-unlock admin mode
          if (userData.role === 'admin' || userData.username === 'admin') {
            App.isAdminUnlocked = true;
            sessionStorage.setItem('cloudpool_admin_session', 'true');
          } else {
            App.isAdminUnlocked = false;
            sessionStorage.removeItem('cloudpool_admin_session');
          }

          App.applyAdminState();
          FilesManager.loadFiles('root');
          Toast.success(`Chào mừng trở lại, ${userData.display_name || userData.username}!`);
        } catch (err) {
          Toast.error(err.message);
          if (typeof turnstile !== 'undefined' && this.turnstileWidgetId !== null) {
            try { turnstile.reset(this.turnstileWidgetId); } catch (_) {}
            this.turnstileToken = '';
          }
        }
      });
    }

    // Admin Add User Form Submit (Dành riêng cho Quản trị viên trong trang Quản lý Người Dùng)
    const formAdminAddUser = document.getElementById('form-admin-add-user');
    if (formAdminAddUser) {
      formAdminAddUser.addEventListener('submit', async (e) => {
        e.preventDefault();
        const username = document.getElementById('admin-new-username').value.trim();
        const displayName = document.getElementById('admin-new-displayname').value.trim();
        const password = document.getElementById('admin-new-password').value;
        const quotaGb = parseFloat(document.getElementById('admin-new-quota').value) || 0;
        const quotaBytes = quotaGb > 0 ? Math.round(quotaGb * 1024 * 1024 * 1024) : 0;

        try {
          const res = await API.register(username, password, displayName);
          if (quotaBytes > 0 && res.user && res.user.id) {
            try { await API.updateUserQuota(res.user.id, quotaBytes); } catch (_) {}
          }
          this.closeAdminAddUserModal();
          this.loadUsers();
          Toast.success(`Đã tạo tài khoản thành viên '${displayName || username}' thành công!`);
          formAdminAddUser.reset();
        } catch (err) {
          Toast.error('Lỗi tạo tài khoản: ' + err.message);
        }
      });
    }

    // Change Password in Settings View
    const formChangePass = document.getElementById('form-change-password');
    if (formChangePass) {
      formChangePass.addEventListener('submit', async (e) => {
        e.preventDefault();
        const oldPass = document.getElementById('cp-old-pass').value;
        const newPass = document.getElementById('cp-new-pass').value;
        const confirmPass = document.getElementById('cp-confirm-pass').value;

        if (newPass !== confirmPass) {
          Toast.error('Xác nhận mật khẩu mới không khớp');
          return;
        }

        try {
          await API.changePassword(oldPass, newPass);
          formChangePass.reset();
          Toast.success('Đã cập nhật mật khẩu thành công!');
        } catch (err) {
          Toast.error('Lỗi đổi mật khẩu: ' + err.message);
        }
      });
    }

    // Change Password in Modal
    const modalFormChangePass = document.getElementById('modal-form-change-password');
    if (modalFormChangePass) {
      modalFormChangePass.addEventListener('submit', async (e) => {
        e.preventDefault();
        const oldPass = document.getElementById('mcp-old-pass').value;
        const newPass = document.getElementById('mcp-new-pass').value;
        const confirmPass = document.getElementById('mcp-confirm-pass').value;

        if (newPass !== confirmPass) {
          Toast.error('Xác nhận mật khẩu mới không khớp');
          return;
        }

        try {
          await API.changePassword(oldPass, newPass);
          modalFormChangePass.reset();
          const modal = document.getElementById('modal-change-password');
          if (modal) modal.classList.remove('active');
          Toast.success('Đã đổi mật khẩu tài khoản thành công!');
        } catch (err) {
          Toast.error('Lỗi đổi mật khẩu: ' + err.message);
        }
      });
    }

    // Security PIN Form in Multi-Tier Security Center
    const formPin = document.getElementById('form-security-pin');
    if (formPin) {
      formPin.addEventListener('submit', async (e) => {
        e.preventDefault();
        const oldPin = document.getElementById('pin-old').value.trim();
        const newPin = document.getElementById('pin-new').value.trim();
        const tier = parseInt(document.getElementById('pin-tier').value, 10) || 2;

        if (newPin.length !== 6 || isNaN(newPin)) {
          Toast.error('Mã PIN cấp 2 phải có đúng 6 chữ số');
          return;
        }

        try {
          const res = await API.setSecurityPin(oldPin, newPin, tier);
          formPin.reset();
          Toast.success(res.message || 'Đã cài đặt mã PIN cấp 2 thành công!');
        } catch (err) {
          Toast.error('Lỗi cài đặt mã PIN: ' + err.message);
        }
      });
    }
  },

  openChangePasswordModal() {
    if (!this.currentUser) {
      this.openLoginModal();
      return;
    }
    const modal = document.getElementById('modal-change-password');
    if (modal) modal.classList.add('active');
  },

  openLoginModal() {
    const modal = document.getElementById('modal-auth');
    if (modal) modal.classList.add('active');
    this.fetchTurnstileConfig().then(() => {
      setTimeout(() => this.renderTurnstile(), 60);
    });
  },

  openRegisterModal() {
    // Đã bỏ hoàn toàn đăng ký công khai - chuyển sang đăng nhập
    this.openLoginModal();
  },

  openAdminAddUserModal() {
    const modal = document.getElementById('modal-admin-add-user');
    if (modal) modal.classList.add('active');
  },

  closeAdminAddUserModal() {
    const modal = document.getElementById('modal-admin-add-user');
    if (modal) modal.classList.remove('active');
  },

  closeAuthModal() {
    const modal = document.getElementById('modal-auth');
    if (modal) modal.classList.remove('active');
  },

  async logout() {
    this.currentUser = null;
    API.setCurrentUser(null);
    App.isAdminUnlocked = false;
    sessionStorage.removeItem('cloudpool_admin_session');
    localStorage.removeItem('cloudpool_current_user');
    try {
      await API.logout();
    } catch (_) {}
    this.updateUserUI();
    App.applyAdminState();
    App.switchTab('files');
    FilesManager.loadFiles('root');
    Toast.info('Đã đăng xuất tài khoản an toàn.');
  },

  updateUserUI() {
    const nameEl = document.getElementById('user-display-name');
    const roleEl = document.getElementById('user-role-badge');
    const avatarEl = document.getElementById('user-avatar-badge');
    const btnLabel = document.getElementById('auth-btn-label');

    if (this.currentUser) {
      const name = this.currentUser.display_name || this.currentUser.username;
      if (nameEl) nameEl.textContent = name;
      if (roleEl) {
        roleEl.textContent = this.currentUser.role === 'admin' ? '🛡️ Quản trị viên' : '👤 Thành viên';
        roleEl.style.color = this.currentUser.role === 'admin' ? 'var(--accent-amber)' : 'var(--accent-blue)';
      }
      if (avatarEl) {
        avatarEl.textContent = name.charAt(0).toUpperCase();
        avatarEl.style.background = this.currentUser.role === 'admin' ? 'var(--accent-amber)' : 'var(--accent-blue)';
      }
      if (btnLabel) btnLabel.textContent = '🚪 Đăng xuất';
    } else {
      if (nameEl) nameEl.textContent = 'Khách';
      if (roleEl) {
        roleEl.textContent = 'Chưa đăng nhập';
        roleEl.style.color = 'var(--text-muted)';
      }
      if (avatarEl) {
        avatarEl.textContent = '?';
        avatarEl.style.background = 'var(--bg-tertiary)';
      }
      if (btnLabel) btnLabel.textContent = '🔑 Đăng nhập';
    }

    if (typeof App !== 'undefined' && App.applyAdminState) {
      App.applyAdminState();
    }
  },

  // -------------------------------------------------------------
  // Admin: Users Management Tab
  // -------------------------------------------------------------
  async loadUsers() {
    const tbody = document.getElementById('users-table-body');
    if (!tbody) return;

    try {
      const users = await API.getAdminUsers();
      if (typeof FilesManager !== 'undefined' && FilesManager.populateUserFilter) {
        FilesManager.populateUserFilter(users);
      }
      if (!Array.isArray(users) || users.length === 0) {
        tbody.innerHTML = `<tr><td colspan="7" style="text-align: center; padding: 24px; color: var(--text-muted);">Không tìm thấy người dùng.</td></tr>`;
        return;
      }

      let html = '';
      users.forEach(u => {
        const isAdmin = u.role === 'admin';
        const usedFormatted = Utils.formatBytes(u.used_bytes || 0);
        const quotaFormatted = u.quota_bytes > 0 ? Utils.formatBytes(u.quota_bytes) : 'Không giới hạn';
        const roleBadge = isAdmin
          ? `<span class="account-status-badge badge-active" style="background: rgba(210,153,34,0.15); color: var(--accent-amber);">Quản trị viên</span>`
          : `<span class="account-status-badge badge-active">Thành viên</span>`;

        let formatEncrypted = (str) => {
          if (!str || !str.startsWith('ENC:')) return str || '-';
          return `<span style="color: var(--accent-green); font-size: 12px; display: inline-flex; align-items: center; gap: 4px;" title="Dữ liệu đã được mã hoá bảo mật">
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="11" width="18" height="11" rx="2" ry="2"></rect><path d="M7 11V7a5 5 0 0 1 10 0v4"></path></svg>
                    Bảo mật ẩn danh (***${str.slice(-4)})
                  </span>`;
        };
        const displayUsername = formatEncrypted(u.username);
        const displayDisplayName = formatEncrypted(u.display_name);
        const shortUser = u.username && u.username.startsWith('ENC:') ? '***' + u.username.slice(-4) : u.username;

        html += `
          <tr>
            <td style="font-weight: 600; color: var(--text-primary);"><span style="display: flex; align-items: center; gap: 6px;">👤 ${displayUsername}</span></td>
            <td>${displayDisplayName}</td>
            <td>${roleBadge}</td>
            <td>${usedFormatted}</td>
            <td>${quotaFormatted}</td>
            <td>${Utils.formatDate(u.created_at)}</td>
            <td>
              <div style="display: flex; gap: 6px;">
                ${!isAdmin ? `
                  <button class="btn btn-secondary btn-sm" style="font-size: 11px; padding: 2px 6px;" onclick="AuthManager.promptChangeQuota('${u.id}', '${shortUser}')">Đổi hạn mức</button>
                  <button class="btn btn-secondary btn-sm" style="font-size: 11px; padding: 2px 6px;" onclick="AuthManager.promptResetPassword('${u.id}', '${shortUser}')">Đổi MK</button>
                  <button class="btn btn-danger btn-sm" style="font-size: 11px; padding: 2px 6px;" onclick="AuthManager.confirmDeleteUser('${u.id}', '${shortUser}')">Xóa</button>
                ` : `
                  <button class="btn btn-secondary btn-sm" style="font-size: 11px; padding: 2px 6px;" onclick="AuthManager.openChangePasswordModal()">Đổi MK</button>
                `}
              </div>
            </td>
          </tr>
        `;
      });

      tbody.innerHTML = html;
    } catch (err) {
      tbody.innerHTML = `<tr><td colspan="7" style="text-align: center; padding: 24px; color: var(--accent-red);">Lỗi: ${err.message}</td></tr>`;
    }
  },

  async promptResetPassword(userId, username) {
    const newPass = prompt(`Nhập mật khẩu mới cho người dùng '${username}':`, "");
    if (!newPass) return;
    if (newPass.length < 4) {
      Toast.error('Mật khẩu mới phải có tối thiểu 4 ký tự');
      return;
    }

    try {
      await API.adminResetPassword(userId, newPass);
      Toast.success(`Đã cập nhật mật khẩu mới cho '${username}' thành công!`);
    } catch (err) {
      Toast.error('Lỗi đặt lại mật khẩu: ' + err.message);
    }
  },

  async promptChangeQuota(userId, username) {
    const input = prompt(`Nhập hạn mức dung lượng mới cho người dùng '${username}' theo Gigabyte (GB):\n(Nhập 0 nếu không giới hạn)`, "100");
    if (input === null) return;
    const gb = parseFloat(input);
    if (isNaN(gb) || gb < 0) {
      Toast.error('Dung lượng không hợp lệ');
      return;
    }

    const bytes = Math.round(gb * 1024 * 1024 * 1024);
    try {
      await API.updateUserQuota(userId, bytes);
      Toast.success(`Đã cập nhật hạn mức cho '${username}'`);
      this.loadUsers();
    } catch (err) {
      Toast.error('Lỗi cập nhật: ' + err.message);
    }
  },

  async confirmDeleteUser(userId, username) {
    if (!confirm(`Bạn có chắc chắn muốn xóa tài khoản người dùng '${username}' không?`)) return;
    try {
      await API.deleteUser(userId);
      Toast.success(`Đã xóa người dùng '${username}'`);
      this.loadUsers();
    } catch (err) {
      Toast.error('Lỗi xóa người dùng: ' + err.message);
    }
  },

  async loadLoginSessions() {
    const tbody = document.getElementById('sessions-table-body');
    if (!tbody) return;

    try {
      const list = await API.getLoginSessions();
      if (!Array.isArray(list) || list.length === 0) {
        tbody.innerHTML = `<tr><td colspan="6" style="text-align: center; padding: 20px; color: var(--text-muted);">Chưa có phiên đăng nhập nào được ghi nhận.</td></tr>`;
        return;
      }

      let html = '';
      list.forEach(s => {
        const timeStr = new Date(s.created_at).toLocaleString('vi-VN');
        let statusBadge = `<span style="color: var(--accent-green); font-weight: 600;">✓ Thành công</span>`;
        if (s.status.includes('FAILED') || s.status.includes('WRONG')) {
          statusBadge = `<span style="color: var(--accent-red); font-weight: 600;">❌ Sai Mật khẩu</span>`;
        } else if (s.status.includes('LOCK') || s.status.includes('BLOCKED')) {
          statusBadge = `<span style="color: var(--accent-amber); font-weight: 600;">🔒 Tạm khóa 15p</span>`;
        }

        html += `
          <tr>
            <td style="padding: 8px 12px; font-family: var(--font-mono); font-size: 11px;">${timeStr}</td>
            <td style="padding: 8px 12px; font-weight: 600; color: var(--accent-blue);">${s.username || 'Khách'}</td>
            <td style="padding: 8px 12px; font-family: var(--font-mono); color: var(--text-primary);"><code>${s.ip_address || '127.0.0.1'}</code></td>
            <td style="padding: 8px 12px; color: var(--text-secondary);">${s.device_info || 'Trình duyệt Web'}</td>
            <td style="padding: 8px 12px; color: var(--text-muted); font-size: 11px;">📍 ${s.location_info || 'Localhost'}</td>
            <td style="padding: 8px 12px;">${statusBadge}</td>
          </tr>
        `;
      });

      tbody.innerHTML = html;
    } catch (err) {
      tbody.innerHTML = `<tr><td colspan="6" style="text-align: center; padding: 20px; color: var(--accent-red);">Lỗi: ${err.message}</td></tr>`;
    }
  }
};
