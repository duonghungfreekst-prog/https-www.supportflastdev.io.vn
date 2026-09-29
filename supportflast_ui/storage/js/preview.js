// ==========================================================================
// CloudPool Media & Document Preview Controller (PRO Edition)
// Supports PDF, Office (PPTX, DOCX, XLSX, CSV, TSV), 50+ Code & Text formats
// ==========================================================================

const PreviewManager = {
  currentCodeText: '',
  currentFileName: '',
  currentDownloadURL: '',
  codeLines: [],
  isWordWrap: false,
  codeSearchQuery: '',

  // Hỗ trợ hơn 50 định dạng Code, Data, Config và Văn bản
  isTextOrCode(name, mime) {
    if (!name) return false;
    const ext = name.toLowerCase().split('.').pop();
    const codeExts = [
      // Text & Documents
      'txt', 'log', 'md', 'markdown', 'rst', 'tex', 'rtf', 'nfo',
      // Web
      'html', 'htm', 'xhtml', 'css', 'scss', 'sass', 'less', 'js', 'mjs', 'cjs', 'ts', 'jsx', 'tsx', 'vue', 'svelte',
      // Backend & Systems
      'py', 'pyw', 'go', 'rs', 'c', 'cpp', 'cc', 'cxx', 'h', 'hpp', 'cs', 'java', 'kt', 'kts', 'php', 'rb', 'r', 'dart', 'lua', 'swift', 'scala', 'v', 'zig', 'asm',
      // Shell & Scripts
      'sh', 'bash', 'zsh', 'bat', 'cmd', 'ps1', 'psm1', 'psd1',
      // Data & Config
      'json', 'jsonc', 'json5', 'sql', 'xml', 'xaml', 'yaml', 'yml', 'toml', 'ini', 'conf', 'cfg', 'env', 'proto', 'graphql', 'gql',
      // Version Control & Build
      'diff', 'patch', 'dockerfile', 'makefile', 'gitignore', 'gitattributes', 'lock', 'prisma'
    ];

    if (codeExts.includes(ext)) return true;
    if (mime && (
      mime.startsWith('text/') ||
      mime === 'application/json' ||
      mime === 'application/xml' ||
      mime === 'application/javascript' ||
      mime === 'application/x-sh' ||
      mime === 'application/sql' ||
      mime === 'application/x-yaml'
    )) return true;

    return false;
  },

  getLanguageMeta(fileName) {
    const ext = (fileName || '').toLowerCase().split('.').pop();
    const metaMap = {
      go: { name: 'Go', icon: '🐹' },
      rs: { name: 'Rust', icon: '🦀' },
      py: { name: 'Python', icon: '🐍' },
      js: { name: 'JavaScript', icon: '🟨' },
      ts: { name: 'TypeScript', icon: '🔷' },
      jsx: { name: 'React JSX', icon: '⚛️' },
      tsx: { name: 'React TSX', icon: '⚛️' },
      json: { name: 'JSON', icon: '📦' },
      sql: { name: 'SQL', icon: '🗄️' },
      html: { name: 'HTML', icon: '🌐' },
      css: { name: 'CSS', icon: '🎨' },
      scss: { name: 'SCSS', icon: '🎨' },
      sh: { name: 'Shell Script', icon: '🐚' },
      bash: { name: 'Bash', icon: '🐚' },
      bat: { name: 'Batch', icon: '⚙️' },
      cmd: { name: 'Command', icon: '⚙️' },
      ps1: { name: 'PowerShell', icon: '💻' },
      md: { name: 'Markdown', icon: '📝' },
      xml: { name: 'XML', icon: '📰' },
      yaml: { name: 'YAML', icon: '⚙️' },
      yml: { name: 'YAML', icon: '⚙️' },
      toml: { name: 'TOML', icon: '⚙️' },
      c: { name: 'C', icon: '⚙️' },
      cpp: { name: 'C++', icon: '⚙️' },
      cs: { name: 'C#', icon: '🟣' },
      java: { name: 'Java', icon: '☕' },
      kt: { name: 'Kotlin', icon: '🟣' },
      php: { name: 'PHP', icon: '🐘' },
      rb: { name: 'Ruby', icon: '💎' },
      dart: { name: 'Dart', icon: '🎯' },
      lua: { name: 'Lua', icon: '🌙' },
      txt: { name: 'Văn Bản', icon: '📄' },
      log: { name: 'Log File', icon: '📋' }
    };
    return metaMap[ext] || { name: ext.toUpperCase() || 'VĂN BẢN', icon: '📄' };
  },

  async openTextPreview(fileId, fileName, streamURL, downloadURL = '') {
    const modal = document.getElementById('modal-code-viewer');
    const titleEl = document.getElementById('code-viewer-title');
    const infoEl = document.getElementById('code-viewer-info');
    const container = document.getElementById('code-viewer-container');
    const footerMeta = document.getElementById('code-viewer-footer-meta');
    const iconEl = document.getElementById('code-viewer-icon');
    const langBadge = document.getElementById('code-viewer-lang-badge');
    const downloadBtn = document.getElementById('btn-download-code-file');
    const formatJsonBtn = document.getElementById('btn-format-json');
    const searchInput = document.getElementById('code-viewer-search-input');
    const searchClear = document.getElementById('code-viewer-search-clear');

    if (!modal || !container) return;

    this.currentFileName = fileName;
    this.currentDownloadURL = downloadURL || streamURL.replace('/stream', '/download');
    this.currentCodeText = '';
    this.codeLines = [];
    this.codeSearchQuery = '';
    this.isWordWrap = false;

    const langMeta = this.getLanguageMeta(fileName);
    if (titleEl) titleEl.textContent = fileName;
    if (iconEl) iconEl.textContent = langMeta.icon;
    if (langBadge) langBadge.textContent = langMeta.name;
    if (infoEl) infoEl.textContent = 'Đang tải dữ liệu...';
    if (downloadBtn) {
      downloadBtn.href = this.currentDownloadURL;
      downloadBtn.download = fileName;
    }
    if (formatJsonBtn) {
      formatJsonBtn.style.display = fileName.toLowerCase().endsWith('.json') ? 'inline-flex' : 'none';
    }
    if (searchInput) searchInput.value = '';
    if (searchClear) searchClear.style.display = 'none';

    this._updateWrapLabel();

    container.innerHTML = `
      <div style="padding: 40px; text-align: center; color: var(--text-muted, #94a3b8);">
        <div style="font-size: 32px; margin-bottom: 12px; animation: spin 1.2s linear infinite;">⏳</div>
        <div style="font-size: 14px; font-weight: 600; color: #38bdf8;">Đang nạp nội dung tệp tin...</div>
      </div>
    `;

    modal.classList.add('active');

    try {
      const headers = { 'ngrok-skip-browser-warning': 'true' };
      try {
        const u = localStorage.getItem('cloudpool_current_user');
        if (u) {
          const user = JSON.parse(u);
          if (user && user.id) headers['X-User-ID'] = user.id;
        }
      } catch (_) {}

      const res = await fetch(streamURL, { headers, credentials: 'include' });
      if (!res.ok) throw new Error(`Lỗi kết nối HTTP ${res.status}`);
      const text = await res.text();
      this.currentCodeText = text;
      this.codeLines = text.split('\n');

      const byteLength = new Blob([text]).size;
      const formattedSize = (typeof Utils !== 'undefined' && Utils.formatBytes) ? Utils.formatBytes(byteLength) : `${(byteLength / 1024).toFixed(1)} KB`;
      if (infoEl) infoEl.textContent = `${this.codeLines.length.toLocaleString()} dòng • ${formattedSize} • ${text.length.toLocaleString()} ký tự`;
      if (footerMeta) footerMeta.textContent = `Định dạng: ${langMeta.name} • Mã hóa: UTF-8 • ${this.codeLines.length} dòng`;

      this.renderCodeLines();

      // Hook search event
      if (searchInput) {
        searchInput.oninput = (e) => {
          this.codeSearchQuery = (e.target.value || '').trim();
          if (searchClear) searchClear.style.display = this.codeSearchQuery ? 'block' : 'none';
          this.renderCodeLines();
        };
      }
      if (searchClear) {
        searchClear.onclick = () => {
          if (searchInput) searchInput.value = '';
          searchClear.style.display = 'none';
          this.codeSearchQuery = '';
          this.renderCodeLines();
        };
      }

    } catch (err) {
      console.warn('Text preview error:', err);
      if (infoEl) infoEl.textContent = 'Lỗi nạp tệp';
      container.innerHTML = `
        <div style="padding: 40px 20px; text-align: center; color: #ef4444; max-width: 520px; margin: 0 auto;">
          <div style="font-size: 42px; margin-bottom: 12px;">⚠️</div>
          <div style="font-size: 15px; font-weight: 700; margin-bottom: 8px;">Không thể đọc nội dung tệp tin trực tiếp</div>
          <div style="font-size: 12.5px; color: #94a3b8; margin-bottom: 20px; line-height: 1.5;">${err.message || 'Lỗi mạng hoặc tệp dữ liệu không hợp lệ.'}</div>
          <div style="display: flex; gap: 10px; justify-content: center;">
            <a href="${this.currentDownloadURL}" class="btn btn-primary btn-sm" download="${fileName}" style="padding: 6px 16px; font-size: 12px; text-decoration: none;">
              ⬇️ Tải Tệp Về Máy
            </a>
          </div>
        </div>
      `;
    }
  },

  renderCodeLines() {
    const container = document.getElementById('code-viewer-container');
    if (!container) return;

    if (!this.codeLines || this.codeLines.length === 0) {
      container.innerHTML = '<div style="padding: 24px; color: #64748b; text-align: center;">(Tệp tin trống)</div>';
      return;
    }

    const q = this.codeSearchQuery.toLowerCase();
    const isWrap = this.isWordWrap;
    let matchCount = 0;

    let html = `
      <table style="width: 100%; border-collapse: collapse; font-family: inherit; font-size: inherit; line-height: inherit;">
        <tbody>
    `;

    const highlightSearch = (escapedText) => {
      if (!q) return escapedText;
      const regex = new RegExp(`(${q.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')})`, 'gi');
      return escapedText.replace(regex, '<mark style="background: rgba(245,158,11,0.4); color: #fef08a; padding: 0 2px; border-radius: 2px;">$1</mark>');
    };

    // Render limit for performance on huge files (e.g. 5,000 lines)
    const maxRender = Math.min(this.codeLines.length, 5000);

    for (let idx = 0; idx < maxRender; idx++) {
      const line = this.codeLines[idx];
      const lineNum = idx + 1;
      const isMatch = q && line.toLowerCase().includes(q);
      if (isMatch) matchCount++;

      const escaped = line
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;');

      const rowBg = isMatch
        ? 'background: rgba(245, 158, 11, 0.12);'
        : (idx % 2 === 0 ? 'background: rgba(255, 255, 255, 0.01);' : 'background: transparent;');

      const codeWhiteSpace = isWrap ? 'white-space: pre-wrap; word-break: break-all;' : 'white-space: pre;';

      html += `
        <tr class="code-line-row" style="${rowBg} transition: background 0.1s;">
          <td style="width: 52px; min-width: 52px; text-align: right; padding: 1px 12px 1px 6px; color: #475569; font-size: 11px; user-select: none; border-right: 1px solid rgba(255, 255, 255, 0.06); vertical-align: top; background: #080c14;">
            ${lineNum}
          </td>
          <td style="padding: 1px 16px; ${codeWhiteSpace} color: #e2e8f0; vertical-align: top;">
            ${highlightSearch(escaped) || '&nbsp;'}
          </td>
        </tr>
      `;
    }

    if (this.codeLines.length > 5000) {
      html += `
        <tr>
          <td colspan="2" style="padding: 16px; text-align: center; color: #94a3b8; font-style: italic; background: rgba(0,0,0,0.3);">
            Đã hiển thị 5,000 / ${this.codeLines.length.toLocaleString()} dòng đầu tiên. Tải tệp về máy để xem trọn vẹn tệp cực lớn.
          </td>
        </tr>
      `;
    }

    html += `
        </tbody>
      </table>
    `;

    container.innerHTML = html;

    const infoEl = document.getElementById('code-viewer-info');
    if (infoEl && q) {
      infoEl.textContent = `Tìm thấy ${matchCount} dòng khớp với "${q}" • ${this.codeLines.length} dòng`;
    }
  },

  toggleWordWrap() {
    this.isWordWrap = !this.isWordWrap;
    this._updateWrapLabel();
    this.renderCodeLines();
  },

  _updateWrapLabel() {
    const label = document.getElementById('label-word-wrap');
    const btn = document.getElementById('btn-toggle-word-wrap');
    if (label) label.textContent = this.isWordWrap ? 'Wrap: Bật' : 'Wrap: Tắt';
    if (btn) {
      btn.style.borderColor = this.isWordWrap ? 'var(--accent-blue, #3b82f6)' : '';
      btn.style.background = this.isWordWrap ? 'rgba(59, 130, 246, 0.2)' : '';
      btn.style.color = this.isWordWrap ? '#60a5fa' : '';
    }
  },

  formatJson() {
    if (!this.currentCodeText) return;
    try {
      const parsed = JSON.parse(this.currentCodeText);
      const pretty = JSON.stringify(parsed, null, 2);
      this.currentCodeText = pretty;
      this.codeLines = pretty.split('\n');
      this.renderCodeLines();
      if (typeof Toast !== 'undefined' && Toast.success) Toast.success('Đã định dạng JSON thành công!');
    } catch (err) {
      if (typeof Toast !== 'undefined' && Toast.error) Toast.error(`Cú pháp JSON không hợp lệ: ${err.message}`);
    }
  },

  copyCodeContent() {
    if (!this.currentCodeText) return;
    const btn = document.getElementById('btn-copy-code-content');
    navigator.clipboard.writeText(this.currentCodeText).then(() => {
      if (typeof Toast !== 'undefined' && Toast.success) {
        Toast.success('Đã sao chép toàn bộ nội dung tệp!');
      }
      if (btn) {
        const originalHtml = btn.innerHTML;
        btn.innerHTML = '<span>✓</span><span>Đã chép</span>';
        btn.style.color = '#10b981';
        setTimeout(() => {
          btn.innerHTML = originalHtml;
          btn.style.color = '';
        }, 2000);
      }
    }).catch(() => {
      if (typeof Toast !== 'undefined' && Toast.error) Toast.error('Không thể sao chép văn bản');
    });
  },

  // ─────────────────────────────────────────────────────────────
  // Universal Preview Router
  // Điều hướng chính xác theo định dạng tệp tin
  // ─────────────────────────────────────────────────────────────
  openPreview(fileId, fileName, mimeType, otpCode = '') {
    // 0. Pre-flight Check: Kiểm tra xem tệp có bị thiếu chunks không để tránh treo spinner
    const cachedFile = (typeof FilesManager !== 'undefined' && FilesManager.files)
      ? FilesManager.files.find(f => f.id === fileId)
      : null;
    if (cachedFile && (cachedFile.has_missing_chunks === 1 || cachedFile.has_missing_chunks === true)) {
      this.showMissingChunksError(fileId, fileName, {
        missing_chunks: cachedFile.chunk_count,
        total_chunks: cachedFile.chunk_count,
        size_bytes: cachedFile.size_bytes
      });
      return;
    }

    let streamURL = `/api/files/stream?id=${encodeURIComponent(fileId)}`;
    let downloadURL = `/api/files/download?id=${encodeURIComponent(fileId)}`;

    // Gắn token xác thực nếu có
    const user = (typeof API !== 'undefined' && API.getCurrentUser) ? API.getCurrentUser() : null;
    const authToken = (typeof API !== 'undefined' && API.getToken)
      ? API.getToken()
      : (localStorage.getItem('cloudpool_jwt_token') || localStorage.getItem('cloudpool_token') || sessionStorage.getItem('cloudpool_token') || (user && user.token ? user.token : ''));

    if (authToken) {
      streamURL += `&token=${encodeURIComponent(authToken)}`;
      downloadURL += `&token=${encodeURIComponent(authToken)}`;
    }

    if (otpCode) {
      streamURL += `&otp=${encodeURIComponent(otpCode)}`;
      downloadURL += `&otp=${encodeURIComponent(otpCode)}`;
    }

    // 1. Check if Text / Code file (50+ formats)
    if (this.isTextOrCode(fileName, mimeType)) {
      this.openTextPreview(fileId, fileName, streamURL, downloadURL);
      return;
    }

    const lowerName = (fileName || '').toLowerCase();

    // 2. Check if Image -> Open Lightbox PRO Album Viewer
    const isImage = (typeof ImageArchiveViewer !== 'undefined' && ImageArchiveViewer.isImageFile)
      ? ImageArchiveViewer.isImageFile(fileName, mimeType)
      : (mimeType?.startsWith('image/') || /\.(jpe?g|png|gif|webp|svg|bmp|ico|avif|tiff|tif|heic|heif)$/i.test(lowerName));

    if (isImage && typeof ImageArchiveViewer !== 'undefined' && typeof ImageArchiveViewer.openImageViewer === 'function') {
      ImageArchiveViewer.openImageViewer(fileId, fileName, mimeType, streamURL, downloadURL);
      return;
    }

    const modal = document.getElementById('modal-preview');
    const titleEl = document.getElementById('preview-file-title');
    const area = document.getElementById('preview-content-area');

    if (!modal || !area) return;

    if (titleEl) titleEl.textContent = fileName;
    area.innerHTML = '';

    // 3. Check if Archive -> Open Zero-Unpack Archive Inspector
    const isArchive = (typeof ImageArchiveViewer !== 'undefined' && ImageArchiveViewer.isArchiveFile)
      ? ImageArchiveViewer.isArchiveFile(fileName, mimeType)
      : (/\.(zip|rar|7z|tar|gz|tgz|bz2|xz|jar|apk)$/i.test(lowerName) || (mimeType && (mimeType.includes('zip') || mimeType.includes('compressed') || mimeType.includes('tar') || mimeType.includes('archive'))));

    if (isArchive && typeof ImageArchiveViewer !== 'undefined' && typeof ImageArchiveViewer.renderArchiveInspector === 'function') {
      ImageArchiveViewer.renderArchiveInspector(area, streamURL, fileName, downloadURL, fileId);
      modal.classList.add('active');
      return;
    }

    const isPDF = (mimeType === 'application/pdf' || lowerName.endsWith('.pdf'));
    const isPPT = (/\.(pptx|ppt|ppsx|key|odp)$/i.test(lowerName));
    const isExcel = (/\.(xlsx|xls|csv|tsv|ods|xlsb|numbers)$/i.test(lowerName));
    const isWord = (/\.(docx|doc|odt|rtf)$/i.test(lowerName));
    const viewer = (typeof OfficeViewer !== 'undefined' ? OfficeViewer : (typeof window !== 'undefined' ? window.OfficeViewer : null));

    // 2. PDF Document (Interactive Embedded Viewer)
    if (isPDF) {
      if (viewer && typeof viewer.renderPDF === 'function') {
        viewer.renderPDF(area, streamURL, fileName, downloadURL);
      } else {
        area.innerHTML = `
          <div style="width: 100%; display: flex; flex-direction: column; gap: 8px;">
            <iframe
              src="${streamURL}"
              style="width: 100%; height: 75vh; border: none; border-radius: var(--radius-md, 8px); background: #525659;"
              title="${fileName}"
              loading="lazy"
            ></iframe>
            <div style="display: flex; justify-content: flex-end; gap: 8px;">
              <a class="btn btn-primary btn-sm" href="${downloadURL}" download="${fileName}">
                ⬇️ Tải PDF về máy
              </a>
            </div>
          </div>
        `;
      }
    }
    // 3. PowerPoint Presentations
    else if (isPPT) {
      if (viewer && typeof viewer.renderPPTX === 'function') {
        viewer.renderPPTX(area, streamURL, fileName, downloadURL);
      } else {
        area.innerHTML = `
          <div style="padding: 30px; text-align: center;">
            <div style="font-size: 40px; margin-bottom: 12px;">📽️</div>
            <div style="font-size: 15px; font-weight: 700; color: #f59e0b;">${fileName}</div>
            <div style="font-size: 12px; color: #94a3b8; margin: 10px 0 20px 0;">Bản trình chiếu PowerPoint</div>
            <a href="${downloadURL}" class="btn btn-primary" download="${fileName}">⬇️ Tải File PPTX Về Máy</a>
          </div>
        `;
      }
    }
    // 4. Excel & Spreadsheets (XLSX, XLS, CSV, TSV)
    else if (isExcel) {
      if (viewer && typeof viewer.renderExcel === 'function') {
        viewer.renderExcel(area, streamURL, fileName, downloadURL);
      } else {
        area.innerHTML = `
          <div style="padding: 30px; text-align: center;">
            <div style="font-size: 40px; margin-bottom: 12px;">📊</div>
            <div style="font-size: 15px; font-weight: 700; color: #10b981;">${fileName}</div>
            <div style="font-size: 12px; color: #94a3b8; margin: 10px 0 20px 0;">Bảng tính Excel</div>
            <a href="${downloadURL}" class="btn btn-primary" download="${fileName}">⬇️ Tải File Excel Về Máy</a>
          </div>
        `;
      }
    }
    // 5. Word Documents (DOCX, DOC)
    else if (isWord) {
      if (viewer && typeof viewer.renderDocx === 'function') {
        viewer.renderDocx(area, streamURL, fileName, downloadURL);
      } else {
        area.innerHTML = `
          <div style="padding: 30px; text-align: center;">
            <div style="font-size: 40px; margin-bottom: 12px;">📄</div>
            <div style="font-size: 15px; font-weight: 700; color: #38bdf8;">${fileName}</div>
            <div style="font-size: 12px; color: #94a3b8; margin: 10px 0 20px 0;">Tài liệu Word</div>
            <a href="${downloadURL}" class="btn btn-primary" download="${fileName}">⬇️ Tải File Word Về Máy</a>
          </div>
        `;
      }
    }
    // 6. Video Player PRO (Native Stream + Direct Source + Smart Resume + Anti-Flicker)
    else if ((mimeType && mimeType.startsWith('video/')) || /\.(mp4|webm|mkv|avi|mov|wmv|flv|m4v|ts|3gp|vob|ogv)$/i.test(lowerName)) {
      this._currentFileId = fileId;
      this._currentFileName = fileName;
      this._currentStreamURL = streamURL;
      this._currentDownloadURL = downloadURL;
      this._lastVideoTime = 0;
      this._isRetrying = false;

      area.innerHTML = `
        <div style="width: 100%; display: flex; flex-direction: column; gap: 8px;">
          <div id="video-loading-status" style="display: flex; align-items: center; justify-content: center; gap: 8px; padding: 9px 14px; background: rgba(59, 130, 246, 0.1); border: 1px solid rgba(59, 130, 246, 0.25); border-radius: var(--radius-md, 8px); font-size: 12px; color: var(--accent-blue, #3b82f6);">
            <span class="spinner-small" style="display: inline-block; width: 14px; height: 14px; border: 2px solid rgba(59,130,246,0.3); border-top-color: #3b82f6; border-radius: 50%; animation: spin 0.8s linear infinite;"></span>
            <span id="video-loading-text">⚡ Đang đệm luồng video trực tuyến từ Google Drive...</span>
          </div>
          <div id="video-unmute-hint" style="display: none; padding: 8px 14px; background: rgba(59, 130, 246, 0.15); border: 1px solid rgba(59, 130, 246, 0.3); border-radius: var(--radius-md, 8px); font-size: 12px; color: #60a5fa; text-align: center; cursor: pointer;">
            🔊 Trình duyệt đang tắt tiếng tự động. Chạm vào đây để bật âm thanh.
          </div>
          <video id="pro-video-player" src="${streamURL}" controls playsinline preload="metadata" style="width: 100%; max-height: 60vh; border-radius: var(--radius-md, 8px); background: #000; box-shadow: 0 4px 24px rgba(0,0,0,0.6);">
            Trình duyệt của bạn không hỗ trợ phát trực tiếp định dạng video này.
          </video>
          <div id="video-error-status" style="display: none; padding: 14px; background: rgba(239, 68, 68, 0.1); border: 1px solid rgba(239, 68, 68, 0.3); border-radius: var(--radius-md, 8px); text-align: center;">
            <div id="video-error-msg" style="color: #ef4444; font-size: 13px; font-weight: 500; margin-bottom: 10px; line-height: 1.5;">⚠️ Không thể phát trực tiếp trên trình duyệt này.</div>
            <div style="display: flex; gap: 8px; justify-content: center; flex-wrap: wrap;">
              <button id="btn-retry-video" class="btn btn-secondary btn-sm" onclick="PreviewManager.retryVideoPlayback()">🔄 Thử phát lại</button>
              <button class="btn btn-secondary btn-sm" onclick="PreviewManager.copyStreamLink('${streamURL}')">📋 Sao chép link (VLC / PotPlayer)</button>
              <a class="btn btn-primary btn-sm" href="${downloadURL}" download="${fileName}">⬇️ Tải tệp video về máy</a>
            </div>
          </div>
          <div style="display: flex; justify-content: space-between; align-items: center; padding: 4px 8px; font-size: 12px; color: var(--text-secondary, #94a3b8); flex-wrap: wrap; gap: 8px;">
            <div style="display: flex; gap: 6px; align-items: center;">
              <span>Tốc độ:</span>
              <span class="video-speed-badge" onclick="PreviewManager.setVideoSpeed(0.5)">0.5x</span>
              <span class="video-speed-badge" onclick="PreviewManager.setVideoSpeed(1.0)" style="color: var(--accent-blue, #3b82f6);">1x</span>
              <span class="video-speed-badge" onclick="PreviewManager.setVideoSpeed(1.25)">1.25x</span>
              <span class="video-speed-badge" onclick="PreviewManager.setVideoSpeed(1.5)">1.5x</span>
              <span class="video-speed-badge" onclick="PreviewManager.setVideoSpeed(2.0)">2x</span>
            </div>
            <div style="display: flex; gap: 8px; flex-wrap: wrap;">
              <button class="btn btn-secondary btn-sm" onclick="PreviewManager.copyStreamLink('${streamURL}')" title="Sao chép liên kết luồng để phát bằng phần mềm ngoài như VLC">
                📋 Sao chép link phát
              </button>
              <a class="btn btn-primary btn-sm" href="${downloadURL}" download="${fileName}">
                ⬇️ Tải về máy
              </a>
              <button class="btn btn-secondary btn-sm" onclick="FilesManager.showChunkMap('${fileId}', '${fileName.replace(/'/g, "\\'")}')">
                🔍 Xem bản đồ Chunks
              </button>
            </div>
          </div>
        </div>
      `;

      const vidPlayer = document.getElementById('pro-video-player');
      const loadStatus = document.getElementById('video-loading-status');
      const loadText = document.getElementById('video-loading-text');
      const errStatus = document.getElementById('video-error-status');
      const errMsg = document.getElementById('video-error-msg');
      const unmuteHint = document.getElementById('video-unmute-hint');

      if (this._videoWatchdog) {
        clearTimeout(this._videoWatchdog);
        this._videoWatchdog = null;
      }

      if (unmuteHint && vidPlayer) {
        unmuteHint.addEventListener('click', () => {
          vidPlayer.muted = false;
          unmuteHint.style.display = 'none';
        });
      }

      if (vidPlayer) {
        const onPlaybackSuccess = () => {
          if (PreviewManager._videoWatchdog) {
            clearTimeout(PreviewManager._videoWatchdog);
            PreviewManager._videoWatchdog = null;
          }
          if (loadStatus) loadStatus.style.display = 'none';
          if (errStatus) errStatus.style.display = 'none';
          PreviewManager._isRetrying = false;
        };

        vidPlayer.addEventListener('playing', onPlaybackSuccess);
        vidPlayer.addEventListener('canplay', onPlaybackSuccess);
        vidPlayer.addEventListener('loadeddata', () => {
          if (loadStatus) loadStatus.style.display = 'none';
        });

        // Theo dõi tiến trình để ghi nhớ mốc thời gian xem dở (phục vụ tự động nối luồng)
        vidPlayer.addEventListener('timeupdate', () => {
          if (vidPlayer.currentTime > 0) {
            PreviewManager._lastVideoTime = vidPlayer.currentTime;
          }
        });

        // Đệm dữ liệu mượt mà, không giật màn hình khi mạng chậm
        vidPlayer.addEventListener('waiting', () => {
          if (loadStatus && !PreviewManager._isRetrying) {
            loadStatus.style.display = 'flex';
            if (loadText) loadText.textContent = '⚡ Đang đệm thêm dữ liệu từ Google Drive...';
          }
        });

        const handleVideoFailure = async () => {
          // Nếu đang trong tiến trình reconnect chủ động, không kích hoạt lỗi để tránh chớp màn hình
          if (PreviewManager._isRetrying) return;

          if (PreviewManager._videoWatchdog) {
            clearTimeout(PreviewManager._videoWatchdog);
            PreviewManager._videoWatchdog = null;
          }
          if (loadStatus) loadStatus.style.display = 'none';

          // Gọi API trạng thái để kiểm tra nếu tệp thực sự bị xóa mất chunk trên Google Drive
          try {
            const st = await fetch(`/api/files/status?id=${encodeURIComponent(fileId)}`).then(r => r.json());
            if (st && (st.has_missing_chunks || st.status === 'missing_chunks')) {
              PreviewManager.showMissingChunksError(fileId, fileName, st);
              if (cachedFile) cachedFile.has_missing_chunks = true;
              return;
            }
          } catch (_) {}

          if (errStatus) {
            errStatus.style.display = 'block';
            const err = vidPlayer.error;
            let detail = 'Không thể phát trực tiếp định dạng video này trên trình duyệt hiện tại.';
            const resumeTime = PreviewManager._lastVideoTime || 0;
            const m = Math.floor(resumeTime / 60);
            const s = Math.floor(resumeTime % 60);
            const timeStr = `${m}:${s < 10 ? '0' : ''}${s}`;

            if (err) {
              if (err.code === 4) {
                // Nếu video đã từng phát được một đoạn (> 0s), đây là đứt kết nối luồng chứ không phải do sai codec!
                if (resumeTime > 0) {
                  detail = `Luồng dữ liệu đám mây tạm thời bị ngắt quãng tại phút ${timeStr}. Bạn có thể bấm Thử phát lại để tiếp tục xem từ đoạn này:`;
                } else {
                  detail = 'Video sử dụng định dạng nén camera (H.265 / HEVC 10-bit HDR) mà trình duyệt chưa hỗ trợ bộ giải mã phần cứng. Bạn có thể sao chép link phát dán vào VLC / PotPlayer hoặc tải về xem offline:';
                }
              } else if (err.code === 2) {
                detail = resumeTime > 0
                  ? `Gián đoạn kết nối mạng khi tải luồng dữ liệu đám mây tại phút ${timeStr}. Bạn có thể nhấn Thử phát lại:`
                  : 'Gián đoạn kết nối mạng khi tải luồng dữ liệu đám mây. Bạn có thể nhấn Thử lại:';
              }
            } else if (resumeTime > 0) {
              detail = `Luồng video tạm thời bị nghẽn tại phút ${timeStr}. Bạn có thể nhấn Thử phát lại:`;
            }

            if (errMsg) errMsg.innerHTML = '⚠️ ' + detail;
          }
        };

        vidPlayer.addEventListener('error', handleVideoFailure);

        // Khởi động phát video thông minh với xử lý chính sách Autoplay trên thiết bị di động
        vidPlayer.play().catch(playErr => {
          if (playErr && playErr.name === 'NotAllowedError') {
            // Trình duyệt di động chặn tự động phát có tiếng -> fallback sang tắt tiếng để hiển thị hình ảnh
            vidPlayer.muted = true;
            vidPlayer.play().catch(() => {});
            if (unmuteHint) unmuteHint.style.display = 'block';
          }
        });

        // Watchdog Timeout (15 giây): Chỉ kích hoạt nếu trình duyệt hoàn toàn không nhận được dữ liệu (readyState = 0)
        // Tuyệt đối không bắt lỗi khi video chỉ đang bị paused bởi người dùng
        this._videoWatchdog = setTimeout(() => {
          if (vidPlayer && vidPlayer.readyState === 0 && !vidPlayer.error) {
            handleVideoFailure();
          }
        }, 15000);
      }
    }
    // 7. Audio Player PRO (Native Stream + Direct Source)
    else if ((mimeType && mimeType.startsWith('audio/')) || /\.(mp3|wav|ogg|flac|aac|m4a|wma|opus|midi|mid|aiff)$/i.test(lowerName)) {
      area.innerHTML = `
        <div style="padding: 36px; text-align: center; width: 100%;">
          <div style="font-size: 56px; margin-bottom: 16px;">🎵</div>
          <p style="font-size: 15px; font-weight: 700; margin-bottom: 16px; color: var(--text-primary, #f1f5f9);">${fileName}</p>
          <div id="audio-loading-status" style="display: flex; align-items: center; justify-content: center; gap: 8px; margin-bottom: 12px; font-size: 12px; color: var(--accent-blue, #3b82f6);">
            <span class="spinner-small" style="display: inline-block; width: 12px; height: 12px; border: 2px solid rgba(59,130,246,0.3); border-top-color: #3b82f6; border-radius: 50%; animation: spin 0.8s linear infinite;"></span>
            <span>⚡ Đang đệm luồng âm thanh từ Google Drive...</span>
          </div>
          <audio id="pro-audio-player" src="${streamURL}" controls autoplay style="width: 100%; max-width: 480px;">
            Trình duyệt không hỗ trợ phát nhạc.
          </audio>
          <div id="audio-error-status" style="display: none; margin-top: 14px; padding: 12px; background: rgba(239, 68, 68, 0.1); border: 1px solid rgba(239, 68, 68, 0.25); border-radius: var(--radius-md, 8px); color: #ef4444; font-size: 13px;">
            ⚠️ Không thể phát trực tiếp tệp âm thanh này.
          </div>
          <div style="margin-top: 20px; display: flex; gap: 8px; justify-content: center; flex-wrap: wrap;">
            <button class="btn btn-secondary btn-sm" onclick="PreviewManager.copyStreamLink('${streamURL}')">📋 Sao chép link</button>
            <a class="btn btn-primary btn-sm" href="${downloadURL}" download="${fileName}">⬇️ Tải nhạc về máy</a>
            <button class="btn btn-secondary btn-sm" onclick="FilesManager.showChunkMap('${fileId}', '${fileName.replace(/'/g, "\\'")}')">🔍 Bản đồ Chunks</button>
          </div>
        </div>
      `;

      const audPlayer = document.getElementById('pro-audio-player');
      const audLoad = document.getElementById('audio-loading-status');
      const audErr = document.getElementById('audio-error-status');
      if (audPlayer) {
        audPlayer.addEventListener('playing', () => {
          if (audLoad) audLoad.style.display = 'none';
          if (audErr) audErr.style.display = 'none';
        });
        audPlayer.addEventListener('canplay', () => {
          if (audLoad) audLoad.style.display = 'none';
        });
        audPlayer.addEventListener('error', async () => {
          if (audLoad) audLoad.style.display = 'none';
          try {
            const st = await fetch(`/api/files/status?id=${encodeURIComponent(fileId)}`).then(r => r.json());
            if (st && (st.has_missing_chunks || st.status === 'missing_chunks')) {
              PreviewManager.showMissingChunksError(fileId, fileName, st);
              if (cachedFile) cachedFile.has_missing_chunks = true;
              return;
            }
          } catch (_) {}
          if (audErr) audErr.style.display = 'block';
        });
      }
    }
    // 8. Image Viewer (Fallback)
    else if (isImage || (mimeType && mimeType.startsWith('image/'))) {
      if (typeof ImageArchiveViewer !== 'undefined' && typeof ImageArchiveViewer.openImageViewer === 'function') {
        modal.classList.remove('active');
        ImageArchiveViewer.openImageViewer(fileId, fileName, mimeType, streamURL, downloadURL);
        return;
      }
      area.innerHTML = `
        <div style="display: flex; flex-direction: column; align-items: center; gap: 10px; width: 100%;">
          <img src="${streamURL}" alt="${fileName}" style="max-width: 100%; max-height: 68vh; object-fit: contain; border-radius: var(--radius-md, 8px); box-shadow: 0 4px 20px rgba(0,0,0,0.5);">
          <div style="display: flex; gap: 8px; margin-top: 4px;">
            <a class="btn btn-primary btn-sm" href="${downloadURL}" download="${fileName}">⬇️ Tải ảnh về máy</a>
            <button class="btn btn-secondary btn-sm" onclick="FilesManager.showChunkMap('${fileId}', '${fileName.replace(/'/g, "\\'")}')">🔍 Bản đồ Chunks</button>
          </div>
        </div>
      `;
    }
    // 8.5 Archive Inspector (Fallback)
    else if (isArchive) {
      if (typeof ImageArchiveViewer !== 'undefined' && typeof ImageArchiveViewer.renderArchiveInspector === 'function') {
        ImageArchiveViewer.renderArchiveInspector(area, streamURL, fileName, downloadURL, fileId);
      }
    }
    // 9. Generic / Binary Files
    else {
      area.innerHTML = `
        <div style="padding: 40px 20px; text-align: center; max-width: 520px; margin: 0 auto;">
          <div style="font-size: 54px; margin-bottom: 16px;">📦</div>
          <p style="font-size: 16px; font-weight: 700; margin-bottom: 8px; color: var(--text-primary, #f1f5f9); word-break: break-word;">${fileName}</p>
          <p style="font-size: 12.5px; color: var(--text-muted, #94a3b8); margin-bottom: 22px; line-height: 1.5;">
            Tệp dữ liệu nhị phân đã được mã hóa an toàn trên các tài khoản Google Drive.<br>
            Bạn có thể tải tệp tin về thiết bị để mở bằng ứng dụng tương thích.
          </p>
          <div style="display: flex; gap: 10px; justify-content: center; flex-wrap: wrap;">
            <a class="btn btn-primary btn-sm" href="${downloadURL}" download="${fileName}" style="padding: 8px 20px; font-size: 13px; text-decoration: none;">
              ⬇️ Tải Tệp Về Máy
            </a>
            <button class="btn btn-secondary btn-sm" onclick="FilesManager.showChunkMap('${fileId}', '${fileName.replace(/'/g, "\\'")}')" style="padding: 8px 16px; font-size: 13px;">
              🔍 Xem Bản Đồ Chunks
            </button>
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
        badge.style.color = badge.textContent.includes(speed + 'x') ? 'var(--accent-blue, #3b82f6)' : 'var(--text-secondary, #94a3b8)';
      });
    }
  },

  retryVideoPlayback() {
    const video = document.getElementById('pro-video-player');
    const errStatus = document.getElementById('video-error-status');
    const loadStatus = document.getElementById('video-loading-status');
    const loadText = document.getElementById('video-loading-text');
    const retryBtn = document.getElementById('btn-retry-video');

    if (!video || this._isRetrying) return;
    this._isRetrying = true;

    // Vô hiệu hóa nút tạm thời và đổi chữ để ngăn bấm dồn dập gây giật màn hình
    if (retryBtn) {
      retryBtn.disabled = true;
      retryBtn.innerHTML = '⏳ Đang kết nối lại...';
    }

    if (errStatus) errStatus.style.display = 'none';

    const resumeTime = this._lastVideoTime || 0;
    const m = Math.floor(resumeTime / 60);
    const s = Math.floor(resumeTime % 60);
    const timeStr = `${m}:${s < 10 ? '0' : ''}${s}`;

    if (loadStatus) {
      loadStatus.style.display = 'flex';
      const msg = resumeTime > 0
        ? `⚡ Đang kết nối lại luồng video tại phút ${timeStr}...`
        : '⚡ Đang kết nối lại luồng video đám mây...';
      if (loadText) {
        loadText.textContent = msg;
      } else {
        loadStatus.innerHTML = `<span class="spinner-small" style="display: inline-block; width: 14px; height: 14px; border: 2px solid rgba(59,130,246,0.3); border-top-color: #3b82f6; border-radius: 50%; animation: spin 0.8s linear infinite;"></span><span>${msg}</span>`;
      }
    }

    // Thêm tham số _retry để bypass cache bị lỗi
    try {
      const curUrl = new URL(this._currentStreamURL || video.src, window.location.origin);
      curUrl.searchParams.set('_retry', Date.now().toString());

      const onResumeReady = () => {
        video.removeEventListener('loadedmetadata', onResumeReady);
        video.removeEventListener('canplay', onResumeReady);

        if (resumeTime > 0) {
          try {
            video.currentTime = resumeTime;
          } catch (_) {}
        }

        video.play().catch(e => {
          if (e && e.name === 'NotAllowedError') {
            video.muted = true;
            video.play().catch(() => {});
            const hint = document.getElementById('video-unmute-hint');
            if (hint) hint.style.display = 'block';
          }
        }).finally(() => {
          setTimeout(() => {
            this._isRetrying = false;
            if (retryBtn) {
              retryBtn.disabled = false;
              retryBtn.innerHTML = '🔄 Thử phát lại';
            }
          }, 1000);
        });
      };

      video.addEventListener('loadedmetadata', onResumeReady, { once: true });
      video.addEventListener('canplay', onResumeReady, { once: true });

      video.src = curUrl.toString();
      video.load();

      // Fallback bảo vệ: Tự động mở khóa nút sau 5 giây nếu không nhận được metadata
      setTimeout(() => {
        if (this._isRetrying) {
          this._isRetrying = false;
          if (retryBtn) {
            retryBtn.disabled = false;
            retryBtn.innerHTML = '🔄 Thử phát lại';
          }
        }
      }, 5000);
    } catch (e) {
      this._isRetrying = false;
      if (retryBtn) {
        retryBtn.disabled = false;
        retryBtn.innerHTML = '🔄 Thử phát lại';
      }
      video.load();
      video.play().catch(() => {});
    }
  },

  copyStreamLink(url) {
    const fullURL = window.location.origin + url;
    navigator.clipboard.writeText(fullURL).then(() => {
      if (typeof Toast !== 'undefined' && Toast.success) {
        Toast.success('📋 Đã sao chép liên kết phát luồng! Bạn có thể dán vào VLC hoặc PotPlayer.');
      } else {
        alert('Đã chép link luồng video vào bộ nhớ tạm.');
      }
    }).catch(() => {
      prompt('Sao chép link luồng dưới đây:', fullURL);
    });
  },

  closePreview() {
    if (this._videoWatchdog) {
      clearTimeout(this._videoWatchdog);
      this._videoWatchdog = null;
    }
    const vid = document.getElementById('pro-video-player');
    if (vid) {
      vid.pause();
      vid.removeAttribute('src');
      vid.load();
    }
    const aud = document.getElementById('pro-audio-player');
    if (aud) {
      aud.pause();
      aud.removeAttribute('src');
      aud.load();
    }
    const modal = document.getElementById('modal-preview');
    if (modal) modal.classList.remove('active');
  },

  showMissingChunksError(fileId, fileName, details = {}) {
    if (this._videoWatchdog) {
      clearTimeout(this._videoWatchdog);
      this._videoWatchdog = null;
    }
    const modal = document.getElementById('modal-preview');
    const titleEl = document.getElementById('preview-file-title');
    const area = document.getElementById('preview-content-area');
    if (!modal || !area) return;

    if (titleEl) titleEl.textContent = fileName;

    const missingCount = details.missing_chunks ?? 37;
    const totalCount = details.total_chunks ?? details.chunk_count ?? 37;
    const sizeStr = details.size_bytes ? Utils.formatBytes(details.size_bytes) : '';

    area.innerHTML = `
      <div style="padding: 36px 20px; text-align: center; max-width: 540px; margin: 0 auto;">
        <div style="font-size: 58px; margin-bottom: 14px; animation: bounce 1.2s infinite alternate;">⚠️</div>
        <p style="font-size: 17px; font-weight: 700; margin-bottom: 8px; color: #f59e0b; word-break: break-word;">Tệp Bị Thiếu Dữ Liệu Nguồn Trên Google Drive</p>
        <div style="background: rgba(245, 158, 11, 0.08); border: 1px solid rgba(245, 158, 11, 0.25); border-radius: var(--radius-md, 8px); padding: 14px; margin-bottom: 18px; text-align: left; font-size: 13px; color: #cbd5e1; line-height: 1.6;">
          <div style="display: flex; justify-content: space-between; margin-bottom: 6px; border-bottom: 1px dashed rgba(255,255,255,0.1); padding-bottom: 6px;">
            <span>Tên tệp:</span>
            <b style="color: #f8fafc;">${fileName}</b>
          </div>
          ${sizeStr ? `
          <div style="display: flex; justify-content: space-between; margin-bottom: 6px; border-bottom: 1px dashed rgba(255,255,255,0.1); padding-bottom: 6px;">
            <span>Dung lượng gốc:</span>
            <span>${sizeStr}</span>
          </div>` : ''}
          <div style="display: flex; justify-content: space-between; margin-bottom: 6px;">
            <span>Tình trạng phân mảnh:</span>
            <span style="color: #ef4444; font-weight: 700;">Thiếu ${missingCount} / ${totalCount} chunks (HTTP 410)</span>
          </div>
          <div style="font-size: 12px; color: #94a3b8; margin-top: 8px; line-height: 1.5;">
            Các mảnh tệp đã bị xóa vĩnh viễn trên các tài khoản Google Drive liên kết. Hệ thống đã tự động ngăn chặn phát trực tiếp để bảo vệ tài nguyên và loại bỏ tình trạng xoay vòng vô tận.
          </div>
        </div>
        <div style="display: flex; gap: 10px; justify-content: center; flex-wrap: wrap;">
          <button class="btn btn-secondary btn-sm" onclick="FilesManager.showChunkMap('${fileId}', '${fileName.replace(/'/g, "\\'")}')" style="padding: 8px 16px; font-size: 13px;">
            🔍 Xem Chi Tiết Bản Đồ Chunks
          </button>
          <button class="btn btn-primary btn-sm" onclick="FilesManager.checkFileIntegrity('${fileId}')" style="padding: 8px 16px; font-size: 13px;">
            🛡️ Quét Lại Tính Toàn Vẹn
          </button>
          <button class="btn btn-secondary btn-sm" onclick="PreviewManager.closePreview()" style="padding: 8px 16px; font-size: 13px;">
            ✕ Đóng Cửa Sổ
          </button>
        </div>
      </div>
    `;
    modal.classList.add('active');
  }
};
