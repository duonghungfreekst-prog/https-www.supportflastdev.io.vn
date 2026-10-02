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
        private static readonly HttpClient client = new HttpClient { BaseAddress = new Uri("http://localhost:8080") };
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
                Content = "Chào mừng bạn đến với Cổng Hỗ Trợ Đăng Tải Ứng Dụng (SupportFlast). Hãy mô tả vấn đề của bạn.",
                Color = Brushes.White
            });

            _ = LoadAgentsAsync();
            _ = CheckServerHealthAsync();
        }

        private async Task LoadAgentsAsync()
        {
            try
            {
                // Quy tắc 7.2: Sử dụng Cache cho API kết quả từ Go Engine (TTL: 30 giây)
                if (!_cache.TryGetValue("agents_list", out string? cachedData))
                {
                    var response = await client.GetAsync("/api/v1/agents");
                    if (response.IsSuccessStatusCode)
                    {
                        cachedData = await response.Content.ReadAsStringAsync();
                        
                        // Set cache với Size = 1 và TTL 30 giây
                        var cacheEntryOptions = new MemoryCacheEntryOptions()
                            .SetSize(1)
                            .SetAbsoluteExpiration(TimeSpan.FromSeconds(30));
                            
                        _cache.Set("agents_list", cachedData, cacheEntryOptions);
                    }
                }

                if (!string.IsNullOrEmpty(cachedData))
                {
                    var apiResponse = JsonConvert.DeserializeObject<AgentsResponse>(cachedData);
                    if (apiResponse != null && apiResponse.Agents != null)
                    {
                        Dispatcher.Invoke(() =>
                        {
                            Agents.Clear();
                            foreach (var agent in apiResponse.Agents)
                            {
                                Agents.Add(new AgentViewModel
                                {
                                    Name = agent.Name,
                                    RoleDescription = agent.RoleDescription,
                                    ColorBrush = (SolidColorBrush)new BrushConverter().ConvertFromString(agent.Color ?? "#FFFFFF")
                                });
                            }
                        });
                    }
                }
            }
            catch (Exception ex)
            {
                Dispatcher.Invoke(() =>
                {
                    TxtServerStatus.Text = "Lỗi kết nối Go Engine";
                    TxtServerStatus.Foreground = Brushes.Red;
                });
            }
        }

        private async Task CheckServerHealthAsync()
        {
            try
            {
                var response = await client.GetAsync("/api/v1/health");
                if (response.IsSuccessStatusCode)
                {
                    Dispatcher.Invoke(() =>
                    {
                        TxtServerStatus.Text = "Go Engine Online";
                        TxtServerStatus.Foreground = Brushes.Lime;
                    });
                }
            }
            catch
            {
                Dispatcher.Invoke(() =>
                {
                    TxtServerStatus.Text = "Offline";
                    TxtServerStatus.Foreground = Brushes.Red;
                });
            }
        }

        private async void BtnRefresh_Click(object sender, RoutedEventArgs e)
        {
            TxtServerStatus.Text = "Đang tải...";
            TxtServerStatus.Foreground = Brushes.Yellow;
            // Xóa cache khi người dùng refresh thủ công
            _cache.Remove("agents_list");
            await LoadAgentsAsync();
            await CheckServerHealthAsync();
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
                
                // Gửi request qua Go Engine
                var response = await client.PostAsync("/api/v1/request", content);
                var responseString = await response.Content.ReadAsStringAsync();

                if (response.IsSuccessStatusCode)
                {
                    var resData = JsonConvert.DeserializeObject<RequestResponse>(responseString);
                    if (resData != null)
                    {
                        Messages.Add(new MessageViewModel
                        {
                            Role = resData.AgentName,
                            Content = resData.Response,
                            Color = Brushes.Yellow
                        });
                    }
                }
                else
                {
                    Messages.Add(new MessageViewModel { Role = "Lỗi", Content = "Gặp lỗi xử lý từ server: " + responseString, Color = Brushes.Red });
                }
            }
            catch (Exception ex)
            {
                Messages.Add(new MessageViewModel { Role = "Lỗi", Content = "Lỗi mạng: " + ex.Message, Color = Brushes.Red });
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
        [JsonProperty("color")]
        public string? Color { get; set; }
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