// ==========================================================================
// CloudPool File Explorer Controller (PRO Edition)
// ==========================================================================

const FilesManager = {
  currentFolderId: 'root',
  breadcrumbs: [{ id: 'root', name: 'Gốc' }],
  files: [],
  selectedIds: new Set(),
  viewMode: localStorage.getItem('cloudpool_view_mode') || 'list',
  currentCategory: 'all',
  currentSort: 'date_desc',
  contextTarget: null,
  filterAccountId: '',
  filterUserId: '',
  pendingOTPRequestId: null,   // Request đang chờ admin phê duyệt thời gian
  otpPollingInterval: null,    // ID vòng lặp polling badge thông báo
  lastPendingCount: 0,         // Đếm lần trước để so sánh

  init() {
    this.bindEvents();
    this.bindContextMenu();
    this.updateViewModeUI();
    this.initDurationCardInteraction();
  },

  bindEvents() {
    // User Partition Filter Selection (Admin Only)
    const filterUserSelect = document.getElementById('filter-user-select');
    if (filterUserSelect) {
      filterUserSelect.addEventListener('change', (e) => {
        this.filterUserId = e.target.value;
        this.loadFiles(this.currentFolderId);
      });
    }

    // Account Filter Selection
    const filterSelect = document.getElementById('filter-account-select');
    if (filterSelect) {
      filterSelect.addEventListener('change', (e) => {
        this.filterAccountId = e.target.value;
        this.loadFiles(this.currentFolderId);
      });
    }

    // Category Filter Chips
    document.querySelectorAll('.filter-chip').forEach(chip => {
      chip.addEventListener('click', () => {
        document.querySelectorAll('.filter-chip').forEach(c => c.classList.remove('active'));
        chip.classList.add('active');
        this.currentCategory = chip.getAttribute('data-cat') || 'all';
        const query = document.getElementById('global-search')?.value.trim() || '';
        this.renderFiles(query);
      });
    });

    // Sort files dropdown
    const sortSelect = document.getElementById('sort-files-select');
    if (sortSelect) {
      sortSelect.addEventListener('change', (e) => {
        this.currentSort = e.target.value;
        const query = document.getElementById('global-search')?.value.trim() || '';
        this.renderFiles(query);
      });
    }

    // Quick Upload top button & explorer upload button
    const btnQuick = document.getElementById('btn-quick-upload');
    const btnExp = document.getElementById('btn-upload-file-explorer');
    const fileInput = document.getElementById('file-input-hidden');
    const btnUploadFolder = document.getElementById('btn-upload-folder-explorer');
    const folderInput = document.getElementById('folder-input-hidden');

    // Trigger chọn file an toàn cho cả Desktop lẫn Cảm ứng Di động (Mobile Safari/Chrome)
    const triggerFileInput = (inputEl) => {
      if (!inputEl) return;
      const user = API.getCurrentUser();
      if (!user) {
        Toast.warning('🔒 Vui lòng đăng nhập tài khoản trước khi tải lên tệp tin.');
        if (typeof AuthManager !== 'undefined' && AuthManager.openLoginModal) {
          AuthManager.openLoginModal();
        }
        return;
      }
      try {
        inputEl.click();
      } catch (e) {
        console.warn('Lỗi gọi input.click():', e);
      }
    };

    if (btnQuick) {
      btnQuick.addEventListener('click', (e) => { e.preventDefault(); triggerFileInput(fileInput); });
    }
    if (btnExp) {
      btnExp.addEventListener('click', (e) => { e.preventDefault(); triggerFileInput(fileInput); });
    }
    if (btnUploadFolder && folderInput) {
      btnUploadFolder.addEventListener('click', (e) => { e.preventDefault(); triggerFileInput(folderInput); });
    }

    if (fileInput) {
      fileInput.addEventListener('change', (e) => {
        if (e.target.files && e.target.files.length > 0) {
          this.handleFilesUpload(Array.from(e.target.files));
          fileInput.value = '';
        }
      });
    }

    if (folderInput) {
      folderInput.addEventListener('change', (e) => {
        if (e.target.files && e.target.files.length > 0) {
          this.handleFolderUpload(Array.from(e.target.files));
          folderInput.value = '';
        }
      });
    }

    // View Mode Toggle
    const btnList = document.getElementById('btn-view-list');
    const btnGrid = document.getElementById('btn-view-grid');
    if (btnList) {
      btnList.addEventListener('click', () => {
        this.viewMode = 'list';
        localStorage.setItem('cloudpool_view_mode', 'list');
        this.updateViewModeUI();
        this.renderFiles();
      });
    }
    if (btnGrid) {
      btnGrid.addEventListener('click', () => {
        this.viewMode = 'grid';
        localStorage.setItem('cloudpool_view_mode', 'grid');
        this.updateViewModeUI();
        this.renderFiles();
      });
    }

    // Drag and Drop Zone (Supports files and recursive folder drop, touch & click)
    const dropZone = document.getElementById('file-drop-zone');
    if (dropZone) {
      dropZone.addEventListener('click', (e) => {
        e.preventDefault();
        triggerFileInput(fileInput);
      });
      dropZone.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          triggerFileInput(fileInput);
        }
      });
      ['dragenter', 'dragover'].forEach(eventName => {
        dropZone.addEventListener(eventName, (e) => {
          e.preventDefault();
          e.stopPropagation();
          dropZone.classList.add('drag-over');
        }, false);
      });

      ['dragleave', 'drop'].forEach(eventName => {
        dropZone.addEventListener(eventName, (e) => {
          e.preventDefault();
          e.stopPropagation();
          dropZone.classList.remove('drag-over');
        }, false);
      });

      dropZone.addEventListener('drop', async (e) => {
        const items = e.dataTransfer?.items;
        if (items && items.length > 0) {
          const filesToUpload = [];
          for (let i = 0; i < items.length; i++) {
            const item = items[i];
            if (item.webkitGetAsEntry) {
              const entry = item.webkitGetAsEntry();
              if (entry) {
                const scanned = await FilesManager.scanEntry(entry, '');
                filesToUpload.push(...scanned);
              }
            }
          }
          if (filesToUpload.length > 0) {
            this.handleFolderUpload(filesToUpload);
            return;
          }
        }

        const dt = e.dataTransfer;
        if (dt && dt.files && dt.files.length > 0) {
          this.handleFilesUpload(Array.from(dt.files));
        }
      });
    }

    // Check all files checkbox
    const checkAll = document.getElementById('check-all-files');
    if (checkAll) {
      checkAll.addEventListener('change', (e) => {
        if (e.target.checked) {
          this.files.forEach(f => this.selectedIds.add(f.id));
        } else {
          this.selectedIds.clear();
        }
        this.updateBulkActionBar();
        this.renderFiles();
      });
    }

    // Bulk Action Buttons
    const btnBulkZip = document.getElementById('btn-bulk-zip');
    const btnBulkDel = document.getElementById('btn-bulk-delete');
    const btnBulkCancel = document.getElementById('btn-bulk-cancel');

    if (btnBulkZip) {
      btnBulkZip.addEventListener('click', () => this.handleBulkZipDownload());
    }
    if (btnBulkDel) {
      btnBulkDel.addEventListener('click', () => this.handleBulkDelete());
    }
    if (btnBulkCancel) {
      btnBulkCancel.addEventListener('click', () => {
        this.selectedIds.clear();
        this.updateBulkActionBar();
        this.renderFiles();
      });
    }

    // New Folder Modal & Actions
    const btnNewFolder = document.getElementById('btn-new-folder');
    const btnConfirmFolder = document.getElementById('btn-confirm-new-folder');
    if (btnNewFolder) {
      btnNewFolder.addEventListener('click', () => {
        document.getElementById('new-folder-name').value = '';
        document.getElementById('modal-new-folder').classList.add('active');
      });
    }

    if (btnConfirmFolder) {
      btnConfirmFolder.addEventListener('click', () => this.confirmCreateFolder());
    }

    // Rename Modal Action
    const btnConfirmRename = document.getElementById('btn-confirm-rename');
    if (btnConfirmRename) {
      btnConfirmRename.addEventListener('click', () => this.confirmRename());
    }

    // Search Filter
    const searchInput = document.getElementById('global-search');
    if (searchInput) {
      searchInput.addEventListener('input', (e) => {
        const query = e.target.value.toLowerCase().trim();
        this.renderFiles(query);
      });
    }

    // Close Upload Drawer
    const btnCloseDrawer = document.getElementById('btn-close-upload-drawer');
    if (btnCloseDrawer) {
      btnCloseDrawer.addEventListener('click', () => {
        document.getElementById('upload-drawer').classList.remove('active');
      });
    }

    // OTP Modal Enter key listener
    const otpInput = document.getElementById('otp-input-code');
    if (otpInput) {
      otpInput.addEventListener('keydown', (e) => {
        if (e.key === 'Enter') {
          e.preventDefault();
          this.confirmVerifyOTP();
        }
      });
    }
  },

  async scanEntry(entry, currentPath = '') {
    if (entry.isFile) {
      return new Promise((resolve) => {
        entry.file((file) => {
          file.relPath = currentPath;
          resolve([file]);
        }, () => resolve([]));
      });
    } else if (entry.isDirectory) {
      const dirReader = entry.createReader();
      const entries = await new Promise((resolve) => {
        dirReader.readEntries((entries) => resolve(entries), () => resolve([]));
      });
      const results = [];
      const newPath = currentPath ? `${currentPath}/${entry.name}` : entry.name;
      for (const child of entries) {
        const subFiles = await this.scanEntry(child, newPath);
        results.push(...subFiles);
      }
      return results;
    }
    return [];
  },

  bindContextMenu() {
    const menu = document.getElementById('file-context-menu');
    if (!menu) return;

    window.addEventListener('click', () => menu.classList.remove('active'));

    document.getElementById('ctx-open').addEventListener('click', () => {
      if (this.contextTarget) {
        this.handleItemClick(this.contextTarget.id, this.contextTarget.is_dir, this.contextTarget.name, this.contextTarget.mime_type || '');
      }
    });

    const ctxShare = document.getElementById('ctx-share');
    if (ctxShare) {
      ctxShare.addEventListener('click', () => {
        if (this.contextTarget) {
          this.openShareModal(this.contextTarget.id, this.contextTarget.name, this.contextTarget.is_dir);
        }
      });
    }

    document.getElementById('ctx-chunks').addEventListener('click', () => {
      if (this.contextTarget && !this.contextTarget.is_dir) {
        this.showChunkMap(this.contextTarget.id, this.contextTarget.name);
      }
    });

    document.getElementById('ctx-download').addEventListener('click', () => {
      if (this.contextTarget && !this.contextTarget.is_dir) {
        this.downloadFile(this.contextTarget.id);
      }
    });

    document.getElementById('ctx-rename').addEventListener('click', () => {
      if (this.contextTarget) {
        this.openRenameModal(this.contextTarget.id, this.contextTarget.name);
      }
    });

    document.getElementById('ctx-delete').addEventListener('click', () => {
      if (this.contextTarget) {
        this.deleteFile(this.contextTarget.id, this.contextTarget.name);
      }
    });
  },

  openContextMenu(e, file) {
    e.preventDefault();
    e.stopPropagation();
    this.contextTarget = file;

    const menu = document.getElementById('file-context-menu');
    const ctxShare = document.getElementById('ctx-share');
    const ctxChunks = document.getElementById('ctx-chunks');
    const ctxDownload = document.getElementById('ctx-download');

    if (ctxShare) ctxShare.style.display = 'flex';

    if (file.is_dir) {
      ctxChunks.style.display = 'none';
      ctxDownload.style.display = 'none';
    } else {
      ctxChunks.style.display = 'flex';
      ctxDownload.style.display = 'flex';
    }

    menu.style.left = `${Math.min(e.clientX, window.innerWidth - 200)}px`;
    menu.style.top = `${Math.min(e.clientY, window.innerHeight - 220)}px`;
    menu.classList.add('active');
  },

  updateViewModeUI() {
    const btnList = document.getElementById('btn-view-list');
    const btnGrid = document.getElementById('btn-view-grid');
    const tableContainer = document.getElementById('files-table-container');
    const gridContainer = document.getElementById('files-grid-container');

    if (this.viewMode === 'grid') {
      if (btnGrid) btnGrid.classList.add('active');
      if (btnList) btnList.classList.remove('active');
      if (tableContainer) tableContainer.style.display = 'none';
      if (gridContainer) gridContainer.style.display = 'grid';
    } else {
      if (btnList) btnList.classList.add('active');
      if (btnGrid) btnGrid.classList.remove('active');
      if (tableContainer) tableContainer.style.display = 'block';
      if (gridContainer) gridContainer.style.display = 'none';
    }
  },

  populateUserFilter(users) {
    const filterUserSelect = document.getElementById('filter-user-select');
    if (!filterUserSelect) return;

    const currentVal = this.filterUserId;
    let html = '<option value="">👥 Tất cả phân vùng</option>';
    if (Array.isArray(users)) {
      users.forEach(u => {
        const roleIcon = u.role === 'admin' ? '👑' : '👤';
        const roleLabel = u.role === 'admin' ? 'Admin' : 'Thành viên';
        const displayName = u.display_name || u.username;
        html += `<option value="${u.id}" ${currentVal === u.id ? 'selected' : ''}>${roleIcon} ${displayName} (${roleLabel})</option>`;
      });
    }
    filterUserSelect.innerHTML = html;
  },

  populateAccountFilter(accounts) {
    const filterSelect = document.getElementById('filter-account-select');
    if (!filterSelect) return;

    const currentVal = this.filterAccountId;
    let html = '<option value="">☁️ Tất cả Drive</option>';
    if (Array.isArray(accounts)) {
      accounts.forEach(acc => {
        let name = acc.name || acc.email;
        if (typeof AccountsManager !== 'undefined' && AccountsManager.isPrivacyMode) {
          name = AccountsManager.maskName(acc.name, acc.email);
        }
        html += `<option value="${acc.id}" ${currentVal === acc.id ? 'selected' : ''}>☁️ ${name} (${Utils.formatBytes(acc.used_quota_bytes)})</option>`;
      });
    }
    filterSelect.innerHTML = html;
  },

  filterByAccount(accountId) {
    this.filterAccountId = accountId;
    const filterSelect = document.getElementById('filter-account-select');
    if (filterSelect) filterSelect.value = accountId;
    App.switchTab('files');
    this.loadFiles('root');
  },

  async loadFiles(folderId = 'root') {
    this.currentFolderId = folderId;
    try {
      let data;
      if (this.filterAccountId) {
        data = await API.listFilesByAccount(this.filterAccountId);
      } else {
        data = await API.listFiles(folderId, this.filterUserId);
      }
      this.files = (data && data.files) ? data.files : [];
      this.selectedIds.clear();
      this.updateBulkActionBar();
      this.renderBreadcrumbs();
      this.renderFiles();
    } catch (err) {
      Toast.error('Không thể tải tệp tin: ' + err.message);
    }
  },

  navigateUp() {
    this.filterAccountId = '';
    const filterSelect = document.getElementById('filter-account-select');
    if (filterSelect) filterSelect.value = '';

    if (this.breadcrumbs && this.breadcrumbs.length > 1) {
      this.breadcrumbs.pop();
      const target = this.breadcrumbs[this.breadcrumbs.length - 1];
      this.loadFiles(target.id);
    } else {
      this.navigateTo('root', '🏠 Gốc');
    }
  },

  navigateTo(folderId, folderName = 'Gốc') {
    this.filterAccountId = '';
    const filterSelect = document.getElementById('filter-account-select');
    if (filterSelect) filterSelect.value = '';

    if (folderId === 'root' || folderId === '') {
      this.breadcrumbs = [{ id: 'root', name: '🏠 Gốc' }];
      folderId = 'root';
    } else {
      const idx = this.breadcrumbs.findIndex(b => b.id === folderId);
      if (idx >= 0) {
        this.breadcrumbs = this.breadcrumbs.slice(0, idx + 1);
      } else {
        this.breadcrumbs.push({ id: folderId, name: folderName });
      }
    }
    this.loadFiles(folderId);
  },

  renderBreadcrumbs() {
    const container = document.getElementById('breadcrumbs-container');
    const btnBack = document.getElementById('btn-nav-back');
    if (!container) return;

    // Show or highlight Back button if inside subfolder
    if (btnBack) {
      if (this.currentFolderId !== 'root' || this.filterAccountId) {
        btnBack.style.display = 'inline-flex';
      } else {
        btnBack.style.display = 'none';
      }
    }

    let html = '';
    if (this.filterAccountId) {
      html = `
        <span class="crumb-item" onclick="FilesManager.navigateTo('root', '🏠 Gốc')">🏠 Gốc</span>
        <span class="crumb-separator">/</span>
        <span class="crumb-item active" style="color: var(--accent-blue);">📂 Đang lọc theo tài khoản</span>
        <button class="btn btn-secondary btn-sm" style="font-size: 11px; padding: 2px 6px; margin-left: 6px;" onclick="FilesManager.navigateTo('root', '🏠 Gốc')">✕ Xem tất cả</button>
      `;
    } else {
      this.breadcrumbs.forEach((crumb, i) => {
        const isLast = i === this.breadcrumbs.length - 1;
        const displayName = crumb.name || (crumb.id === 'root' ? '🏠 Gốc' : 'Thư mục');
        html += `<span class="crumb-item ${isLast ? 'active' : ''}" data-crumb-id="${crumb.id}">${displayName}</span>`;
        if (!isLast) {
          html += `<span class="crumb-separator">/</span>`;
        }
      });
    }

    container.innerHTML = html;

    // Attach click listeners cleanly to avoid quote escaping issues
    container.querySelectorAll('.crumb-item[data-crumb-id]').forEach(el => {
      el.addEventListener('click', (e) => {
        const cid = el.getAttribute('data-crumb-id');
        this.navigateTo(cid, el.textContent.trim());
      });
    });
  },

  renderFiles(filterQuery = '') {
    let displayFiles = [...this.files];

    // 1. Filter by category
    if (this.currentCategory && this.currentCategory !== 'all') {
      displayFiles = displayFiles.filter(f => {
        if (f.is_dir) return false;
        const lowerMime = (f.mime_type || '').toLowerCase();
        const lowerName = (f.name || '').toLowerCase();
        switch (this.currentCategory) {
          case 'video':
            return lowerMime.startsWith('video/') || /\.(mp4|webm|mkv|avi|mov|wmv|flv|m4v|ts|3gp|vob|ogv)$/i.test(lowerName);
          case 'image':
            return lowerMime.startsWith('image/') || /\.(jpg|jpeg|png|webp|gif|svg|bmp|ico|tiff|tif|heic|heif|avif|raw|cr2|nef)$/i.test(lowerName);
          case 'audio':
            return lowerMime.startsWith('audio/') || /\.(mp3|flac|wav|aac|m4a|ogg|wma|opus|midi|mid|aiff)$/i.test(lowerName);
          case 'document':
            return lowerMime.includes('pdf') || lowerMime.includes('word') || lowerMime.includes('document') || /\.(pdf|doc|docx|odt|rtf|epub|mobi|txt|log|tex|pages)$/i.test(lowerName);
          case 'spreadsheet':
            return lowerMime.includes('excel') || lowerMime.includes('sheet') || lowerMime.includes('csv') || /\.(xls|xlsx|csv|tsv|ods|numbers|parquet|feather)$/i.test(lowerName);
          case 'presentation':
            return lowerMime.includes('presentation') || lowerMime.includes('powerpoint') || /\.(ppt|pptx|ppsx|key|odp|potx)$/i.test(lowerName);
          case 'archive':
            return lowerMime.includes('zip') || lowerMime.includes('compressed') || lowerMime.includes('tar') || /\.(zip|rar|7z|tar|gz|tgz|bz2|xz|iso|img|dmg|vhd|vhdx|wim)$/i.test(lowerName);
          case 'code':
            return /\.(go|rs|py|js|ts|jsx|tsx|java|c|cpp|h|hpp|cs|php|rb|html|css|scss|sql|sqlite|db|json|yaml|yml|xml|sh|bat|ps1|cmd|md|env)$/i.test(lowerName);
          case 'design':
            return /\.(psd|ai|eps|fig|xd|sketch|blend|obj|fbx|stl|dwg|dxf|step|iges)$/i.test(lowerName);
          case 'app':
            return /\.(exe|msi|apk|aab|ipa|deb|rpm|appimage|bin|pkg)$/i.test(lowerName);
          default:
            return true;
        }
      });
    }

    // 2. Filter by search query
    if (filterQuery) {
      displayFiles = displayFiles.filter(f => f.name.toLowerCase().includes(filterQuery.toLowerCase()));
    }

    // 3. Sort files
    displayFiles.sort((a, b) => {
      const timeA = Utils.getTimestamp(a.updated_at || a.created_at);
      const timeB = Utils.getTimestamp(b.updated_at || b.created_at);

      if (this.currentSort === 'date_desc') {
        // Tệp tin hoặc thư mục mới tải lên gần đây nhất sẽ luôn hiển thị trên đầu
        if (timeB !== timeA) return timeB - timeA;
        if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1;
        return a.name.localeCompare(b.name, 'vi', { sensitivity: 'base', numeric: true });
      }

      // Đối với sắp xếp theo tên hoặc các tiêu chí khác: Folders first
      if (a.is_dir !== b.is_dir) {
        return a.is_dir ? -1 : 1;
      }

      switch (this.currentSort) {
        case 'name_asc':
          return a.name.localeCompare(b.name, 'vi', { sensitivity: 'base', numeric: true });
        case 'name_desc':
          return b.name.localeCompare(a.name, 'vi', { sensitivity: 'base', numeric: true });
        case 'size_desc':
          return (b.size_bytes || 0) - (a.size_bytes || 0);
        case 'size_asc':
          return (a.size_bytes || 0) - (b.size_bytes || 0);
        case 'chunks_desc':
          return (b.chunk_count || 0) - (a.chunk_count || 0);
        case 'chunks_asc':
          return (a.chunk_count || 0) - (b.chunk_count || 0);
        case 'date_asc':
          return timeA - timeB;
        default:
          return timeB - timeA;
      }
    });

    // Cập nhật số lượng đếm trên các filter chips
    this.updateCategoryCounts();

    if (this.viewMode === 'grid') {
      this.renderGridView(displayFiles);
    } else {
      this.renderListView(displayFiles);
    }
  },

  updateCategoryCounts() {
    let videoCnt = 0, imgCnt = 0, docCnt = 0, otherCnt = 0, dirCnt = 0;
    (this.files || []).forEach(f => {
      if (f.is_dir) {
        dirCnt++;
      } else {
        const lowerName = (f.name || '').toLowerCase();
        const lowerMime = (f.mime_type || '').toLowerCase();
        if (lowerMime.startsWith('video/') || /\.(mp4|webm|mkv|avi|mov|wmv|flv|m4v|ts|3gp)$/i.test(lowerName)) {
          videoCnt++;
        } else if (lowerMime.startsWith('image/') || /\.(jpg|jpeg|png|webp|gif|svg|bmp)$/i.test(lowerName)) {
          imgCnt++;
        } else if (lowerMime.includes('pdf') || lowerMime.includes('word') || lowerMime.includes('sheet') || /\.(pdf|doc|docx|xls|xlsx|csv|txt)$/i.test(lowerName)) {
          docCnt++;
        } else {
          otherCnt++;
        }
      }
    });

    const videoChip = document.querySelector('.filter-chip[data-cat="video"]');
    if (videoChip) videoChip.textContent = `🎬 Video (${videoCnt})`;
    const allChip = document.querySelector('.filter-chip[data-cat="all"]');
    if (allChip) allChip.textContent = `📂 Tất cả (${this.files ? this.files.length : 0})`;
    const imgChip = document.querySelector('.filter-chip[data-cat="image"]');
    if (imgChip) imgChip.textContent = `🖼️ Hình ảnh (${imgCnt})`;
    const docChip = document.querySelector('.filter-chip[data-cat="document"]');
    if (docChip) docChip.textContent = `📄 Tài liệu (${docCnt})`;
  },

  toggleSort(field) {
    if (field === 'date') {
      this.currentSort = (this.currentSort === 'date_desc') ? 'date_asc' : 'date_desc';
    } else if (field === 'name') {
      this.currentSort = (this.currentSort === 'name_asc') ? 'name_desc' : 'name_asc';
    } else if (field === 'size') {
      this.currentSort = (this.currentSort === 'size_desc') ? 'size_asc' : 'size_desc';
    } else if (field === 'chunks') {
      this.currentSort = (this.currentSort === 'chunks_desc') ? 'chunks_asc' : 'chunks_desc';
    }
    const sortSelect = document.getElementById('sort-files-select');
    if (sortSelect) sortSelect.value = this.currentSort;
    const query = document.getElementById('global-search')?.value.trim() || '';
    this.renderFiles(query);
  },

  // Hàm mã hóa tên thư mục / phân vùng tài khoản để bảo mật tuyệt đối
  maskFolderName(name) {
    if (!name) return name;
    const isGDrive = name.includes('[Google Drive]') || name.startsWith('[Google Drive]');
    if (!isGDrive) return name.replace(/^📁\s*/, '');

    const clean = name.replace(/^📁\s*/, '').replace(/^\[Google Drive\]\s*/, '').trim();
    if (!clean) return '🔒 Phân vùng [••••••••]';

    let masked = clean;
    if (clean.length <= 3) {
      masked = clean[0] + '•••';
    } else {
      masked = clean[0] + '•'.repeat(Math.min(8, Math.max(4, clean.length - 2))) + clean.slice(-1);
    }
    return `🔒 Phân vùng [${masked}]`;
  },

  renderListView(displayFiles) {
    const tbody = document.getElementById('files-table-body');
    if (!tbody) return;

    if (displayFiles.length === 0) {
      tbody.innerHTML = `
        <tr>
          <td colspan="6" style="text-align: center; padding: 36px; color: var(--text-muted);">
            Thư mục này hiện đang trống. Kéo thả tệp vào đây hoặc bấm "Tải lên" để bắt đầu.
          </td>
        </tr>
      `;
      return;
    }

    const isAdmin = (typeof AuthManager !== 'undefined' && AuthManager.currentUser && AuthManager.currentUser.role === 'admin');

    let html = '';

    // If inside a subfolder, add standard ".." row to return to parent folder
    if (this.currentFolderId !== 'root' && !this.filterAccountId) {
      const parentId = this.breadcrumbs.length > 1 ? this.breadcrumbs[this.breadcrumbs.length - 2].id : 'root';
      html += `
        <tr class="folder-back-row" onclick="FilesManager.navigateTo('${parentId}')" style="cursor: pointer;">
          <td></td>
          <td>
            <div class="file-name-cell" style="color: var(--accent-blue);">
              <span class="file-icon folder">
                <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor"><path d="M20 11H7.83l5.59-5.59L12 4l-8 8 8 8 1.41-1.41L7.83 13H20v-2z"></path></svg>
              </span>
              <span style="font-weight: 600;">.. (Quay lại thư mục cha)</span>
            </div>
          </td>
          <td>-</td>
          <td>-</td>
          <td>-</td>
          <td></td>
        </tr>
      `;
    }

    displayFiles.forEach(f => {
      const isSelected = this.selectedIds.has(f.id);
      const icon = Utils.getFileIconSVG(f.mime_type || '', f.is_dir, f.name);
      const sizeText = f.is_dir ? '-' : Utils.formatBytes(f.size_bytes);
      const chunksText = f.is_dir ? '-' : `${f.chunk_count} chunks`;
      const dateText = Utils.formatDate(f.updated_at || f.created_at);
      const isMember = (typeof AuthManager !== 'undefined' && AuthManager.currentUser && AuthManager.currentUser.role === 'member');
      const isAdmin = !isMember;
      const isLockedAdminFile = (isMember && (f.is_admin_owned || f.requires_otp));

      const isGDriveFolder = f.is_dir && (f.name.includes('[Google Drive]') || f.name.startsWith('[Google Drive]'));
      const displayName = isGDriveFolder ? this.maskFolderName(f.name) : f.name.replace(/^📁\s*/, '');

      let badgeHtml = '';
      if (isLockedAdminFile) {
        badgeHtml = `<span class="badge" style="background: rgba(239, 68, 68, 0.15); color: #f87171; border: 1px solid rgba(239, 68, 68, 0.3); font-size: 10px; margin-left: 6px; padding: 1px 6px; border-radius: 4px;">🔒 Cần OTP Admin</span>`;
      } else if (f.user_id === 'user_admin' && !isGDriveFolder) {
        badgeHtml = `<span class="badge" style="background: rgba(59, 130, 246, 0.12); color: #60a5fa; font-size: 10px; margin-left: 6px; padding: 1px 5px; border-radius: 4px;">👑 Quản trị viên</span>`;
      }

      let nameContent = '';
      if (isGDriveFolder) {
        nameContent = `
          <span style="font-weight: 600; color: #38bdf8; font-family: var(--font-mono); letter-spacing: 0.02em;">${displayName}</span>
        `;
      } else {
        nameContent = `<span style="user-select: none;">${displayName}</span>`;
      }

      html += `
        <tr class="${isSelected ? 'selected' : ''}" oncontextmenu="FilesManager.openContextMenu(event, ${JSON.stringify(f).replace(/"/g, '&quot;')})">
          <td onclick="event.stopPropagation();">
            <input type="checkbox" ${isSelected ? 'checked' : ''} onchange="FilesManager.toggleSelect('${f.id}')" style="cursor: pointer;">
          </td>
          <td onclick="FilesManager.handleItemClick('${f.id}', ${f.is_dir}, '${displayName.replace(/'/g, "\\'")}', '${f.mime_type || ''}', ${f.is_admin_owned ? 'true' : 'false'}, ${f.requires_otp ? 'true' : 'false'})">
            <div class="file-name-cell">
              <span class="file-icon ${f.is_dir ? 'folder' : ''}">${icon}</span>
              ${nameContent}
              ${badgeHtml}
            </div>
          </td>
          <td>${sizeText}</td>
          <td style="color: var(--text-muted); font-size: 12px;">${chunksText}</td>
          <td style="color: var(--text-secondary); font-size: 12px; white-space: nowrap;">${dateText}</td>
          <td>
            <div class="file-actions-cell">
              ${isLockedAdminFile ? `
                <button class="btn btn-secondary btn-sm" style="font-size: 11px; padding: 2px 8px; color: #f87171; border-color: rgba(239,68,68,0.3);" onclick="event.stopPropagation(); FilesManager.openOTPModal(${JSON.stringify(f).replace(/"/g, '&quot;')}, 'preview')" title="Mở khóa bằng mã OTP">
                  🔑 Nhập OTP
                </button>
                <button class="action-icon-btn" onclick="event.stopPropagation(); FilesManager.openOTPModal(${JSON.stringify(f).replace(/"/g, '&quot;')}, 'download')" title="Tải về qua mã OTP">
                  <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"></path><polyline points="7 10 12 15 17 10"></polyline><line x1="12" y1="15" x2="12" y2="3"></line></svg>
                </button>
              ` : `
                ${isAdmin && !f.is_dir ? `
                  <button class="btn btn-secondary btn-sm" style="font-size: 11px; padding: 2px 6px; color: #60a5fa;" onclick="event.stopPropagation(); FilesManager.openAdminGenerateOTPModal(${JSON.stringify(f).replace(/"/g, '&quot;')})" title="Cấp mã OTP 1 lần cho người dùng con">
                    🔑 Cấp OTP
                  </button>
                ` : ''}
                ${!f.is_dir ? `
                  <button class="action-icon-btn" onclick="event.stopPropagation(); FilesManager.handleItemClick('${f.id}', false, '${displayName.replace(/'/g, "\\'")}', '${f.mime_type || ''}', ${f.is_admin_owned ? 'true' : 'false'}, ${f.requires_otp ? 'true' : 'false'})" title="👁️ Xem trước nội dung (Không cần tải về)" style="color: #38bdf8;">
                    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"></path><circle cx="12" cy="12" r="3"></circle></svg>
                  </button>
                ` : ''}
                ${!isGDriveFolder ? `
                  <button class="action-icon-btn" onclick="event.stopPropagation(); FilesManager.openShareModal('${f.id}', '${f.name.replace(/'/g, "\\'")}', ${f.is_dir})" title="${f.is_dir ? 'Tạo link chia sẻ toàn bộ thư mục' : 'Tạo link chia sẻ xem trực tuyến 24/7'}" style="color: var(--accent-cyan);">
                    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="18" cy="5" r="3"></circle><circle cx="6" cy="12" r="3"></circle><circle cx="18" cy="19" r="3"></circle><line x1="8.59" y1="13.51" x2="15.42" y2="17.49"></line><line x1="15.41" y1="6.51" x2="8.59" y2="10.49"></line></svg>
                  </button>
                ` : ''}
                ${!f.is_dir ? `
                  <button class="action-icon-btn" onclick="event.stopPropagation(); FilesManager.downloadFile('${f.id}')" title="Tải về máy">
                    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"></path><polyline points="7 10 12 15 17 10"></polyline><line x1="12" y1="15" x2="12" y2="3"></line></svg>
                  </button>
                ` : ''}
                <button class="action-icon-btn" onclick="event.stopPropagation(); FilesManager.openRenameModal('${f.id}', '${f.name}')" title="Đổi tên">
                  <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"></path><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"></path></svg>
                </button>
                <button class="action-icon-btn" onclick="event.stopPropagation(); FilesManager.deleteFile('${f.id}', '${f.name}')" title="Xóa" style="color: var(--accent-red);">
                  <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"></polyline><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path></svg>
                </button>
              `}
            </div>
          </td>
        </tr>
      `;
    });

    tbody.innerHTML = html;
  },

  renderGridView(displayFiles) {
    const container = document.getElementById('files-grid-container');
    if (!container) return;

    if (displayFiles.length === 0 && this.currentFolderId === 'root') {
      container.innerHTML = `
        <div style="grid-column: 1/-1; text-align: center; padding: 48px; color: var(--text-muted); background: var(--bg-secondary); border: 1px dashed var(--border-subtle); border-radius: var(--radius-md);">
          Thư mục này hiện đang trống. Kéo thả tệp vào đây để tải lên.
        </div>
      `;
      return;
    }

    const isMember = (typeof AuthManager !== 'undefined' && AuthManager.currentUser && AuthManager.currentUser.role === 'member');

    let html = '';

    // If inside a subfolder, add standard ".." card in Grid view
    if (this.currentFolderId !== 'root' && !this.filterAccountId) {
      html += `
        <div class="file-card up-card" onclick="FilesManager.navigateUp()" style="cursor: pointer; border: 1px dashed rgba(59, 130, 246, 0.4); background: rgba(59, 130, 246, 0.05);">
          <div class="file-card-icon folder" style="color: #60a5fa; font-size: 26px;">📁 ⬆️</div>
          <div class="file-card-title" style="color: var(--accent-blue); font-weight: 600;">.. (Quay lại)</div>
          <div class="file-card-meta"><span>Thư mục cha</span></div>
        </div>
      `;
    }

    displayFiles.forEach(f => {
      const isSelected = this.selectedIds.has(f.id);
      const icon = Utils.getFileIconSVG(f.mime_type || '', f.is_dir, f.name);
      const sizeText = f.is_dir ? 'Thư mục' : Utils.formatBytes(f.size_bytes);
      const isLockedAdminFile = (isMember && (f.is_admin_owned || f.requires_otp));
      const isGDriveFolder = f.is_dir && (f.name.includes('[Google Drive]') || f.name.startsWith('[Google Drive]'));
      const displayName = isGDriveFolder ? this.maskFolderName(f.name) : f.name.replace(/^📁\s*/, '');

      html += `
        <div class="file-card ${isSelected ? 'selected' : ''} ${isLockedAdminFile ? 'locked-card' : ''}" 
             onclick="FilesManager.handleItemClick('${f.id}', ${f.is_dir}, '${displayName.replace(/'/g, "\\'")}', '${f.mime_type || ''}', ${f.is_admin_owned ? 'true' : 'false'}, ${f.requires_otp ? 'true' : 'false'})"
             oncontextmenu="FilesManager.openContextMenu(event, ${JSON.stringify(f).replace(/"/g, '&quot;')})">
          <input type="checkbox" class="file-card-checkbox" ${isSelected ? 'checked' : ''} onclick="event.stopPropagation(); FilesManager.toggleSelect('${f.id}')">
          <div class="file-card-icon ${f.is_dir ? 'folder' : ''}">
            ${icon}
          </div>
          <div class="file-card-title" title="${displayName}">
            ${isGDriveFolder ? `<b style="color:#38bdf8; font-family: var(--font-mono);">${displayName}</b>` : displayName}
            ${isLockedAdminFile ? '<span style="color:#f87171; font-size:11px; display:block; margin-top:2px;">🔒 Cần OTP Admin</span>' : ''}
          </div>
          <div class="file-card-meta">
            <span>${sizeText}</span>
          </div>
          ${!f.is_dir ? `<span class="file-card-chunk-badge" onclick="event.stopPropagation(); FilesManager.showChunkMap('${f.id}', '${displayName.replace(/'/g, "\\'")}')">${f.chunk_count} Chunks</span>` : ''}
        </div>
      `;
    });

    container.innerHTML = html;
  },

  toggleSelect(id) {
    if (this.selectedIds.has(id)) {
      this.selectedIds.delete(id);
    } else {
      this.selectedIds.add(id);
    }
    this.updateBulkActionBar();
    this.renderFiles();
  },

  updateBulkActionBar() {
    const bar = document.getElementById('bulk-action-bar');
    const countEl = document.getElementById('bulk-selected-count');
    const checkAll = document.getElementById('check-all-files');

    if (!bar || !countEl) return;

    const count = this.selectedIds.size;
    if (count > 0) {
      countEl.textContent = `Đã chọn ${count} mục`;
      bar.classList.add('active');
    } else {
      bar.classList.remove('active');
    }

    if (checkAll) {
      checkAll.checked = count > 0 && count === this.files.length;
    }
  },

  async handleBulkDelete() {
    const count = this.selectedIds.size;
    if (!confirm(`Bạn có chắc chắn muốn xóa ${count} mục đã chọn?`)) return;

    try {
      const ids = Array.from(this.selectedIds);
      await API.bulkDeleteFiles(ids);
      Toast.success(`Đã xóa ${count} mục`);
      this.selectedIds.clear();
      this.loadFiles(this.currentFolderId);
      App.refreshStats();
      AccountsManager.loadAccounts();
    } catch (err) {
      Toast.error('Lỗi xóa mục: ' + err.message);
    }
  },

  handleBulkZipDownload() {
    const ids = Array.from(this.selectedIds);
    if (ids.length === 0) return;
    window.open(`/api/files/zip?ids=${encodeURIComponent(ids.join(','))}`, '_blank');
  },

  async showChunkMap(fileId, fileName) {
    try {
      const data = await API.getFileChunks(fileId);
      const modal = document.getElementById('modal-chunk-map');
      const titleEl = document.getElementById('chunk-map-title');
      const metaEl = document.getElementById('chunk-map-meta');
      const grid = document.getElementById('chunk-map-grid');

      titleEl.textContent = `Bản đồ Chunks: ${fileName}`;
      metaEl.innerHTML = `Tổng kích thước: <b>${Utils.formatBytes(data.file.size_bytes)}</b> | Số lượng: <b>${data.chunks.length} chunks mã hóa AES-256</b>`;

      let html = '';
      data.chunks.forEach(c => {
        let accLabel = c.account_name || c.account_email;
        if (typeof AccountsManager !== 'undefined' && AccountsManager.isPrivacyMode) {
          accLabel = AccountsManager.maskName(c.account_name, c.account_email);
        }

        html += `
          <div class="chunk-card">
            <div class="chunk-card-header">
              <span>Chunk #${c.chunk_index + 1}</span>
              <span>${Utils.formatBytes(c.chunk_size_bytes)}</span>
            </div>
            <div class="chunk-card-account">
              ☁️ ${accLabel}
            </div>
            <div class="chunk-card-hash" title="SHA-256: ${c.sha256}">
              Hash: ${c.sha256 ? c.sha256.substring(0, 12) + '...' : '-'}
            </div>
          </div>
        `;
      });

      grid.innerHTML = html;
      modal.classList.add('active');
    } catch (err) {
      Toast.error('Không thể tải bản đồ chunk: ' + err.message);
    }
  },

  handleItemClick(id, isDir, name, mimeType, isAdminOwned, requiresOTP) {
    if (isDir) {
      this.navigateTo(id, name);
    } else {
      const file = this.files.find(f => f.id === id) || { id, name, mime_type: mimeType, is_admin_owned: isAdminOwned, requires_otp: requiresOTP };
      const isMember = (typeof AuthManager !== 'undefined' && AuthManager.currentUser && AuthManager.currentUser.role === 'member');
      if (isMember && (file.is_admin_owned || file.requires_otp)) {
        this.openOTPModal(file, 'preview');
        return;
      }
      PreviewManager.openPreview(id, name, mimeType);
    }
  },

  async confirmCreateFolder() {
    const input = document.getElementById('new-folder-name');
    const name = input ? input.value.trim() : '';
    if (!name) {
      Toast.error('Vui lòng nhập tên thư mục');
      return;
    }

    try {
      await API.createFolder(this.currentFolderId, name);
      Toast.success(`Đã tạo thư mục "${name}"`);
      document.getElementById('modal-new-folder').classList.remove('active');
      this.loadFiles(this.currentFolderId);
      App.refreshStats();
    } catch (err) {
      Toast.error(err.message);
    }
  },

  openRenameModal(id, currentName) {
    document.getElementById('rename-target-id').value = id;
    document.getElementById('rename-target-name').value = currentName;
    document.getElementById('modal-rename').classList.add('active');
  },

  async confirmRename() {
    const id = document.getElementById('rename-target-id').value;
    const newName = document.getElementById('rename-target-name').value.trim();
    if (!newName) {
      Toast.error('Vui lòng nhập tên mới');
      return;
    }

    try {
      await API.renameFile(id, newName);
      Toast.success('Đổi tên thành công');
      document.getElementById('modal-rename').classList.remove('active');
      this.loadFiles(this.currentFolderId);
    } catch (err) {
      Toast.error(err.message);
    }
  },

  async deleteFile(id, name) {
    if (!confirm(`Bạn có chắc chắn muốn chuyển "${name}" vào Thùng rác (Recycle Bin)? Bạn có thể khôi phục lại bất kỳ lúc nào.`)) {
      return;
    }

    try {
      await API.deleteFile(id);
      Toast.success('Đã chuyển tệp vào Thùng rác');
      this.loadFiles(this.currentFolderId);
      App.refreshStats();
      AccountsManager.loadAccounts();
    } catch (err) {
      Toast.error(err.message);
    }
  },


  downloadFile(id) {
    const file = this.files.find(f => f.id === id);
    const isMember = (typeof AuthManager !== 'undefined' && AuthManager.currentUser && AuthManager.currentUser.role === 'member');
    if (file && isMember && (file.is_admin_owned || file.requires_otp)) {
      this.openOTPModal(file, 'download');
      return;
    }
    window.open(`/api/files/download?id=${encodeURIComponent(id)}`, '_blank');
  },

  // =========================================================================
  // Single-Use OTP File Access & Admin Approval Handlers
  // =========================================================================

  openOTPModal(file, actionType = 'preview') {
    this.otpTargetFile = file;
    this.otpActionType = actionType;

    const modal = document.getElementById('modal-file-otp');
    if (!modal) return;

    const nameEl = document.getElementById('otp-target-file-name');
    const sizeEl = document.getElementById('otp-target-file-size');
    const inputEl = document.getElementById('otp-input-code');
    const statusEl = document.getElementById('otp-status-message');

    if (nameEl) nameEl.textContent = file.name;
    if (sizeEl) sizeEl.textContent = Utils.formatBytes(file.size_bytes || 0);
    if (inputEl) inputEl.value = '';
    if (statusEl) statusEl.innerHTML = '';

    modal.classList.add('active');
    setTimeout(() => {
      if (inputEl) inputEl.focus();
    }, 150);
  },

  async confirmVerifyOTP() {
    if (!this.otpTargetFile) return;
    const inputEl = document.getElementById('otp-input-code');
    const otpCode = inputEl ? inputEl.value.trim() : '';
    if (!otpCode) {
      Toast.error('Vui lòng nhập mã OTP');
      return;
    }

    const btn = document.getElementById('btn-submit-file-otp');
    const oldText = btn ? btn.textContent : 'Xác thực';
    if (btn) {
      btn.textContent = '🔓 Đang xác thực...';
      btn.disabled = true;
    }

    // Hiện trạng thái đang xử lý
    const statusEl = document.getElementById('otp-status-message');
    if (statusEl) {
      statusEl.innerHTML = `<span style="color: #38bdf8; font-size: 13px;">⏳ Đang kiểm tra mã OTP...</span>`;
    }

    try {
      const res = await API.verifyFileOTP(this.otpTargetFile.id, otpCode);

      // Cập nhật UI thành công trước khi đóng modal
      if (statusEl) {
        statusEl.innerHTML = `<span style="color: #10b981; font-size: 13px;">✅ Xác thực thành công! Đang mở tệp...</span>`;
      }

      // Đợi 400ms để user thấy thông báo thành công, rồi tự động mở file
      await new Promise(r => setTimeout(r, 400));

      // Đóng modal
      document.getElementById('modal-file-otp').classList.remove('active');

      // Tự động mở file (auto_unlock từ backend xác nhận)
      if (this.otpActionType === 'download') {
        Toast.success('🔓 Đã xác thực! Bắt đầu tải xuống...');
        window.location.href = `/api/files/download?id=${encodeURIComponent(this.otpTargetFile.id)}&otp=${encodeURIComponent(otpCode)}`;
      } else {
        Toast.success('🔓 Đã xác thực! Đang mở xem trực tiếp...');
        PreviewManager.openPreviewWithOTP(this.otpTargetFile.id, this.otpTargetFile.name, this.otpTargetFile.mime_type, otpCode);
      }
    } catch (err) {
      Toast.error(err.message || 'Mã OTP không chính xác hoặc đã hết hạn');
      if (statusEl) {
        statusEl.innerHTML = `<span style="color: #ef4444; font-size: 13px;">❌ ${err.message || 'Mã OTP không hợp lệ'}</span>`;
      }
      // Shake input
      if (inputEl) {
        inputEl.style.transition = 'border-color 0.15s';
        inputEl.style.borderColor = '#ef4444';
        setTimeout(() => { inputEl.style.borderColor = ''; }, 1500);
      }
    } finally {
      if (btn) {
        btn.textContent = oldText;
        btn.disabled = false;
      }
    }
  },

  async requestOTPForFile() {
    if (!this.otpTargetFile) return;
    try {
      const res = await API.requestFileOTP(this.otpTargetFile.id);
      Toast.success(res.message || 'Đã gửi yêu cầu cấp mã OTP tới Quản Trị Viên!');
      const statusEl = document.getElementById('otp-status-message');
      if (statusEl) {
        statusEl.innerHTML = `<span style="color: #10b981; font-size: 13px;">✅ Đã gửi yêu cầu tới Quản Trị Viên. Khi Admin phê duyệt, mã OTP sẽ được cấp cho bạn.</span>`;
      }
    } catch (err) {
      Toast.error('Lỗi gửi yêu cầu: ' + err.message);
    }
  },

  openAdminGenerateOTPModal(file) {
    this.adminOTPFile = file;
    const modal = document.getElementById('modal-admin-generate-otp');
    if (!modal) return;

    if (this.otpCountdownInterval) {
      clearInterval(this.otpCountdownInterval);
      this.otpCountdownInterval = null;
    }

    document.getElementById('admin-otp-file-name').textContent = file.name;
    document.getElementById('admin-otp-file-size').textContent = Utils.formatBytes(file.size_bytes || 0);
    document.getElementById('admin-otp-result-box').style.display = 'none';

    // Populate user target dropdown
    const selectEl = document.getElementById('admin-otp-target-user');
    if (selectEl && typeof UsersManager !== 'undefined' && UsersManager.users) {
      let html = '<option value="all">👥 Tất cả thành viên</option>';
      UsersManager.users.filter(u => u.role !== 'admin').forEach(u => {
        html += `<option value="${u.id}">👤 ${u.display_name || u.username} (${u.username})</option>`;
      });
      selectEl.innerHTML = html;
    }

    modal.classList.add('active');
  },

  copyGeneratedOTP() {
    const codeEl = document.getElementById('admin-otp-generated-code');
    if (!codeEl) return;
    const code = codeEl.textContent.trim();
    if (code && code !== '000000') {
      navigator.clipboard.writeText(code);
      Toast.success(`Đã sao chép mã OTP: ${code}`);
    }
  },

  async confirmAdminGenerateOTP() {
    if (!this.adminOTPFile) return;
    const selectEl = document.getElementById('admin-otp-target-user');
    const targetUserId = selectEl ? selectEl.value : 'all';
    const durationEl = document.getElementById('admin-otp-duration');
    const durationMinutes = durationEl ? parseInt(durationEl.value) : 5;

    try {
      const res = await API.adminGenerateOTP(this.adminOTPFile.id, targetUserId, durationMinutes);
      Toast.success(res.message || 'Đã tạo mã OTP 1 lần thành công!');

      const resultBox = document.getElementById('admin-otp-result-box');
      const codeEl = document.getElementById('admin-otp-generated-code');
      const expireEl = document.getElementById('admin-otp-expire-text');
      const timerEl = document.getElementById('admin-otp-countdown-timer');
      const barEl = document.getElementById('admin-otp-countdown-bar');

      if (codeEl) codeEl.textContent = res.otp_code;
      if (expireEl) expireEl.textContent = `Hết hạn lúc: ${res.expires_at} (Khả dụng đúng 1 lần)`;
      if (resultBox) resultBox.style.display = 'block';

      // Start Live Countdown Timer
      if (this.otpCountdownInterval) {
        clearInterval(this.otpCountdownInterval);
      }

      const totalDurationSec = durationMinutes * 60;
      const expireTime = Date.now() + totalDurationSec * 1000;

      const updateCountdown = () => {
        const now = Date.now();
        const diffSec = Math.max(0, Math.floor((expireTime - now) / 1000));

        if (diffSec <= 0) {
          if (timerEl) {
            timerEl.textContent = '🔴 Đã hết hạn (00:00)';
            timerEl.style.color = '#f87171';
          }
          if (barEl) {
            barEl.style.width = '0%';
            barEl.style.background = '#ef4444';
          }
          clearInterval(this.otpCountdownInterval);
          this.otpCountdownInterval = null;
          return;
        }

        const m = Math.floor(diffSec / 60);
        const s = diffSec % 60;
        let formatted = '';
        if (diffSec >= 3600) {
          const h = Math.floor(diffSec / 3600);
          const remM = Math.floor((diffSec % 3600) / 60);
          formatted = `${h}h ${remM}m ${s}s`;
        } else {
          formatted = `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
        }

        if (timerEl) {
          timerEl.textContent = `⏱️ Còn ${formatted}`;
          timerEl.style.color = diffSec < 60 ? '#f87171' : (diffSec < 300 ? '#facc15' : '#38bdf8');
        }

        if (barEl) {
          const percent = Math.min(100, Math.max(0, (diffSec / totalDurationSec) * 100));
          barEl.style.width = `${percent}%`;
          barEl.style.background = percent < 20 ? '#ef4444' : (percent < 50 ? 'linear-gradient(90deg, #f59e0b, #ef4444)' : 'linear-gradient(90deg, #38bdf8, #10b981)');
        }
      };

      updateCountdown();
      this.otpCountdownInterval = setInterval(updateCountdown, 1000);
    } catch (err) {
      Toast.error('Lỗi tạo OTP: ' + err.message);
    }
  },

  async openAdminOTPManagerModal() {
    const modal = document.getElementById('modal-admin-otp-manager');
    if (!modal) return;
    modal.classList.add('active');
    await this.loadAdminOTPsAndRequests();
  },

  async loadAdminOTPsAndRequests() {
    try {
      const [otps, requests] = await Promise.all([
        API.adminListOTPs(),
        API.adminListAccessRequests()
      ]);

      // ── Section 1: Render Requests as Cards ──────────────────
      const reqList = document.getElementById('otp-requests-list');
      const reqCountBadge = document.getElementById('otp-req-count-badge');

      if (reqList) {
        const pendingReqs = (requests || []).filter(r => r.status === 'pending');

        // Update count badge
        if (reqCountBadge) {
          if (requests && requests.length > 0) {
            reqCountBadge.textContent = requests.length;
            reqCountBadge.style.display = 'inline-flex';
            reqCountBadge.style.background = pendingReqs.length > 0
              ? 'rgba(239,68,68,0.15)' : 'rgba(59,130,246,0.15)';
            reqCountBadge.style.color = pendingReqs.length > 0 ? '#f87171' : '#60a5fa';
          } else {
            reqCountBadge.style.display = 'none';
          }
        }

        if (!requests || requests.length === 0) {
          reqList.innerHTML = `
            <div style="text-align: center; padding: 28px; color: var(--text-muted); font-size: 12px; background: rgba(255,255,255,0.02); border-radius: 10px; border: 1px dashed var(--border-subtle);">
              <div style="font-size: 24px; margin-bottom: 6px;">📭</div>
              Không có yêu cầu xin mã OTP nào
            </div>`;
        } else {
          reqList.innerHTML = requests.map(r => {
            const isPending = r.status === 'pending';
            const isApproved = r.status === 'approved';
            const statusColor = isPending ? '#f59e0b' : (isApproved ? '#10b981' : '#6b7280');
            const statusBg = isPending ? 'rgba(245,158,11,0.1)' : (isApproved ? 'rgba(16,185,129,0.1)' : 'rgba(107,114,128,0.1)');
            const statusText = isPending ? '⏳ Chờ duyệt' : (isApproved ? `✅ Đã cấp` : '❌ Từ chối');

            return `
            <div data-req-id="${r.id}" data-username="${r.user_display_name || r.username}" style="display: flex; align-items: center; gap: 12px; padding: 12px 14px; background: rgba(255,255,255,0.03); border: 1px solid var(--border-subtle); border-radius: 10px; transition: background 0.15s;" onmouseover="this.style.background='rgba(255,255,255,0.05)'" onmouseout="this.style.background='rgba(255,255,255,0.03)'">
              <!-- Avatar -->
              <div style="width: 36px; height: 36px; border-radius: 50%; background: linear-gradient(135deg, #3b82f6, #8b5cf6); display: flex; align-items: center; justify-content: center; font-size: 14px; font-weight: 700; color: white; flex-shrink: 0;">
                ${(r.user_display_name || r.username || '?')[0].toUpperCase()}
              </div>
              <!-- Info -->
              <div style="flex: 1; min-width: 0;">
                <div style="font-size: 13px; font-weight: 600; color: var(--text-primary); margin-bottom: 2px;">${r.user_display_name || r.username}</div>
                <div style="font-size: 11px; color: var(--text-muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">📄 ${r.file_name}</div>
              </div>
              <!-- Status -->
              <div style="display: flex; flex-direction: column; align-items: flex-end; gap: 4px; flex-shrink: 0;">
                <span style="background: ${statusBg}; color: ${statusColor}; font-size: 10px; font-weight: 700; padding: 2px 8px; border-radius: 20px; white-space: nowrap;">${statusText}${isApproved && r.otp_code ? ` (${r.otp_code})` : ''}</span>
                <span style="font-size: 10px; color: var(--text-muted);">${Utils.formatDate(r.created_at)}</span>
              </div>
              <!-- Actions -->
              ${isPending ? `
              <div style="display: flex; gap: 6px; flex-shrink: 0;">
                <button class="btn btn-primary" style="font-size: 11px; padding: 4px 10px; height: auto;" onclick="FilesManager.adminApproveRequest('${r.id}')">✓ Duyệt</button>
                <button class="btn btn-secondary" style="font-size: 11px; padding: 4px 8px; height: auto; color: #ef4444;" onclick="FilesManager.adminRejectRequest('${r.id}')">✕</button>
              </div>` : ''}
            </div>`;
          }).join('');
        }
      }

      // Cập nhật legacy tbody (cho JS cũ nếu cần)
      const reqTbody = document.getElementById('admin-otp-requests-tbody');
      if (reqTbody) reqTbody.innerHTML = '';

      // ── Section 2: Render OTP Table ──────────────────────────
      const otpTbody = document.getElementById('admin-otps-list-tbody');
      const otpCountBadge = document.getElementById('otp-list-count-badge');

      if (otpCountBadge) {
        if (otps && otps.length > 0) {
          otpCountBadge.textContent = otps.length;
          otpCountBadge.style.display = 'inline-flex';
        } else {
          otpCountBadge.style.display = 'none';
        }
      }

      if (otpTbody) {
        if (!otps || otps.length === 0) {
          otpTbody.innerHTML = `<tr><td colspan="6" style="text-align:center; padding:28px; color:var(--text-muted); font-size:12px;">
            <div style="font-size: 20px; margin-bottom: 6px;">🔐</div>
            Chưa có mã OTP nào được tạo
          </td></tr>`;
        } else {
          const now = Date.now();

          otpTbody.innerHTML = otps.map(o => {
            let statusBadge = '';
            let expireDate = new Date(o.expires_at).getTime();
            let isExpired = (expireDate < now);

            if (o.status === 'used') {
              statusBadge = `<span style="background:rgba(107,114,128,0.15); color:#9ca3af; font-size:10px; font-weight:600; padding:2px 8px; border-radius:20px;">Đã dùng</span>`;
            } else if (o.status === 'expired' || isExpired) {
              statusBadge = `<span style="background:rgba(239,68,68,0.15); color:#f87171; font-size:10px; font-weight:600; padding:2px 8px; border-radius:20px;">Hết hạn</span>`;
            } else {
              statusBadge = `<span style="background:rgba(16,185,129,0.15); color:#34d399; font-size:10px; font-weight:600; padding:2px 8px; border-radius:20px;">🟢 Khả dụng</span>`;
            }

            let remainText = '';
            if (isExpired || o.status === 'used') {
              remainText = `<span style="color:var(--text-muted); font-size:11px;">${Utils.formatDate(o.expires_at)}</span>`;
            } else {
              const diffSec = Math.floor((expireDate - now) / 1000);
              const m = Math.floor(diffSec / 60);
              const s = diffSec % 60;
              const timeStr = diffSec >= 3600
                ? `${Math.floor(diffSec/3600)}h ${Math.floor((diffSec%3600)/60)}p`
                : `${m}p ${s}s`;
              remainText = `<span style="color:#38bdf8; font-family:monospace; font-size:11px; font-weight:700;">⏱ ${timeStr}</span>`;
            }

            return `
            <tr>
              <td style="padding: 10px 14px;">
                <span style="font-family:monospace; font-weight:800; font-size:14px; color:#60a5fa; background:rgba(59,130,246,0.1); padding:3px 10px; border-radius:6px; letter-spacing:0.1em;">${o.otp_code}</span>
              </td>
              <td style="padding: 10px 14px; max-width: 180px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--text-secondary); font-size: 12px;" title="${o.file_name}">
                <span style="color:var(--text-muted); margin-right:4px;">📄</span>${o.file_name}
              </td>
              <td style="padding: 10px 14px; white-space: nowrap;">
                <div style="display:flex; align-items:center; gap:6px;">
                  <div style="width:22px; height:22px; border-radius:50%; background:linear-gradient(135deg,#8b5cf6,#3b82f6); display:flex; align-items:center; justify-content:center; font-size:10px; font-weight:700; color:white;">
                    ${(o.target_username || (o.target_user_id === 'all' ? 'A' : '?'))[0].toUpperCase()}
                  </div>
                  <span style="font-size:12px; color:var(--text-secondary);">${o.target_username || (o.target_user_id === 'all' ? 'Tất cả' : o.target_user_id)}</span>
                </div>
              </td>
              <td style="padding: 10px 14px;">${statusBadge}</td>
              <td style="padding: 10px 14px;">${remainText}</td>
              <td style="padding: 10px 14px; text-align: right;">
                ${o.status === 'active' && !isExpired ? `
                  <button class="btn btn-secondary btn-sm" style="font-size:11px; padding:3px 10px; color:#ef4444; border-color:rgba(239,68,68,0.3);" onclick="FilesManager.adminRevokeOTP('${o.id}')">Thu hồi</button>
                ` : `<span style="color:var(--text-muted); font-size:11px;">—</span>`}
              </td>
            </tr>`;
          }).join('');
        }
      }
    } catch (err) {
      Toast.error('Lỗi tải dữ liệu OTP: ' + err.message);
    }
  },

  async adminApproveRequest(requestId) {
    try {
      const res = await API.adminApproveRequest(requestId, 1440);
      Toast.success(res.message || 'Đã phê duyệt yêu cầu');
      this.loadAdminOTPsAndRequests();
    } catch (err) {
      Toast.error('Lỗi phê duyệt: ' + err.message);
    }
  },

  async adminRejectRequest(requestId) {
    try {
      await API.adminRejectRequest(requestId);
      Toast.success('Đã từ chối yêu cầu');
      this.loadAdminOTPsAndRequests();
    } catch (err) {
      Toast.error('Lỗi từ chối: ' + err.message);
    }
  },

  async adminRevokeOTP(otpId) {
    if (!confirm('Bạn có chắc muốn thu hồi mã OTP này?')) return;
    try {
      await API.adminRevokeOTP(otpId);
      Toast.success('Đã thu hồi mã OTP');
      this.loadAdminOTPsAndRequests();
    } catch (err) {
      Toast.error('Lỗi thu hồi: ' + err.message);
    }
  },

  async handleFilesUpload(fileList) {
    if (!fileList || fileList.length === 0) return;

    // Kiểm tra đăng nhập
    const user = API.getCurrentUser();
    if (!user) {
      Toast.warning('🔒 Vui lòng đăng nhập tài khoản để được cấp quyền tải lên tệp tin.', 6000);
      if (typeof AuthManager !== 'undefined' && AuthManager.openLoginModal) {
        AuthManager.openLoginModal();
      }
      return;
    }

    const drawer = document.getElementById('upload-drawer');
    const listContainer = document.getElementById('upload-list');
    drawer.classList.add('active');

    for (const file of fileList) {
      // Kiểm tra file rỗng
      if (file.size === 0) {
        Toast.warning(`Tệp "${file.name}" có kích thước 0 byte, bỏ qua.`);
        continue;
      }

      // Cảnh báo file lớn
      if (file.size > 2 * 1024 * 1024 * 1024) {
        Toast.warning(`Tệp "${file.name}" lớn hơn 2GB. Tải lên có thể mất nhiều thời gian.`, 5000);
      }

      const isLargeFile = file.size > 5 * 1024 * 1024;
      const uploadId = 'up_' + Math.random().toString(36).substr(2, 9);
      const itemEl = document.createElement('div');
      itemEl.className = 'upload-item';
      itemEl.id = uploadId;
      itemEl.innerHTML = `
        <div class="upload-item-header">
          <span class="upload-item-name" title="${file.name}">${file.name}</span>
          <span id="${uploadId}-percent" class="upload-item-status">${isLargeFile ? '0% (Mã hóa phân tán)' : '0% (Đang tải lên)'}</span>
        </div>
        <div class="storage-bar-bg">
          <div id="${uploadId}-bar" class="storage-bar-fill" style="width: 0%;"></div>
        </div>
      `;
      listContainer.prepend(itemEl);

      try {
        const result = await API.uploadFile(this.currentFolderId, file, (percent, statusText) => {
          const percentEl = document.getElementById(`${uploadId}-percent`);
          const barEl = document.getElementById(`${uploadId}-bar`);
          let displayStatus = `${percent}% (Đang tải lên)`;
          if (statusText) {
            displayStatus = statusText;
          } else if (isLargeFile) {
            displayStatus = `${percent}% (Phân mảnh & mã hóa AES-256)`;
          }
          if (percentEl) percentEl.textContent = displayStatus;
          if (barEl) barEl.style.width = `${percent}%`;
        });

        const percentEl = document.getElementById(`${uploadId}-percent`);
        const barEl = document.getElementById(`${uploadId}-bar`);
        if (barEl) barEl.style.width = '100%';
        if (result && result.replaced) {
          if (percentEl) percentEl.textContent = 'Đã thay thế ✓';
          Toast.success(`Đã tự động thay thế phiên bản cũ của "${file.name}"`);
        } else {
          if (percentEl) percentEl.textContent = 'Hoàn tất ✓';
          Toast.success(`Đã tải lên và mã hóa đa luồng tệp "${file.name}"`);
        }
      } catch (err) {
        const percentEl = document.getElementById(`${uploadId}-percent`);
        const errMsg = err.message || 'Lỗi không xác định';
        if (percentEl) {
          const shortErr = errMsg.length > 25 ? errMsg.substring(0, 22) + '...' : errMsg;
          percentEl.textContent = `Lỗi: ${shortErr}`;
          percentEl.style.color = 'var(--accent-red)';
          percentEl.title = errMsg;
        }
        if (errMsg.includes('Khách') || errMsg.includes('đăng nhập') || errMsg.includes('403') || errMsg.includes('Forbidden')) {
          Toast.warning(`🔒 ${errMsg || 'Vui lòng đăng nhập tài khoản để được cấp quyền tải lên tệp tin.'}`, 7000);
          if (typeof AuthManager !== 'undefined' && AuthManager.openLoginModal) {
            setTimeout(() => AuthManager.openLoginModal(), 1200);
          }
        } else {
          Toast.error(`Lỗi tải lên ${file.name}: ${errMsg}`);
        }
      }
    }

    // Reset filter về "Tất cả" sau khi upload để user thấy file vừa tải lên
    this.currentCategory = 'all';
    document.querySelectorAll('.filter-chip').forEach(c => c.classList.remove('active'));
    const allChip = document.querySelector('.filter-chip[data-cat="all"]');
    if (allChip) allChip.classList.add('active');

    this.loadFiles(this.currentFolderId);
    App.refreshStats();
    AccountsManager.loadAccounts();
  },

  async importDriveFiles(accountId = '') {
    try {
      Toast.info('⚡ Đang quét và tự động nạp tất cả các file có sẵn từ Google Drive...');
      const res = await API.importDriveFiles(accountId);
      const count = res.imported_count || 0;
      if (count > 0) {
        Toast.success(`🎉 ${res.message || `Đã nạp thành công ${count} tệp tin từ Google Drive!`}`);
      } else {
        Toast.info('Tất cả các tệp tin trên Google Drive đã được đồng bộ đầy đủ vào CloudPool.');
      }
      this.loadFiles(this.currentFolderId);
      App.refreshStats();
      AccountsManager.loadAccounts();
    } catch (err) {
      Toast.error('Lỗi nạp tệp từ Google Drive: ' + err.message);
    }
  },

  // ────────────────────────────────────────────────────────
  // OTP Notification Polling — Admin Badge Thông Báo
  // ────────────────────────────────────────────────────────

  startAdminOTPPolling() {
    if (this.otpPollingInterval) return; // Đã chạy rồi
    const checkPending = async () => {
      try {
        const data = await API.adminGetOTPPendingCount();
        const count = data.pending_count || 0;
        this._updateOTPBadge(count);

        // Có yêu cầu mới so với lần check trước → hiện Toast thông báo
        if (count > this.lastPendingCount && this.lastPendingCount >= 0) {
          const diff = count - this.lastPendingCount;
          Toast.warning(`🔔 Có ${diff} yêu cầu OTP mới đang chờ phê duyệt!`, 5000);
          // Làm nút OTP rung nhẹ để thu hút chú ý
          const btn = document.getElementById('btn-open-otp-mgr');
          if (btn) {
            btn.style.transition = 'box-shadow 0.2s';
            btn.style.boxShadow = '0 0 0 3px rgba(239,68,68,0.4)';
            setTimeout(() => { btn.style.boxShadow = ''; }, 2000);
          }
        }
        this.lastPendingCount = count;
      } catch (_) { /* Không có quyền admin hoặc offline — bỏ qua */ }
    };

    // Chạy ngay lập tức lần đầu
    checkPending();
    // Sau đó polling mỗi 15 giây
    this.otpPollingInterval = setInterval(checkPending, 15000);
  },

  stopAdminOTPPolling() {
    if (this.otpPollingInterval) {
      clearInterval(this.otpPollingInterval);
      this.otpPollingInterval = null;
    }
  },

  _updateOTPBadge(count) {
    const badge = document.getElementById('otp-pending-badge');
    if (!badge) return;
    if (count > 0) {
      badge.textContent = count > 99 ? '99+' : count;
      badge.setAttribute('data-count', count);
      badge.style.display = 'inline-flex';
    } else {
      badge.setAttribute('data-count', '0');
      badge.style.display = 'none';
    }
    // Cập nhật badge trong OTP manager modal nếu đang mở
    const reqCountBadge = document.getElementById('otp-req-count-badge');
    if (reqCountBadge && count > 0) {
      reqCountBadge.textContent = count;
      reqCountBadge.style.display = 'inline-flex';
    }
  },

  // ────────────────────────────────────────────────────────
  // Admin Approve với Modal Chọn Thời Gian
  // ────────────────────────────────────────────────────────

  adminApproveRequest(requestId) {
    // Lưu requestId để dùng khi confirm
    this.pendingOTPRequestId = requestId;

    // Tìm username của người yêu cầu để hiển thị trong modal
    let displayName = 'người dùng';
    // Tìm từ DOM hiện tại
    const reqCards = document.querySelectorAll('#otp-requests-list [data-req-id="' + requestId + '"]');
    if (reqCards.length > 0) {
      displayName = reqCards[0].getAttribute('data-username') || displayName;
    }

    const usernameEl = document.getElementById('approve-modal-username');
    if (usernameEl) {
      usernameEl.textContent = `Cấp mã OTP cho: ${displayName}`;
    }

    // Reset về mặc định 8 giờ
    document.querySelectorAll('.otp-dur-option').forEach(opt => {
      opt.classList.remove('selected');
      const radio = opt.querySelector('input[type=radio]');
      if (radio) radio.checked = false;
    });
    const defaultOpt = document.querySelector('.otp-dur-option[data-minutes="480"]');
    if (defaultOpt) {
      defaultOpt.classList.add('selected');
      const radio = defaultOpt.querySelector('input[type=radio]');
      if (radio) radio.checked = true;
    }

    // Mở modal chọn thời gian
    const modal = document.getElementById('modal-approve-duration');
    if (modal) modal.classList.add('active');
  },

  async confirmApproveWithDuration() {
    if (!this.pendingOTPRequestId) return;

    // Lấy giá trị đã chọn
    const selectedRadio = document.querySelector('input[name="approve_duration"]:checked');
    const durationMinutes = selectedRadio ? parseInt(selectedRadio.value) : 480;

    const btn = document.getElementById('btn-confirm-approve');
    const oldText = btn ? btn.innerHTML : '';
    if (btn) { btn.innerHTML = '⏳ Đang cấp...'; btn.disabled = true; }

    try {
      const res = await API.adminApproveRequest(this.pendingOTPRequestId, durationMinutes);

      // Đóng modal chọn thời gian
      document.getElementById('modal-approve-duration').classList.remove('active');

      // Hiển thị OTP đã cấp đẹp hơn
      const durationText = this._formatDurationText(durationMinutes);
      Toast.success(`✅ Đã cấp OTP: ${res.otp_code || ''} (hiệu lực ${durationText})`);

      // Làm mới danh sách OTP
      this.loadAdminOTPsAndRequests();
      // Cập nhật badge
      this.lastPendingCount = Math.max(0, this.lastPendingCount - 1);
      this._updateOTPBadge(this.lastPendingCount);

      this.pendingOTPRequestId = null;
    } catch (err) {
      Toast.error('Lỗi phê duyệt: ' + err.message);
    } finally {
      if (btn) { btn.innerHTML = oldText; btn.disabled = false; }
    }
  },

  _formatDurationText(minutes) {
    if (minutes < 60) return `${minutes} phút`;
    if (minutes < 1440) return `${Math.round(minutes/60)} giờ`;
    if (minutes < 10080) return `${Math.round(minutes/1440)} ngày`;
    return `${Math.round(minutes/10080)} tuần`;
  },

  // ────────────────────────────────────────────────────────
  // OTP Duration Card — Tương tác click radio
  // ────────────────────────────────────────────────────────
  initDurationCardInteraction() {
    document.addEventListener('click', (e) => {
      const opt = e.target.closest('.otp-dur-option');
      if (!opt) return;
      // Bỏ selected toàn bộ
      document.querySelectorAll('.otp-dur-option').forEach(o => o.classList.remove('selected'));
      // Chọn cái đang click
      opt.classList.add('selected');
      const radio = opt.querySelector('input[type=radio]');
      if (radio) radio.checked = true;
    });
  },

  // ────────────────────────────────────────────────────────
  // Folder Upload Handler
  // ────────────────────────────────────────────────────────
  async handleFolderUpload(files) {
    if (!files || files.length === 0) return;
    const drawer = document.getElementById('upload-drawer');
    const listEl = document.getElementById('upload-list');
    const titleEl = document.getElementById('upload-drawer-title');

    if (drawer) drawer.classList.add('active');
    if (titleEl) titleEl.textContent = `Đang tải lên thư mục (${files.length} tệp)...`;

    let successCount = 0;
    for (let i = 0; i < files.length; i++) {
      const file = files[i];
      let relPath = '';
      if (file.webkitRelativePath) {
        const parts = file.webkitRelativePath.split('/');
        parts.pop(); // Bỏ tên file để lấy relative dir path
        relPath = parts.join('/');
      } else if (file.relPath) {
        relPath = file.relPath;
      }

      const itemEl = document.createElement('div');
      itemEl.className = 'upload-drawer-item';
      itemEl.innerHTML = `
        <div style="display: flex; justify-content: space-between; font-size: 12px; margin-bottom: 4px;">
          <span style="font-weight: 500; max-width: 260px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">${file.name}</span>
          <span class="upload-status-text" style="color: var(--text-muted); font-size: 11px;">0%</span>
        </div>
        ${relPath ? `<div style="font-size: 10px; color: var(--accent-blue); margin-bottom: 4px;">📁 ${relPath}</div>` : ''}
        <div style="height: 4px; background: rgba(255,255,255,0.08); border-radius: 2px; overflow: hidden;">
          <div class="upload-progress-fill" style="width: 0%; height: 100%; background: var(--accent-blue); transition: width 0.15s ease;"></div>
        </div>
      `;
      if (listEl) listEl.prepend(itemEl);

      const fillEl = itemEl.querySelector('.upload-progress-fill');
      const statEl = itemEl.querySelector('.upload-status-text');

      try {
        await API.uploadFile(this.currentFolderId, file, (pct) => {
          if (fillEl) fillEl.style.width = pct + '%';
          if (statEl) statEl.textContent = pct + '%';
        }, relPath);
        if (statEl) {
          statEl.textContent = '✓ Xong';
          statEl.style.color = '#10b981';
        }
        successCount++;
      } catch (err) {
        if (statEl) {
          statEl.textContent = '❌ ' + err.message;
          statEl.style.color = '#ef4444';
          statEl.title = err.message;
        }
        Toast.error('Lỗi tải lên ' + file.name + ': ' + err.message);
      }
    }

    Toast.success(`Đã hoàn tất tải lên thư mục (${successCount}/${files.length} tệp)!`);
    this.loadFiles(this.currentFolderId);
    App.refreshStats();
    AccountsManager.loadAccounts();
  },

  // ────────────────────────────────────────────────────────
  // Trash (Recycle Bin) Methods
  // ────────────────────────────────────────────────────────
  openTrashModal() {
    document.getElementById('modal-trash')?.classList.add('active');
    this.loadTrashList();
  },

  async loadTrashList() {
    const tbody = document.getElementById('trash-table-tbody');
    if (!tbody) return;
    tbody.innerHTML = '<tr><td colspan="4" style="text-align: center; padding: 24px; color: var(--text-muted);">Đang tải dữ liệu thùng rác...</td></tr>';
    try {
      const files = await API.listTrashFiles();
      if (!files || files.length === 0) {
        tbody.innerHTML = '<tr><td colspan="4" style="text-align: center; padding: 32px; color: var(--text-muted); font-size: 13px;">🗑️ Thùng rác hiện đang trống</td></tr>';
        return;
      }

      let html = '';
      files.forEach(f => {
        const icon = Utils.getFileIconSVG(f.mime_type || '', f.is_dir);
        const sizeText = f.is_dir ? 'Thư mục' : Utils.formatBytes(f.size_bytes);
        const delDate = f.deleted_at ? Utils.formatDate(f.deleted_at) : '-';
        html += `
          <tr>
            <td>
              <div class="file-name-cell">
                <span class="file-icon ${f.is_dir ? 'folder' : ''}">${icon}</span>
                <span>${f.name}</span>
              </div>
            </td>
            <td>${sizeText}</td>
            <td style="font-size: 11px; color: var(--text-muted);">${delDate}</td>
            <td style="text-align: right; white-space: nowrap;">
              <button class="btn btn-secondary btn-sm" onclick="FilesManager.restoreTrashFile('${f.id}')" style="font-size: 11px; padding: 3px 8px; color: #10b981; margin-right: 6px;" title="Khôi phục tệp">
                ↩️ Khôi phục
              </button>
              <button class="btn btn-danger btn-sm" onclick="FilesManager.purgeTrashFile('${f.id}', '${f.name.replace(/'/g, "\\'")}')" style="font-size: 11px; padding: 3px 8px;" title="Xóa vĩnh viễn">
                ✕ Xóa hẳn
              </button>
            </td>
          </tr>
        `;
      });
      tbody.innerHTML = html;
    } catch (err) {
      tbody.innerHTML = `<tr><td colspan="4" style="text-align: center; padding: 20px; color: #ef4444;">Lỗi: ${err.message}</td></tr>`;
    }
  },

  async restoreTrashFile(id) {
    try {
      await API.restoreTrashFile(id);
      Toast.success('Đã khôi phục tệp thành công!');
      this.loadTrashList();
      this.loadFiles(this.currentFolderId);
      App.refreshStats();
    } catch (err) {
      Toast.error('Lỗi khôi phục: ' + err.message);
    }
  },

  async purgeTrashFile(id, name) {
    if (!confirm(`Bạn có chắc muốn xóa VĨNH VIỄN "${name}"? Thao tác này không thể hoàn tác và sẽ xóa sạch chunks trên Google Drive.`)) return;
    try {
      await API.purgeTrashFile(id);
      Toast.success('Đã xóa vĩnh viễn tệp!');
      this.loadTrashList();
      App.refreshStats();
      AccountsManager.loadAccounts();
    } catch (err) {
      Toast.error('Lỗi xóa vĩnh viễn: ' + err.message);
    }
  },

  async emptyTrash() {
    if (!confirm('Bạn có chắc chắn muốn DỌN SẠCH THÙNG RÁC? Tất cả tệp trong thùng rác sẽ bị xóa vĩnh viễn khỏi Google Drive.')) return;
    try {
      const res = await API.emptyTrash();
      Toast.success(res.message || 'Đã dọn sạch thùng rác!');
      this.loadTrashList();
      App.refreshStats();
      AccountsManager.loadAccounts();
    } catch (err) {
      Toast.error('Lỗi dọn thùng rác: ' + err.message);
    }
  },

  // ────────────────────────────────────────────────────────
  // Public Share Links Methods
  // ────────────────────────────────────────────────────────
  openShareModal(fileId, fileName, isFolder = false) {
    document.getElementById('share-target-file-id').value = fileId;
    const labelEl = document.getElementById('share-file-name-label');
    if (labelEl) {
      labelEl.innerHTML = isFolder 
        ? `<span style="color: var(--accent-amber); font-weight:700;">📁 Thư mục:</span> <b>${fileName}</b> (Bao gồm tất cả tệp con)`
        : `<span style="color: var(--accent-blue); font-weight:700;">📄 Tệp tin:</span> <b>${fileName}</b>`;
    }
    document.getElementById('share-password-input').value = '';
    document.getElementById('share-expiry-select').value = '0'; // Default to permanent
    document.getElementById('share-max-downloads-select').value = '0';
    document.getElementById('share-result-box').style.display = 'none';
    document.getElementById('modal-share-link')?.classList.add('active');
  },

  async confirmCreateShareLink() {
    const fileId = document.getElementById('share-target-file-id').value;
    const pass = document.getElementById('share-password-input').value.trim();
    const expiry = document.getElementById('share-expiry-select').value;
    const maxDl = document.getElementById('share-max-downloads-select').value;

    try {
      const res = await API.createPublicShare(fileId, pass, expiry, maxDl);
      const host = window.location.origin;
      const shareURL = `${host}/share.html?token=${res.share_token}&ngrok-skip-browser-warning=true`;
      document.getElementById('share-result-url').value = shareURL;
      document.getElementById('share-result-box').style.display = 'block';
      Toast.success('Tạo link xem trực tiếp thành công!');
    } catch (err) {
      Toast.error('Lỗi tạo link chia sẻ: ' + err.message);
    }
  },

  openCurrentSharePreview() {
    const inp = document.getElementById('share-result-url');
    if (inp && inp.value) {
      window.open(inp.value, '_blank');
    }
  },

  copyShareLink() {
    const inp = document.getElementById('share-result-url');
    if (inp && inp.value) {
      navigator.clipboard.writeText(inp.value).then(() => {
        Toast.success('Đã sao chép đường link vào bộ nhớ tạm!');
      }).catch(() => {
        inp.select();
        document.execCommand('copy');
        Toast.success('Đã sao chép đường link!');
      });
    }
  },

  openSharesManagerModal() {
    document.getElementById('modal-shares-manager')?.classList.add('active');
    this.loadSharesList();
  },

  async loadSharesList() {
    const tbody = document.getElementById('shares-manager-tbody');
    if (!tbody) return;
    tbody.innerHTML = '<tr><td colspan="5" style="text-align: center; padding: 24px; color: var(--text-muted);">Đang tải danh sách link chia sẻ...</td></tr>';
    try {
      const shares = await API.listPublicShares();
      if (!shares || shares.length === 0) {
        tbody.innerHTML = '<tr><td colspan="5" style="text-align: center; padding: 32px; color: var(--text-muted); font-size: 13px;">🔗 Chưa có đường link chia sẻ nào được tạo</td></tr>';
        return;
      }

      let html = '';
      const host = window.location.origin;
      shares.forEach(sh => {
        const shareURL = `${host}/share.html?token=${sh.id}&ngrok-skip-browser-warning=true`;
        const hasPass = sh.has_password ? '🔒 Có mật khẩu' : '🌐 Công khai';
        const dlInfo = sh.max_downloads > 0 ? `${sh.download_count}/${sh.max_downloads}` : `${sh.download_count} (Không giới hạn)`;
        const expInfo = sh.expires_at ? Utils.formatDate(sh.expires_at) : 'Vĩnh viễn';
        const statusBadge = sh.is_active ? '<span class="badge badge-success" style="font-size: 10px;">Hoạt động</span>' : '<span class="badge badge-danger" style="font-size: 10px;">Hết hạn/Khóa</span>';

        html += `
          <tr>
            <td>
              <div style="font-weight: 600; color: var(--text-primary); max-width: 200px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">${sh.file_name}</div>
              <div style="font-size: 10px; color: var(--text-muted);">${Utils.formatBytes(sh.file_size)} • ${statusBadge}</div>
            </td>
            <td><span style="font-size: 11px; color: ${sh.has_password ? '#f59e0b' : '#38bdf8'};">${hasPass}</span></td>
            <td style="font-size: 11px;">${dlInfo}</td>
            <td style="font-size: 11px; color: var(--text-muted);">${expInfo}</td>
            <td style="text-align: right; white-space: nowrap;">
              <button class="btn btn-secondary btn-sm" onclick="window.open('${shareURL}', '_blank')" style="font-size: 11px; padding: 2px 7px; margin-right: 4px; color: #38bdf8;" title="Xem thử trực tiếp">
                ▶️ Xem
              </button>
              <button class="btn btn-secondary btn-sm" onclick="navigator.clipboard.writeText('${shareURL}'); Toast.success('Đã sao chép link!');" style="font-size: 11px; padding: 2px 7px; margin-right: 4px;">
                📋 Copy
              </button>
              <button class="btn btn-danger btn-sm" onclick="FilesManager.revokeShare('${sh.id}')" style="font-size: 11px; padding: 2px 7px;">
                ✕ Thu hồi
              </button>
            </td>
          </tr>
        `;
      });
      tbody.innerHTML = html;
    } catch (err) {
      tbody.innerHTML = `<tr><td colspan="5" style="text-align: center; padding: 20px; color: #ef4444;">Lỗi: ${err.message}</td></tr>`;
    }
  },

  async revokeShare(id) {
    if (!confirm('Bạn có chắc muốn thu hồi đường link chia sẻ này? Người nhận sẽ không thể truy cập được nữa.')) return;
    try {
      await API.revokePublicShare(id);
      Toast.success('Đã thu hồi link chia sẻ!');
      this.loadSharesList();
    } catch (err) {
      Toast.error('Lỗi thu hồi: ' + err.message);
    }
  },

};

