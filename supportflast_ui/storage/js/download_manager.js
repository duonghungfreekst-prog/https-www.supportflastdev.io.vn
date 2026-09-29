/**
 * SupportFlast CloudPool - Quản Lý Tải Xuống Đa Luồng (DownloadManager)
 * ---------------------------------------------------------------------
 * Module chuyên trách quản lý và tối ưu hóa việc tải dữ liệu từ CloudPool:
 * 1. Tải hàng loạt (Batch Download / Zip Download): Nén và tải trọn gói nhiều tệp và thư mục qua /api/files/download-zip.
 * 2. Tải trực tiếp tối ưu hóa: Hỗ trợ cả Blob Stream (có theo dõi % tiến trình, MB/s) và Fast Anchor (tải trực tiếp không tốn RAM).
 * 3. Hàng đợi đa luồng (Concurrent Queue): Điều phối tải song song có kiểm soát luồng (Worker Pool) chống nghẽn trình duyệt.
 * 4. Tự động xác thực: Gắn JWT Token và tự động truyền mã OTP 1 lần cho các tệp bảo mật cấp cao.
 * 5. Giám sát trực quan: Bảng điều khiển tải xuống nổi (Floating Drawer) và hệ thống thông báo Toast theo thời gian thực.
 */

const DownloadManager = {
  // Cấu hình hoạt động
  config: {
    maxConcurrent: 3,               // Số luồng tải đồng thời tối đa (Worker pool pattern)
    blobSizeThreshold: 80 * 1024 * 1024, // Tệp > 80MB chuyển sang Fast Anchor để tránh chiếm dụng heap RAM
    autoCloseDelayMs: 6000,         // Tự thu gọn drawer sau 6s khi tất cả tác vụ hoàn tất
  },

  // Quản lý trạng thái hàng đợi và bộ nhớ đệm
  tasks: [],                        // Danh sách tác vụ [{ id, name, size, type, status, progress, speed, loaded, total, ... }]
  activeCount: 0,                   // Số lượng luồng tải đang chạy thực tế
  otpCache: {},                     // Bộ nhớ đệm OTP phiên làm việc { fileId: otpCode }
  isDrawerOpen: false,              // Trạng thái hiển thị của Floating Drawer

  /**
   * Khởi tạo DownloadManager và gắn kết giao diện
   */
  init() {
    this.injectStyles();
    this.createDownloadDrawerUI();
    this.bindEvents();
    console.log('[DownloadManager] Đã khởi tạo thành công Module Quản Lý Tải Xuống Đa Luồng.');
  },

  /**
   * Thêm mã CSS chuyên dụng cho Floating Download Drawer và ProgressBar
   */
  injectStyles() {
    if (document.getElementById('download-manager-styles')) return;
    const style = document.createElement('style');
    style.id = 'download-manager-styles';
    style.textContent = `
      /* Floating Download Widget Container */
      .dl-floating-container {
        position: fixed;
        bottom: 24px;
        right: 24px;
        z-index: 9990;
        display: flex;
        flex-direction: column;
        align-items: flex-end;
        font-family: inherit;
        pointer-events: none;
      }
      .dl-floating-container * {
        pointer-events: auto;
      }

      /* Nút kích hoạt nổi (Badge & Icon) */
      .dl-trigger-btn {
        background: linear-gradient(135deg, #0284c7, #0369a1);
        color: #ffffff;
        border: 1px solid rgba(255, 255, 255, 0.2);
        border-radius: 999px;
        padding: 8px 16px;
        display: none;
        align-items: center;
        gap: 8px;
        cursor: pointer;
        box-shadow: 0 8px 24px rgba(2, 132, 199, 0.45);
        font-size: 12px;
        font-weight: 600;
        transition: all 0.25s cubic-bezier(0.4, 0, 0.2, 1);
        user-select: none;
      }
      .dl-trigger-btn:hover {
        transform: translateY(-2px);
        box-shadow: 0 12px 28px rgba(2, 132, 199, 0.6);
        background: linear-gradient(135deg, #0ea5e9, #0284c7);
      }
      .dl-trigger-btn.active {
        display: flex;
      }
      .dl-trigger-icon {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        width: 20px;
        height: 20px;
      }
      .dl-trigger-icon.spinning svg {
        animation: dl-spin 1.5s linear infinite;
      }
      @keyframes dl-spin {
        from { transform: rotate(0deg); }
        to { transform: rotate(360deg); }
      }
      .dl-badge {
        background: #ef4444;
        color: #ffffff;
        font-size: 11px;
        font-weight: 700;
        padding: 2px 7px;
        border-radius: 999px;
        min-width: 18px;
        text-align: center;
      }

      /* Hộp danh sách tải nổi (Download Drawer Panel) */
      .dl-panel {
        width: 360px;
        max-width: calc(100vw - 32px);
        background: var(--bg-secondary, #182234);
        border: 1px solid var(--border-strong, rgba(255, 255, 255, 0.15));
        border-radius: 12px;
        box-shadow: 0 16px 40px rgba(0, 0, 0, 0.5);
        overflow: hidden;
        margin-bottom: 12px;
        display: none;
        flex-direction: column;
        animation: dl-slide-up 0.25s cubic-bezier(0.16, 1, 0.3, 1);
        backdrop-filter: blur(12px);
      }
      .dl-panel.active {
        display: flex;
      }
      @keyframes dl-slide-up {
        from { opacity: 0; transform: translateY(16px) scale(0.96); }
        to { opacity: 1; transform: translateY(0) scale(1); }
      }

      .dl-panel-header {
        padding: 12px 16px;
        background: rgba(255, 255, 255, 0.03);
        border-bottom: 1px solid var(--border-subtle, rgba(255, 255, 255, 0.08));
        display: flex;
        align-items: center;
        justify-content: space-between;
      }
      .dl-panel-title {
        display: flex;
        align-items: center;
        gap: 8px;
        font-size: 13px;
        font-weight: 700;
        color: var(--text-primary, #ffffff);
      }
      .dl-panel-actions {
        display: flex;
        align-items: center;
        gap: 6px;
      }
      .dl-panel-btn-icon {
        background: transparent;
        border: none;
        color: var(--text-muted, #94a3b8);
        cursor: pointer;
        padding: 4px;
        border-radius: 4px;
        display: flex;
        align-items: center;
        justify-content: center;
        transition: all 0.15s ease;
      }
      .dl-panel-btn-icon:hover {
        background: rgba(255, 255, 255, 0.1);
        color: var(--text-primary, #ffffff);
      }

      .dl-panel-body {
        max-height: 320px;
        overflow-y: auto;
        padding: 8px 12px;
        display: flex;
        flex-direction: column;
        gap: 8px;
      }

      /* Từng mục tải trong danh sách */
      .dl-item {
        background: rgba(255, 255, 255, 0.02);
        border: 1px solid var(--border-subtle, rgba(255, 255, 255, 0.06));
        border-radius: 8px;
        padding: 10px;
        display: flex;
        flex-direction: column;
        gap: 6px;
        transition: background 0.2s;
      }
      .dl-item:hover {
        background: rgba(255, 255, 255, 0.04);
      }
      .dl-item-top {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: 8px;
      }
      .dl-item-info {
        display: flex;
        align-items: center;
        gap: 8px;
        min-width: 0;
        flex: 1;
      }
      .dl-item-icon {
        font-size: 16px;
        flex-shrink: 0;
      }
      .dl-item-name {
        font-size: 12px;
        font-weight: 600;
        color: var(--text-primary, #ffffff);
        white-space: nowrap;
        overflow: hidden;
        text-overflow: ellipsis;
      }
      .dl-item-actions {
        display: flex;
        align-items: center;
        gap: 4px;
        flex-shrink: 0;
      }
      .dl-item-btn {
        background: transparent;
        border: none;
        color: var(--text-muted, #94a3b8);
        cursor: pointer;
        padding: 2px;
        border-radius: 4px;
        display: flex;
        align-items: center;
        justify-content: center;
        transition: color 0.15s;
      }
      .dl-item-btn:hover {
        color: #ef4444;
      }

      .dl-progress-track {
        width: 100%;
        height: 5px;
        background: rgba(255, 255, 255, 0.1);
        border-radius: 999px;
        overflow: hidden;
      }
      .dl-progress-bar {
        height: 100%;
        width: 0%;
        background: linear-gradient(90deg, #38bdf8, #0ea5e9);
        border-radius: 999px;
        transition: width 0.15s ease-out;
      }
      .dl-progress-bar.completed {
        background: #22c55e;
      }
      .dl-progress-bar.failed {
        background: #ef4444;
      }

      .dl-item-meta {
        display: flex;
        align-items: center;
        justify-content: space-between;
        font-size: 10px;
        color: var(--text-muted, #94a3b8);
      }
      .dl-item-status-text {
        font-weight: 500;
      }
      .dl-item-status-text.completed { color: #4ade80; }
      .dl-item-status-text.failed { color: #f87171; }
      .dl-item-status-text.downloading { color: #38bdf8; }

      /* Responsive cho thiết bị di động */
      @media (max-width: 640px) {
        .dl-floating-container {
          bottom: 74px;
          right: 12px;
        }
        .dl-panel {
          width: calc(100vw - 24px);
        }
      }
    `;
    document.head.appendChild(style);
  },

  /**
   * Tạo cấu trúc HTML cho Floating Download Drawer
   */
  createDownloadDrawerUI() {
    if (document.getElementById('download-floating-container')) return;

    const container = document.createElement('div');
    container.id = 'download-floating-container';
    container.className = 'dl-floating-container';

    container.innerHTML = `
      <!-- Panel chi tiết tiến trình tải -->
      <div id="dl-panel" class="dl-panel">
        <div class="dl-panel-header">
          <div class="dl-panel-title">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"></path><polyline points="7 10 12 15 17 10"></polyline><line x1="12" y1="15" x2="12" y2="3"></line></svg>
            <span>Tải Xuống Đa Luồng</span>
            <span id="dl-panel-active-badge" class="badge badge-info" style="font-size: 10px; padding: 1px 6px;">0 đang tải</span>
          </div>
          <div class="dl-panel-actions">
            <button id="dl-btn-clear-completed" class="dl-panel-btn-icon" title="Dọn dẹp các tệp đã hoàn tất">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"></polyline><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path></svg>
            </button>
            <button id="dl-btn-minimize" class="dl-panel-btn-icon" title="Thu nhỏ">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg>
            </button>
          </div>
        </div>
        <div id="dl-panel-body" class="dl-panel-body">
          <!-- Dynamically populated download tasks -->
        </div>
      </div>

      <!-- Nút kích hoạt thu nhỏ/mở rộng nổi -->
      <button id="dl-trigger-btn" class="dl-trigger-btn" title="Xem tiến trình tải xuống">
        <span class="dl-trigger-icon spinning">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"></path><polyline points="7 10 12 15 17 10"></polyline><line x1="12" y1="15" x2="12" y2="3"></line></svg>
        </span>
        <span id="dl-trigger-label">Tải xuống</span>
        <span id="dl-trigger-badge" class="dl-badge">0</span>
      </button>
    `;

    document.body.appendChild(container);
  },

  /**
   * Đăng ký sự kiện tương tác giao diện
   */
  bindEvents() {
    const triggerBtn = document.getElementById('dl-trigger-btn');
    const minimizeBtn = document.getElementById('dl-btn-minimize');
    const clearBtn = document.getElementById('dl-btn-clear-completed');

    if (triggerBtn) {
      triggerBtn.addEventListener('click', () => {
        this.toggleDrawer();
      });
    }

    if (minimizeBtn) {
      minimizeBtn.addEventListener('click', () => {
        this.closeDrawer();
      });
    }

    if (clearBtn) {
      clearBtn.addEventListener('click', () => {
        this.clearCompletedTasks();
      });
    }
  },

  toggleDrawer() {
    if (this.isDrawerOpen) {
      this.closeDrawer();
    } else {
      this.openDrawer();
    }
  },

  openDrawer() {
    const panel = document.getElementById('dl-panel');
    if (panel) {
      panel.classList.add('active');
      this.isDrawerOpen = true;
    }
  },

  closeDrawer() {
    const panel = document.getElementById('dl-panel');
    if (panel) {
      panel.classList.remove('active');
      this.isDrawerOpen = false;
    }
  },

  // =========================================================================
  // 1. TẢI HÀNG LOẠT (BATCH / ZIP DOWNLOAD QUA /api/files/download-zip)
  // =========================================================================

  /**
   * Tải trọn gói một hoặc nhiều tệp/thư mục thành tệp nén ZIP duy nhất
   * @param {Array<string>} selectedIds - Danh sách file ID hoặc folder ID
   * @param {string} customName - Tên tùy chọn cho tệp zip (mặc định lấy theo tên folder hoặc ngày giờ)
   */
  async downloadZip(selectedIds, customName = '') {
    if (!selectedIds || selectedIds.length === 0) {
      Toast.warning('⚠️ Vui lòng chọn ít nhất một tệp tin hoặc thư mục để tải trọn gói Zip!');
      return;
    }

    const ids = Array.from(selectedIds);
    Toast.info(`📦 Đang chuẩn bị gói nén Zip cho ${ids.length} mục đã chọn...`);

    // Thu thập token và OTP xác thực tự động
    const token = (typeof API !== 'undefined' && API.getToken) ? API.getToken() : (localStorage.getItem('cloudpool_jwt_token') || '');
    let otpParam = '';

    // Kiểm tra xem có OTP trong cache cho các mục đã chọn không
    for (const id of ids) {
      if (this.otpCache[id]) {
        otpParam = this.otpCache[id];
        break;
      }
    }

    // Xác định tên tệp ZIP dự kiến
    let zipName = customName;
    if (!zipName) {
      if (ids.length === 1 && typeof FilesManager !== 'undefined' && FilesManager.files) {
        const item = FilesManager.files.find(f => f.id === ids[0]);
        if (item) {
          zipName = `${item.name}.zip`;
        }
      }
      if (!zipName) {
        const dateStr = new Date().toISOString().replace(/[-:T.]/g, '').slice(0, 14);
        zipName = `CloudPool_Archive_${dateStr}.zip`;
      }
    }
    if (!zipName.toLowerCase().endsWith('.zip')) {
      zipName += '.zip';
    }

    // Xây dựng URL kết nối tới endpoint /api/files/download-zip
    const params = new URLSearchParams();
    params.set('ids', ids.join(','));
    params.set('name', zipName);
    if (token) params.set('token', token);
    if (otpParam) params.set('otp', otpParam);

    const downloadZipUrl = `/api/files/download-zip?${params.toString()}`;

    // Tạo tác vụ trong DownloadManager
    const task = this.createTask({
      name: zipName,
      size: 0,
      type: 'zip',
      url: downloadZipUrl,
      method: 'anchor', // Nén zip phía server được stream trực tiếp qua Fast Anchor để chống đầy RAM
      token,
      otp: otpParam,
    });

    this.tasks.unshift(task);
    this.openDrawer();
    this.renderUI();

    try {
      task.status = 'downloading';
      task.progress = 100;
      this.renderUI();

      // Sử dụng Fast Anchor Download tối ưu hóa cho trình duyệt
      this.downloadViaAnchor(downloadZipUrl, zipName);

      task.status = 'completed';
      Toast.success(`✅ Đã bắt đầu tải trọn gói Zip: ${zipName}`);
      this.renderUI();

      // Nếu đang chọn nhiều mục trên UI, tự động bỏ chọn sau khi tải xong
      if (typeof FilesManager !== 'undefined' && FilesManager.selectedIds) {
        FilesManager.selectedIds.clear();
        FilesManager.updateBulkActionBar();
        FilesManager.renderFiles();
      }
    } catch (err) {
      task.status = 'failed';
      task.error = err.message;
      Toast.error(`❌ Lỗi khi tải gói Zip: ${err.message}`);
      this.renderUI();
    }
  },

  // =========================================================================
  // 2. TẢI TỆP TIN ĐƠN LẺ VÀ ĐIỀU PHỐI ĐA LUỒNG
  // =========================================================================

  /**
   * Tải một tệp tin với cơ chế kiểm tra bảo mật OTP và tối ưu hóa tải
   * @param {string|object} fileOrId - ID của file hoặc đối tượng VirtualFile
   * @param {object} options - Tùy chọn tải { method: 'auto'|'stream'|'anchor', otp: string }
   */
  async downloadFile(fileOrId, options = {}) {
    let file = null;

    if (typeof fileOrId === 'object' && fileOrId !== null) {
      file = fileOrId;
    } else if (typeof fileOrId === 'string') {
      if (typeof FilesManager !== 'undefined' && FilesManager.files) {
        file = FilesManager.files.find(f => f.id === fileOrId);
      }
    }

    if (!file && typeof fileOrId === 'string') {
      file = { id: fileOrId, name: 'tep_tin_tai_ve', size_bytes: 0, is_dir: false };
    }

    if (!file) {
      Toast.error('Không tìm thấy thông tin tệp tin cần tải');
      return;
    }

    // Nếu là thư mục: chuyển sang tải trọn gói Zip của thư mục
    if (file.is_dir) {
      return this.downloadZip([file.id], `${file.name}.zip`);
    }

    // Kiểm tra quyền truy cập và mã OTP cho tài khoản thành viên
    const currentUser = (typeof AuthManager !== 'undefined' && AuthManager.currentUser) ? AuthManager.currentUser : null;
    const isMember = currentUser && currentUser.role === 'member';

    if (isMember && (file.is_admin_owned || file.requires_otp)) {
      // Nếu chưa có OTP trong cache
      if (!this.otpCache[file.id] && !options.otp) {
        if (typeof FilesManager !== 'undefined' && FilesManager.openOTPModal) {
          Toast.info('🔒 Tệp tin yêu cầu mã OTP Admin 1 lần để tải xuống.');
          FilesManager.openOTPModal(file, 'download');
          return;
        }
      }
    }

    const otp = options.otp || this.otpCache[file.id] || '';
    const token = (typeof API !== 'undefined' && API.getToken) ? API.getToken() : (localStorage.getItem('cloudpool_jwt_token') || '');

    // Xây dựng URL tải file
    const params = new URLSearchParams();
    params.set('id', file.id);
    if (token) params.set('token', token);
    if (otp) params.set('otp', otp);

    const downloadUrl = `/api/files/download?${params.toString()}`;

    // Xác định phương thức tải: Stream (hiển thị % progress) hoặc Anchor (tối ưu bộ nhớ cho tệp lớn)
    let method = options.method || this.config.defaultMethod;
    if (method === 'auto') {
      method = (file.size_bytes && file.size_bytes > this.config.blobSizeThreshold) ? 'anchor' : 'stream';
    }

    const task = this.createTask({
      fileId: file.id,
      name: file.name,
      size: file.size_bytes || 0,
      mimeType: file.mime_type || 'application/octet-stream',
      type: 'file',
      url: downloadUrl,
      method,
      token,
      otp,
    });

    this.tasks.unshift(task);
    this.openDrawer();
    this.renderUI();
    Toast.info(`⏳ Bắt đầu tải: ${file.name}`);

    // Đưa vào hàng đợi đa luồng
    this.processQueue();
  },

  /**
   * Tải hàng loạt nhiều tệp riêng lẻ vào hàng đợi đa luồng
   * @param {Array<string|object>} files - Danh sách file hoặc file ID
   */
  downloadBatchFiles(files) {
    if (!files || files.length === 0) return;
    Toast.info(`🚀 Đã thêm ${files.length} tệp vào hàng đợi tải xuống đa luồng...`);
    for (const f of files) {
      this.downloadFile(f);
    }
  },

  /**
   * Tạo đối tượng Task chuẩn hóa
   */
  createTask(data) {
    return {
      id: 'dl_' + Date.now() + '_' + Math.random().toString(36).substring(2, 8),
      fileId: data.fileId || '',
      name: data.name || 'untilted_file',
      size: data.size || 0,
      mimeType: data.mimeType || 'application/octet-stream',
      type: data.type || 'file',
      url: data.url,
      method: data.method || 'stream',
      token: data.token || '',
      otp: data.otp || '',
      status: 'pending',          // 'pending' | 'downloading' | 'completed' | 'failed' | 'cancelled'
      progress: 0,
      loaded: 0,
      total: data.size || 0,
      speed: 0,                   // Bytes per second
      error: null,
      controller: new AbortController(),
      startTime: null,
    };
  },

  /**
   * Bộ điều phối hàng đợi đa luồng (Worker Pool Pattern)
   */
  async processQueue() {
    if (this.activeCount >= this.config.maxConcurrent) {
      return;
    }

    const nextTask = this.tasks.find(t => t.status === 'pending');
    if (!nextTask) {
      return;
    }

    this.activeCount++;
    this.executeTask(nextTask).finally(() => {
      this.activeCount--;
      this.renderUI();
      this.processQueue(); // Kích hoạt luồng tiếp theo
    });

    this.renderUI();
  },

  /**
   * Thực thi một tác vụ tải xuống
   */
  async executeTask(task) {
    task.status = 'downloading';
    task.startTime = Date.now();
    this.renderUI();

    try {
      if (task.method === 'anchor') {
        // Phương thức Fast Anchor Link: Trình duyệt tự tải về ổ cứng, không tiêu hao RAM
        this.downloadViaAnchor(task.url, task.name);
        task.progress = 100;
        task.status = 'completed';
        Toast.success(`✅ Đã tải xuống thành công: ${task.name}`);
      } else {
        // Phương thức Blob Stream: Tải qua ReadableStream, đo lường tốc độ và % chính xác
        await this.downloadViaBlobStream(task);
        task.status = 'completed';
        Toast.success(`✅ Đã tải hoàn tất: ${task.name}`);
      }
    } catch (err) {
      if (err.name === 'AbortError') {
        task.status = 'cancelled';
        Toast.warning(`Đã hủy tải: ${task.name}`);
      } else {
        task.status = 'failed';
        task.error = err.message;
        Toast.error(`❌ Tải thất bại (${task.name}): ${err.message}`);
      }
    }

    this.renderUI();
  },

  /**
   * Tải tệp trực tiếp qua Blob Stream có thanh tiến trình % và đo tốc độ MB/s
   */
  async downloadViaBlobStream(task) {
    const headers = {
      'ngrok-skip-browser-warning': 'true',
    };
    if (task.token) {
      headers['Authorization'] = `Bearer ${task.token}`;
    }
    if (task.otp) {
      headers['X-File-OTP'] = task.otp;
    }

    const response = await fetch(task.url, {
      method: 'GET',
      headers,
      signal: task.controller.signal,
    });

    if (!response.ok) {
      let errMsg = `HTTP Error ${response.status}`;
      try {
        const errJson = await response.json();
        if (errJson && errJson.error) errMsg = errJson.error;
      } catch (_) {}
      throw new Error(errMsg);
    }

    const contentLength = response.headers.get('Content-Length');
    const totalBytes = contentLength ? parseInt(contentLength, 10) : task.size;
    task.total = totalBytes;

    const reader = response.body.getReader();
    const chunks = [];
    let receivedBytes = 0;
    let lastTime = Date.now();
    let lastLoaded = 0;

    while (true) {
      const { done, value } = await reader.read();
      if (done) break;

      chunks.push(value);
      receivedBytes += value.length;
      task.loaded = receivedBytes;

      const now = Date.now();
      const timeDiff = (now - lastTime) / 1000;
      if (timeDiff >= 0.25 || receivedBytes === totalBytes) {
        if (totalBytes > 0) {
          task.progress = Math.min(99, Math.round((receivedBytes / totalBytes) * 100));
        }
        task.speed = (receivedBytes - lastLoaded) / (timeDiff || 1);
        lastTime = now;
        lastLoaded = receivedBytes;
        this.renderUI();
      }
    }

    // Hoàn tất việc đọc Stream: Tạo Blob và kích hoạt lưu file
    task.progress = 100;
    const blob = new Blob(chunks, { type: task.mimeType || 'application/octet-stream' });
    const blobUrl = URL.createObjectURL(blob);

    this.downloadViaAnchor(blobUrl, task.name);

    // Thu hồi ObjectURL sau 60 giây để giải phóng RAM triệt để (Chống rò rỉ bộ nhớ)
    setTimeout(() => {
      URL.revokeObjectURL(blobUrl);
    }, 60000);
  },

  /**
   * Tải tệp siêu tốc qua thẻ Anchor ảo (Direct Browser Download)
   */
  downloadViaAnchor(url, fileName) {
    const a = document.createElement('a');
    a.style.display = 'none';
    a.href = url;
    a.download = fileName || 'download';
    document.body.appendChild(a);
    a.click();
    setTimeout(() => {
      document.body.removeChild(a);
    }, 300);
  },

  /**
   * Hủy một tác vụ tải
   */
  cancelTask(taskId) {
    const task = this.tasks.find(t => t.id === taskId);
    if (task && task.status === 'downloading') {
      task.controller.abort();
    } else if (task && task.status === 'pending') {
      task.status = 'cancelled';
      this.renderUI();
    }
  },

  /**
   * Thử tải lại một tác vụ bị lỗi hoặc bị hủy
   */
  retryTask(taskId) {
    const task = this.tasks.find(t => t.id === taskId);
    if (task) {
      task.status = 'pending';
      task.progress = 0;
      task.loaded = 0;
      task.error = null;
      task.controller = new AbortController();
      this.renderUI();
      this.processQueue();
    }
  },

  /**
   * Dọn dẹp danh sách các tác vụ đã hoàn thành hoặc bị hủy
   */
  clearCompletedTasks() {
    this.tasks = this.tasks.filter(t => t.status === 'downloading' || t.status === 'pending');
    this.renderUI();
    if (this.tasks.length === 0) {
      this.closeDrawer();
    }
  },

  // =========================================================================
  // 3. RENDER VÀ CẬP NHẬT GIAO DIỆN THEO THỜI GIAN THỰC
  // =========================================================================

  renderUI() {
    const triggerBtn = document.getElementById('dl-trigger-btn');
    const triggerBadge = document.getElementById('dl-trigger-badge');
    const triggerLabel = document.getElementById('dl-trigger-label');
    const activeBadge = document.getElementById('dl-panel-active-badge');
    const panelBody = document.getElementById('dl-panel-body');

    const downloadingTasks = this.tasks.filter(t => t.status === 'downloading');
    const pendingTasks = this.tasks.filter(t => t.status === 'pending');
    const totalActive = downloadingTasks.length + pendingTasks.length;

    // Cập nhật nút kích hoạt nổi
    if (triggerBtn) {
      if (this.tasks.length > 0) {
        triggerBtn.classList.add('active');
        if (triggerBadge) triggerBadge.textContent = this.tasks.length;
        if (triggerLabel) {
          triggerLabel.textContent = totalActive > 0 ? `Đang tải (${totalActive})` : 'Tải xuống';
        }
        const iconWrap = triggerBtn.querySelector('.dl-trigger-icon');
        if (iconWrap) {
          if (downloadingTasks.length > 0) {
            iconWrap.classList.add('spinning');
          } else {
            iconWrap.classList.remove('spinning');
          }
        }
      } else {
        triggerBtn.classList.remove('active');
      }
    }

    if (activeBadge) {
      activeBadge.textContent = `${totalActive} đang xử lý`;
      activeBadge.className = totalActive > 0 ? 'badge badge-info' : 'badge badge-success';
    }

    if (!panelBody) return;

    if (this.tasks.length === 0) {
      panelBody.innerHTML = `
        <div style="text-align: center; padding: 24px 12px; color: var(--text-muted, #94a3b8); font-size: 12px;">
          Chưa có tác vụ tải xuống nào
        </div>
      `;
      return;
    }

    let html = '';
    this.tasks.forEach(task => {
      let icon = '📄';
      if (task.type === 'zip') icon = '📦';
      else if (task.mimeType && task.mimeType.startsWith('video/')) icon = '🎬';
      else if (task.mimeType && task.mimeType.startsWith('image/')) icon = '🖼️';
      else if (task.mimeType && task.mimeType.startsWith('audio/')) icon = '🎵';

      let statusClass = task.status;
      let statusText = 'Đang chờ...';
      let speedText = '';

      if (task.status === 'downloading') {
        statusText = `Đang tải: ${task.progress}%`;
        if (task.speed > 0) {
          speedText = `${this.formatSpeed(task.speed)} • ${this.formatBytes(task.loaded)} / ${this.formatBytes(task.total)}`;
        } else if (task.total > 0) {
          speedText = `${this.formatBytes(task.loaded)} / ${this.formatBytes(task.total)}`;
        }
      } else if (task.status === 'completed') {
        statusText = 'Hoàn tất ✓';
      } else if (task.status === 'failed') {
        statusText = 'Thất bại ✕';
      } else if (task.status === 'cancelled') {
        statusText = 'Đã hủy';
      }

      html += `
        <div class="dl-item" id="task-${task.id}">
          <div class="dl-item-top">
            <div class="dl-item-info">
              <span class="dl-item-icon">${icon}</span>
              <span class="dl-item-name" title="${task.name}">${this.escapeHtml(task.name)}</span>
            </div>
            <div class="dl-item-actions">
              ${task.status === 'downloading' || task.status === 'pending' ? `
                <button class="dl-item-btn" onclick="DownloadManager.cancelTask('${task.id}')" title="Hủy tải xuống">
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg>
                </button>
              ` : ''}
              ${task.status === 'failed' || task.status === 'cancelled' ? `
                <button class="dl-item-btn" onclick="DownloadManager.retryTask('${task.id}')" title="Tải lại" style="color: #38bdf8;">
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><path d="M23 4v6h-6"></path><path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"></path></svg>
                </button>
              ` : ''}
            </div>
          </div>

          <div class="dl-progress-track">
            <div class="dl-progress-bar ${statusClass}" style="width: ${task.progress}%;"></div>
          </div>

          <div class="dl-item-meta">
            <span class="dl-item-status-text ${statusClass}">${statusText}</span>
            <span>${speedText}</span>
          </div>
        </div>
      `;
    });

    panelBody.innerHTML = html;
  },

  // =========================================================================
  // TIỆN ÍCH ĐỊNH DẠNG
  // =========================================================================

  formatBytes(bytes) {
    if (!bytes || bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
  },

  formatSpeed(bytesPerSec) {
    if (!bytesPerSec || bytesPerSec <= 0) return '';
    return this.formatBytes(bytesPerSec) + '/s';
  },

  escapeHtml(str) {
    if (!str) return '';
    return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
  },
};

// Tự động khởi chạy khi tài liệu HTML sẵn sàng
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', () => DownloadManager.init());
} else {
  DownloadManager.init();
}
