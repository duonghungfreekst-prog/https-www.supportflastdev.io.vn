package main

import (
	"encoding/json"
	"net/http"
	"os"
	"sync/atomic"
)

// handleStatusAPI trả về thông tin trạng thái cho giao diện Desktop
func (c *DesktopClient) handleStatusAPI(w http.ResponseWriter, r *http.Request) {
	c.mu.RLock()
	tokensCount := len(c.tokens)
	renderURL := c.config.RenderURL
	apiKey := c.config.MasterAPIKey
	port := c.config.LocalPort
	lastSyncStr := ""
	if !c.lastSync.IsZero() {
		lastSyncStr = c.lastSync.Format("15:04:05 02/01/2006")
	}
	c.mu.RUnlock()

	maskedKey := apiKey
	if len(apiKey) > 16 {
		maskedKey = apiKey[:10] + "..." + apiKey[len(apiKey)-4:]
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"enabled":         c.isProxyEnabled(),
		"tokens_in_ram":   tokensCount,
		"render_url":      renderURL,
		"master_api_key":  apiKey,
		"masked_key":      maskedKey,
		"local_port":      port,
		"total_requests":  atomic.LoadUint64(&c.totalReqs),
		"total_tokens":    atomic.LoadUint64(&c.totalTokens),
		"last_sync":       lastSyncStr,
		"base_url":        "http://127.0.0.1:" + string(rune(port)),
	})
}

// handleConfigAPI cập nhật cấu hình từ giao diện Desktop
func (c *DesktopClient) handleConfigAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if r.Method == http.MethodPost {
		var patch struct {
			RenderURL    string `json:"render_url"`
			MasterAPIKey string `json:"master_api_key"`
			LocalPort    int    `json:"local_port"`
			Enabled      *bool  `json:"enabled,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			http.Error(w, `{"error":"Invalid JSON"}`, http.StatusBadRequest)
			return
		}

		c.mu.Lock()
		if patch.RenderURL != "" {
			c.config.RenderURL = patch.RenderURL
		}
		if patch.MasterAPIKey != "" {
			c.config.MasterAPIKey = patch.MasterAPIKey
		}
		if patch.LocalPort > 0 {
			c.config.LocalPort = patch.LocalPort
		}
		if patch.Enabled != nil {
			c.proxyEnabled = *patch.Enabled
		}
		c.mu.Unlock()

		// Lưu file config.json
		cfgBytes, _ := json.MarshalIndent(c.config, "", "  ")
		_ = os.WriteFile("config.json", cfgBytes, 0644)

		// Thử đồng bộ ngay với URL mới
		go c.syncWithRender()

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Đã lưu cấu hình và kết nối lại thành công",
		})
		return
	}

	c.mu.RLock()
	cfgCopy := c.config
	c.mu.RUnlock()
	json.NewEncoder(w).Encode(cfgCopy)
}

// handleToggleProxy bật/tắt proxy trực tiếp
func (c *DesktopClient) handleToggleProxy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	newState := !c.isProxyEnabled()
	c.setProxyEnabled(newState)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"enabled": newState,
	})
}

// handleManualSyncEndpoint kích hoạt đồng bộ từ giao diện
func (c *DesktopClient) handleManualSyncEndpoint(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.syncWithRender()
	c.mu.RLock()
	cnt := len(c.tokens)
	c.mu.RUnlock()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"tokens_in_ram": cnt,
		"message":       "Đồng bộ hoàn tất từ Render Cloud!",
	})
}

// handleDesktopUI phục vụ giao diện Webview độc lập của App Desktop
func (c *DesktopClient) handleDesktopUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(desktopHTMLContent))
}

const desktopHTMLContent = `<!DOCTYPE html>
<html lang="vi">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>9Router Desktop Gateway</title>
  <script src="https://cdn.tailwindcss.com"></script>
  <link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.4.0/css/all.min.css">
  <style>
    @import url('https://fonts.googleapis.com/css2?family=Plus+Jakarta+Sans:wght@400;500;600;700;800&family=JetBrains+Mono:wght@400;500;700&display=swap');
    body {
      font-family: 'Plus Jakarta Sans', sans-serif;
      background: radial-gradient(circle at top right, #1e1b4b 0%, #0f172a 60%, #020617 100%);
      user-select: none;
    }
    code, pre, .font-mono {
      font-family: 'JetBrains Mono', monospace;
    }
    .glass-card {
      background: rgba(30, 41, 59, 0.75);
      backdrop-filter: blur(16px);
      border: 1px solid rgba(255, 255, 255, 0.08);
    }
  </style>
</head>
<body class="text-slate-100 p-5 min-h-screen flex flex-col justify-between">

  <!-- Header & Tray Status -->
  <div>
    <header class="flex items-center justify-between pb-4 border-b border-slate-800">
      <div class="flex items-center gap-3">
        <div class="w-10 h-10 rounded-xl bg-gradient-to-tr from-indigo-500 to-emerald-400 flex items-center justify-center text-white text-xl shadow-lg shadow-indigo-500/20">
          <i class="fa-solid fa-bolt"></i>
        </div>
        <div>
          <h1 class="text-lg font-bold text-white flex items-center gap-2">
            9Router Desktop
            <span id="statusBadge" class="px-2 py-0.5 rounded-full text-[10px] font-semibold bg-emerald-500/10 text-emerald-400 border border-emerald-500/20">
              Đang Chạy
            </span>
          </h1>
          <p class="text-[11px] text-slate-400">Local Proxy & LLM DNS (0ms Latency)</p>
        </div>
      </div>

      <!-- Toggle Switch -->
      <button onclick="toggleProxy()" id="btnToggle" class="px-3.5 py-1.5 rounded-lg bg-emerald-500 hover:bg-emerald-600 text-white text-xs font-semibold shadow-md transition flex items-center gap-1.5">
        <i class="fa-solid fa-power-off"></i> <span id="toggleText">Bật Proxy</span>
      </button>
    </header>

    <!-- Metrics Grid -->
    <div class="grid grid-cols-3 gap-3 my-4">
      <div class="glass-card rounded-xl p-3 text-center">
        <span class="text-[10px] uppercase tracking-wider text-slate-400 font-medium block">Kho RAM</span>
        <span id="ramTokensCount" class="text-2xl font-bold text-emerald-400 block mt-1">--</span>
        <span class="text-[10px] text-slate-500">tài khoản AI</span>
      </div>

      <div class="glass-card rounded-xl p-3 text-center">
        <span class="text-[10px] uppercase tracking-wider text-slate-400 font-medium block">Đã Xử Lý</span>
        <span id="statRequests" class="text-2xl font-bold text-amber-400 block mt-1">0</span>
        <span class="text-[10px] text-slate-500">requests</span>
      </div>

      <div class="glass-card rounded-xl p-3 text-center">
        <span class="text-[10px] uppercase tracking-wider text-slate-400 font-medium block">Độ Trễ</span>
        <span class="text-2xl font-bold text-sky-400 block mt-1">0ms</span>
        <span class="text-[10px] text-slate-500">trung gian</span>
      </div>
    </div>

    <!-- Base URL Box -->
    <div class="glass-card rounded-xl p-3.5 mb-4 border border-indigo-500/30 bg-indigo-950/20">
      <div class="flex items-center justify-between text-xs mb-1">
        <span class="text-indigo-300 font-semibold flex items-center gap-1.5">
          <i class="fa-solid fa-link text-indigo-400"></i> Base URL cho Antigravity / Cursor:
        </span>
        <button onclick="copyBaseURL()" class="text-xs text-indigo-400 hover:text-white transition flex items-center gap-1">
          <i class="fa-solid fa-copy"></i> Chép
        </button>
      </div>
      <div class="font-mono text-xs text-emerald-300 bg-slate-900/90 p-2 rounded border border-slate-800 flex items-center justify-between">
        <span id="localBaseUrl">http://127.0.0.1:8080/v1</span>
      </div>
    </div>

    <!-- Configuration Form -->
    <div class="glass-card rounded-xl p-4 space-y-3">
      <div class="flex items-center justify-between">
        <h2 class="text-xs font-bold uppercase tracking-wider text-slate-300 flex items-center gap-1.5">
          <i class="fa-solid fa-sliders text-indigo-400"></i> Cấu Hình Kết Nối Render
        </h2>
        <button onclick="triggerSync()" id="btnSync" class="text-[11px] text-indigo-400 hover:text-indigo-300 transition flex items-center gap-1">
          <i class="fa-solid fa-rotate"></i> Đồng bộ ngay
        </button>
      </div>

      <div>
        <label class="text-[11px] text-slate-400 block mb-1">URL Máy Chủ Web Render:</label>
        <input id="inputRenderURL" type="text" placeholder="https://supportflastdev.io.vn" 
               class="w-full px-3 py-2 rounded-lg bg-slate-900 border border-slate-700 text-xs text-white focus:outline-none focus:border-indigo-500 font-mono">
      </div>

      <div>
        <label class="text-[11px] text-slate-400 block mb-1">Master API Key (sk-9router-...):</label>
        <div class="relative">
          <input id="inputAPIKey" type="password" placeholder="sk-9router-xxxxxxxx" 
                 class="w-full pl-3 pr-8 py-2 rounded-lg bg-slate-900 border border-slate-700 text-xs text-white focus:outline-none focus:border-indigo-500 font-mono">
          <button onclick="toggleKeyVisibility()" class="absolute right-2.5 top-1/2 -translate-y-1/2 text-slate-500 hover:text-slate-300 text-xs">
            <i id="eyeIcon" class="fa-solid fa-eye"></i>
          </button>
        </div>
      </div>

      <div class="flex gap-2 pt-1">
        <button onclick="saveConfig()" id="btnSave" class="flex-1 py-2 rounded-lg bg-indigo-600 hover:bg-indigo-500 text-white font-semibold text-xs shadow transition flex items-center justify-center gap-1.5">
          <i class="fa-solid fa-check"></i> Lưu & Kết Nối Lại
        </button>
      </div>
    </div>
  </div>

  <!-- Footer Notice -->
  <footer class="mt-4 pt-3 border-t border-slate-800/80 text-[11px] text-slate-500 flex items-center justify-between">
    <span class="flex items-center gap-1">
      <i class="fa-solid fa-tray text-indigo-400"></i> Chạy nền ở Khay Hệ Thống
    </span>
    <span id="lastSyncTime">Chưa đồng bộ</span>
  </footer>

  <script>
    let isShowingKey = false;

    function toggleKeyVisibility() {
      const input = document.getElementById('inputAPIKey');
      const icon = document.getElementById('eyeIcon');
      isShowingKey = !isShowingKey;
      input.type = isShowingKey ? 'text' : 'password';
      icon.className = isShowingKey ? 'fa-solid fa-eye-slash' : 'fa-solid fa-eye';
    }

    async function loadStatus() {
      try {
        const res = await fetch('/api/status');
        const data = await res.json();

        // Cập nhật số token trong RAM
        document.getElementById('ramTokensCount').innerText = data.tokens_in_ram || 0;
        document.getElementById('statRequests').innerText = data.total_requests || 0;
        document.getElementById('localBaseUrl').innerText = 'http://127.0.0.1:' + data.local_port + '/v1';
        document.getElementById('lastSyncTime').innerText = data.last_sync ? 'Lần cuối: ' + data.last_sync : 'Vừa xong';

        // Điền input nếu trống
        if (!document.getElementById('inputRenderURL').value) {
          document.getElementById('inputRenderURL').value = data.render_url || '';
        }
        if (!document.getElementById('inputAPIKey').value) {
          document.getElementById('inputAPIKey').value = data.master_api_key || '';
        }

        // Cập nhật nút trạng thái bật/tắt
        const isEnabled = data.enabled;
        const btnToggle = document.getElementById('btnToggle');
        const toggleText = document.getElementById('toggleText');
        const badge = document.getElementById('statusBadge');

        if (isEnabled) {
          btnToggle.className = 'px-3.5 py-1.5 rounded-lg bg-emerald-600 hover:bg-emerald-500 text-white text-xs font-semibold shadow-md transition flex items-center gap-1.5';
          toggleText.innerText = 'Đang Bật';
          badge.className = 'px-2 py-0.5 rounded-full text-[10px] font-semibold bg-emerald-500/10 text-emerald-400 border border-emerald-500/20';
          badge.innerText = 'Đang Chạy';
        } else {
          btnToggle.className = 'px-3.5 py-1.5 rounded-lg bg-rose-600 hover:bg-rose-500 text-white text-xs font-semibold shadow-md transition flex items-center gap-1.5';
          toggleText.innerText = 'Đã Tắt';
          badge.className = 'px-2 py-0.5 rounded-full text-[10px] font-semibold bg-rose-500/10 text-rose-400 border border-rose-500/20';
          badge.innerText = 'Tạm Dừng';
        }
      } catch (err) {
        console.error(err);
      }
    }

    async function toggleProxy() {
      try {
        await fetch('/api/toggle', { method: 'POST' });
        loadStatus();
      } catch (err) {
        alert('Lỗi kết nối tới client local');
      }
    }

    async function triggerSync() {
      const btn = document.getElementById('btnSync');
      btn.innerHTML = '<i class="fa-solid fa-spinner animate-spin"></i> Đang tải...';
      try {
        const res = await fetch('/api/sync-now', { method: 'POST' });
        const data = await res.json();
        alert(data.message || 'Đồng bộ hoàn tất!');
        loadStatus();
      } catch (err) {
        alert('Không thể đồng bộ với Render');
      } finally {
        btn.innerHTML = '<i class="fa-solid fa-rotate"></i> Đồng bộ ngay';
      }
    }

    async function saveConfig() {
      const render_url = document.getElementById('inputRenderURL').value.trim();
      const master_api_key = document.getElementById('inputAPIKey').value.trim();
      const btn = document.getElementById('btnSave');
      btn.disabled = true;
      btn.innerHTML = '<i class="fa-solid fa-spinner animate-spin"></i> Đang lưu...';

      try {
        const res = await fetch('/api/config', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({ render_url, master_api_key })
        });
        const data = await res.json();
        if (data.success) {
          alert('Đã lưu cấu hình và kết nối lại thành công!');
          loadStatus();
        }
      } catch (err) {
        alert('Lỗi lưu cấu hình');
      } finally {
        btn.disabled = false;
        btn.innerHTML = '<i class="fa-solid fa-check"></i> Lưu & Kết Nối Lại';
      }
    }

    function copyBaseURL() {
      const url = document.getElementById('localBaseUrl').innerText;
      navigator.clipboard.writeText(url);
      alert('Đã chép Base URL: ' + url);
    }

    // Tự động làm mới mỗi 3 giây
    loadStatus();
    setInterval(loadStatus, 3000);
  </script>
</body>
</html>`
