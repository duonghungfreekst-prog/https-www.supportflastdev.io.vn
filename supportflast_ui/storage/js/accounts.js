// ==========================================================================
// CloudPool Google Drive Accounts Controller
// ==========================================================================

const AccountsManager = {
  accounts: [],
  isPrivacyMode: true, // Luôn BẬT — không thể tắt (bảo mật cố định)

  init() {
    this.bindEvents();
    // Đảm bảo localStorage không thể override
    localStorage.setItem('cloudpool_privacy_mode', 'true');
  },

  // Giữ hàm để không lỗi nếu có nơi nào gọi tới — nhưng không làm gì
  togglePrivacyMode() { /* Đã vô hiệu hóa — luôn ẩn danh */ },
  updatePrivacyButton() { /* Đã vô hiệu hóa — không có nút toggle */ },

  // Che email: chỉ giữ 1 ký tự đầu + domain bị che một phần
  maskEmail(email) {
    if (!email) return '••••••@••••••';
    const parts = email.split('@');
    if (parts.length !== 2) return '••••••';
    const name = parts[0];
    const domainParts = parts[1].split('.');
    const domainMasked = domainParts.map((seg, i) =>
      i === domainParts.length - 1 ? seg : seg[0] + '•'.repeat(Math.max(2, seg.length - 1))
    ).join('.');
    // Chỉ hiện ký tự đầu tiên + số lượng ký tự bị che
    return `${name[0]}${'•'.repeat(Math.min(name.length - 1, 6))}@${domainMasked}`;
  },

  // Che tên: chỉ hiện chữ cái đầu mỗi từ + dấu chấm
  maskName(name, email) {
    if (!name || name === email) {
      return this.maskEmail(email);
    }
    const words = name.trim().split(/\s+/);
    // Hiện chữ đầu mỗi từ + dấu chấm, không lộ độ dài thật
    return words.map(w => w[0] + '.').join(' ');
  },

  bindEvents() {
    // Add Account Buttons
    const btnTop = document.getElementById('btn-add-account-top');
    const btnMain = document.getElementById('btn-add-account-main');
    if (btnTop) btnTop.addEventListener('click', () => this.openAddModal());
    if (btnMain) btnMain.addEventListener('click', () => this.openAddModal());

    // Tab Switcher inside Modal (OAuth vs Service Account)
    const tabOAuth = document.getElementById('btn-tab-oauth');
    const tabSA = document.getElementById('btn-tab-sa');
    const secOAuth = document.getElementById('section-oauth');
    const secSA = document.getElementById('section-sa');

    if (tabOAuth && tabSA) {
      tabOAuth.addEventListener('click', () => {
        tabOAuth.className = 'btn btn-primary btn-sm';
        tabSA.className = 'btn btn-secondary btn-sm';
        secOAuth.style.display = 'block';
        secSA.style.display = 'none';
      });

      tabSA.addEventListener('click', () => {
        tabSA.className = 'btn btn-primary btn-sm';
        tabOAuth.className = 'btn btn-secondary btn-sm';
        secSA.style.display = 'block';
        secOAuth.style.display = 'none';
      });
    }

    // Start OAuth Login
    const btnOAuthLogin = document.getElementById('btn-start-oauth-login');
    if (btnOAuthLogin) {
      btnOAuthLogin.addEventListener('click', () => this.startOAuthFlow());
    }

    // Upload Service Account JSON
    const btnUploadSA = document.getElementById('btn-upload-sa');
    if (btnUploadSA) {
      btnUploadSA.addEventListener('click', () => this.handleServiceAccountUpload());
    }

    // Refresh All Quotas
    const btnRefreshAll = document.getElementById('btn-refresh-all-quotas');
    if (btnRefreshAll) {
      btnRefreshAll.addEventListener('click', () => this.refreshAllQuotas());
    }
  },

  getCanonicalRedirectURI() {
    let origin = window.location.origin;
    try {
      const u = new URL(origin);
      if (u.hostname.startsWith('www.')) {
        u.hostname = u.hostname.substring(4);
        origin = u.origin;
      }
    } catch (e) {}
    return origin + '/api/accounts/oauth/callback';
  },

  openAddModal() {
    const modal = document.getElementById('modal-add-account');
    if (modal) modal.classList.add('active');

    const uriPreview = document.getElementById('oauth-redirect-preview');
    if (uriPreview) {
      const redirectURI = this.getCanonicalRedirectURI();
      uriPreview.textContent = redirectURI;
      uriPreview.setAttribute('data-uri', redirectURI);
    }
  },

  copyRedirectURI() {
    const uriPreview = document.getElementById('oauth-redirect-preview');
    const uri = (uriPreview && uriPreview.getAttribute('data-uri')) || this.getCanonicalRedirectURI();
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(uri).then(() => {
        Toast.success('Đã sao chép Canonical Redirect URI vào Clipboard!');
      }).catch(() => {
        Toast.info('URI: ' + uri);
      });
    } else {
      prompt('Sao chép OAuth Redirect URI:', uri);
    }
  },

  async loadAccounts() {
    try {
      this.accounts = await API.getAccounts();
      this.renderAccounts();
      this.updatePrivacyButton();
      if (typeof FilesManager !== 'undefined' && FilesManager.populateAccountFilter) {
        FilesManager.populateAccountFilter(this.accounts);
      }
    } catch (err) {
      this.accounts = [];
      this.renderAccounts();
      Toast.error('Không thể tải danh sách tài khoản: ' + err.message);
    }
  },

  renderAccounts() {
    const dashGrid = document.getElementById('dashboard-accounts-grid');
    const manageGrid = document.getElementById('accounts-management-grid');

    const accountsList = Array.isArray(this.accounts) ? this.accounts : [];
    const html = accountsList.length === 0
      ? `<div style="grid-column: 1/-1; text-align: center; padding: 40px; color: var(--text-secondary); background: var(--bg-secondary); border: 1px dashed var(--border-subtle); border-radius: var(--radius-md);">
           <p style="font-size: 14px; font-weight: 500; margin-bottom: 10px;">Chưa có tài khoản Google Drive nào được kết nối.</p>
           <button class="btn btn-primary btn-sm" onclick="AccountsManager.openAddModal()">Thêm Google Drive ngay</button>
         </div>`
      : this.accounts.map((acc, idx) => this.buildAccountCardHTML(acc, idx + 1)).join('');

    if (dashGrid) dashGrid.innerHTML = html;
    if (manageGrid) manageGrid.innerHTML = html;
  },

  buildAccountCardHTML(acc, index = 1) {
    const usedFormatted = Utils.formatBytes(acc.used_quota_bytes);
    const totalFormatted = Utils.formatBytes(acc.total_quota_bytes);
    const percent = Math.min(100, Math.round(acc.usage_percent || 0));
    const isPaused = acc.status === 'paused';
    const badgeClass = isPaused ? 'badge-full' : (acc.status === 'active' ? 'badge-active' : (acc.status === 'full' ? 'badge-full' : 'badge-error'));
    const badgeText = isPaused ? 'Tạm dừng' : (acc.status === 'active' ? 'Hoạt động' : (acc.status === 'full' ? 'Đã đầy' : 'Lỗi'));
    const authTypeLabel = acc.auth_type === 'service_account' ? 'Service Account' : 'Google OAuth';

    const displayName = this.maskName(acc.name, acc.email);
    const displayEmail = this.maskEmail(acc.email);
    const avatarDisplay = this.isPrivacyMode
      ? `<div style="background: rgba(56, 189, 248, 0.15); color: #38bdf8; display:flex; align-items:center; justify-content:center; width:100%; height:100%; border-radius:50%; font-size:14px; font-weight:700;">🔒</div>`
      : (acc.avatar_url ? `<img src="${acc.avatar_url}" alt="Avatar">` : acc.email.charAt(0).toUpperCase());

    const numStr = index < 10 ? '0' + index : index;

    return `
      <div class="account-card" style="${isPaused ? 'opacity: 0.7;' : ''}">
        <div class="account-card-header">
          <!-- Số thứ tự STT nổi bật -->
          <div class="account-index-badge" title="Tài khoản Google Drive thứ ${index}">#${numStr}</div>
          
          <div class="account-avatar">
            ${avatarDisplay}
          </div>
          <div class="account-meta">
            <div class="account-name" title="${this.isPrivacyMode ? 'Đã che giấu an toàn' : (acc.name || acc.email)}">${displayName}</div>
            <div class="account-email" style="font-family: var(--font-mono); font-size: 11.5px;" title="${this.isPrivacyMode ? 'Đã che giấu an toàn' : acc.email}">${displayEmail}</div>
            <div style="display: flex; gap: 6px; align-items: center; margin-top: 3px; flex-wrap: wrap;">
              <span class="account-type-pill">${authTypeLabel}</span>
              ${acc.is_upload_excluded ? `
                <span style="display: inline-flex; align-items: center; gap: 4px; background: rgba(245, 158, 11, 0.15); border: 1px solid rgba(245, 158, 11, 0.35); color: #fbbf24; padding: 1px 7px; border-radius: 999px; font-size: 10.5px; font-weight: 700;" title="Đã né lưu trữ khi tải lên để tránh bị đầy tài khoản. Dành riêng cho sao lưu hệ thống &amp; đọc dữ liệu cũ.">
                  🛡️ Né Upload (Chống đầy)
                </span>
              ` : `
                <span style="display: inline-flex; align-items: center; gap: 4px; background: rgba(16, 185, 129, 0.12); border: 1px solid rgba(16, 185, 129, 0.3); color: #34d399; padding: 1px 7px; border-radius: 999px; font-size: 10.5px; font-weight: 600;" title="Sẵn sàng nhận các mảnh tệp tin khi người dùng tải lên">
                  ☁️ Sẵn sàng Upload
                </span>
              `}
            </div>
          </div>
          <span class="account-status-badge ${badgeClass}" title="${acc.last_error ? `Lỗi: ` + acc.last_error : badgeText}">${badgeText}</span>
        </div>

        <div style="margin-top: 4px; background: rgba(0,0,0,0.15); padding: 8px 10px; border-radius: var(--radius-sm); border: 1px solid var(--border-subtle);">
          <div style="display: flex; justify-content: space-between; font-size: 11.5px; margin-bottom: 5px; color: var(--text-secondary);">
            <span>Đã dùng: <b style="color: var(--text-primary); font-family: var(--font-mono);">${usedFormatted}</b> / <span style="font-family: var(--font-mono);">${totalFormatted}</span></span>
            <span style="font-weight: 700; color: ${percent > 85 ? '#f87171' : (percent > 60 ? '#facc15' : '#38bdf8')}; font-family: var(--font-mono);">${percent}%</span>
          </div>
          <div class="storage-bar-bg" style="height: 6px; border-radius: 3px; background: rgba(255,255,255,0.06);">
            <div class="storage-bar-fill" style="width: ${percent}%; height: 100%; border-radius: 3px; background: ${percent > 85 ? 'linear-gradient(90deg, #f59e0b, #ef4444)' : 'linear-gradient(90deg, #38bdf8, #2563eb)'};"></div>
          </div>
        </div>

        <div style="display: flex; justify-content: space-between; align-items: center; gap: 6px; margin-top: 4px; flex-wrap: wrap;">
          <div style="display: flex; gap: 4px;">
            <button class="btn btn-secondary btn-sm" onclick="FilesManager.filterByAccount('${acc.id}')" title="Xem danh sách tệp lưu trên tài khoản này">
              <span>📁 Xem tệp</span>
            </button>
            <button class="btn btn-secondary btn-sm" onclick="AccountsManager.importAccountDriveFiles('${acc.id}')" title="Quét và tự động nạp các tệp có sẵn từ tài khoản Drive này vào CloudPool">
              <span style="color: #38bdf8;">⚡ Nạp tệp</span>
            </button>
          </div>
          <div style="display: flex; gap: 4px;">
            <button class="btn btn-secondary btn-sm" onclick="AccountsManager.toggleAccount('${acc.id}')" title="${isPaused ? 'Kích hoạt lại' : 'Tạm dừng ghi'}">
              <span>${isPaused ? '▶ Bật' : '⏸ Tắt'}</span>
            </button>
            <button class="btn btn-secondary btn-sm" onclick="AccountsManager.refreshAccount('${acc.id}')" title="Cập nhật dung lượng">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="23 4 23 10 17 10"></polyline><polyline points="1 20 1 14 7 14"></polyline><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"></path></svg>
            </button>
            <button class="btn btn-danger btn-sm" onclick="AccountsManager.deleteAccount('${acc.id}')" title="Xóa tài khoản">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"></polyline><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path></svg>
            </button>
          </div>
        </div>
      </div>
    `;
  },

  async toggleAccount(id) {
    try {
      await API.toggleAccount(id);
      Toast.success('Đã cập nhật trạng thái tài khoản');
      this.loadAccounts();
      App.refreshStats();
    } catch (err) {
      Toast.error(err.message);
    }
  },

  async startOAuthFlow() {
    try {
      const res = await API.getOAuthURL();
      if (res.auth_url) {
        window.location.href = res.auth_url;
      }
    } catch (err) {
      const errMsg = err.message || '';
      if (errMsg.includes('Client ID') || errMsg.includes('Cài đặt')) {
        Toast.error('Chưa cấu hình Google Client ID! Đang chuyển đến trang Cài đặt...', 4000);
        setTimeout(() => {
          const modal = document.getElementById('modal-add-account');
          if (modal) modal.classList.remove('active');
          if (typeof App !== 'undefined') {
            App.switchTab('settings');
            const inp = document.getElementById('set-google-client-id');
            if (inp) {
              inp.focus();
              inp.scrollIntoView({ behavior: 'smooth', block: 'center' });
            }
          }
        }, 1200);
      } else {
        Toast.error(errMsg || 'Lỗi khởi tạo đăng nhập Google OAuth');
      }
    }
  },

  async handleServiceAccountUpload() {
    const fileInput = document.getElementById('sa-file-input');
    if (!fileInput || !fileInput.files || fileInput.files.length === 0) {
      Toast.error('Vui lòng chọn tệp JSON của Service Account');
      return;
    }

    try {
      Toast.info('Đang xác thực Service Account với Google...');
      const file = fileInput.files[0];
      await API.uploadServiceAccount(file);
      Toast.success('Thêm Service Account thành công!');
      document.getElementById('modal-add-account').classList.remove('active');
      fileInput.value = '';
      this.loadAccounts();
      App.refreshStats();
    } catch (err) {
      Toast.error(err.message);
    }
  },

  async refreshAccount(id) {
    try {
      Toast.info('Đang cập nhật dung lượng...');
      await API.refreshAccount(id);
      Toast.success('Đã cập nhật dung lượng thành công!');
      this.loadAccounts();
      App.refreshStats();
    } catch (err) {
      Toast.error(err.message);
    }
  },

  async refreshAllQuotas() {
    try {
      Toast.info('Đang quét lại dung lượng tất cả tài khoản...');
      const res = await API.refreshAccount('');
      Toast.success(res && res.message ? res.message : 'Đang quét và làm mới dung lượng tất cả tài khoản!');
      setTimeout(() => {
        this.loadAccounts();
        App.refreshStats();
      }, 2000);
    } catch (err) {
      Toast.error(err.message || 'Lỗi quét dung lượng tài khoản');
    }
  },

  async deleteAccount(id) {
    if (!confirm('Bạn có chắc chắn muốn xóa tài khoản Google Drive này khỏi cụm lưu trữ?')) {
      return;
    }
    try {
      await API.deleteAccount(id);
      Toast.success('Đã xóa tài khoản');
      this.loadAccounts();
      App.refreshStats();
    } catch (err) {
      Toast.error(err.message);
    }
  },

  async importAllDriveFiles() {
    try {
      Toast.info('⚡ Đang tự động quét và nạp tất cả tệp có sẵn từ 10 tài khoản Google Drive...');
      const res = await API.importDriveFiles('');
      const count = res.imported_count || 0;
      if (count > 0) {
        Toast.success(`🎉 ${res.message || `Đã nạp thành công ${count} tệp tin từ Google Drive!`}`);
      } else {
        Toast.info('Tất cả các tệp tin trên Google Drive đã được đồng bộ vào CloudPool.');
      }
      FilesManager.loadFiles('root');
      App.refreshStats();
      this.loadAccounts();
    } catch (err) {
      Toast.error('Lỗi nạp tệp từ Google Drive: ' + err.message);
    }
  },

  async importAccountDriveFiles(accountId) {
    try {
      Toast.info('⚡ Đang quét tệp có sẵn trên tài khoản này...');
      const res = await API.importDriveFiles(accountId);
      const count = res.imported_count || 0;
      if (count > 0) {
        Toast.success(`🎉 ${res.message || `Đã nạp thành công ${count} tệp tin!`}`);
      } else {
        Toast.info('Tài khoản này chưa có thêm tệp mới nào cần nạp.');
      }
      FilesManager.loadFiles('root');
      App.refreshStats();
      this.loadAccounts();
    } catch (err) {
      Toast.error('Lỗi nạp tệp: ' + err.message);
    }
  },
};
