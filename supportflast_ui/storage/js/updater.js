// ==========================================================================
// CloudPool Software Update & Granular File Hot-Patching Controller
// ==========================================================================

const UpdaterManager = {
  selectedFile: null,
  fileHash: null,
  filesList: [],

  init() {
    this.bindEvents();
  },

  bindEvents() {
    // Tab activation
    const navItem = document.querySelector('.nav-item[data-tab="updater"]');
    if (navItem) {
      navItem.addEventListener('click', () => {
        this.loadUpdateInfo();
      });
    }

    // Module dropdown change
    const moduleSelect = document.getElementById('update-target-module');
    if (moduleSelect) {
      moduleSelect.addEventListener('change', () => this.onModuleChange());
    }

    // Dropzone events
    const dropzone = document.getElementById('update-dropzone');
    const fileInput = document.getElementById('update-file-input');

    if (dropzone && fileInput) {
      dropzone.addEventListener('click', () => fileInput.click());

      dropzone.addEventListener('dragover', (e) => {
        e.preventDefault();
        dropzone.classList.add('dragover');
      });

      dropzone.addEventListener('dragleave', () => {
        dropzone.classList.remove('dragover');
      });

      dropzone.addEventListener('drop', (e) => {
        e.preventDefault();
        dropzone.classList.remove('dragover');
        if (e.dataTransfer.files && e.dataTransfer.files.length > 0) {
          this.handleFileSelected(e.dataTransfer.files[0]);
        }
      });

      fileInput.addEventListener('change', (e) => {
        if (e.target.files && e.target.files.length > 0) {
          this.handleFileSelected(e.target.files[0]);
        }
      });
    }

    // Update Form Submit
    const form = document.getElementById('form-software-update');
    if (form) {
      form.addEventListener('submit', (e) => {
        e.preventDefault();
        this.submitUpdate();
      });
    }
  },

  onModuleChange() {
    const moduleSelect = document.getElementById('update-target-module');
    const targetFileGroup = document.getElementById('group-target-filename');
    const targetFileSelect = document.getElementById('update-target-filename');
    if (!moduleSelect || !targetFileGroup || !targetFileSelect) return;

    const mod = moduleSelect.value;
    if (mod === 'ui_js') {
      targetFileGroup.style.display = 'block';
      targetFileSelect.innerHTML = `
        <option value="app.js">app.js (Điều phối chính & Khởi tạo)</option>
        <option value="files.js">files.js (Quản lý Tệp tin & VFS Explorer)</option>
        <option value="auth.js">auth.js (Xác thực & Người dùng)</option>
        <option value="responsive.js">responsive.js (Thích ứng đa thiết bị & Màn hình)</option>
        <option value="settings.js">settings.js (Cài đặt Hệ thống & Bảo mật)</option>
        <option value="sql_studio.js">sql_studio.js (SQL Studio SQLite)</option>
        <option value="accounts.js">accounts.js (Quản lý Cụm Drive)</option>
        <option value="updater.js">updater.js (Trung tâm Nâng cấp Phần mềm)</option>
        <option value="remote.js">remote.js (Truy cập Từ xa)</option>
        <option value="preview.js">preview.js (Xem trước Tệp tin)</option>
        <option value="api.js">api.js (Giao tiếp Backend API)</option>
      `;
    } else if (mod === 'ui_html') {
      targetFileGroup.style.display = 'block';
      targetFileSelect.innerHTML = `<option value="index.html">index.html (Giao diện Trang chủ)</option>`;
    } else if (mod === 'ui_css') {
      targetFileGroup.style.display = 'block';
      targetFileSelect.innerHTML = `<option value="style.css">css/style.css (Bảng mã Style giao diện)</option>`;
    } else if (mod === 'engine_binary') {
      targetFileGroup.style.display = 'block';
      targetFileSelect.innerHTML = `<option value="cloudpool.exe">cloudpool.exe (File thực thi Go Engine)</option>`;
    } else if (mod === 'core_dll') {
      targetFileGroup.style.display = 'block';
      targetFileSelect.innerHTML = `<option value="cloudpool_core.dll">cloudpool_core.dll (Lõi Rust FFI DLL)</option>`;
    } else if (mod === 'patch_bundle') {
      targetFileGroup.style.display = 'none';
    }
  },

  async handleFileSelected(file) {
    this.selectedFile = file;
    const infoContainer = document.getElementById('update-selected-file-info');
    const nameEl = document.getElementById('update-file-name');
    const sizeEl = document.getElementById('update-file-size');
    const hashEl = document.getElementById('update-file-sha256');

    if (!infoContainer || !nameEl || !sizeEl || !hashEl) return;

    nameEl.textContent = file.name;
    sizeEl.textContent = this.formatBytes(file.size);
    hashEl.textContent = 'Đang tính toán mã băm SHA-256...';
    infoContainer.style.display = 'block';

    // Auto-detect module based on file extension
    const ext = file.name.split('.').pop().toLowerCase();
    const moduleSelect = document.getElementById('update-target-module');
    const targetFileSelect = document.getElementById('update-target-filename');

    if (moduleSelect) {
      if (ext === 'zip') {
        moduleSelect.value = 'patch_bundle';
      } else if (ext === 'html') {
        moduleSelect.value = 'ui_html';
      } else if (ext === 'css') {
        moduleSelect.value = 'ui_css';
      } else if (ext === 'js') {
        moduleSelect.value = 'ui_js';
        this.onModuleChange();
        if (targetFileSelect) {
          const matchingOpt = Array.from(targetFileSelect.options).find(opt => opt.value === file.name);
          if (matchingOpt) {
            targetFileSelect.value = file.name;
          }
        }
      } else if (ext === 'exe') {
        moduleSelect.value = 'engine_binary';
      } else if (ext === 'dll') {
        moduleSelect.value = 'core_dll';
      }
      this.onModuleChange();
    }

    // Compute SHA-256 using Web Crypto API
    try {
      const buffer = await file.arrayBuffer();
      const hashBuffer = await crypto.subtle.digest('SHA-256', buffer);
      const hashArray = Array.from(new Uint8Array(hashBuffer));
      const hashHex = hashArray.map(b => b.toString(16).padStart(2, '0')).join('');
      this.fileHash = hashHex;
      hashEl.textContent = hashHex;
    } catch (err) {
      hashEl.textContent = 'Không tính được SHA-256 trên trình duyệt';
    }
  },

  async loadUpdateInfo() {
    try {
      const data = await API.getUpdateInfo();
      
      // Fill System Version Card
      const appVerEl = document.getElementById('updater-app-ver');
      const engineVerEl = document.getElementById('updater-engine-ver');
      const coreStatusEl = document.getElementById('updater-core-status');
      const uiDirEl = document.getElementById('updater-ui-dir');

      if (appVerEl) appVerEl.textContent = data.app_version || '2.7.5-PRO';
      if (engineVerEl) engineVerEl.textContent = data.engine_version || 'Go 1.22';
      if (coreStatusEl) {
        coreStatusEl.innerHTML = data.rust_core_active 
          ? '<span style="color: var(--success); font-weight: 600;">● Đang Hoạt Động (Hardware AES-NI)</span>'
          : '<span style="color: var(--warning);">Native Go Crypto</span>';
      }
      if (uiDirEl) uiDirEl.textContent = data.ui_dir || 'cloudpool_ui';

      // Render File Catalog Table
      this.filesList = data.files || [];
      this.renderFilesTable();
    } catch (err) {
      console.warn('Lỗi lấy thông tin update:', err);
    }
  },

  renderFilesTable() {
    const tbody = document.getElementById('updater-files-table-body');
    if (!tbody) return;

    if (!this.filesList || this.filesList.length === 0) {
      tbody.innerHTML = `<tr><td colspan="6" style="text-align: center; padding: 20px; color: var(--text-muted);">Chưa có thông tin tệp tin hệ thống.</td></tr>`;
      return;
    }

    tbody.innerHTML = this.filesList.map(f => {
      const moduleBadge = this.getModuleBadge(f.module);
      const backupBadge = f.has_backup 
        ? `<button class="btn btn-secondary btn-xs" onclick="UpdaterManager.quickRollback('${f.module}', '${f.name}')" title="Khôi phục lại phiên bản trước khi nâng cấp">
             <span>↩️ Khôi phục .bak</span>
           </button>` 
        : `<span style="color: var(--text-muted); font-size: 11px;">Không có</span>`;

      return `
        <tr>
          <td style="padding: 8px 12px; font-weight: 600; color: var(--text-primary);">
            <div style="display: flex; align-items: center; gap: 6px;">
              <span>${this.getFileIcon(f.name)}</span>
              <span>${f.name}</span>
            </div>
          </td>
          <td style="padding: 8px 12px;">${moduleBadge}</td>
          <td style="padding: 8px 12px; font-family: monospace; font-size: 11px;">${f.exists ? this.formatBytes(f.size_bytes) : 'Chưa tạo'}</td>
          <td style="padding: 8px 12px; font-size: 11px; color: var(--text-muted);">${f.updated_at || '—'}</td>
          <td style="padding: 8px 12px;">${backupBadge}</td>
          <td style="padding: 8px 12px; text-align: right;">
            <button class="btn btn-secondary btn-xs" onclick="UpdaterManager.quickSelectFile('${f.module}', '${f.name}')">
              <span>Nâng cấp tệp này</span>
            </button>
          </td>
        </tr>
      `;
    }).join('');
  },

  getModuleBadge(mod) {
    switch (mod) {
      case 'ui_html': return '<span class="badge" style="background: rgba(59, 130, 246, 0.15); color: #60a5fa;">HTML</span>';
      case 'ui_css': return '<span class="badge" style="background: rgba(236, 72, 153, 0.15); color: #f472b6;">CSS Style</span>';
      case 'ui_js': return '<span class="badge" style="background: rgba(234, 179, 8, 0.15); color: #facc15;">JavaScript</span>';
      case 'engine_binary': return '<span class="badge" style="background: rgba(34, 197, 94, 0.15); color: #4ade80;">Go Engine</span>';
      case 'core_dll': return '<span class="badge" style="background: rgba(168, 85, 247, 0.15); color: #c084fc;">Rust Core DLL</span>';
      default: return '<span class="badge">Module</span>';
    }
  },

  getFileIcon(filename) {
    if (filename.endsWith('.html')) return '📄';
    if (filename.endsWith('.css')) return '🎨';
    if (filename.endsWith('.js')) return '📜';
    if (filename.endsWith('.exe')) return '⚙️';
    if (filename.endsWith('.dll')) return '🛡️';
    if (filename.endsWith('.zip')) return '📦';
    return '📁';
  },

  quickSelectFile(module, filename) {
    const moduleSelect = document.getElementById('update-target-module');
    const targetFileSelect = document.getElementById('update-target-filename');
    if (moduleSelect) {
      moduleSelect.value = module;
      this.onModuleChange();
    }
    if (targetFileSelect) {
      targetFileSelect.value = filename;
    }
    const fileInput = document.getElementById('update-file-input');
    if (fileInput) fileInput.click();
  },

  async quickRollback(module, filename) {
    if (!confirm(`Bạn có chắc chắn muốn hoàn tác tệp '${filename}' về bản sao lưu (.bak) gần nhất không?`)) {
      return;
    }
    try {
      const res = await API.rollbackUpdate(module, filename);
      Toast.success(res.message || 'Đã hoàn tác thành công!');
      this.appendLog(`[HOÀN TÁC THÀNH CÔNG] Đã khôi phục ${filename} từ .bak`);
      this.loadUpdateInfo();
    } catch (err) {
      Toast.error('Lỗi hoàn tác: ' + err.message);
      this.appendLog(`[LỖI HOÀN TÁC] ${err.message}`);
    }
  },

  async submitUpdate() {
    if (!this.selectedFile) {
      Toast.error('Vui lòng chọn hoặc kéo thả tệp tin nâng cấp!');
      return;
    }

    const moduleSelect = document.getElementById('update-target-module');
    const targetFileSelect = document.getElementById('update-target-filename');
    const pinInput = document.getElementById('update-security-pin');
    const btnSubmit = document.getElementById('btn-submit-update');

    const mod = moduleSelect ? moduleSelect.value : 'ui_js';
    const targetFilename = (mod !== 'patch_bundle' && targetFileSelect) ? targetFileSelect.value : this.selectedFile.name;
    const pin = pinInput ? pinInput.value.trim() : '';

    const formData = new FormData();
    formData.append('target_module', mod);
    formData.append('target_filename', targetFilename);
    formData.append('file', this.selectedFile);
    if (pin) formData.append('security_pin', pin);

    if (btnSubmit) {
      btnSubmit.disabled = true;
      btnSubmit.innerHTML = `<span>⏳ Đang áp dụng bản vá...</span>`;
    }

    this.appendLog(`[BẮT ĐẦU NÂNG CẤP] Tệp: ${this.selectedFile.name} -> Mục: ${mod} (${targetFilename})`);

    try {
      const res = await API.uploadUpdate(formData);
      Toast.success(res.message || 'Nâng cấp tệp tin thành công!');
      this.appendLog(`[THÀNH CÔNG] ${res.message}`);
      if (res.sha256) this.appendLog(` -> SHA-256 Checksum: ${res.sha256}`);
      if (res.updated_files && res.updated_files.length > 0) {
        this.appendLog(` -> Danh sách tệp đã cập nhật: ${res.updated_files.join(', ')}`);
      }

      // Reset form selection
      this.selectedFile = null;
      document.getElementById('update-selected-file-info').style.display = 'none';
      if (document.getElementById('update-file-input')) {
        document.getElementById('update-file-input').value = '';
      }

      this.loadUpdateInfo();

      if (res.need_restart_engine) {
        this.appendLog(`⚠️ Lõi Engine / DLL đã được cập nhật. Bạn nên bấm nút 'Khởi động lại Server' để nạp phiên bản mới.`);
      } else if (res.need_reload_ui) {
        this.appendLog(`✨ Giao diện Web đã được cập nhật. Bạn có thể bấm F5 hoặc nút 'Tải lại Giao diện' để áp dụng ngay.`);
      }
    } catch (err) {
      Toast.error('Lỗi nâng cấp: ' + err.message);
      this.appendLog(`[LỖI] ${err.message}`);
    } finally {
      if (btnSubmit) {
        btnSubmit.disabled = false;
        btnSubmit.innerHTML = `<span>🚀 Tiến Hành Nâng Cấp Tức Thời</span>`;
      }
    }
  },

  appendLog(msg) {
    const logBox = document.getElementById('updater-live-log');
    if (!logBox) return;

    const time = new Date().toLocaleTimeString();
    const line = document.createElement('div');
    line.style.marginBottom = '4px';
    line.textContent = `[${time}] ${msg}`;
    logBox.appendChild(line);
    logBox.scrollTop = logBox.scrollHeight;
  },

  reloadUI() {
    window.location.reload();
  },

  async restartServer() {
    if (!confirm('Bạn có chắc chắn muốn khởi động lại CloudPool Engine ngay bây giờ không?')) {
      return;
    }
    try {
      const res = await API.restartEngine();
      Toast.success(res.message || 'Đang khởi động lại...');
      this.appendLog(`[KHỞI ĐỘNG LẠI] Đang gửi lệnh khởi động lại hệ thống...`);
      setTimeout(() => {
        window.location.reload();
      }, 2500);
    } catch (err) {
      Toast.error('Lỗi khởi động lại: ' + err.message);
    }
  },

  formatBytes(bytes) {
    if (!bytes || bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
  },
};
