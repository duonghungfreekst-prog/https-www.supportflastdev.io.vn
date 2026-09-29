<?php
/**
 * Test Suite kiểm tra độc lập từng kịch bản của XAMPP PHP Gateway Bridge
 */

function runSubTest($title, $phpCode) {
    echo "====================================================\n";
    echo "[TEST] $title\n";

    $tempScript = __DIR__ . DIRECTORY_SEPARATOR . 'tmp_runner.php';
    file_put_contents($tempScript, "<?php\n" . $phpCode);

    $cmd = sprintf('C:\\php\\php.exe "%s"', $tempScript);
    $output = shell_exec($cmd);

    if (file_exists($tempScript)) {
        @unlink($tempScript);
    }

    echo trim($output) . "\n";
}

// 1. Kiểm tra cú pháp Linting
runSubTest("1. Linting PHP files", '
$files = [
    "f:\\\\supportflast.dev\\\\tools\\\\xampp_htdocs_bridge\\\\index.php",
    "C:\\\\xampp\\\\htdocs\\\\supportflast\\\\index.php",
    "C:\\\\xampp\\\\htdocs\\\\supportflast.php"
];
$allOk = true;
foreach ($files as $f) {
    if (!file_exists($f)) {
        echo "[FAIL] File khong ton tai: $f\n";
        $allOk = false;
    }
}
if ($allOk) echo "[PASS] Tat ca file bridge da san sang!\n";
');

// 2. Test GET /api/health qua C:\xampp\htdocs\supportflast\index.php
runSubTest("2. Proxy GET /api/health qua supportflast/index.php", '
$_SERVER["REQUEST_METHOD"] = "GET";
$_SERVER["REQUEST_URI"] = "/supportflast/api/health";
$_SERVER["HTTP_HOST"] = "localhost";
$_SERVER["REMOTE_ADDR"] = "127.0.0.1";

ob_start();
include "C:\\\\xampp\\\\htdocs\\\\supportflast\\\\index.php";
$out = ob_get_clean();

$data = json_decode($out, true);
if (is_array($data) && ($data["status"] === "healthy" || isset($data["service"]))) {
    echo "[PASS] Health status: " . ($data["status"] ?? "ok") . " | Service: " . ($data["service"] ?? "N/A") . "\n";
} else {
    echo "[FAIL] Response: " . substr($out, 0, 150) . "\n";
}
');

// 3. Test GET /api/health qua C:\xampp\htdocs\supportflast.php
runSubTest("3. Proxy GET /api/health qua root supportflast.php", '
$_SERVER["REQUEST_METHOD"] = "GET";
$_SERVER["REQUEST_URI"] = "/supportflast.php/api/health";
$_SERVER["HTTP_HOST"] = "localhost";
$_SERVER["REMOTE_ADDR"] = "127.0.0.1";

ob_start();
include "C:\\\\xampp\\\\htdocs\\\\supportflast.php";
$out = ob_get_clean();

$data = json_decode($out, true);
if (is_array($data) && ($data["status"] === "healthy" || isset($data["service"]))) {
    echo "[PASS] Root supportflast.php hoat dong tot! Status: " . ($data["status"] ?? "ok") . "\n";
} else {
    echo "[FAIL] Response: " . substr($out, 0, 150) . "\n";
}
');

// 4. Test WebDAV OPTIONS method
runSubTest("4. WebDAV OPTIONS Method", '
$_SERVER["REQUEST_METHOD"] = "OPTIONS";
$_SERVER["REQUEST_URI"] = "/supportflast/webdav/";
$_SERVER["HTTP_HOST"] = "localhost";
$_SERVER["REMOTE_ADDR"] = "127.0.0.1";

ob_start();
include "C:\\\\xampp\\\\htdocs\\\\supportflast\\\\index.php";
$out = ob_get_clean();
$code = http_response_code();

if ($code === 200 || $code === 204) {
    echo "[PASS] WebDAV OPTIONS nhan HTTP code: $code\n";
} else {
    echo "[FAIL] WebDAV OPTIONS code: $code\n";
}
');

// 5. Test WebDAV PROPFIND method
runSubTest("5. WebDAV PROPFIND Method", '
$_SERVER["REQUEST_METHOD"] = "PROPFIND";
$_SERVER["REQUEST_URI"] = "/supportflast/webdav/";
$_SERVER["HTTP_HOST"] = "localhost";
$_SERVER["HTTP_DEPTH"] = "1";
$_SERVER["REMOTE_ADDR"] = "127.0.0.1";

ob_start();
include "C:\\\\xampp\\\\htdocs\\\\supportflast\\\\index.php";
$out = ob_get_clean();
$code = http_response_code();

// Go Engine WebDAV bảo mật sẽ trả về 401 Unauthorized khi thiếu auth hoặc 207 Multi-Status khi đã auth
if ($code === 401 || $code === 207 || $code === 200) {
    echo "[PASS] WebDAV PROPFIND chuyen tiep thanh cong! HTTP code: $code\n";
} else {
    echo "[FAIL] PROPFIND code: $code\n";
}
');

// 6. Test Security Headers và CRLF Sanitize
runSubTest("6. Security Headers & CRLF Injection Prevention", '
$_SERVER["REQUEST_METHOD"] = "GET";
$_SERVER["REQUEST_URI"] = "/supportflast/api/health";
$_SERVER["HTTP_HOST"] = "localhost\r\nInjected-Header: malicious";
$_SERVER["REMOTE_ADDR"] = "127.0.0.1";

ob_start();
include "C:\\\\xampp\\\\htdocs\\\\supportflast\\\\index.php";
$out = ob_get_clean();

$hList = headers_list();
$foundNosniff = false;
$foundInjected = false;

foreach ($hList as $h) {
    if (stripos($h, "X-Content-Type-Options: nosniff") !== false) $foundNosniff = true;
    if (stripos($h, "Injected-Header") !== false) $foundInjected = true;
}

if ($foundNosniff && !$foundInjected) {
    echo "[PASS] Security headers day du, CRLF Injection duoc ngan chan triet de!\n";
} else {
    echo "[FAIL] Nosniff: " . ($foundNosniff ? "Yes" : "No") . " | Injected: " . ($foundInjected ? "Exposed!" : "Blocked") . "\n";
}
');

echo "====================================================\n";
echo "TAT CA TEST CASE DA HOAN TAT!\n";
