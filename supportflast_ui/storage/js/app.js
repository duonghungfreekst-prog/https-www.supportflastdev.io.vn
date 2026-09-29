// ==========================================================================
// CloudPool Main Application Controller (PRO Edition with Privacy Lock)
// ==========================================================================

const App = {
  currentTab: 'files',
  isAdminUnlocked: true,

  async init() {
    this.bindNavigation();
    this.bindModals();

    // Initialize Submodules
    if (typeof AuthManager !== 'undefined') await AuthManager.init();
    if (typeof AccountsManager !== 'undefined') AccountsManager.init();
    if (typeof FilesManager !== 'undefined') FilesManager.init();
    if (typeof SettingsManager !== 'undefined') SettingsManager.init();
    if (typeof SQLStudio !== 'undefined') SQLStudio.init();
    if (typeof UpdaterManager !== 'undefined') UpdaterManager.init();
    if (typeof TunnelManager !== 'undefined') TunnelManager.fetchStatus();

    // Apply Admin Lock visibility
    this.applyAdminState();

    // Check OAuth Return (if returning from Google OAuth, stay on accounts tab)
    const isOAuthReturn = this.checkOAuthReturn();

    // Switch to initial tab (Files if normal visit, Accounts if OAuth callback)
    if (!isOAuthReturn) {
      this.switchTab('files');
    }

    // Load Initial Data
    this.refreshStats();
    if (typeof AccountsManager !== 'undefined') AccountsManager.loadAccounts();
    if (typeof RemoteManager !== 'undefined') RemoteManager.loadRemoteInfo();
    if (typeof SettingsManager !== 'undefined') SettingsManager.loadSettings();
    if (typeof AuthManager !== 'undefined') AuthManager.loadUsers();
    if (typeof UpdaterManager !== 'undefined') UpdaterManager.loadUpdateInfo();
    if (typeof FilesManager !== 'undefined') FilesManager.startAdminOTPPolling();

    // Background Stats Polling (every 10s)
    setInterval(() => this.refreshStats(), 10000);
  },

  applyAdminState() {
    const isAdmin = typeof AuthManager !== 'undefined' && AuthManager.currentUser && AuthManager.currentUser.role === 'admin';

    document.body.classList.toggle('is-admin', !!isAdmin);
    document.body.classList.toggle('user-member', !isAdmin);

    const adminElements = document.querySelectorAll('.admin-only, [data-admin="true"]');
    adminElements.forEach(el => {
      el.style.display = isAdmin ? '' : 'none';
    });
  },

  bindNavigation() {
    // Mobile Menu Toggle
    const btnMobileMenu = document.getElementById('btn-mobile-menu-toggle');
    const btnSidebarClose = document.getElementById('btn-sidebar-close');
    const sidebar = document.querySelector('.sidebar');
    const backdrop = document.getElementById('mobile-sidebar-backdrop');

    const closeMobileSidebar = () => {
      if (sidebar) sidebar.classList.remove('mobile-open');
      if (backdrop) backdrop.classList.remove('active');
    };

    if (btnMobileMenu) {
      btnMobileMenu.addEventListener('click', () => {
        if (sidebar) sidebar.classList.add('mobile-open');
        if (backdrop) backdrop.classList.add('active');
      });
    }

    if (btnSidebarClose) {
      btnSidebarClose.addEventListener('click', closeMobileSidebar);
    }

    if (backdrop) {
      backdrop.addEventListener('click', closeMobileSidebar);
    }

    // Sidebar Navigation Tabs
    const navItems = document.querySelectorAll('.nav-item');
    navItems.forEach(item => {
      item.addEventListener('click', () => {
        const tab = item.getAttribute('data-tab');
        if (tab) {
          this.switchTab(tab);
          closeMobileSidebar();
        }
      });
    });
  },

  switchTab(tab) {
    this.currentTab = tab;

    // Update Sidebar Active state
    document.querySelectorAll('.nav-item').forEach(el => {
      el.classList.toggle('active', el.getAttribute('data-tab') === tab);
    });

    // Update Views visibility
    document.querySelectorAll('.view-container').forEach(el => {
      el.classList.remove('active');
    });

    const targetView = document.getElementById(`view-${tab}`);
    if (targetView) targetView.classList.add('active');

    // Tab specific refreshes
    if (tab === 'dashboard' || tab === 'accounts') {
      if (typeof AccountsManager !== 'undefined') {
        AccountsManager.loadAccounts();
      }
      this.refreshStats();
    } else if (tab === 'files') {
      if (typeof FilesManager !== 'undefined') FilesManager.loadFiles(FilesManager.currentFolderId);
    } else if (tab === 'remote') {
      if (typeof RemoteManager !== 'undefined') RemoteManager.loadRemoteInfo();
    } else if (tab === 'users') {
      if (typeof AuthManager !== 'undefined') AuthManager.loadUsers();
    } else if (tab === 'sql') {
      if (typeof SQLStudio !== 'undefined') {
        SQLStudio.loadTables();
        SQLStudio.runQuery();
      }
    } else if (tab === 'settings') {
      if (typeof SettingsManager !== 'undefined') SettingsManager.loadSettings();
    } else if (tab === 'updater') {
      if (typeof UpdaterManager !== 'undefined') UpdaterManager.loadUpdateInfo();
    }
  },

  bindModals() {
    // Close on backdrop click
    document.querySelectorAll('.modal-overlay').forEach(modal => {
      modal.addEventListener('click', (e) => {
        if (e.target === modal) modal.classList.remove('active');
      });
    });

    // Close on [data-close] button click
    document.querySelectorAll('[data-close]').forEach(btn => {
      btn.addEventListener('click', () => {
        const targetId = btn.getAttribute('data-close');
        const modal = document.getElementById(targetId);
        if (modal) modal.classList.remove('active');
      });
    });

    // Keyboard Shortcuts
    window.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') {
        document.querySelectorAll('.modal-overlay.active').forEach(m => m.classList.remove('active'));
        const menu = document.getElementById('file-context-menu');
        if (menu) menu.classList.remove('active');
      }

      // Delete key for selected files
      if (e.key === 'Delete' && FilesManager.selectedIds.size > 0 && !['INPUT', 'TEXTAREA'].includes(document.activeElement.tagName)) {
        FilesManager.handleBulkDelete();
      }
    });
  },

  async refreshStats() {
    try {
      const stats = await API.getStats();

      // Top Cards
      const totalGB = Utils.formatBytes(stats.total_capacity_bytes);
      const usedGB = Utils.formatBytes(stats.total_used_bytes);
      const freeGB = Utils.formatBytes(stats.total_free_bytes);
      const percent = Math.min(100, Math.round(stats.overall_usage_percent || 0));

      if (document.getElementById('dash-total-cap')) document.getElementById('dash-total-cap').textContent = totalGB;
      if (document.getElementById('dash-used-cap')) document.getElementById('dash-used-cap').textContent = `Đã dùng: ${usedGB}`;
      if (document.getElementById('dash-free-cap')) document.getElementById('dash-free-cap').textContent = freeGB;
      if (document.getElementById('dash-percent-free')) document.getElementById('dash-percent-free').textContent = `${100 - percent}% trống`;

      if (document.getElementById('dash-accounts-count')) document.getElementById('dash-accounts-count').textContent = `${stats.total_accounts} Acc`;
      if (document.getElementById('dash-active-accs')) document.getElementById('dash-active-accs').textContent = `${stats.active_accounts} đang hoạt động`;

      if (document.getElementById('dash-files-count')) document.getElementById('dash-files-count').textContent = `${stats.total_files_count} tệp`;
      if (document.getElementById('dash-chunks-count')) document.getElementById('dash-chunks-count').textContent = `${stats.total_chunks_count} chunks mã hóa`;

      // Sidebar Storage Widget
      const sideTitle = document.getElementById('side-storage-title');
      if (stats.is_user_partition) {
        if (sideTitle) sideTitle.textContent = 'Dung lượng của bạn';
        const userUsedStr = Utils.formatBytes(stats.user_used_bytes || 0);
        const userQuotaStr = (stats.user_quota_bytes && stats.user_quota_bytes > 0) ? Utils.formatBytes(stats.user_quota_bytes) : 'Không giới hạn';
        const userPercent = (stats.user_quota_bytes && stats.user_quota_bytes > 0) 
          ? Math.min(100, Math.round(((stats.user_used_bytes || 0) / stats.user_quota_bytes) * 100)) 
          : 0;

        if (document.getElementById('side-percent')) document.getElementById('side-percent').textContent = (stats.user_quota_bytes && stats.user_quota_bytes > 0) ? `${userPercent}%` : 'Đang dùng';
        if (document.getElementById('side-storage-fill')) document.getElementById('side-storage-fill').style.width = `${userPercent}%`;
        if (document.getElementById('side-used')) document.getElementById('side-used').textContent = userUsedStr;
        if (document.getElementById('side-total')) document.getElementById('side-total').textContent = userQuotaStr;
      } else {
        if (sideTitle) sideTitle.textContent = 'Tổng kho dung lượng';
        if (document.getElementById('side-percent')) document.getElementById('side-percent').textContent = `${percent}%`;
        if (document.getElementById('side-storage-fill')) document.getElementById('side-storage-fill').style.width = `${percent}%`;
        if (document.getElementById('side-used')) document.getElementById('side-used').textContent = usedGB;
        if (document.getElementById('side-total')) document.getElementById('side-total').textContent = totalGB;
      }

      // Rust Core Status Badge
      const coreBadge = document.getElementById('core-badge-text');
      if (coreBadge) {
        coreBadge.textContent = stats.rust_core_active ? 'Rust AES-256 Core: Active' : 'Go Native AES-256: Active';
      }

      // Load Storage Breakdown
      this.loadStorageBreakdown();
    } catch (err) {
      console.error('Lỗi refresh stats:', err);
    }
  },

  async loadStorageBreakdown() {
    const barEl = document.getElementById('storage-breakdown-bar');
    const chipsEl = document.getElementById('storage-breakdown-chips');
    const summaryEl = document.getElementById('storage-breakdown-summary');
    if (!barEl || !chipsEl) return;

    try {
      const data = await API.getStorageBreakdown();
      if (!data) return;

      if (summaryEl) {
        summaryEl.textContent = `Tổng: ${Utils.formatBytes(data.total_bytes)} (${data.total_files} tệp)`;
      }

      if (!data.categories || data.categories.length === 0 || data.total_bytes === 0) {
        barEl.innerHTML = '<div style="width: 100%; height: 100%; background: rgba(255,255,255,0.05); text-align: center; font-size: 10px; color: var(--text-muted); line-height: 12px;">Chưa có dữ liệu tệp tin</div>';
        chipsEl.innerHTML = '<span style="font-size: 11px; color: var(--text-muted);">Kho chưa có tệp tin nào được tải lên.</span>';
        return;
      }

      let barHtml = '';
      let chipsHtml = '';

      data.categories.forEach(cat => {
        const catName = cat.label || cat.name || cat.category || 'Khác';
        const percentVal = typeof cat.percentage === 'number' ? cat.percentage.toFixed(1) : cat.percentage;
        if (cat.percentage > 0) {
          barHtml += `<div style="width: ${cat.percentage}%; height: 100%; background: ${cat.color}; transition: width 0.3s ease;" title="${catName}: ${Utils.formatBytes(cat.total_bytes)} (${percentVal}%)"></div>`;
        }
        chipsHtml += `
          <div style="display: flex; align-items: center; gap: 6px; font-size: 11px; padding: 4px 8px; background: rgba(255,255,255,0.03); border: 1px solid var(--border-subtle); border-radius: 6px;">
            <div style="width: 8px; height: 8px; border-radius: 50%; background: ${cat.color};"></div>
            <span style="font-weight: 600; color: var(--text-primary);">${catName}:</span>
            <span style="color: var(--text-secondary);">${Utils.formatBytes(cat.total_bytes)} (${percentVal}%)</span>
          </div>
        `;
      });

      barEl.innerHTML = barHtml;
      chipsEl.innerHTML = chipsHtml;
    } catch (err) {
      console.error('Lỗi loadStorageBreakdown:', err);
    }
  },


  checkOAuthReturn() {
    const urlParams = new URLSearchParams(window.location.search);
    if (urlParams.get('oauth_success') === 'true') {
      this.isAdminUnlocked = true;
      sessionStorage.setItem('cloudpool_admin_session', 'true');
      this.applyAdminState();
      Toast.success('🎉 Kết nối tài khoản Google Drive thành công!');
      window.history.replaceState({}, document.title, window.location.pathname);
      this.switchTab('accounts');
      if (typeof AccountsManager !== 'undefined') {
        AccountsManager.loadAccounts();
      }
      this.refreshStats();
      return true;
    } else if (urlParams.get('oauth_error')) {
      const err = urlParams.get('oauth_error');
      window.history.replaceState({}, document.title, window.location.pathname);
      this.isAdminUnlocked = true;
      sessionStorage.setItem('cloudpool_admin_session', 'true');
      this.applyAdminState();

      const decodedErr = decodeURIComponent(err);
      if (err === 'insufficient_scope' || decodedErr.includes('insufficient')) {
        Toast.error('Bạn chưa tích ô cấp quyền Google Drive! Vui lòng bấm "+ Thêm Tài khoản Mới" lại và đánh dấu tích ☑ vào ô "Xem, chỉnh sửa, tạo và xóa tất cả tệp Google Drive".', 8000);
      } else if (err.includes('redirect_uri_mismatch') || decodedErr.includes('redirect_uri_mismatch')) {
        Toast.error('Lỗi URI chuyển hướng không khớp (redirect_uri_mismatch)! Vui lòng kiểm tra lại cấu hình Redirect URI trong Google Cloud Console.', 9000);
      } else if (err.includes('access_denied') || decodedErr.includes('access_denied')) {
        Toast.info('Bạn đã hủy yêu cầu đăng nhập tài khoản Google Drive.');
      } else if (err.includes('invalid_client') || decodedErr.includes('invalid_client')) {
        Toast.error('Google Client ID hoặc Client Secret không đúng. Vui lòng kiểm tra lại trong mục Cài đặt.', 8000);
      } else {
        Toast.error('Lỗi xác thực Google: ' + decodedErr, 6000);
      }
      this.switchTab('accounts');
      return true;
    }
    return false;
  },
};

// Start application when DOM is ready
document.addEventListener('DOMContentLoaded', () => App.init());
