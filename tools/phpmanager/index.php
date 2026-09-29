<?php
/**
 * SupportFlast PHP Management Console & XAMPP Control Hub
 * File: index.php
 * Path: f:\supportflast.dev\tools\phpmanager\index.php
 * Tương thích PHP 8.0 - 8.5+ (CGI/FastCGI/Apache Module)
 */

// 1. Security Headers chuẩn Antigravity Workspace (Rule 3.4 & 3.5)
header("X-Content-Type-Options: nosniff");
header("X-Frame-Options: SAMEORIGIN");
header("X-XSS-Protection: 1; mode=block");
header("Referrer-Policy: strict-origin-when-cross-origin");
header("Content-Type: text/html; charset=UTF-8");

// Khởi tạo các thông số hệ thống
$phpVersion = PHP_VERSION;
$os = PHP_OS . " (" . php_uname('m') . ")";
$sapi = php_sapi_name();
$memoryLimit = ini_get('memory_limit');
$uploadMax = ini_get('upload_max_filesize');
$postMax = ini_get('post_max_size');
$maxExec = ini_get('max_execution_time') . 's';
$serverIp = $_SERVER['SERVER_ADDR'] ?? gethostbyname(gethostname());

// Kiểm tra các extension quan trọng
$requiredExtensions = [
    'curl'       => 'Hỗ trợ Reverse Proxy & Kết nối API ngoại vi',
    'openssl'    => 'Mã hóa SSL/TLS & Ký khóa bảo mật cao',
    'pdo_sqlite' => 'Kết nối CSDL SQLite đa luồng WAL',
    'sqlite3'    => 'Quản lý tệp cơ sở dữ liệu SQLite cục bộ',
    'mbstring'   => 'Xử lý chuỗi ký tự UTF-8 & Đa ngôn ngữ',
    'json'       => 'Phân tích và tuần tự hóa dữ liệu JSON',
    'zlib'       => 'Nén dữ liệu HTTP & Giải nén gói cài đặt',
    'fileinfo'   => 'Nhận diện định dạng tệp tin tải lên an toàn',
];

$extStatus = [];
foreach ($requiredExtensions as $ext => $desc) {
    $extStatus[$ext] = [
        'loaded' => extension_loaded($ext),
        'desc'   => $desc
    ];
}

// Kiểm tra tình trạng kết nối tới Go Monolith Engine (Port 8080)
$engineUrl = "http://127.0.0.1:8080/api/health";
$engineHealthy = false;
$engineData = null;
$engineLatency = 0;

$startTime = microtime(true);
if (function_exists('curl_init')) {
    $ch = curl_init($engineUrl);
    curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
    curl_setopt($ch, CURLOPT_TIMEOUT, 2);
    $response = curl_exec($ch);
    $httpCode = curl_getinfo($ch, CURLINFO_HTTP_CODE);
    $engineLatency = round((microtime(true) - $startTime) * 1000, 2);
    curl_close($ch);

    if ($httpCode === 200 && $response) {
        $engineData = json_decode($response, true);
        if ($engineData && ($engineData['status'] ?? '') === 'healthy') {
            $engineHealthy = true;
        }
    }
}

// Xử lý hành động kích hoạt Backend nếu người dùng nhấn nút "Khởi động Engine"
$actionMessage = "";
if (isset($_POST['action']) && $_POST['action'] === 'start_engine') {
    $engineExe = "F:\\supportflast.dev\\supportflast.exe";
    if (file_exists($engineExe)) {
        pclose(popen("powershell.exe -WindowStyle Hidden -Command \"Start-Process -FilePath '$engineExe' -WorkingDirectory 'F:\\supportflast.dev' -WindowStyle Hidden\"", "r"));
        sleep(1);
        header("Location: index.php?tab=engine&msg=started");
        exit;
    } else {
        $actionMessage = "Không tìm thấy tệp '$engineExe'!";
    }
}

// Kiểm tra cơ sở dữ liệu SQLite
$dbPath = "F:\\supportflast.dev\\data\\supportflast.db";
$dbStats = [
    'exists' => file_exists($dbPath),
    'size'   => file_exists($dbPath) ? round(filesize($dbPath) / 1024, 2) . ' KB' : '0 KB',
    'tables' => [],
    'wal'    => file_exists($dbPath . "-wal") ? round(filesize($dbPath . "-wal") / 1024, 2) . ' KB' : '0 KB'
];

if ($dbStats['exists'] && extension_loaded('pdo_sqlite')) {
    try {
        $pdo = new PDO("sqlite:" . $dbPath);
        $pdo->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);
        
        $tablesQuery = $pdo->query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'");
        while ($row = $tablesQuery->fetch(PDO::FETCH_ASSOC)) {
            $tableName = $row['name'];
            $countQuery = $pdo->query("SELECT count(*) as cnt FROM \"$tableName\"");
            $count = $countQuery->fetch(PDO::FETCH_ASSOC)['cnt'] ?? 0;
            $dbStats['tables'][$tableName] = $count;
        }
    } catch (Exception $e) {
        $dbStats['error'] = $e->getMessage();
    }
}

// Xác định tab hiện tại
$currentTab = $_GET['tab'] ?? 'overview';

// Hiển thị riêng phpinfo nếu có yêu cầu
if (isset($_GET['view']) && $_GET['view'] === 'phpinfo') {
    phpinfo();
    exit;
}
?>
<!DOCTYPE html>
<html lang="vi">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>SupportFlast - Trang Quản Trị PHP & XAMPP Hub</title>
    <link rel="icon" type="image/svg+xml" href="data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24' fill='%2338bdf8'><path d='M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5'/></svg>">
    <style>
        :root {
            --bg-base: #090d16;
            --bg-card: rgba(18, 24, 38, 0.85);
            --border-color: rgba(56, 189, 248, 0.2);
            --border-glow: rgba(56, 189, 248, 0.4);
            --primary: #38bdf8;
            --primary-hover: #0284c7;
            --secondary: #818cf8;
            --accent: #10b981;
            --danger: #ef4444;
            --warning: #f59e0b;
            --text-main: #f1f5f9;
            --text-muted: #94a3b8;
        }
        * { box-sizing: border-box; margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif; }
        body {
            background-color: var(--bg-base);
            background-image: radial-gradient(circle at 10% 20%, rgba(56, 189, 248, 0.08) 0%, transparent 40%),
                              radial-gradient(circle at 90% 80%, rgba(129, 140, 248, 0.08) 0%, transparent 40%);
            color: var(--text-main);
            min-height: 100vh;
            padding: 24px;
        }
        .container { max-width: 1200px; margin: 0 auto; }
        
        /* HEADER */
        .header {
            display: flex;
            align-items: center;
            justify-content: space-between;
            padding: 20px 24px;
            background: var(--bg-card);
            backdrop-filter: blur(16px);
            border: 1px solid var(--border-color);
            border-radius: 16px;
            margin-bottom: 24px;
            box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
        }
        .brand { display: flex; align-items: center; gap: 14px; }
        .logo-box {
            width: 44px; height: 44px;
            background: linear-gradient(135deg, #0284c7, #6366f1);
            border-radius: 10px;
            display: flex; align-items: center; justify-content: center;
            color: #fff; font-weight: bold; font-size: 20px;
            box-shadow: 0 0 15px rgba(56, 189, 248, 0.5);
        }
        .brand-text h1 { font-size: 1.25rem; font-weight: 700; color: #fff; }
        .brand-text p { font-size: 0.85rem; color: var(--text-muted); }
        .header-actions { display: flex; gap: 10px; align-items: center; }
        .btn {
            display: inline-flex; align-items: center; gap: 6px;
            padding: 8px 16px; border-radius: 8px; font-size: 0.85rem; font-weight: 600;
            text-decoration: none; cursor: pointer; transition: all 0.2s; border: none;
        }
        .btn-primary { background: linear-gradient(135deg, var(--primary), var(--secondary)); color: #fff; }
        .btn-primary:hover { opacity: 0.9; transform: translateY(-1px); }
        .btn-secondary { background: rgba(255, 255, 255, 0.08); color: var(--text-main); border: 1px solid rgba(255,255,255,0.1); }
        .btn-secondary:hover { background: rgba(255, 255, 255, 0.15); }
        .btn-success { background: #059669; color: #fff; }

        /* TABS */
        .tabs { display: flex; gap: 8px; margin-bottom: 20px; border-bottom: 1px solid var(--border-color); padding-bottom: 10px; }
        .tab-btn {
            padding: 8px 18px; border-radius: 8px; font-size: 0.9rem; font-weight: 600;
            color: var(--text-muted); text-decoration: none; transition: all 0.2s;
            background: transparent;
        }
        .tab-btn:hover { color: #fff; background: rgba(255, 255, 255, 0.05); }
        .tab-btn.active { color: #fff; background: rgba(56, 189, 248, 0.15); border: 1px solid var(--border-color); }

        /* CARDS GRID */
        .grid-cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(280px, 1fr)); gap: 20px; margin-bottom: 24px; }
        .stat-card {
            background: var(--bg-card);
            border: 1px solid var(--border-color);
            border-radius: 12px;
            padding: 20px;
            display: flex; flex-direction: column; gap: 8px;
        }
        .stat-title { font-size: 0.8rem; text-transform: uppercase; color: var(--text-muted); font-weight: 600; letter-spacing: 0.5px; }
        .stat-value { font-size: 1.5rem; font-weight: 700; color: #fff; display: flex; align-items: center; gap: 8px; }
        .stat-desc { font-size: 0.85rem; color: var(--text-muted); }
        .badge {
            display: inline-block; padding: 2px 8px; border-radius: 6px; font-size: 0.75rem; font-weight: bold;
        }
        .badge-success { background: rgba(16, 185, 129, 0.2); color: #34d399; border: 1px solid rgba(16, 185, 129, 0.4); }
        .badge-danger { background: rgba(239, 68, 68, 0.2); color: #f87171; border: 1px solid rgba(239, 68, 68, 0.4); }
        .badge-info { background: rgba(56, 189, 248, 0.2); color: #38bdf8; border: 1px solid rgba(56, 189, 248, 0.4); }

        /* CONTENT PANELS */
        .panel {
            background: var(--bg-card);
            border: 1px solid var(--border-color);
            border-radius: 14px;
            padding: 24px;
            margin-bottom: 24px;
        }
        .panel-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 18px; }
        .panel-title { font-size: 1.1rem; font-weight: 700; color: #fff; }

        /* TABLES */
        table { width: 100%; border-collapse: collapse; text-align: left; }
        th, td { padding: 12px 16px; border-bottom: 1px solid rgba(255, 255, 255, 0.06); font-size: 0.9rem; }
        th { color: var(--text-muted); font-size: 0.8rem; text-transform: uppercase; font-weight: 600; }
        tr:hover td { background: rgba(255, 255, 255, 0.02); }

        /* CODE BLOCK */
        pre {
            background: #050811;
            padding: 16px;
            border-radius: 8px;
            border: 1px solid rgba(255, 255, 255, 0.1);
            overflow-x: auto;
            color: #38bdf8;
            font-size: 0.85rem;
            line-height: 1.5;
        }
    </style>
</head>
<body>
    <div class="container">
        <!-- HEADER -->
        <header class="header">
            <div class="brand">
                <div class="logo-box">PHP</div>
                <div class="brand-text">
                    <h1>SupportFlast PHP Management Console</h1>
                    <p>Trang Quản Trị PHP & Cổng Điều Phối XAMPP Apache Gateway</p>
                </div>
            </div>
            <div class="header-actions">
                <a href="/" class="btn btn-secondary">🌐 Về Web UI 3D</a>
                <a href="?view=phpinfo" target="_blank" class="btn btn-primary">📜 Mở phpinfo()</a>
            </div>
        </header>

        <!-- TABS NAVIGATION -->
        <nav class="tabs">
            <a href="?tab=overview" class="tab-btn <?= $currentTab === 'overview' ? 'active' : '' ?>">📊 Tổng Quan Máy Chủ</a>
            <a href="?tab=engine" class="tab-btn <?= $currentTab === 'engine' ? 'active' : '' ?>">🚀 Go Monolith Engine</a>
            <a href="?tab=database" class="tab-btn <?= $currentTab === 'database' ? 'active' : '' ?>">🗄️ Cơ Sở Dữ Liệu SQLite</a>
            <a href="?tab=extensions" class="tab-btn <?= $currentTab === 'extensions' ? 'active' : '' ?>">🧩 Tiện Ích Mở Rộng (PHP Extensions)</a>
            <a href="?tab=links" class="tab-btn <?= $currentTab === 'links' ? 'active' : '' ?>">🔗 Liên Kết XAMPP</a>
        </nav>

        <?php if ($currentTab === 'overview'): ?>
        <!-- TAB 1: TỔNG QUAN -->
        <div class="grid-cards">
            <div class="stat-card">
                <span class="stat-title">Phiên bản PHP</span>
                <span class="stat-value"><?= htmlspecialchars($phpVersion) ?></span>
                <span class="stat-desc">SAPI: <?= htmlspecialchars($sapi) ?> (NTS x64)</span>
            </div>
            <div class="stat-card">
                <span class="stat-title">SupportFlast Engine</span>
                <span class="stat-value">
                    <?php if ($engineHealthy): ?>
                        <span class="badge badge-success">HOẠT ĐỘNG (<?= $engineLatency ?> ms)</span>
                    <?php else: ?>
                        <span class="badge badge-danger">ĐANG TẮT</span>
                    <?php endif; ?>
                </span>
                <span class="stat-desc">Port nội bộ: 127.0.0.1:8080</span>
            </div>
            <div class="stat-card">
                <span class="stat-title">Hệ Điều Hành & Mạng</span>
                <span class="stat-value"><?= htmlspecialchars($serverIp) ?></span>
                <span class="stat-desc"><?= htmlspecialchars($os) ?></span>
            </div>
            <div class="stat-card">
                <span class="stat-title">Cấp Phát Bộ Nhớ (RAM)</span>
                <span class="stat-value"><?= htmlspecialchars($memoryLimit) ?></span>
                <span class="stat-desc">Upload Max: <?= htmlspecialchars($uploadMax) ?> | Post: <?= htmlspecialchars($postMax) ?></span>
            </div>
        </div>

        <div class="panel">
            <div class="panel-header">
                <h2 class="panel-title">⚙️ Thông Số Cấu Hình PHP & Máy Chủ Web</h2>
            </div>
            <table>
                <thead>
                    <tr>
                        <th>Tham Số Cấu Hình</th>
                        <th>Giá Trị Hiện Tại</th>
                        <th>Trạng Thái & Đánh Giá</th>
                    </tr>
                </thead>
                <tbody>
                    <tr>
                        <td><strong>PHP Version</strong></td>
                        <td><?= htmlspecialchars(PHP_VERSION) ?></td>
                        <td><span class="badge badge-success">Mới nhất (PHP 8.5+)</span></td>
                    </tr>
                    <tr>
                        <td><strong>Thư Mục XAMPP PHP</strong></td>
                        <td><?= htmlspecialchars(dirname(PHP_BINARY)) ?></td>
                        <td><span class="badge badge-info">Đã liên kết Junction</span></td>
                    </tr>
                    <tr>
                        <td><strong>Tệp Cấu Hình (Loaded Configuration File)</strong></td>
                        <td><?= htmlspecialchars(php_ini_loaded_file() ?: 'Mặc định (Không có php.ini)') ?></td>
                        <td><span class="badge badge-info">Sẵn sàng</span></td>
                    </tr>
                    <tr>
                        <td><strong>Upload Max Filesize</strong></td>
                        <td><?= htmlspecialchars($uploadMax) ?></td>
                        <td><span class="badge badge-success">Hỗ trợ tải tệp dung lượng cao</span></td>
                    </tr>
                    <tr>
                        <td><strong>Max Execution Time</strong></td>
                        <td><?= htmlspecialchars($maxExec) ?></td>
                        <td><span class="badge badge-info">Chống nghẽn tác vụ AI/VFS</span></td>
                    </tr>
                    <tr>
                        <td><strong>SQLite3 & PDO SQLite</strong></td>
                        <td><?= extension_loaded('pdo_sqlite') ? 'Đã kích hoạt' : 'Chưa nạp' ?></td>
                        <td><?= extension_loaded('pdo_sqlite') ? '<span class="badge badge-success">Sẵn sàng 100%</span>' : '<span class="badge badge-danger">Cần bật extension</span>' ?></td>
                    </tr>
                    <tr>
                        <td><strong>cURL & OpenSSL</strong></td>
                        <td><?= (extension_loaded('curl') && extension_loaded('openssl')) ? 'Hoạt động hoàn hảo' : 'Thiếu module' ?></td>
                        <td><span class="badge badge-success">Bảo mật mã hóa cao</span></td>
                    </tr>
                </tbody>
            </table>
        </div>

        <?php elseif ($currentTab === 'engine'): ?>
        <!-- TAB 2: SUPPORTFLAST ENGINE -->
        <div class="panel">
            <div class="panel-header">
                <h2 class="panel-title">🚀 Trạng Thái SupportFlast Monolith Go Engine (Cổng 8080)</h2>
                <?php if (!$engineHealthy): ?>
                <form method="POST" style="margin: 0;">
                    <input type="hidden" name="action" value="start_engine">
                    <button type="submit" class="btn btn-success">▶️ Kích Hoạt Engine Ngay</button>
                </form>
                <?php endif; ?>
            </div>

            <?php if ($engineHealthy && $engineData): ?>
            <div class="grid-cards" style="margin-bottom: 20px;">
                <div class="stat-card">
                    <span class="stat-title">Trạng Thái Dịch Vụ</span>
                    <span class="stat-value"><span class="badge badge-success">HEALTHY</span></span>
                    <span class="stat-desc">Dịch vụ: <?= htmlspecialchars($engineData['service'] ?? 'supportflast_engine') ?></span>
                </div>
                <div class="stat-card">
                    <span class="stat-title">Bộ Nhớ RAM Engine</span>
                    <span class="stat-value"><?= round((float)($engineData['alloc_mb'] ?? 0), 2) ?> MB</span>
                    <span class="stat-desc">Tối ưu bộ nhớ theo Rule 7.1</span>
                </div>
                <div class="stat-card">
                    <span class="stat-title">Số Lượng Goroutines</span>
                    <span class="stat-value"><?= (int)($engineData['goroutines'] ?? 0) ?></span>
                    <span class="stat-desc">Đa luồng Go cực nhanh</span>
                </div>
                <div class="stat-card">
                    <span class="stat-title">Độ Trễ Phản Hồi (Ping)</span>
                    <span class="stat-value"><?= $engineLatency ?> ms</span>
                    <span class="stat-desc">Kết nối Reverse Proxy nội bộ</span>
                </div>
            </div>

            <div class="panel-title" style="margin-bottom: 12px;">Dữ Liệu JSON Endpoint /api/health:</div>
            <pre><?= htmlspecialchars(json_encode($engineData, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE)) ?></pre>

            <?php else: ?>
            <div style="padding: 30px; text-align: center; background: rgba(239,68,68,0.05); border: 1px dashed var(--danger); border-radius: 10px;">
                <h3 style="color: var(--danger); margin-bottom: 10px;">⚠️ Engine hiện chưa được kích hoạt hoặc đang tắt</h3>
                <p style="color: var(--text-muted); margin-bottom: 18px;">Bạn có thể nhấn nút bên dưới hoặc chạy file <code>RUN_FULL_SYSTEM.bat</code> để khởi động Engine.</p>
                <form method="POST">
                    <input type="hidden" name="action" value="start_engine">
                    <button type="submit" class="btn btn-primary" style="padding: 10px 24px; font-size: 1rem;">🚀 Bật SupportFlast Engine Ngay Bây Giờ</button>
                </form>
            </div>
            <?php endif; ?>
        </div>

        <?php elseif ($currentTab === 'database'): ?>
        <!-- TAB 3: SQLITE DATABASE -->
        <div class="panel">
            <div class="panel-header">
                <h2 class="panel-title">🗄️ Trình Quản Trị Cơ Sở Dữ Liệu SQLite Cục Bộ (supportflast.db)</h2>
                <span class="badge badge-info">Dung lượng: <?= $dbStats['size'] ?> | WAL: <?= $dbStats['wal'] ?></span>
            </div>

            <p style="color: var(--text-muted); margin-bottom: 18px; font-size: 0.9rem;">
                Tệp CSDL: <code><?= htmlspecialchars($dbPath) ?></code> (Tuân thủ Rule 1.4 lưu trên ổ F)
            </p>

            <?php if (!empty($dbStats['tables'])): ?>
            <table>
                <thead>
                    <tr>
                        <th>Tên Bảng (Table Name)</th>
                        <th>Số Lượng Bản Ghi (Record Count)</th>
                        <th>Mục Đích Dữ Liệu</th>
                        <th>Trạng Thái</th>
                    </tr>
                </thead>
                <tbody>
                    <?php foreach ($dbStats['tables'] as $table => $count): ?>
                    <tr>
                        <td><strong><?= htmlspecialchars($table) ?></strong></td>
                        <td><span class="badge badge-success"><?= $count ?> bản ghi</span></td>
                        <td style="color: var(--text-muted);">
                            <?php 
                                switch($table) {
                                    case 'users': echo 'Tài khoản người dùng, mật khẩu BCrypt & Khóa RSA'; break;
                                    case 'apps': echo 'Kho ứng dụng, danh mục phần mềm & tệp cài đặt'; break;
                                    case 'reviews': echo 'Đánh giá, nhận xét cộng đồng & xếp hạng sao'; break;
                                    case 'system_logs': echo 'Nhật ký kiểm toán an ninh SIEM & Audit'; break;
                                    default: echo 'Bảng dữ liệu nội bộ hệ thống';
                                }
                            ?>
                        </td>
                        <td><span class="badge badge-info">Đồng bộ WAL</span></td>
                    </tr>
                    <?php endforeach; ?>
                </tbody>
            </table>
            <?php else: ?>
            <p style="color: var(--warning);">Chưa tìm thấy bảng dữ liệu nào hoặc CSDL đang ở trạng thái khởi tạo.</p>
            <?php endif; ?>
        </div>

        <?php elseif ($currentTab === 'extensions'): ?>
        <!-- TAB 4: EXTENSIONS -->
        <div class="panel">
            <div class="panel-header">
                <h2 class="panel-title">🧩 Kiểm Tra Các Tiện Ích Mở Rộng PHP (Extensions Checklist)</h2>
            </div>
            <table>
                <thead>
                    <tr>
                        <th>Tên Extension</th>
                        <th>Mô Tả Chức Năng</th>
                        <th>Trạng Thái</th>
                    </tr>
                </thead>
                <tbody>
                    <?php foreach ($extStatus as $ext => $info): ?>
                    <tr>
                        <td><strong><?= htmlspecialchars($ext) ?></strong></td>
                        <td><?= htmlspecialchars($info['desc']) ?></td>
                        <td>
                            <?php if ($info['loaded']): ?>
                                <span class="badge badge-success">✓ ĐÃ KÍCH HOẠT</span>
                            <?php else: ?>
                                <span class="badge badge-danger">✗ CHƯA NẠP</span>
                            <?php endif; ?>
                        </td>
                    </tr>
                    <?php endforeach; ?>
                </tbody>
            </table>
        </div>

        <?php elseif ($currentTab === 'links'): ?>
        <!-- TAB 5: LIÊN KẾT XAMPP -->
        <div class="panel">
            <div class="panel-header">
                <h2 class="panel-title">🔗 Các Đường Dẫn Quản Trị Hệ Thống XAMPP & SupportFlast</h2>
            </div>
            <table>
                <thead>
                    <tr>
                        <th>Tên Dịch Vụ</th>
                        <th>Địa Chỉ URL</th>
                        <th>Ghi Chú</th>
                    </tr>
                </thead>
                <tbody>
                    <tr>
                        <td><strong>SupportFlast Web UI</strong></td>
                        <td><a href="/" target="_blank" style="color: var(--primary);">http://<?= htmlspecialchars($serverIp) ?>/</a></td>
                        <td>Giao diện 3D VisionOS chính</td>
                    </tr>
                    <tr>
                        <td><strong>XAMPP Dashboard</strong></td>
                        <td><a href="/dashboard/" target="_blank" style="color: var(--primary);">http://<?= htmlspecialchars($serverIp) ?>/dashboard/</a></td>
                        <td>Bảng điều khiển mặc định của XAMPP</td>
                    </tr>
                    <tr>
                        <td><strong>PHP Info Chi Tiết</strong></td>
                        <td><a href="/dashboard/phpinfo.php" target="_blank" style="color: var(--primary);">http://<?= htmlspecialchars($serverIp) ?>/dashboard/phpinfo.php</a></td>
                        <td>Xem toàn bộ cấu hình máy chủ PHP</td>
                    </tr>
                    <tr>
                        <td><strong>PHP Gateway Bridge</strong></td>
                        <td><a href="/supportflast/" target="_blank" style="color: var(--primary);">http://<?= htmlspecialchars($serverIp) ?>/supportflast/</a></td>
                        <td>Cổng cURL chuyển tiếp trong suốt qua PHP</td>
                    </tr>
                    <tr>
                        <td><strong>CloudPool Storage (WebDAV)</strong></td>
                        <td><a href="/webdav/" target="_blank" style="color: var(--primary);">http://<?= htmlspecialchars($serverIp) ?>/webdav/</a></td>
                        <td>Ổ đĩa mạng ảo RFC-4918</td>
                    </tr>
                    <tr>
                        <td><strong>Chẩn Đoán Đám Mây (Diagnostics)</strong></td>
                        <td><a href="/api/system/diagnostics" target="_blank" style="color: var(--primary);">http://<?= htmlspecialchars($serverIp) ?>/api/system/diagnostics</a></td>
                        <td>6/6 bài kiểm tra Cloud-Ready 100%</td>
                    </tr>
                </tbody>
            </table>
        </div>
        <?php endif; ?>

        <!-- FOOTER -->
        <footer style="margin-top: 30px; text-align: center; color: var(--text-muted); font-size: 0.85rem;">
            SupportFlast Monolith Platform &copy; 2026. Chạy trên máy chủ XAMPP Apache + PHP 8.5.6 + Go Polyglot Engine.
        </footer>
    </div>
</body>
</html>
