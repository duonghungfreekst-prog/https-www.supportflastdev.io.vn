// ==========================================================================
// CloudPool Native In-Browser Office & Document Renderer (PRO Edition)
// Supports PDF, PPTX (PowerPoint), DOCX / DOC (Word), XLSX / XLS / CSV / TSV (Excel)
// ==========================================================================

var OfficeViewer = window.OfficeViewer = {

  // Universal Zip Extractor (JSZip Engine with DecompressionStream fallback)
  async unzipArrayBuffer(arrayBuffer) {
    if (typeof JSZip !== 'undefined') {
      try {
        const zip = await JSZip.loadAsync(arrayBuffer);
        const files = {};
        const promises = [];
        zip.forEach((relativePath, file) => {
          if (!file.dir) {
            promises.push(
              file.async('uint8array').then(data => {
                files[relativePath] = data;
              })
            );
          }
        });
        await Promise.all(promises);
        return files;
      } catch (err) {
        console.warn('JSZip failed, falling back to native stream:', err);
      }
    }

    const uint8 = new Uint8Array(arrayBuffer);
    const view = new DataView(arrayBuffer);
    const files = {};

    let offset = 0;
    const len = uint8.length;

    // Helper to inflate raw deflate bytes
    const inflateBytes = async (compBytes) => {
      if (typeof DecompressionStream !== 'undefined') {
        try {
          const ds = new DecompressionStream('deflate-raw');
          const writer = ds.writable.getWriter();
          writer.write(compBytes);
          writer.close();
          const res = new Response(ds.readable);
          const buf = await res.arrayBuffer();
          return new Uint8Array(buf);
        } catch (e) {
          // Fallback if deflate-raw throws
        }
      }
      return null;
    };

    // Scan Local File Headers
    while (offset < len - 30) {
      const sig = view.getUint32(offset, true);
      if (sig === 0x04034b50) { // Local File Header
        const flags = view.getUint16(offset + 6, true);
        const compMethod = view.getUint16(offset + 8, true);
        let compSize = view.getUint32(offset + 18, true);
        let uncompSize = view.getUint32(offset + 22, true);
        const fnLen = view.getUint16(offset + 26, true);
        const extraLen = view.getUint16(offset + 28, true);

        const fnBytes = uint8.subarray(offset + 30, offset + 30 + fnLen);
        const filename = new TextDecoder('utf-8').decode(fnBytes);
        const dataOffset = offset + 30 + fnLen + extraLen;

        let compData = null;
        if ((flags & 8) !== 0 && compSize === 0) {
          let nextSig = dataOffset;
          while (nextSig < len - 4) {
            const s = view.getUint32(nextSig, true);
            if (s === 0x08074b50 || s === 0x04034b50 || s === 0x02014b50) break;
            nextSig++;
          }
          compData = uint8.subarray(dataOffset, nextSig);
          offset = nextSig;
        } else {
          compData = uint8.subarray(dataOffset, dataOffset + compSize);
          offset = dataOffset + compSize;
        }

        if (compMethod === 0) {
          files[filename] = compData;
        } else if (compMethod === 8 && compData) {
          const uncomp = await inflateBytes(compData);
          if (uncomp) files[filename] = uncomp;
        }
      } else if (sig === 0x02014b50) { // Central Directory
        const compMethod = view.getUint16(offset + 10, true);
        const compSize = view.getUint32(offset + 20, true);
        const uncompSize = view.getUint32(offset + 24, true);
        const fnLen = view.getUint16(offset + 28, true);
        const extraLen = view.getUint16(offset + 30, true);
        const commentLen = view.getUint16(offset + 32, true);
        const localHdrOffset = view.getUint32(offset + 42, true);

        const fnBytes = uint8.subarray(offset + 46, offset + 46 + fnLen);
        const filename = new TextDecoder('utf-8').decode(fnBytes);

        if (!files[filename] && compSize > 0 && localHdrOffset < len - 30) {
          const locFnLen = view.getUint16(localHdrOffset + 26, true);
          const locExtraLen = view.getUint16(localHdrOffset + 28, true);
          const locDataOffset = localHdrOffset + 30 + locFnLen + locExtraLen;
          const compData = uint8.subarray(locDataOffset, locDataOffset + compSize);

          if (compMethod === 0) {
            files[filename] = compData;
          } else if (compMethod === 8) {
            const uncomp = await inflateBytes(compData);
            if (uncomp) files[filename] = uncomp;
          }
        }
        offset += 46 + fnLen + extraLen + commentLen;
      } else {
        offset++;
      }
    }

    return files;
  },

  async _fetchOfficeBinary(streamURL) {
    const headers = {
      'ngrok-skip-browser-warning': 'true'
    };
    try {
      const u = localStorage.getItem('cloudpool_current_user');
      if (u) {
        const user = JSON.parse(u);
        if (user && user.id) headers['X-User-ID'] = user.id;
      }
    } catch (_) {}

    const res = await fetch(streamURL, {
      headers,
      credentials: 'include'
    });
    if (!res.ok) {
      let errMsg = `Lỗi kết nối HTTP ${res.status}`;
      try {
        const errJson = await res.json();
        if (errJson && errJson.error) errMsg = errJson.error;
      } catch(_) {
        try { errMsg = await res.text() || errMsg; } catch(e) {}
      }
      throw new Error(errMsg);
    }
    const buf = await res.arrayBuffer();
    if (buf.byteLength === 0) {
      throw new Error('Server trả về file rỗng (0 bytes). Có thể token Google Drive đã hết hạn hoặc dữ liệu phân mảnh bị lỗi.');
    }
    return buf;
  },

  // Fallback card thông báo lỗi mềm mại và chuyên nghiệp
  renderErrorFallback(container, options = {}) {
    const {
      fileName = 'Tệp tin',
      fileType = 'Tài liệu',
      fileIcon = '📁',
      error = null,
      downloadURL = '#',
      onRetry = null
    } = options;

    const errMsg = (error && error.message) ? error.message : (typeof error === 'string' ? error : 'Không thể xử lý định dạng tệp tin này.');
    const isDriveToken = /token|502|0 bytes/i.test(errMsg);
    const isNotFound = /404|not found|không tìm thấy/i.test(errMsg);

    let adviceHtml = '';
    if (isDriveToken) {
      adviceHtml = `
        <div style="background: rgba(245, 158, 11, 0.1); border: 1px solid rgba(245, 158, 11, 0.3); border-radius: 8px; padding: 10px 14px; margin: 12px 0; text-align: left; font-size: 12px; color: #fbbf24;">
          <b>⚠️ Lưu ý tài khoản Google Drive:</b> Token phiên làm việc có thể đã hết hạn. Hãy vào mục <b>Tài khoản Drive</b> để bấm <b>Làm mới Token</b> hoặc tải lại tệp lên hệ thống.
        </div>
      `;
    } else if (isNotFound) {
      adviceHtml = `
        <div style="background: rgba(239, 68, 68, 0.1); border: 1px solid rgba(239, 68, 68, 0.3); border-radius: 8px; padding: 10px 14px; margin: 12px 0; text-align: left; font-size: 12px; color: #f87171;">
          <b>⚠️ Không tìm thấy dữ liệu:</b> Phân mảnh tệp tin trên đám mây có thể đã bị di chuyển hoặc xoá. Vui lòng kiểm tra lại trạng thái tệp tin.
        </div>
      `;
    } else {
      adviceHtml = `
        <div style="font-size: 12.5px; color: var(--text-muted, #94a3b8); margin-bottom: 18px; line-height: 1.6;">
          Trình duyệt không thể kết xuất trực quan toàn bộ cấu trúc ${fileType} này (${errMsg}).<br>
          Bạn có thể tải tệp tin gốc về máy để mở trực tiếp bằng phần mềm chuyên dụng.
        </div>
      `;
    }

    container.innerHTML = `
      <div class="office-error-fallback-card" style="padding: 32px 20px; text-align: center; width: 100%; max-width: 580px; margin: 0 auto; box-sizing: border-box;">
        <div style="font-size: 52px; margin-bottom: 12px; line-height: 1;">${fileIcon}</div>
        <div style="font-size: 16px; font-weight: 700; color: var(--text-primary, #f1f5f9); margin-bottom: 4px; word-break: break-word;">${fileName}</div>
        <div style="font-size: 12px; color: var(--accent-amber, #f59e0b); font-weight: 600; margin-bottom: 12px;">${fileType}</div>
        ${adviceHtml}
        <div style="display: flex; gap: 10px; justify-content: center; flex-wrap: wrap; margin-top: 14px;">
          ${onRetry ? `
            <button id="btn-fallback-retry" class="btn btn-secondary btn-sm" style="padding: 8px 16px; font-size: 13px; gap: 6px;">
              <span>🔄</span><span>Thử Lại</span>
            </button>
          ` : ''}
          <a href="${downloadURL}" class="btn btn-primary btn-sm" download="${fileName}" style="padding: 8px 18px; font-size: 13px; gap: 6px; text-decoration: none;">
            <span>⬇️</span><span>Tải ${fileType} Về Máy</span>
          </a>
        </div>
      </div>
    `;

    if (onRetry) {
      container.querySelector('#btn-fallback-retry')?.addEventListener('click', () => {
        onRetry();
      });
    }
  },


  // ==========================================================================
  // 1. PDF DOCUMENT VIEWER (PRO Interactive Edition)
  // Thanh công cụ điều hướng trang, Zoom In/Out, Fit, Xoay, Tải về dự phòng
  // ==========================================================================
  async renderPDF(container, streamURL, fileName, downloadURL) {
    let currentPage = 1;
    let currentZoom = 100; // percent
    let currentRotation = 0; // degrees

    const updateIframeUrl = () => {
      const iframe = container.querySelector('#pdf-embed-frame');
      if (!iframe) return;
      // Many modern PDF viewers respect #page=X&zoom=Y
      const targetHash = `#page=${currentPage}&zoom=${currentZoom}`;
      try {
        const cleanBase = streamURL.split('#')[0];
        iframe.src = `${cleanBase}${targetHash}`;
      } catch (_) {}
    };

    const updateTransformStage = () => {
      const stage = container.querySelector('#pdf-stage-inner');
      const zoomText = container.querySelector('#pdf-zoom-val');
      if (zoomText) zoomText.textContent = `${currentZoom}%`;
      if (stage) {
        stage.style.transform = `scale(${currentZoom / 100}) rotate(${currentRotation}deg)`;
        stage.style.transformOrigin = 'top center';
      }
    };

    container.innerHTML = `
      <div class="pdf-viewer-wrapper" style="display: flex; flex-direction: column; width: 100%; height: 76vh; min-height: 480px; gap: 8px; box-sizing: border-box;">
        
        <!-- PDF Header Control Bar -->
        <div class="pdf-header-bar" style="display: flex; justify-content: space-between; align-items: center; background: rgba(239, 68, 68, 0.08); border: 1px solid rgba(239, 68, 68, 0.25); border-radius: 12px; padding: 8px 14px; gap: 8px; flex-wrap: wrap;">
          
          <div style="display: flex; align-items: center; gap: 10px; min-width: 0; flex: 1;">
            <span style="font-size: 22px; flex-shrink: 0;">📑</span>
            <div style="min-width: 0;">
              <div style="font-weight: 700; font-size: 13px; color: #ef4444; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 320px;" title="${fileName}">${fileName}</div>
              <div style="font-size: 11px; color: #94a3b8;">Tài liệu PDF nhúng • Điều hướng & Thu phóng thời gian thực</div>
            </div>
          </div>

          <!-- Navigation & Zoom Controls -->
          <div style="display: flex; align-items: center; gap: 6px; flex-wrap: wrap;">
            
            <!-- Page Navigation -->
            <div style="display: flex; align-items: center; background: rgba(255,255,255,0.06); border: 1px solid rgba(255,255,255,0.12); border-radius: 8px; padding: 2px 4px; gap: 3px;">
              <button id="pdf-btn-prev" class="btn btn-secondary btn-sm" title="Trang trước" style="padding: 3px 8px; font-size: 11px; min-width: 26px;">◀</button>
              <span style="font-size: 11px; color: #94a3b8; padding: 0 4px;">Trang</span>
              <input type="number" id="pdf-page-num" min="1" value="1" style="width: 44px; text-align: center; background: rgba(0,0,0,0.3); border: 1px solid rgba(255,255,255,0.15); color: #fff; font-size: 11px; padding: 2px 4px; border-radius: 4px; outline: none;" />
              <button id="pdf-btn-next" class="btn btn-secondary btn-sm" title="Trang sau" style="padding: 3px 8px; font-size: 11px; min-width: 26px;">▶</button>
            </div>

            <!-- Zoom Controls -->
            <div style="display: flex; align-items: center; background: rgba(255,255,255,0.06); border: 1px solid rgba(255,255,255,0.12); border-radius: 8px; padding: 2px 4px; gap: 4px;">
              <button id="pdf-btn-zoom-out" class="btn btn-secondary btn-sm" title="Thu nhỏ (-)" style="padding: 3px 8px; font-size: 12px; font-weight: bold;">➖</button>
              <span id="pdf-zoom-val" style="font-size: 11px; font-weight: 600; color: #cbd5e1; min-width: 40px; text-align: center;">100%</span>
              <button id="pdf-btn-zoom-in" class="btn btn-secondary btn-sm" title="Phóng to (+)" style="padding: 3px 8px; font-size: 12px; font-weight: bold;">➕</button>
              <button id="pdf-btn-zoom-reset" class="btn btn-secondary btn-sm" title="Vừa chiều rộng (100%)" style="padding: 3px 6px; font-size: 11px;">100%</button>
            </div>

            <!-- Rotate & Action Buttons -->
            <button id="pdf-btn-rotate" class="btn btn-secondary btn-sm" title="Xoay 90 độ" style="padding: 4px 8px; font-size: 11px;">
              <span>↻</span>
            </button>
            <button id="pdf-btn-print" class="btn btn-secondary btn-sm" title="In tài liệu" style="padding: 4px 8px; font-size: 11px;">
              <span>🖨️</span>
            </button>
            <a href="${streamURL}" target="_blank" rel="noopener noreferrer" class="btn btn-secondary btn-sm" title="Mở trong tab mới" style="padding: 4px 8px; font-size: 11px; text-decoration: none;">
              <span>↗️</span>
            </a>
            <a href="${downloadURL}" class="btn btn-primary btn-sm" download="${fileName}" title="Tải PDF về máy" style="padding: 4px 12px; font-size: 11px; gap: 4px; text-decoration: none;">
              <span>⬇️</span><span>Tải PDF</span>
            </a>

          </div>

        </div>

        <!-- PDF Stage Frame -->
        <div class="pdf-stage-container" style="flex: 1; min-height: 0; background: #2d3748; border: 1px solid rgba(255,255,255,0.1); border-radius: 12px; overflow: hidden; position: relative; display: flex; justify-content: center; align-items: stretch;">
          
          <!-- Loading Spinner indicator -->
          <div id="pdf-loading-indicator" style="position: absolute; top: 0; left: 0; width: 100%; height: 100%; display: flex; flex-direction: column; align-items: center; justify-content: center; background: rgba(15, 23, 42, 0.85); z-index: 5; gap: 12px; transition: opacity 0.3s;">
            <div style="font-size: 36px; animation: spin 1.2s linear infinite;">⏳</div>
            <div style="font-size: 14px; font-weight: 600; color: #ef4444;">Đang tải luồng tài liệu PDF...</div>
            <div style="font-size: 12px; color: #94a3b8;">Đang kết nối luồng đọc bảo mật từ đám mây</div>
          </div>

          <!-- Scalable Inner Wrapper -->
          <div id="pdf-stage-inner" style="width: 100%; height: 100%; transition: transform 0.2s cubic-bezier(0.4, 0, 0.2, 1); display: flex;">
            <iframe
              id="pdf-embed-frame"
              src="${streamURL}#page=1&zoom=100"
              style="width: 100%; height: 100%; border: none; background: #374151;"
              title="${fileName}"
              loading="lazy"
            ></iframe>
          </div>

        </div>

      </div>
    `;

    // Hook listeners
    const iframe = container.querySelector('#pdf-embed-frame');
    const loader = container.querySelector('#pdf-loading-indicator');
    const pageInput = container.querySelector('#pdf-page-num');

    if (iframe) {
      iframe.onload = () => {
        if (loader) {
          loader.style.opacity = '0';
          setTimeout(() => { loader.style.display = 'none'; }, 300);
        }
      };

      // In case iframe load hangs or is blocked by third-party plugin policy
      setTimeout(() => {
        if (loader && loader.style.display !== 'none') {
          loader.style.opacity = '0';
          setTimeout(() => { loader.style.display = 'none'; }, 300);
        }
      }, 5000);
    }

    // Prev / Next Page
    container.querySelector('#pdf-btn-prev')?.addEventListener('click', () => {
      if (currentPage > 1) {
        currentPage--;
        if (pageInput) pageInput.value = currentPage;
        updateIframeUrl();
      }
    });

    container.querySelector('#pdf-btn-next')?.addEventListener('click', () => {
      currentPage++;
      if (pageInput) pageInput.value = currentPage;
      updateIframeUrl();
    });

    pageInput?.addEventListener('change', (e) => {
      const val = parseInt(e.target.value) || 1;
      currentPage = Math.max(1, val);
      updateIframeUrl();
    });

    // Zoom Controls
    container.querySelector('#pdf-btn-zoom-in')?.addEventListener('click', () => {
      if (currentZoom < 250) {
        currentZoom += 25;
        updateTransformStage();
      }
    });

    container.querySelector('#pdf-btn-zoom-out')?.addEventListener('click', () => {
      if (currentZoom > 50) {
        currentZoom -= 25;
        updateTransformStage();
      }
    });

    container.querySelector('#pdf-btn-zoom-reset')?.addEventListener('click', () => {
      currentZoom = 100;
      updateTransformStage();
    });

    // Rotate
    container.querySelector('#pdf-btn-rotate')?.addEventListener('click', () => {
      currentRotation = (currentRotation + 90) % 360;
      updateTransformStage();
    });

    // Print
    container.querySelector('#pdf-btn-print')?.addEventListener('click', () => {
      try {
        const frame = container.querySelector('#pdf-embed-frame');
        if (frame && frame.contentWindow) {
          frame.contentWindow.focus();
          frame.contentWindow.print();
        } else {
          window.print();
        }
      } catch (_) {
        window.open(streamURL, '_blank');
      }
    });
  },


  // ==========================================================================
  // 2. EXCEL & SPREADSHEET VIEWER (PRO Edition)
  // Hỗ trợ XLSX, XLS (BIFF8/BIFF5), CSV, TSV, ODS.
  // Tab chuyển Sheet, lưới bảng tính sắc nét, ô tìm kiếm thời gian thực, formula bar.
  // ==========================================================================
  async renderExcel(container, streamURL, fileName, downloadURL) {
    container.innerHTML = `
      <div style="padding: 40px 16px; text-align: center; color: var(--text-muted, #94a3b8); width: 100%;">
        <div style="font-size: 36px; margin-bottom: 12px; animation: spin 1.5s linear infinite;">⏳</div>
        <div style="font-size: 15px; font-weight: 600; color: #10b981;">Đang nạp và phân tích bảng tính Excel...</div>
        <div style="font-size: 12px; margin-top: 6px; color: #94a3b8;">Xử lý các trang tính, đường lưới ô và công thức trực tiếp trong trình duyệt</div>
      </div>
    `;

    try {
      const arrayBuffer = await this._fetchOfficeBinary(streamURL);
      let sheets = [];

      // 1. Primary engine: SheetJS (xlsx.full.min.js) for .xlsx, .xls, .csv, .tsv, .ods
      if (typeof XLSX !== 'undefined') {
        try {
          const workbook = XLSX.read(arrayBuffer, {
            type: 'array',
            cellDates: true,
            cellNF: true,
            cellText: true,
            raw: false
          });

          if (workbook && workbook.SheetNames && workbook.SheetNames.length > 0) {
            for (const sheetName of workbook.SheetNames) {
              const worksheet = workbook.Sheets[sheetName];
              if (!worksheet) continue;
              const rawData = XLSX.utils.sheet_to_json(worksheet, { header: 1, defval: '', raw: false });
              if (Array.isArray(rawData) && rawData.length > 0) {
                let maxCols = 0;
                rawData.forEach(row => {
                  if (Array.isArray(row) && row.length > maxCols) maxCols = row.length;
                });
                if (maxCols === 0) maxCols = 1;

                const normalizedRows = rawData.map(row => {
                  const r = Array.isArray(row) ? [...row] : [row];
                  while (r.length < maxCols) r.push('');
                  return r.map(c => (c !== null && c !== undefined ? String(c) : ''));
                });

                // Trim trailing empty rows
                while (normalizedRows.length > 0 && normalizedRows[normalizedRows.length - 1].every(c => c.trim() === '')) {
                  normalizedRows.pop();
                }

                if (normalizedRows.length > 0) {
                  sheets.push({ name: sheetName, rows: normalizedRows });
                }
              }
            }
          }
        } catch (xlsxErr) {
          console.warn('SheetJS engine parse warning:', xlsxErr);
        }
      }

      // 2. Secondary fallback: Unzip XML structure for .xlsx
      if (sheets.length === 0) {
        try {
          const zipFiles = await this.unzipArrayBuffer(arrayBuffer);
          const textDecoder = new TextDecoder('utf-8');

          if (zipFiles['xl/workbook.xml'] || zipFiles['xl/sharedStrings.xml']) {
            const sharedStrings = [];
            if (zipFiles['xl/sharedStrings.xml']) {
              const ssXml = textDecoder.decode(zipFiles['xl/sharedStrings.xml']);
              const siRegex = /<si\b[^>]*>([\s\S]*?)<\/si>/gi;
              let siMatch;
              while ((siMatch = siRegex.exec(ssXml)) !== null) {
                const siContent = siMatch[1];
                const tRegex = /<t\b[^>]*>([\s\S]*?)<\/t>/gi;
                let tMatch;
                let sText = '';
                while ((tMatch = tRegex.exec(siContent)) !== null) {
                  sText += tMatch[1];
                }
                sharedStrings.push(sText);
              }
            }

            const sheetDefs = [];
            if (zipFiles['xl/workbook.xml']) {
              const wbXml = textDecoder.decode(zipFiles['xl/workbook.xml']);
              const sheetRegex = /<sheet\b[^>]*name="([^"]+)"[^>]*sheetId="(\d+)"/gi;
              let sMatch;
              while ((sMatch = sheetRegex.exec(wbXml)) !== null) {
                sheetDefs.push({ name: sMatch[1], id: sMatch[2] });
              }
            }

            const wsKeys = Object.keys(zipFiles).filter(k => /^xl\/worksheets\/sheet\d+\.xml$/i.test(k));
            wsKeys.sort((a, b) => {
              const nA = parseInt(a.match(/\d+/)[0]);
              const nB = parseInt(b.match(/\d+/)[0]);
              return nA - nB;
            });

            for (let i = 0; i < wsKeys.length; i++) {
              const wsKey = wsKeys[i];
              const wsXml = textDecoder.decode(zipFiles[wsKey]);
              const sheetName = (sheetDefs[i] ? sheetDefs[i].name : `Sheet ${i + 1}`);

              const rowsData = [];
              const rowRegex = /<row\b[^>]*>([\s\S]*?)<\/row>/gi;
              let rMatch;

              while ((rMatch = rowRegex.exec(wsXml)) !== null) {
                const rowContent = rMatch[1];
                const cRegex = /<c\b[^>]*r="([A-Z]+)(\d+)"(?:\b[^>]*t="([^"]+)")?[^>]*>([\s\S]*?)<\/c>/gi;
                let cMatch;
                const rowCells = {};
                let maxColIdx = 0;

                while ((cMatch = cRegex.exec(rowContent)) !== null) {
                  const colLetters = cMatch[1];
                  const cType = cMatch[3];
                  const cBody = cMatch[4];

                  let val = '';
                  const vMatch = cBody.match(/<v\b[^>]*>([\s\S]*?)<\/v>/i);
                  if (vMatch) {
                    val = vMatch[1];
                  } else {
                    const isMatch = cBody.match(/<is\b[^>]*><t\b[^>]*>([\s\S]*?)<\/t><\/is>/i);
                    if (isMatch) val = isMatch[1];
                  }

                  if (cType === 's') {
                    const idx = parseInt(val);
                    val = (sharedStrings[idx] !== undefined ? sharedStrings[idx] : val);
                  }

                  let colIdx = 0;
                  for (let k = 0; k < colLetters.length; k++) {
                    colIdx = colIdx * 26 + (colLetters.charCodeAt(k) - 64);
                  }
                  colIdx -= 1;

                  rowCells[colIdx] = val;
                  if (colIdx > maxColIdx) maxColIdx = colIdx;
                }

                const finalRow = [];
                for (let c = 0; c <= maxColIdx; c++) {
                  finalRow.push(rowCells[c] || '');
                }
                if (finalRow.some(cell => cell.toString().trim().length > 0)) {
                  rowsData.push(finalRow);
                }
              }

              if (rowsData.length > 0) {
                sheets.push({ name: sheetName, rows: rowsData });
              }
            }
          }
        } catch (_) {}
      }

      // 3. Third fallback: Text / CSV / TSV / Delimited parser
      if (sheets.length === 0) {
        const text = new TextDecoder('utf-8').decode(arrayBuffer);
        const parseDelimitedText = (txt) => {
          const lines = txt.split(/\r?\n/).filter(l => l.trim().length > 0);
          if (lines.length === 0) return [];

          // Determine separator: \t, comma, or semicolon
          const sample = lines[0];
          const tabCount = (sample.match(/\t/g) || []).length;
          const commaCount = (sample.match(/,/g) || []).length;
          const semiCount = (sample.match(/;/g) || []).length;

          let sep = ',';
          if (tabCount >= commaCount && tabCount >= semiCount && tabCount > 0) sep = '\t';
          else if (semiCount > commaCount && semiCount > 0) sep = ';';

          // RFC-4180 robust line splitter
          const parseRow = (line) => {
            const result = [];
            let inQuotes = false;
            let current = '';
            for (let i = 0; i < line.length; i++) {
              const char = line[i];
              if (char === '"') {
                if (inQuotes && line[i + 1] === '"') {
                  current += '"';
                  i++;
                } else {
                  inQuotes = !inQuotes;
                }
              } else if (char === sep && !inQuotes) {
                result.push(current.trim());
                current = '';
              } else {
                current += char;
              }
            }
            result.push(current.trim());
            return result;
          };

          return lines.map(line => parseRow(line));
        };

        const rows = parseDelimitedText(text);
        if (rows.length > 0) {
          const baseName = fileName.replace(/\.[^/.]+$/, '');
          sheets.push({ name: baseName || 'Dữ liệu', rows });
        }
      }

      if (sheets.length === 0) {
        this.renderErrorFallback(container, {
          fileName,
          fileType: 'Bảng tính Excel',
          fileIcon: '📊',
          error: new Error('Không thể trích xuất cấu trúc dữ liệu bảng tính này.'),
          downloadURL,
          onRetry: () => this.renderExcel(container, streamURL, fileName, downloadURL)
        });
        return;
      }

      this._renderExcelUI(container, sheets, fileName, downloadURL);

    } catch (err) {
      console.warn('Excel render error:', err);
      this.renderErrorFallback(container, {
        fileName,
        fileType: 'Bảng tính Excel',
        fileIcon: '📊',
        error: err,
        downloadURL,
        onRetry: () => this.renderExcel(container, streamURL, fileName, downloadURL)
      });
    }
  },

  _renderExcelUI(container, sheets, fileName, downloadURL) {
    let activeSheetIdx = 0;
    let selectedCell = { colLetter: 'A', rowNum: 1, val: '' };

    container.innerHTML = `
      <div class="excel-viewer-wrapper" style="display: flex; flex-direction: column; gap: 8px; width: 100%; height: 76vh; min-height: 480px; box-sizing: border-box;">
        
        <!-- Header Toolbar -->
        <div style="display: flex; justify-content: space-between; align-items: center; background: rgba(16, 185, 129, 0.08); border: 1px solid rgba(16, 185, 129, 0.25); border-radius: 12px; padding: 8px 14px; gap: 8px; flex-wrap: wrap;">
          
          <div style="display: flex; align-items: center; gap: 10px; min-width: 0; flex: 1;">
            <span style="font-size: 22px; flex-shrink: 0;">📊</span>
            <div style="min-width: 0;">
              <div style="font-weight: 700; font-size: 13px; color: #10b981; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 320px;" title="${fileName}">${fileName}</div>
              <div style="font-size: 11px; color: #94a3b8;" id="excel-stat-info">Đang phân tích bảng tính...</div>
            </div>
          </div>
          
          <!-- Actions & Live Search -->
          <div style="display: flex; gap: 6px; align-items: center; flex-wrap: wrap;">
            
            <div style="position: relative; display: flex; align-items: center;">
              <input type="text" id="excel-search-input" placeholder="🔍 Tìm kiếm ô & dòng..." style="background: rgba(255,255,255,0.06); border: 1px solid rgba(255,255,255,0.18); color: #fff; font-size: 11px; padding: 5px 28px 5px 10px; border-radius: 8px; outline: none; width: 170px;" />
              <button id="excel-search-clear" style="position: absolute; right: 6px; background: none; border: none; color: #94a3b8; font-size: 12px; cursor: pointer; display: none;" title="Xóa tìm kiếm">✕</button>
            </div>

            <button id="excel-btn-export-csv" class="btn btn-secondary btn-sm" title="Xuất sheet hiện tại thành CSV" style="font-size: 11px; padding: 5px 10px; gap: 4px;">
              <span>📑</span><span class="btn-label"> Xuất CSV</span>
            </button>

            <a href="${downloadURL}" class="btn btn-primary btn-sm" download="${fileName}" title="Tải tệp gốc về máy" style="font-size: 11px; padding: 5px 12px; gap: 4px; text-decoration: none;">
              <span>⬇️</span><span class="btn-label"> Tải File</span>
            </a>

          </div>

        </div>

        <!-- Formula / Active Cell Inspector Bar -->
        <div class="excel-formula-bar" style="display: flex; align-items: center; gap: 8px; background: rgba(0, 0, 0, 0.35); border: 1px solid rgba(255, 255, 255, 0.08); border-radius: 8px; padding: 4px 10px; font-size: 12px;">
          <span id="excel-cell-coords" style="font-weight: 700; color: #10b981; font-family: monospace; min-width: 45px; text-align: center; background: rgba(16, 185, 129, 0.12); padding: 2px 6px; border-radius: 4px; border: 1px solid rgba(16, 185, 129, 0.3);">A1</span>
          <span style="font-weight: bold; color: #64748b; font-style: italic; user-select: none;">fx</span>
          <div id="excel-cell-value-display" style="flex: 1; min-width: 0; color: #e2e8f0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; font-family: monospace; font-size: 11.5px;">-</div>
          <button id="excel-btn-copy-cell" class="btn btn-secondary btn-sm" title="Sao chép nội dung ô này" style="font-size: 10px; padding: 2px 6px;">📋 Chép</button>
        </div>

        <!-- High-DPI Sharp Table Grid Container -->
        <div class="excel-table-scroll-stage" style="flex: 1; min-height: 0; background: #0b0f19; border: 1px solid rgba(255,255,255,0.1); border-radius: 10px; overflow: auto; position: relative;">
          <table id="excel-data-table" style="width: 100%; border-collapse: collapse; font-size: 12px; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', sans-serif;">
            <thead id="excel-table-head" style="position: sticky; top: 0; z-index: 10;"></thead>
            <tbody id="excel-table-body"></tbody>
          </table>
        </div>

        <!-- Sheet Selector Tabs Bar -->
        <div style="display: flex; justify-content: space-between; align-items: center; padding: 6px 12px; background: rgba(0,0,0,0.5); border-top: 1px solid rgba(255,255,255,0.08); border-radius: 0 0 10px 10px; gap: 10px; overflow-x: auto;">
          <div style="display: flex; gap: 6px; overflow-x: auto; padding-bottom: 2px;" id="excel-sheet-tabs">
            ${sheets.map((s, idx) => `
              <button class="excel-tab-btn ${idx === 0 ? 'active' : ''}" data-idx="${idx}" style="padding: 5px 14px; font-size: 11px; font-weight: 600; border-radius: 6px; border: 1px solid ${idx === 0 ? '#10b981' : 'rgba(255,255,255,0.1)'}; background: ${idx === 0 ? 'rgba(16,185,129,0.2)' : 'rgba(255,255,255,0.03)'}; color: ${idx === 0 ? '#34d399' : '#94a3b8'}; cursor: pointer; white-space: nowrap; display: flex; align-items: center; gap: 5px;">
                <span>📋</span><span>${s.name}</span><span style="font-size: 9.5px; opacity: 0.75;">(${s.rows.length})</span>
              </button>
            `).join('')}
          </div>
          <div style="font-size: 11px; color: #94a3b8; white-space: nowrap;" id="excel-row-col-count"></div>
        </div>

      </div>
    `;

    const getColLetter = (idx) => {
      let letter = '';
      let temp = idx;
      while (temp >= 0) {
        letter = String.fromCharCode((temp % 26) + 65) + letter;
        temp = Math.floor(temp / 26) - 1;
      }
      return letter;
    };

    const renderSheetData = (sheetIdx, filterQuery = '') => {
      const sheet = sheets[sheetIdx];
      const thead = container.querySelector('#excel-table-head');
      const tbody = container.querySelector('#excel-table-body');
      const statEl = container.querySelector('#excel-stat-info');
      const countEl = container.querySelector('#excel-row-col-count');
      const clearBtn = container.querySelector('#excel-search-clear');
      if (!tbody || !sheet) return;

      const q = (filterQuery || '').toLowerCase().trim();
      if (clearBtn) clearBtn.style.display = q ? 'block' : 'none';

      let maxCols = 0;
      sheet.rows.forEach(r => { if (r.length > maxCols) maxCols = r.length; });

      let filteredRows = sheet.rows;
      if (q) {
        filteredRows = sheet.rows.filter((row, rIdx) => {
          if (rIdx === 0) return true; // Keep header row
          return row.some(cell => cell.toString().toLowerCase().includes(q));
        });
      }

      if (statEl) statEl.textContent = `Sheet: ${sheet.name} • ${sheet.rows.length} dòng • ${maxCols} cột`;
      if (countEl) countEl.textContent = q ? `Khớp ${filteredRows.length - 1} / ${sheet.rows.length - 1} dòng` : `Tổng cộng ${sheet.rows.length} dòng`;

      // Sticky Header Columns
      let headHtml = '<tr style="background: #161f30; box-shadow: 0 2px 4px rgba(0,0,0,0.5);">';
      headHtml += '<th style="padding: 7px 10px; width: 48px; text-align: center; color: #64748b; border: 1px solid rgba(255,255,255,0.08); font-size: 10px; position: sticky; left: 0; background: #161f30; z-index: 12; user-select: none;">#</th>';
      for (let c = 0; c < maxCols; c++) {
        headHtml += `<th style="padding: 7px 14px; text-align: center; color: #cbd5e1; border: 1px solid rgba(255,255,255,0.08); font-weight: 700; font-size: 11px; min-width: 90px; user-select: none;">${getColLetter(c)}</th>`;
      }
      headHtml += '</tr>';
      if (thead) thead.innerHTML = headHtml;

      // Table Body
      let bodyHtml = '';
      const renderLimit = Math.min(filteredRows.length, 1000);

      const highlightMatch = (text) => {
        if (!q || !text) return text;
        const str = String(text);
        const idx = str.toLowerCase().indexOf(q);
        if (idx === -1) return str;
        const before = str.substring(0, idx);
        const match = str.substring(idx, idx + q.length);
        const after = str.substring(idx + q.length);
        return `${before}<mark style="background: rgba(245, 158, 11, 0.35); color: #fef08a; padding: 0 2px; border-radius: 2px;">${match}</mark>${after}`;
      };

      for (let r = 0; r < renderLimit; r++) {
        const row = filteredRows[r];
        const isHeaderRow = (r === 0);
        const rowBg = isHeaderRow ? 'background: rgba(16,185,129,0.1); font-weight: 700;' : (r % 2 === 0 ? 'background: rgba(255,255,255,0.015);' : 'background: transparent;');

        bodyHtml += `<tr class="excel-grid-row" style="${rowBg} transition: background 0.1s;">`;
        // Sticky row index
        bodyHtml += `<td style="padding: 6px 8px; text-align: center; color: #64748b; border: 1px solid rgba(255,255,255,0.05); font-size: 10px; user-select: none; position: sticky; left: 0; background: #0e1422; z-index: 5;">${r + 1}</td>`;

        for (let c = 0; c < maxCols; c++) {
          const rawVal = row[c] !== undefined ? row[c] : '';
          const colL = getColLetter(c);
          const cellColor = isHeaderRow ? '#34d399' : '#f1f5f9';
          bodyHtml += `
            <td
              class="excel-grid-cell"
              data-col="${colL}"
              data-row="${r + 1}"
              data-val="${encodeURIComponent(rawVal)}"
              style="padding: 6px 12px; border: 1px solid rgba(255,255,255,0.05); color: ${cellColor}; white-space: nowrap; max-width: 380px; overflow: hidden; text-overflow: ellipsis; cursor: cell;"
              title="${rawVal}"
            >
              ${highlightMatch(rawVal)}
            </td>
          `;
        }
        bodyHtml += '</tr>';
      }

      if (filteredRows.length > 1000) {
        bodyHtml += `<tr><td colspan="${maxCols + 1}" style="text-align: center; padding: 14px; color: #94a3b8; font-style: italic; background: rgba(0,0,0,0.2);">Đã hiển thị 1,000 / ${filteredRows.length} dòng đầu tiên. Hãy tải tệp về để xem trọn vẹn toàn bộ bảng tính lớn.</td></tr>`;
      }

      tbody.innerHTML = bodyHtml;

      // Cell selection and click listener
      tbody.querySelectorAll('.excel-grid-cell').forEach(td => {
        td.addEventListener('click', () => {
          tbody.querySelectorAll('.excel-grid-cell.selected').forEach(c => c.classList.remove('selected'));
          td.classList.add('selected');
          td.style.outline = '2px solid #10b981';
          td.style.outlineOffset = '-2px';

          const col = td.getAttribute('data-col');
          const row = td.getAttribute('data-row');
          const val = decodeURIComponent(td.getAttribute('data-val') || '');
          selectedCell = { colLetter: col, rowNum: row, val };

          const coordsEl = container.querySelector('#excel-cell-coords');
          const valDisplayEl = container.querySelector('#excel-cell-value-display');
          if (coordsEl) coordsEl.textContent = `${col}${row}`;
          if (valDisplayEl) valDisplayEl.textContent = val || '(ô trống)';
        });
      });
    };

    // Sheet tab switching
    container.querySelectorAll('.excel-tab-btn').forEach(btn => {
      btn.addEventListener('click', () => {
        activeSheetIdx = parseInt(btn.getAttribute('data-idx') || '0');
        container.querySelectorAll('.excel-tab-btn').forEach((b, i) => {
          const active = (i === activeSheetIdx);
          b.style.borderColor = active ? '#10b981' : 'rgba(255,255,255,0.1)';
          b.style.background = active ? 'rgba(16,185,129,0.2)' : 'rgba(255,255,255,0.03)';
          b.style.color = active ? '#34d399' : '#94a3b8';
        });
        const q = container.querySelector('#excel-search-input')?.value || '';
        renderSheetData(activeSheetIdx, q);
      });
    });

    // Real-time live search with debounce
    let searchTimeout = null;
    const searchInput = container.querySelector('#excel-search-input');
    searchInput?.addEventListener('input', (e) => {
      clearTimeout(searchTimeout);
      searchTimeout = setTimeout(() => {
        renderSheetData(activeSheetIdx, e.target.value);
      }, 180);
    });

    container.querySelector('#excel-search-clear')?.addEventListener('click', () => {
      if (searchInput) searchInput.value = '';
      renderSheetData(activeSheetIdx, '');
    });

    // Copy single cell content
    container.querySelector('#excel-btn-copy-cell')?.addEventListener('click', () => {
      if (selectedCell && selectedCell.val) {
        navigator.clipboard.writeText(selectedCell.val).then(() => {
          if (typeof Toast !== 'undefined' && Toast.success) Toast.success(`Đã sao chép giá trị ô ${selectedCell.colLetter}${selectedCell.rowNum}`);
        }).catch(() => {});
      }
    });

    // Export current sheet to CSV
    container.querySelector('#excel-btn-export-csv')?.addEventListener('click', () => {
      const activeSheet = sheets[activeSheetIdx];
      if (!activeSheet || !activeSheet.rows) return;

      const csvContent = activeSheet.rows.map(r =>
        r.map(c => `"${String(c).replace(/"/g, '""')}"`).join(',')
      ).join('\r\n');

      const blob = new Blob(['\uFEFF' + csvContent], { type: 'text/csv;charset=utf-8;' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `${activeSheet.name}_export.csv`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
    });

    // Initial render
    renderSheetData(0);
  },


  // ==========================================================================
  // 3. WORD DOCUMENT VIEWER (PRO Edition)
  // Hỗ trợ .docx và .doc.
  // Render chuẩn xác tiêu đề, đoạn văn, bảng biểu, danh sách qua docx-preview & Mammoth.
  // ==========================================================================
  async renderDocx(container, streamURL, fileName, downloadURL) {
    container.innerHTML = `
      <div style="padding: 40px 16px; text-align: center; color: var(--text-muted, #94a3b8); width: 100%;">
        <div style="font-size: 36px; margin-bottom: 12px; animation: spin 1.5s linear infinite;">⏳</div>
        <div style="font-size: 15px; font-weight: 600; color: #38bdf8;">Đang mở và kết xuất tài liệu Word...</div>
        <div style="font-size: 12px; margin-top: 6px; color: #94a3b8;">Xử lý định dạng văn bản, tiêu đề, bảng biểu và danh sách trực tiếp</div>
      </div>
    `;

    try {
      const arrayBuffer = await this._fetchOfficeBinary(streamURL);
      const isDocOld = /\.doc$/i.test(fileName);

      // Check if old binary .doc (OLE2 Compound File format)
      if (isDocOld) {
        // Try extracting text strings from OLE2 binary stream
        try {
          const uint8 = new Uint8Array(arrayBuffer);
          let extractedText = '';
          let tempAscii = '';
          for (let i = 0; i < uint8.length; i++) {
            const byte = uint8[i];
            if (byte >= 32 && byte <= 126) {
              tempAscii += String.fromCharCode(byte);
            } else if (byte === 10 || byte === 13) {
              if (tempAscii.length >= 4) {
                extractedText += tempAscii + '\n';
              }
              tempAscii = '';
            } else {
              if (tempAscii.length >= 4) {
                extractedText += tempAscii + ' ';
              }
              tempAscii = '';
            }
          }

          this._renderWordDocumentUI(container, {
            mode: 'doc_legacy',
            rawText: extractedText.trim(),
            fileName,
            downloadURL
          });
          return;
        } catch (_) {
          // Fall through to error fallback
        }
      }

      // 1. Premier renderer: docx-preview (Pixel-perfect Microsoft Word layout)
      if (typeof docx !== 'undefined' && typeof docx.renderAsync === 'function') {
        try {
          this._renderWordDocumentUI(container, {
            mode: 'docx_preview',
            arrayBuffer,
            fileName,
            downloadURL
          });
          return;
        } catch (docxErr) {
          console.warn('docx-preview failed, falling back to Mammoth:', docxErr);
        }
      }

      // 2. Secondary renderer: Mammoth.js (Rich HTML converter for headings, tables, lists)
      if (typeof mammoth !== 'undefined' && typeof mammoth.convertToHtml === 'function') {
        try {
          const result = await mammoth.convertToHtml({ arrayBuffer });
          if (result && result.value && result.value.trim().length > 0) {
            this._renderWordDocumentUI(container, {
              mode: 'mammoth',
              htmlContent: result.value,
              fileName,
              downloadURL
            });
            return;
          }
        } catch (mammothErr) {
          console.warn('Mammoth parser failed, falling back to OpenXML:', mammothErr);
        }
      }

      // 3. Native OpenXML fallback parser
      const zipFiles = await this.unzipArrayBuffer(arrayBuffer);
      const textDecoder = new TextDecoder('utf-8');

      if (!zipFiles['word/document.xml']) {
        throw new Error('Không tìm thấy tệp cấu trúc word/document.xml trong tệp DOCX.');
      }

      const docXml = textDecoder.decode(zipFiles['word/document.xml']);
      let parsedHtml = '';
      const pRegex = /<w:p\b[^>]*>([\s\S]*?)<\/w:p>/gi;
      let pMatch;

      while ((pMatch = pRegex.exec(docXml)) !== null) {
        const pBlock = pMatch[1];
        const isHeading = /<w:pStyle\b[^>]*w:val="(Heading\d|Title)"/i.test(pBlock);

        let pText = '';
        const rRegex = /<w:r\b[^>]*>([\s\S]*?)<\/w:r>/gi;
        let rMatch;

        while ((rMatch = rRegex.exec(pBlock)) !== null) {
          const rBlock = rMatch[1];
          const isBold = /<w:b\b/i.test(rBlock);
          const isItalic = /<w:i\b/i.test(rBlock);

          let runText = '';
          const tRegex = /<w:t\b[^>]*>([\s\S]*?)<\/w:t>/gi;
          let tMatch;
          while ((tMatch = tRegex.exec(rBlock)) !== null) {
            runText += tMatch[1];
          }

          if (runText) {
            let chunk = runText.replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&');
            if (isBold) chunk = `<b>${chunk}</b>`;
            if (isItalic) chunk = `<i>${chunk}</i>`;
            pText += chunk;
          }
        }

        if (pText.trim()) {
          if (isHeading) {
            parsedHtml += `<h3 style="font-size: 18px; font-weight: 700; color: #0284c7; margin: 18px 0 8px 0; border-bottom: 2px solid #e0f2fe; padding-bottom: 6px;">${pText}</h3>`;
          } else {
            parsedHtml += `<p style="font-size: 14px; line-height: 1.7; color: #334155; margin-bottom: 12px;">${pText}</p>`;
          }
        }
      }

      this._renderWordDocumentUI(container, {
        mode: 'mammoth',
        htmlContent: parsedHtml,
        fileName,
        downloadURL
      });

    } catch (err) {
      console.warn('Docx render error:', err);
      this.renderErrorFallback(container, {
        fileName,
        fileType: 'Tài liệu Word',
        fileIcon: '📄',
        error: err,
        downloadURL,
        onRetry: () => this.renderDocx(container, streamURL, fileName, downloadURL)
      });
    }
  },

  _renderWordDocumentUI(container, options = {}) {
    const { mode, arrayBuffer, htmlContent, rawText, fileName, downloadURL } = options;
    let isDarkMode = false;
    let currentZoom = 100;

    container.innerHTML = `
      <div class="docx-viewer-wrapper" style="display: flex; flex-direction: column; width: 100%; height: 76vh; min-height: 480px; gap: 8px; box-sizing: border-box;">
        
        <!-- Header Toolbar -->
        <div class="docx-header-bar" style="display: flex; justify-content: space-between; align-items: center; background: rgba(56, 189, 248, 0.08); border: 1px solid rgba(56, 189, 248, 0.25); border-radius: 12px; padding: 8px 14px; gap: 8px; flex-wrap: wrap;">
          
          <div style="display: flex; align-items: center; gap: 10px; min-width: 0; flex: 1;">
            <span style="font-size: 22px; flex-shrink: 0;">📄</span>
            <div style="min-width: 0;">
              <div style="font-weight: 700; font-size: 13px; color: #38bdf8; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 320px;" title="${fileName}">${fileName}</div>
              <div style="font-size: 11px; color: #94a3b8;">Tài liệu Văn bản Word • Trích xuất cấu trúc & Giấy in</div>
            </div>
          </div>

          <!-- Actions Toolbar -->
          <div style="display: flex; align-items: center; gap: 6px; flex-wrap: wrap;">
            
            <!-- Zoom Controls -->
            <div style="display: flex; align-items: center; background: rgba(255,255,255,0.06); border: 1px solid rgba(255,255,255,0.12); border-radius: 8px; padding: 2px 4px; gap: 4px;">
              <button id="docx-btn-zoom-out" class="btn btn-secondary btn-sm" title="Thu nhỏ chữ (-)" style="padding: 3px 8px; font-size: 11px;">➖</button>
              <span id="docx-zoom-val" style="font-size: 11px; font-weight: 600; color: #cbd5e1; min-width: 36px; text-align: center;">100%</span>
              <button id="docx-btn-zoom-in" class="btn btn-secondary btn-sm" title="Phóng to chữ (+)" style="padding: 3px 8px; font-size: 11px;">➕</button>
            </div>

            <!-- Dark / Paper Mode Toggle -->
            <button id="docx-btn-theme-toggle" class="btn btn-secondary btn-sm" title="Chuyển chế độ Nền tối / Giấy trắng" style="font-size: 11px; padding: 4px 8px;">
              <span id="docx-theme-icon">🌙</span><span class="btn-label"> Nền tối</span>
            </button>

            <!-- Copy All Text -->
            <button id="docx-btn-copy" class="btn btn-secondary btn-sm" title="Sao chép toàn bộ văn bản" style="font-size: 11px; padding: 4px 8px;">
              <span>📋</span><span class="btn-label"> Sao chép</span>
            </button>

            <!-- Print -->
            <button id="docx-btn-print" class="btn btn-secondary btn-sm" title="In tài liệu" style="font-size: 11px; padding: 4px 8px;">
              <span>🖨️</span><span class="btn-label"> In</span>
            </button>

            <!-- Download -->
            <a href="${downloadURL}" class="btn btn-primary btn-sm" download="${fileName}" title="Tải tệp Word về máy" style="font-size: 11px; padding: 4px 12px; gap: 4px; text-decoration: none;">
              <span>⬇️</span><span class="btn-label"> Tải File</span>
            </a>

          </div>

        </div>

        <!-- Document Paper Canvas Stage -->
        <div class="docx-stage-wrapper" id="docx-stage-wrapper" style="flex: 1; min-height: 0; background: #0f172a; border: 1px solid rgba(255,255,255,0.1); border-radius: 12px; overflow-y: auto; padding: clamp(14px, 2.5vw, 36px); display: flex; justify-content: center; box-sizing: border-box;">
          
          <div id="docx-paper-sheet" class="docx-paper-sheet" style="width: 100%; max-width: 860px; min-height: 100%; background: #ffffff; color: #1e293b; padding: clamp(24px, 4vw, 56px); border-radius: 6px; box-shadow: 0 10px 30px rgba(0,0,0,0.4); font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif; line-height: 1.7; font-size: 14px; box-sizing: border-box; transition: background 0.2s, color 0.2s;">
            
            ${mode === 'doc_legacy' ? `
              <div style="background: rgba(245, 158, 11, 0.1); border: 1px solid rgba(245, 158, 11, 0.3); border-radius: 8px; padding: 12px 16px; margin-bottom: 20px; font-size: 13px; color: #b45309;">
                <b>📌 Định dạng Word 97-2003 (.doc nhị phân):</b> Trình duyệt hiển thị trích xuất nội dung văn bản. Để hiển thị chuẩn xác các bảng biểu và hình ảnh đồ họa đầy đủ, bạn có thể tải tệp về để mở trực tiếp trong Microsoft Word.
              </div>
              <div style="white-space: pre-wrap; font-family: inherit; word-break: break-word;">
                ${rawText || '(Tài liệu không có nội dung văn bản khả dụng)'}
              </div>
            ` : mode === 'docx_preview' ? `
              <div id="docx-preview-render-target"></div>
            ` : `
              <div class="docx-mammoth-body" style="word-break: break-word;">
                ${htmlContent || '<div style="color: #94a3b8; text-align: center; padding: 40px;">(Tài liệu trống)</div>'}
              </div>
            `}

          </div>

        </div>

      </div>
    `;

    // If docx-preview mode, trigger async render
    if (mode === 'docx_preview' && arrayBuffer && typeof docx !== 'undefined') {
      const target = container.querySelector('#docx-preview-render-target');
      if (target) {
        docx.renderAsync(arrayBuffer, target, null, {
          className: 'docx-preview-content',
          inWrapper: false,
          ignoreWidth: false,
          ignoreHeight: false
        }).catch(err => {
          console.warn('docx.renderAsync inner failure:', err);
          target.innerHTML = `<div style="color: #ef4444; padding: 20px;">Không thể kết xuất trang in: ${err.message}</div>`;
        });
      }
    }

    // Toggle Dark Mode / Paper White
    const themeBtn = container.querySelector('#docx-btn-theme-toggle');
    const themeIcon = container.querySelector('#docx-theme-icon');
    const sheet = container.querySelector('#docx-paper-sheet');

    themeBtn?.addEventListener('click', () => {
      isDarkMode = !isDarkMode;
      if (sheet) {
        sheet.style.background = isDarkMode ? '#1e293b' : '#ffffff';
        sheet.style.color = isDarkMode ? '#f1f5f9' : '#1e293b';
      }
      if (themeIcon) themeIcon.textContent = isDarkMode ? '☀️' : '🌙';
      const label = themeBtn.querySelector('.btn-label');
      if (label) label.textContent = isDarkMode ? ' Giấy trắng' : ' Nền tối';
    });

    // Zoom Font Size
    const zoomVal = container.querySelector('#docx-zoom-val');
    container.querySelector('#docx-btn-zoom-in')?.addEventListener('click', () => {
      if (currentZoom < 180) {
        currentZoom += 15;
        if (sheet) sheet.style.fontSize = `${14 * (currentZoom / 100)}px`;
        if (zoomVal) zoomVal.textContent = `${currentZoom}%`;
      }
    });

    container.querySelector('#docx-btn-zoom-out')?.addEventListener('click', () => {
      if (currentZoom > 70) {
        currentZoom -= 15;
        if (sheet) sheet.style.fontSize = `${14 * (currentZoom / 100)}px`;
        if (zoomVal) zoomVal.textContent = `${currentZoom}%`;
      }
    });

    // Copy Content
    container.querySelector('#docx-btn-copy')?.addEventListener('click', () => {
      if (sheet) {
        const text = sheet.innerText || sheet.textContent || '';
        navigator.clipboard.writeText(text).then(() => {
          if (typeof Toast !== 'undefined' && Toast.success) Toast.success('Đã sao chép nội dung văn bản Word!');
        }).catch(() => {});
      }
    });

    // Print
    container.querySelector('#docx-btn-print')?.addEventListener('click', () => {
      window.print();
    });
  },


  // ==========================================================================
  // 4. POWERPOINT PRESENTATION (.PPTX) VIEWER (PRO Edition)
  // Hai chế độ: Trình chiếu Slide Deck (Stage Mode) & Toàn bộ tài liệu (Outline List)
  // Trích xuất tiêu đề, danh sách, bảng biểu, hình ảnh nhúng.
  // ==========================================================================
  async renderPPTX(container, streamURL, fileName, downloadURL) {
    container.innerHTML = `
      <div style="padding: 40px 16px; text-align: center; color: var(--text-muted, #94a3b8); width: 100%;">
        <div style="font-size: 36px; margin-bottom: 12px; animation: spin 1.5s linear infinite;">⏳</div>
        <div style="font-size: 15px; font-weight: 600; color: #f59e0b;">Đang nạp và trích xuất các trang slide PowerPoint...</div>
        <div style="font-size: 12px; margin-top: 6px; color: #94a3b8;">Xử lý nội dung văn bản, bảng biểu, sơ đồ và hình ảnh trực tiếp trong trình duyệt</div>
      </div>
    `;

    try {
      const arrayBuffer = await this._fetchOfficeBinary(streamURL);
      const zipFiles = await this.unzipArrayBuffer(arrayBuffer);
      const textDecoder = new TextDecoder('utf-8');

      // Create blob URLs for extracted media images
      const mediaMap = {};
      for (const [path, data] of Object.entries(zipFiles)) {
        if (path.startsWith('ppt/media/')) {
          let mime = 'image/png';
          if (/\.(jpg|jpeg)$/i.test(path)) mime = 'image/jpeg';
          else if (/\.svg$/i.test(path)) mime = 'image/svg+xml';
          else if (/\.gif$/i.test(path)) mime = 'image/gif';
          else if (/\.webp$/i.test(path)) mime = 'image/webp';

          const blob = new Blob([data], { type: mime });
          mediaMap[path] = URL.createObjectURL(blob);
          const shortName = path.split('/').pop();
          mediaMap[shortName] = mediaMap[path];
        }
      }

      // Collect all slides in numerical order
      const slideKeys = Object.keys(zipFiles).filter(k => /^ppt\/slides\/slide\d+\.xml$/i.test(k));
      slideKeys.sort((a, b) => {
        const numA = parseInt(a.match(/\d+/)[0]);
        const numB = parseInt(b.match(/\d+/)[0]);
        return numA - numB;
      });

      if (slideKeys.length === 0) {
        throw new Error('Không tìm thấy trang slide nào trong tệp tin PowerPoint.');
      }

      const parsedSlides = [];
      for (let i = 0; i < slideKeys.length; i++) {
        const slideKey = slideKeys[i];
        const slideXml = textDecoder.decode(zipFiles[slideKey]);

        // Check relationships for this slide
        const relsKey = slideKey.replace('ppt/slides/', 'ppt/slides/_rels/') + '.rels';
        let relsXml = '';
        if (zipFiles[relsKey]) {
          relsXml = textDecoder.decode(zipFiles[relsKey]);
        }

        const slideInfo = this._parseSingleSlideXml(slideXml, relsXml, mediaMap, i + 1);
        parsedSlides.push(slideInfo);
      }

      this._renderSlideDeckUI(container, parsedSlides, fileName, downloadURL);

    } catch (err) {
      console.warn('PPTX native render error:', err);
      this.renderErrorFallback(container, {
        fileName,
        fileType: 'Bản trình chiếu PowerPoint',
        fileIcon: '📽️',
        error: err,
        downloadURL,
        onRetry: () => this.renderPPTX(container, streamURL, fileName, downloadURL)
      });
    }
  },

  _parseSingleSlideXml(slideXml, relsXml, mediaMap, slideNum) {
    const slide = { num: slideNum, title: '', paragraphs: [], tables: [], images: [] };

    // 1. Extract images referenced in slide rels
    if (relsXml) {
      for (const m of relsXml.matchAll(/Target="([^"]+)"/g)) {
        const target = m[1].replace('../', 'ppt/');
        if (mediaMap[target]) {
          slide.images.push(mediaMap[target]);
        } else {
          const shortName = target.split('/').pop();
          if (mediaMap[shortName]) slide.images.push(mediaMap[shortName]);
        }
      }
    }

    // 2. Extract tables (<a:tbl>)
    for (const tblM of slideXml.matchAll(/<a:tbl[\s>][\s\S]*?<\/a:tbl>/gi)) {
      const tableRows = [];
      for (const trM of tblM[0].matchAll(/<a:tr[\s>][\s\S]*?<\/a:tr>/gi)) {
        const rowCells = [];
        for (const tcM of trM[0].matchAll(/<a:tc[\s>][\s\S]*?<\/a:tc>/gi)) {
          let cellText = '';
          for (const tM of tcM[0].matchAll(/<a:t[\s>]([\s\S]*?)<\/a:t>/gi)) { cellText += tM[1]; }
          rowCells.push(cellText.trim());
        }
        if (rowCells.some(c => c.length > 0)) tableRows.push(rowCells);
      }
      if (tableRows.length > 0) slide.tables.push(tableRows);
    }

    // 3. Per-shape text extraction
    for (const spM of slideXml.matchAll(/<p:sp[\s>]([\s\S]*?)<\/p:sp>/gi)) {
      const spBlock = spM[1];
      const isTitleShape = /<p:ph\b[^>]*\btype="(title|ctrTitle|subTitle)"/i.test(spBlock);

      const shapeParagraphs = [];
      for (const pM of spBlock.matchAll(/<a:p[\s>]([\s\S]*?)<\/a:p>/gi)) {
        const pBlock = pM[1];
        let text = '';
        for (const tM of pBlock.matchAll(/<a:t[\s>]([\s\S]*?)<\/a:t>/gi)) { text += tM[1]; }
        text = text.trim();
        if (!text) continue;
        const isBold = /<a:rPr\b[^>]*\bb="1"/i.test(pBlock);
        const lvlM = pBlock.match(/\blvl="(\d+)"/i);
        shapeParagraphs.push({ text, bold: isBold, level: lvlM ? parseInt(lvlM[1]) : 0 });
      }
      if (shapeParagraphs.length === 0) continue;

      if (isTitleShape && !slide.title) {
        slide.title = shapeParagraphs[0].text;
        for (let i = 1; i < shapeParagraphs.length; i++) slide.paragraphs.push(shapeParagraphs[i]);
      } else {
        for (const p of shapeParagraphs) slide.paragraphs.push(p);
      }
    }

    // Fallback: no explicit title shape found -> promote first body paragraph
    if (!slide.title && slide.paragraphs.length > 0) slide.title = slide.paragraphs.shift().text;
    if (!slide.title) slide.title = `Slide ${slideNum}`;
    return slide;
  },

  _renderSlideDeckUI(container, slides, fileName, downloadURL) {
    let currentIdx = 0;
    let isOutlineView = false;

    container.innerHTML = `
      <style>
        .pptx-mobile-nav-bar { display: flex; gap: 8px; overflow-x: auto; padding-bottom: 4px; }
        .pptx-mobile-pill { background: rgba(255,255,255,0.1); padding: 4px 12px; border-radius: 20px; font-size: 12px; cursor: pointer; white-space: nowrap; color: #94a3b8; }
        .pptx-mobile-pill.active { background: #f59e0b; color: #fff; font-weight: bold; }
        .pptx-thumbnails-sidebar { width: 220px; display: flex; flex-direction: column; gap: 8px; overflow-y: auto; padding-right: 4px; }
        .pptx-thumbnails-sidebar::-webkit-scrollbar { width: 4px; }
        .pptx-thumbnails-sidebar::-webkit-scrollbar-thumb { background: rgba(255,255,255,0.1); border-radius: 4px; }
        .pptx-thumb-item { background: rgba(255,255,255,0.03); border-radius: 8px; padding: 10px; cursor: pointer; border: 1px solid transparent; transition: all 0.2s; }
        .pptx-thumb-item:hover { background: rgba(255,255,255,0.08); }
        .pptx-thumb-item.active { background: rgba(245,158,11,0.12); border-color: rgba(245,158,11,0.4); }
        .pptx-thumb-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 6px; }
        .pptx-thumb-num { font-size: 11px; font-weight: 700; color: #94a3b8; }
        .pptx-thumb-title { font-size: 12px; color: #cbd5e1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
        .pptx-thumb-item.active .pptx-thumb-title { color: #f59e0b; font-weight: 600; }
        @media (max-width: 768px) { .pptx-thumbnails-sidebar { display: none !important; } }
        @media (min-width: 769px) { .pptx-mobile-nav-bar { display: none !important; } }
      </style>
      <div class="pptx-player-wrapper" id="pptx-player-main" style="display: flex; flex-direction: column; gap: 8px; width: 100%; height: 76vh; min-height: 480px; box-sizing: border-box;">
        
        <!-- Header Bar -->
        <div class="pptx-header-bar" style="display: flex; justify-content: space-between; align-items: center; background: rgba(245, 158, 11, 0.08); border: 1px solid rgba(245, 158, 11, 0.25); border-radius: 12px; padding: 8px 14px; gap: 8px; flex-wrap: wrap;">
          
          <div class="pptx-header-left" style="display: flex; align-items: center; gap: 10px; min-width: 0; flex: 1;">
            <span style="font-size: 22px; flex-shrink: 0;">📽️</span>
            <div style="min-width: 0;">
              <div class="pptx-file-name" style="font-weight: 700; font-size: 13px; color: #f59e0b; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 340px;" title="${fileName}">${fileName}</div>
              <div class="pptx-meta-info" style="font-size: 11px; color: #94a3b8; white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">Tổng cộng: <b>${slides.length}</b> slide • Trình chiếu tương tác</div>
            </div>
          </div>

          <div class="pptx-header-actions" style="display: flex; gap: 6px; align-items: center; flex-wrap: wrap;">
            
            <!-- Quick Jump Dropdown -->
            <select class="pptx-slide-select" id="pptx-slide-select" title="Chuyển nhanh tới slide" style="background: rgba(255, 255, 255, 0.06); border: 1px solid rgba(255, 255, 255, 0.15); color: #f1f5f9; font-size: 11px; padding: 4px 8px; border-radius: 8px; outline: none; cursor: pointer; max-width: 150px;">
              ${slides.map((s, idx) => `<option value="${idx}">Trang ${s.num}: ${s.title.substring(0, 18)}...</option>`).join('')}
            </select>

            <!-- Toggle View Mode: Stage vs Outline List -->
            <button id="pptx-btn-view-toggle" class="btn btn-secondary btn-sm" title="Chuyển chế độ Xem Trình Chiếu / Danh Sách Cuộn" style="font-size: 11px; padding: 4px 9px;">
              <span id="pptx-view-mode-icon">📜</span><span id="pptx-view-mode-label" class="btn-label"> Dạng Danh Sách</span>
            </button>

            <!-- Copy Slide Content -->
            <button id="pptx-btn-copy-slide" class="btn btn-secondary btn-sm" title="Sao chép văn bản slide hiện tại" style="font-size: 11px; padding: 4px 8px;">
              <span>📋</span><span class="btn-label"> Sao chép</span>
            </button>

            <!-- Fullscreen -->
            <button id="pptx-btn-fullscreen" class="btn btn-secondary btn-sm" title="Toàn màn hình" style="font-size: 11px; padding: 4px 9px;">
              <span>🖥️</span><span class="btn-label"> Toàn Màn</span>
            </button>

            <!-- Download File -->
            <a href="${downloadURL}" class="btn btn-primary btn-sm" download="${fileName}" title="Tải file gốc" style="font-size: 11px; padding: 4px 12px; text-decoration: none;">
              <span>⬇️</span><span class="btn-label"> Tải File</span>
            </a>

          </div>

        </div>

        <!-- Mobile Horizontal Slide Navigator Strip -->
        <div class="pptx-mobile-nav-bar" id="pptx-mobile-nav-bar">
          ${slides.map((s, idx) => `
            <div class="pptx-mobile-pill ${idx === 0 ? 'active' : ''}" data-index="${idx}">
              Trang ${s.num}
            </div>
          `).join('')}
        </div>

        <!-- Stage View Container -->
        <div id="pptx-stage-view-wrapper" class="pptx-stage-container" style="display: flex; gap: 12px; flex: 1; min-height: 0; box-sizing: border-box;">
          
          <!-- Desktop Left Thumbnails Sidebar -->
          <div id="pptx-thumbnails-bar" class="pptx-thumbnails-sidebar">
            ${slides.map((s, idx) => `
              <div class="pptx-thumb-item ${idx === 0 ? 'active' : ''}" data-index="${idx}">
                <div class="pptx-thumb-header">
                  <span class="pptx-thumb-num">Trang ${s.num}</span>
                  <span style="font-size: 8px; color: ${idx === 0 ? '#f59e0b' : 'rgba(255,255,255,0.2)'};">●</span>
                </div>
                <div class="pptx-thumb-title">${s.title}</div>
              </div>
            `).join('')}
          </div>

          <!-- Center Slide Stage -->
          <div id="pptx-slide-stage" class="pptx-slide-stage" style="flex: 1; min-width: 0; background: linear-gradient(145deg, #090d16 0%, #131c31 100%); border: 1px solid rgba(255, 255, 255, 0.1); border-radius: 14px; display: flex; flex-direction: column; overflow: hidden; box-shadow: 0 12px 36px -8px rgba(0, 0, 0, 0.6); position: relative;">
            
            <!-- Slide Progress Bar -->
            <div class="pptx-progress-track" style="height: 3px; width: 100%; background: rgba(255, 255, 255, 0.05);">
              <div id="pptx-progress-bar" class="pptx-progress-fill" style="height: 100%; background: linear-gradient(90deg, #f59e0b, #38bdf8); width: ${(1 / slides.length) * 100}%;"></div>
            </div>

            <!-- Slide Canvas Content -->
            <div id="pptx-slide-canvas" class="pptx-slide-canvas" style="flex: 1; padding: clamp(14px, 2.5vw, 32px); overflow-y: auto; display: flex; flex-direction: column; justify-content: flex-start; gap: 16px; box-sizing: border-box; touch-action: pan-y;">
              <!-- Rendered slide content -->
            </div>

            <!-- Slide Bottom Navigator -->
            <div class="pptx-bottom-nav" style="display: flex; justify-content: space-between; align-items: center; padding: 8px 16px; background: rgba(0, 0, 0, 0.45); border-top: 1px solid rgba(255, 255, 255, 0.08); gap: 8px; flex-shrink: 0;">
              <button id="pptx-btn-prev" class="btn btn-secondary pptx-nav-btn" style="font-size: 12px; padding: 6px 14px;">
                ◀ <span>Trước</span>
              </button>

              <div class="pptx-counter-badge" id="pptx-counter-text" style="font-size: 13px; font-weight: 700; color: #f59e0b;">
                Trang 1 / ${slides.length}
              </div>

              <button id="pptx-btn-next" class="btn btn-primary pptx-nav-btn" style="font-size: 12px; padding: 6px 14px;">
                <span>Sau</span> ▶
              </button>
            </div>

          </div>

        </div>

        <!-- Continuous Document Outline List View (Hidden by default) -->
        <div id="pptx-outline-view-wrapper" style="display: none; flex: 1; min-height: 0; background: #0b0f19; border: 1px solid rgba(255,255,255,0.1); border-radius: 12px; overflow-y: auto; padding: 20px; box-sizing: border-box;">
          <div style="max-width: 860px; margin: 0 auto; display: flex; flex-direction: column; gap: 20px;">
            ${slides.map(s => `
              <div class="pptx-outline-card" style="background: rgba(255,255,255,0.03); border: 1px solid rgba(255,255,255,0.08); border-radius: 10px; padding: 20px; display: flex; flex-direction: column; gap: 12px;">
                <div style="display: flex; justify-content: space-between; align-items: center; border-bottom: 1px solid rgba(245, 158, 11, 0.25); padding-bottom: 8px;">
                  <span style="font-size: 15px; font-weight: 700; color: #f59e0b;">Slide ${s.num}: ${s.title}</span>
                  <span style="font-size: 11px; color: #94a3b8; background: rgba(255,255,255,0.06); padding: 2px 8px; border-radius: 12px;">Trang ${s.num}</span>
                </div>
                ${s.paragraphs.length > 0 ? `
                  <div style="display: flex; flex-direction: column; gap: 6px;">
                    ${s.paragraphs.map(p => `
                      <div style="margin-left: ${p.level * 16}px; font-size: 13px; color: ${p.bold ? '#fff' : '#cbd5e1'}; font-weight: ${p.bold ? '700' : '400'};">
                        • ${p.text}
                      </div>
                    `).join('')}
                  </div>
                ` : ''}
                ${s.tables && s.tables.length > 0 ? `
                  <div style="width: 100%; overflow-x: auto; margin-top: 8px;">
                    ${s.tables.map(tbl => `
                      <table style="width: 100%; border-collapse: collapse; font-size: 12px;">
                        ${tbl.map((row, rIdx) => `
                          <tr>
                            ${row.map(cell => `
                              <${rIdx === 0 ? 'th' : 'td'} style="padding: 6px 10px; border: 1px solid rgba(255,255,255,0.08); ${rIdx === 0 ? 'background: rgba(245,158,11,0.12); color:#f59e0b;' : 'color:#cbd5e1;'}">${cell}</${rIdx === 0 ? 'th' : 'td'}>
                            `).join('')}
                          </tr>
                        `).join('')}
                      </table>
                    `).join('')}
                  </div>
                ` : ''}
                ${s.images && s.images.length > 0 ? `
                  <div style="display: flex; flex-wrap: wrap; gap: 10px; justify-content: center; margin-top: 10px;">
                    ${s.images.map(img => `<img src="${img}" style="max-height: 160px; border-radius: 6px; border: 1px solid rgba(255,255,255,0.1);" />`).join('')}
                  </div>
                ` : ''}
              </div>
            `).join('')}
          </div>
        </div>

      </div>
    `;

    const renderCurrentSlide = () => {
      const s = slides[currentIdx];
      const canvas      = container.querySelector('#pptx-slide-canvas');
      const counter     = container.querySelector('#pptx-counter-text');
      const btnPrev     = container.querySelector('#pptx-btn-prev');
      const btnNext     = container.querySelector('#pptx-btn-next');
      const progressBar = container.querySelector('#pptx-progress-bar');
      const slideSelect = container.querySelector('#pptx-slide-select');

      if (!canvas) return;

      if (counter) counter.textContent = `Trang ${currentIdx + 1} / ${slides.length}`;
      if (btnPrev) btnPrev.disabled = (currentIdx === 0);
      if (btnNext) btnNext.disabled = (currentIdx === slides.length - 1);
      if (progressBar) progressBar.style.width = `${((currentIdx + 1) / slides.length) * 100}%`;
      if (slideSelect) slideSelect.value = currentIdx;

      container.querySelectorAll('.pptx-thumb-item').forEach((el, idx) => {
        const isActive = (idx === currentIdx);
        el.classList.toggle('active', isActive);
        if (isActive) el.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
      });

      container.querySelectorAll('.pptx-mobile-pill').forEach((el, idx) => {
        const isActive = (idx === currentIdx);
        el.classList.toggle('active', isActive);
        if (isActive) el.scrollIntoView({ behavior: 'smooth', inline: 'center', block: 'nearest' });
      });

      let contentHtml = `
        <div class="pptx-slide-title-card" style="border-bottom: 2px solid #f59e0b; padding-bottom: 10px; margin-bottom: 4px;">
          <h2 class="pptx-slide-title" style="font-size: clamp(17px, 3.2vw, 24px); font-weight: 700; color: #f8fafc; margin: 0; line-height: 1.35; word-break: break-word;">
            ${s.title}
          </h2>
        </div>
      `;

      const isMobile = window.innerWidth <= 768;
      if (s.paragraphs.length > 0) {
        contentHtml += `<div class="pptx-bullet-list" style="display: flex; flex-direction: column; gap: 10px; margin-top: 6px;">`;
        s.paragraphs.forEach(p => {
          const indent = isMobile ? p.level * 10 : p.level * 20;
          const bullet = p.level === 0 ? '📌' : (p.level === 1 ? '🔹' : '▪️');
          contentHtml += `
            <div class="pptx-bullet-item" style="display: flex; align-items: flex-start; gap: 8px; margin-left: ${indent}px;">
              <span style="font-size: 13px; margin-top: 2px; flex-shrink: 0;">${bullet}</span>
              <div class="pptx-bullet-text" style="font-size: clamp(13px, 2.2vw, 15px); line-height: 1.6; word-break: break-word; color: ${p.bold ? '#ffffff' : '#cbd5e1'}; font-weight: ${p.bold ? '700' : '400'};">
                ${p.text}
              </div>
            </div>
          `;
        });
        contentHtml += `</div>`;
      } else if (!s.tables || s.tables.length === 0) {
        if (!s.images || s.images.length === 0) {
          contentHtml += `
            <div style="color: #94a3b8; font-size: 13px; font-style: italic; margin-top: 14px;">
              (Slide tiêu đề — không có nội dung phụ)
            </div>
          `;
        }
      }

      if (s.tables && s.tables.length > 0) {
        s.tables.forEach(tbl => {
          contentHtml += `<div class="pptx-slide-table-wrapper" style="width: 100%; overflow-x: auto; margin-top: 12px; border-radius: 8px; border: 1px solid rgba(255, 255, 255, 0.1);"><table class="pptx-slide-table" style="width: 100%; border-collapse: collapse; font-size: clamp(11px, 2vw, 13px);">`;
          tbl.forEach((row, rIdx) => {
            const isHeader = (rIdx === 0);
            contentHtml += `<tr>`;
            row.forEach(cell => {
              const bg = isHeader ? 'background: rgba(245, 158, 11, 0.12); color: #f59e0b; font-weight: 700;' : 'color: #cbd5e1;';
              contentHtml += `<${isHeader ? 'th' : 'td'} style="padding: 8px 12px; border-bottom: 1px solid rgba(255, 255, 255, 0.06); text-align: left; ${bg}">${cell}</${isHeader ? 'th' : 'td'}>`;
            });
            contentHtml += `</tr>`;
          });
          contentHtml += `</table></div>`;
        });
      }

      if (s.images && s.images.length > 0) {
        contentHtml += `
          <div class="pptx-slide-images" style="display: flex; flex-wrap: wrap; gap: 12px; margin-top: 16px; justify-content: center; align-items: center;">
            ${s.images.map(imgSrc => `
              <img src="${imgSrc}" alt="Slide Media" loading="lazy" style="max-width: 100%; max-height: clamp(150px, 32vh, 260px); border-radius: 8px; object-fit: contain; border: 1px solid rgba(255, 255, 255, 0.12); background: rgba(0, 0, 0, 0.3); box-shadow: 0 4px 14px rgba(0, 0, 0, 0.4);" />
            `).join('')}
          </div>
        `;
      }

      canvas.innerHTML = contentHtml;
      canvas.scrollTop = 0;
    };

    // Navigation events
    container.querySelector('#pptx-btn-prev')?.addEventListener('click', () => {
      if (currentIdx > 0) { currentIdx--; renderCurrentSlide(); }
    });

    container.querySelector('#pptx-btn-next')?.addEventListener('click', () => {
      if (currentIdx < slides.length - 1) { currentIdx++; renderCurrentSlide(); }
    });

    container.querySelector('#pptx-slide-select')?.addEventListener('change', (e) => {
      currentIdx = parseInt(e.target.value) || 0;
      renderCurrentSlide();
    });

    container.querySelectorAll('.pptx-thumb-item').forEach(thumb => {
      thumb.addEventListener('click', () => {
        currentIdx = parseInt(thumb.getAttribute('data-index') || '0');
        renderCurrentSlide();
      });
    });

    container.querySelectorAll('.pptx-mobile-pill').forEach(pill => {
      pill.addEventListener('click', () => {
        currentIdx = parseInt(pill.getAttribute('data-index') || '0');
        renderCurrentSlide();
      });
    });

    // Toggle View Mode: Stage vs Outline List
    const viewToggleBtn = container.querySelector('#pptx-btn-view-toggle');
    const stageWrapper = container.querySelector('#pptx-stage-view-wrapper');
    const outlineWrapper = container.querySelector('#pptx-outline-view-wrapper');
    const viewIcon = container.querySelector('#pptx-view-mode-icon');
    const viewLabel = container.querySelector('#pptx-view-mode-label');

    viewToggleBtn?.addEventListener('click', () => {
      isOutlineView = !isOutlineView;
      if (stageWrapper) stageWrapper.style.display = isOutlineView ? 'none' : 'flex';
      if (outlineWrapper) outlineWrapper.style.display = isOutlineView ? 'flex' : 'none';
      if (viewIcon) viewIcon.textContent = isOutlineView ? '🎞️' : '📜';
      if (viewLabel) viewLabel.textContent = isOutlineView ? ' Trình Chiếu' : ' Dạng Danh Sách';
    });

    // Copy slide text
    container.querySelector('#pptx-btn-copy-slide')?.addEventListener('click', () => {
      const s = slides[currentIdx];
      let txt = `${s.title}\n\n`;
      s.paragraphs.forEach(p => { txt += `• ${p.text}\n`; });
      navigator.clipboard.writeText(txt.trim()).then(() => {
        if (typeof Toast !== 'undefined' && Toast.success) Toast.success(`Đã sao chép nội dung Slide ${s.num}!`);
      }).catch(() => {});
    });

    // Touch swipe navigation
    const stageEl = container.querySelector('#pptx-slide-stage');
    if (stageEl) {
      let touchStartX = 0;
      let touchStartY = 0;
      stageEl.addEventListener('touchstart', (e) => {
        touchStartX = e.changedTouches[0].screenX;
        touchStartY = e.changedTouches[0].screenY;
      }, { passive: true });

      stageEl.addEventListener('touchend', (e) => {
        const touchEndX = e.changedTouches[0].screenX;
        const touchEndY = e.changedTouches[0].screenY;
        const diffX = touchEndX - touchStartX;
        const diffY = touchEndY - touchStartY;
        if (Math.abs(diffX) > 40 && Math.abs(diffX) > Math.abs(diffY) * 1.5) {
          if (diffX < 0 && currentIdx < slides.length - 1) { currentIdx++; renderCurrentSlide(); }
          else if (diffX > 0 && currentIdx > 0) { currentIdx--; renderCurrentSlide(); }
        }
      }, { passive: true });
    }

    // Keyboard navigation
    const keyHandler = (e) => {
      if (isOutlineView) return;
      if (e.key === 'ArrowRight' || e.key === ' ' || e.key === 'PageDown') {
        if (currentIdx < slides.length - 1) { currentIdx++; renderCurrentSlide(); }
      } else if (e.key === 'ArrowLeft' || e.key === 'PageUp') {
        if (currentIdx > 0) { currentIdx--; renderCurrentSlide(); }
      }
    };
    window.removeEventListener('keydown', keyHandler);
    window.addEventListener('keydown', keyHandler);

    // Fullscreen
    container.querySelector('#pptx-btn-fullscreen')?.addEventListener('click', () => {
      const player = container.querySelector('#pptx-player-main');
      if (player) {
        if (!document.fullscreenElement) player.requestFullscreen?.().catch(() => {});
        else document.exitFullscreen?.().catch(() => {});
      }
    });

    setTimeout(() => { renderCurrentSlide(); }, 0);
  }

};
