// ==========================================================================
// CloudPool Image Viewer PRO & Archive Inspector (High-Performance Module)
// ==========================================================================

const ImageArchiveViewer = {
  // Trạng thái Lightbox Image Viewer
  imageState: {
    isOpen: false,
    currentFileId: null,
    currentFileName: '',
    currentMimeType: '',
    streamURL: '',
    downloadURL: '',
    scale: 1,
    minScale: 0.1,
    maxScale: 25,
    rotation: 0,
    flipH: 1,
    flipV: 1,
    translateX: 0,
    translateY: 0,
    isDragging: false,
    dragStartX: 0,
    dragStartY: 0,
    album: [],          // Danh sách các ảnh cùng thư mục hiện tại
    currentIndex: -1,
    naturalWidth: 0,
    naturalHeight: 0,
    fileSize: 0,
    blobUrlToRevoke: null
  },

  // Trạng thái Archive Inspector
  archiveState: {
    currentFileId: null,
    currentFileName: '',
    entries: [],
    filterQuery: '',
    filterType: 'all',  // 'all' | 'file' | 'folder'
    zipInstance: null,
    downloadURL: '',
    totalUncompressed: 0,
    totalCompressed: 0,
    totalFiles: 0,
    totalFolders: 0
  },

  // Kiểm tra định dạng ảnh hỗ trợ
  isImageFile(name = '', mimeType = '') {
    if (mimeType && mimeType.toLowerCase().startsWith('image/')) return true;
    const ext = (name || '').toLowerCase().split('.').pop();
    const imageExts = ['jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'bmp', 'ico', 'avif', 'tiff', 'tif', 'heic', 'heif'];
    return imageExts.includes(ext);
  },

  // Kiểm tra định dạng tệp nén hỗ trợ
  isArchiveFile(name = '', mimeType = '') {
    const ext = (name || '').toLowerCase().split('.').pop();
    const archiveExts = ['zip', 'rar', '7z', 'tar', 'gz', 'tgz', 'bz2', 'xz', 'jar', 'apk', 'war', 'epub'];
    if (archiveExts.includes(ext)) return true;
    const lowerMime = (mimeType || '').toLowerCase();
    return lowerMime.includes('zip') || lowerMime.includes('compressed') || lowerMime.includes('archive') || lowerMime.includes('tar') || lowerMime.includes('7z') || lowerMime.includes('rar');
  },

  // Khởi tạo và liên kết sự kiện
  init() {
    this.injectStyles();
    this.createLightboxDOM();
    this.bindLightboxEvents();
  },

  // Inject CSS Styles tinh tế, hiện đại cho Image Viewer PRO và Archive Inspector
  injectStyles() {
    if (document.getElementById('image-archive-viewer-styles')) return;
    const style = document.createElement('style');
    style.id = 'image-archive-viewer-styles';
    style.textContent = `
      /* =========================================================
         LIGHTBOX IMAGE VIEWER PRO STYLES
         ========================================================= */
      .lightbox-pro-overlay {
        position: fixed;
        inset: 0;
        z-index: 999999;
        background: rgba(8, 12, 20, 0.96);
        backdrop-filter: blur(16px);
        -webkit-backdrop-filter: blur(16px);
        display: flex;
        flex-direction: column;
        opacity: 0;
        pointer-events: none;
        transition: opacity 0.25s cubic-bezier(0.16, 1, 0.3, 1);
        user-select: none;
        overflow: hidden;
      }
      .lightbox-pro-overlay.active {
        opacity: 1;
        pointer-events: auto;
      }

      /* Lightbox Header */
      .lightbox-pro-header {
        height: 60px;
        padding: 0 20px;
        display: flex;
        align-items: center;
        justify-content: space-between;
        background: rgba(13, 17, 23, 0.75);
        border-bottom: 1px solid rgba(255, 255, 255, 0.08);
        backdrop-filter: blur(12px);
        z-index: 100;
        flex-shrink: 0;
      }
      .lightbox-pro-meta {
        display: flex;
        align-items: center;
        gap: 12px;
        overflow: hidden;
      }
      .lightbox-pro-badge {
        font-size: 11px;
        font-weight: 700;
        padding: 3px 8px;
        border-radius: 6px;
        background: linear-gradient(135deg, #10b981, #06b6d4);
        color: #fff;
        letter-spacing: 0.5px;
        text-transform: uppercase;
        flex-shrink: 0;
      }
      .lightbox-pro-title {
        font-size: 14px;
        font-weight: 600;
        color: #f1f5f9;
        white-space: nowrap;
        overflow: hidden;
        text-overflow: ellipsis;
        max-width: 380px;
      }
      .lightbox-pro-pill {
        font-size: 11px;
        padding: 2px 8px;
        border-radius: 999px;
        background: rgba(255, 255, 255, 0.08);
        color: #94a3b8;
        border: 1px solid rgba(255, 255, 255, 0.06);
        white-space: nowrap;
        flex-shrink: 0;
      }

      .lightbox-pro-actions {
        display: flex;
        align-items: center;
        gap: 8px;
        flex-shrink: 0;
      }
      .lightbox-action-btn {
        background: rgba(255, 255, 255, 0.06);
        border: 1px solid rgba(255, 255, 255, 0.1);
        color: #f1f5f9;
        padding: 6px 12px;
        border-radius: 8px;
        font-size: 12px;
        font-weight: 500;
        display: inline-flex;
        align-items: center;
        gap: 6px;
        cursor: pointer;
        transition: all 0.15s ease;
        text-decoration: none;
      }
      .lightbox-action-btn:hover {
        background: rgba(255, 255, 255, 0.14);
        border-color: rgba(255, 255, 255, 0.25);
        color: #fff;
      }
      .lightbox-close-btn {
        width: 34px;
        height: 34px;
        border-radius: 8px;
        background: rgba(239, 68, 68, 0.15);
        border: 1px solid rgba(239, 68, 68, 0.3);
        color: #f87171;
        font-size: 18px;
        display: flex;
        align-items: center;
        justify-content: center;
        cursor: pointer;
        transition: all 0.15s ease;
        padding: 0;
      }
      .lightbox-close-btn:hover {
        background: #ef4444;
        color: #fff;
        transform: scale(1.05);
      }

      /* Lightbox Stage Area */
      .lightbox-pro-stage {
        flex: 1;
        position: relative;
        overflow: hidden;
        display: flex;
        align-items: center;
        justify-content: center;
        cursor: grab;
      }
      .lightbox-pro-stage.dragging {
        cursor: grabbing;
      }

      .lightbox-pro-img-wrapper {
        position: absolute;
        display: flex;
        align-items: center;
        justify-content: center;
        transition: transform 0.05s linear;
        will-change: transform;
        transform-origin: center center;
      }
      .lightbox-pro-img {
        max-width: 90vw;
        max-height: 82vh;
        object-fit: contain;
        border-radius: 4px;
        box-shadow: 0 20px 50px rgba(0, 0, 0, 0.7);
        pointer-events: none;
        user-select: none;
      }

      /* Navigation Buttons (Prev / Next) */
      .lightbox-nav-arrow {
        position: absolute;
        top: 50%;
        transform: translateY(-50%);
        width: 52px;
        height: 52px;
        border-radius: 50%;
        background: rgba(15, 23, 42, 0.65);
        border: 1px solid rgba(255, 255, 255, 0.15);
        color: #fff;
        display: flex;
        align-items: center;
        justify-content: center;
        font-size: 24px;
        cursor: pointer;
        z-index: 1000;
        transition: all 0.2s cubic-bezier(0.16, 1, 0.3, 1);
        backdrop-filter: blur(8px);
      }
      .lightbox-nav-arrow:hover {
        background: rgba(47, 129, 247, 0.85);
        border-color: #58a6ff;
        transform: translateY(-50%) scale(1.1);
        box-shadow: 0 0 20px rgba(47, 129, 247, 0.5);
      }
      .lightbox-nav-arrow:active {
        transform: translateY(-50%) scale(0.95);
      }
      .lightbox-nav-arrow.prev { left: 24px; }
      .lightbox-nav-arrow.next { right: 24px; }
      .lightbox-nav-arrow.disabled {
        opacity: 0.25;
        pointer-events: none;
      }

      /* Floating Bottom Dock Toolbar */
      .lightbox-pro-dock {
        position: absolute;
        bottom: 24px;
        left: 50%;
        transform: translateX(-50%);
        background: rgba(15, 23, 42, 0.85);
        border: 1px solid rgba(255, 255, 255, 0.15);
        border-radius: 999px;
        padding: 6px 14px;
        display: flex;
        align-items: center;
        gap: 6px;
        backdrop-filter: blur(16px);
        box-shadow: 0 10px 30px rgba(0, 0, 0, 0.6);
        z-index: 1000;
      }
      .lightbox-dock-btn {
        background: transparent;
        border: none;
        color: #cbd5e1;
        width: 36px;
        height: 36px;
        border-radius: 50%;
        display: flex;
        align-items: center;
        justify-content: center;
        cursor: pointer;
        transition: all 0.15s ease;
      }
      .lightbox-dock-btn:hover {
        background: rgba(255, 255, 255, 0.12);
        color: #fff;
        transform: translateY(-2px);
      }
      .lightbox-dock-btn:active {
        transform: translateY(0);
      }
      .lightbox-dock-divider {
        width: 1px;
        height: 20px;
        background: rgba(255, 255, 255, 0.15);
        margin: 0 4px;
      }
      .lightbox-dock-zoom-badge {
        font-size: 12px;
        font-weight: 600;
        color: #38bdf8;
        padding: 4px 8px;
        border-radius: 6px;
        cursor: pointer;
        min-width: 52px;
        text-align: center;
        transition: background 0.15s ease;
      }
      .lightbox-dock-zoom-badge:hover {
        background: rgba(56, 189, 248, 0.15);
      }

      /* Loading Spinner for Image */
      .lightbox-spinner-wrap {
        position: absolute;
        display: flex;
        flex-direction: column;
        align-items: center;
        gap: 12px;
        color: #94a3b8;
        font-size: 13px;
        z-index: 10;
      }
      .lightbox-spinner {
        width: 44px;
        height: 44px;
        border: 3px solid rgba(255, 255, 255, 0.1);
        border-top-color: #38bdf8;
        border-radius: 50%;
        animation: lb-spin 0.8s linear infinite;
      }
      @keyframes lb-spin {
        to { transform: rotate(360deg); }
      }

      /* =========================================================
         ARCHIVE INSPECTOR STYLES
         ========================================================= */
      .archive-inspector-wrap {
        width: 100%;
        height: 100%;
        display: flex;
        flex-direction: column;
        gap: 12px;
        background: var(--bg-primary, #0d1117);
        color: var(--text-primary, #f0f6fc);
      }
      .archive-header-card {
        background: var(--bg-secondary, #161b22);
        border: 1px solid var(--border-subtle, #30363d);
        border-radius: var(--radius-md, 10px);
        padding: 16px;
        display: flex;
        flex-direction: column;
        gap: 14px;
      }
      .archive-header-top {
        display: flex;
        align-items: center;
        justify-content: space-between;
        flex-wrap: wrap;
        gap: 12px;
      }
      .archive-title-group {
        display: flex;
        align-items: center;
        gap: 12px;
      }
      .archive-icon-badge {
        width: 44px;
        height: 44px;
        border-radius: 10px;
        background: linear-gradient(135deg, rgba(236, 72, 153, 0.2), rgba(168, 85, 247, 0.2));
        border: 1px solid rgba(236, 72, 153, 0.4);
        display: flex;
        align-items: center;
        justify-content: center;
        font-size: 22px;
        flex-shrink: 0;
      }
      .archive-file-name {
        font-size: 16px;
        font-weight: 700;
        color: #f1f5f9;
        word-break: break-all;
      }
      .archive-type-badge {
        font-size: 11px;
        padding: 2px 8px;
        border-radius: 4px;
        font-weight: 600;
        background: rgba(236, 72, 153, 0.15);
        color: #f472b6;
        border: 1px solid rgba(236, 72, 153, 0.3);
      }
      .archive-stats-grid {
        display: grid;
        grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
        gap: 10px;
      }
      .archive-stat-card {
        background: rgba(255, 255, 255, 0.03);
        border: 1px solid rgba(255, 255, 255, 0.06);
        border-radius: 8px;
        padding: 10px 12px;
      }
      .archive-stat-label {
        font-size: 11px;
        color: var(--text-muted, #8b949e);
        margin-bottom: 4px;
      }
      .archive-stat-value {
        font-size: 15px;
        font-weight: 700;
        color: #f1f5f9;
      }

      /* Archive Toolbar & Search */
      .archive-toolbar {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: 10px;
        flex-wrap: wrap;
      }
      .archive-search-box {
        position: relative;
        flex: 1;
        min-width: 220px;
        max-width: 400px;
      }
      .archive-search-input {
        width: 100%;
        padding: 8px 12px 8px 34px;
        background: var(--bg-secondary, #161b22);
        border: 1px solid var(--border-subtle, #30363d);
        border-radius: var(--radius-sm, 6px);
        color: #f1f5f9;
        font-size: 12px;
        outline: none;
        transition: border-color 0.15s ease;
      }
      .archive-search-input:focus {
        border-color: var(--accent-blue, #2f81f7);
      }
      .archive-search-icon {
        position: absolute;
        left: 10px;
        top: 50%;
        transform: translateY(-50%);
        color: var(--text-muted, #8b949e);
        pointer-events: none;
      }
      .archive-filter-btn-group {
        display: flex;
        gap: 4px;
        background: rgba(255, 255, 255, 0.04);
        padding: 3px;
        border-radius: 6px;
        border: 1px solid rgba(255, 255, 255, 0.08);
      }
      .archive-filter-btn {
        padding: 4px 10px;
        font-size: 11px;
        font-weight: 500;
        border-radius: 4px;
        border: none;
        background: transparent;
        color: var(--text-secondary, #8b949e);
        cursor: pointer;
        transition: all 0.15s ease;
      }
      .archive-filter-btn.active {
        background: var(--accent-blue, #2f81f7);
        color: #fff;
      }

      /* Archive Table */
      .archive-table-wrapper {
        flex: 1;
        overflow: auto;
        border: 1px solid var(--border-subtle, #30363d);
        border-radius: var(--radius-md, 10px);
        background: var(--bg-secondary, #161b22);
        min-height: 280px;
        max-height: 52vh;
      }
      .archive-table {
        width: 100%;
        border-collapse: collapse;
        font-size: 12px;
      }
      .archive-table th {
        position: sticky;
        top: 0;
        background: #1c2128;
        padding: 10px 14px;
        text-align: left;
        font-weight: 600;
        color: #8b949e;
        border-bottom: 1px solid var(--border-subtle, #30363d);
        white-space: nowrap;
        z-index: 5;
      }
      .archive-table td {
        padding: 8px 14px;
        border-bottom: 1px solid rgba(255, 255, 255, 0.04);
        color: #f1f5f9;
        vertical-align: middle;
      }
      .archive-table tr:hover td {
        background: rgba(255, 255, 255, 0.03);
      }
      .archive-file-cell {
        display: flex;
        align-items: center;
        gap: 10px;
        word-break: break-all;
      }
      .archive-file-icon {
        font-size: 16px;
        flex-shrink: 0;
      }
      .archive-path-sub {
        font-size: 10px;
        color: var(--text-muted, #8b949e);
        display: block;
        margin-top: 2px;
      }
      .archive-action-btn-sm {
        padding: 4px 8px;
        border-radius: 4px;
        font-size: 11px;
        border: 1px solid rgba(255, 255, 255, 0.12);
        background: rgba(255, 255, 255, 0.05);
        color: #cbd5e1;
        cursor: pointer;
        display: inline-flex;
        align-items: center;
        gap: 4px;
        text-decoration: none;
        transition: all 0.15s ease;
      }
      .archive-action-btn-sm:hover {
        background: var(--accent-blue, #2f81f7);
        border-color: var(--accent-blue, #2f81f7);
        color: #fff;
      }
      .archive-ratio-badge {
        font-size: 10px;
        font-weight: 600;
        padding: 2px 6px;
        border-radius: 4px;
        background: rgba(16, 185, 129, 0.15);
        color: #34d399;
        display: inline-block;
        margin-left: 6px;
      }
    `;
    document.head.appendChild(style);
  },

  // Tạo cấu trúc DOM Lightbox tự động vào body nếu chưa có
  createLightboxDOM() {
    if (document.getElementById('modal-image-lightbox')) return;

    const overlay = document.createElement('div');
    overlay.id = 'modal-image-lightbox';
    overlay.className = 'lightbox-pro-overlay';
    overlay.innerHTML = `
      <!-- Lightbox Header Bar -->
      <div class="lightbox-pro-header">
        <div class="lightbox-pro-meta">
          <span class="lightbox-pro-badge">ẢNH PRO</span>
          <span id="lb-title" class="lightbox-pro-title" title="">Tên tệp ảnh</span>
          <span id="lb-counter" class="lightbox-pro-pill">1 / 1</span>
          <span id="lb-dims" class="lightbox-pro-pill" style="color: #38bdf8;">0 × 0 px</span>
          <span id="lb-filesize" class="lightbox-pro-pill" style="display: none;">0 KB</span>
        </div>
        <div class="lightbox-pro-actions">
          <button id="lb-btn-chunk" class="lightbox-action-btn" title="Xem bản đồ Chunks trên CloudPool">
            🔍 Xem Chunks
          </button>
          <a id="lb-btn-download" class="lightbox-action-btn" title="Tải ảnh gốc về máy" download>
            ⬇️ Tải về
          </a>
          <button id="lb-btn-fullscreen" class="lightbox-action-btn" title="Toàn màn hình (F)">
            ⛶ Toàn màn hình
          </button>
          <button id="lb-btn-close" class="lightbox-close-btn" title="Đóng Lightbox (ESC)">
            ✕
          </button>
        </div>
      </div>

      <!-- Stage Area with Nav Arrows -->
      <div class="lightbox-pro-stage" id="lb-stage">
        <!-- Navigation Buttons -->
        <button id="lb-btn-prev" class="lightbox-nav-arrow prev" title="Ảnh trước (Mũi tên Trái / A)">❮</button>
        <button id="lb-btn-next" class="lightbox-nav-arrow next" title="Ảnh kế tiếp (Mũi tên Phải / D)">❯</button>

        <!-- Loading Spinner -->
        <div id="lb-loading" class="lightbox-spinner-wrap" style="display: none;">
          <div class="lightbox-spinner"></div>
          <span>Đang nạp ảnh từ Google Drive...</span>
        </div>

        <!-- Center Image Wrapper for Pan & Zoom -->
        <div class="lightbox-pro-img-wrapper" id="lb-img-wrapper">
          <img id="lb-img-element" class="lightbox-pro-img" src="" alt="Preview" draggable="false" style="display: none;">
        </div>
      </div>

      <!-- Bottom Floating Glassmorphism Toolbar Dock -->
      <div class="lightbox-pro-dock">
        <button class="lightbox-dock-btn" id="lb-tool-zoom-out" title="Thu nhỏ (Ctrl - / Lăn chuột xuống)">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="8"></circle><line x1="21" y1="21" x2="16.65" y2="16.65"></line><line x1="8" y1="11" x2="14" y2="11"></line></svg>
        </button>
        <span id="lb-zoom-badge" class="lightbox-dock-zoom-badge" title="Nhấn để reset tỷ lệ 100%">100%</span>
        <button class="lightbox-dock-btn" id="lb-tool-zoom-in" title="Phóng to (Ctrl + / Lăn chuột lên)">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="8"></circle><line x1="21" y1="21" x2="16.65" y2="16.65"></line><line x1="11" y1="8" x2="11" y2="14"></line><line x1="8" y1="11" x2="14" y2="11"></line></svg>
        </button>
        <div class="lightbox-dock-divider"></div>
        <button class="lightbox-dock-btn" id="lb-tool-fit" title="Vừa khít khung hình">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M8 3H5a2 2 0 0 0-2 2v3m18 0V5a2 2 0 0 0-2-2h-3m0 18h3a2 2 0 0 0 2-2v-3M3 16v3a2 2 0 0 0 2 2h3"></path></svg>
        </button>
        <button class="lightbox-dock-btn" id="lb-tool-1to1" title="Kích thước thực tế (1:1)" style="font-size: 11px; font-weight: 700;">
          1:1
        </button>
        <div class="lightbox-dock-divider"></div>
        <button class="lightbox-dock-btn" id="lb-tool-rotate-left" title="Xoay ngược chiều 90°">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"></path><polyline points="3 3 3 8 8 8"></polyline></svg>
        </button>
        <button class="lightbox-dock-btn" id="lb-tool-rotate-right" title="Xoay thuận chiều 90° (R)">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 12a9 9 0 1 1-9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"></path><polyline points="21 3 21 8 16 8"></polyline></svg>
        </button>
        <div class="lightbox-dock-divider"></div>
        <button class="lightbox-dock-btn" id="lb-tool-flip-h" title="Lật ảnh ngang (H)">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 2v20M7 8l-5 4 5 4V8zM17 8l5 4-5 4V8z"></path></svg>
        </button>
        <button class="lightbox-dock-btn" id="lb-tool-flip-v" title="Lật ảnh dọc (V)">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M2 12h20M8 7l4-5 4 5H8zM8 17l4 5 4-5H8z"></path></svg>
        </button>
        <div class="lightbox-dock-divider"></div>
        <button class="lightbox-dock-btn" id="lb-tool-reset" title="Khôi phục góc nhìn gốc (0)">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"></path><path d="M21 3v5h-5"></path><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"></path><path d="M3 21v-5h5"></path></svg>
        </button>
      </div>
    `;

    document.body.appendChild(overlay);
  },

  // Bắt các sự kiện tương tác của Lightbox
  bindLightboxEvents() {
    const overlay = document.getElementById('modal-image-lightbox');
    if (!overlay || overlay._eventsBound) return;
    overlay._eventsBound = true;

    // Nút đóng
    document.getElementById('lb-btn-close')?.addEventListener('click', () => this.closeImageViewer());

    // Nút Prev / Next
    document.getElementById('lb-btn-prev')?.addEventListener('click', (e) => { e.stopPropagation(); this.prevImage(); });
    document.getElementById('lb-btn-next')?.addEventListener('click', (e) => { e.stopPropagation(); this.nextImage(); });

    // Nút Fullscreen
    document.getElementById('lb-btn-fullscreen')?.addEventListener('click', () => this.toggleFullscreen());

    // Nút Chunks
    document.getElementById('lb-btn-chunk')?.addEventListener('click', () => {
      if (this.imageState.currentFileId && typeof FilesManager !== 'undefined' && FilesManager.showChunkMap) {
        this.closeImageViewer();
        FilesManager.showChunkMap(this.imageState.currentFileId, this.imageState.currentFileName);
      }
    });

    // Tool zoom in / out / reset
    document.getElementById('lb-tool-zoom-in')?.addEventListener('click', () => this.zoom(1.25));
    document.getElementById('lb-tool-zoom-out')?.addEventListener('click', () => this.zoom(0.8));
    document.getElementById('lb-zoom-badge')?.addEventListener('click', () => this.resetTransform());
    document.getElementById('lb-tool-fit')?.addEventListener('click', () => this.resetTransform());
    document.getElementById('lb-tool-1to1')?.addEventListener('click', () => this.setActualSize());
    document.getElementById('lb-tool-reset')?.addEventListener('click', () => this.resetTransform());

    // Rotate & Flip
    document.getElementById('lb-tool-rotate-right')?.addEventListener('click', () => this.rotate(90));
    document.getElementById('lb-tool-rotate-left')?.addEventListener('click', () => this.rotate(-90));
    document.getElementById('lb-tool-flip-h')?.addEventListener('click', () => this.flip('h'));
    document.getElementById('lb-tool-flip-v')?.addEventListener('click', () => this.flip('v'));

    // Stage Wheel Zoom & Pan Dragging
    const stage = document.getElementById('lb-stage');
    if (stage) {
      // Zoom bằng con lăn chuột
      stage.addEventListener('wheel', (e) => {
        if (!this.imageState.isOpen) return;
        e.preventDefault();
        const factor = e.deltaY < 0 ? 1.15 : 0.87;
        this.zoom(factor);
      }, { passive: false });

      // Pan Drag (kéo rê chuột)
      stage.addEventListener('mousedown', (e) => {
        // Chỉ kéo khi click chuột trái và không click vào nút navigation
        if (e.button !== 0 || e.target.closest('.lightbox-nav-arrow')) return;
        this.imageState.isDragging = true;
        this.imageState.dragStartX = e.clientX - this.imageState.translateX;
        this.imageState.dragStartY = e.clientY - this.imageState.translateY;
        stage.classList.add('dragging');
      });

      window.addEventListener('mousemove', (e) => {
        if (!this.imageState.isDragging || !this.imageState.isOpen) return;
        this.imageState.translateX = e.clientX - this.imageState.dragStartX;
        this.imageState.translateY = e.clientY - this.imageState.dragStartY;
        this.updateTransform();
      });

      window.addEventListener('mouseup', () => {
        if (this.imageState.isDragging) {
          this.imageState.isDragging = false;
          stage.classList.remove('dragging');
        }
      });

      // Double click để zoom to 2.5x hoặc reset về vừa màn hình
      stage.addEventListener('dblclick', (e) => {
        if (e.target.closest('.lightbox-nav-arrow') || e.target.closest('.lightbox-pro-dock')) return;
        if (this.imageState.scale > 1.2 || this.imageState.scale < 0.9) {
          this.resetTransform();
        } else {
          this.imageState.scale = 2.5;
          this.updateTransform();
        }
      });
    }

    // Keyboard Shortcuts
    window.addEventListener('keydown', (e) => {
      if (!this.imageState.isOpen) return;
      if (e.key === 'Escape') {
        this.closeImageViewer();
      } else if (e.key === 'ArrowLeft' || e.key === 'a' || e.key === 'A') {
        this.prevImage();
      } else if (e.key === 'ArrowRight' || e.key === 'd' || e.key === 'D') {
        this.nextImage();
      } else if (e.key === '+' || e.key === '=' || e.key === 'ArrowUp') {
        e.preventDefault();
        this.zoom(1.25);
      } else if (e.key === '-' || e.key === '_' || e.key === 'ArrowDown') {
        e.preventDefault();
        this.zoom(0.8);
      } else if (e.key === '0') {
        this.resetTransform();
      } else if (e.key === 'r' || e.key === 'R') {
        this.rotate(90);
      } else if (e.key === 'h' || e.key === 'H') {
        this.flip('h');
      } else if (e.key === 'v' || e.key === 'V') {
        this.flip('v');
      } else if (e.key === 'f' || e.key === 'F') {
        this.toggleFullscreen();
      }
    });
  },

  // Áp dụng biến đổi Transform (Zoom, Pan, Rotate, Flip) lên ảnh
  updateTransform() {
    const wrapper = document.getElementById('lb-img-wrapper');
    const badge = document.getElementById('lb-zoom-badge');
    if (!wrapper) return;

    const { translateX, translateY, scale, rotation, flipH, flipV } = this.imageState;
    wrapper.style.transform = `translate(${translateX}px, ${translateY}px) scale(${scale}) rotate(${rotation}deg) scaleX(${flipH}) scaleY(${flipV})`;

    if (badge) {
      badge.textContent = `${Math.round(scale * 100)}%`;
    }
  },

  // Phóng to / Thu nhỏ theo tỉ lệ
  zoom(factor) {
    let newScale = this.imageState.scale * factor;
    newScale = Math.max(this.imageState.minScale, Math.min(newScale, this.imageState.maxScale));
    this.imageState.scale = newScale;
    this.updateTransform();
  },

  // Xoay ảnh 90 độ
  rotate(deg) {
    this.imageState.rotation = (this.imageState.rotation + deg) % 360;
    this.updateTransform();
  },

  // Lật ảnh ngang / dọc
  flip(direction) {
    if (direction === 'h') {
      this.imageState.flipH = this.imageState.flipH === 1 ? -1 : 1;
    } else {
      this.imageState.flipV = this.imageState.flipV === 1 ? -1 : 1;
    }
    this.updateTransform();
  },

  // Khôi phục về kích thước 1:1 pixel
  setActualSize() {
    this.imageState.scale = 1;
    this.imageState.translateX = 0;
    this.imageState.translateY = 0;
    this.updateTransform();
  },

  // Reset toàn bộ transform về mặc định
  resetTransform() {
    this.imageState.scale = 1;
    this.imageState.translateX = 0;
    this.imageState.translateY = 0;
    this.imageState.rotation = 0;
    this.imageState.flipH = 1;
    this.imageState.flipV = 1;
    this.updateTransform();
  },

  // Bật / Tắt Toàn màn hình
  toggleFullscreen() {
    const overlay = document.getElementById('modal-image-lightbox');
    if (!overlay) return;
    if (!document.fullscreenElement) {
      if (overlay.requestFullscreen) overlay.requestFullscreen().catch(() => {});
      else if (overlay.webkitRequestFullscreen) overlay.webkitRequestFullscreen();
    } else {
      if (document.exitFullscreen) document.exitFullscreen().catch(() => {});
    }
  },

  // Mở Lightbox Image Viewer PRO
  openImageViewer(fileId, fileName, mimeType, streamURL, downloadURL, optionalBlobUrl = null, customAlbum = null) {
    this.init(); // Đảm bảo DOM & Style đã được inject

    const overlay = document.getElementById('modal-image-lightbox');
    const titleEl = document.getElementById('lb-title');
    const counterEl = document.getElementById('lb-counter');
    const dimsEl = document.getElementById('lb-dims');
    const sizeEl = document.getElementById('lb-filesize');
    const downloadBtn = document.getElementById('lb-btn-download');
    const imgEl = document.getElementById('lb-img-element');
    const loadingEl = document.getElementById('lb-loading');
    const prevBtn = document.getElementById('lb-btn-prev');
    const nextBtn = document.getElementById('lb-btn-next');

    if (!overlay || !imgEl) return;

    // Dọn dẹp blob url cũ nếu có
    if (this.imageState.blobUrlToRevoke) {
      try { URL.revokeObjectURL(this.imageState.blobUrlToRevoke); } catch(_) {}
      this.imageState.blobUrlToRevoke = null;
    }
    if (optionalBlobUrl) {
      this.imageState.blobUrlToRevoke = optionalBlobUrl;
    }

    // Reset transform
    this.resetTransform();

    // Lưu state
    this.imageState.isOpen = true;
    this.imageState.currentFileId = fileId;
    this.imageState.currentFileName = fileName;
    this.imageState.currentMimeType = mimeType;
    this.imageState.streamURL = optionalBlobUrl || streamURL;
    this.imageState.downloadURL = downloadURL || streamURL;

    // Cập nhật Album thông minh
    if (Array.isArray(customAlbum) && customAlbum.length > 0) {
      this.imageState.album = customAlbum;
    } else if (typeof FilesManager !== 'undefined' && Array.isArray(FilesManager.files)) {
      this.imageState.album = FilesManager.files.filter(f => !f.is_dir && this.isImageFile(f.name, f.mime_type));
    } else {
      this.imageState.album = [{ id: fileId, name: fileName, mime_type: mimeType, size_bytes: 0 }];
    }

    // Tìm index hiện tại trong album
    const idx = this.imageState.album.findIndex(f => f.id === fileId || f.name === fileName);
    this.imageState.currentIndex = idx !== -1 ? idx : 0;

    // Cập nhật Header
    if (titleEl) {
      titleEl.textContent = fileName;
      titleEl.title = fileName;
    }
    if (counterEl) {
      const total = this.imageState.album.length;
      counterEl.textContent = `Ảnh ${this.imageState.currentIndex + 1} / ${Math.max(total, 1)}`;
    }
    if (dimsEl) dimsEl.textContent = 'Đang đọc kích thước...';

    // File size nếu có
    const currentItem = this.imageState.album[this.imageState.currentIndex];
    if (sizeEl) {
      if (currentItem && currentItem.size_bytes) {
        sizeEl.style.display = 'inline-block';
        sizeEl.textContent = (typeof Utils !== 'undefined' && Utils.formatBytes) ? Utils.formatBytes(currentItem.size_bytes) : '';
      } else {
        sizeEl.style.display = 'none';
      }
    }

    // Download Button
    if (downloadBtn) {
      downloadBtn.href = this.imageState.downloadURL;
      downloadBtn.setAttribute('download', fileName);
    }

    // Điều khiển nút Prev/Next
    const hasMultiple = this.imageState.album.length > 1;
    if (prevBtn) prevBtn.classList.toggle('disabled', !hasMultiple);
    if (nextBtn) nextBtn.classList.toggle('disabled', !hasMultiple);

    // Mở overlay
    overlay.style.display = 'flex';
    requestAnimationFrame(() => overlay.classList.add('active'));

    // Nạp ảnh
    if (loadingEl) loadingEl.style.display = 'flex';
    imgEl.style.display = 'none';

    const tempImg = new Image();
    const targetSrc = this.imageState.streamURL;

    tempImg.onload = () => {
      if (!this.imageState.isOpen || this.imageState.streamURL !== targetSrc) return;
      this.imageState.naturalWidth = tempImg.naturalWidth;
      this.imageState.naturalHeight = tempImg.naturalHeight;

      imgEl.src = targetSrc;
      imgEl.style.display = 'block';
      if (loadingEl) loadingEl.style.display = 'none';

      if (dimsEl) {
        dimsEl.textContent = `${tempImg.naturalWidth} × ${tempImg.naturalHeight} px`;
      }
    };

    tempImg.onerror = () => {
      if (!this.imageState.isOpen) return;
      if (loadingEl) loadingEl.style.display = 'none';
      if (dimsEl) dimsEl.textContent = 'Không thể nạp ảnh';
      if (typeof Toast !== 'undefined') {
        Toast.error(`Không thể tải hình ảnh "${fileName}". Có thể tệp bị lỗi hoặc cần xác thực.`);
      }
    };

    tempImg.src = targetSrc;
  },

  // Đóng Lightbox Image Viewer
  closeImageViewer() {
    const overlay = document.getElementById('modal-image-lightbox');
    if (!overlay) return;
    this.imageState.isOpen = false;
    overlay.classList.remove('active');
    setTimeout(() => {
      if (!this.imageState.isOpen) {
        overlay.style.display = 'none';
        const imgEl = document.getElementById('lb-img-element');
        if (imgEl) imgEl.src = '';
        if (this.imageState.blobUrlToRevoke) {
          try { URL.revokeObjectURL(this.imageState.blobUrlToRevoke); } catch(_) {}
          this.imageState.blobUrlToRevoke = null;
        }
        if (document.fullscreenElement && document.exitFullscreen) {
          document.exitFullscreen().catch(() => {});
        }
      }
    }, 250);
  },

  // Chuyển sang ảnh trước
  prevImage() {
    if (this.imageState.album.length <= 1) return;
    let idx = this.imageState.currentIndex - 1;
    if (idx < 0) idx = this.imageState.album.length - 1; // Wrap-around
    this.navigateToAlbumItem(idx);
  },

  // Chuyển sang ảnh tiếp theo
  nextImage() {
    if (this.imageState.album.length <= 1) return;
    let idx = this.imageState.currentIndex + 1;
    if (idx >= this.imageState.album.length) idx = 0; // Wrap-around
    this.navigateToAlbumItem(idx);
  },

  // Điều hướng tới phần tử trong Album
  navigateToAlbumItem(index) {
    const item = this.imageState.album[index];
    if (!item) return;

    let streamURL = `/api/files/stream?id=${encodeURIComponent(item.id)}`;
    let downloadURL = `/api/files/download?id=${encodeURIComponent(item.id)}`;

    // Gắn token xác thực
    const user = (typeof API !== 'undefined' && API.getCurrentUser) ? API.getCurrentUser() : null;
    const authToken = (typeof API !== 'undefined' && API.getToken) ? API.getToken() : (localStorage.getItem('cloudpool_jwt_token') || localStorage.getItem('cloudpool_token') || sessionStorage.getItem('cloudpool_token') || (user && user.token ? user.token : ''));
    if (authToken) {
      streamURL += `&token=${encodeURIComponent(authToken)}`;
      downloadURL += `&token=${encodeURIComponent(authToken)}`;
    }

    this.openImageViewer(item.id, item.name, item.mime_type || '', streamURL, downloadURL, null, this.imageState.album);
  },

  // ==========================================================================
  // ARCHIVE INSPECTOR MODULE (Zero-Unpack Cloud Zip & Rar Inspection)
  // ==========================================================================

  // Render giao diện Archive Inspector vào container preview
  async renderArchiveInspector(container, streamURL, fileName, downloadURL, fileId = '') {
    this.init(); // Ensure styles are injected

    this.archiveState.currentFileId = fileId;
    this.archiveState.currentFileName = fileName;
    this.archiveState.downloadURL = downloadURL;
    this.archiveState.entries = [];
    this.archiveState.filterQuery = '';
    this.archiveState.filterType = 'all';

    // Giao diện khung chờ ban đầu
    container.innerHTML = `
      <div class="archive-inspector-wrap" id="archive-inspector-root">
        <div class="archive-header-card">
          <div class="archive-header-top">
            <div class="archive-title-group">
              <div class="archive-icon-badge">📦</div>
              <div>
                <div class="archive-file-name">${fileName}</div>
                <div style="display: flex; gap: 8px; align-items: center; margin-top: 4px;">
                  <span class="archive-type-badge">TỆP NÉN CLOUD</span>
                  <span id="archive-status-text" style="font-size: 11px; color: var(--accent-cyan, #06b6d4);">⚡ Đang tải và phân tích cấu trúc nhị phân...</span>
                </div>
              </div>
            </div>
            <div style="display: flex; gap: 8px;">
              ${fileId ? `<button class="btn btn-secondary btn-sm" onclick="FilesManager.showChunkMap('${fileId}', '${fileName}')">🔍 Xem bản đồ Chunks</button>` : ''}
              <a class="btn btn-primary btn-sm" href="${downloadURL}" download="${fileName}">⬇️ Tải tệp nén về máy</a>
            </div>
          </div>
          <div class="archive-stats-grid" id="archive-stats-box">
            <div class="archive-stat-card"><div class="archive-stat-label">Tổng số tệp con</div><div class="archive-stat-value" id="stat-files">-</div></div>
            <div class="archive-stat-card"><div class="archive-stat-label">Tổng số thư mục</div><div class="archive-stat-value" id="stat-folders">-</div></div>
            <div class="archive-stat-card"><div class="archive-stat-label">Kích thước giải nén</div><div class="archive-stat-value" id="stat-uncomp">-</div></div>
            <div class="archive-stat-card"><div class="archive-stat-label">Kích thước tệp nén</div><div class="archive-stat-value" id="stat-comp">-</div></div>
          </div>
        </div>

        <div id="archive-content-body" style="flex: 1; display: flex; flex-direction: column; gap: 10px;">
          <div style="padding: 40px; text-align: center; color: var(--text-muted, #8b949e);">
            <div class="lightbox-spinner" style="margin: 0 auto 16px auto;"></div>
            <div>Đang tải dữ liệu tệp nén từ Google Drive (không giải nén ra đĩa)...</div>
          </div>
        </div>
      </div>
    `;

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
      if (!res.ok) throw new Error(`HTTP Error ${res.status}`);

      const arrayBuffer = await res.arrayBuffer();
      const uint8 = new Uint8Array(arrayBuffer);

      // Nhận diện định dạng qua Magic Bytes
      const isZip = (uint8[0] === 0x50 && uint8[1] === 0x4B); // 'PK'
      const isRar = (uint8[0] === 0x52 && uint8[1] === 0x61 && uint8[2] === 0x72 && uint8[3] === 0x21); // 'Rar!'
      const is7z = (uint8[0] === 0x37 && uint8[1] === 0x7A && uint8[2] === 0xBC && uint8[3] === 0xAF); // '7z'
      const isTar = this._checkIsTar(uint8);

      if (isZip || (typeof JSZip !== 'undefined' && !isRar && !is7z && !isTar)) {
        await this._parseZipArchive(arrayBuffer, fileName);
      } else if (isTar) {
        this._parseTarArchive(uint8, fileName);
      } else if (isRar || is7z) {
        this._renderProprietaryArchiveCard(isRar ? 'WinRAR (RAR)' : '7-Zip (7z)', uint8.length, fileName, downloadURL, fileId);
      } else {
        // Fallback thử với JSZip
        await this._parseZipArchive(arrayBuffer, fileName);
      }
    } catch (err) {
      console.warn('Lỗi phân tích tệp nén:', err);
      const statusText = document.getElementById('archive-status-text');
      if (statusText) statusText.textContent = '⚠️ ' + err.message;
      const body = document.getElementById('archive-content-body');
      if (body) {
        body.innerHTML = `
          <div style="padding: 30px; text-align: center; background: rgba(239, 68, 68, 0.08); border: 1px solid rgba(239, 68, 68, 0.2); border-radius: var(--radius-md, 10px);">
            <div style="font-size: 36px; margin-bottom: 12px;">⚠️</div>
            <div style="font-size: 14px; font-weight: 600; color: #ef4444; margin-bottom: 8px;">Không thể đọc trực tiếp danh sách tệp tin trong archive này</div>
            <div style="font-size: 12px; color: var(--text-muted, #8b949e); margin-bottom: 20px;">
              Tệp nén có thể được bảo vệ bằng mật khẩu (AES encrypted), sử dụng chuẩn nén độc quyền cao cấp hoặc tệp quá lớn.
            </div>
            <div style="display: flex; gap: 10px; justify-content: center;">
              <a href="${downloadURL}" class="btn btn-primary" download="${fileName}">⬇️ Tải tệp nén về máy</a>
            </div>
          </div>
        `;
      }
    }
  },

  // Kiểm tra định dạng Tar
  _checkIsTar(uint8) {
    if (uint8.length < 512) return false;
    const magic = String.fromCharCode(...uint8.subarray(257, 262));
    return magic === 'ustar';
  },

  // Phân tích tệp ZIP bằng thư viện JSZip
  async _parseZipArchive(arrayBuffer, fileName) {
    if (typeof JSZip === 'undefined') {
      throw new Error('Thư viện JSZip chưa được nạp.');
    }

    const zip = await JSZip.loadAsync(arrayBuffer);
    this.archiveState.zipInstance = zip;

    const entries = [];
    let totalUncompressed = 0;
    let totalCompressed = 0;
    let totalFiles = 0;
    let totalFolders = 0;

    zip.forEach((relativePath, file) => {
      const isDir = file.dir || relativePath.endsWith('/');
      const parts = relativePath.replace(/\/$/, '').split('/');
      const name = parts[parts.length - 1];
      const dirPath = parts.length > 1 ? parts.slice(0, -1).join('/') : '/';

      const uncompressed = file._data ? (file._data.uncompressedSize || 0) : 0;
      const compressed = file._data ? (file._data.compressedSize || 0) : 0;

      if (isDir) {
        totalFolders++;
      } else {
        totalFiles++;
        totalUncompressed += uncompressed;
        totalCompressed += compressed;
      }

      entries.push({
        path: relativePath,
        name: name,
        dirPath: dirPath,
        isDir: isDir,
        date: file.date || new Date(),
        uncompressedSize: uncompressed,
        compressedSize: compressed,
        zipEntry: file
      });
    });

    // Sắp xếp: Thư mục lên trước, sau đó xếp theo tên A-Z
    entries.sort((a, b) => {
      if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
      return a.path.localeCompare(b.path);
    });

    this.archiveState.entries = entries;
    this.archiveState.totalFiles = totalFiles;
    this.archiveState.totalFolders = totalFolders;
    this.archiveState.totalUncompressed = totalUncompressed;
    this.archiveState.totalCompressed = totalCompressed || arrayBuffer.byteLength;

    // Cập nhật giao diện thống kê
    const formatBytes = (typeof Utils !== 'undefined' && Utils.formatBytes) ? Utils.formatBytes : (b) => `${(b / (1024 * 1024)).toFixed(2)} MB`;

    document.getElementById('stat-files').textContent = totalFiles;
    document.getElementById('stat-folders').textContent = totalFolders;
    document.getElementById('stat-uncomp').textContent = formatBytes(totalUncompressed);

    const savedRatio = totalUncompressed > 0 ? Math.max(0, Math.round((1 - this.archiveState.totalCompressed / totalUncompressed) * 100)) : 0;
    document.getElementById('stat-comp').innerHTML = `${formatBytes(this.archiveState.totalCompressed)} <span class="archive-ratio-badge">Tiết kiệm ${savedRatio}%</span>`;

    const statusText = document.getElementById('archive-status-text');
    if (statusText) statusText.textContent = `✓ Đã quét thành công ${entries.length} mục (JSZip Zero-Unpack Engine)`;

    this._renderArchiveTable();
  },

  // Phân tích tệp TAR thuần (UStar header parser)
  _parseTarArchive(uint8, fileName) {
    const entries = [];
    let offset = 0;
    let totalUncompressed = 0;
    let totalFiles = 0;
    let totalFolders = 0;

    while (offset + 512 <= uint8.length) {
      const header = uint8.subarray(offset, offset + 512);
      // Kiểm tra block trống kết thúc
      let isEmpty = true;
      for (let i = 0; i < 512; i++) {
        if (header[i] !== 0) { isEmpty = false; break; }
      }
      if (isEmpty) break;

      // Đọc tên file (100 bytes)
      let fnEnd = 0;
      while (fnEnd < 100 && header[fnEnd] !== 0) fnEnd++;
      const fn = new TextDecoder('utf-8').decode(header.subarray(0, fnEnd));
      if (!fn) break;

      // Đọc kích thước file (12 bytes octal tại offset 124)
      const sizeStr = new TextDecoder('utf-8').decode(header.subarray(124, 136)).trim().replace(/\0.*$/, '');
      const fileSize = parseInt(sizeStr, 8) || 0;

      // Typeflag tại byte 156 ('0' hoặc '' = tệp, '5' = thư mục)
      const typeFlag = String.fromCharCode(header[156]);
      const isDir = (typeFlag === '5' || fn.endsWith('/'));

      // Modified time tại offset 136 (12 bytes octal)
      const mtimeStr = new TextDecoder('utf-8').decode(header.subarray(136, 148)).trim().replace(/\0.*$/, '');
      const mtime = new Date((parseInt(mtimeStr, 8) || 0) * 1000);

      const parts = fn.replace(/\/$/, '').split('/');
      const name = parts[parts.length - 1];
      const dirPath = parts.length > 1 ? parts.slice(0, -1).join('/') : '/';

      if (isDir) totalFolders++;
      else {
        totalFiles++;
        totalUncompressed += fileSize;
      }

      entries.push({
        path: fn,
        name: name,
        dirPath: dirPath,
        isDir: isDir,
        date: mtime,
        uncompressedSize: fileSize,
        compressedSize: fileSize,
        zipEntry: null
      });

      // Nhảy qua header (512 bytes) + data blocks (làm tròn lên bội số của 512)
      const blocks = Math.ceil(fileSize / 512);
      offset += 512 + (blocks * 512);
    }

    this.archiveState.entries = entries;
    this.archiveState.totalFiles = totalFiles;
    this.archiveState.totalFolders = totalFolders;
    this.archiveState.totalUncompressed = totalUncompressed;
    this.archiveState.totalCompressed = uint8.length;

    const formatBytes = (typeof Utils !== 'undefined' && Utils.formatBytes) ? Utils.formatBytes : (b) => `${(b / (1024 * 1024)).toFixed(2)} MB`;

    document.getElementById('stat-files').textContent = totalFiles;
    document.getElementById('stat-folders').textContent = totalFolders;
    document.getElementById('stat-uncomp').textContent = formatBytes(totalUncompressed);
    document.getElementById('stat-comp').textContent = formatBytes(uint8.length);

    const statusText = document.getElementById('archive-status-text');
    if (statusText) statusText.textContent = `✓ Đã phân tích ${entries.length} mục (TAR UStar Engine)`;

    this._renderArchiveTable();
  },

  // Hiển thị Card thông tin đối với các tệp nén chuyên dụng (RAR / 7z)
  _renderProprietaryArchiveCard(formatName, byteLength, fileName, downloadURL, fileId) {
    const formatBytes = (typeof Utils !== 'undefined' && Utils.formatBytes) ? Utils.formatBytes : (b) => `${(b / (1024 * 1024)).toFixed(2)} MB`;

    document.getElementById('stat-files').textContent = 'N/A';
    document.getElementById('stat-folders').textContent = 'N/A';
    document.getElementById('stat-uncomp').textContent = 'N/A';
    document.getElementById('stat-comp').textContent = formatBytes(byteLength);

    const statusText = document.getElementById('archive-status-text');
    if (statusText) statusText.textContent = `Định dạng ${formatName}`;

    const body = document.getElementById('archive-content-body');
    if (!body) return;

    body.innerHTML = `
      <div style="padding: 36px 20px; text-align: center; background: rgba(255, 255, 255, 0.02); border: 1px solid var(--border-subtle, #30363d); border-radius: var(--radius-md, 10px);">
        <div style="font-size: 52px; margin-bottom: 14px;">🗜️</div>
        <div style="font-size: 16px; font-weight: 700; color: #f1f5f9; margin-bottom: 8px;">Tệp Nén Chuyên Dụng ${formatName}</div>
        <div style="font-size: 13px; color: var(--text-muted, #8b949e); max-width: 520px; margin: 0 auto 20px auto; line-height: 1.6;">
          Tệp <strong>${fileName}</strong> (${formatBytes(byteLength)}) được nén theo thuật toán độc quyền của ${formatName}. Để bảo đảm tính toàn vẹn dữ liệu, vui lòng tải về máy tính và giải nén bằng WinRAR hoặc 7-Zip.
        </div>
        <div style="display: flex; gap: 10px; justify-content: center; flex-wrap: wrap;">
          ${fileId ? `<button class="btn btn-secondary" onclick="FilesManager.showChunkMap('${fileId}', '${fileName}')">🔍 Xem bản đồ Chunks</button>` : ''}
          <a href="${downloadURL}" class="btn btn-primary" download="${fileName}">⬇️ Tải tệp nén về máy</a>
        </div>
      </div>
    `;
  },

  // Render bảng danh sách các tệp con trong Archive
  _renderArchiveTable() {
    const body = document.getElementById('archive-content-body');
    if (!body) return;

    body.innerHTML = `
      <!-- Toolbar Filter & Search -->
      <div class="archive-toolbar">
        <div class="archive-search-box">
          <svg class="archive-search-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="8"></circle><line x1="21" y1="21" x2="16.65" y2="16.65"></line></svg>
          <input type="text" id="archive-search-input" class="archive-search-input" placeholder="🔍 Lọc tệp tin trong archive (nhập tên)...">
        </div>
        <div class="archive-filter-btn-group">
          <button class="archive-filter-btn active" data-type="all" id="af-btn-all">Tất cả (${this.archiveState.entries.length})</button>
          <button class="archive-filter-btn" data-type="file" id="af-btn-file">Tệp (${this.archiveState.totalFiles})</button>
          <button class="archive-filter-btn" data-type="folder" id="af-btn-folder">Thư mục (${this.archiveState.totalFolders})</button>
        </div>
      </div>

      <!-- Table Wrapper -->
      <div class="archive-table-wrapper">
        <table class="archive-table">
          <thead>
            <tr>
              <th style="min-width: 200px;">Tên tệp tin / Thư mục</th>
              <th>Thư mục cha</th>
              <th>Kích thước gốc</th>
              <th>Kích thước nén</th>
              <th>Ngày sửa đổi</th>
              <th style="text-align: right; min-width: 140px;">Hành động</th>
            </tr>
          </thead>
          <tbody id="archive-tbody"></tbody>
        </table>
      </div>
    `;

    // Gắn sự kiện Search & Filter
    const searchInput = document.getElementById('archive-search-input');
    searchInput?.addEventListener('input', (e) => {
      this.archiveState.filterQuery = e.target.value.toLowerCase().trim();
      this._filterAndRenderRows();
    });

    document.querySelectorAll('.archive-filter-btn').forEach(btn => {
      btn.addEventListener('click', () => {
        document.querySelectorAll('.archive-filter-btn').forEach(b => b.classList.remove('active'));
        btn.classList.add('active');
        this.archiveState.filterType = btn.getAttribute('data-type') || 'all';
        this._filterAndRenderRows();
      });
    });

    this._filterAndRenderRows();
  },

  // Lọc và hiển thị các dòng trong bảng
  _filterAndRenderRows() {
    const tbody = document.getElementById('archive-tbody');
    if (!tbody) return;

    const { entries, filterQuery, filterType } = this.archiveState;
    const formatBytes = (typeof Utils !== 'undefined' && Utils.formatBytes) ? Utils.formatBytes : (b) => `${b} B`;
    const formatDate = (typeof Utils !== 'undefined' && Utils.formatDate) ? Utils.formatDate : (d) => d instanceof Date ? d.toLocaleString() : '-';

    const filtered = entries.filter(e => {
      if (filterType === 'file' && e.isDir) return false;
      if (filterType === 'folder' && !e.isDir) return false;
      if (filterQuery && !e.path.toLowerCase().includes(filterQuery)) return false;
      return true;
    });

    if (filtered.length === 0) {
      tbody.innerHTML = `
        <tr>
          <td colspan="6" style="text-align: center; padding: 30px; color: var(--text-muted, #8b949e);">
            Không tìm thấy tệp hoặc thư mục nào khớp với điều kiện lọc.
          </td>
        </tr>
      `;
      return;
    }

    let html = '';
    filtered.slice(0, 500).forEach((item, idx) => {
      const ext = item.name.toLowerCase().split('.').pop();
      const isImg = !item.isDir && this.isImageFile(item.name);
      const isCode = !item.isDir && (typeof PreviewManager !== 'undefined' && PreviewManager.isTextOrCode) ? PreviewManager.isTextOrCode(item.name) : false;

      let icon = item.isDir ? '📁' : '📄';
      if (isImg) icon = '🖼️';
      else if (['mp4', 'mkv', 'avi', 'mov'].includes(ext)) icon = '🎬';
      else if (['mp3', 'flac', 'wav', 'aac'].includes(ext)) icon = '🎵';
      else if (['zip', 'rar', '7z', 'tar', 'gz'].includes(ext)) icon = '📦';
      else if (['pdf'].includes(ext)) icon = '📕';
      else if (['xls', 'xlsx', 'csv'].includes(ext)) icon = '📊';

      const uncomp = item.isDir ? '-' : formatBytes(item.uncompressedSize);
      const comp = item.isDir ? '-' : formatBytes(item.compressedSize);
      const dateText = item.date ? formatDate(item.date) : '-';

      // Nút hành động
      let actionButtons = '';
      if (!item.isDir && item.zipEntry) {
        // Nút Trích xuất lẻ trực tiếp từ zip
        actionButtons += `
          <button class="archive-action-btn-sm" onclick="ImageArchiveViewer.extractSingleFile('${encodeURIComponent(item.path)}', '${encodeURIComponent(item.name)}')" title="Tải riêng tệp này về máy">
            ⬇️ Trích xuất
          </button>
        `;

        // Nút Xem nhanh ảnh
        if (isImg) {
          actionButtons += `
            <button class="archive-action-btn-sm" style="color: #34d399; border-color: rgba(52, 211, 153, 0.3);" onclick="ImageArchiveViewer.quickViewImageFromArchive('${encodeURIComponent(item.path)}', '${encodeURIComponent(item.name)}')" title="Xem ảnh trực tiếp trên Lightbox">
              👁️ Xem
            </button>
          `;
        }
        // Nút Xem nhanh code
        if (isCode) {
          actionButtons += `
            <button class="archive-action-btn-sm" style="color: #38bdf8; border-color: rgba(56, 189, 248, 0.3);" onclick="ImageArchiveViewer.quickViewTextFromArchive('${encodeURIComponent(item.path)}', '${encodeURIComponent(item.name)}')" title="Đọc nội dung văn bản">
              📄 Đọc
            </button>
          `;
        }
      }

      html += `
        <tr>
          <td>
            <div class="archive-file-cell">
              <span class="archive-file-icon">${icon}</span>
              <span style="font-weight: ${item.isDir ? '600' : '400'}; color: ${item.isDir ? '#38bdf8' : '#f1f5f9'};">${item.name}</span>
            </div>
          </td>
          <td style="color: var(--text-muted, #8b949e);">${item.dirPath}</td>
          <td>${uncomp}</td>
          <td>${comp}</td>
          <td style="color: var(--text-muted, #8b949e); font-size: 11px;">${dateText}</td>
          <td style="text-align: right;">
            <div style="display: flex; gap: 4px; justify-content: flex-end;">${actionButtons}</div>
          </td>
        </tr>
      `;
    });

    if (filtered.length > 500) {
      html += `
        <tr>
          <td colspan="6" style="text-align: center; padding: 10px; font-size: 11px; color: var(--text-muted, #8b949e); background: rgba(255,255,255,0.02);">
            Hiển thị 500 / ${filtered.length} mục đầu tiên. Sử dụng ô tìm kiếm để lọc chính xác tệp bạn cần.
          </td>
        </tr>
      `;
    }

    tbody.innerHTML = html;
  },

  // Trích xuất 1 tệp con lẻ trực tiếp từ bộ nhớ Zip mà không cần giải nén cả archive
  async extractSingleFile(encodedPath, encodedName) {
    const path = decodeURIComponent(encodedPath);
    const name = decodeURIComponent(encodedName);

    if (!this.archiveState.zipInstance) {
      if (typeof Toast !== 'undefined') Toast.error('Không tìm thấy thể hiện zip.');
      return;
    }

    try {
      if (typeof Toast !== 'undefined') Toast.info(`Đang trích xuất "${name}" từ tệp nén...`);
      const file = this.archiveState.zipInstance.file(path);
      if (!file) throw new Error('Không tìm thấy tệp trong tệp nén.');

      const blob = await file.async('blob');
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = name;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);

      setTimeout(() => URL.revokeObjectURL(url), 10000);
      if (typeof Toast !== 'undefined') Toast.success(`Đã trích xuất và tải về "${name}" thành công!`);
    } catch (err) {
      if (typeof Toast !== 'undefined') Toast.error(`Lỗi trích xuất tệp: ${err.message}`);
    }
  },

  // Xem nhanh ảnh bên trong tệp nén trực tiếp qua Lightbox Image Viewer
  async quickViewImageFromArchive(encodedPath, encodedName) {
    const path = decodeURIComponent(encodedPath);
    const name = decodeURIComponent(encodedName);

    if (!this.archiveState.zipInstance) return;

    try {
      if (typeof Toast !== 'undefined') Toast.info(`Đang nạp ảnh "${name}" từ bộ nhớ tệp nén...`);
      const file = this.archiveState.zipInstance.file(path);
      if (!file) throw new Error('Không tìm thấy tệp ảnh trong zip');

      const blob = await file.async('blob');
      const blobUrl = URL.createObjectURL(blob);

      // Mở Lightbox với Blob URL
      this.openImageViewer(
        'archive-entry-' + path,
        name + ` (Trong: ${this.archiveState.currentFileName})`,
        blob.type || 'image/png',
        blobUrl,
        blobUrl,
        blobUrl,
        [{ id: 'archive-entry-' + path, name: name, mime_type: blob.type }]
      );
    } catch (err) {
      if (typeof Toast !== 'undefined') Toast.error(`Không thể xem ảnh: ${err.message}`);
    }
  },

  // Xem nhanh văn bản / mã nguồn bên trong tệp nén qua Code Viewer
  async quickViewTextFromArchive(encodedPath, encodedName) {
    const path = decodeURIComponent(encodedPath);
    const name = decodeURIComponent(encodedName);

    if (!this.archiveState.zipInstance) return;

    try {
      if (typeof Toast !== 'undefined') Toast.info(`Đang trích xuất văn bản "${name}"...`);
      const file = this.archiveState.zipInstance.file(path);
      if (!file) throw new Error('Không tìm thấy tệp văn bản trong zip');

      const text = await file.async('string');

      const modal = document.getElementById('modal-code-viewer');
      const titleEl = document.getElementById('code-viewer-title');
      const infoEl = document.getElementById('code-viewer-info');
      const container = document.getElementById('code-viewer-container');
      const footerMeta = document.getElementById('code-viewer-footer-meta');

      if (!modal || !container) {
        alert(text.slice(0, 1000));
        return;
      }

      titleEl.textContent = name + ` (Trong: ${this.archiveState.currentFileName})`;
      const lines = text.split('\n');
      infoEl.textContent = `${lines.length} dòng • ${(typeof Utils !== 'undefined' && Utils.formatBytes) ? Utils.formatBytes(text.length) : text.length + ' B'}`;
      if (footerMeta) footerMeta.textContent = `Định dạng: ${name.split('.').pop().toUpperCase()} • Trích xuất trực tiếp từ ZIP`;

      let formattedHtml = '';
      lines.slice(0, 1500).forEach((line, idx) => {
        const lineNum = idx + 1;
        const escaped = line.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
        formattedHtml += `<div style="display: flex; min-height: 19px;"><span style="user-select: none; width: 45px; text-align: right; margin-right: 16px; color: #475569; font-size: 11px;">${lineNum}</span><span style="flex: 1; white-space: pre-wrap; word-break: break-all;">${escaped || ' '}</span></div>`;
      });

      if (lines.length > 1500) {
        formattedHtml += `<div style="padding: 10px; color: var(--accent-amber); font-size: 11px;">(Đã rút gọn hiển thị 1500 dòng đầu tiên)</div>`;
      }

      container.innerHTML = formattedHtml || '<span style="color: #64748b;">(Tệp trống)</span>';
      if (typeof PreviewManager !== 'undefined') {
        PreviewManager.currentCodeText = text;
      }

      modal.classList.add('active');
    } catch (err) {
      if (typeof Toast !== 'undefined') Toast.error(`Không thể đọc văn bản: ${err.message}`);
    }
  }
};

// Đăng ký toàn cục
window.ImageArchiveViewer = ImageArchiveViewer;

// Tự động khởi tạo sau khi DOM sẵn sàng
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', () => ImageArchiveViewer.init());
} else {
  ImageArchiveViewer.init();
}
