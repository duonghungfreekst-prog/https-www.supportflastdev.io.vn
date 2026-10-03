// Quản lý Tunnel 1-Click
const TunnelManager = {
  async fetchStatus() {
    try {
      const res = await API.request('/api/tunnel/status', { method: 'GET' });
      this.updateUI(res);
    } catch (err) {
      console.error('Lỗi khi tải trạng thái tunnel:', err);
      // Reset trạng thái nút bấm nếu máy chủ không phản hồi
      this.updateUI({ running: false, url: '' });
    }
  },

  async startTunnel() {
    const btn = document.getElementById('btn-tunnel-toggle');
    if (btn) {
      btn.disabled = true;
      btn.innerHTML = `<span class="spinner-border spinner-border-sm" role="status" aria-hidden="true" style="margin-right: 5px;"></span> Đang khởi tạo...`;
    }

    try {
      const res = await API.request('/api/tunnel/start', { method: 'POST' });
      if (res.message) Toast.success(res.message);
      
      // Chờ vài giây để tunnel lấy được link rồi gọi lại status
      setTimeout(() => {
        this.fetchStatus();
      }, 3000);
      
      // Polling thêm 1-2 lần để chắc chắn lấy được URL nếu chậm
      setTimeout(() => this.fetchStatus(), 6000);
      setTimeout(() => this.fetchStatus(), 10000);
      
    } catch (err) {
      Toast.error('Không thể bật Truy cập từ xa: ' + err.message);
      this.fetchStatus(); // Phục hồi trạng thái cũ
    }
  },

  async stopTunnel() {
    const btn = document.getElementById('btn-tunnel-toggle');
    if (btn) {
      btn.disabled = true;
      btn.innerHTML = `<span class="spinner-border spinner-border-sm" role="status" aria-hidden="true" style="margin-right: 5px;"></span> Đang tắt...`;
    }

    try {
      await API.request('/api/tunnel/stop', { method: 'POST' });
      Toast.success("Đã tắt kết nối từ xa.");
      this.updateUI({ running: false, url: '' });
    } catch (err) {
      Toast.error('Không thể tắt Truy cập từ xa: ' + err.message);
      this.fetchStatus();
    }
  },

  updateUI(state) {
    const container = document.getElementById('tunnel-url-container');
    if (!container) return;

    const primaryUrl = (state && state.url) ? state.url : 'https://supportflastdev.io.vn';
    const altUrl = 'https://www.supportflastdev.io.vn';
    const webdavUrl = (state && state.url) ? `${state.url.replace(/\/+$/, '')}/webdav` : 'https://supportflastdev.io.vn/webdav';

    container.style.display = 'block';
    container.innerHTML = `
      <div style="display: flex; flex-direction: column; gap: 10px; margin-top: 14px;">
        <div style="background: rgba(46, 160, 67, 0.1); border: 1px solid rgba(46, 160, 67, 0.3); padding: 14px 16px; border-radius: 8px; display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 10px;">
          <div style="display: flex; align-items: center; gap: 12px;">
            <div style="width: 36px; height: 36px; border-radius: 8px; background: rgba(34, 197, 94, 0.2); display: flex; align-items: center; justify-content: center; color: #22c55e; font-size: 18px;">
              🌐
            </div>
            <div>
              <div style="font-size: 11px; font-weight: 600; text-transform: uppercase; letter-spacing: 0.5px; color: #22c55e;">Tên miền chính thức (HTTPS - 24/7):</div>
              <a href="${primaryUrl}" target="_blank" style="font-size: 15px; font-weight: 700; color: #ffffff; text-decoration: none;">${primaryUrl}</a>
            </div>
          </div>
          <div style="display: flex; gap: 6px;">
            <button class="btn btn-secondary btn-sm" onclick="RemoteManager.copyToClipboard('${primaryUrl}')">Sao chép</button>
            <a href="${primaryUrl}" target="_blank" class="btn btn-primary btn-sm" style="text-decoration: none; display: inline-flex; align-items: center; gap: 4px;">Truy cập ngay ↗</a>
          </div>
        </div>

        <div style="background: rgba(59, 130, 246, 0.1); border: 1px solid rgba(59, 130, 246, 0.3); padding: 12px 16px; border-radius: 8px; display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 10px;">
          <div style="display: flex; align-items: center; gap: 12px;">
            <div style="width: 36px; height: 36px; border-radius: 8px; background: rgba(59, 130, 246, 0.2); display: flex; align-items: center; justify-content: center; color: #3b82f6; font-size: 18px;">
              📁
            </div>
            <div>
              <div style="font-size: 11px; font-weight: 600; text-transform: uppercase; letter-spacing: 0.5px; color: #3b82f6;">Đường dẫn WebDAV Mount Từ Xa:</div>
              <a href="${webdavUrl}" target="_blank" style="font-size: 14px; font-weight: 700; color: #ffffff; text-decoration: none;">${webdavUrl}</a>
            </div>
          </div>
          <div style="display: flex; gap: 6px;">
            <button class="btn btn-secondary btn-sm" onclick="RemoteManager.copyToClipboard('${webdavUrl}')">Sao chép WebDAV</button>
            <button class="btn btn-primary btn-sm" onclick="RemoteManager.copyToClipboard('net use Z: ${webdavUrl} /user:admin [PASSWORD]')">Sao chép lệnh Mount</button>
          </div>
        </div>

        <div style="background: var(--bg-hover); border: 1px solid var(--border-subtle); padding: 10px 16px; border-radius: 8px; display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 10px;">
          <div style="display: flex; align-items: center; gap: 10px;">
            <span style="font-size: 12px; color: var(--text-muted);">Tên miền phụ dự phòng:</span>
            <a href="${altUrl}" target="_blank" style="font-size: 13px; font-weight: 600; color: var(--accent-blue); text-decoration: none;">${altUrl}</a>
          </div>
          <div style="display: flex; gap: 6px;">
            <button class="btn btn-secondary btn-sm" onclick="RemoteManager.copyToClipboard('${altUrl}')">Sao chép</button>
            <a href="${altUrl}" target="_blank" class="btn btn-secondary btn-sm" style="text-decoration: none; display: inline-flex; align-items: center; gap: 4px;">Mở ↗</a>
          </div>
        </div>
      </div>
    `;
  }
};
