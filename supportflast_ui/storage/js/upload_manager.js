// ==========================================================================
// CloudPool - UploadManager: Module Quản Lý Tải Lên Đột Phá
// Hỗ trợ:
// 1. Tải lên thư mục (Folder Upload) đệ quy (webkitGetAsEntry / FileSystemDirectoryEntry)
// 2. Bảng điều khiển tiến trình tải lên (Floating Upload Drawer) với Speed, ETA, Controls
// 3. Tải lên phân đoạn song song (Parallel Chunk Upload) cho tệp lớn (>5MB)
// ==========================================================================

const UploadManager = {
  // Cấu hình tải lên
  config: {
    CHUNK_SIZE: 4 * 1024 * 1024, // 4MB mỗi mảnh chunk (tối ưu cho 4G/Wifi & Cloudflare Tunnel)
    CHUNK_THRESHOLD: 5 * 1024 * 1024, // File > 5MB sẽ kích hoạt tải lên phân đoạn song song
    MAX_PARALLEL_CHUNKS: 3, // Số luồng upload chunk song song cho mỗi file lớn
    MAX_CONCURRENT_FILES: 2, // Tối đa 2 file chạy đồng thời để tối ưu băng thông
    MAX_CHUNK_RETRIES: 4, // Số lần tự động thử lại khi một chunk gặp sự cố mạng
    SPEED_INTERVAL_MS: 600, // Chu kỳ tính toán tốc độ và ETA (ms)
  },

  // Danh sách các tác vụ tải lên (Queue)
  tasks: [],

  // Bộ nhớ đệm phân cấp thư mục đã resolve (path -> folderId)
  folderCache: new Map(),

  // Bộ đếm chu kỳ tính tốc độ
  speedTimer: null,

  // Trạng thái thu nhỏ của Drawer
  isMinimized: false,

  // Key lưu trữ trạng thái tải lên bền vững trong localStorage (chống mất tiến trình khi F5/reload)
  STORAGE_KEY: 'cloudpool_upload_state_v1',

  // ========================================================================
  // 1. KHỞI TẠO VÀ LẮNG NGHE SỰ KIỆN GIAO DIỆN
  // ========================================================================
  init() {
    this.bindDrawerEvents();
    this.startSpeedMonitor();
    this.initGlobalDragDrop();
    this.initBeforeUnloadWarning();
    this.loadFromStorage();
    console.log('[UploadManager] Module Quản lý tải lên đột phá đã sẵn sàng (với Bộ nhớ phục hồi F5).');
  },

  // Cảnh báo người dùng khi đang có tệp tải lên mà vô tình F5 hoặc vuốt kéo tải lại trang
  initBeforeUnloadWarning() {
    window.addEventListener('beforeunload', (e) => {
      const hasActive = this.tasks.some(
        (t) => t.status === 'uploading' || t.status === 'pending'
      );
      if (hasActive) {
        // Lưu trạng thái ngay lập tức trước khi trang bị hủy
        this.saveToStorage();
        e.preventDefault();
        e.returnValue = 'Quá trình tải lên tệp tin đang diễn ra. Nếu tải lại trang hoặc thoát, tiến trình tải có thể bị gián đoạn!';
        return e.returnValue;
      }
    });
  },

  // Lưu trạng thái các tác vụ dở dang vào localStorage
  saveToStorage() {
    try {
      const activeTasks = this.tasks
        .filter((t) => ['uploading', 'pending', 'paused', 'interrupted', 'error'].includes(t.status))
        .map((t) => ({
          id: t.id,
          name: t.name,
          size: t.size,
          relPath: t.relPath || '',
          targetFolderId: t.targetFolderId || 'root',
          resolvedFolderId: t.resolvedFolderId || null,
          isLargeFile: !!t.isLargeFile,
          totalChunks: t.totalChunks || 1,
          chunkSize: t.chunkSize || this.config.CHUNK_SIZE,
          uploadId: t.uploadId || null,
          status: t.status === 'uploading' || t.status === 'pending' ? 'interrupted' : t.status,
          statusText:
            t.status === 'uploading' || t.status === 'pending'
              ? 'Gián đoạn do tải lại trang ⏸ - Nhấn Tiếp tục'
              : t.statusText,
          progress: t.progress || 0,
          uploadedBytes: t.uploadedBytes || 0,
          chunksStatus: t.chunksStatus || [],
          chunksBytes: t.chunksBytes || [],
          lastUpdated: Date.now(),
        }));

      if (activeTasks.length > 0) {
        localStorage.setItem(this.STORAGE_KEY, JSON.stringify(activeTasks));
      } else {
        localStorage.removeItem(this.STORAGE_KEY);
      }
    } catch (err) {
      console.warn('[UploadManager] Không thể lưu trạng thái upload vào localStorage:', err);
    }
  },

  // Khôi phục các tác vụ dở dang từ localStorage sau khi F5 / mở lại trang
  async loadFromStorage() {
    try {
      const saved = localStorage.getItem(this.STORAGE_KEY);
      if (!saved) return;

      const parsed = JSON.parse(saved);
      if (!Array.isArray(parsed) || parsed.length === 0) return;

      const now = Date.now();
      const validTasks = [];

      for (const item of parsed) {
        // Bỏ qua task quá 24h
        if (now - (item.lastUpdated || 0) > 24 * 3600 * 1000) continue;

        const task = {
          id: item.id || ('up_' + Date.now() + '_' + Math.random().toString(36).substr(2, 7)),
          file: null, // Cần người dùng rebind sau khi reload
          needsRebind: true,
          name: item.name,
          size: item.size,
          relPath: item.relPath || '',
          targetFolderId: item.targetFolderId || 'root',
          resolvedFolderId: item.resolvedFolderId || null,
          isLargeFile: !!item.isLargeFile,
          totalChunks: item.totalChunks || 1,
          chunkSize: item.chunkSize || this.config.CHUNK_SIZE,
          status: 'interrupted',
          statusText: 'Gián đoạn do tải lại trang ⏸ - Nhấn Tiếp tục để chọn lại tệp',
          progress: item.progress || 0,
          uploadedBytes: item.uploadedBytes || 0,
          speed: 0,
          eta: 0,
          lastBytes: item.uploadedBytes || 0,
          lastSpeedTime: Date.now(),
          retryCount: 0,
          errorMsg: '',
          uploadId: item.uploadId || null,
          chunksStatus:
            item.chunksStatus && item.chunksStatus.length === (item.totalChunks || 1)
              ? item.chunksStatus
              : new Array(item.totalChunks || 1).fill('pending'),
          chunksBytes:
            item.chunksBytes && item.chunksBytes.length === (item.totalChunks || 1)
              ? item.chunksBytes
              : new Array(item.totalChunks || 1).fill(0),
          activeRequests: new Map(),
          pollController: null,
          xhr: null,
        };

        // Nếu task có uploadId, truy vấn server để lấy received_chunks chính xác nhất
        if (task.uploadId) {
          this.syncTaskStatusWithServer(task).catch(() => {});
        }

        validTasks.push(task);
      }

      if (validTasks.length > 0) {
        this.tasks.push(...validTasks);
        this.openDrawer();
        this.renderDrawer();
        this.updateOverallStats();
        this.showRecoveryBanner(validTasks.length);
        console.log(
          `[UploadManager] Đã khôi phục ${validTasks.length} tác vụ tải lên dở dang sau khi tải lại trang.`
        );
      }
    } catch (err) {
      console.warn('[UploadManager] Lỗi đọc trạng thái upload từ localStorage:', err);
    }
  },

  // Đồng bộ trạng thái chunk từ server
  async syncTaskStatusWithServer(task) {
    if (!task.uploadId) return;
    try {
      const authHeaders = this.getAuthHeaders();
      const res = await fetch(`/api/files/upload-status?upload_id=${encodeURIComponent(task.uploadId)}`, {
        headers: authHeaders,
        credentials: 'include',
      });
      if (res.ok) {
        const data = await res.json();
        if (data.status === 'completed') {
          task.status = 'completed';
          task.progress = 100;
          task.uploadedBytes = task.size;
          task.statusText = 'Hoàn tất ✓ (đã đồng bộ máy chủ)';
          task.needsRebind = false;
          this.updateTaskUI(task);
          this.updateOverallStats();
          this.saveToStorage();
          this.hideRecoveryBannerIfNone();
          return;
        }

        if (Array.isArray(data.received_chunks)) {
          let completedBytes = 0;
          for (const idx of data.received_chunks) {
            if (idx >= 0 && idx < task.totalChunks) {
              task.chunksStatus[idx] = 'completed';
              const start = idx * task.chunkSize;
              const end = Math.min(start + task.chunkSize, task.size);
              const cSize = end - start;
              task.chunksBytes[idx] = cSize;
              completedBytes += cSize;
            }
          }
          task.uploadedBytes = completedBytes;
          task.progress = Math.min(99, Math.round((completedBytes / task.size) * 100));
          task.statusText = `Đã lưu trên máy chủ ${data.received_chunks.length}/${task.totalChunks} mảnh - Nhấn Tiếp tục`;
          this.updateTaskUI(task);
          this.updateOverallStats();
          this.saveToStorage();
        }
      }
    } catch (e) {
      console.warn(`[UploadManager] Không thể đồng bộ status upload ${task.uploadId}:`, e.message);
    }
  },

  // Mở bộ chọn tệp để người dùng rebind lại đúng tệp bị gián đoạn
  promptFileRebind(task) {
    return new Promise((resolve) => {
      const input = document.createElement('input');
      input.type = 'file';
      input.style.position = 'fixed';
      input.style.top = '0';
      input.style.left = '0';
      input.style.width = '0';
      input.style.height = '0';
      input.style.opacity = '0';
      input.style.zIndex = '-1';
      document.body.appendChild(input);

      input.onchange = async () => {
        const file = input.files && input.files[0];
        input.remove();
        if (!file) {
          resolve(false);
          return;
        }

        if (file.name !== task.name || file.size !== task.size) {
          Toast.error(
            `Tệp "${file.name}" (${this.formatSize(file.size)}) không khớp với tệp cần tiếp tục "${task.name}" (${this.formatSize(task.size)})!`
          );
          resolve(false);
          return;
        }

        task.file = file;
        task.needsRebind = false;
        task.status = 'pending';
        task.statusText = 'Đang tiếp tục tải lên...';
        task.errorMsg = '';

        if (task.uploadId) {
          await this.syncTaskStatusWithServer(task);
        }

        this.updateTaskUI(task);
        this.updateOverallStats();
        this.saveToStorage();
        Toast.success(`Đã nhận diện tệp "${task.name}", tiếp tục tải các phần còn lại!`);
        this.processQueue();
        this.hideRecoveryBannerIfNone();
        resolve(true);
      };

      input.oncancel = () => {
        input.remove();
        resolve(false);
      };

      input.click();
    });
  },

  // Khôi phục hàng loạt tất cả các tệp bị gián đoạn
  resumeAllInterrupted() {
    const interruptedTasks = this.tasks.filter((t) => t.status === 'interrupted' || t.needsRebind);
    if (interruptedTasks.length === 0) return;

    const input = document.createElement('input');
    input.type = 'file';
    input.multiple = true;
    input.style.position = 'fixed';
    input.style.top = '0';
    input.style.left = '0';
    input.style.width = '0';
    input.style.height = '0';
    input.style.opacity = '0';
    input.style.zIndex = '-1';
    document.body.appendChild(input);

    input.onchange = async () => {
      const files = Array.from(input.files || []);
      input.remove();
      if (files.length === 0) return;

      let matchedCount = 0;
      for (const file of files) {
        const match = this.tasks.find(
          (t) => (t.status === 'interrupted' || t.needsRebind) && t.name === file.name && t.size === file.size
        );
        if (match) {
          match.file = file;
          match.needsRebind = false;
          match.status = 'pending';
          match.statusText = 'Đang tiếp tục tải lên...';
          if (match.uploadId) {
            await this.syncTaskStatusWithServer(match);
          }
          this.updateTaskUI(match);
          matchedCount++;
        }
      }

      if (matchedCount > 0) {
        Toast.success(`Đã nhận diện và khôi phục thành công ${matchedCount} tệp! Bắt đầu tải tiếp.`);
        this.updateOverallStats();
        this.saveToStorage();
        this.processQueue();
        this.hideRecoveryBannerIfNone();
      } else {
        Toast.warning('Không tìm thấy tệp nào khớp tên và dung lượng trong danh sách chờ khôi phục.');
      }
    };

    input.oncancel = () => input.remove();
    input.click();
  },

  // Hiển thị Banner khôi phục tác vụ dở dang
  showRecoveryBanner(count) {
    const banner = document.getElementById('upload-recovery-banner');
    if (banner) {
      banner.style.display = 'flex';
      const textEl = document.getElementById('upload-recovery-banner-text');
      if (textEl) {
        textEl.textContent = `⚡ Có ${count} tệp bị gián đoạn do tải lại trang. Nhấn Tiếp tục để chọn lại tệp và tải tiếp mà không mất dữ liệu.`;
      }
    }
  },

  // Ẩn Banner nếu không còn tác vụ nào bị gián đoạn
  hideRecoveryBannerIfNone() {
    const hasInterrupted = this.tasks.some((t) => t.status === 'interrupted' || t.needsRebind);
    if (!hasInterrupted) {
      const banner = document.getElementById('upload-recovery-banner');
      if (banner) {
        banner.style.display = 'none';
      }
    }
  },

  // Lắng nghe sự kiện kéo thả toàn trang để kích hoạt drop zone tự nhiên
  initGlobalDragDrop() {
    let dragCounter = 0;
    const dropZone = document.getElementById('file-drop-zone');

    window.addEventListener('dragenter', (e) => {
      e.preventDefault();
      dragCounter++;
      if (dropZone && e.dataTransfer && e.dataTransfer.types && Array.from(e.dataTransfer.types).includes('Files')) {
        dropZone.classList.add('drag-over');
      }
    });

    window.addEventListener('dragleave', (e) => {
      e.preventDefault();
      dragCounter--;
      if (dragCounter <= 0) {
        dragCounter = 0;
        if (dropZone) dropZone.classList.remove('drag-over');
      }
    });

    window.addEventListener('dragover', (e) => {
      e.preventDefault();
    });

    window.addEventListener('drop', (e) => {
      e.preventDefault();
      dragCounter = 0;
      if (dropZone) dropZone.classList.remove('drag-over');
    });
  },

  // Gắn sự kiện các nút trên Floating Drawer
  bindDrawerEvents() {
    const btnClose = document.getElementById('btn-close-upload-drawer');
    const btnMin = document.getElementById('btn-minimize-upload-drawer');
    const btnExpand = document.getElementById('btn-expand-upload-drawer');
    const btnPauseAll = document.getElementById('btn-upload-pause-all');
    const btnResumeAll = document.getElementById('btn-upload-resume-all');
    const btnCancelAll = document.getElementById('btn-upload-cancel-all');
    const btnClearDone = document.getElementById('btn-upload-clear-done');

    if (btnClose) {
      btnClose.addEventListener('click', (e) => {
        e.stopPropagation();
        this.closeDrawer();
      });
    }

    if (btnMin) {
      btnMin.addEventListener('click', (e) => {
        e.stopPropagation();
        this.toggleMinimize(true);
      });
    }

    if (btnExpand) {
      btnExpand.addEventListener('click', (e) => {
        e.stopPropagation();
        this.toggleMinimize(false);
      });
    }

    if (btnPauseAll) {
      btnPauseAll.addEventListener('click', (e) => {
        e.stopPropagation();
        this.pauseAll();
      });
    }

    if (btnResumeAll) {
      btnResumeAll.addEventListener('click', (e) => {
        e.stopPropagation();
        this.resumeAll();
      });
    }

    if (btnCancelAll) {
      btnCancelAll.addEventListener('click', (e) => {
        e.stopPropagation();
        this.cancelAll();
      });
    }

    if (btnClearDone) {
      btnClearDone.addEventListener('click', (e) => {
        e.stopPropagation();
        this.clearCompleted();
      });
    }
  },

  // ========================================================================
  // 2. HỖ TRỢ TẢI LÊN THƯ MỤC (Folder Upload & Recursive Scanner)
  // ========================================================================

  /**
   * Quét đệ quy FileSystemEntry (File hoặc Directory)
   * Vượt qua giới hạn 100 entries của Chromium WebKit bằng vòng lặp while
   */
  async scanEntryRecursive(entry, currentPath = '') {
    if (!entry) return [];

    if (entry.isFile) {
      return new Promise((resolve) => {
        entry.file(
          (file) => {
            file.relPath = currentPath;
            resolve([file]);
          },
          (err) => {
            console.warn('[UploadManager] Không thể đọc tệp:', entry.fullPath, err);
            resolve([]);
          }
        );
      });
    }

    if (entry.isDirectory) {
      const dirReader = entry.createReader();
      const childEntries = [];

      // Vòng lặp đọc toàn bộ entries (Chromium chỉ trả về tối đa 100 entries mỗi mẻ)
      while (true) {
        try {
          const batch = await new Promise((resolve, reject) => {
            dirReader.readEntries(resolve, reject);
          });
          if (!batch || batch.length === 0) break;
          childEntries.push(...batch);
        } catch (err) {
          console.warn('[UploadManager] Lỗi đọc thư mục con:', entry.fullPath, err);
          break;
        }
      }

      const results = [];
      const newPath = currentPath ? `${currentPath}/${entry.name}` : entry.name;
      for (const child of childEntries) {
        const subFiles = await this.scanEntryRecursive(child, newPath);
        results.push(...subFiles);
      }
      return results;
    }

    return [];
  },

  /**
   * Xử lý sự kiện kéo thả (Drop) hỗ trợ cả tệp và thư mục đệ quy
   */
  async handleDrop(event, targetFolderId = 'root') {
    event.preventDefault();
    event.stopPropagation();

    // Kiểm tra đăng nhập
    const user = API.getCurrentUser();
    if (!user) {
      Toast.warning('🔒 Vui lòng đăng nhập tài khoản để được cấp quyền tải lên.', 6000);
      if (typeof AuthManager !== 'undefined' && AuthManager.openLoginModal) {
        AuthManager.openLoginModal();
      }
      return;
    }

    const dt = event.dataTransfer;
    if (!dt) return;

    const filesToUpload = [];

    // 1. Thử đọc theo FileSystem API (DataTransferItem)
    if (dt.items && dt.items.length > 0) {
      for (let i = 0; i < dt.items.length; i++) {
        const item = dt.items[i];
        if (item.webkitGetAsEntry) {
          const entry = item.webkitGetAsEntry();
          if (entry) {
            const scanned = await this.scanEntryRecursive(entry, '');
            filesToUpload.push(...scanned);
            continue;
          }
        }
        if (item.getAsEntry) {
          const entry = item.getAsEntry();
          if (entry) {
            const scanned = await this.scanEntryRecursive(entry, '');
            filesToUpload.push(...scanned);
            continue;
          }
        }
        // Fallback file lẻ
        const file = item.getAsFile();
        if (file) {
          file.relPath = '';
          filesToUpload.push(file);
        }
      }
    } else if (dt.files && dt.files.length > 0) {
      // 2. Fallback sang FileList thông thường
      for (let i = 0; i < dt.files.length; i++) {
        const file = dt.files[i];
        file.relPath = '';
        filesToUpload.push(file);
      }
    }

    if (filesToUpload.length > 0) {
      this.enqueueFiles(filesToUpload, targetFolderId);
    }
  },

  /**
   * Xử lý khi người dùng chọn tải lên từ input thư mục (webkitdirectory)
   */
  uploadFolderInput(fileList, targetFolderId = 'root') {
    if (!fileList || fileList.length === 0) return;

    const files = Array.from(fileList).map((file) => {
      let relPath = '';
      if (file.webkitRelativePath) {
        const parts = file.webkitRelativePath.split('/');
        parts.pop(); // Bỏ tên tệp ở cuối để lấy đường dẫn thư mục cha
        relPath = parts.join('/');
      } else if (file.relPath) {
        relPath = file.relPath;
      }
      file.relPath = relPath;
      return file;
    });

    this.enqueueFiles(files, targetFolderId);
  },

  /**
   * Xử lý khi người dùng chọn tải lên danh sách tệp lẻ
   */
  uploadFiles(fileList, targetFolderId = 'root') {
    if (!fileList || fileList.length === 0) return;
    const files = Array.from(fileList).map((file) => {
      if (!file.relPath) file.relPath = '';
      return file;
    });
    this.enqueueFiles(files, targetFolderId);
  },

  /**
   * Đảm bảo cấu trúc cây thư mục con tồn tại trên VFS trước khi tải tệp
   */
  async ensureFolderPath(rootFolderId, relPath) {
    if (!relPath || relPath === '.' || relPath === '/') {
      return rootFolderId || 'root';
    }

    const segments = relPath
      .replace(/\\/g, '/')
      .split('/')
      .map((s) => s.trim())
      .filter((s) => s && s !== '.');

    let currentParentId = rootFolderId || 'root';

    for (const segment of segments) {
      const cacheKey = `${currentParentId}:${segment}`;
      if (this.folderCache.has(cacheKey)) {
        currentParentId = this.folderCache.get(cacheKey);
        continue;
      }

      try {
        // Tạo hoặc lấy thư mục con
        const res = await API.createFolder(currentParentId, segment);
        if (res && res.id) {
          this.folderCache.set(cacheKey, res.id);
          currentParentId = res.id;
        } else {
          // Nếu đã tồn tại nhưng server không trả id trực tiếp trong res, truy vấn danh sách file
          const files = await API.listFiles(currentParentId);
          const found = (files || []).find((f) => f.is_dir && f.name === segment);
          if (found) {
            this.folderCache.set(cacheKey, found.id);
            currentParentId = found.id;
          }
        }
      } catch (err) {
        console.warn(`[UploadManager] Kiểm tra thư mục con "${segment}":`, err.message);
        try {
          const files = await API.listFiles(currentParentId);
          const found = (files || []).find((f) => f.is_dir && f.name === segment);
          if (found) {
            this.folderCache.set(cacheKey, found.id);
            currentParentId = found.id;
          }
        } catch (_) {}
      }
    }

    return currentParentId;
  },

  // ========================================================================
  // 3. QUẢN LÝ HÀNG ĐỢI & ĐIỀU PHỐI TẢI LÊN
  // ========================================================================

  /**
   * Thêm các tệp vào hàng đợi và bắt đầu xử lý
   */
  enqueueFiles(files, targetFolderId = 'root') {
    // Kiểm tra đăng nhập
    const user = API.getCurrentUser();
    if (!user) {
      Toast.warning('🔒 Vui lòng đăng nhập tài khoản để được cấp quyền tải lên.', 6000);
      if (typeof AuthManager !== 'undefined' && AuthManager.openLoginModal) {
        AuthManager.openLoginModal();
      }
      return;
    }

    let addedCount = 0;
    for (const file of files) {
      // Bỏ qua tệp 0 byte
      if (file.size === 0) {
        Toast.warning(`Tệp "${file.name}" có kích thước 0 byte, đã bỏ qua.`);
        continue;
      }

      // Tự động kiểm tra xem có tác vụ nào đang gián đoạn (sau F5) khớp với tệp này không
      const interruptedMatch = this.tasks.find(
        (t) => (t.status === 'interrupted' || t.needsRebind) && t.name === file.name && t.size === file.size
      );
      if (interruptedMatch) {
        interruptedMatch.file = file;
        interruptedMatch.needsRebind = false;
        interruptedMatch.status = 'pending';
        interruptedMatch.statusText = 'Đang tiếp tục tải lên...';
        interruptedMatch.errorMsg = '';
        if (interruptedMatch.uploadId) {
          this.syncTaskStatusWithServer(interruptedMatch);
        }
        this.updateTaskUI(interruptedMatch);
        this.hideRecoveryBannerIfNone();
        addedCount++;
        continue;
      }

      const isLarge = file.size > this.config.CHUNK_THRESHOLD;
      const totalChunks = isLarge ? Math.ceil(file.size / this.config.CHUNK_SIZE) : 1;

      const task = {
        id: 'up_' + Date.now() + '_' + Math.random().toString(36).substr(2, 7),
        file: file,
        name: file.name,
        size: file.size,
        relPath: file.relPath || '',
        targetFolderId: targetFolderId || 'root',
        resolvedFolderId: null, // Sẽ được gán sau khi ensureFolderPath
        isLargeFile: isLarge,
        totalChunks: totalChunks,
        chunkSize: this.config.CHUNK_SIZE,
        status: 'pending', // 'pending' | 'uploading' | 'paused' | 'error' | 'completed' | 'canceled'
        statusText: 'Đang xếp hàng...',
        progress: 0,
        uploadedBytes: 0,
        speed: 0,
        eta: 0,
        lastBytes: 0,
        lastSpeedTime: Date.now(),
        retryCount: 0,
        errorMsg: '',

        // Các trường phục vụ chunked upload
        uploadId: null,
        chunksStatus: new Array(totalChunks).fill('pending'), // 'pending' | 'uploading' | 'completed' | 'error'
        chunksBytes: new Array(totalChunks).fill(0),
        activeRequests: new Map(), // chunkIndex -> { xhr, controller }
        pollController: null,
        xhr: null, // Cho standard upload
      };

      this.tasks.push(task);
      addedCount++;
    }

    if (addedCount > 0) {
      this.openDrawer();
      this.renderDrawer();
      this.saveToStorage();
      this.processQueue();
    }
  },

  /**
   * Điều phối hàng đợi: lấy các tác vụ pending chạy theo giới hạn concurrent
   */
  processQueue() {
    const activeTasks = this.tasks.filter((t) => t.status === 'uploading');
    const availableSlots = this.config.MAX_CONCURRENT_FILES - activeTasks.length;

    if (availableSlots <= 0) return;

    const pendingTasks = this.tasks.filter((t) => t.status === 'pending');
    const toStart = pendingTasks.slice(0, availableSlots);

    for (const task of toStart) {
      this.startTask(task);
    }
  },

  /**
   * Bắt đầu tải một tệp
   */
  async startTask(task) {
    if (task.status !== 'pending' && task.status !== 'paused' && task.status !== 'error') return;

    task.status = 'uploading';
    task.errorMsg = '';
    task.lastSpeedTime = Date.now();
    task.lastBytes = task.uploadedBytes;
    this.updateTaskUI(task);
    this.updateOverallStats();

    try {
      // 1. Đảm bảo cấu trúc thư mục con nếu có relPath
      if (!task.resolvedFolderId) {
        task.statusText = task.relPath ? 'Đang tạo cây thư mục...' : 'Đang chuẩn bị...';
        this.updateTaskUI(task);
        task.resolvedFolderId = await this.ensureFolderPath(task.targetFolderId, task.relPath);
      }

      // 2. Lựa chọn cơ chế tải lên
      if (task.isLargeFile) {
        // Tải lên phân đoạn song song
        await this.runParallelChunkUpload(task);
      } else {
        // Tải lên tệp nhỏ qua standard multipart upload
        await this.runStandardUpload(task);
      }

      // Hoàn tất tệp
      task.status = 'completed';
      task.progress = 100;
      task.uploadedBytes = task.size;
      task.speed = 0;
      task.eta = 0;
      task.statusText = 'Hoàn tất ✓';
      this.updateTaskUI(task);
      this.updateOverallStats();

      // Thông báo và làm mới giao diện
      Toast.success(`Tải lên thành công "${task.name}"`);
      this.saveToStorage();
      this.hideRecoveryBannerIfNone();
      if (typeof FilesManager !== 'undefined' && FilesManager.loadFiles) {
        // Nếu người dùng đang đứng ở thư mục tải lên thì làm mới danh sách
        if (FilesManager.currentFolderId === task.targetFolderId || FilesManager.currentFolderId === task.resolvedFolderId) {
          FilesManager.loadFiles(FilesManager.currentFolderId);
        }
      }
      if (typeof App !== 'undefined' && App.refreshStats) {
        App.refreshStats();
      }
      if (typeof AccountsManager !== 'undefined' && AccountsManager.loadAccounts) {
        AccountsManager.loadAccounts();
      }
    } catch (err) {
      if (task.status === 'paused' || task.status === 'canceled') {
        return; // Đã xử lý ngắt bởi người dùng
      }
      console.error(`[UploadManager] Lỗi tải lên "${task.name}":`, err);
      task.status = 'error';
      task.speed = 0;
      task.eta = 0;
      task.errorMsg = err.message || 'Lỗi truyền dữ liệu máy chủ';
      task.statusText = `Lỗi: ${task.errorMsg}`;
      this.updateTaskUI(task);
      this.updateOverallStats();
      this.saveToStorage();
      Toast.error(`Lỗi tải lên "${task.name}": ${task.errorMsg}`);
    } finally {
      this.processQueue();
    }
  },

  // ========================================================================
  // 4. TẢI LÊN PHÂN ĐOẠN SONG SONG (Parallel Chunk Upload)
  // ========================================================================

  /**
   * Thuật toán tải lên phân đoạn song song cho tệp lớn (>5MB)
   * Sử dụng Worker Pool để tải đồng thời MAX_PARALLEL_CHUNKS (3 chunks)
   */
  async runParallelChunkUpload(task) {
    const authHeaders = this.getAuthHeaders();

    // Bước 4.1: Khởi tạo phiên Chunked Upload nếu chưa có
    if (!task.uploadId) {
      task.statusText = 'Đang khởi tạo phiên phân đoạn...';
      this.updateTaskUI(task);

      const initRes = await fetch('/api/files/upload-chunked', {
        method: 'POST',
        headers: {
          ...authHeaders,
          'Content-Type': 'application/json',
        },
        credentials: 'include',
        body: JSON.stringify({
          file_name: task.name,
          parent_id: task.resolvedFolderId || 'root',
          total_size: task.size,
          total_chunks: task.totalChunks,
        }),
      });

      if (!initRes.ok) {
        let errMsg = `Lỗi khởi tạo phiên (${initRes.status})`;
        try {
          const errData = await initRes.json();
          if (errData.error) errMsg = errData.error;
        } catch (_) {}
        throw new Error(errMsg);
      }

      const initData = await initRes.json();
      task.uploadId = initData.upload_id;
      this.saveToStorage();
    } else {
      // Đã có uploadId từ phiên trước (sau khi F5 khôi phục), đồng bộ danh sách mảnh trên máy chủ
      await this.syncTaskStatusWithServer(task);
    }

    // Bước 4.2: Worker Pool tải song song các chunk
    task.statusText = `Đang tải song song (${this.config.MAX_PARALLEL_CHUNKS} luồng)...`;
    this.updateTaskUI(task);

    // Lấy chỉ số chunk tiếp theo đang pending hoặc error
    const getNextChunkIndex = () => {
      return task.chunksStatus.findIndex((st) => st === 'pending');
    };

    let workerError = null;

    // Định nghĩa 1 worker lấy chunk và upload
    const worker = async (workerId) => {
      while (task.status === 'uploading') {
        const chunkIndex = getNextChunkIndex();
        if (chunkIndex === -1) break; // Đã hết chunk cần tải

        // Đánh dấu chunk đang upload
        task.chunksStatus[chunkIndex] = 'uploading';

        try {
          await this.uploadSingleChunk(task, chunkIndex);
          task.chunksStatus[chunkIndex] = 'completed';

          // Cập nhật tiến độ tổng hợp của task
          this.recalculateTaskProgress(task);
          this.updateTaskUI(task);
          this.saveToStorage();
        } catch (err) {
          if (task.status === 'paused' || task.status === 'canceled') break;

          task.chunksStatus[chunkIndex] = 'pending'; // Trả về pending để retry hoặc worker khác nhận
          console.warn(`[UploadManager] Worker ${workerId} gặp lỗi mảnh ${chunkIndex + 1}:`, err.message);

          task.retryCount++;
          if (task.retryCount > this.config.MAX_CHUNK_RETRIES * task.totalChunks) {
            workerError = err;
            break;
          }
          await new Promise((r) => setTimeout(r, 1200));
        }
      }
    };

    // Khởi chạy đồng thời MAX_PARALLEL_CHUNKS workers
    const workers = [];
    const concurrency = Math.min(this.config.MAX_PARALLEL_CHUNKS, task.totalChunks);
    for (let i = 0; i < concurrency; i++) {
      workers.push(worker(i + 1));
    }

    await Promise.all(workers);

    if (task.status === 'paused' || task.status === 'canceled') {
      return;
    }

    if (workerError) {
      throw workerError;
    }

    // Kiểm tra xem tất cả các mảnh đã hoàn tất chưa
    const allCompleted = task.chunksStatus.every((st) => st === 'completed');
    if (!allCompleted) {
      throw new Error('Chưa hoàn tất toàn bộ các mảnh chunk.');
    }

    // Bước 4.3: Polling trạng thái ghép tệp và đồng bộ Cloud
    task.statusText = 'Đang ghép tệp & mã hóa AES-256 phân tán lên Google Drive...';
    task.progress = 92;
    this.updateTaskUI(task);

    await this.pollChunkAssembly(task);
  },

  /**
   * Tải một mảnh chunk bằng XMLHttpRequest để bắt sự kiện onprogress chính xác
   */
  uploadSingleChunk(task, chunkIndex) {
    return new Promise((resolve, reject) => {
      if (task.status !== 'uploading') {
        return reject(new Error('Tác vụ không ở trạng thái tải lên'));
      }

      const start = chunkIndex * task.chunkSize;
      const end = Math.min(start + task.chunkSize, task.file.size);
      const chunkBlob = task.file.slice(start, end);
      const currentChunkSize = end - start;

      const xhr = new XMLHttpRequest();
      const formData = new FormData();
      formData.append('chunk', chunkBlob, `chunk_${chunkIndex}`);

      task.activeRequests.set(chunkIndex, { xhr });

      xhr.open(
        'POST',
        `/api/files/upload-chunked?upload_id=${encodeURIComponent(task.uploadId)}&chunk_index=${chunkIndex}`,
        true
      );
      xhr.withCredentials = true;
      xhr.timeout = 180 * 1000; // 3 phút cho 1 chunk 4MB

      const authHeaders = this.getAuthHeaders();
      for (const [key, val] of Object.entries(authHeaders)) {
        xhr.setRequestHeader(key, val);
      }

      // Theo dõi tiến trình tải của riêng chunk này
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable && task.status === 'uploading') {
          task.chunksBytes[chunkIndex] = e.loaded;
          this.recalculateTaskProgress(task);
        }
      };

      xhr.onload = () => {
        task.activeRequests.delete(chunkIndex);
        if (xhr.status >= 200 && xhr.status < 300) {
          task.chunksBytes[chunkIndex] = currentChunkSize;
          resolve();
        } else if (xhr.status === 429) {
          reject(new Error('Máy chủ đang bận, vui lòng thử lại'));
        } else {
          let errMsg = `Lỗi mảnh ${chunkIndex + 1} (${xhr.status})`;
          try {
            const res = JSON.parse(xhr.responseText);
            if (res.error) errMsg = res.error;
          } catch (_) {}
          reject(new Error(errMsg));
        }
      };

      xhr.onerror = () => {
        task.activeRequests.delete(chunkIndex);
        reject(new Error('Lỗi kết nối mạng khi tải mảnh'));
      };

      xhr.ontimeout = () => {
        task.activeRequests.delete(chunkIndex);
        reject(new Error('Hết thời gian tải mảnh (Timeout)'));
      };

      xhr.onabort = () => {
        task.activeRequests.delete(chunkIndex);
        reject(new Error('Tác vụ bị ngắt'));
      };

      xhr.send(formData);
    });
  },

  /**
   * Polling trạng thái ghép tệp trên server
   */
  async pollChunkAssembly(task) {
    const authHeaders = this.getAuthHeaders();
    const pollStart = Date.now();
    const maxPollTime = 30 * 60 * 1000; // Tối đa 30 phút cho file cực lớn

    while (Date.now() - pollStart < maxPollTime) {
      if (task.status === 'paused' || task.status === 'canceled') return;

      await new Promise((r) => setTimeout(r, 1500));
      if (task.status === 'paused' || task.status === 'canceled') return;

      const controller = new AbortController();
      task.pollController = controller;

      try {
        const res = await fetch(`/api/files/upload-status?upload_id=${encodeURIComponent(task.uploadId)}`, {
          method: 'GET',
          headers: authHeaders,
          credentials: 'include',
          signal: controller.signal,
        });

        if (!res.ok) {
          const errData = await res.json().catch(() => ({}));
          throw new Error(errData.error || `Lỗi kiểm tra tiến trình (${res.status})`);
        }

        const data = await res.json();
        if (data.status === 'completed') {
          task.progress = 100;
          return;
        }

        if (data.status === 'error') {
          throw new Error(data.error || 'Lỗi ghép tệp trên máy chủ');
        }

        // Đang xử lý
        const elapsed = Math.round((Date.now() - pollStart) / 1000);
        task.statusText = `Đang mã hóa & đồng bộ Google Drive (${elapsed}s)...`;
        task.progress = Math.min(98, 92 + Math.floor(elapsed / 10));
        this.updateTaskUI(task);
      } catch (err) {
        if (err.name === 'AbortError') return;
        throw err;
      } finally {
        task.pollController = null;
      }
    }

    throw new Error('Quá thời gian chờ máy chủ ghép tệp.');
  },

  /**
   * Tính lại tổng số byte đã tải và phần trăm tiến độ của task chunked
   */
  recalculateTaskProgress(task) {
    const totalLoadedBytes = task.chunksBytes.reduce((sum, b) => sum + (b || 0), 0);
    task.uploadedBytes = Math.min(totalLoadedBytes, task.size);

    // Dành 0% - 90% cho việc upload chunks, 10% còn lại cho ghép tệp
    const uploadPct = Math.round((task.uploadedBytes / task.size) * 90);
    task.progress = Math.min(90, Math.max(0, uploadPct));

    const completedChunks = task.chunksStatus.filter((s) => s === 'completed').length;
    task.statusText = `Đang tải mảnh ${completedChunks}/${task.totalChunks} (${task.progress}%)`;
  },

  // ========================================================================
  // 5. TẢI LÊN TỆP NHỎ (Standard Upload <= 5MB)
  // ========================================================================
  runStandardUpload(task) {
    return new Promise((resolve, reject) => {
      if (task.status !== 'uploading') {
        return reject(new Error('Tác vụ không ở trạng thái tải lên'));
      }

      const xhr = new XMLHttpRequest();
      task.xhr = xhr;

      const formData = new FormData();
      formData.append('parent_id', task.resolvedFolderId || 'root');
      if (task.relPath) {
        formData.append('rel_path', task.relPath);
      }
      formData.append('file', task.file, task.name);

      xhr.open('POST', '/api/files/upload', true);
      xhr.withCredentials = true;
      xhr.timeout = 15 * 60 * 1000;

      const authHeaders = this.getAuthHeaders();
      for (const [key, val] of Object.entries(authHeaders)) {
        xhr.setRequestHeader(key, val);
      }

      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable && task.status === 'uploading') {
          task.uploadedBytes = e.loaded;
          task.progress = Math.round((e.loaded / e.total) * 100);
          task.statusText = `Đang tải lên (${task.progress}%)...`;
          this.updateTaskUI(task);
        }
      };

      xhr.onload = () => {
        task.xhr = null;
        if (xhr.status >= 200 && xhr.status < 300) {
          task.progress = 100;
          task.uploadedBytes = task.size;
          resolve();
        } else {
          let errMsg = `Tải lên thất bại (${xhr.status})`;
          try {
            const res = JSON.parse(xhr.responseText);
            if (res.error) errMsg = res.error;
          } catch (_) {}
          reject(new Error(errMsg));
        }
      };

      xhr.onerror = () => {
        task.xhr = null;
        reject(new Error('Lỗi kết nối mạng khi tải lên'));
      };

      xhr.ontimeout = () => {
        task.xhr = null;
        reject(new Error('Hết thời gian tải lên (Timeout)'));
      };

      xhr.onabort = () => {
        task.xhr = null;
        reject(new Error('Tác vụ bị ngắt'));
      };

      xhr.send(formData);
    });
  },

  // ========================================================================
  // 6. THAO TÁC ĐIỀU KHIỂN: PAUSE, RESUME, RETRY, CANCEL
  // ========================================================================

  /**
   * Tạm dừng một tệp
   */
  pause(taskId) {
    const task = this.tasks.find((t) => t.id === taskId);
    if (!task || task.status !== 'uploading') return;

    task.status = 'paused';
    task.speed = 0;
    task.eta = 0;
    task.statusText = 'Đã tạm dừng ⏸';

    // Ngắt các XHR đang chạy
    if (task.xhr) {
      task.xhr.abort();
      task.xhr = null;
    }
    if (task.activeRequests) {
      task.activeRequests.forEach((req) => {
        if (req.xhr) req.xhr.abort();
      });
      task.activeRequests.clear();
    }
    if (task.pollController) {
      task.pollController.abort();
      task.pollController = null;
    }

    // Đưa các chunk đang dở về pending
    if (task.chunksStatus) {
      task.chunksStatus = task.chunksStatus.map((st) => (st === 'uploading' ? 'pending' : st));
    }

    this.updateTaskUI(task);
    this.updateOverallStats();
    this.saveToStorage();
    this.processQueue();
  },

  /**
   * Tiếp tục tải tệp đang tạm dừng hoặc gián đoạn sau F5
   */
  async resume(taskId) {
    const task = this.tasks.find((t) => t.id === taskId);
    if (!task) return;

    // Nếu task bị gián đoạn sau F5 và chưa có File object trong RAM
    if (task.needsRebind || !task.file) {
      await this.promptFileRebind(task);
      return;
    }

    if (task.status !== 'paused' && task.status !== 'error' && task.status !== 'interrupted') return;

    task.status = 'pending';
    task.statusText = 'Đang tiếp tục...';
    this.updateTaskUI(task);
    this.updateOverallStats();
    this.saveToStorage();
    this.processQueue();
  },

  /**
   * Thử lại tệp bị lỗi
   */
  retry(taskId) {
    const task = this.tasks.find((t) => t.id === taskId);
    if (!task) return;

    if (task.needsRebind || !task.file) {
      this.promptFileRebind(task);
      return;
    }

    task.status = 'pending';
    task.errorMsg = '';
    task.retryCount = 0;
    task.statusText = 'Đang thử lại...';

    // Đặt lại các chunk bị lỗi về pending
    if (task.chunksStatus) {
      task.chunksStatus = task.chunksStatus.map((st) => (st === 'error' ? 'pending' : st));
    }

    this.updateTaskUI(task);
    this.updateOverallStats();
    this.saveToStorage();
    this.processQueue();
  },

  /**
   * Hủy bỏ tải lên của một tệp
   */
  cancel(taskId) {
    const taskIndex = this.tasks.findIndex((t) => t.id === taskId);
    if (taskIndex === -1) return;

    const task = this.tasks[taskIndex];
    task.status = 'canceled';

    // Ngắt kết nối mạng
    if (task.xhr) {
      task.xhr.abort();
      task.xhr = null;
    }
    if (task.activeRequests) {
      task.activeRequests.forEach((req) => {
        if (req.xhr) req.xhr.abort();
      });
      task.activeRequests.clear();
    }
    if (task.pollController) {
      task.pollController.abort();
      task.pollController = null;
    }

    // Xóa task khỏi danh sách
    this.tasks.splice(taskIndex, 1);

    // Xóa DOM item
    const el = document.getElementById(task.id);
    if (el) el.remove();

    this.updateOverallStats();
    this.saveToStorage();
    this.hideRecoveryBannerIfNone();
    this.processQueue();

    if (this.tasks.length === 0) {
      this.closeDrawer();
    }
  },

  /**
   * Tạm dừng tất cả các tệp đang tải
   */
  pauseAll() {
    this.tasks.filter((t) => t.status === 'uploading').forEach((t) => this.pause(t.id));
  },

  /**
   * Tiếp tục tất cả các tệp đang tạm dừng hoặc gián đoạn
   */
  async resumeAll() {
    const interruptedTasks = this.tasks.filter((t) => t.status === 'interrupted' || t.needsRebind);
    if (interruptedTasks.length > 0) {
      this.resumeAllInterrupted();
      return;
    }
    this.tasks
      .filter((t) => t.status === 'paused' || t.status === 'error')
      .forEach((t) => this.resume(t.id));
  },

  /**
   * Hủy tất cả các tệp
   */
  cancelAll() {
    const ids = this.tasks.map((t) => t.id);
    ids.forEach((id) => this.cancel(id));
  },

  /**
   * Xóa các tệp đã hoàn tất khỏi danh sách
   */
  clearCompleted() {
    const completedTasks = this.tasks.filter((t) => t.status === 'completed');
    completedTasks.forEach((t) => {
      const el = document.getElementById(t.id);
      if (el) el.remove();
    });
    this.tasks = this.tasks.filter((t) => t.status !== 'completed');
    this.updateOverallStats();
    this.saveToStorage();
    this.hideRecoveryBannerIfNone();
    if (this.tasks.length === 0) {
      this.closeDrawer();
    }
  },

  // ========================================================================
  // 7. THEO DÕI TỐC ĐỘ (SPEED) VÀ ƯỚC TÍNH THỜI GIAN CÒN LẠI (ETA)
  // ========================================================================

  startSpeedMonitor() {
    if (this.speedTimer) clearInterval(this.speedTimer);

    this.speedTimer = setInterval(() => {
      const now = Date.now();
      let totalSpeed = 0;

      for (const task of this.tasks) {
        if (task.status !== 'uploading') {
          task.speed = 0;
          task.eta = 0;
          continue;
        }

        const timeDiffSec = (now - task.lastSpeedTime) / 1000;
        if (timeDiffSec >= 0.4) {
          const bytesDiff = Math.max(0, task.uploadedBytes - task.lastBytes);
          const currentSpeed = bytesDiff / timeDiffSec;

          // Sử dụng Exponential Moving Average (EMA) để làm mượt tốc độ hiển thị
          task.speed = task.speed > 0 ? 0.65 * task.speed + 0.35 * currentSpeed : currentSpeed;

          // Tính toán ETA (thời gian còn lại)
          const remainingBytes = Math.max(0, task.size - task.uploadedBytes);
          if (task.speed > 1024) {
            task.eta = Math.ceil(remainingBytes / task.speed);
          } else {
            task.eta = 0;
          }

          task.lastBytes = task.uploadedBytes;
          task.lastSpeedTime = now;

          this.updateTaskUI(task);
        }

        totalSpeed += task.speed;
      }

      this.updateOverallStats(totalSpeed);
    }, this.config.SPEED_INTERVAL_MS);
  },

  formatSpeed(bytesPerSec) {
    if (!bytesPerSec || bytesPerSec < 1024) return '0 KB/s';
    if (bytesPerSec < 1024 * 1024) {
      return (bytesPerSec / 1024).toFixed(1) + ' KB/s';
    }
    return (bytesPerSec / (1024 * 1024)).toFixed(1) + ' MB/s';
  },

  formatETA(seconds) {
    if (!seconds || seconds <= 0 || !isFinite(seconds)) return '--';
    if (seconds < 60) return `còn ${seconds}s`;
    const m = Math.floor(seconds / 60);
    const s = seconds % 60;
    if (m < 60) return `còn ${m}p ${s}s`;
    const h = Math.floor(m / 60);
    const rm = m % 60;
    return `còn ${h}h ${rm}p`;
  },

  formatSize(bytes) {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
  },

  getFileIcon(filename) {
    const ext = filename.split('.').pop().toLowerCase();
    const icons = {
      pdf: '📄',
      doc: '📝',
      docx: '📝',
      xls: '📊',
      xlsx: '📊',
      ppt: '📑',
      pptx: '📑',
      zip: '📦',
      rar: '📦',
      '7z': '📦',
      tar: '📦',
      gz: '📦',
      jpg: '🖼️',
      jpeg: '🖼️',
      png: '🖼️',
      gif: '🖼️',
      webp: '🖼️',
      svg: '🖼️',
      mp4: '🎬',
      mkv: '🎬',
      avi: '🎬',
      mov: '🎬',
      mp3: '🎵',
      wav: '🎵',
      flac: '🎵',
      txt: '📃',
      md: '📃',
      json: '⚙️',
      js: '📜',
      ts: '📜',
      py: '🐍',
      go: '🐹',
      rs: '🦀',
      sql: '🗄️',
    };
    return icons[ext] || '📁';
  },

  // ========================================================================
  // 8. RENDER VÀ CẬP NHẬT GIAO DIỆN (Floating Drawer UI)
  // ========================================================================

  openDrawer() {
    const drawer = document.getElementById('upload-drawer');
    if (drawer) {
      drawer.classList.add('active');
    }
  },

  closeDrawer() {
    // Nếu có tệp đang tải, cảnh báo
    const activeTasks = this.tasks.filter((t) => t.status === 'uploading' || t.status === 'pending');
    if (activeTasks.length > 0) {
      if (!confirm(`Đang có ${activeTasks.length} tệp đang tải. Bạn có chắc muốn đóng và hủy tất cả?`)) {
        return;
      }
      this.cancelAll();
    }

    const drawer = document.getElementById('upload-drawer');
    if (drawer) {
      drawer.classList.remove('active');
    }
    this.toggleMinimize(false);
  },

  toggleMinimize(minimized) {
    this.isMinimized = minimized;
    const drawer = document.getElementById('upload-drawer');
    if (drawer) {
      drawer.classList.toggle('minimized', minimized);
    }
  },

  /**
   * Render toàn bộ danh sách tệp lên Drawer
   */
  renderDrawer() {
    const listEl = document.getElementById('upload-list');
    if (!listEl) return;

    for (const task of this.tasks) {
      let itemEl = document.getElementById(task.id);
      if (!itemEl) {
        itemEl = document.createElement('div');
        itemEl.id = task.id;
        itemEl.className = 'upload-drawer-item';
        listEl.prepend(itemEl);
      }
      this.updateTaskUI(task);
    }

    this.updateOverallStats();
  },

  /**
   * Cập nhật giao diện của 1 tệp cụ thể (DOM update cục bộ, mượt mà không nháy)
   */
  updateTaskUI(task) {
    const itemEl = document.getElementById(task.id);
    if (!itemEl) return;

    const fileIcon = this.getFileIcon(task.name);
    const speedStr = task.speed > 0 ? this.formatSpeed(task.speed) : '';
    const etaStr = task.eta > 0 ? this.formatETA(task.eta) : '';
    const uploadedStr = this.formatSize(task.uploadedBytes);
    const totalStr = this.formatSize(task.size);

    // Xác định màu sắc thanh tiến trình theo trạng thái
    const isUploading = task.status === 'uploading';
    const isPaused = task.status === 'paused';
    const isInterrupted = task.status === 'interrupted' || task.needsRebind;
    const isError = task.status === 'error';
    const isCompleted = task.status === 'completed';

    let progressColor = 'var(--accent-blue)';
    if (isCompleted) progressColor = 'var(--accent-teal)';
    if (isPaused || isInterrupted) progressColor = 'var(--accent-amber, #e3b341)';
    if (isError) progressColor = 'var(--accent-red)';

    // Tạo HTML cho nút điều khiển của item
    let actionsHtml = '';
    if (isUploading) {
      actionsHtml += `<button class="upload-btn-action" title="Tạm dừng" onclick="UploadManager.pause('${task.id}')">⏸</button>`;
    } else if (isPaused || isInterrupted) {
      actionsHtml += `<button class="upload-btn-action upload-btn-action-resume" title="Tiếp tục tải lên (Chọn lại tệp nếu F5)" onclick="UploadManager.resume('${task.id}')">▶</button>`;
    } else if (isError) {
      actionsHtml += `<button class="upload-btn-action" title="Thử lại" onclick="UploadManager.retry('${task.id}')">🔄</button>`;
    }
    if (!isCompleted) {
      actionsHtml += `<button class="upload-btn-action upload-btn-cancel" title="Hủy bỏ" onclick="UploadManager.cancel('${task.id}')">✕</button>`;
    }

    itemEl.innerHTML = `
      <div class="upload-item-header-row">
        <div class="upload-item-info">
          <span class="upload-item-icon">${fileIcon}</span>
          <div class="upload-item-names">
            <span class="upload-item-title" title="${task.relPath ? task.relPath + '/' : ''}${task.name}">
              ${task.name}
            </span>
            <div style="display: flex; align-items: center; gap: 4px; flex-wrap: wrap;">
              ${task.relPath ? `<span class="upload-item-folder-badge" title="Thư mục: ${task.relPath}">📁 ${task.relPath}</span>` : ''}
              ${isInterrupted ? `<span class="upload-item-recovery-badge" title="Tác vụ được khôi phục sau khi tải lại trang">⚡ Cần chọn lại tệp</span>` : ''}
            </div>
          </div>
        </div>
        <div class="upload-item-actions">
          ${actionsHtml}
        </div>
      </div>

      <div class="upload-item-progress-track">
        <div class="upload-item-progress-fill" style="width: ${task.progress}%; background: ${progressColor};"></div>
      </div>

      <div class="upload-item-footer-row">
        <span class="upload-item-status-text" title="${task.errorMsg || task.statusText}">
          ${task.statusText}
        </span>
        <div class="upload-item-metrics">
          <span>${uploadedStr} / ${totalStr}</span>
          ${speedStr ? `<span class="upload-item-speed">• ${speedStr}</span>` : ''}
          ${etaStr ? `<span class="upload-item-eta">• ${etaStr}</span>` : ''}
        </div>
      </div>
    `;
  },

  /**
   * Cập nhật thông số tổng thể (Overall Progress, Speed, Count)
   */
  updateOverallStats(currentTotalSpeed = 0) {
    const totalCount = this.tasks.length;
    const completedCount = this.tasks.filter((t) => t.status === 'completed').length;
    const activeCount = this.tasks.filter((t) => t.status === 'uploading').length;
    const pausedCount = this.tasks.filter((t) => t.status === 'paused').length;

    const totalBytes = this.tasks.reduce((sum, t) => sum + (t.size || 0), 0);
    const uploadedBytes = this.tasks.reduce((sum, t) => sum + (t.uploadedBytes || 0), 0);
    const overallProgress = totalBytes > 0 ? Math.round((uploadedBytes / totalBytes) * 100) : 0;

    // Header badge
    const badgeEl = document.getElementById('upload-drawer-badge');
    if (badgeEl) {
      badgeEl.textContent = `${completedCount}/${totalCount}`;
    }

    const titleEl = document.getElementById('upload-drawer-title');
    if (titleEl) {
      if (activeCount > 0) {
        titleEl.textContent = `Đang tải lên (${activeCount} tệp song song)...`;
      } else if (pausedCount > 0 && activeCount === 0) {
        titleEl.textContent = 'Đã tạm dừng tải lên';
      } else if (completedCount === totalCount && totalCount > 0) {
        titleEl.textContent = 'Hoàn tất toàn bộ tệp ✓';
      } else {
        titleEl.textContent = 'Quản lý tải lên';
      }
    }

    // Overall Progress Bar
    const overallBar = document.getElementById('upload-overall-progress-bar');
    if (overallBar) {
      overallBar.style.width = overallProgress + '%';
    }

    // Overall Stats Text
    const overallStatsEl = document.getElementById('upload-overall-stats-text');
    if (overallStatsEl) {
      const formattedUploaded = this.formatSize(uploadedBytes);
      const formattedTotal = this.formatSize(totalBytes);
      let speedText = '';
      if (currentTotalSpeed > 0) {
        speedText = ` • 🚀 ${this.formatSpeed(currentTotalSpeed)}`;
      }
      overallStatsEl.textContent = `${formattedUploaded} / ${formattedTotal} (${overallProgress}%)${speedText}`;
    }

    // Minimized Bar Stats
    const minTextEl = document.getElementById('upload-minimized-text');
    if (minTextEl) {
      const speedPart = currentTotalSpeed > 0 ? ` • ${this.formatSpeed(currentTotalSpeed)}` : '';
      minTextEl.textContent = `Đang tải ${completedCount}/${totalCount} (${overallProgress}%)${speedPart}`;
    }
    const minBarEl = document.getElementById('upload-minimized-bar');
    if (minBarEl) {
      minBarEl.style.width = overallProgress + '%';
    }
  },

  /**
   * Helper lấy các headers xác thực cần thiết
   */
  getAuthHeaders() {
    const headers = {
      'ngrok-skip-browser-warning': 'true',
    };
    const token = API.getToken();
    if (token) {
      headers['Authorization'] = `Bearer ${token}`;
    }
    const user = API.getCurrentUser();
    if (user && user.id) {
      headers['X-User-ID'] = user.id;
    }
    return headers;
  },
};

// Đăng ký toàn cục
window.UploadManager = UploadManager;

// Tự động khởi tạo khi DOM sẵn sàng
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', () => UploadManager.init());
} else {
  UploadManager.init();
}
