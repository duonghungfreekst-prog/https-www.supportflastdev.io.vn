// ==========================================================================
// CloudPool Settings Controller
// ==========================================================================

const SettingsManager = {
  init() {
    this.bindEvents();
  },

  bindEvents() {
    const form = document.getElementById('settings-form');
    if (form) {
      form.addEventListener('submit', (e) => {
        e.preventDefault();
        this.saveSettings();
      });
    }
  },

  async loadSettings() {
    try {
      const data = await API.getSettings();
      if (document.getElementById('set-chunk-size')) {
        document.getElementById('set-chunk-size').value = Math.round(data.chunk_size_bytes / (1024 * 1024)) || 20;
      }
      if (document.getElementById('set-strategy')) {
        document.getElementById('set-strategy').value = data.allocation_strategy || 'least_used';
      }
      if (document.getElementById('set-guest-mode')) {
        document.getElementById('set-guest-mode').value = data.guest_access_mode || 'view_only';
      }
      if (document.getElementById('set-allow-self-reg')) {
        document.getElementById('set-allow-self-reg').checked = data.allow_self_registration !== false;
      }
      if (document.getElementById('set-google-client-id')) {
        document.getElementById('set-google-client-id').value = data.google_client_id || '';
      }
      if (document.getElementById('set-google-client-secret')) {
        document.getElementById('set-google-client-secret').value = data.google_client_secret || '';
      }
      if (document.getElementById('set-redirect-url')) {
        document.getElementById('set-redirect-url').value = data.redirect_url || '';
      }
      if (document.getElementById('set-webdav-user')) {
        document.getElementById('set-webdav-user').value = data.webdav_username || 'admin';
      }
    } catch (err) {
      console.error('Lỗi tải cài đặt:', err);
    }
  },

  async saveSettings() {
    const chunkSizeMB = parseInt(document.getElementById('set-chunk-size').value, 10) || 20;
    const payload = {
      master_passphrase: document.getElementById('set-passphrase').value,
      chunk_size_bytes: chunkSizeMB * 1024 * 1024,
      allocation_strategy: document.getElementById('set-strategy').value,
      guest_access_mode: document.getElementById('set-guest-mode') ? document.getElementById('set-guest-mode').value : 'view_only',
      allow_self_registration: document.getElementById('set-allow-self-reg') ? document.getElementById('set-allow-self-reg').checked : false,
      google_client_id: document.getElementById('set-google-client-id').value.trim(),
      google_client_secret: document.getElementById('set-google-client-secret').value.trim(),
      redirect_url: document.getElementById('set-redirect-url') ? document.getElementById('set-redirect-url').value.trim() : '',
      webdav_username: document.getElementById('set-webdav-user').value.trim(),
      webdav_password: document.getElementById('set-webdav-pass').value.trim(),
      webdav_enabled: true,
      server_port: 8080,
    };

    try {
      await API.saveSettings(payload);
      Toast.success('Đã lưu cấu hình hệ thống thành công!');
    } catch (err) {
      Toast.error('Lỗi lưu cấu hình: ' + err.message);
    }
  },
};
