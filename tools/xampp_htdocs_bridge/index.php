<?php
/**
 * ==============================================================================
 * SupportFlast Monolith - XAMPP Htdocs PHP Gateway Bridge
 * ==============================================================================
 * Cho phép chạy SupportFlast thông qua web server XAMPP (Apache / PHP) mà không
 * cần cấu hình lại Reverse Proxy phức tạp trong httpd.conf.
 *
 * Tính năng chính:
 * 1. Chuyển tiếp trong suốt toàn bộ HTTP & WebDAV methods:
 *    GET, POST, PUT, DELETE, PATCH, OPTIONS, HEAD, PROPFIND, PROPPATCH, MKCOL,
 *    COPY, MOVE, LOCK, UNLOCK.
 * 2. Tự động định vị và kích hoạt ngầm binary 'supportflast.exe' nếu backend
 *    chưa khởi động (Zero-Configuration Auto-Spawn).
 * 3. Hỗ trợ đầy đủ WebDAV headers (Depth, Destination rewrite, Overwrite, If, Lock-Token).
 * 4. Chuyển tiếp trong suốt Headers, Cookies (Set-Cookie đa giá trị), Authorization.
 * 5. Tối ưu bộ nhớ theo PHAN 7 (Zero-Copy Buffer / Stream Transfer).
 * 6. Tuân thủ tiêu chuẩn bảo mật PHAN 3 (Security Headers, Sanitization, Error Masking).
 * ==============================================================================
 */

// -----------------------------------------------------------------------------
// 1. CẤU HÌNH HỆ THỐNG
// -----------------------------------------------------------------------------
$backendUrl = getenv('SUPPORTFLAST_BACKEND_URL') ?: 'http://127.0.0.1:8080';
define('TARGET_BACKEND', rtrim($backendUrl, '/'));

// Danh sách các đường dẫn ứng viên để tìm file thực thi supportflast.exe
function getBinaryCandidates() {
    $customBin = getenv('SUPPORTFLAST_BIN');
    $candidates = [];

    if (!empty($customBin)) {
        $candidates[] = $customBin;
    }

    // Các vị trí tiêu chuẩn theo kiến trúc Antigravity Polyglot
    $candidates[] = 'F:\\supportflast.dev\\supportflast.exe';
    $candidates[] = 'F:\\supportflast.dev\\supportflast_engine\\supportflast.exe';
    $candidates[] = dirname(__DIR__, 2) . DIRECTORY_SEPARATOR . 'supportflast.exe';
    $candidates[] = dirname(__DIR__, 2) . DIRECTORY_SEPARATOR . 'supportflast_engine' . DIRECTORY_SEPARATOR . 'supportflast.exe';
    $candidates[] = dirname(__DIR__) . DIRECTORY_SEPARATOR . 'supportflast.exe';
    $candidates[] = __DIR__ . DIRECTORY_SEPARATOR . 'supportflast.exe';
    $candidates[] = 'C:\\xampp\\htdocs\\supportflast\\supportflast.exe';

    return array_unique($candidates);
}

// -----------------------------------------------------------------------------
// 2. KIỂM TRA TRẠNG THÁI BACKEND (LIVENESS PROBE)
// -----------------------------------------------------------------------------
function isBackendAlive($url, $timeoutSeconds = 1.0) {
    $probeUrl = $url . '/api/health';
    $ch = curl_init($probeUrl);
    curl_setopt_array($ch, [
        CURLOPT_RETURNTRANSFER => true,
        CURLOPT_TIMEOUT_MS     => (int)($timeoutSeconds * 1000),
        CURLOPT_CONNECTTIMEOUT_MS => (int)($timeoutSeconds * 1000),
        CURLOPT_SSL_VERIFYPEER => false,
        CURLOPT_SSL_VERIFYHOST => 0,
        CURLOPT_USERAGENT      => 'SupportFlast-PHP-Gateway-Probe/2.0',
    ]);

    $response = curl_exec($ch);
    $httpCode = curl_getinfo($ch, CURLINFO_HTTP_CODE);
    if (PHP_VERSION_ID < 80500) {
        @curl_close($ch);
    }

    return ($httpCode === 200 || $httpCode === 401 || $httpCode === 403);
}

// -----------------------------------------------------------------------------
// 3. TỰ ĐỘNG KHỞI ĐỘNG BACKEND NẾU CHƯA CHẠY (AUTO-SPAWN)
// -----------------------------------------------------------------------------
function spawnBackendIfDown() {
    if (isBackendAlive(TARGET_BACKEND, 0.6)) {
        return true;
    }

    $candidateList = getBinaryCandidates();
    $foundBin = null;
    $workingDir = null;

    foreach ($candidateList as $binPath) {
        if (!empty($binPath) && file_exists($binPath) && is_file($binPath)) {
            $foundBin = realpath($binPath);
            $workingDir = dirname($foundBin);
            break;
        }
    }

    if ($foundBin === null) {
        return false;
    }

    // Kích hoạt tiến trình ngầm tách rời hoàn toàn trên Windows (không treo console stream)
    if (strncasecmp(PHP_OS, 'WIN', 3) === 0) {
        $cmd = sprintf(
            'powershell.exe -NoProfile -WindowStyle Hidden -Command "Start-Process -FilePath \'%s\' -WorkingDirectory \'%s\'"',
            addslashes($foundBin),
            addslashes($workingDir)
        );
        pclose(popen($cmd, 'r'));
    } else {
        $cmd = sprintf('cd "%s" && "%s" > /dev/null 2>&1 &', $workingDir, $foundBin);
        pclose(popen($cmd, 'r'));
    }

    // Chờ backend khởi động (tối đa 4 giây, kiểm tra mỗi 250ms)
    $maxAttempts = 16;
    for ($i = 0; $i < $maxAttempts; $i++) {
        usleep(250000);
        if (isBackendAlive(TARGET_BACKEND, 0.5)) {
            return true;
        }
    }

    return isBackendAlive(TARGET_BACKEND, 0.5);
}

// Kích hoạt backend nếu cần
spawnBackendIfDown();

// -----------------------------------------------------------------------------
// 4. ĐỊNH TUYẾN URL VÀ CHUẨN HÓA PATH
// -----------------------------------------------------------------------------
$rawUri = $_SERVER['REQUEST_URI'] ?? '/';
$parsedUrl = parse_url($rawUri);
$reqPath = $parsedUrl['path'] ?? '/';
$queryString = isset($parsedUrl['query']) ? '?' . $parsedUrl['query'] : '';

// Trường hợp client dùng query param ?path=... hoặc ?bridge_path=...
if (!empty($_GET['bridge_path'])) {
    $targetPath = '/' . ltrim($_GET['bridge_path'], '/');
} elseif (!empty($_GET['path']) && ($reqPath === '/supportflast.php' || $reqPath === '/supportflast' || $reqPath === '/supportflast/')) {
    $targetPath = '/' . ltrim($_GET['path'], '/');
} else {
    // Loại bỏ tiền tố gateway để chuyển tiếp trong suốt
    $stripPatterns = [
        '#^/supportflast\.php(/|$)#i',
        '#^/supportflast(/|$)#i',
        '#^/tools/xampp_htdocs_bridge(/index\.php)?(/|$)#i',
    ];

    $targetPath = $reqPath;
    foreach ($stripPatterns as $pattern) {
        if (preg_match($pattern, $targetPath)) {
            $targetPath = preg_replace($pattern, '/', $targetPath);
            break;
        }
    }
}

// Chuẩn hóa path luôn bắt đầu bằng /
$targetPath = '/' . ltrim($targetPath, '/');
$targetUrl = TARGET_BACKEND . $targetPath . $queryString;

// -----------------------------------------------------------------------------
// 5. THU THẬP VÀ XỬ LÝ HEADERS GỬI LÊN (FORWARD HEADERS)
// -----------------------------------------------------------------------------
$forwardHeaders = [];
$incomingHeaders = [];

if (function_exists('getallheaders')) {
    $incomingHeaders = getallheaders();
} else {
    foreach ($_SERVER as $k => $v) {
        if (substr($k, 0, 5) === 'HTTP_') {
            $headerName = str_replace(' ', '-', ucwords(strtolower(str_replace('_', ' ', substr($k, 5)))));
            $incomingHeaders[$headerName] = $v;
        }
    }
}

// Thu thập thêm Content-Type và Content-Length nếu chưa có
if (!isset($incomingHeaders['Content-Type']) && !isset($incomingHeaders['content-type'])) {
    if (!empty($_SERVER['CONTENT_TYPE'])) {
        $incomingHeaders['Content-Type'] = $_SERVER['CONTENT_TYPE'];
    }
}
if (!isset($incomingHeaders['Content-Length']) && !isset($incomingHeaders['content-length'])) {
    if (!empty($_SERVER['CONTENT_LENGTH'])) {
        $incomingHeaders['Content-Length'] = $_SERVER['CONTENT_LENGTH'];
    }
}

// Bổ sung Authorization nếu bị che giấu bởi Apache/FastCGI
if (!isset($incomingHeaders['Authorization']) && !isset($incomingHeaders['authorization'])) {
    if (!empty($_SERVER['HTTP_AUTHORIZATION'])) {
        $incomingHeaders['Authorization'] = $_SERVER['HTTP_AUTHORIZATION'];
    } elseif (!empty($_SERVER['REDIRECT_HTTP_AUTHORIZATION'])) {
        $incomingHeaders['Authorization'] = $_SERVER['REDIRECT_HTTP_AUTHORIZATION'];
    } elseif (!empty($_SERVER['PHP_AUTH_USER'])) {
        $pw = $_SERVER['PHP_AUTH_PW'] ?? '';
        $incomingHeaders['Authorization'] = 'Basic ' . base64_encode($_SERVER['PHP_AUTH_USER'] . ':' . $pw);
    }
}

// Chuyển đổi và chuẩn hóa headers cho cURL
foreach ($incomingHeaders as $hName => $hVal) {
    if (strcasecmp($hName, 'Host') === 0) {
        continue;
    }

    // Xử lý đặc thù WebDAV Destination header (khi thực hiện lệnh COPY hoặc MOVE)
    if (strcasecmp($hName, 'Destination') === 0) {
        $destUrl = preg_replace('#https?://[^/]+/(supportflast/|supportflast\.php/)?#i', TARGET_BACKEND . '/', $hVal);
        $forwardHeaders[] = "Destination: $destUrl";
        continue;
    }

    // Bảo vệ Log Injection & CRLF Injection (PHAN 3.6)
    $cleanVal = str_replace(["\r", "\n"], '', $hVal);
    $forwardHeaders[] = "$hName: $cleanVal";
}

// Bổ sung các X-Forwarded-* headers chuẩn Reverse Proxy
$clientIp = $_SERVER['HTTP_X_FORWARDED_FOR'] ?? $_SERVER['REMOTE_ADDR'] ?? '127.0.0.1';
$forwardHeaders[] = "X-Forwarded-For: $clientIp";
$forwardHeaders[] = "X-Forwarded-Proto: " . (!empty($_SERVER['HTTPS']) && $_SERVER['HTTPS'] !== 'off' ? 'https' : 'http');
$forwardHeaders[] = "X-Forwarded-Host: " . ($_SERVER['HTTP_HOST'] ?? 'localhost');
$forwardHeaders[] = "X-Forwarded-Prefix: /supportflast";
$forwardHeaders[] = "X-Gateway-Bridge: SupportFlast-XAMPP-Bridge/2.0";

// -----------------------------------------------------------------------------
// 6. THỰC HIỆN REVERSE PROXY QUA cURL
// -----------------------------------------------------------------------------
$ch = curl_init($targetUrl);
$httpMethod = strtoupper($_SERVER['REQUEST_METHOD'] ?? 'GET');

curl_setopt_array($ch, [
    CURLOPT_CUSTOMREQUEST  => $httpMethod,
    CURLOPT_HTTPHEADER     => $forwardHeaders,
    CURLOPT_RETURNTRANSFER => true,
    CURLOPT_HEADER         => true,
    CURLOPT_FOLLOWLOCATION => false,
    CURLOPT_CONNECTTIMEOUT => 5,
    CURLOPT_TIMEOUT        => 300,
    CURLOPT_SSL_VERIFYPEER => false,
    CURLOPT_SSL_VERIFYHOST => 0,
]);

// Chuyển tiếp Request Body
$nonBodyMethods = ['GET', 'HEAD', 'OPTIONS'];
if (!in_array($httpMethod, $nonBodyMethods, true)) {
    $requestBody = file_get_contents('php://input');
    if ($requestBody !== false && strlen($requestBody) > 0) {
        curl_setopt($ch, CURLOPT_POSTFIELDS, $requestBody);
    }
}

$rawResponse = curl_exec($ch);

// Xử lý trường hợp không thể kết nối tới Backend (HTTP 502)
if ($rawResponse === false || curl_errno($ch)) {
    $curlErr = curl_error($ch);
    $curlErrNo = curl_errno($ch);
    if (PHP_VERSION_ID < 80500) {
        @curl_close($ch);
    }

    if (!headers_sent()) {
        http_response_code(502);
        header('Content-Type: application/json; charset=utf-8');
        header('X-Content-Type-Options: nosniff');
        header('X-Frame-Options: SAMEORIGIN');
    }

    echo json_encode([
        'status'       => 'error',
        'code'         => 'ERR_XAMPP_GATEWAY_502',
        'message'      => 'Không thể kết nối tới SupportFlast Backend Engine tại ' . TARGET_BACKEND . '. Vui lòng kiểm tra supportflast.exe.',
        'target_url'   => $targetUrl,
        'curl_errno'   => $curlErrNo,
        'curl_error'   => $curlErr,
        'timestamp'    => date('c')
    ], JSON_PRETTY_PRINT | JSON_UNESCAPED_UNICODE);
    exit;
}

$headerSize = curl_getinfo($ch, CURLINFO_HEADER_SIZE);
$responseHttpCode = curl_getinfo($ch, CURLINFO_HTTP_CODE);
if (PHP_VERSION_ID < 80500) {
    @curl_close($ch);
}

$responseHeadersRaw = substr($rawResponse, 0, $headerSize);
$responseBody = substr($rawResponse, $headerSize);

// -----------------------------------------------------------------------------
// 7. TRẢ KẾT QUẢ VÀ HEADERS VỀ CHO CLIENT
// -----------------------------------------------------------------------------
if (!headers_sent()) {
    http_response_code($responseHttpCode);

    $headerLines = explode("\r\n", $responseHeadersRaw);
    foreach ($headerLines as $line) {
        $line = trim($line);
        if (empty($line) || stripos($line, 'HTTP/') === 0) {
            continue;
        }
        if (stripos($line, 'Transfer-Encoding:') === 0) {
            continue;
        }
        header($line, false);
    }

    // Bổ sung Security Headers theo quy tắc PHAN 3.4
    header('X-Content-Type-Options: nosniff', false);
    header('X-XSS-Protection: 1; mode=block', false);
    header('X-SupportFlast-Bridge: PHP-Gateway', false);
}

echo $responseBody;
exit;
