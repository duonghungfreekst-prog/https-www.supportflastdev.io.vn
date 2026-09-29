// ==========================================================================
// CloudPool Native In-Browser Office & Presentation Renderer (Zero External Dependency)
// Supports .pptx (PowerPoint), .docx (Word), .xlsx / .csv (Excel)
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
          // Search for next signature
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
      // Parse JSON error message from backend for user-friendly display
      let errMsg = `Lỗi HTTP ${res.status}`;
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
      throw new Error('Server trả về file rỗng (0 bytes). Có thể token Google Drive đã hết hạn hoặc dữ liệu bị lỗi.');
    }
    return buf;
  },


  // ────────────────────────────────────────────────────────
  // PowerPoint Presentation (.pptx) Native Renderer (PRO Edition)
  // ────────────────────────────────────────────────────────
  async renderPPTX(container, streamURL, fileName, downloadURL) {
    container.innerHTML = `
      <div style="padding: 40px 16px; text-align: center; color: var(--text-muted, #94a3b8); width: 100%;">
        <div style="font-size: 36px; margin-bottom: 12px; animation: spin 1.5s linear infinite;">⏳</div>
        <div style="font-size: 15px; font-weight: 600; color: #f59e0b;">Đang tải và giải nén các trang slide PowerPoint...</div>
        <div style="font-size: 12px; margin-top: 6px; color: #94a3b8;">Đang trích xuất nội dung văn bản, bảng biểu và hình ảnh trực tiếp trong trình duyệt</div>
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
      container.innerHTML = `
        <div style="padding: 30px 16px; text-align: center; width: 100%;">
          <div style="font-size: 48px; margin-bottom: 12px;">📽️</div>
          <div style="font-size: 16px; font-weight: 700; color: #f59e0b; margin-bottom: 6px; word-break: break-word;">${fileName}</div>
          <div style="font-size: 13px; color: #94a3b8; margin-bottom: 20px; line-height: 1.5;">
            Không thể trích xuất toàn bộ slide trực tiếp (${err.message}).<br>
            Bạn có thể tải tệp gốc về máy để mở bằng PowerPoint.
          </div>
          <div style="display: flex; gap: 10px; justify-content: center; flex-wrap: wrap;">
            <a href="${downloadURL}" class="btn btn-primary btn-sm" download="${fileName}" style="padding: 8px 18px; font-size: 13px;">
              ⬇️ Tải File PPTX Về Máy
            </a>
          </div>
        </div>
      `;
    }
  },

  // ─────────────────────────────────────────────────────────────
  // Parse a single slide XML into structured slide data.
  // IMPORTANT: Uses per-shape (p:sp) iteration to correctly
  // distinguish title placeholders (p:ph type="title"/"ctrTitle")
  // from body content shapes. This prevents body text from being
  // incorrectly swallowed into the slide title.
  // ─────────────────────────────────────────────────────────────
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

    // 3. Per-shape text extraction — correctly identifies title vs body shapes
    // ONLY shapes with p:ph type="title"/"ctrTitle"/"subTitle" are title shapes.
    // All other shapes (body, text box, object placeholder) are body.
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
        // First paragraph of title shape = slide title
        slide.title = shapeParagraphs[0].text;
        // Remaining lines in title shape go to body
        for (let i = 1; i < shapeParagraphs.length; i++) slide.paragraphs.push(shapeParagraphs[i]);
      } else {
        // All paragraphs from body/content shapes go to body list
        for (const p of shapeParagraphs) slide.paragraphs.push(p);
      }
    }

    // Fallback: no explicit title shape found → promote first body paragraph
    if (!slide.title && slide.paragraphs.length > 0) slide.title = slide.paragraphs.shift().text;
    if (!slide.title) slide.title = `Slide ${slideNum}`;
    return slide;
  },


  _renderSlideDeckUI(container, slides, fileName, downloadURL) {
    let currentIdx = 0;

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
      <div class="pptx-player-wrapper" id="pptx-player-main" style="display: flex; flex-direction: column; gap: 10px; width: 100%; height: 76vh; min-height: 480px; user-select: none; box-sizing: border-box;">
        
        <!-- Header Bar -->
        <div class="pptx-header-bar" style="display: flex; justify-content: space-between; align-items: center; background: rgba(245, 158, 11, 0.08); border: 1px solid rgba(245, 158, 11, 0.25); border-radius: 12px; padding: 8px 14px; gap: 10px; flex-wrap: wrap;">
          <div class="pptx-header-left" style="display: flex; align-items: center; gap: 10px; min-width: 0; flex: 1;">
            <span style="font-size: 22px; flex-shrink: 0;">📽️</span>
            <div style="min-width: 0;">
              <div class="pptx-file-name" style="font-weight: 700; font-size: 13px; color: #f59e0b; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 480px;" title="${fileName}">${fileName}</div>
              <div class="pptx-meta-info" style="font-size: 11px; color: #94a3b8; white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">Tổng số: <b>${slides.length}</b> trang slide • Trình chiếu tương tác</div>
            </div>
          </div>
          <div class="pptx-header-actions" style="display: flex; gap: 8px; align-items: center; flex-shrink: 0;">
            <select class="pptx-slide-select" id="pptx-slide-select" title="Chuyển nhanh tới slide" style="background: rgba(255, 255, 255, 0.06); border: 1px solid rgba(255, 255, 255, 0.15); color: #f1f5f9; font-size: 11px; padding: 4px 8px; border-radius: 8px; outline: none; cursor: pointer;">
              ${slides.map((s, idx) => `<option value="${idx}">Trang ${s.num}: ${s.title.substring(0, 24)}...</option>`).join('')}
            </select>
            <button id="pptx-btn-fullscreen" class="btn btn-secondary btn-sm" title="Toàn màn hình" style="font-size: 11px; padding: 4px 10px;">
              <span>🖥️</span><span class="btn-label"> Toàn Màn</span>
            </button>
            <a href="${downloadURL}" class="btn btn-primary btn-sm" download="${fileName}" title="Tải file gốc" style="font-size: 11px; padding: 4px 12px;">
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

        <!-- Main Slide Presentation Stage -->
        <div class="pptx-stage-container" style="display: flex; gap: 12px; flex: 1; min-height: 0; box-sizing: border-box;">
          
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

      </div>
    `;

    const renderCurrentSlide = () => {
      const s = slides[currentIdx];
      // Use container.querySelector (scoped) — NOT document.getElementById (global)
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

    const keyHandler = (e) => {
      if (e.key === 'ArrowRight' || e.key === ' ' || e.key === 'PageDown') {
        if (currentIdx < slides.length - 1) { currentIdx++; renderCurrentSlide(); }
      } else if (e.key === 'ArrowLeft' || e.key === 'PageUp') {
        if (currentIdx > 0) { currentIdx--; renderCurrentSlide(); }
      }
    };
    window.removeEventListener('keydown', keyHandler);
    window.addEventListener('keydown', keyHandler);

    container.querySelector('#pptx-btn-fullscreen')?.addEventListener('click', () => {
      const player = container.querySelector('#pptx-player-main');
      if (player) {
        if (!document.fullscreenElement) player.requestFullscreen?.().catch(() => {});
        else document.exitFullscreen?.().catch(() => {});
      }
    });

    // setTimeout(0) ensures DOM is painted before first slide render
    setTimeout(() => { renderCurrentSlide(); }, 0);

  },

  // ────────────────────────────────────────────────────────
  // 2. Excel Spreadsheet (.xlsx / .xls / .csv) Native Renderer
  // ────────────────────────────────────────────────────────
  async renderExcel(container, streamURL, fileName, downloadURL) {
    container.innerHTML = `
      <div style="padding: 40px 16px; text-align: center; color: var(--text-muted, #94a3b8); width: 100%;">
        <div style="font-size: 36px; margin-bottom: 12px; animation: spin 1.5s linear infinite;">⏳</div>
        <div style="font-size: 15px; font-weight: 600; color: #10b981;">Đang tải và phân tích bảng tính Excel...</div>
        <div style="font-size: 12px; margin-top: 6px; color: #94a3b8;">Trích xuất các trang tính và dữ liệu bảng trực tiếp 100% nội bộ</div>
      </div>
    `;

    try {
      const arrayBuffer = await this._fetchOfficeBinary(streamURL);

      let sheets = [];

      // 1. Primary: Use SheetJS (xlsx.full.min.js) for full support of .xls (BIFF8/BIFF5), .xlsx, .ods, .csv, .xlsb
      if (typeof XLSX !== 'undefined') {
        try {
          const workbook = XLSX.read(arrayBuffer, { type: 'array', cellDates: true, cellNF: true, cellText: true });
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
          console.warn('SheetJS parser fallback:', xlsxErr);
        }
      }

      // 2. Secondary fallback: Unzip XML for .xlsx if SheetJS not present
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
        } catch (e) {
          // Not a standard zip
        }
      }

      // Fallback for XML Spreadsheet / HTML / CSV / Text
      if (sheets.length === 0) {
        const text = new TextDecoder('utf-8').decode(arrayBuffer);
        
        if (text.includes('<Workbook') && text.includes('<Table>')) {
          const rows = [];
          const rRegex = /<Row\b[^>]*>([\s\S]*?)<\/Row>/gi;
          let rM;
          while ((rM = rRegex.exec(text)) !== null) {
            const cells = [];
            const cRegex = /<Cell\b[^>]*><Data\b[^>]*>([\s\S]*?)<\/Data><\/Cell>/gi;
            let cM;
            while ((cM = cRegex.exec(rM[1])) !== null) {
              cells.push(cM[1].replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&'));
            }
            if (cells.length > 0) rows.push(cells);
          }
          if (rows.length > 0) sheets.push({ name: 'Sheet1', rows });
        } else if (text.includes('<table') || text.includes('<TABLE')) {
          const rows = [];
          const rRegex = /<tr\b[^>]*>([\s\S]*?)<\/tr>/gi;
          let rM;
          while ((rM = rRegex.exec(text)) !== null) {
            const cells = [];
            const cRegex = /<t[dh]\b[^>]*>([\s\S]*?)<\/t[dh]>/gi;
            let cM;
            while ((cM = cRegex.exec(rM[1])) !== null) {
              cells.push(cM[1].replace(/<[^>]+>/g, '').trim());
            }
            if (cells.length > 0) rows.push(cells);
          }
          if (rows.length > 0) sheets.push({ name: 'Sheet1', rows });
        } else {
          const lines = text.split(/\r?\n/).filter(l => l.trim().length > 0);
          if (lines.length > 0 && (lines[0].includes(',') || lines[0].includes('\t') || lines[0].includes(';'))) {
            const delim = lines[0].includes('\t') ? '\t' : (lines[0].includes(';') ? ';' : ',');
            const rows = lines.map(line => line.split(delim).map(c => c.replace(/^"|"$/g, '').trim()));
            sheets.push({ name: 'Dữ liệu Bảng', rows });
          }
        }
      }

      if (sheets.length === 0) {
        container.innerHTML = `
          <div style="padding: 36px 20px; text-align: center; width: 100%;">
            <div style="font-size: 54px; margin-bottom: 14px;">📊</div>
            <div style="font-size: 16px; font-weight: 700; color: #10b981; margin-bottom: 6px;">${fileName}</div>
            <div style="font-size: 13px; color: #94a3b8; margin-bottom: 20px; max-width: 480px; margin-left: auto; margin-right: auto; line-height: 1.5;">
              Tệp tin bảng tính định dạng <b>Excel nhị phân (BIFF8 / .xls)</b>.<br>
              Bạn có thể tải tệp về máy để mở trực tiếp bằng Microsoft Excel hoặc Google Sheets.
            </div>
            <div style="display: flex; gap: 10px; justify-content: center;">
              <a href="${downloadURL}" class="btn btn-primary" download="${fileName}" style="padding: 10px 24px; font-size: 14px;">
                ⬇️ Tải Bảng Tính Excel Về Máy
              </a>
            </div>
          </div>
        `;
        return;
      }

      this._renderExcelUI(container, sheets, fileName, downloadURL);

    } catch (err) {
      console.warn('Excel render error:', err);
      let errMsg = err.message || '';
      const isDriveMissing = errMsg.includes('404') || errMsg.includes('Failed to fetch') || errMsg.includes('502') || errMsg.includes('token') || errMsg.includes('Token') || errMsg.includes('0 bytes');
      const isDriveToken = errMsg.toLowerCase().includes('token') || errMsg.includes('502') || errMsg.includes('0 bytes');
      container.innerHTML = `
        <div style="padding: 30px 16px; text-align: center; width: 100%;">
          <div style="font-size: 48px; margin-bottom: 12px;">📊</div>
          <div style="font-size: 16px; font-weight: 700; color: #10b981; margin-bottom: 6px;">${fileName}</div>
          <div style="font-size: 13px; color: #94a3b8; margin-bottom: 20px; line-height: 1.6; max-width: 560px; margin-left: auto; margin-right: auto;">
            ${isDriveToken
              ? `<b style="color:#f59e0b;">⚠️ Token Google Drive đã hết hạn</b><br>${errMsg}<br><br>
                 <span style="color:#94a3b8;">Để sửa: Vào <b>Tài khoản Drive</b> → Nhấn nút <b>Làm mới Token</b> cho tài khoản bị lỗi, hoặc <b>tải lại file</b> lên CloudPool.</span>`
              : isDriveMissing
              ? `<b>Lỗi kết nối / Dữ liệu không tìm thấy:</b> Có vẻ như phân mảnh của tệp tin này trên Google Drive đã bị xoá, hoặc không thể truy cập.<br><span style="color:#f59e0b;">💡 ${errMsg}</span><br>Bạn hãy thử tải lại tệp tin này lên CloudPool.`
              : `Không thể trích xuất cấu trúc bảng (${errMsg}).<br>Bạn có thể tải tệp tin về máy để mở trực tiếp.`}
          </div>
          <div style="display: flex; gap: 10px; justify-content: center;">
            <a href="${downloadURL}" class="btn btn-primary" download="${fileName}">
              ⬇️ Thử Tải Bảng Tính Về Máy
            </a>
          </div>
        </div>
      `;
    }
  },

  _renderExcelUI(container, sheets, fileName, downloadURL) {
    let activeSheetIdx = 0;

    container.innerHTML = `
      <div class="excel-viewer-wrapper" style="display: flex; flex-direction: column; gap: 10px; width: 100%; height: 76vh; min-height: 480px; box-sizing: border-box;">
        
        <!-- Header Toolbar -->
        <div style="display: flex; justify-content: space-between; align-items: center; background: rgba(16, 185, 129, 0.08); border: 1px solid rgba(16, 185, 129, 0.25); border-radius: 12px; padding: 8px 14px; gap: 10px; flex-wrap: wrap;">
          <div style="display: flex; align-items: center; gap: 10px; min-width: 0; flex: 1;">
            <span style="font-size: 22px; flex-shrink: 0;">📊</span>
            <div style="min-width: 0;">
              <div style="font-weight: 700; font-size: 13px; color: #10b981; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 400px;" title="${fileName}">${fileName}</div>
              <div style="font-size: 11px; color: #94a3b8;" id="excel-stat-info">Đang phân tích bảng...</div>
            </div>
          </div>
          
          <div style="display: flex; gap: 8px; align-items: center; flex-wrap: wrap;">
            <input type="text" id="excel-search-input" placeholder="🔍 Lọc dòng..." style="background: rgba(255,255,255,0.06); border: 1px solid rgba(255,255,255,0.15); color: #fff; font-size: 11px; padding: 4px 10px; border-radius: 8px; outline: none; width: 140px;" />
            <a href="${downloadURL}" class="btn btn-primary btn-sm" download="${fileName}" style="font-size: 11px; padding: 4px 12px;">
              ⬇️ Tải File
            </a>
          </div>
        </div>

        <!-- Table Grid Container -->
        <div style="flex: 1; min-height: 0; background: #0d1117; border: 1px solid rgba(255,255,255,0.1); border-radius: 12px; overflow: auto; position: relative;">
          <table id="excel-data-table" style="width: 100%; border-collapse: collapse; font-size: 12px; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;">
            <tbody id="excel-table-body"></tbody>
          </table>
        </div>

        <!-- Sheet Selector Tabs Bar -->
        <div style="display: flex; justify-content: space-between; align-items: center; padding: 6px 12px; background: rgba(0,0,0,0.4); border-top: 1px solid rgba(255,255,255,0.08); border-radius: 0 0 12px 12px; gap: 10px; overflow-x: auto;">
          <div style="display: flex; gap: 6px;" id="excel-sheet-tabs">
            ${sheets.map((s, idx) => `
              <button class="excel-tab-btn ${idx === 0 ? 'active' : ''}" data-idx="${idx}" style="padding: 4px 12px; font-size: 11px; font-weight: 600; border-radius: 6px; border: 1px solid ${idx === 0 ? '#10b981' : 'rgba(255,255,255,0.1)'}; background: ${idx === 0 ? 'rgba(16,185,129,0.2)' : 'rgba(255,255,255,0.03)'}; color: ${idx === 0 ? '#34d399' : '#94a3b8'}; cursor: pointer;">
                📋 ${s.name}
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
      const tbody = container.querySelector('#excel-table-body');
      const statEl = container.querySelector('#excel-stat-info');
      const countEl = container.querySelector('#excel-row-col-count');
      if (!tbody || !sheet) return;

      const q = (filterQuery || '').toLowerCase().trim();
      let maxCols = 0;
      sheet.rows.forEach(r => { if (r.length > maxCols) maxCols = r.length; });

      let filteredRows = sheet.rows;
      if (q) {
        filteredRows = sheet.rows.filter((row, rIdx) => {
          if (rIdx === 0) return true;
          return row.some(cell => cell.toString().toLowerCase().includes(q));
        });
      }

      if (statEl) statEl.textContent = `Trang tính: ${sheet.name} • ${sheet.rows.length} dòng • ${maxCols} cột`;
      if (countEl) countEl.textContent = `Hiển thị ${filteredRows.length} / ${sheet.rows.length} dòng`;

      let html = '<tr style="background: rgba(255,255,255,0.05); position: sticky; top: 0; z-index: 10;">';
      html += '<th style="padding: 6px 10px; width: 45px; text-align: center; color: #64748b; border: 1px solid rgba(255,255,255,0.08); font-size: 10px;">#</th>';
      for (let c = 0; c < maxCols; c++) {
        html += `<th style="padding: 6px 12px; text-align: center; color: #94a3b8; border: 1px solid rgba(255,255,255,0.08); font-weight: 700; font-size: 11px;">${getColLetter(c)}</th>`;
      }
      html += '</tr>';

      const renderLimit = Math.min(filteredRows.length, 1000);
      for (let r = 0; r < renderLimit; r++) {
        const row = filteredRows[r];
        const isHeaderRow = (r === 0);
        html += `<tr style="${isHeaderRow ? 'background: rgba(16,185,129,0.08); font-weight: 700;' : 'background: rgba(255,255,255,0.01);'}">`;
        html += `<td style="padding: 6px 8px; text-align: center; color: #64748b; border: 1px solid rgba(255,255,255,0.05); font-size: 10px; user-select: none;">${r + 1}</td>`;

        for (let c = 0; c < maxCols; c++) {
          const val = row[c] !== undefined ? row[c] : '';
          html += `<td style="padding: 6px 12px; border: 1px solid rgba(255,255,255,0.05); color: ${isHeaderRow ? '#34d399' : '#f1f5f9'}; white-space: nowrap; max-width: 350px; overflow: hidden; text-overflow: ellipsis;" title="${val}">${val}</td>`;
        }
        html += '</tr>';
      }

      if (filteredRows.length > 1000) {
        html += `<tr><td colspan="${maxCols + 1}" style="text-align: center; padding: 12px; color: #94a3b8; font-style: italic;">Đã hiển thị 1,000 / ${filteredRows.length} dòng đầu tiên. Tải tệp tin về để xem toàn bộ.</td></tr>`;
      }

      tbody.innerHTML = html;
    };

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

    container.querySelector('#excel-search-input')?.addEventListener('input', (e) => {
      renderSheetData(activeSheetIdx, e.target.value);
    });

    renderSheetData(0);

  },

  // ────────────────────────────────────────────────────────
  // 3. Word Document (.docx / .doc) Native Renderer
  // ────────────────────────────────────────────────────────
  async renderDocx(container, streamURL, fileName, downloadURL) {
    container.innerHTML = `
      <div style="padding: 40px 16px; text-align: center; color: var(--text-muted, #94a3b8); width: 100%;">
        <div style="font-size: 36px; margin-bottom: 12px; animation: spin 1.5s linear infinite;">⏳</div>
        <div style="font-size: 15px; font-weight: 600; color: #38bdf8;">Đang tải và mở tài liệu Word...</div>
        <div style="font-size: 12px; margin-top: 6px; color: #94a3b8;">Trích xuất định dạng văn bản, tiêu đề và bảng biểu trực tiếp</div>
      </div>
    `;

    try {
      const arrayBuffer = await this._fetchOfficeBinary(streamURL);

      // 1. Premier: Use docx-preview for pixel-perfect Microsoft Word layout
      if (typeof docx !== 'undefined' && typeof docx.renderAsync === 'function') {
        try {
          container.innerHTML = `
            <div style="max-height: 75vh; overflow-y: auto; background: #0f172a; padding: 20px 10px; border-radius: 8px; display: flex; flex-direction: column; align-items: center;">
              <div style="width: 100%; max-width: 860px; display: flex; justify-content: space-between; align-items: center; background: #1e293b; padding: 10px 16px; border-radius: 6px; margin-bottom: 16px;">
                <div style="font-size: 14px; font-weight: 700; color: #38bdf8;">📄 ${fileName}</div>
                <div style="display: flex; gap: 8px;">
                  <button class="btn btn-secondary btn-sm" onclick="window.print()" style="font-size: 11px;">🖨️ In ấn</button>
                  <a href="${downloadURL}" class="btn btn-primary btn-sm" download="${fileName}" style="font-size: 11px;">⬇️ Tải Word</a>
                </div>
              </div>
              <div id="docx-render-target" style="width: 100%; max-width: 860px; background: #fff; color: #000; border-radius: 4px; box-shadow: 0 4px 20px rgba(0,0,0,0.4); padding: 10px;"></div>
            </div>
          `;
          const target = container.querySelector('#docx-render-target');
          if (target) {
            await docx.renderAsync(arrayBuffer, target, null, {
              className: 'docx-preview-content',
              inWrapper: false,
              ignoreWidth: false,
              ignoreHeight: false
            });
            return;
          }
        } catch (docxErr) {
          console.warn('docx-preview fallback:', docxErr);
        }
      }

      // 2. Secondary: Use Mammoth.js for rich formatting, tables, images, headings, lists
      if (typeof mammoth !== 'undefined') {
        try {
          const result = await mammoth.convertToHtml({ arrayBuffer });
          if (result && result.value && result.value.trim().length > 0) {
            container.innerHTML = `
              <div class="docx-paper-container" style="max-height: 72vh; overflow-y: auto; padding: 24px 16px; background: #0f172a; border-radius: 8px; display: flex; justify-content: center;">
                <div class="docx-page-sheet" style="background: #ffffff; color: #1e293b; width: 100%; max-width: 840px; min-height: 60vh; padding: 48px 56px; border-radius: 4px; box-shadow: 0 10px 25px rgba(0,0,0,0.3); font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif; line-height: 1.7; font-size: 14px;">
                  <div style="display: flex; justify-content: space-between; align-items: center; border-bottom: 2px solid #e2e8f0; padding-bottom: 12px; margin-bottom: 24px;">
                    <div style="font-size: 16px; font-weight: 700; color: #0f172a;">📄 ${fileName}</div>
                    <a href="${downloadURL}" class="btn btn-primary btn-sm" download="${fileName}" style="padding: 4px 12px; font-size: 12px;">⬇️ Tải file gốc</a>
                  </div>
                  <div class="docx-body-rendered" style="word-break: break-word;">
                    ${result.value}
                  </div>
                </div>
              </div>
            `;
            return;
          }
        } catch (mammothErr) {
          console.warn('Mammoth parser fallback:', mammothErr);
        }
      }

      // 2. Secondary fallback: Manual XML parser
      const zipFiles = await this.unzipArrayBuffer(arrayBuffer);
      const textDecoder = new TextDecoder('utf-8');

      if (!zipFiles['word/document.xml']) {
        throw new Error('Không tìm thấy tệp cấu trúc word/document.xml.');
      }

      const docXml = textDecoder.decode(zipFiles['word/document.xml']);

      // Parse Media Images
      const mediaMap = {};
      for (const [path, data] of Object.entries(zipFiles)) {
        if (path.startsWith('word/media/')) {
          let mime = 'image/png';
          if (/\.(jpg|jpeg)$/i.test(path)) mime = 'image/jpeg';
          const blob = new Blob([data], { type: mime });
          mediaMap[path] = URL.createObjectURL(blob);
          const short = path.split('/').pop();
          mediaMap[short] = mediaMap[path];
        }
      }

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
            parsedHtml += `<h3 style="font-size: 18px; font-weight: 700; color: #38bdf8; margin: 16px 0 8px 0; border-bottom: 1px solid rgba(56,189,248,0.3); padding-bottom: 6px;">${pText}</h3>`;
          } else {
            parsedHtml += `<p style="font-size: 14px; line-height: 1.7; color: #f1f5f9; margin-bottom: 10px;">${pText}</p>`;
          }
        }
      }

      container.innerHTML = `
        <div style="display: flex; flex-direction: column; gap: 10px; width: 100%; height: 76vh; min-height: 480px; box-sizing: border-box;">
          
          <!-- Header Toolbar -->
          <div style="display: flex; justify-content: space-between; align-items: center; background: rgba(56, 189, 248, 0.08); border: 1px solid rgba(56, 189, 248, 0.25); border-radius: 12px; padding: 8px 14px; gap: 10px; flex-wrap: wrap;">
            <div style="display: flex; align-items: center; gap: 10px; min-width: 0; flex: 1;">
              <span style="font-size: 22px; flex-shrink: 0;">📄</span>
              <div style="min-width: 0;">
                <div style="font-weight: 700; font-size: 13px; color: #38bdf8; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 480px;" title="${fileName}">${fileName}</div>
                <div style="font-size: 11px; color: #94a3b8;">Tài Liệu Văn Bản Word • Đọc trực tiếp 100% nội bộ</div>
              </div>
            </div>
            <a href="${downloadURL}" class="btn btn-primary btn-sm" download="${fileName}" style="font-size: 11px; padding: 4px 12px;">
              ⬇️ Tải File Word
            </a>
          </div>

          <!-- Document Canvas -->
          <div style="flex: 1; min-height: 0; background: #0f172a; border: 1px solid rgba(255,255,255,0.1); border-radius: 12px; overflow-y: auto; padding: clamp(16px, 3vw, 40px); display: flex; justify-content: center;">
            <div style="max-width: 800px; width: 100%; background: #1e293b; padding: clamp(16px, 4vw, 36px); border-radius: 10px; box-shadow: 0 10px 30px rgba(0,0,0,0.5); border: 1px solid rgba(255,255,255,0.06);">
              ${parsedHtml || '<div style="color: #94a3b8; text-align: center; padding: 40px;">(Tài liệu trống)</div>'}
            </div>
          </div>

        </div>
      `;

    } catch (err) {
      console.warn('Docx render error:', err);
      container.innerHTML = `
        <div style="padding: 30px 16px; text-align: center; width: 100%;">
          <div style="font-size: 48px; margin-bottom: 12px;">📄</div>
          <div style="font-size: 16px; font-weight: 700; color: #38bdf8; margin-bottom: 6px;">${fileName}</div>
          <div style="font-size: 13px; color: #94a3b8; margin-bottom: 20px; line-height: 1.5;">
            Tệp tin văn bản Word (.docx/.doc).<br>
            Bạn có thể tải tệp tin này về máy để mở trực tiếp.
          </div>
          <div style="display: flex; gap: 10px; justify-content: center;">
            <a href="${downloadURL}" class="btn btn-primary" download="${fileName}">
              ⬇️ Tải File Word Về Máy
            </a>
          </div>
        </div>
      `;
    }
  }
};

