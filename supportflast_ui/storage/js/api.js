// ==========================================================================
// CloudPool API Client & Utility Library
// ==========================================================================

const API = {
  baseURL: '',

  getCurrentUser() {
    try {
      const u = localStorage.getItem('cloudpool_current_user');
      return u ? JSON.parse(u) : null;
    } catch (_) {
      return null;
    }
  },

  setCurrentUser(user) {
    if (user) {
      localStorage.setItem('cloudpool_current_user', JSON.stringify(user));
    } else {
      localStorage.removeItem('cloudpool_current_user');
    }
  },

  getToken() {
    return localStorage.getItem('cloudpool_jwt_token') || '';
  },

  setToken(token) {
    if (token) {
      localStorage.setItem('cloudpool_jwt_token', token);
    } else {
      localStorage.removeItem('cloudpool_jwt_token');
    }
  },

  async logout() {
    this.setCurrentUser(null);
    this.setToken(null);
    return this.request('/api/auth/logout', { method: 'POST' }).catch(() => {});
  },

  async request(endpoint, options = {}) {
    try {
      const headers = options.headers ? { ...options.headers } : {};
      headers['ngrok-skip-browser-warning'] = 'true';
      const token = this.getToken();
      if (token && !headers['Authorization']) {
        headers['Authorization'] = `Bearer ${token}`;
      }
      const user = this.getCurrentUser();
      if (user && user.id) {
        headers['X-User-ID'] = user.id;
      }
      options.headers = headers;
      options.credentials = options.credentials || 'include';
      
      const timeoutMs = options.timeout !== undefined ? options.timeout : 30000;
      const controller = new AbortController();
      const timeoutId = timeoutMs > 0 ? setTimeout(() => controller.abort(), timeoutMs) : null;
      options.signal = controller.signal;

      const res = await fetch(endpoint, options);
      if (timeoutId) clearTimeout(timeoutId);

      if (!res.ok) {
        let errData;
        try {
          errData = await res.json();
        } catch (_) {
          errData = { error: res.statusText || 'Yêu cầu thất bại' };
        }
        throw new Error(errData.error || `HTTP Error ${res.status}`);
      }
      return await res.json();
    } catch (err) {
      if (err.name === 'AbortError') {
        err = new Error('Yêu cầu hết thời gian chờ (Timeout)');
      }
      console.error(`[API ERROR] ${endpoint}:`, err);
      throw err;
    }
  },

  getStats() {
    return this.request('/api/stats');
  },

  getAccounts() {
    return this.request('/api/accounts');
  },

  getOAuthURL() {
    return this.request('/api/accounts/oauth/url');
  },

  submitOAuthCallback(code) {
    return this.request('/api/accounts/oauth/callback', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ code }),
    });
  },

  uploadServiceAccount(file) {
    const formData = new FormData();
    formData.append('sa_file', file);
    return this.request('/api/accounts/service-account', {
      method: 'POST',
      body: formData,
    });
  },

  deleteAccount(id) {
    return this.request(`/api/accounts/delete?id=${encodeURIComponent(id)}`);
  },

  toggleAccount(id) {
    return this.request(`/api/accounts/toggle?id=${encodeURIComponent(id)}`, {
      method: 'POST',
    });
  },

  refreshAccount(id = '') {
    return this.request(`/api/accounts/refresh?id=${encodeURIComponent(id)}`);
  },

  listFiles(parentId = 'root', userId = '') {
    let url = `/api/files?parent_id=${encodeURIComponent(parentId)}`;
    if (userId) {
      url += `&user_id=${encodeURIComponent(userId)}`;
    }
    return this.request(url);
  },

  listFilesByAccount(accountId) {
    return this.request(`/api/files?account_id=${encodeURIComponent(accountId)}`);
  },

  getFileChunks(id) {
    return this.request(`/api/files/chunks?id=${encodeURIComponent(id)}`);
  },

  createFolder(parentId, name) {
    return this.request('/api/files/mkdir', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ parent_id: parentId, name }),
    });
  },

  renameFile(id, newName) {
    return this.request('/api/files/rename', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id, new_name: newName }),
    });
  },

  deleteFile(id) {
    return this.request(`/api/files/delete?id=${encodeURIComponent(id)}`);
  },

  bulkDeleteFiles(ids) {
    return this.request('/api/files/bulk-delete', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ids }),
    });
  },

  uploadFile(parentId, file, onProgress, relPath = '') {
    return new Promise((resolve, reject) => {
      // Auto-switch: file lớn > 5MB → dùng chunked upload (chia nhỏ 5MB, chống rớt mạng di động & Cloudflare timeout)
      if (file.size > 5 * 1024 * 1024) {
        this.uploadFileChunked(parentId, file, onProgress)
          .then(resolve)
          .catch(reject);
        return;
      }

      const maxRetries = 2; // Tổng cộng 3 lần (1 lần đầu + 2 retry)
      let attempt = 0;

      const doUpload = () => {
        const xhr = new XMLHttpRequest();
        const formData = new FormData();
        formData.append('parent_id', parentId || 'root');
        if (relPath) {
          formData.append('rel_path', relPath);
        }
        formData.append('file', file, file.name);

        xhr.open('POST', '/api/files/upload', true);

        // CRITICAL: phải set withCredentials=true để gửi cookie cloudpool_token lên server
        xhr.withCredentials = true;
        xhr.timeout = 30 * 60 * 1000; // 30 phút timeout cho upload

        // Gửi token xác thực nếu có (hỗ trợ cả Authorization header và Cookie)
        const token = this.getToken();
        if (token) {
          xhr.setRequestHeader('Authorization', `Bearer ${token}`);
        }

        const user = this.getCurrentUser();
        if (user && user.id) {
          xhr.setRequestHeader('X-User-ID', user.id);
        }
        xhr.setRequestHeader('ngrok-skip-browser-warning', 'true');

        if (xhr.upload && onProgress) {
          xhr.upload.onprogress = (e) => {
            if (e.lengthComputable) {
              const percent = Math.round((e.loaded / e.total) * 100);
              onProgress(percent, e.loaded, e.total);
            }
          };
        }

        xhr.onload = () => {
          if (xhr.status >= 200 && xhr.status < 300) {
            try {
              resolve(JSON.parse(xhr.responseText));
            } catch (e) {
              resolve({ message: 'Success' });
            }
          } else {
            let errMsg = 'Tải lên thất bại';
            try {
              const res = JSON.parse(xhr.responseText);
              if (res.error) errMsg = res.error;
            } catch (_) {}
            reject(new Error(errMsg));
          }
        };

        xhr.onerror = () => {
          attempt++;
          if (attempt <= maxRetries) {
            console.warn(`[Upload] Retry ${attempt}/${maxRetries} cho ${file.name}`);
            setTimeout(doUpload, 1000 * attempt);
          } else {
            reject(new Error('Lỗi kết nối mạng khi tải lên'));
          }
        };

        xhr.ontimeout = () => {
          attempt++;
          if (attempt <= maxRetries) {
            console.warn(`[Upload] Timeout retry ${attempt}/${maxRetries} cho ${file.name}`);
            setTimeout(doUpload, 1000 * attempt);
          } else {
            reject(new Error('Upload quá thời gian cho phép (30 phút). Vui lòng thử lại.'));
          }
        };

        xhr.send(formData);
      };

      doUpload();
    });
  },

  // Recycle Bin (Trash) API Methods
  listTrashFiles() {
    return this.request('/api/files/trash');
  },

  restoreTrashFile(id) {
    return this.request('/api/files/trash/restore', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id }),
    });
  },

  purgeTrashFile(id) {
    return this.request('/api/files/trash/delete-forever', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id }),
    });
  },

  emptyTrash() {
    return this.request('/api/files/trash/empty', {
      method: 'POST',
    });
  },

  // Public Share Links API Methods
  createPublicShare(fileId, password = '', expiresHours = 24, maxDownloads = 0) {
    return this.request('/api/shares/create', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        file_id: fileId,
        password: password,
        expires_hours: parseInt(expiresHours) || 0,
        max_downloads: parseInt(maxDownloads) || 0,
      }),
    });
  },

  listPublicShares() {
    return this.request('/api/shares/list');
  },

  revokePublicShare(id) {
    return this.request('/api/shares/revoke', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id }),
    });
  },

  // Storage Breakdown & Analytics
  getStorageBreakdown() {
    return this.request('/api/stats/breakdown');
  },

  getSettings() {
    return this.request('/api/settings');
  },


  saveSettings(data) {
    return this.request('/api/settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    });
  },

  getSQLTables() {
    return this.request('/api/sql/tables');
  },

  runSQLQuery(query) {
    return this.request('/api/sql/query', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ query }),
    });
  },

  getRemoteInfo() {
    return this.request('/api/remote/info');
  },

  importDriveFiles(accountId = '') {
    return this.request('/api/admin/drive/import', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ account_id: accountId }),
      timeout: 180000, // 3 minutes timeout for scanning and importing files from drive accounts
    });
  },

  getDriveFiles(accountId) {
    return this.request(`/api/admin/drive/files?account_id=${encodeURIComponent(accountId)}`, {
      timeout: 60000,
    });
  },

  // User Auth & Management
  async login(username, password, turnstileToken) {
    const res = await this.request('/api/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password, turnstile_token: turnstileToken }),
    });
    if (res && res.token) {
      this.setToken(res.token);
    }
    if (res && res.user) {
      this.setCurrentUser(res.user);
    }
    return res;
  },

  async verifyAdminPass(password) {
    try {
      return await this.request('/api/auth/verify-admin-pass', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password }),
      });
    } catch (err) {
      // Fallback to login as admin directly
      return await this.login('admin', password);
    }
  },

  register(username, password, displayName, turnstileToken) {
    return this.request('/api/auth/register', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password, display_name: displayName, turnstile_token: turnstileToken }),
    });
  },

  getMe() {
    return this.request('/api/auth/me');
  },

  getAdminUsers() {
    return this.request('/api/admin/users');
  },

  updateUserQuota(userId, quotaBytes) {
    return this.request('/api/admin/users/quota', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ user_id: userId, quota_bytes: quotaBytes }),
    });
  },

  deleteUser(userId) {
    return this.request('/api/admin/users/delete', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ user_id: userId }),
    });
  },

  changePassword(oldPassword, newPassword) {
    return this.request('/api/auth/change-password', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ old_password: oldPassword, new_password: newPassword }),
    });
  },

  adminResetPassword(userId, newPassword) {
    return this.request('/api/admin/users/reset-password', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ user_id: userId, new_password: newPassword }),
    });
  },

  sqlOptimize() {
    return this.request('/api/sql/optimize', { method: 'POST' });
  },

  sqlCheck() {
    return this.request('/api/sql/check');
  },

  sqlBackup() {
    return this.request('/api/sql/backup', { method: 'POST' });
  },

  setSecurityPin(oldPin, newPin, tier = 2) {
    return this.request('/api/auth/security-pin', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ old_pin: oldPin, new_pin: newPin, security_tier: tier }),
    });
  },

  verifySecurityPin(pin) {
    return this.request('/api/auth/verify-pin', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ pin }),
    });
  },

  getActivityLogs() {
    return this.request('/api/admin/logs');
  },

  getLoginSessions() {
    return this.request('/api/admin/sessions');
  },

  // Software Update & Hot-Patching
  getUpdateInfo() {
    return this.request('/api/admin/update/info');
  },

  uploadUpdate(formData) {
    const headers = {};
    const user = this.getCurrentUser();
    if (user && user.id) {
      headers['X-User-ID'] = user.id;
    }
    return fetch('/api/admin/update/upload', {
      method: 'POST',
      headers,
      body: formData,
    }).then(async (res) => {
      const data = await res.json();
      if (!res.ok) {
        throw new Error(data.error || 'Nâng cấp thất bại');
      }
      return data;
    });
  },

  rollbackUpdate(targetModule, targetFilename) {
    return this.request('/api/admin/update/rollback', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ target_module: targetModule, target_filename: targetFilename }),
    });
  },

  restartEngine() {
    return this.request('/api/admin/update/restart', { method: 'POST' });
  },

  // Single-Use OTP File Access API
  verifyFileOTP(fileId, otpCode) {
    return this.request('/api/files/otp/verify', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ file_id: fileId, otp_code: otpCode }),
    });
  },

  requestFileOTP(fileId) {
    return this.request('/api/files/otp/request', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ file_id: fileId }),
    });
  },

  getMyFileRequests() {
    return this.request('/api/files/otp/my-requests');
  },

  adminGenerateOTP(fileId, targetUserId = 'all', durationMinutes = 1440) {
    return this.request('/api/admin/otp/generate', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ file_id: fileId, target_user_id: targetUserId, duration_minutes: durationMinutes }),
    });
  },

  adminListOTPs() {
    return this.request('/api/admin/otp/list');
  },

  adminRevokeOTP(id) {
    return this.request('/api/admin/otp/revoke', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id }),
    });
  },

  adminListAccessRequests() {
    return this.request('/api/admin/otp/requests');
  },

  adminApproveRequest(requestId, durationMinutes = 1440) {
    return this.request('/api/admin/otp/approve-request', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ request_id: requestId, duration_minutes: durationMinutes }),
    });
  },

  adminRejectRequest(requestId) {
    return this.request('/api/admin/otp/reject-request', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ request_id: requestId }),
    });
  },

  // Polling nhẹ - chỉ lấy số lượng yêu cầu đang chờ duyệt
  adminGetOTPPendingCount() {
    return this.request('/api/admin/otp/pending-count');
  },

  // Chunked Upload cho file lớn (>5MB) - chia file thành chunk 5MB, chống Cloudflare timeout 100s & ổn định trên mạng di động 4G/Wifi
  async uploadFileChunked(parentId, file, onProgress) {
    const CHUNK_SIZE = 5 * 1024 * 1024; // 5MB - tối ưu tuyệt đối cho mạng di động 4G/Wifi & Cloudflare Tunnel
    const totalChunks = Math.ceil(file.size / CHUNK_SIZE);

    // Chuẩn bị headers xác thực
    const authHeaders = {
      'ngrok-skip-browser-warning': 'true'
    };
    const token = this.getToken();
    if (token) {
      authHeaders['Authorization'] = `Bearer ${token}`;
    }
    const user = this.getCurrentUser();
    if (user && user.id) {
      authHeaders['X-User-ID'] = user.id;
    }

    // Bước 1: Khởi tạo session
    const initRes = await fetch('/api/files/upload-chunked', {
      method: 'POST',
      headers: {
        ...authHeaders,
        'Content-Type': 'application/json'
      },
      credentials: 'include',
      body: JSON.stringify({
        file_name: file.name,
        parent_id: parentId || 'root',
        total_size: file.size,
        total_chunks: totalChunks
      })
    });
    if (!initRes.ok) {
      const errData = await initRes.json().catch(() => ({}));
      throw new Error(errData.error || `Lỗi khởi tạo phiên (${initRes.status})`);
    }
    const { upload_id } = await initRes.json();

    // Bước 2: Upload từng chunk tuần tự với cơ chế retry tự động
    let lastResult = null;
    for (let i = 0; i < totalChunks; i++) {
      const start = i * CHUNK_SIZE;
      const end = Math.min(start + CHUNK_SIZE, file.size);
      const chunk = file.slice(start, end);

      let retries = 0;
      const maxRetries = 6;
      while (true) {
        try {
          // Tạo FormData MỚI mỗi lần retry
          const formData = new FormData();
          formData.append('chunk', chunk, `chunk_${i}`);

          const controller = new AbortController();
          const timeoutId = setTimeout(() => controller.abort(), 180 * 1000); // 3 phút cho 1 chunk 5MB

          const res = await fetch(
            `/api/files/upload-chunked?upload_id=${encodeURIComponent(upload_id)}&chunk_index=${i}`,
            {
              method: 'POST',
              body: formData,
              credentials: 'include',
              headers: authHeaders,
              signal: controller.signal
            }
          );
          clearTimeout(timeoutId);

          if (res.status === 429) {
            // Nhận tín hiệu giới hạn tốc độ từ máy chủ, tự động đợi và gửi lại mảnh này
            const retrySec = parseInt(res.headers.get('Retry-After')) || 2;
            await new Promise(r => setTimeout(r, (retrySec + 1) * 1000));
            continue;
          }

          if (!res.ok) {
            // Nếu gặp lỗi mạng tạm thời hoặc gateway đang kết nối lại (502, 503, 504, 520, 521, 522, 524)
            if (res.status === 502 || res.status === 503 || res.status === 504 || res.status === 520 || res.status === 521 || res.status === 522 || res.status === 524) {
              throw new Error(`Đang kết nối lại máy chủ (HTTP ${res.status})...`);
            }
            const errData = await res.json().catch(() => ({}));
            throw new Error(errData.error || `Lỗi tải mảnh ${i + 1}/${totalChunks} (HTTP ${res.status})`);
          }
          lastResult = await res.json();
          break;
        } catch (err) {
          retries++;
          if (retries >= maxRetries) {
            const friendlyErr = (err.name === 'AbortError' || (err.message && (err.message.includes('fetch') || err.message.includes('kết nối'))))
              ? `Mạng chập chờn khi gửi mảnh ${i + 1}/${totalChunks}. Vui lòng thử lại.`
              : (err.message || `Lỗi tải mảnh ${i + 1}/${totalChunks}`);
            throw new Error(friendlyErr);
          }
          if (onProgress) {
            onProgress(
              Math.min(90, Math.round((i / totalChunks) * 90)),
              `Mạng chậm, đang thử lại mảnh ${i + 1}/${totalChunks} (lần ${retries}/${maxRetries})...`
            );
          }
          await new Promise(r => setTimeout(r, Math.min(1500 * retries, 8000)));
        }
      }

      if (onProgress) {
        // Dành 0-90% cho việc truyền các chunk lên máy chủ
        const percent = Math.min(90, Math.round(((i + 1) / totalChunks) * 90));
        onProgress(percent, `Đang tải mảnh ${i + 1}/${totalChunks} (${percent}%)`);
      }
    }

    // Bước 3: Nếu server đang ghép và upload lên Cloud (Assembling), polling tiến trình
    if (lastResult && lastResult.status === 'assembling') {
      if (onProgress) {
        onProgress(92, 'Đang ghép mảnh & mã hóa AES-256 phân tán lên Google Drive...');
      }
      const pollStart = Date.now();
      const maxPollTime = 30 * 60 * 1000; // 30 phút tối đa cho file cực lớn
      while (Date.now() - pollStart < maxPollTime) {
        await new Promise(r => setTimeout(r, 1500));
        const statusRes = await fetch(`/api/files/upload-status?upload_id=${encodeURIComponent(upload_id)}`, {
          method: 'GET',
          headers: authHeaders,
          credentials: 'include'
        });
        if (!statusRes.ok) {
          const errData = await statusRes.json().catch(() => ({}));
          throw new Error(errData.error || 'Lỗi kiểm tra tiến trình hoàn tất');
        }
        const statusData = await statusRes.json();
        if (statusData.status === 'completed' && statusData.file) {
          if (onProgress) {
            onProgress(100, 'Hoàn tất ✓');
          }
          return statusData.file;
        }
        if (statusData.status === 'error') {
          throw new Error(statusData.error || 'Ghép tệp và đồng bộ Google Drive thất bại');
        }
        if (onProgress) {
          const elapsedSec = Math.round((Date.now() - pollStart) / 1000);
          onProgress(95, `Đang mã hóa & đồng bộ Google Drive (${elapsedSec}s)...`);
        }
      }
      throw new Error('Quá thời gian chờ xử lý tệp tin trên máy chủ.');
    }

    return lastResult;
  },
};

// Toast Notifications
const Toast = {
  show(message, type = 'info', duration = 3500) {
    const container = document.getElementById('toast-container');
    if (!container) return;

    const toast = document.createElement('div');
    toast.className = `toast ${type}`;
    toast.textContent = message;

    container.appendChild(toast);

    setTimeout(() => {
      toast.style.opacity = '0';
      toast.style.transform = 'translateX(100%)';
      toast.style.transition = 'all 0.3s ease';
      setTimeout(() => toast.remove(), 300);
    }, duration);
  },

  success(msg) { this.show(msg, 'success'); },
  error(msg) { this.show(msg, 'error', 5000); },
  info(msg) { this.show(msg, 'info'); },
  warning(msg, duration = 4000) { this.show(msg, 'warning', duration); },
};

// Formatting Utilities
const Utils = {
  formatBytes(bytes, decimals = 2) {
    if (!+bytes) return '0 Bytes';
    const k = 1024;
    const dm = decimals < 0 ? 0 : decimals;
    const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB', 'PB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return `${parseFloat((bytes / Math.pow(k, i)).toFixed(dm))} ${sizes[i]}`;
  },

  formatDate(dateStr) {
    if (!dateStr) return '-';
    let d = new Date(dateStr);
    if (isNaN(d.getTime())) {
      d = new Date(String(dateStr).replace(' ', 'T'));
    }
    if (isNaN(d.getTime())) return String(dateStr);
    return d.toLocaleString('vi-VN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    });
  },

  getTimestamp(dateStr) {
    if (!dateStr) return 0;
    let d = new Date(dateStr);
    if (isNaN(d.getTime())) {
      d = new Date(String(dateStr).replace(' ', 'T'));
    }
    return isNaN(d.getTime()) ? 0 : d.getTime();
  },

  getFileIconSVG(mimeType = '', isDir = false, fileName = '') {
    if (isDir) {
      return `<svg viewBox="0 0 24 24" fill="currentColor" width="20" height="20" style="color: var(--accent-amber);"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"></path></svg>`;
    }
    const lowerMime = (mimeType || '').toLowerCase();
    const lowerName = (fileName || '').toLowerCase();

    // 1. Video
    if (lowerMime.startsWith('video/') || /\.(mp4|webm|mkv|avi|mov|wmv|flv|m4v|ts|3gp|vob|ogv)$/i.test(lowerName)) {
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="20" height="20" style="color: #ef4444;"><polygon points="23 7 16 12 23 17 23 7"></polygon><rect x="1" y="5" width="15" height="14" rx="2" ry="2"></rect></svg>`;
    }
    // 2. Image
    if (lowerMime.startsWith('image/') || /\.(jpg|jpeg|png|webp|gif|svg|bmp|ico|tiff|tif|heic|heif|avif|raw|cr2|nef)$/i.test(lowerName)) {
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="20" height="20" style="color: #10b981;"><rect x="3" y="3" width="18" height="18" rx="2" ry="2"></rect><circle cx="8.5" cy="8.5" r="1.5"></circle><polyline points="21 15 16 10 5 21"></polyline></svg>`;
    }
    // 3. Audio
    if (lowerMime.startsWith('audio/') || /\.(mp3|flac|wav|aac|m4a|ogg|wma|opus|midi|mid|aiff)$/i.test(lowerName)) {
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="20" height="20" style="color: #f59e0b;"><path d="M9 18V5l12-2v13"></path><circle cx="6" cy="18" r="3"></circle><circle cx="18" cy="16" r="3"></circle></svg>`;
    }
    // 4. Spreadsheets & Excel
    if (lowerMime.includes('excel') || lowerMime.includes('sheet') || lowerMime.includes('csv') || /\.(xls|xlsx|csv|tsv|ods|numbers|parquet|feather)$/i.test(lowerName)) {
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="20" height="20" style="color: #22c55e;"><rect x="3" y="3" width="18" height="18" rx="2" ry="2"></rect><line x1="3" y1="9" x2="21" y2="9"></line><line x1="3" y1="15" x2="21" y2="15"></line><line x1="9" y1="3" x2="9" y2="21"></line><line x1="15" y1="3" x2="15" y2="21"></line></svg>`;
    }
    // 5. Presentations & Slides
    if (lowerMime.includes('presentation') || lowerMime.includes('powerpoint') || /\.(ppt|pptx|ppsx|key|odp|potx)$/i.test(lowerName)) {
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="20" height="20" style="color: #f97316;"><rect x="2" y="3" width="20" height="14" rx="2" ry="2"></rect><line x1="8" y1="21" x2="16" y2="21"></line><line x1="12" y1="17" x2="12" y2="21"></line><circle cx="12" cy="10" r="3"></circle></svg>`;
    }
    // 6. Documents & PDF
    if (lowerMime.includes('pdf') || lowerMime.includes('word') || lowerMime.includes('document') || /\.(pdf|doc|docx|odt|rtf|epub|mobi|txt|log|tex|pages)$/i.test(lowerName)) {
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="20" height="20" style="color: #8b5cf6;"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"></path><polyline points="14 2 14 8 20 8"></polyline><line x1="16" y1="13" x2="8" y2="13"></line><line x1="16" y1="17" x2="8" y2="17"></line><polyline points="10 9 9 9 8 9"></polyline></svg>`;
    }
    // 7. Archives
    if (lowerMime.includes('zip') || lowerMime.includes('compressed') || lowerMime.includes('tar') || /\.(zip|rar|7z|tar|gz|tgz|bz2|xz|iso|img|dmg|vhd|vhdx|wim)$/i.test(lowerName)) {
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="20" height="20" style="color: #ec4899;"><polyline points="21 8 21 21 3 21 3 8"></polyline><rect x="1" y="3" width="22" height="5"></rect><line x1="10" y1="12" x2="14" y2="12"></line></svg>`;
    }
    // 8. Code & Databases
    if (/\.(go|rs|py|js|ts|jsx|tsx|java|c|cpp|h|hpp|cs|php|rb|html|css|scss|sql|sqlite|db|json|yaml|yml|xml|sh|bat|ps1|cmd|md|env)$/i.test(lowerName)) {
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="20" height="20" style="color: #06b6d4;"><polyline points="16 18 22 12 16 6"></polyline><polyline points="8 6 2 12 8 18"></polyline></svg>`;
    }
    // 9. Design & 3D / CAD
    if (/\.(psd|ai|eps|fig|xd|sketch|blend|obj|fbx|stl|dwg|dxf|step|iges)$/i.test(lowerName)) {
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="20" height="20" style="color: #a855f7;"><circle cx="12" cy="12" r="10"></circle><path d="m4.93 4.93 4.24 4.24"></path><path d="m14.83 9.17 4.24-4.24"></path><path d="m14.83 14.83 4.24 4.24"></path><path d="m9.17 14.83-4.24 4.24"></path></svg>`;
    }
    // 10. Executables & Apps
    if (/\.(exe|msi|apk|aab|ipa|deb|rpm|appimage|bin|pkg)$/i.test(lowerName)) {
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="20" height="20" style="color: #6366f1;"><rect x="2" y="4" width="20" height="16" rx="2"></rect><path d="M10 4v4"></path><path d="M2 8h20"></path><path d="M6 4v4"></path></svg>`;
    }

    return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="20" height="20" style="color: var(--text-secondary);"><path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"></path><polyline points="13 2 13 9 20 9"></polyline></svg>`;
  },
};
