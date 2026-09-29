// ==========================================================================
// CloudPool Media & Document Preview Controller (PRO Edition)
// ==========================================================================

const PreviewManager = {
  currentCodeText: '',

  isTextOrCode(name, mime) {
    const ext = name.toLowerCase().split('.').pop();
    const codeExts = ['txt', 'log', 'md', 'markdown', 'json', 'sql', 'go', 'rs', 'js', 'ts', 'jsx', 'tsx', 'py', 'html', 'htm', 'css', 'scss', 'xml', 'yaml', 'yml', 'sh', 'bat', 'cmd', 'ps1', 'ini', 'conf', 'cfg', 'env', 'proto'];
    if (codeExts.includes(ext)) return true;
    if (mime && (mime.startsWith('text/') || mime === 'application/json' || mime === 'application/xml')) return true;
    return false;
  },

  async openTextPreview(fileId, fileName, streamURL) {
    const modal = document.getElementById('modal-code-viewer');
    const titleEl = document.getElementById('code-viewer-title');
    const infoEl = document.getElementById('code-viewer-info');
    const container = document.getElementById('code-viewer-container');
    const footerMeta = document.getElementById('code-viewer-footer-meta');

    if (!modal || !container) return;

    titleEl.textContent = fileName;
    infoEl.textContent = 'Đang tải dữ liệu...';
    container.textContent = 'Đang tải tệp tin từ Google Drive...';
    modal.classList.add('active');

    try {
      const res = await fetch(streamURL);
      if (!res.ok) throw new Error(`HTTP Error ${res.status}`);
      const text = await res.text();
      this.currentCodeText = text;

      const lines = text.split('\n');
      infoEl.textContent = `${lines.length} dòng • ${Utils.formatBytes(text.length)}`;
      if (footerMeta) footerMeta.textContent = `Định dạng: ${fileName.split('.').pop().toUpperCase()} • Mã hóa: UTF-8`;

      // Render line numbered code view
      let formattedHtml = '';
      lines.forEach((line, idx) => {
        const lineNum = idx + 1;
        const escaped = line.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
        formattedHtml += `<div style="display: flex; min-height: 19px;"><span style="user-select: none; width: 45px; text-align: right; margin-right: 16px; color: #475569; font-size: 11px;">${lineNum}</span><span style="flex: 1; white-space: pre-wrap; word-break: break-all;">${escaped || ' '}</span></div>`;
      });
      container.innerHTML = formattedHtml || '<span style="color: #64748b;">(Tệp trống)</span>';
    } catch (err) {
      container.innerHTML = `<span style="color: #ef4444;">Lỗi khi đọc tệp tin: ${err.message}</span>`;
      infoEl.textContent = 'Lỗi nạp';
    }
  },

  copyCodeContent() {
    if (!this.currentCodeText) return;
    navigator.clipboard.writeText(this.currentCodeText).then(() => {
      Toast.success('Đã sao chép toàn bộ nội dung tệp!');
    }).catch(() => {
      Toast.error('Không thể sao chép');
    });
  },

  openPreview(fileId, fileName, mimeType, otpCode = '') {
    let streamURL = `/api/files/stream?id=${encodeURIComponent(fileId)}`;
    let downloadURL = `/api/files/download?id=${encodeURIComponent(fileId)}`;

    // Gắn token xác thực nếu có
    const user = (typeof API !== 'undefined' && API.getCurrentUser) ? API.getCurrentUser() : null;
    const authToken = (typeof API !== 'undefined' && API.getToken) ? API.getToken() : (localStorage.getItem('cloudpool_jwt_token') || localStorage.getItem('cloudpool_token') || sessionStorage.getItem('cloudpool_token') || (user && user.token ? user.token : ''));
    if (authToken) {
      streamURL += `&token=${encodeURIComponent(authToken)}`;
      downloadURL += `&token=${encodeURIComponent(authToken)}`;
    }

    if (otpCode) {
      streamURL += `&otp=${encodeURIComponent(otpCode)}`;
      downloadURL += `&otp=${encodeURIComponent(otpCode)}`;
    }

    // Check if Text or Code file
    if (this.isTextOrCode(fileName, mimeType)) {
      this.openTextPreview(fileId, fileName, streamURL);
      return;
    }

    const modal = document.getElementById('modal-preview');
    const titleEl = document.getElementById('preview-file-title');
    const area = document.getElementById('preview-content-area');

    titleEl.textContent = fileName;
    area.innerHTML = '';

    const lowerName = fileName.toLowerCase();
    const isPDF = (mimeType === 'application/pdf' || lowerName.endsWith('.pdf'));
    const isPPT = (/\.(pptx|ppt|ppsx|key|odp)$/i.test(lowerName));
    const isExcel = (/\.(xlsx|xls|ods|numbers)$/i.test(lowerName));
    const isWord = (/\.(docx|doc|odt|rtf)$/i.test(lowerName));
    const isCSV = (/\.(csv|tsv)$/i.test(lowerName));

    if (isPDF) {
      area.innerHTML = `
        <div style="width: 100%; display: flex; flex-direction: column; gap: 8px;">
          <iframe
            src="${streamURL}"
            style="width: 100%; height: 75vh; border: none; border-radius: var(--radius-md); background: #525659;"
            title="${fileName}"
            loading="lazy"
          ></iframe>
          <div style="display: flex; justify-content: flex-end; gap: 8px;">
            <button class="btn btn-secondary btn-sm" onclick="FilesManager.showChunkMap('${fileId}', '${fileName}')">
              🔍 Xem bản đồ Chunks
            </button>
            <a class="btn btn-primary btn-sm" href="${downloadURL}" download="${fileName}">
              Tải PDF về máy
            </a>
          </div>
        </div>
      `;

    } else if (isPPT) {
      // PowerPoint Presentation Native Slide Deck Viewer
      const viewer = (typeof OfficeViewer !== 'undefined' ? OfficeViewer : (typeof window !== 'undefined' ? window.OfficeViewer : null));
      if (viewer && typeof viewer.renderPPTX === 'function') {
        viewer.renderPPTX(area, streamURL, fileName, downloadURL);
      } else {
        area.innerHTML = `
          <div style="padding: 30px; text-align: center;">
            <div style="font-size: 40px; margin-bottom: 12px;">📽️</div>
            <div style="font-size: 15px; font-weight: 700; color: var(--accent-amber);">${fileName}</div>
            <div style="font-size: 12px; color: var(--text-muted); margin: 10px 0 20px 0;">Bản trình chiếu PowerPoint</div>
            <a href="${downloadURL}" class="btn btn-primary" download="${fileName}">⬇️ Tải File PPTX Về Máy</a>
          </div>
        `;
      }
    } else if (isExcel) {
      // Native Excel Spreadsheet Viewer (Zero External Dependency)
      const viewer = (typeof OfficeViewer !== 'undefined' ? OfficeViewer : (typeof window !== 'undefined' ? window.OfficeViewer : null));
      if (viewer && typeof viewer.renderExcel === 'function') {
        viewer.renderExcel(area, streamURL, fileName, downloadURL);
      } else {
        area.innerHTML = `
          <div style="padding: 30px; text-align: center;">
            <div style="font-size: 40px; margin-bottom: 12px;">📊</div>
            <div style="font-size: 15px; font-weight: 700; color: #10b981;">${fileName}</div>
            <div style="font-size: 12px; color: var(--text-muted); margin: 10px 0 20px 0;">Bảng tính Excel</div>
            <a href="${downloadURL}" class="btn btn-primary" download="${fileName}">⬇️ Tải File Excel Về Máy</a>
          </div>
        `;
      }
    } else if (isWord) {
      // Native Word Document Viewer (Zero External Dependency)
      const viewer = (typeof OfficeViewer !== 'undefined' ? OfficeViewer : (typeof window !== 'undefined' ? window.OfficeViewer : null));
      if (viewer && typeof viewer.renderDocx === 'function') {
        viewer.renderDocx(area, streamURL, fileName, downloadURL);
      } else {
        area.innerHTML = `
          <div style="padding: 30px; text-align: center;">
            <div style="font-size: 40px; margin-bottom: 12px;">📄</div>
            <div style="font-size: 15px; font-weight: 700; color: #38bdf8;">${fileName}</div>
            <div style="font-size: 12px; color: var(--text-muted); margin: 10px 0 20px 0;">Tài liệu Word</div>
            <a href="${downloadURL}" class="btn btn-primary" download="${fileName}">⬇️ Tải File Word Về Máy</a>
          </div>
        `;
      }
    } else if (isCSV) {
      // Interactive CSV Spreadsheet Table
      area.innerHTML = `
        <div style="width: 100%; display: flex; flex-direction: column; gap: 10px;">
          <div style="display: flex; justify-content: space-between; align-items: center;">
            <span style="font-size: 13px; color: var(--accent-cyan); font-weight: 600;">📊 Bảng Dữ Liệu Phân Tách (${fileName})</span>
            <input type="text" id="csv-filter-input" placeholder="🔍 Lọc dòng dữ liệu..." class="form-control" style="max-width: 250px; font-size: 12px; padding: 4px 10px;">
          </div>
          <div id="csv-table-wrapper" style="width: 100%; max-height: 65vh; overflow: auto; border: 1px solid var(--border-subtle); border-radius: var(--radius-md); background: #0d1117;">
            <div style="padding: 24px; text-align: center; color: var(--text-muted);">Đang nạp và phân tích dữ liệu bảng...</div>
          </div>
          <div style="display: flex; justify-content: flex-end; gap: 8px;">
            <button class="btn btn-secondary btn-sm" onclick="FilesManager.showChunkMap('${fileId}', '${fileName}')">
              🔍 Xem bản đồ Chunks
            </button>
            <a class="btn btn-primary btn-sm" href="${downloadURL}" download="${fileName}">
              ⬇️ Tải CSV về máy
            </a>
          </div>
        </div>
      `;

      fetch(streamURL)
        .then(r => r.text())
        .then(csvText => {
          const wrapper = document.getElementById('csv-table-wrapper');
          if (!wrapper) return;
          const rows = csvText.split('\n').filter(r => r.trim().length > 0);
          if (rows.length === 0) {
            wrapper.innerHTML = '<div style="padding: 24px; text-align: center; color: var(--text-muted);">Tệp CSV trống</div>';
            return;
          }

          const parseRow = (line) => line.split(',').map(c => c.replace(/^"|"$/g, '').trim());
          const headerCols = parseRow(rows[0]);
          let tableHtml = '<table class="files-table" style="font-size: 12px; margin: 0; min-width: 100%;"><thead><tr>';
          headerCols.forEach(col => {
            tableHtml += `<th style="padding: 8px 12px; background: rgba(255,255,255,0.05);">${col}</th>`;
          });
          tableHtml += '</tr></thead><tbody id="csv-tbody">';
          for (let i = 1; i < Math.min(rows.length, 500); i++) {
            const cols = parseRow(rows[i]);
            tableHtml += '<tr>';
            cols.forEach(c => {
              tableHtml += `<td style="padding: 6px 12px; border-bottom: 1px solid rgba(255,255,255,0.04);">${c}</td>`;
            });
            tableHtml += '</tr>';
          }
          tableHtml += '</tbody></table>';
          if (rows.length > 500) {
            tableHtml += `<div style="padding: 8px; text-align: center; font-size: 11px; color: var(--text-muted); background: rgba(255,255,255,0.02);">Hiển thị 500 / ${rows.length} dòng đầu tiên. Tải về để xem toàn bộ.</div>`;
          }
          wrapper.innerHTML = tableHtml;

          // Search filter for CSV
          document.getElementById('csv-filter-input')?.addEventListener('input', (e) => {
            const q = e.target.value.toLowerCase().trim();
            const trs = document.querySelectorAll('#csv-tbody tr');
            trs.forEach(tr => {
              tr.style.display = tr.textContent.toLowerCase().includes(q) ? '' : 'none';
            });
          });
        })
        .catch(err => {
          const wrapper = document.getElementById('csv-table-wrapper');
          if (wrapper) wrapper.innerHTML = `<div style="padding: 20px; color: #ef4444;">Lỗi: ${err.message}</div>`;
        });
    } else if (mimeType.startsWith('video/')) {
      area.innerHTML = `
        <div style="width: 100%; display: flex; flex-direction: column; gap: 8px;">
          <div id="video-loading-status" style="display: flex; align-items: center; justify-content: center; gap: 8px; padding: 8px 12px; background: rgba(59, 130, 246, 0.08); border: 1px solid rgba(59, 130, 246, 0.2); border-radius: var(--radius-md); font-size: 12px; color: var(--accent-blue);">
            <span>⚡ Đang đệm luồng video trực tuyến từ Google Drive...</span>
          </div>
          <video id="pro-video-player" controls autoplay preload="auto" playsinline style="width: 100%; max-height: 60vh; border-radius: var(--radius-md); background: #000;">
            <source src="${streamURL}" type="${mimeType}">
            Trình duyệt của bạn không hỗ trợ phát trực tiếp định dạng video này.
          </video>
          <div id="video-error-status" style="display: none; padding: 14px; background: rgba(239, 68, 68, 0.1); border: 1px solid rgba(239, 68, 68, 0.25); border-radius: var(--radius-md); text-align: center;">
            <div id="video-error-msg" style="color: #ef4444; font-size: 13px; font-weight: 500; margin-bottom: 10px; line-height: 1.5;">⚠️ Không thể phát trực tiếp trên trình duyệt này.</div>
            <div style="display: flex; gap: 8px; justify-content: center; flex-wrap: wrap;">
              <button class="btn btn-secondary btn-sm" onclick="PreviewManager.retryVideoPlayback()">🔄 Thử phát lại</button>
              <a class="btn btn-primary btn-sm" href="${downloadURL}" download="${fileName}">⬇️ Tải tệp video về máy</a>
            </div>
          </div>
          <div style="display: flex; justify-content: space-between; align-items: center; padding: 4px 8px; font-size: 12px; color: var(--text-secondary); flex-wrap: wrap; gap: 8px;">
            <div style="display: flex; gap: 6px; align-items: center;">
              <span>Tốc độ:</span>
              <span class="video-speed-badge" onclick="PreviewManager.setVideoSpeed(0.5)">0.5x</span>
              <span class="video-speed-badge" onclick="PreviewManager.setVideoSpeed(1.0)" style="color: var(--accent-blue);">1x</span>
              <span class="video-speed-badge" onclick="PreviewManager.setVideoSpeed(1.25)">1.25x</span>
              <span class="video-speed-badge" onclick="PreviewManager.setVideoSpeed(1.5)">1.5x</span>
              <span class="video-speed-badge" onclick="PreviewManager.setVideoSpeed(2.0)">2x</span>
            </div>
            <div style="display: flex; gap: 8px;">
              <a class="btn btn-primary btn-sm" href="${downloadURL}" download="${fileName}">
                ⬇️ Tải về máy
              </a>
              <button class="btn btn-secondary btn-sm" onclick="FilesManager.showChunkMap('${fileId}', '${fileName}')">
                🔍 Xem bản đồ Chunks
              </button>
            </div>
          </div>
        </div>
      `;

      const vidPlayer = document.getElementById('pro-video-player');
      const loadStatus = document.getElementById('video-loading-status');
      const errStatus = document.getElementById('video-error-status');
      const errMsg = document.getElementById('video-error-msg');

      if (vidPlayer) {
        vidPlayer.addEventListener('playing', () => {
          if (loadStatus) loadStatus.style.display = 'none';
          if (errStatus) errStatus.style.display = 'none';
        });
        vidPlayer.addEventListener('canplay', () => {
          if (loadStatus) loadStatus.style.display = 'none';
        });
        vidPlayer.addEventListener('waiting', () => {
          if (loadStatus) {
            loadStatus.style.display = 'flex';
            loadStatus.innerHTML = '<span>⚡ Đang nạp tiếp luồng video từ đám mây Google Drive...</span>';
          }
        });
        vidPlayer.addEventListener('stalled', () => {
          if (loadStatus) {
            loadStatus.style.display = 'flex';
            loadStatus.innerHTML = '<span>⚡ Đang đệm dữ liệu video...</span>';
          }
        });
        vidPlayer.addEventListener('error', () => {
          const err = vidPlayer.error;
          if (loadStatus) loadStatus.style.display = 'none';
          // Bỏ qua lỗi 1 (MEDIA_ERR_ABORTED - xảy ra khi người dùng dừng hoặc đổi đoạn)
          if (!err || err.code === 1) return;

          if (errStatus) {
            errStatus.style.display = 'block';
            let detail = 'Không thể phát trực tiếp định dạng video này trên trình duyệt hiện tại.';
            if (err.code === 4) { // MEDIA_ERR_SRC_NOT_SUPPORTED
              detail = 'Video sử dụng định dạng nén camera cao cấp (H.265 / HEVC 10-bit HDR) mà trình duyệt máy tính chưa hỗ trợ sẵn bộ giải mã phần cứng. Bạn có thể mở trực tiếp trên điện thoại di động hoặc tải về xem offline qua VLC / Media Player:';
            } else if (err.code === 2) { // MEDIA_ERR_NETWORK
              detail = 'Gián đoạn kết nối mạng khi tải luồng dữ liệu đám mây. Bạn có thể nhấn Thử lại:';
            } else if (err.code === 3) { // MEDIA_ERR_DECODE
              detail = 'Bộ giải mã video trình duyệt gặp lỗi trong quá trình xử lý khung hình:';
            }
            if (errMsg) errMsg.textContent = '⚠️ ' + detail;
          }
        });
      }
    } else if (mimeType.startsWith('audio/')) {
      area.innerHTML = `
        <div style="padding: 36px; text-align: center; width: 100%;">
          <svg width="64" height="64" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="color: var(--accent-amber); margin-bottom: 20px;"><path d="M9 18V5l12-2v13"></path><circle cx="6" cy="18" r="3"></circle><circle cx="18" cy="16" r="3"></circle></svg>
          <audio controls autoplay style="width: 100%; max-width: 500px;">
            <source src="${streamURL}" type="${mimeType}">
            Trình duyệt không hỗ trợ phát nhạc.
          </audio>
          <div style="margin-top: 16px;">
            <button class="btn btn-secondary btn-sm" onclick="FilesManager.showChunkMap('${fileId}', '${fileName}')">
              🔍 Xem bản đồ Chunks
            </button>
          </div>
        </div>
      `;
    } else if (mimeType.startsWith('image/')) {
      area.innerHTML = `
        <div style="display: flex; flex-direction: column; align-items: center; gap: 10px; width: 100%;">
          <img src="${streamURL}" alt="${fileName}" style="max-width: 100%; max-height: 60vh; object-fit: contain; border-radius: var(--radius-md);">
          <button class="btn btn-secondary btn-sm" onclick="FilesManager.showChunkMap('${fileId}', '${fileName}')">
            🔍 Xem bản đồ Chunks
          </button>
        </div>
      `;
    } else {
      area.innerHTML = `
        <div style="padding: 40px; text-align: center;">
          <svg width="56" height="56" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="color: var(--text-secondary); margin-bottom: 16px;"><path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"></path><polyline points="13 2 13 9 20 9"></polyline></svg>
          <p style="font-size: 15px; font-weight: 600; margin-bottom: 8px;">${fileName}</p>
          <p style="font-size: 12px; color: var(--text-secondary); margin-bottom: 20px;">Tệp dữ liệu nhị phân đã được mã hóa an toàn trên Google Drive.</p>
          <div style="display: flex; gap: 10px; justify-content: center;">
            <button class="btn btn-secondary btn-sm" onclick="FilesManager.showChunkMap('${fileId}', '${fileName}')">
              🔍 Xem bản đồ Chunks
            </button>
            <a class="btn btn-primary btn-sm" href="${downloadURL}" download="${fileName}">
              Tải tệp về máy
            </a>
          </div>
        </div>
      `;
    }

    modal.classList.add('active');
  },

  openPreviewWithOTP(fileId, fileName, mimeType, otpCode) {
    this.openPreview(fileId, fileName, mimeType, otpCode);
  },

  setVideoSpeed(speed) {
    const video = document.getElementById('pro-video-player');
    if (video) {
      video.playbackRate = speed;
      document.querySelectorAll('.video-speed-badge').forEach(badge => {
        badge.style.color = badge.textContent.includes(speed + 'x') ? 'var(--accent-blue)' : 'var(--text-secondary)';
      });
    }
  },

  retryVideoPlayback() {
    const video = document.getElementById('pro-video-player');
    const errStatus = document.getElementById('video-error-status');
    const loadStatus = document.getElementById('video-loading-status');
    if (video) {
      if (errStatus) errStatus.style.display = 'none';
      if (loadStatus) {
        loadStatus.style.display = 'flex';
        loadStatus.innerHTML = '<span>⚡ Đang kết nối lại luồng video...</span>';
      }
      video.load();
      video.play().catch(() => {});
    }
  },
};

