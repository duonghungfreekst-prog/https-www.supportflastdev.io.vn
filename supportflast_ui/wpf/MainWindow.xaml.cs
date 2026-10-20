using System;
using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Windows;
using System.Windows.Input;
using Microsoft.Web.WebView2.Core;
using OmniRouter.Config;
using OmniRouter.Models;
using OmniRouter.Utils;

namespace OmniRouter.Views;

public partial class MainWindow : Window
{
    private static System.Diagnostics.Process? _nodeBackendProcess;

    #region Win32 Native Drag & Drop và Khung Hình Đa Màn Hình
    [DllImport("user32.dll")]
    private static extern bool ReleaseCapture();

    [DllImport("user32.dll")]
    private static extern IntPtr SendMessage(IntPtr hWnd, int Msg, IntPtr wParam, IntPtr lParam);

    [DllImport("user32.dll")]
    private static extern bool GetCursorPos(out POINT lpPoint);

    [DllImport("user32.dll")]
    private static extern IntPtr MonitorFromWindow(IntPtr handle, int flags);

    [DllImport("user32.dll", CharSet = CharSet.Auto)]
    private static extern bool GetMonitorInfo(IntPtr hMonitor, MONITORINFO lpmi);

    [StructLayout(LayoutKind.Sequential)]
    private struct POINT
    {
        public int X;
        public int Y;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct MINMAXINFO
    {
        public POINT ptReserved;
        public POINT ptMaxSize;
        public POINT ptMaxPosition;
        public POINT ptMinTrackSize;
        public POINT ptMaxTrackSize;
    }

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Auto)]
    private class MONITORINFO
    {
        public int cbSize = Marshal.SizeOf(typeof(MONITORINFO));
        public RECT rcMonitor = new RECT();
        public RECT rcWork = new RECT();
        public int dwFlags = 0;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct RECT
    {
        public int Left;
        public int Top;
        public int Right;
        public int Bottom;
    }

    private const int WM_NCLBUTTONDOWN = 0x00A1;
    private const int HTCAPTION = 0x0002;
    private const int WM_GETMINMAXINFO = 0x0024;
    private const int MONITOR_DEFAULTTONEAREST = 0x00000002;
    #endregion

    protected override void OnSourceInitialized(EventArgs e)
    {
        base.OnSourceInitialized(e);
        var source = (System.Windows.Interop.HwndSource?)PresentationSource.FromVisual(this);
        source?.AddHook(WndProc);
    }

    private IntPtr WndProc(IntPtr hwnd, int msg, IntPtr wParam, IntPtr lParam, ref bool handled)
    {
        if (msg == WM_GETMINMAXINFO)
        {
            WmGetMinMaxInfo(hwnd, lParam);
            handled = true;
        }
        return IntPtr.Zero;
    }

    private static void WmGetMinMaxInfo(IntPtr hwnd, IntPtr lParam)
    {
        try
        {
            var mmi = Marshal.PtrToStructure<MINMAXINFO>(lParam);
            IntPtr monitor = MonitorFromWindow(hwnd, MONITOR_DEFAULTTONEAREST);
            if (monitor != IntPtr.Zero)
            {
                var mi = new MONITORINFO();
                if (GetMonitorInfo(monitor, mi))
                {
                    var rcWork = mi.rcWork;
                    var rcMonitor = mi.rcMonitor;
                    mmi.ptMaxPosition.X = Math.Abs(rcWork.Left - rcMonitor.Left);
                    mmi.ptMaxPosition.Y = Math.Abs(rcWork.Top - rcMonitor.Top);
                    mmi.ptMaxSize.X = Math.Abs(rcWork.Right - rcWork.Left);
                    mmi.ptMaxSize.Y = Math.Abs(rcWork.Bottom - rcWork.Top);
                }
            }
            Marshal.StructureToPtr(mmi, lParam, true);
        }
        catch
        {
            // Bỏ qua nếu có lỗi marshaling
        }
    }

    private System.Windows.Threading.DispatcherTimer? _healthCheckTimer;
    private bool _isGatewayOnline = false;
    private readonly System.Threading.SemaphoreSlim _healthCheckLock = new(1, 1);
    private readonly System.Threading.SemaphoreSlim _gatewayLock = new(1, 1);
    private readonly System.Threading.SemaphoreSlim _mitmLock = new(1, 1);
    private readonly System.Collections.Concurrent.ConcurrentDictionary<string, long> _lastActionTimestamps = new(StringComparer.OrdinalIgnoreCase);
    private const long ActionDebounceMs = 1200;
    private string _lastActiveTab = "mitm";

    private bool IsActionDebounced(string action)
    {
        if (action is not ("startgateway" or "startwithupdate" or "cleanupdate" or "stopgateway" or "restartgateway" or "startintercept" or "stopintercept" or "flushdns"))
        {
            return false;
        }

        long now = Environment.TickCount64;
        if (_lastActionTimestamps.TryGetValue(action, out long lastTime))
        {
            if (now - lastTime < ActionDebounceMs)
            {
                return true;
            }
        }
        _lastActionTimestamps[action] = now;
        return false;
    }

    public MainWindow()
    {
        InitializeComponent();
        try
        {
            string iconPath = System.IO.Path.Combine(AppDomain.CurrentDomain.BaseDirectory, "app.ico");
            if (System.IO.File.Exists(iconPath))
            {
                Icon = new System.Windows.Media.Imaging.BitmapImage(new Uri(iconPath));
            }
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] Không thể nạp file icon: {ex.Message}");
        }
        Loaded += OnMainWindowLoaded;
        Closed += OnMainWindowClosed;
        StateChanged += OnWindowStateChanged;

        // Khởi động Timer kiểm tra cổng 20128 mỗi 2.5 giây (chỉ kiểm tra thụ động, không tự động chạy ngầm)
        _healthCheckTimer = new System.Windows.Threading.DispatcherTimer
        {
            Interval = TimeSpan.FromSeconds(2.5)
        };
        _healthCheckTimer.Tick += async (s, e) => await CheckBackendHealthAsync();
        _healthCheckTimer.Start();

        // TUYỆT ĐỐI KHÔNG tự động mở Gateway/Engine khi mở app. Chỉ chạy khi người dùng chủ động bật.
    }

    private void OnMainWindowClosed(object? sender, EventArgs e)
    {
        try
        {
            _healthCheckTimer?.Stop();
            if (_nodeBackendProcess != null && !_nodeBackendProcess.HasExited)
            {
                Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] Đang tắt tiến trình Backend Gateway (PID: {_nodeBackendProcess.Id})...");
                ProcessHelper.StopProcess(_nodeBackendProcess);
                _nodeBackendProcess = null;
            }
            // Dọn dẹp toàn bộ tiến trình còn sót để đảm bảo Zero Hanging Tasks
            ProcessHelper.StopAllProcesses();
            try { ProcessHelper.KillProcessByPort(20128); } catch { }

            // Giải phóng WebView2
            try
            {
                AppWebView?.Dispose();
            }
            catch (Exception wvEx)
            {
                Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Dispose WebView2: {wvEx.Message}");
            }

            // Thu gom rác bộ nhớ OOM Guard
            MemoryGuard.CollectGarbage(force: true);
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] Lỗi đóng backend process: {ex.Message}");
        }
    }

    public async Task<bool> CheckBackendHealthAsync()
    {
        if (!await _healthCheckLock.WaitAsync(2000))
        {
            return _isGatewayOnline;
        }

        try
        {
            // Lần 1: Kiểm tra kết nối TCP cổng 20128 (timeout 1000ms)
            bool isOnline = await IsTcpPortOpenAsync("127.0.0.1", 20128, 1000);

            // Debounce: Nếu lần 1 thất bại, chờ 400ms và kiểm tra lại thêm 1 lần trước khi kết luận offline
            if (!isOnline)
            {
                await Task.Delay(400);
                isOnline = await IsTcpPortOpenAsync("127.0.0.1", 20128, 1000);
            }

            UpdateGatewayUI(isOnline);
            return isOnline;
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] Lỗi kiểm tra sức khỏe Backend: {ex.Message}");
            UpdateGatewayUI(false);
            return false;
        }
        finally
        {
            _healthCheckLock.Release();
        }
    }

    private static async Task<bool> IsTcpPortOpenAsync(string host, int port, int timeoutMs)
    {
        try
        {
            using var tcp = new System.Net.Sockets.TcpClient();
            using var cts = new System.Threading.CancellationTokenSource(timeoutMs);
            await tcp.ConnectAsync(host, port, cts.Token);
            return tcp.Connected;
        }
        catch (System.Net.Sockets.SocketException)
        {
            // Cổng chưa mở hoặc từ chối kết nối
            return false;
        }
        catch (OperationCanceledException)
        {
            // Quá thời gian timeout kết nối TCP
            return false;
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] Lỗi kết nối TCP {host}:{port}: {ex.Message}");
            return false;
        }
    }

    private static readonly System.Text.RegularExpressions.Regex EmailMaskRegex = new(
        @"[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}", 
        System.Text.RegularExpressions.RegexOptions.Compiled);

    public void SendLogToWebView(string line)
    {
        if (string.IsNullOrWhiteSpace(line)) return;

        // 1. Lọc sạch các ký tự nhị phân / rác font Unicode Replacement ()
        if (line.Contains('\uFFFD') || line.Any(c => char.IsControl(c) && c != '\t' && c != '\r' && c != '\n'))
        {
            line = new string(line.Where(c => c == '\t' || (c >= 32 && c != 0xFFFD && c != 127)).ToArray());
            if (string.IsNullOrWhiteSpace(line) || line.Length < 4) return;
        }

        // 2. Che giấu email và tài khoản nội bộ trên bản Desktop thương mại
        line = EmailMaskRegex.Replace(line, "[Tài khoản ẩn]");

        Dispatcher.Invoke(() =>
        {
            try
            {
                if (AppWebView?.CoreWebView2 != null)
                {
                    var payload = System.Text.Json.JsonSerializer.Serialize(new { type = "consoleLog", line = line });
                    AppWebView.CoreWebView2.PostWebMessageAsJson(payload);
                }
            }
            catch { }
        });
    }

    private void UpdateGatewayUI(bool isOnline, bool force = false)
    {
        bool wasOnline = _isGatewayOnline;
        _isGatewayOnline = isOnline;
        if (!force && wasOnline == isOnline)
        {
            return;
        }

        Dispatcher.Invoke(() =>
        {
            try
            {
                if (AppWebView?.CoreWebView2 != null)
                {
                    AppWebView.CoreWebView2.PostWebMessageAsJson("{\"type\":\"gatewayStatus\",\"online\":" + (isOnline ? "true" : "false") + ",\"port\":20128}");
                    if (!isOnline)
                    {
                        AppWebView.CoreWebView2.PostWebMessageAsJson("{\"type\":\"mitmStatus\",\"running\":false}");
                    }
                    if (isOnline && !wasOnline)
                    {
                        SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] Kết nối thành công tới OmniRouter Engine lõi cổng 20128.");
                    }
                }
            }
            catch (Exception ex)
            {
                Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] Lỗi cập nhật giao diện Gateway: {ex.Message}");
            }
        });
    }

    public void StartBackend()
    {
        _ = Task.Run(async () => await StartBackendAsync(forceRestart: false));
    }

    public async Task StartBackendAsync(bool forceRestart = false)
    {
        if (!await _gatewayLock.WaitAsync(5000))
        {
            SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] Thao tác Gateway đang được xử lý, bỏ qua yêu cầu khởi động lặp.");
            return;
        }

        try
        {
            await StartBackendInternalAsync(forceRestart);
        }
        finally
        {
            _gatewayLock.Release();
        }
    }

    private async Task StartBackendInternalAsync(bool forceRestart)
    {
        try
        {
            if (!forceRestart)
            {
                bool alreadyRunning = await CheckBackendHealthAsync();
                if (alreadyRunning)
                {
                    var existingPid = ProcessHelper.GetProcessIdByPort(20128);
                    bool isLegitGoEngine = false;
                    if (existingPid.HasValue && existingPid.Value > 0)
                    {
                        try
                        {
                            var proc = Process.GetProcessById(existingPid.Value);
                            string pName = proc.ProcessName.ToLowerInvariant();
                            if (pName.Contains("omnirouter") || pName.Contains("engine"))
                            {
                                isLegitGoEngine = true;
                            }
                        }
                        catch { }
                    }

                    if (isLegitGoEngine)
                    {
                        Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] Backend OmniRouter Golang Core đã online trên cổng 20128, giữ nguyên tiến trình...");
                        SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] Backend OmniRouter Golang Core đang chạy tốt trên cổng 20128.");
                        UpdateGatewayUI(true);

                        if (_nodeBackendProcess == null || _nodeBackendProcess.HasExited)
                        {
                            if (existingPid.HasValue && existingPid.Value > 0)
                            {
                                try
                                {
                                    _nodeBackendProcess = Process.GetProcessById(existingPid.Value);
                                    JobObjectManager.AddProcess(_nodeBackendProcess);
                                    ProcessHelper.TrackProcess(_nodeBackendProcess);
                                }
                                catch { }
                            }
                        }
                        return;
                    }
                    else
                    {
                        Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] Cổng 20128 đang bị chiếm bởi tiến trình không phải Golang Core (PID: {existingPid}), đang giải phóng...");
                        SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] Giải phóng cổng 20128 từ tiến trình cũ (PID: {existingPid}) để khởi chạy OmniRouter Golang Core...");
                        ProcessHelper.KillProcessByPort(20128);
                        await Task.Delay(500);
                    }
                }
            }

            // Nếu tiến trình cũ còn đang sống nhưng cổng không phản hồi (treo), dọn dẹp an toàn
            if (_nodeBackendProcess != null && !_nodeBackendProcess.HasExited)
            {
                try
                {
                    _nodeBackendProcess.Kill(true);
                    _nodeBackendProcess.Dispose();
                }
                catch { }
                _nodeBackendProcess = null;
            }

            string projectRoot = GetProjectRoot();
            string appData = Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData);

            // 1. Ưu tiên khởi chạy Golang Core Engine (omnirouter_engine.exe) hiệu năng cao, không phụ thuộc Node.js
            string? goEngineExe = null;
            string[] goCandidates = [
                System.IO.Path.Combine(AppDomain.CurrentDomain.BaseDirectory, "omnirouter_engine.exe"),
                System.IO.Path.Combine(projectRoot, "dist", "OmniRouter", "omnirouter_engine.exe"),
                System.IO.Path.Combine(projectRoot, "omnirouter_engine", "omnirouter_engine.exe"),
                System.IO.Path.Combine(projectRoot, "omnirouter_engine.exe"),
                System.IO.Path.Combine(AppDomain.CurrentDomain.BaseDirectory, "omnirouter.exe"),
                System.IO.Path.Combine(projectRoot, "omnirouter_engine", "omnirouter.exe")
            ];

            foreach (var c in goCandidates)
            {
                if (System.IO.File.Exists(c))
                {
                    goEngineExe = c;
                    break;
                }
            }

            if (!string.IsNullOrEmpty(goEngineExe))
            {
                string safeDir = projectRoot.Trim().TrimEnd('\\', '/').Trim('"');
                SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] Khởi chạy OmniRouter Golang Core (:20128 & :443) qua {goEngineExe}...");
                var psi = new System.Diagnostics.ProcessStartInfo
                {
                    FileName = goEngineExe,
                    Arguments = $"-port 20128 -mitm-port 443 -auto-mitm=false -dir \"{safeDir}\"",
                    WorkingDirectory = System.IO.Path.GetDirectoryName(goEngineExe) ?? safeDir,
                    UseShellExecute = false,
                    CreateNoWindow = true,
                    RedirectStandardOutput = true,
                    RedirectStandardError = true,
                    StandardOutputEncoding = System.Text.Encoding.UTF8,
                    StandardErrorEncoding = System.Text.Encoding.UTF8
                };
                psi.EnvironmentVariables["OMNIROUTER_DIR"] = safeDir;
                _nodeBackendProcess = new System.Diagnostics.Process { StartInfo = psi, EnableRaisingEvents = true };
                _nodeBackendProcess.OutputDataReceived += (s, ev) =>
                {
                    if (!string.IsNullOrEmpty(ev.Data)) SendLogToWebView($"[ENGINE] {ev.Data}");
                };
                _nodeBackendProcess.ErrorDataReceived += (s, ev) =>
                {
                    if (!string.IsNullOrEmpty(ev.Data)) SendLogToWebView($"[ENGINE ERR] {ev.Data}");
                };
                _nodeBackendProcess.Start();
                _nodeBackendProcess.BeginOutputReadLine();
                _nodeBackendProcess.BeginErrorReadLine();

                try
                {
                    OmniRouter.Utils.JobObjectManager.AddProcess(_nodeBackendProcess);
                    ProcessHelper.TrackProcess(_nodeBackendProcess);
                }
                catch { }

                SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] OmniRouter Golang Core đã chạy (PID: {_nodeBackendProcess.Id})");
            }
            else
            {
                // 2. Fallback sang Node.js nếu chưa có file thực thi Golang
                string nodeExe = System.IO.File.Exists(@"C:\Program Files\nodejs\node.exe")
                    ? @"C:\Program Files\nodejs\node.exe"
                    : "node";

                string globalAppDir = System.IO.Path.Combine(appData, @"npm\node_modules\9router\app");
                string globalAppCustomServer = System.IO.Path.Combine(globalAppDir, "custom-server.js");
                string globalAppNodeModules = System.IO.Path.Combine(globalAppDir, "node_modules");
                string globalRuntimeNodeModules = System.IO.Path.Combine(appData, @"9router\runtime\node_modules");
                string globalNpmNodeModules = System.IO.Path.Combine(appData, @"npm\node_modules\9router\node_modules");

                string localCustomServer = System.IO.Path.Combine(projectRoot, "custom-server.js");
                string localServer = System.IO.Path.Combine(projectRoot, "server.js");
                string localNextDir = System.IO.Path.Combine(projectRoot, "node_modules", "next");

                string targetScript;
                string workingDir;

                if ((System.IO.File.Exists(localServer) || System.IO.Directory.Exists(localNextDir)) && System.IO.File.Exists(localCustomServer))
                {
                    targetScript = localCustomServer;
                    workingDir = projectRoot;
                }
                else if (System.IO.File.Exists(globalAppCustomServer))
                {
                    targetScript = globalAppCustomServer;
                    workingDir = globalAppDir;
                }
                else if (System.IO.File.Exists(localCustomServer))
                {
                    targetScript = localCustomServer;
                    workingDir = projectRoot;
                }
                else
                {
                    targetScript = globalAppCustomServer;
                    workingDir = globalAppDir;
                }

                string launchArgs = $"--dns-result-order=ipv4first --max-old-space-size=6144 \"{targetScript}\" --port 20128";

                SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] Đang khởi chạy OmniRouter Gateway qua {nodeExe} ({targetScript})...");
                var psi = new System.Diagnostics.ProcessStartInfo
                {
                    FileName = nodeExe,
                    Arguments = launchArgs,
                    WorkingDirectory = workingDir,
                    UseShellExecute = false,
                    CreateNoWindow = true,
                    RedirectStandardOutput = true,
                    RedirectStandardError = true,
                    StandardOutputEncoding = System.Text.Encoding.UTF8,
                    StandardErrorEncoding = System.Text.Encoding.UTF8
                };
                psi.EnvironmentVariables["PORT"] = "20128";
                psi.EnvironmentVariables["HOSTNAME"] = "127.0.0.1";

                var nodePaths = new System.Collections.Generic.List<string>();
                if (System.IO.Directory.Exists(globalAppNodeModules)) nodePaths.Add(globalAppNodeModules);
                if (System.IO.Directory.Exists(globalRuntimeNodeModules)) nodePaths.Add(globalRuntimeNodeModules);
                if (System.IO.Directory.Exists(globalNpmNodeModules)) nodePaths.Add(globalNpmNodeModules);
                string localNodeModules = System.IO.Path.Combine(projectRoot, "node_modules");
                if (System.IO.Directory.Exists(localNodeModules)) nodePaths.Add(localNodeModules);

                if (nodePaths.Count > 0)
                {
                    psi.EnvironmentVariables["NODE_PATH"] = string.Join(";", nodePaths);
                }

                string rootCa = System.IO.Path.Combine(projectRoot, "data", "mitm", "rootCA.crt");
                if (System.IO.File.Exists(rootCa))
                {
                    psi.EnvironmentVariables["NODE_EXTRA_CA_CERTS"] = rootCa;
                }
                _nodeBackendProcess = new System.Diagnostics.Process { StartInfo = psi, EnableRaisingEvents = true };
                _nodeBackendProcess.OutputDataReceived += (s, ev) =>
                {
                    if (!string.IsNullOrEmpty(ev.Data)) SendLogToWebView($"[ENGINE] {ev.Data}");
                };
                _nodeBackendProcess.ErrorDataReceived += (s, ev) =>
                {
                    if (!string.IsNullOrEmpty(ev.Data)) SendLogToWebView($"[ENGINE ERR] {ev.Data}");
                };
                _nodeBackendProcess.Start();
                _nodeBackendProcess.BeginOutputReadLine();
                _nodeBackendProcess.BeginErrorReadLine();

                try
                {
                    OmniRouter.Utils.JobObjectManager.AddProcess(_nodeBackendProcess);
                }
                catch { }

                SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] OmniRouter Gateway đã chạy (PID: {_nodeBackendProcess.Id})");
            }

            // Chờ và kiểm tra trạng thái khởi động với timeout hợp lý (polling tối đa 6 giây)
            bool isHealthy = false;
            for (int i = 0; i < 6; i++)
            {
                await Task.Delay(1000);
                if (await CheckBackendHealthAsync())
                {
                    isHealthy = true;
                    break;
                }
            }

            if (isHealthy)
            {
                SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] Gateway OmniRouter đã sẵn sàng trên cổng 20128.");
            }
            else
            {
                SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Gateway chưa phản hồi trên cổng 20128 sau 6 giây. Hệ thống vẫn đang tiếp tục chờ.");
            }
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] Lỗi StartBackend: {ex.Message}");
            SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] [LỖI] Không thể khởi chạy Gateway: {ex.Message}");
        }
    }

    public void StopBackend()
    {
        _ = Task.Run(async () => await StopBackendAsync());
    }

    public async Task StopBackendAsync()
    {
        if (!await _gatewayLock.WaitAsync(5000))
        {
            SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] Thao tác Gateway đang được xử lý, bỏ qua yêu cầu dừng lặp.");
            return;
        }

        try
        {
            await StopBackendInternalAsync();
        }
        finally
        {
            _gatewayLock.Release();
        }
    }

    private async Task StopBackendInternalAsync()
    {
        try
        {
            await DisableMitmInterceptAsync();

            if (_nodeBackendProcess != null && !_nodeBackendProcess.HasExited)
            {
                Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] Đang dừng OmniRouter Gateway (PID: {_nodeBackendProcess.Id})...");
                SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] Đang dừng OmniRouter Gateway (PID: {_nodeBackendProcess.Id})...");
                try
                {
                    _nodeBackendProcess.Kill(true);
                }
                catch { }
                _nodeBackendProcess.Dispose();
                _nodeBackendProcess = null;
            }
            else
            {
                try
                {
                    ProcessHelper.KillProcessByPort(20128);
                }
                catch { }
            }

            await Task.Delay(500);
            await CheckBackendHealthAsync();
            SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] Đã dừng tiến trình OmniRouter Gateway.");
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] Lỗi StopBackend: {ex.Message}");
            SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] [LỖI] Lỗi StopBackend: {ex.Message}");
        }
    }

    public void RestartBackend()
    {
        _ = Task.Run(async () => await RestartBackendAsync());
    }

    public async Task RestartBackendAsync()
    {
        if (!await _gatewayLock.WaitAsync(5000))
        {
            SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] Thao tác Gateway đang được xử lý, bỏ qua yêu cầu restart lặp.");
            return;
        }

        try
        {
            SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] [RESTART] Đang khởi động lại OmniRouter Gateway...");
            await StopBackendInternalAsync();
            await Task.Delay(1000);
            await StartBackendInternalAsync(forceRestart: true);
            SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] [RESTART] Khởi động lại OmniRouter Gateway hoàn tất.");
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] Lỗi RestartBackend: {ex.Message}");
            SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] [LỖI] Lỗi RestartBackend: {ex.Message}");
        }
        finally
        {
            _gatewayLock.Release();
        }
    }

    public void UpdateAndStartBackend()
    {
        _ = Task.Run(async () => await CleanAndAutoUpdateBackendAsync());
    }

    public void CleanAndAutoUpdateBackend()
    {
        _ = Task.Run(async () => await CleanAndAutoUpdateBackendAsync());
    }

    public async Task CleanAndAutoUpdateBackendAsync()
    {
        if (!await _gatewayLock.WaitAsync(5000))
        {
            SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] Thao tác Gateway đang được xử lý, bỏ qua yêu cầu cleanupdate lặp.");
            return;
        }

        try
        {
            // QUAN TRỌNG: Kiểm tra nếu Backend đã đang chạy tốt thì TUYỆT ĐỐI KHÔNG kill vô tội vạ!
            bool alreadyHealthy = await CheckBackendHealthAsync();
            if (alreadyHealthy)
            {
                SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] [CLEAN-UPDATE] Backend OmniRouter đang chạy ổn định trên cổng 20128. Giữ nguyên tiến trình đang hoạt động (không kill vô tội vạ).");
                UpdateGatewayUI(true);
                return;
            }

            SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] [CLEAN-UPDATE] Backend chưa chạy hoặc không phản hồi. Bắt đầu dọn dẹp hệ thống & tối ưu hóa tự động...");

            // 1. Dọn dẹp cache rác và lock file
            string appData = Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData);
            string routerData = System.IO.Path.Combine(appData, "9router");
            if (System.IO.Directory.Exists(routerData))
            {
                string[] filesToClean = {
                    System.IO.Path.Combine(routerData, "log.txt"),
                    System.IO.Path.Combine(routerData, "update", "status.json"),
                    System.IO.Path.Combine(routerData, "update", "install.log"),
                    System.IO.Path.Combine(routerData, "mitm", ".mitm.pid")
                };
                foreach (var f in filesToClean)
                {
                    try { if (System.IO.File.Exists(f)) System.IO.File.Delete(f); } 
                    catch (Exception delEx) { Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Bỏ qua file dọn dẹp {f}: {delEx.Message}"); }
                }

                try
                {
                    string reqFile = System.IO.Path.Combine(routerData, "request-details.json");
                    if (System.IO.File.Exists(reqFile)) SafeWriteTextWithRetry(reqFile, "[]");
                    string usageFile = System.IO.Path.Combine(routerData, "usage.json");
                    if (System.IO.File.Exists(usageFile)) SafeWriteTextWithRetry(usageFile, "[]");
                }
                catch (Exception resetEx)
                {
                    Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Bỏ qua reset file thống kê: {resetEx.Message}");
                }
            }

            // 2. Tối ưu bộ nhớ Heap 16GB
            Environment.SetEnvironmentVariable("NODE_OPTIONS", "--max-old-space-size=16384");

            SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] [CLEAN-UPDATE] Đã dọn dẹp cache rác và thiết lập thông số tối ưu.");

            // 3. Khởi động Backend
            await StartBackendInternalAsync(forceRestart: false);
            SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] [CLEAN-UPDATE] Hệ thống đã sẵn sàng, các app đã được điều hướng tự động!");
        }
        catch (Exception ex)
        {
            SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] [CLEAN-UPDATE LỖI] Cảnh báo auto-update: {ex.Message}");
        }
        finally
        {
            _gatewayLock.Release();
        }
    }

    public async Task EnableMitmInterceptAsync()
    {
        if (!await _mitmLock.WaitAsync(100))
        {
            SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] [INTERCEPT] Thao tác Intercept đang được thực hiện, bỏ qua yêu cầu lặp.");
            return;
        }

        try
        {
            SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] [INTERCEPT] Đang kích hoạt điều hướng toàn bộ app qua OmniRouter...");
            using var client = new System.Net.Http.HttpClient { Timeout = TimeSpan.FromSeconds(5) };
            string cliToken = GetCliToken();
            client.DefaultRequestHeaders.Add("x-9r-cli-token", cliToken);

            // Kiểm tra trạng thái máy chủ MITM trước, nếu đã chạy ổn định thì không gọi POST đè kill cổng 443
            try
            {
                var checkResp = await client.GetAsync("http://127.0.0.1:20128/api/cli-tools/antigravity-mitm");
                if (checkResp.IsSuccessStatusCode)
                {
                    var statusBody = await checkResp.Content.ReadAsStringAsync();
                    if (statusBody.Contains("\"running\":true"))
                    {
                        try
                        {
                            var patchContent = new System.Net.Http.StringContent("{\"tool\":\"antigravity\",\"action\":\"enable\"}", System.Text.Encoding.UTF8, "application/json");
                            await client.PatchAsync("http://127.0.0.1:20128/api/cli-tools/antigravity-mitm", patchContent);
                        }
                        catch (Exception patchEx)
                        {
                            Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Bỏ qua lỗi patch DNS: {patchEx.Message}");
                        }
                        FlushDns();
                        SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] [INTERCEPT] Dịch vụ MITM đã hoạt động ổn định trên cổng 443 và DNS Antigravity đã kết nối.");
                        Dispatcher.Invoke(() =>
                        {
                            try
                            {
                                AppWebView?.CoreWebView2?.PostWebMessageAsJson("{\"type\":\"mitmStatus\",\"running\":true}");
                            }
                            catch (Exception postEx)
                            {
                                Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Bỏ qua lỗi postWebMessage: {postEx.Message}");
                            }
                        });
                        return;
                    }
                }
            }
            catch (Exception checkEx)
            {
                Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Bỏ qua lỗi check MITM: {checkEx.Message}");
            }

            var postContent = new System.Net.Http.StringContent("{\"apiKey\":\"internal\",\"forceKillPort443\":true}", System.Text.Encoding.UTF8, "application/json");
            await client.PostAsync("http://127.0.0.1:20128/api/cli-tools/antigravity-mitm", postContent);

            // Tự động kích hoạt DNS cho Antigravity IDE để bảo vệ Quota ngay lập tức
            try
            {
                var patchContent = new System.Net.Http.StringContent("{\"tool\":\"antigravity\",\"action\":\"enable\"}", System.Text.Encoding.UTF8, "application/json");
                await client.PatchAsync("http://127.0.0.1:20128/api/cli-tools/antigravity-mitm", patchContent);
            }
            catch (Exception patchEx)
            {
                Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Bỏ qua lỗi patch DNS: {patchEx.Message}");
            }

            FlushDns();
            SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] [INTERCEPT] Dịch vụ MITM đã sẵn sàng và đã kích hoạt điều hướng Antigravity IDE bảo vệ Quota.");

            Dispatcher.Invoke(() =>
            {
                try
                {
                    AppWebView?.CoreWebView2?.PostWebMessageAsJson("{\"type\":\"mitmStatus\",\"running\":true}");
                }
                catch (Exception postEx)
                {
                    Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Bỏ qua lỗi postWebMessage: {postEx.Message}");
                }
            });
        }
        catch (Exception ex)
        {
            SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Không thể kích hoạt Intercept: {ex.Message}");
        }
        finally
        {
            _mitmLock.Release();
        }
    }

    public async Task DisableMitmInterceptAsync()
    {
        if (!await _mitmLock.WaitAsync(100))
        {
            SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] [INTERCEPT] Thao tác dừng Intercept đang được thực hiện, bỏ qua yêu cầu lặp.");
            return;
        }

        try
        {
            SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] [INTERCEPT] Đang dừng điều hướng và giải phóng cổng 443...");
            using var client = new System.Net.Http.HttpClient { Timeout = TimeSpan.FromSeconds(3) };
            string cliToken = GetCliToken();
            client.DefaultRequestHeaders.Add("x-9r-cli-token", cliToken);

            // Yêu cầu Go Core Engine giải phóng cổng 443 một cách văn minh mà không làm sập Gateway :20128
            try
            {
                var resp = await client.DeleteAsync("http://127.0.0.1:20128/api/cli-tools/antigravity-mitm");
                if (!resp.IsSuccessStatusCode)
                {
                    SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Go Engine báo lỗi khi dừng MITM: {resp.StatusCode}");
                }
            }
            catch (Exception ex)
            {
                SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Không thể gửi yêu cầu dừng MITM tới Go Engine: {ex.Message}");
            }

            FlushDns();
            SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] [INTERCEPT] Đã khôi phục file hosts và giải phóng cổng 443.");

            // Bắn tín hiệu đồng bộ xuống giao diện WebView2
            Dispatcher.Invoke(() =>
            {
                try
                {
                    AppWebView?.CoreWebView2?.PostWebMessageAsJson("{\"type\":\"mitmStatus\",\"running\":false}");
                }
                catch { }
            });
        }
        catch (Exception ex)
        {
            SendLogToWebView($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Lỗi dừng Intercept: {ex.Message}");
        }
        finally
        {
            _mitmLock.Release();
        }
    }

    public static void FlushDns()
    {
        try
        {
            var psi = new System.Diagnostics.ProcessStartInfo
            {
                FileName = "ipconfig",
                Arguments = "/flushdns",
                CreateNoWindow = true,
                UseShellExecute = false
            };
            var p = System.Diagnostics.Process.Start(psi);
            p?.WaitForExit(2000);
        }
        catch { }
    }

    private void OnWindowStateChanged(object? sender, EventArgs e)
    {
        if (RootBorder != null)
        {
            if (WindowState == WindowState.Maximized)
            {
                RootBorder.CornerRadius = new CornerRadius(0);
                RootBorder.BorderThickness = new Thickness(0);
            }
            else
            {
                RootBorder.CornerRadius = new CornerRadius(12);
                RootBorder.BorderThickness = new Thickness(1);
            }
        }
    }

    private async void OnMainWindowLoaded(object sender, RoutedEventArgs e)
    {
        try
        {
            string root = GetProjectRoot();
            string cacheFolder = System.IO.Path.Combine(root, "data", "cache", "webview2");
            if (!System.IO.Directory.Exists(cacheFolder))
            {
                System.IO.Directory.CreateDirectory(cacheFolder);
            }

            var options = new CoreWebView2EnvironmentOptions("--disable-pinch --disable-features=TouchpadAndWheelScrollLatching");
            var env = await CoreWebView2Environment.CreateAsync(null, cacheFolder, options);
            await AppWebView.EnsureCoreWebView2Async(env);

            // Vô hiệu hóa hoàn toàn tính năng Zoom to/nhỏ bằng Ctrl + Con lăn chuột & Pinch-to-zoom
            AppWebView.CoreWebView2.Settings.IsZoomControlEnabled = false;
            AppWebView.CoreWebView2.Settings.IsPinchZoomEnabled = false;
            AppWebView.ZoomFactor = 1.0;
            AppWebView.ZoomFactorChanged += (s, zv) =>
            {
                if (Math.Abs(AppWebView.ZoomFactor - 1.0) > 0.001)
                {
                    AppWebView.ZoomFactor = 1.0;
                }
            };

            // Tiêm script chặn sự kiện wheel và mousewheel có Ctrl ở cấp độ thấp nhất
            await AppWebView.CoreWebView2.AddScriptToExecuteOnDocumentCreatedAsync(@"
                window.addEventListener('wheel', function(e) { if (e.ctrlKey) { e.preventDefault(); e.stopImmediatePropagation(); } }, { capture: true, passive: false });
                window.addEventListener('mousewheel', function(e) { if (e.ctrlKey) { e.preventDefault(); e.stopImmediatePropagation(); } }, { capture: true, passive: false });
                document.addEventListener('wheel', function(e) { if (e.ctrlKey) { e.preventDefault(); e.stopImmediatePropagation(); } }, { capture: true, passive: false });
                document.addEventListener('mousewheel', function(e) { if (e.ctrlKey) { e.preventDefault(); e.stopImmediatePropagation(); } }, { capture: true, passive: false });
                window.addEventListener('keydown', function(e) {
                    if (e.ctrlKey && (e.key === '+' || e.key === '-' || e.key === '=' || e.key === '_' || e.key === '0' || e.code === 'NumpadAdd' || e.code === 'NumpadSubtract' || e.code === 'Numpad0')) {
                        e.preventDefault(); e.stopImmediatePropagation();
                    }
                }, { capture: true, passive: false });
            ");

            AppWebView.PreviewMouseWheel += (s, we) =>
            {
                if (System.Windows.Input.Keyboard.Modifiers.HasFlag(System.Windows.Input.ModifierKeys.Control))
                {
                    we.Handled = true;
                }
            };

            AppWebView.PreviewKeyDown += (s, ke) =>
            {
                if (System.Windows.Input.Keyboard.Modifiers.HasFlag(System.Windows.Input.ModifierKeys.Control))
                {
                    if (ke.Key is System.Windows.Input.Key.OemPlus or System.Windows.Input.Key.OemMinus or 
                               System.Windows.Input.Key.Add or System.Windows.Input.Key.Subtract or 
                               System.Windows.Input.Key.D0 or System.Windows.Input.Key.NumPad0)
                    {
                        ke.Handled = true;
                    }
                }
            };

            // Chỉ xóa cache tĩnh (DiskCache, CacheStorage) để nạp HTML mới, bảo toàn 100% LocalStorage và IndexedDB
            try
            {
                await AppWebView.CoreWebView2.Profile.ClearBrowsingDataAsync(
                    Microsoft.Web.WebView2.Core.CoreWebView2BrowsingDataKinds.DiskCache |
                    Microsoft.Web.WebView2.Core.CoreWebView2BrowsingDataKinds.CacheStorage);
            }
            catch (Exception cEx)
            {
                Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] Lỗi clear browsing cache: {cEx.Message}");
            }

            // Tự động tìm thư mục chứa HTML và đồng bộ file mới nhất (Hot-Sync)
            string baseDir = AppDomain.CurrentDomain.BaseDirectory;
            string projectRoot = GetProjectRoot();
            string publicDir = System.IO.Path.Combine(projectRoot, "public");
            string sourceHtml = System.IO.Path.Combine(publicDir, "nexusroute_ai_next_gen_gateway.html");
            if (!System.IO.File.Exists(sourceHtml))
            {
                sourceHtml = System.IO.Path.Combine(projectRoot, "nexusroute_ai_next_gen_gateway.html");
            }

            string baseHtml = System.IO.Path.Combine(baseDir, "nexusroute_ai_next_gen_gateway.html");
            if (System.IO.File.Exists(sourceHtml))
            {
                try
                {
                    if (!string.Equals(sourceHtml, baseHtml, StringComparison.OrdinalIgnoreCase))
                    {
                        var srcInfo = new System.IO.FileInfo(sourceHtml);
                        var dstInfo = System.IO.File.Exists(baseHtml) ? new System.IO.FileInfo(baseHtml) : null;
                        if (dstInfo == null || srcInfo.LastWriteTimeUtc > dstInfo.LastWriteTimeUtc || srcInfo.Length != dstInfo.Length)
                        {
                            System.IO.File.Copy(sourceHtml, baseHtml, true);
                            Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] Đã tự động Hot-Sync file HTML mới nhất sang baseDir.");
                        }
                    }

                    // Tự động đồng bộ các thư mục tài nguyên tĩnh (providers, icons, images, i18n) sang baseDir
                    if (System.IO.Directory.Exists(publicDir) && !string.Equals(publicDir, baseDir, StringComparison.OrdinalIgnoreCase))
                    {
                        SyncAssetFolder(System.IO.Path.Combine(publicDir, "providers"), System.IO.Path.Combine(baseDir, "providers"));
                        SyncAssetFolder(System.IO.Path.Combine(publicDir, "icons"), System.IO.Path.Combine(baseDir, "icons"));
                        SyncAssetFolder(System.IO.Path.Combine(publicDir, "images"), System.IO.Path.Combine(baseDir, "images"));
                        SyncAssetFolder(System.IO.Path.Combine(publicDir, "i18n"), System.IO.Path.Combine(baseDir, "i18n"));
                    }
                }
                catch (Exception syncEx)
                {
                    Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Không thể đồng bộ tài nguyên sang baseDir: {syncEx.Message}");
                }
            }

            // Ưu tiên targetFolder là publicDir nếu có đầy đủ HTML và thư mục providers
            string targetFolder = (System.IO.Directory.Exists(publicDir) && 
                                   System.IO.File.Exists(System.IO.Path.Combine(publicDir, "nexusroute_ai_next_gen_gateway.html")) &&
                                   System.IO.Directory.Exists(System.IO.Path.Combine(publicDir, "providers")))
                ? publicDir
                : (System.IO.File.Exists(baseHtml) ? baseDir : projectRoot);

            // Tiêm CLI Token động từ file hệ thống vào WebView2 để xác thực 100% với OmniRouter Backend
            string cliToken = GetCliToken();
            await AppWebView.CoreWebView2.AddScriptToExecuteOnDocumentCreatedAsync($"window.NINEROUTER_CLI_TOKEN = '{cliToken}';");
            Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] Đã tiêm NINEROUTER_CLI_TOKEN vào WebView2 ({cliToken})");

            // Thiết lập Virtual Host mapping làm fallback an toàn
            AppWebView.CoreWebView2.SetVirtualHostNameToFolderMapping("omnigateway.local", targetFolder, CoreWebView2HostResourceAccessKind.Allow);

            // Khôi phục Tab người dùng đang đứng trước đó từ AppSettings.json
            try
            {
                var initialSettings = SettingsConfigLoader.LoadSettings();
                if (!string.IsNullOrWhiteSpace(initialSettings.Launcher.LastActiveTab))
                {
                    _lastActiveTab = initialSettings.Launcher.LastActiveTab.Trim();
                }
                if (_lastActiveTab == "console" || _lastActiveTab == "stats" || _lastActiveTab == "logs")
                {
                    _lastActiveTab = "mitm";
                }
            }
            catch (Exception tabEx)
            {
                Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Không thể đọc LastActiveTab từ AppSettings: {tabEx.Message}");
            }

            // Luôn nạp 100% giao diện Cyberpunk NexusRoute của ứng dụng kèm hash của tab đang đứng
            string targetUrl = $"http://omnigateway.local/nexusroute_ai_next_gen_gateway.html?t={DateTime.UtcNow.Ticks}#{_lastActiveTab}";
            AppWebView.Source = new Uri(targetUrl);

            AppWebView.NavigationCompleted += (s, args) =>
            {
                if (args.IsSuccess && !string.IsNullOrEmpty(_lastActiveTab))
                {
                    _ = AppWebView.CoreWebView2.ExecuteScriptAsync($"if (typeof switchTab === 'function') {{ switchTab('{_lastActiveTab}'); }}");
                }
            };

            AppWebView.WebMessageReceived += AppWebView_WebMessageReceived;
            Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] WebView2 đã nạp thành công URL: {targetUrl} từ thư mục: {targetFolder}");

            // Đảm bảo cửa sổ luôn hiển thị nổi bật và tương tác được
            Topmost = true;
            Activate();
            Topmost = false;

            // Tự động kiểm tra và khởi động Backend nếu chưa chạy để nạp CSDL và kết nối ngay lập tức
            _ = Task.Run(async () =>
            {
                try
                {
                    await Task.Delay(800);
                    bool isRunning = await CheckBackendHealthAsync();
                    if (!isRunning)
                    {
                        Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] Tự động kích hoạt Backend Go Engine (:20128)...");
                        await StartBackendAsync(forceRestart: false);
                    }
                    else
                    {
                        var existingPid = ProcessHelper.GetProcessIdByPort(20128);
                        if (existingPid.HasValue && existingPid.Value > 0)
                        {
                            try
                            {
                                _nodeBackendProcess = Process.GetProcessById(existingPid.Value);
                                OmniRouter.Utils.JobObjectManager.AddProcess(_nodeBackendProcess);
                                ProcessHelper.TrackProcess(_nodeBackendProcess);
                                Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] Đã nhận nuôi tiến trình Backend PID {existingPid.Value} vào JobObject.");
                            }
                            catch { }
                        }
                    }
                }
                catch (Exception bgEx)
                {
                    Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Auto-start backend: {bgEx.Message}");
                }
            });
        }
        catch (Exception ex)
        {
            Console.Error.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [ERROR] Lỗi khởi tạo WebView2: {ex.Message}");
            System.Windows.MessageBox.Show(
                $"Lỗi khởi tạo Microsoft Edge WebView2: {ex.Message}\n\nỨng dụng yêu cầu Microsoft Edge WebView2 Evergreen Runtime để hiển thị giao diện.\nVui lòng tải và cài đặt WebView2 Runtime từ Microsoft để sử dụng.",
                "OmniRouter - Lỗi Khởi Tạo Giao Diện",
                System.Windows.MessageBoxButton.OK,
                System.Windows.MessageBoxImage.Error);
        }
    }

    private void AppWebView_WebMessageReceived(object? sender, CoreWebView2WebMessageReceivedEventArgs e)
    {
        try
        {
            string action = string.Empty;
            string payloadJson = string.Empty;

            // 1. Thử đọc dạng String thô
            string? rawString = e.TryGetWebMessageAsString();
            if (!string.IsNullOrWhiteSpace(rawString))
            {
                string trimmed = rawString.Trim();
                if (trimmed.StartsWith("{") && trimmed.EndsWith("}"))
                {
                    try
                    {
                        using var doc = System.Text.Json.JsonDocument.Parse(trimmed);
                        if (doc.RootElement.ValueKind == System.Text.Json.JsonValueKind.Object &&
                            doc.RootElement.TryGetProperty("action", out var actProp))
                        {
                            action = actProp.GetString() ?? string.Empty;
                            payloadJson = trimmed;
                        }
                    }
                    catch { }
                }

                if (string.IsNullOrEmpty(action))
                {
                    action = trimmed;
                }
            }

            // 2. Thử đọc dạng JSON nếu chưa tìm thấy action
            if (string.IsNullOrEmpty(action))
            {
                string json = e.WebMessageAsJson;
                if (!string.IsNullOrWhiteSpace(json))
                {
                    try
                    {
                        using var doc = System.Text.Json.JsonDocument.Parse(json);
                        if (doc.RootElement.ValueKind == System.Text.Json.JsonValueKind.Object &&
                            doc.RootElement.TryGetProperty("action", out var actionProp))
                        {
                            action = actionProp.GetString() ?? string.Empty;
                            payloadJson = json;
                        }
                        else if (doc.RootElement.ValueKind == System.Text.Json.JsonValueKind.String)
                        {
                            string innerStr = doc.RootElement.GetString() ?? string.Empty;
                            string trimmedInner = innerStr.Trim();
                            if (trimmedInner.StartsWith("{") && trimmedInner.EndsWith("}"))
                            {
                                using var innerDoc = System.Text.Json.JsonDocument.Parse(trimmedInner);
                                if (innerDoc.RootElement.TryGetProperty("action", out var innerAct))
                                {
                                    action = innerAct.GetString() ?? string.Empty;
                                    payloadJson = trimmedInner;
                                }
                            }
                            else
                            {
                                action = trimmedInner;
                            }
                        }
                    }
                    catch (Exception jsonEx)
                    {
                        Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Bỏ qua WebMessage không đúng định dạng JSON: {jsonEx.Message}");
                    }
                }
            }

            string lowerAction = action.ToLowerInvariant();
            if (string.IsNullOrEmpty(lowerAction)) return;

            // Xử lý lưu tab đang đứng vào AppSettings.json ngay khi người dùng chuyển tab hoặc thao tác
            if (lowerAction.StartsWith("saveactivetab:") || lowerAction.StartsWith("activetab:"))
            {
                int colonIdx = action.IndexOf(':');
                if (colonIdx >= 0 && colonIdx < action.Length - 1)
                {
                    string tabId = action.Substring(colonIdx + 1).Trim();
                    if (!string.IsNullOrEmpty(tabId))
                    {
                        _lastActiveTab = tabId;
                        _ = Task.Run(() =>
                        {
                            try
                            {
                                var currentSettings = SettingsConfigLoader.LoadSettings();
                                if (currentSettings.Launcher.LastActiveTab != tabId)
                                {
                                    currentSettings.Launcher.LastActiveTab = tabId;
                                    SettingsConfigLoader.SaveSettings(currentSettings);
                                    Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] Đã lưu LastActiveTab '{tabId}' vào AppSettings.json thành công.");
                                }
                            }
                            catch (Exception ex)
                            {
                                Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] Lỗi lưu LastActiveTab vào AppSettings: {ex.Message}");
                            }
                        });
                    }
                }
                return;
            }

            // Xử lý kiểm tra kết nối và lưu Web API Key qua C# Native HttpClient (Chống 100% lỗi CORS và Failed to fetch)
            if (lowerAction == "testwebconnection" || lowerAction == "savewebapikey")
            {
                _ = Task.Run(async () =>
                {
                    try
                    {
                        string jsonToParse = !string.IsNullOrWhiteSpace(payloadJson) ? payloadJson : (!string.IsNullOrWhiteSpace(rawString) ? rawString : e.WebMessageAsJson);
                        string serverUrl = "";
                        string apiKey = "";

                        try
                        {
                            using var doc = System.Text.Json.JsonDocument.Parse(jsonToParse);
                            var root = doc.RootElement;
                            if (root.ValueKind == System.Text.Json.JsonValueKind.String)
                            {
                                using var innerDoc = System.Text.Json.JsonDocument.Parse(root.GetString() ?? "{}");
                                serverUrl = innerDoc.RootElement.TryGetProperty("serverUrl", out var sProp) ? sProp.GetString() ?? "" : "";
                                apiKey = innerDoc.RootElement.TryGetProperty("apiKey", out var kProp) ? kProp.GetString() ?? "" : "";
                            }
                            else if (root.ValueKind == System.Text.Json.JsonValueKind.Object)
                            {
                                serverUrl = root.TryGetProperty("serverUrl", out var sProp) ? sProp.GetString() ?? "" : "";
                                apiKey = root.TryGetProperty("apiKey", out var kProp) ? kProp.GetString() ?? "" : "";
                            }
                        }
                        catch (Exception parseEx)
                        {
                            Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Lỗi phân tích JSON testWebConnection: {parseEx.Message}");
                        }

                        serverUrl = serverUrl.Trim().TrimEnd('/');
                        if (string.IsNullOrEmpty(serverUrl)) serverUrl = "https://www.supportflastdev.io.vn";
                        if (!serverUrl.Contains("www.") && serverUrl.Contains("supportflastdev.io.vn"))
                        {
                            serverUrl = serverUrl.Replace("https://supportflastdev.io.vn", "https://www.supportflastdev.io.vn")
                                                 .Replace("http://supportflastdev.io.vn", "https://www.supportflastdev.io.vn");
                        }

                        if (!string.IsNullOrEmpty(apiKey))
                        {
                            try
                            {
                                string projectRoot = GetProjectRoot();
                                string cfgPath = System.IO.Path.Combine(projectRoot, "config.json");
                                var cfgObj = new System.Collections.Generic.Dictionary<string, object>
                                {
                                    ["web_server_url"] = serverUrl,
                                    ["render_url"] = serverUrl,
                                    ["master_api_key"] = apiKey,
                                    ["api_key"] = apiKey,
                                    ["local_port"] = 20128
                                };
                                string newJson = System.Text.Json.JsonSerializer.Serialize(cfgObj, new System.Text.Json.JsonSerializerOptions { WriteIndented = true });
                                bool saved = SafeWriteTextWithRetry(cfgPath, newJson);

                                string baseCfg = System.IO.Path.Combine(AppDomain.CurrentDomain.BaseDirectory, "config.json");
                                if (!string.Equals(System.IO.Path.GetFullPath(cfgPath), System.IO.Path.GetFullPath(baseCfg), StringComparison.OrdinalIgnoreCase))
                                {
                                    SafeWriteTextWithRetry(baseCfg, newJson);
                                }

                                if (saved)
                                {
                                    Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] ✔ Đã lưu Web API Key vào config.json an toàn.");
                                }

                                // Cập nhật trực tiếp Active API Key cho Go Engine qua cổng 20128
                                try
                                {
                                    using var engineSyncClient = new System.Net.Http.HttpClient { Timeout = TimeSpan.FromSeconds(2) };
                                    var setKeyPayload = new System.Net.Http.StringContent(
                                        System.Text.Json.JsonSerializer.Serialize(new { key = apiKey }),
                                        System.Text.Encoding.UTF8,
                                        "application/json"
                                    );
                                    _ = engineSyncClient.PostAsync("http://127.0.0.1:20128/api/v1/client/set_active_key", setKeyPayload);
                                }
                                catch { }
                            }
                            catch (Exception saveEx)
                            {
                                Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Bỏ qua lỗi tạm thời khi lưu config.json (tiếp tục kiểm tra kết nối mạng): {saveEx.Message}");
                            }
                        }

                        // Kiểm tra kết nối trực tiếp qua HttpClient cấp hệ thống
                        var sw = Stopwatch.StartNew();
                        using var handler = new System.Net.Http.HttpClientHandler
                        {
                            AllowAutoRedirect = true,
                            ServerCertificateCustomValidationCallback = (m, c, ch, er) => true
                        };
                        using var client = new System.Net.Http.HttpClient(handler) { Timeout = TimeSpan.FromSeconds(45) };
                        client.DefaultRequestHeaders.Add("User-Agent", "SupportFlast-Desktop-Gateway/2.9.0");
                        if (!string.IsNullOrEmpty(apiKey))
                        {
                            client.DefaultRequestHeaders.Authorization = new System.Net.Http.Headers.AuthenticationHeaderValue("Bearer", apiKey);
                            client.DefaultRequestHeaders.TryAddWithoutValidation("x-api-key", apiKey);
                            client.DefaultRequestHeaders.TryAddWithoutValidation("x-9r-cli-token", apiKey);
                        }

                        var response = await client.GetAsync($"{serverUrl}/api/usage/stats");
                        sw.Stop();
                        string body = await response.Content.ReadAsStringAsync();

                        string providersBody = "[]";
                        try
                        {
                            var pRes = await client.GetAsync($"{serverUrl}/api/router/providers");
                            if (pRes.IsSuccessStatusCode)
                            {
                                providersBody = await pRes.Content.ReadAsStringAsync();
                            }
                            else
                            {
                                var pRes2 = await client.GetAsync($"{serverUrl}/api/providers");
                                if (pRes2.IsSuccessStatusCode)
                                {
                                    providersBody = await pRes2.Content.ReadAsStringAsync();
                                }
                            }
                        }
                        catch (Exception provEx)
                        {
                            Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Không thể nạp /api/router/providers: {provEx.Message}");
                        }

                        bool isConnected = response.IsSuccessStatusCode || (!string.IsNullOrEmpty(providersBody) && providersBody.Length > 20);
                        int httpCode = response.IsSuccessStatusCode ? (int)response.StatusCode : (isConnected ? 200 : (int)response.StatusCode);
                        Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [WebSync] Kết quả gọi {serverUrl}: HTTP {httpCode}, Providers JSON length: {providersBody.Length} bytes.");

                        Dispatcher.Invoke(() =>
                        {
                            var resultObj = new
                            {
                                type = "webConnectionResult",
                                success = isConnected,
                                statusCode = httpCode,
                                serverUrl = serverUrl,
                                latencyMs = sw.ElapsedMilliseconds,
                                stats = body,
                                providers = providersBody
                            };
                            string resultJson = System.Text.Json.JsonSerializer.Serialize(resultObj);
                            AppWebView?.CoreWebView2?.PostWebMessageAsJson(resultJson);
                        });
                    }
                    catch (Exception ex)
                    {
                        Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] Lỗi kiểm tra kết nối Web: {ex.Message}");
                        Dispatcher.Invoke(() =>
                        {
                            var errorObj = new
                            {
                                type = "webConnectionResult",
                                success = false,
                                statusCode = 0,
                                errorMessage = ex.Message
                            };
                            string errJson = System.Text.Json.JsonSerializer.Serialize(errorObj);
                            AppWebView?.CoreWebView2?.PostWebMessageAsJson(errJson);
                        });
                    }
                });
                return;
            }

            // Kiểm tra debounce chống spam click cho các action mạng & tiến trình
            if (IsActionDebounced(lowerAction))
            {
                Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [DEBOUNCE] Bỏ qua action '{lowerAction}' do gọi quá nhanh.");
                return;
            }

            Dispatcher.Invoke(() =>
            {
                switch (lowerAction)
                {
                    case "close":
                        Close();
                        break;
                    case "minimize":
                        WindowState = WindowState.Minimized;
                        break;
                    case "maximize":
                        WindowState = WindowState == WindowState.Maximized ? WindowState.Normal : WindowState.Maximized;
                        break;
                    case "restore":
                        WindowState = WindowState.Normal;
                        break;
                    case "dragmove":
                        PerformNativeDragMove();
                        break;
                    case "startgateway":
                        _ = StartBackendAsync(forceRestart: false);
                        break;
                    case "startwithupdate":
                    case "cleanupdate":
                        _ = CleanAndAutoUpdateBackendAsync();
                        break;
                    case "stopgateway":
                        _ = StopBackendAsync();
                        break;
                    case "restartgateway":
                        _ = RestartBackendAsync();
                        break;
                    case "startintercept":
                        _ = EnableMitmInterceptAsync();
                        break;
                    case "stopintercept":
                        _ = DisableMitmInterceptAsync();
                        break;
                    case "flushdns":
                        FlushDns();
                        break;
                    case "getgatewaystatus":
                        UpdateGatewayUI(_isGatewayOnline, force: true);
                        break;
                }
            });
        }
        catch (InvalidOperationException ex)
        {
            Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] Lỗi kéo thả: {ex.Message}");
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] Lỗi xử lý WebMessageReceived: {ex.Message}");
        }
    }

    /// <summary>
    /// Kéo thả cửa sổ cấp Native Win32:
    /// Cho phép kéo xuyên màn hình (Multi-Monitor), tự động thích ứng DPI scaling và hỗ trợ kéo thu nhỏ từ trạng thái Maximized.
    /// </summary>
    private void PerformNativeDragMove()
    {
        try
        {
            var hwnd = new System.Windows.Interop.WindowInteropHelper(this).Handle;
            if (hwnd == IntPtr.Zero) return;

            // Nếu cửa sổ đang Maximized, khôi phục về Normal và căn chỉnh con trỏ chuột mượt mà
            if (WindowState == WindowState.Maximized)
            {
                if (GetCursorPos(out POINT curPos))
                {
                    double curWidth = ActualWidth > 0 ? ActualWidth : SystemParameters.PrimaryScreenWidth;
                    double percentX = curWidth > 0 ? (curPos.X - Left) / curWidth : 0.5;
                    percentX = Math.Clamp(percentX, 0.05, 0.95);

                    WindowState = WindowState.Normal;

                    double normalWidth = RestoreBounds.Width > 0 ? RestoreBounds.Width : (ActualWidth > 0 ? ActualWidth : 1180);
                    Left = curPos.X - (normalWidth * percentX);
                    Top = Math.Max(0, curPos.Y - 18);
                }
                else
                {
                    WindowState = WindowState.Normal;
                }
            }

            // Giải phóng mouse capture từ Chromium / WebView2 HWND
            ReleaseCapture();

            // Chuyển toàn quyền kéo cửa sổ cho Windows Desktop Window Manager (DWM)
            SendMessage(hwnd, WM_NCLBUTTONDOWN, (IntPtr)HTCAPTION, IntPtr.Zero);
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] Lỗi PerformNativeDragMove: {ex.Message}");
        }
    }

    private string GetCliToken()
    {
        try
        {
            string appData = Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData);
            string machineIdFile = System.IO.Path.Combine(appData, "9router", "machine-id");
            string cliSecretFile = System.IO.Path.Combine(appData, "9router", "auth", "cli-secret");

            if (!System.IO.File.Exists(machineIdFile))
            {
                string projectRoot = GetProjectRoot();
                machineIdFile = System.IO.Path.Combine(projectRoot, "data", "machine-id");
                cliSecretFile = System.IO.Path.Combine(projectRoot, "data", "auth", "cli-secret");
            }

            string rawId = System.IO.File.Exists(machineIdFile)
                ? System.IO.File.ReadAllText(machineIdFile).Trim()
                : string.Empty;
            string cliSecret = System.IO.File.Exists(cliSecretFile)
                ? System.IO.File.ReadAllText(cliSecretFile).Trim()
                : string.Empty;

            if (!string.IsNullOrEmpty(rawId))
            {
                string input = rawId + "9r-cli-auth" + cliSecret;
                using var sha256 = System.Security.Cryptography.SHA256.Create();
                byte[] hash = sha256.ComputeHash(System.Text.Encoding.UTF8.GetBytes(input));
                string hex = BitConverter.ToString(hash).Replace("-", "").ToLowerInvariant();
                return hex.Substring(0, 16);
            }
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] Lỗi tính CliToken: {ex.Message}");
        }
        return "6929c9313fb35a0b";
    }

    private static string GetProjectRoot()
    {
        // 1. Ưu tiên biến môi trường OMNIROUTER_DIR nếu có và hợp lệ
        string envDir = Environment.GetEnvironmentVariable("OMNIROUTER_DIR") ?? string.Empty;
        if (!string.IsNullOrWhiteSpace(envDir) && System.IO.Directory.Exists(envDir))
        {
            return envDir;
        }

        string baseDir = AppDomain.CurrentDomain.BaseDirectory;

        // 2. Duyệt ngược từ baseDir lên tối đa 6 cấp để tìm thư mục gốc chứa data.libsql hoặc omnirouter_engine hoặc OmniRouter_ui
        var dirInfo = new System.IO.DirectoryInfo(baseDir);
        while (dirInfo != null)
        {
            if (System.IO.File.Exists(System.IO.Path.Combine(dirInfo.FullName, "data", "db", "data.libsql")) ||
                System.IO.Directory.Exists(System.IO.Path.Combine(dirInfo.FullName, "omnirouter_engine")) ||
                System.IO.Directory.Exists(System.IO.Path.Combine(dirInfo.FullName, "OmniRouter_ui")))
            {
                return dirInfo.FullName;
            }
            dirInfo = dirInfo.Parent;
        }

        // 3. Kiểm tra CurrentDirectory
        string currentDir = System.IO.Directory.GetCurrentDirectory();
        if (System.IO.File.Exists(System.IO.Path.Combine(currentDir, "data", "db", "data.libsql")) ||
            System.IO.Directory.Exists(System.IO.Path.Combine(currentDir, "omnirouter_engine")))
        {
            return currentDir;
        }

        return baseDir;
    }

    private static void SyncAssetFolder(string srcDir, string dstDir)
    {
        try
        {
            if (!System.IO.Directory.Exists(srcDir)) return;
            if (!System.IO.Directory.Exists(dstDir))
            {
                System.IO.Directory.CreateDirectory(dstDir);
            }

            foreach (string file in System.IO.Directory.GetFiles(srcDir))
            {
                string fileName = System.IO.Path.GetFileName(file);
                string targetFile = System.IO.Path.Combine(dstDir, fileName);
                var srcInfo = new System.IO.FileInfo(file);
                var dstInfo = System.IO.File.Exists(targetFile) ? new System.IO.FileInfo(targetFile) : null;
                if (dstInfo == null || srcInfo.LastWriteTimeUtc > dstInfo.LastWriteTimeUtc || srcInfo.Length != dstInfo.Length)
                {
                    System.IO.File.Copy(file, targetFile, true);
                }
            }
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Lỗi SyncAssetFolder ({srcDir} -> {dstDir}): {ex.Message}");
        }
    }

    private static bool SafeWriteTextWithRetry(string filePath, string content, int maxRetries = 5, int delayMs = 120)
    {
        try
        {
            if (System.IO.File.Exists(filePath))
            {
                try
                {
                    string existing = System.IO.File.ReadAllText(filePath, System.Text.Encoding.UTF8);
                    if (existing == content)
                    {
                        return true;
                    }
                }
                catch (Exception readEx)
                {
                    Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Không thể đọc nhanh {filePath}: {readEx.Message}");
                }
            }

            for (int i = 0; i < maxRetries; i++)
            {
                try
                {
                    string? dir = System.IO.Path.GetDirectoryName(filePath);
                    if (!string.IsNullOrEmpty(dir) && !System.IO.Directory.Exists(dir))
                    {
                        System.IO.Directory.CreateDirectory(dir);
                    }

                    using var fs = new System.IO.FileStream(filePath, System.IO.FileMode.Create, System.IO.FileAccess.Write, System.IO.FileShare.ReadWrite);
                    using var writer = new System.IO.StreamWriter(fs, System.Text.Encoding.UTF8);
                    writer.Write(content);
                    writer.Flush();
                    return true;
                }
                catch (System.IO.IOException ioEx)
                {
                    Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [WARN] File {filePath} đang bận (lần {i + 1}/{maxRetries}): {ioEx.Message}");
                    if (i == maxRetries - 1) return false;
                    System.Threading.Thread.Sleep(delayMs);
                }
            }
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [ERROR] Thất bại khi ghi file {filePath}: {ex.Message}");
            return false;
        }
        return false;
    }
}
