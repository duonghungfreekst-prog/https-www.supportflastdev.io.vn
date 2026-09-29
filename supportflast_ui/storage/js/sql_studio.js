// ==========================================================================
// CloudPool SQL Studio Controller PRO (Database Inspector, Query Console & Visualizer)
// ==========================================================================

const SQLStudio = {
  lastResult: null,
  rawTables: [],
  historyKey: 'cloudpool_sql_history_v2',
  isChartVisible: false,

  init() {
    this.bindEvents();
    this.renderHistory();
  },

  bindEvents() {
    const btnRun = document.getElementById('btn-run-sql');
    const input = document.getElementById('sql-query-input');
    const btnRefresh = document.getElementById('btn-refresh-sql-tables');
    const btnJson = document.getElementById('btn-sql-export-json');
    const btnCsv = document.getElementById('btn-sql-export-csv');

    if (btnRun) {
      btnRun.addEventListener('click', () => this.runQuery());
    }

    if (btnRefresh) {
      btnRefresh.addEventListener('click', () => this.loadTables());
    }

    if (input) {
      input.addEventListener('keydown', (e) => {
        if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
          e.preventDefault();
          this.runQuery();
        }
      });
    }

    if (btnJson) {
      btnJson.addEventListener('click', () => this.exportJSON());
    }

    if (btnCsv) {
      btnCsv.addEventListener('click', () => this.exportCSV());
    }
  },

  // -------------------------------------------------------------
  // Sidebar Tabs & Schema Inspector
  // -------------------------------------------------------------

  switchSidebarTab(tabName) {
    const tabs = ['tables', 'snippets', 'history'];
    tabs.forEach(t => {
      const btn = document.getElementById(`btn-tab-sql-${t}`);
      const pane = document.getElementById(`sql-sidebar-${t}`);
      if (btn) {
        if (t === tabName) {
          btn.classList.add('active');
          btn.style.background = 'var(--accent-blue)';
          btn.style.color = '#fff';
        } else {
          btn.classList.remove('active');
          btn.style.background = 'transparent';
          btn.style.color = 'var(--text-secondary)';
        }
      }
      if (pane) {
        pane.style.display = t === tabName ? 'flex' : 'none';
      }
    });

    if (tabName === 'history') {
      this.renderHistory();
    }
  },

  async loadTables() {
    const container = document.getElementById('sql-tables-list');
    if (!container) return;

    try {
      container.innerHTML = `<span style="font-size: 11px; color: var(--text-muted);">Đang nạp cấu trúc CSDL...</span>`;
      const tables = await API.getSQLTables();
      if (!Array.isArray(tables) || tables.length === 0) {
        container.innerHTML = `<span style="font-size: 11px; color: var(--text-muted);">Không tìm thấy bảng.</span>`;
        return;
      }

      this.rawTables = tables;
      this.renderTablesList(tables);
    } catch (err) {
      container.innerHTML = `<span style="font-size: 11px; color: var(--accent-red);">Lỗi: ${err.message}</span>`;
    }
  },

  filterTables(query) {
    const q = (query || '').toLowerCase().trim();
    if (!q) {
      this.renderTablesList(this.rawTables);
      return;
    }
    const filtered = this.rawTables.filter(t => t.name.toLowerCase().includes(q));
    this.renderTablesList(filtered);
  },

  renderTablesList(tables) {
    const container = document.getElementById('sql-tables-list');
    if (!container) return;

    if (!tables || tables.length === 0) {
      container.innerHTML = `<span style="font-size: 11px; color: var(--text-muted); padding: 8px;">Không có bảng khớp tìm kiếm.</span>`;
      return;
    }

    let html = '';
    tables.forEach(t => {
      const colCount = t.columns ? t.columns.length : 0;
      let colsHtml = '';
      if (t.columns && t.columns.length > 0) {
        colsHtml = `
          <div id="cols-for-${t.name}" style="display: none; padding-left: 12px; margin-top: 4px; border-left: 2px solid var(--border-subtle); flex-direction: column; gap: 3px;">
            ${t.columns.map(c => `
              <div onclick="SQLStudio.insertColumn('${c.name}')" style="display: flex; justify-content: space-between; align-items: center; padding: 2px 4px; font-size: 10px; cursor: pointer; border-radius: 2px; color: var(--text-secondary); transition: background 0.15s;" onmouseover="this.style.background='rgba(255,255,255,0.05)'" onmouseout="this.style.background='transparent'" title="Bấm để chèn cột '${c.name}' vào truy vấn">
                <span style="font-family: var(--font-mono); color: ${c.is_pk ? 'var(--accent-amber)' : 'var(--text-primary)'};">${c.is_pk ? '🔑 ' : '• '}${c.name}</span>
                <span style="font-size: 9px; color: var(--text-muted);">${c.type || 'TEXT'}</span>
              </div>
            `).join('')}
          </div>
        `;
      }

      html += `
        <div class="sql-table-card" style="background: var(--bg-primary); border: 1px solid var(--border-subtle); border-radius: var(--radius-sm); padding: 6px 8px; font-size: 11px; display: flex; flex-direction: column;">
          <div style="display: flex; justify-content: space-between; align-items: center;">
            <div onclick="SQLStudio.queryTable('${t.name}')" style="cursor: pointer; display: flex; align-items: center; gap: 5px; font-family: var(--font-mono); color: var(--accent-blue); font-weight: 600;" title="Bấm để xem 50 dòng đầu tiên">
              <span>📊 ${t.name}</span>
            </div>
            <div style="display: flex; align-items: center; gap: 4px;">
              <span style="font-size: 10px; color: var(--text-muted); background: var(--bg-tertiary); padding: 1px 5px; border-radius: var(--radius-full);">${t.row_count} dòng</span>
              ${colCount > 0 ? `
                <button onclick="SQLStudio.toggleColumns('${t.name}', this)" style="background: none; border: none; color: var(--text-muted); cursor: pointer; padding: 0 3px; font-size: 10px;" title="Xem danh sách cột">▼</button>
              ` : ''}
            </div>
          </div>
          ${colsHtml}
        </div>
      `;
    });

    container.innerHTML = html;
  },

  toggleColumns(tableName, btn) {
    const el = document.getElementById(`cols-for-${tableName}`);
    if (el) {
      const isHidden = el.style.display === 'none';
      el.style.display = isHidden ? 'flex' : 'none';
      if (btn) btn.textContent = isHidden ? '▲' : '▼';
    }
  },

  insertColumn(colName) {
    const input = document.getElementById('sql-query-input');
    if (!input) return;
    const start = input.selectionStart;
    const end = input.selectionEnd;
    const text = input.value;
    input.value = text.substring(0, start) + colName + text.substring(end);
    input.focus();
    input.selectionStart = input.selectionEnd = start + colName.length;
  },

  queryTable(tableName) {
    this.setQuery(`SELECT * FROM ${tableName} LIMIT 50;`);
    this.runQuery();
  },

  setQuery(sql) {
    const input = document.getElementById('sql-query-input');
    if (input) {
      input.value = sql;
      input.focus();
    }
  },

  copySQL() {
    const input = document.getElementById('sql-query-input');
    if (input && input.value.trim()) {
      navigator.clipboard.writeText(input.value.trim()).then(() => {
        Toast.success('Đã sao chép câu truy vấn SQL vào bộ nhớ tạm');
      });
    }
  },

  formatSQL() {
    const input = document.getElementById('sql-query-input');
    if (!input) return;
    let sql = input.value.trim();
    if (!sql) return;

    // Standard keyword uppercase & newlines
    const keywords = ['SELECT', 'FROM', 'WHERE', 'AND', 'OR', 'LEFT JOIN', 'INNER JOIN', 'JOIN', 'GROUP BY', 'ORDER BY', 'HAVING', 'LIMIT', 'OFFSET', 'UNION', 'INSERT INTO', 'VALUES', 'UPDATE', 'SET', 'DELETE FROM', 'CREATE TABLE', 'DROP TABLE', 'ALTER TABLE'];
    keywords.forEach(kw => {
      const regex = new RegExp(`\\b${kw}\\b`, 'gi');
      sql = sql.replace(regex, kw);
    });

    // Formatting indentations
    sql = sql
      .replace(/\s*SELECT\s+/g, 'SELECT\n  ')
      .replace(/\s*FROM\s+/g, '\nFROM\n  ')
      .replace(/\s*WHERE\s+/g, '\nWHERE\n  ')
      .replace(/\s*AND\s+/g, '\n  AND ')
      .replace(/\s*OR\s+/g, '\n  OR ')
      .replace(/\s*GROUP BY\s+/g, '\nGROUP BY\n  ')
      .replace(/\s*ORDER BY\s+/g, '\nORDER BY\n  ')
      .replace(/\s*LIMIT\s+/g, '\nLIMIT ');

    input.value = sql.trim();
    Toast.success('Đã căn chỉnh định dạng SQL chuẩn');
  },

  // -------------------------------------------------------------
  // Query Execution & History
  // -------------------------------------------------------------

  async runQuery() {
    const input = document.getElementById('sql-query-input');
    const query = input ? input.value.trim() : '';
    if (!query) {
      Toast.error('Vui lòng nhập câu truy vấn SQL');
      return;
    }

    const statusEl = document.getElementById('sql-result-status');
    const container = document.getElementById('sql-result-container');
    const filterInput = document.getElementById('sql-result-filter');
    if (filterInput) filterInput.value = '';

    statusEl.innerHTML = `<span style="color: var(--accent-blue);">⏳ Đang thực thi truy vấn...</span>`;

    try {
      const res = await API.runSQLQuery(query);
      this.lastResult = res;

      // Save to History
      this.saveToHistory(query);

      statusEl.innerHTML = `✓ Thực thi thành công trong <b>${res.execution_ms.toFixed(2)} ms</b> | Trả về: <b>${res.affected_rows} dòng</b>`;
      this.renderTable(res);
      this.updateChartData(res);
    } catch (err) {
      this.lastResult = null;
      statusEl.innerHTML = `<span style="color: var(--accent-red);">❌ Lỗi: ${err.message}</span>`;
      container.innerHTML = `
        <div style="padding: 24px; color: var(--accent-red); font-family: var(--font-mono); font-size: 12px; background: rgba(248, 81, 73, 0.08); border-radius: var(--radius-sm); border: 1px solid rgba(248, 81, 73, 0.2);">
          <b>Lỗi cú pháp / Thực thi SQLite:</b><br><br>
          ${err.message}
        </div>
      `;
    }
  },

  saveToHistory(query) {
    try {
      let hist = JSON.parse(localStorage.getItem(this.historyKey) || '[]');
      hist = hist.filter(q => q.sql !== query);
      hist.unshift({
        sql: query,
        time: new Date().toLocaleTimeString('vi-VN', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
      });
      if (hist.length > 30) hist = hist.slice(0, 30);
      localStorage.setItem(this.historyKey, JSON.stringify(hist));
    } catch (e) {}
  },

  renderHistory() {
    const list = document.getElementById('sql-history-list');
    if (!list) return;

    try {
      const hist = JSON.parse(localStorage.getItem(this.historyKey) || '[]');
      if (hist.length === 0) {
        list.innerHTML = `<span style="font-size: 11px; color: var(--text-muted); padding: 8px;">Chưa có lịch sử câu lệnh nào.</span>`;
        return;
      }

      list.innerHTML = hist.map(h => `
        <div class="sql-history-item" onclick="SQLStudio.setQuery(\`${h.sql.replace(/\\/g, '\\\\').replace(/`/g, '\\`')}\`)" style="background: var(--bg-primary); border: 1px solid var(--border-subtle); border-radius: var(--radius-sm); padding: 6px 8px; cursor: pointer; transition: border-color 0.15s;" title="Bấm để nạp câu lệnh này">
          <div style="font-size: 10px; color: var(--accent-blue); font-weight: 600; margin-bottom: 2px;">🕒 ${h.time}</div>
          <div style="font-family: var(--font-mono); font-size: 11px; color: var(--text-primary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">${h.sql}</div>
        </div>
      `).join('');
    } catch (e) {
      list.innerHTML = `<span style="font-size: 11px; color: var(--text-muted);">Không thể đọc lịch sử.</span>`;
    }
  },

  clearHistory() {
    localStorage.removeItem(this.historyKey);
    this.renderHistory();
    Toast.success('Đã xóa toàn bộ lịch sử truy vấn');
  },

  // -------------------------------------------------------------
  // Result Table & In-Table Search
  // -------------------------------------------------------------

  renderTable(res, filterText = '') {
    const container = document.getElementById('sql-result-container');
    if (!container) return;

    if (!res || !res.columns || res.columns.length === 0) {
      container.innerHTML = `<div style="text-align: center; padding: 36px; color: var(--text-muted);">Không có dữ liệu trả về.</div>`;
      return;
    }

    let rows = res.rows || [];
    if (filterText) {
      const q = filterText.toLowerCase();
      rows = rows.filter(row => row.some(val => String(val ?? '').toLowerCase().includes(q)));
    }

    let tableHtml = `<table class="files-table" style="font-family: var(--font-mono); font-size: 12px; min-width: max-content; width: 100%;"><thead><tr>`;

    // Header with STT
    tableHtml += `<th style="padding: 8px 10px; white-space: nowrap; color: var(--text-muted); width: 40px; text-align: center;">#</th>`;
    res.columns.forEach(col => {
      tableHtml += `<th style="padding: 8px 12px; white-space: nowrap; text-align: left;">${col}</th>`;
    });
    tableHtml += `</tr></thead><tbody>`;

    if (rows.length === 0) {
      tableHtml += `<tr><td colspan="${res.columns.length + 1}" style="text-align: center; padding: 24px; color: var(--text-muted);">${filterText ? 'Không có bản ghi nào khớp bộ lọc.' : '0 bản ghi.'}</td></tr>`;
    } else {
      rows.forEach((row, rowIndex) => {
        tableHtml += `<tr>`;
        tableHtml += `<td style="padding: 6px 10px; color: var(--text-muted); text-align: center; font-size: 11px;">${rowIndex + 1}</td>`;
        row.forEach((val, colIndex) => {
          let displayVal = val;
          let rawStr = '';
          if (val === null || val === undefined) {
            displayVal = `<i style="color: var(--text-muted);">NULL</i>`;
            rawStr = 'NULL';
          } else if (typeof val === 'object') {
            rawStr = JSON.stringify(val);
            displayVal = rawStr;
          } else {
            rawStr = String(val);
            displayVal = rawStr;
          }

          const isLong = rawStr.length > 50;
          const shortVal = isLong ? rawStr.substring(0, 50) + '…' : displayVal;
          const escapedRaw = rawStr.replace(/'/g, "\\'").replace(/"/g, '&quot;');

          if (isLong) {
            tableHtml += `<td style="padding: 6px 12px; max-width: 260px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis;" title="${escapedRaw}">
              <span style="cursor: pointer; text-decoration: underline dotted; color: var(--accent-blue);" onclick="SQLStudio.showCellValue(${rowIndex}, ${colIndex})">${shortVal}</span>
            </td>`;
          } else {
            tableHtml += `<td style="padding: 6px 12px; white-space: nowrap;">${displayVal}</td>`;
          }
        });
        tableHtml += `</tr>`;
      });
    }

    tableHtml += `</tbody></table>`;
    container.innerHTML = `
      <div style="overflow-x: auto; overflow-y: auto; max-height: 380px; width: 100%;">
        ${tableHtml}
      </div>
    `;
  },

  filterResultRows(filterText) {
    if (!this.lastResult) return;
    this.renderTable(this.lastResult, filterText);
  },

  showCellValue(rowIndex, colIndex) {
    if (!this.lastResult || !this.lastResult.rows) return;
    const val = this.lastResult.rows[rowIndex]?.[colIndex];
    const colName = this.lastResult.columns?.[colIndex] || 'Giá trị';
    const strVal = (val === null || val === undefined) ? 'NULL'
      : (typeof val === 'object' ? JSON.stringify(val, null, 2) : String(val));

    let existingModal = document.getElementById('sql-cell-modal');
    if (existingModal) existingModal.remove();

    const modal = document.createElement('div');
    modal.id = 'sql-cell-modal';
    modal.style.cssText = `
      position: fixed; top: 50%; left: 50%; transform: translate(-50%, -50%);
      background: var(--bg-secondary); border: 1px solid var(--border-color);
      border-radius: var(--radius-md); padding: 20px; max-width: 600px; width: 90vw;
      max-height: 70vh; overflow: auto; z-index: 9999; box-shadow: 0 20px 60px rgba(0,0,0,0.5);
    `;
    modal.innerHTML = `
      <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px;">
        <span style="font-size: 13px; font-weight: 600; color: var(--text-primary);">📋 ${colName} — Dòng ${rowIndex + 1}</span>
        <button onclick="document.getElementById('sql-cell-modal').remove()" style="background: none; border: none; color: var(--text-muted); cursor: pointer; font-size: 18px; line-height: 1;">×</button>
      </div>
      <pre style="background: var(--bg-tertiary, #0d1117); border: 1px solid var(--border-subtle); border-radius: var(--radius-sm); padding: 12px; font-family: var(--font-mono); font-size: 12px; color: var(--text-primary); white-space: pre-wrap; word-break: break-all; margin: 0;">${strVal}</pre>
      <div style="margin-top: 10px; display: flex; gap: 8px; justify-content: flex-end;">
        <button class="btn btn-secondary btn-sm" onclick="navigator.clipboard.writeText(\`${strVal.replace(/`/g, '\\`')}\`).then(()=>Toast.success('Đã sao chép'))">📋 Sao chép</button>
        <button class="btn btn-secondary btn-sm" onclick="document.getElementById('sql-cell-modal').remove()">Đóng</button>
      </div>
    `;
    const overlay = document.createElement('div');
    overlay.style.cssText = 'position: fixed; inset: 0; background: rgba(0,0,0,0.5); z-index: 9998;';
    overlay.onclick = () => { modal.remove(); overlay.remove(); };
    document.body.appendChild(overlay);
    document.body.appendChild(modal);
  },

  // -------------------------------------------------------------
  // Visual Chart View
  // -------------------------------------------------------------

  toggleChart() {
    this.isChartVisible = !this.isChartVisible;
    const chartEl = document.getElementById('sql-chart-container');
    const btn = document.getElementById('btn-sql-toggle-chart');
    if (chartEl) {
      chartEl.style.display = this.isChartVisible ? 'block' : 'none';
    }
    if (btn) {
      btn.style.background = this.isChartVisible ? 'var(--accent-blue)' : '';
      btn.style.color = this.isChartVisible ? '#fff' : '';
    }
    if (this.isChartVisible && this.lastResult) {
      this.updateChartData(this.lastResult);
    }
  },

  updateChartData(res) {
    const chartEl = document.getElementById('sql-chart-container');
    if (!chartEl || !res || !res.rows || res.rows.length === 0) return;

    // Detect first numeric column and first string column
    let labelColIdx = 0;
    let numColIdx = -1;

    for (let c = 0; c < res.columns.length; c++) {
      const sample = res.rows.find(r => r[c] !== null && r[c] !== undefined);
      if (sample && typeof sample[c] === 'number') {
        numColIdx = c;
        break;
      }
    }

    if (numColIdx === -1) {
      chartEl.innerHTML = `<div style="font-size: 12px; color: var(--text-muted); text-align: center;">Không phát hiện cột số liệu để dựng biểu đồ. Hãy thử câu lệnh có COUNT(), SUM(), AVG() hoặc cột số.</div>`;
      return;
    }

    const labelName = res.columns[labelColIdx];
    const numName = res.columns[numColIdx];

    const chartData = res.rows.slice(0, 15).map(r => ({
      label: String(r[labelColIdx] ?? 'N/A'),
      value: Number(r[numColIdx] ?? 0)
    }));

    const maxVal = Math.max(...chartData.map(d => d.value), 1);

    const colors = ['#3b82f6', '#10b981', '#f59e0b', '#8b5cf6', '#ec4899', '#06b6d4', '#22c55e', '#f97316'];

    chartEl.innerHTML = `
      <div style="font-size: 13px; font-weight: 600; margin-bottom: 12px; color: var(--text-primary); display: flex; justify-content: space-between;">
        <span>📊 Biểu đồ so sánh: <b>${numName}</b> theo <b>${labelName}</b></span>
        <span style="font-size: 11px; color: var(--text-muted);">(Top 15 bản ghi)</span>
      </div>
      <div style="display: flex; flex-direction: column; gap: 8px;">
        ${chartData.map((d, i) => {
          const pct = ((d.value / maxVal) * 100).toFixed(1);
          const color = colors[i % colors.length];
          return `
            <div style="display: flex; align-items: center; gap: 10px; font-size: 11px;">
              <span style="width: 140px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; text-align: right; color: var(--text-secondary); font-family: var(--font-mono);">${d.label}</span>
              <div style="flex: 1; background: rgba(255,255,255,0.05); border-radius: 4px; height: 18px; overflow: hidden; display: flex; align-items: center;">
                <div style="width: ${pct}%; background: ${color}; height: 100%; border-radius: 4px; transition: width 0.4s ease; min-width: 4px;"></div>
              </div>
              <span style="width: 70px; text-align: right; font-weight: 700; color: var(--text-primary); font-family: var(--font-mono);">${d.value.toLocaleString()}</span>
            </div>
          `;
        }).join('')}
      </div>
    `;
  },

  // -------------------------------------------------------------
  // Export Capabilities (JSON, CSV, Excel)
  // -------------------------------------------------------------

  exportJSON() {
    if (!this.lastResult || !this.lastResult.rows) {
      Toast.error('Chưa có dữ liệu để xuất');
      return;
    }

    const data = this.lastResult.rows.map(row => {
      const obj = {};
      this.lastResult.columns.forEach((col, i) => {
        obj[col] = row[i];
      });
      return obj;
    });

    const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `query_export_${Date.now()}.json`;
    a.click();
    URL.revokeObjectURL(url);
    Toast.success('Đã xuất file JSON thành công');
  },

  exportCSV() {
    if (!this.lastResult || !this.lastResult.rows) {
      Toast.error('Chưa có dữ liệu để xuất');
      return;
    }

    let csv = '\uFEFF' + this.lastResult.columns.join(',') + '\n';
    this.lastResult.rows.forEach(row => {
      csv += row.map(v => `"${(v ?? '').toString().replace(/"/g, '""')}"`).join(',') + '\n';
    });

    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `query_export_${Date.now()}.csv`;
    a.click();
    URL.revokeObjectURL(url);
    Toast.success('Đã xuất file CSV thành công');
  },

  exportExcel() {
    if (!this.lastResult || !this.lastResult.rows) {
      Toast.error('Chưa có dữ liệu để xuất');
      return;
    }

    let xml = `
      <html xmlns:o="urn:schemas-microsoft-com:office:office" xmlns:x="urn:schemas-microsoft-com:office:excel" xmlns="http://www.w3.org/TR/REC-html40">
      <head><!--[if gte mso 9]><xml><x:ExcelWorkbook><x:ExcelWorksheets><x:ExcelWorksheet><x:Name>SQL Result</x:Name><x:WorksheetOptions><x:DisplayGridlines/></x:WorksheetOptions></x:ExcelWorksheet></x:ExcelWorksheets></x:ExcelWorkbook></xml><![endif]--><meta charset="utf-8"></head>
      <body><table>
        <thead><tr>${this.lastResult.columns.map(c => `<th style="background:#f1f5f9;border:1px solid #cbd5e1;">${c}</th>`).join('')}</tr></thead>
        <tbody>
          ${this.lastResult.rows.map(r => `<tr>${r.map(v => `<td style="border:1px solid #e2e8f0;">${v ?? ''}</td>`).join('')}</tr>`).join('')}
        </tbody>
      </table></body></html>
    `;

    const blob = new Blob([xml], { type: 'application/vnd.ms-excel;charset=utf-8;' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `query_export_${Date.now()}.xls`;
    a.click();
    URL.revokeObjectURL(url);
    Toast.success('Đã xuất file Excel thành công');
  },

  async optimizeDB() {
    try {
      Toast.info('Đang chạy chống phân mảnh và tối ưu hóa CSDL (VACUUM)...');
      const res = await API.sqlOptimize();
      Toast.success(res.message || 'Đã tối ưu CSDL thành công!');
      this.loadTables();
    } catch (err) {
      Toast.error('Lỗi tối ưu CSDL: ' + err.message);
    }
  },

  async checkIntegrity() {
    try {
      Toast.info('Đang kiểm tra tính toàn vẹn CSDL (PRAGMA integrity_check)...');
      const res = await API.sqlCheck();
      if (res.is_ok) {
        Toast.success('Kiểm tra Toàn vẹn CSDL: Hoàn hảo (Status: ' + res.status + ')');
      } else {
        Toast.warn('Cảnh báo toàn vẹn: ' + res.status);
      }
    } catch (err) {
      Toast.error('Lỗi kiểm tra toàn vẹn: ' + err.message);
    }
  },

  async backupDB() {
    try {
      Toast.info('Đang tạo bản sao lưu CSDL Snapshot...');
      const res = await API.sqlBackup();
      Toast.success('Đã tạo bản snapshot thành công: ' + res.backup_file);
    } catch (err) {
      Toast.error('Lỗi tạo sao lưu: ' + err.message);
    }
  },
};
