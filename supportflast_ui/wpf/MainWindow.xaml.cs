using System;
using System.Collections.ObjectModel;
using System.Net.Http;
using System.Text;
using System.Threading.Tasks;
using System.Windows;
using System.Windows.Input;
using System.Windows.Media;
using Microsoft.Extensions.Caching.Memory;
using Newtonsoft.Json;

namespace SupportFlast
{
    public partial class MainWindow : Window
    {
        private const string CloudBaseUrl = "https://www.supportflastdev.io.vn";
        private const string LocalBaseUrl = "http://localhost:8080";
        private static readonly object _clientLock = new object();
        private static string _currentBaseUrl = CloudBaseUrl;
        private static HttpClient client = CreateClient(CloudBaseUrl);
        private readonly IMemoryCache _cache;
        
        public ObservableCollection<AgentViewModel> Agents { get; set; } = new ObservableCollection<AgentViewModel>();
        public ObservableCollection<MessageViewModel> Messages { get; set; } = new ObservableCollection<MessageViewModel>();

        public MainWindow()
        {
            InitializeComponent();
            
            // Khởi tạo MemoryCache với SizeLimit = 1000 theo quy tắc 7.2
            _cache = new MemoryCache(new MemoryCacheOptions
            {
                SizeLimit = 1000
            });
            
            ListAgents.ItemsSource = Agents;
            ListMessages.ItemsSource = Messages;
            
            Messages.Add(new MessageViewModel
            {
                Role = "System",
                Content = "Chào mừng bạn đến với Cổng Hỗ Trợ Đăng Tải Ứng Dụng (SupportFlast). Đang kết nối máy chủ Cloud...",
                Color = Brushes.White
            });

            _ = StartupAsync();
        }

        private async Task StartupAsync()
        {
            await CheckConnectionAndFallbackAsync();
            await LoadAgentsAsync();
        }

        private static HttpClient CreateClient(string baseUrl)
        {
            var httpClient = new HttpClient
            {
                BaseAddress = new Uri(baseUrl),
                Timeout = TimeSpan.FromSeconds(8)
            };
            httpClient.DefaultRequestHeaders.Add("User-Agent", "SupportFlast-Desktop/1.0");
            return httpClient;
        }

        private static void SwitchEndpoint(string newBaseUrl)
        {
            lock (_clientLock)
            {
                if (_currentBaseUrl == newBaseUrl) return;
                _currentBaseUrl = newBaseUrl;
                var oldClient = client;
                client = CreateClient(newBaseUrl);
                try
                {
                    oldClient?.Dispose();
                }
                catch (Exception ex)
                {
                    System.Diagnostics.Debug.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Dispose HttpClient error: {ex.Message}");
                }
            }
        }

        private static HttpClient GetClient()
        {
            lock (_clientLock)
            {
                return client;
            }
        }

        public async Task<bool> CheckConnectionAndFallbackAsync()
        {
            Dispatcher.Invoke(() =>
            {
                TxtServerStatus.Text = "Kiểm tra Cloud...";
                TxtServerStatus.Foreground = Brushes.Yellow;
            });

            System.Diagnostics.Debug.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [HEALTH] Kiểm tra kết nối Cloud: {CloudBaseUrl}");
            bool cloudHealthy = await PingHealthAsync(CloudBaseUrl);

            if (cloudHealthy)
            {
                SwitchEndpoint(CloudBaseUrl);
                Dispatcher.Invoke(() =>
                {
                    TxtServerStatus.Text = "Cloud Online (supportflastdev.io.vn)";
                    TxtServerStatus.Foreground = Brushes.Lime;
                });
                System.Diagnostics.Debug.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [INFO] Kết nối Cloud thành công: {CloudBaseUrl}");
                return true;
            }

            // Cloud không khả dụng -> Kích hoạt Fallback sang Local Engine
            System.Diagnostics.Debug.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Cloud không phản hồi. Bắt đầu fallback sang Local: {LocalBaseUrl}");
            Dispatcher.Invoke(() =>
            {
                TxtServerStatus.Text = "Cloud offline. Thử Local...";
                TxtServerStatus.Foreground = Brushes.Orange;
            });

            bool localHealthy = await PingHealthAsync(LocalBaseUrl);
            if (localHealthy)
            {
                SwitchEndpoint(LocalBaseUrl);
                Dispatcher.Invoke(() =>
                {
                    TxtServerStatus.Text = "Local Engine (Fallback: localhost:8080)";
                    TxtServerStatus.Foreground = Brushes.Orange;
                    Messages.Add(new MessageViewModel
                    {
                        Role = "System",
                        Content = "⚠️ Máy chủ Cloud (supportflastdev.io.vn) gián đoạn. Đã tự động kích hoạt chế độ Fallback sang Local Engine (http://localhost:8080).",
                        Color = Brushes.Orange
                    });
                });
                System.Diagnostics.Debug.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [INFO] Chuyển sang Local Engine thành công: {LocalBaseUrl}");
                return true;
            }

            // Cả Cloud và Local đều thất bại
            Dispatcher.Invoke(() =>
            {
                TxtServerStatus.Text = "Offline (Cloud & Local failed)";
                TxtServerStatus.Foreground = Brushes.Red;
                Messages.Add(new MessageViewModel
                {
                    Role = "System",
                    Content = "❌ Mất kết nối: Không thể kết nối tới Cloud (supportflastdev.io.vn) lẫn Local Engine (http://localhost:8080).",
                    Color = Brushes.Red
                });
            });
            System.Diagnostics.Debug.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [ERROR] Cả Cloud và Local đều không phản hồi.");
            return false;
        }

        private async Task<bool> PingHealthAsync(string baseUrl)
        {
            try
            {
                using var cts = new System.Threading.CancellationTokenSource(TimeSpan.FromSeconds(5));
                using var pingClient = new HttpClient { Timeout = TimeSpan.FromSeconds(5) };
                var healthUri = new Uri(new Uri(baseUrl), "/api/v1/health");
                var response = await pingClient.GetAsync(healthUri, cts.Token);
                return response.IsSuccessStatusCode;
            }
            catch (Exception ex)
            {
                System.Diagnostics.Debug.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [DEBUG] Ping {baseUrl} thất bại: {ex.Message}");
                return false;
            }
        }

        public async Task CheckServerHealthAsync()
        {
            await CheckConnectionAndFallbackAsync();
        }

        private async Task LoadAgentsAsync()
        {
            try
            {
                string cacheKey = $"agents_list_{_currentBaseUrl}";
                if (!_cache.TryGetValue(cacheKey, out string? cachedData))
                {
                    var httpClient = GetClient();
                    var response = await httpClient.GetAsync("/api/v1/agents");
                    if (response.IsSuccessStatusCode)
                    {
                        cachedData = await response.Content.ReadAsStringAsync();
                        
                        // Set cache với Size = 1 và TTL 30 giây (Quy tắc 7.2)
                        var cacheEntryOptions = new MemoryCacheEntryOptions()
                            .SetSize(1)
                            .SetAbsoluteExpiration(TimeSpan.FromSeconds(30));
                            
                        _cache.Set(cacheKey, cachedData, cacheEntryOptions);
                    }
                }

                if (!string.IsNullOrEmpty(cachedData))
                {
                    var apiResponse = JsonConvert.DeserializeObject<AgentsResponse>(cachedData);
                    if (apiResponse?.Agents != null)
                    {
                        Dispatcher.Invoke(() =>
                        {
                            Agents.Clear();
                            foreach (var agent in apiResponse.Agents)
                            {
                                Agents.Add(new AgentViewModel
                                {
                                    Name = agent.Name ?? "Unknown Agent",
                                    RoleDescription = agent.DisplayDescription,
                                    ColorBrush = ParseBrush(agent.Color)
                                });
                            }
                        });
                    }
                }
            }
            catch (Exception ex)
            {
                System.Diagnostics.Debug.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [ERROR] LoadAgentsAsync lỗi: {ex.Message}");
                Dispatcher.Invoke(() =>
                {
                    TxtServerStatus.Text = $"Lỗi tải Subagents ({(_currentBaseUrl == CloudBaseUrl ? "Cloud" : "Local")})";
                    TxtServerStatus.Foreground = Brushes.Red;
                });
            }
        }

        private static SolidColorBrush ParseBrush(string? hex)
        {
            try
            {
                if (!string.IsNullOrWhiteSpace(hex))
                {
                    if (new BrushConverter().ConvertFromString(hex) is SolidColorBrush brush)
                    {
                        return brush;
                    }
                }
            }
            catch (Exception ex)
            {
                System.Diagnostics.Debug.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Không thể parse mã màu '{hex}': {ex.Message}");
            }
            return Brushes.White;
        }

        private async void BtnRefresh_Click(object sender, RoutedEventArgs e)
        {
            TxtServerStatus.Text = "Đang làm mới...";
            TxtServerStatus.Foreground = Brushes.Yellow;
            // Xóa cache khi người dùng refresh thủ công
            _cache.Remove($"agents_list_{CloudBaseUrl}");
            _cache.Remove($"agents_list_{LocalBaseUrl}");
            _cache.Remove("agents_list");
            
            await CheckConnectionAndFallbackAsync();
            await LoadAgentsAsync();
        }

        private async void BtnSend_Click(object sender, RoutedEventArgs e)
        {
            await SendRequestAsync();
        }

        private async void TxtInput_KeyDown(object sender, KeyEventArgs e)
        {
            if (e.Key == Key.Enter)
            {
                await SendRequestAsync();
            }
        }

        private async Task SendRequestAsync()
        {
            string text = TxtInput.Text.Trim();
            if (string.IsNullOrEmpty(text)) return;

            TxtInput.Text = "";
            Messages.Add(new MessageViewModel
            {
                Role = "You",
                Content = text,
                Color = Brushes.Cyan
            });

            try
            {
                var payload = new { query = text };
                var content = new StringContent(JsonConvert.SerializeObject(payload), Encoding.UTF8, "application/json");

                HttpResponseMessage? response = null;
                var currentClient = GetClient();

                try
                {
                    response = await currentClient.PostAsync("/api/v1/request", content);
                }
                catch (Exception ex)
                {
                    System.Diagnostics.Debug.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [WARN] Gửi request tới {_currentBaseUrl} thất bại: {ex.Message}");
                }

                // Nếu đang dùng Cloud mà thất bại, tự động kích hoạt fallback sang Local Engine
                if ((response == null || !response.IsSuccessStatusCode) && _currentBaseUrl == CloudBaseUrl)
                {
                    System.Diagnostics.Debug.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [INFO] Cloud không phản hồi tốt. Kiểm tra fallback sang Local Engine: {LocalBaseUrl}...");
                    bool localHealthy = await PingHealthAsync(LocalBaseUrl);
                    if (localHealthy)
                    {
                        SwitchEndpoint(LocalBaseUrl);
                        Dispatcher.Invoke(() =>
                        {
                            TxtServerStatus.Text = "Local Engine (Fallback: localhost:8080)";
                            TxtServerStatus.Foreground = Brushes.Orange;
                            Messages.Add(new MessageViewModel
                            {
                                Role = "System",
                                Content = "⚠️ Kết nối Cloud gián đoạn, tự động chuyển sang Local Engine để tiếp tục xử lý yêu cầu.",
                                Color = Brushes.Orange
                            });
                        });

                        var retryContent = new StringContent(JsonConvert.SerializeObject(payload), Encoding.UTF8, "application/json");
                        try
                        {
                            response = await GetClient().PostAsync("/api/v1/request", retryContent);
                        }
                        catch (Exception ex)
                        {
                            System.Diagnostics.Debug.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [ERROR] Thử lại trên Local thất bại: {ex.Message}");
                        }
                    }
                }

                if (response != null && response.IsSuccessStatusCode)
                {
                    var responseString = await response.Content.ReadAsStringAsync();
                    var resData = JsonConvert.DeserializeObject<RequestResponse>(responseString);
                    if (resData != null)
                    {
                        Messages.Add(new MessageViewModel
                        {
                            Role = resData.AgentName ?? "AI Assistant",
                            Content = resData.Response ?? "",
                            Color = Brushes.Yellow
                        });
                    }
                }
                else
                {
                    string errorDetail = response != null ? await response.Content.ReadAsStringAsync() : "Không nhận được phản hồi từ server.";
                    Messages.Add(new MessageViewModel
                    {
                        Role = "Lỗi",
                        Content = $"Gặp lỗi xử lý từ server ({(_currentBaseUrl == CloudBaseUrl ? "Cloud" : "Local")}): {errorDetail}",
                        Color = Brushes.Red
                    });
                }
            }
            catch (Exception ex)
            {
                System.Diagnostics.Debug.WriteLine($"[UI {DateTime.Now:HH:mm:ss}] [ERROR] SendRequestAsync ngoại lệ: {ex.Message}");
                Messages.Add(new MessageViewModel
                {
                    Role = "Lỗi",
                    Content = "Lỗi mạng: " + ex.Message,
                    Color = Brushes.Red
                });
            }
        }
    }

    public class AgentViewModel
    {
        public string Name { get; set; } = "";
        public string RoleDescription { get; set; } = "";
        public SolidColorBrush ColorBrush { get; set; } = Brushes.White;
    }

    public class MessageViewModel
    {
        public string Role { get; set; } = "";
        public string Content { get; set; } = "";
        public Brush Color { get; set; } = Brushes.White;
    }

    public class AgentsResponse
    {
        [JsonProperty("agents")]
        public AgentInfo[]? Agents { get; set; }
    }

    public class AgentInfo
    {
        [JsonProperty("id")]
        public string? Id { get; set; }

        [JsonProperty("name")]
        public string? Name { get; set; }

        [JsonProperty("role_description")]
        public string? RoleDescription { get; set; }

        [JsonProperty("description")]
        public string? Description { get; set; }

        [JsonProperty("color")]
        public string? Color { get; set; }

        [JsonIgnore]
        public string DisplayDescription => !string.IsNullOrEmpty(RoleDescription) 
            ? RoleDescription 
            : (!string.IsNullOrEmpty(Description) ? Description : "");
    }

    public class RequestResponse
    {
        [JsonProperty("agent_id")]
        public string? AgentId { get; set; }

        [JsonProperty("agent_name")]
        public string? AgentName { get; set; }

        [JsonProperty("response")]
        public string? Response { get; set; }
    }
}