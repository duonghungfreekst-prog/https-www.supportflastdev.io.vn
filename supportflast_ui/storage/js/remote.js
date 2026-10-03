// ==========================================================================
// CloudPool Remote Access Controller (PRO Edition)
// ==========================================================================

const RemoteManager = {
  async loadRemoteInfo() {
    try {
      const data = await API.getRemoteInfo();
      
      // Update Windows Mount Command
      const winCmdEl = document.getElementById('windows-mount-code');
      if (winCmdEl) {
        let mountCmd = data.windows_mount_cmd || 'net use Z: https://supportflastdev.io.vn/webdav /user:admin [PASSWORD]';
        if (window.location.hostname !== 'localhost' && window.location.hostname !== '127.0.0.1') {
          mountCmd = mountCmd.replace(/http:\/\/(localhost|127\.0\.0\.1)(:\d+)?\/webdav/g, 'https://supportflastdev.io.vn/webdav');
        }
        winCmdEl.innerHTML = `
          <div style="display: flex; justify-content: space-between; align-items: center; gap: 10px; flex-wrap: wrap;">
            <span style="word-break: break-all; font-size: 12px;">${mountCmd}</span>
            <button class="btn btn-secondary btn-sm" onclick="RemoteManager.copyToClipboard('${mountCmd.replace(/'/g, "\\'")}')">
              Sao chép
            </button>
          </div>
        `;
      }

      // Update LAN Access URLs
      const lanUrlEl = document.getElementById('lan-access-url');
      if (lanUrlEl) {
        if (data.lan_web_urls && data.lan_web_urls.length > 0) {
          lanUrlEl.innerHTML = data.lan_web_urls.map(url => `
            <div class="lan-access-card">
              <div class="lan-access-info">
                <div class="lan-access-row">
                  <span class="lan-tag">Web UI:</span>
                  <a href="${url}" target="_blank" class="lan-url">${url}</a>
                </div>
                <div class="lan-access-row">
                  <span class="lan-tag dav">WebDAV:</span>
                  <span class="lan-url dav">${url}/webdav</span>
                </div>
              </div>
              <div class="lan-access-actions">
                <button class="btn btn-secondary btn-sm" onclick="RemoteManager.copyToClipboard('${url}')">Copy Web</button>
                <button class="btn btn-secondary btn-sm" onclick="RemoteManager.copyToClipboard('${url}/webdav')">Copy WebDAV</button>
              </div>
            </div>
          `).join('');
        } else {
          lanUrlEl.textContent = `Web UI: ${data.local_url}\nWebDAV: ${data.webdav_url}`;
        }
      }
    } catch (err) {
      console.error('Lỗi tải thông tin remote:', err);
    }
  },

  copyToClipboard(text) {
    if (navigator.clipboard) {
      navigator.clipboard.writeText(text).then(() => {
        Toast.success('Đã sao chép vào bộ nhớ đệm!');
      }).catch(() => {
        this.fallbackCopy(text);
      });
    } else {
      this.fallbackCopy(text);
    }
  },

  fallbackCopy(text) {
    const ta = document.createElement('textarea');
    ta.value = text;
    document.body.appendChild(ta);
    ta.select();
    document.execCommand('copy');
    document.body.removeChild(ta);
    Toast.success('Đã sao chép vào bộ nhớ đệm!');
  },
};
