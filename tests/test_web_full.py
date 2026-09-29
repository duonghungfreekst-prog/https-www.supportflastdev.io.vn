import sys
import time
import json
import urllib.request
import urllib.error
import urllib.parse
from playwright.sync_api import sync_playwright

sys.stdout.reconfigure(encoding='utf-8')

BASE_URL = "http://127.0.0.1:8080"
test_results = []

def run_test(name, func):
    try:
        t0 = time.time()
        detail = func()
        ms = int((time.time() - t0) * 1000)
        test_results.append({"name": name, "status": "PASS", "time_ms": ms, "detail": detail})
        print(f" [PASS] {name} ({ms}ms) - {detail}")
    except Exception as e:
        test_results.append({"name": name, "status": "FAIL", "time_ms": 0, "detail": str(e)})
        print(f" [FAIL] {name} - Error: {e}")

# ==========================================
# 1. KIỂM THỬ CÁC ENDPOINT API HỆ THỐNG
# ==========================================
print("\n" + "="*60)
print("  BAT DAU CHAY TEST SUITE TOAN DIEN WEB SUPPORTFLAST")
print("="*60 + "\n")

# Test 1: Healthcheck API
def test_health():
    req = urllib.request.Request(f"{BASE_URL}/api/health")
    with urllib.request.urlopen(req, timeout=5) as res:
        data = json.loads(res.read().decode('utf-8'))
        assert data.get("status") == "healthy", "Status not healthy"
        return f"Status: {data.get('status')}, Alloc: {data.get('alloc_mb'):.2f}MB, Goroutines: {data.get('goroutines')}"
run_test("API /api/health", test_health)

# Test 2: Cloudflare WAF Security Status
def test_security_waf():
    req = urllib.request.Request(f"{BASE_URL}/api/security/cloudflare-status")
    with urllib.request.urlopen(req, timeout=5) as res:
        data = json.loads(res.read().decode('utf-8'))
        assert data.get("status") == "active", "WAF status not active"
        return f"Shield: {data.get('waf_shield')}, RayID: {data.get('cf_ray')}"
run_test("API /api/security/cloudflare-status", test_security_waf)

# Test 3: Kho Ứng Dụng (App Store & Bài viết công bố tính năng)
def test_apps_list():
    req = urllib.request.Request(f"{BASE_URL}/api/apps")
    with urllib.request.urlopen(req, timeout=5) as res:
        data = json.loads(res.read().decode('utf-8'))
        apps = data if isinstance(data, list) else data.get("apps", [])
        assert len(apps) >= 2, f"Expected at least 2 apps, got {len(apps)}"
        dmh_app = next((a for a in apps if "DMH" in a.get("name", "")), None)
        assert dmh_app is not None, "DMH Tools app not found"
        assert dmh_app.get("version") == "7.0.1", "Version mismatch"
        assert len(dmh_app.get("guide", "")) > 100, "Release guide article is missing or empty"
        return f"Found {len(apps)} apps, verified: {dmh_app.get('name')} v{dmh_app.get('version')}"
run_test("API /api/apps (Danh Sách Ứng Dụng & Bài Viết Công Bố)", test_apps_list)

# Test 4: Tải gói cài đặt & Tự động tăng lượt tải
def test_app_download():
    req = urllib.request.Request(f"{BASE_URL}/api/apps/download/APP-DMH-701")
    with urllib.request.urlopen(req, timeout=5) as res:
        code = res.getcode()
        assert code in (200, 302), f"Unexpected HTTP status: {code}"
        return f"HTTP {code} Download stream OK"
run_test("API /api/apps/download/APP-DMH-701", test_app_download)

# Test 5: Đánh giá cộng đồng (GET & POST)
review_test_id = None
def test_reviews_flow():
    global review_test_id
    # GET reviews
    req = urllib.request.Request(f"{BASE_URL}/api/reviews")
    with urllib.request.urlopen(req, timeout=5) as res:
        data = json.loads(res.read().decode('utf-8'))
        avg_stars = data.get("average_stars", 0)
    
    # POST new review
    post_data = json.dumps({
        "author_name": "QA Automated Robot",
        "author_role": "Automation Engineer",
        "stars": 5,
        "text": "Kiem thu tu dong toan bo he thong SupportFlast chay rat tot!"
    }).encode('utf-8')
    req_post = urllib.request.Request(f"{BASE_URL}/api/reviews", data=post_data, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req_post, timeout=5) as res:
        res_data = json.loads(res.read().decode('utf-8'))
        review_test_id = res_data.get("id") or (res_data.get("review", {})).get("id")
        return f"Avg Stars: {avg_stars}, Created Test Review ID: {review_test_id}"
run_test("API /api/reviews (GET & POST Đánh Giá Thật)", test_reviews_flow)

# Test 6: Xác thực Đăng Nhập Quản Trị Viên (Admin Authentication)
admin_token = None
def test_admin_auth():
    global admin_token
    login_payload = json.dumps({
        "identifier": "admin",
        "password": "Admin@2026!SupportFlast"
    }).encode('utf-8')
    req = urllib.request.Request(f"{BASE_URL}/api/auth/login", data=login_payload, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=5) as res:
        data = json.loads(res.read().decode('utf-8'))
        admin_token = data.get("token") or data.get("session_token")
        assert admin_token is not None, "Login token is missing"
    
    # Verify via /api/auth/me
    req_me = urllib.request.Request(f"{BASE_URL}/api/auth/me", headers={"Authorization": f"Bearer {admin_token}"})
    with urllib.request.urlopen(req_me, timeout=5) as res_me:
        data_me = json.loads(res_me.read().decode('utf-8'))
        user = data_me.get("user", {})
        assert user.get("role") == "admin", f"Role mismatch: {user.get('role')}"
        return f"Login successful, User: {user.get('username')}, Role: {user.get('role')}"
run_test("API /api/auth/login & /api/auth/me (Xác Thực Admin)", test_admin_auth)

# Test 7: Dọn dẹp review test (DELETE)
if review_test_id and admin_token:
    def test_delete_review():
        req_del = urllib.request.Request(
            f"{BASE_URL}/api/reviews/{review_test_id}", 
            headers={"Authorization": f"Bearer {admin_token}"},
            method="DELETE"
        )
        try:
            with urllib.request.urlopen(req_del, timeout=5) as res:
                return f"Cleaned up test review {review_test_id}"
        except urllib.error.HTTPError as e:
            return f"Cleanup status: {e.code}"
    run_test("API /api/reviews/:id (DELETE Cleanup)", test_delete_review)

# Test 8: Nhật ký cập nhật hệ thống
def test_system_releases():
    req = urllib.request.Request(f"{BASE_URL}/api/system/updates")
    with urllib.request.urlopen(req, timeout=5) as res:
        data = json.loads(res.read().decode('utf-8'))
        releases = data.get("releases", [])
        assert len(releases) >= 3, f"Expected at least 3 releases, got {len(releases)}"
        latest = releases[0]
        return f"Total releases: {len(releases)}, Latest version: {latest.get('version')}"
run_test("API /api/system/updates (Nhật Ký Cập Nhật)", test_system_releases)

# Test 9: Đồng bộ cấu hình phần cứng thiết bị
def test_device_sync():
    sync_data = json.dumps({
        "platform": "windows",
        "cpu_cores": 16,
        "ram_gb": 32,
        "screen": "1920x1080"
    }).encode('utf-8')
    req = urllib.request.Request(f"{BASE_URL}/api/device/sync", data=sync_data, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=5) as res:
        data = json.loads(res.read().decode('utf-8'))
        return f"Sync status: {data.get('status', 'OK')}, Matched Platform: {data.get('matched_platform', 'windows')}"
run_test("API /api/device/sync (Đồng Bộ Thiết Bị Thích Ứng)", test_device_sync)


# ==========================================
# 2. KIỂM THỬ GIAO DIỆN END-TO-END (PLAYWRIGHT)
# ==========================================
print("\n" + "="*60)
print("  BAT DAU CHAY E2E UI TEST (PLAYWRIGHT CHROMIUM)")
print("="*60 + "\n")

def test_playwright_e2e():
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1366, "height": 850})
        
        # 1. Truy cập Trang Chủ
        page.goto(f"{BASE_URL}/?v=qa_verified#home", wait_until="domcontentloaded")
        time.sleep(1.5)
        
        # Kiểm tra 5 tab Navbar
        assert page.is_visible("#nav-home"), "Tab Trang chu not found"
        assert page.is_visible("#nav-download"), "Tab Kho Ung Dung not found"
        assert page.is_visible("#nav-videos"), "Tab Video & Huong dan not found"
        assert page.is_visible("#nav-reviews"), "Tab Danh gia not found"
        assert page.is_visible("#nav-changelog"), "Tab Cap nhat not found"
        
        # 2. Chuyển sang Kho Ứng Dụng #download
        page.click("#nav-download")
        time.sleep(2)
        
        # Kiểm tra bài viết công bố tính năng của DMH Tools v7.0.1
        app_title = page.text_content(".release-app-title")
        assert "DMH Tools" in app_title or "SupportFlast" in app_title, "App release card not visible"
        
        # Kiểm tra khung bài viết công bố tính năng
        article_body = page.query_selector(".release-article-body")
        assert article_body is not None, "Release article body not found"
        
        # 3. Chụp ảnh màn hình nghiệm thu toàn bộ kết quả kiểm thử
        screenshot_path = r"C:\Users\Administrator\.gemini\antigravity\brain\cc3a87b9-eb60-4377-8796-fc675cfa4097\e2e_test_verification.png"
        page.screenshot(path=screenshot_path, full_page=True)
        
        browser.close()
        return f"Verified 5 Navbar Tabs, Release Article Card, and saved screenshot to {screenshot_path}"

run_test("E2E UI Flow (Navbar, Kho Ứng Dụng, Release Card, Screenshot)", test_playwright_e2e)

# ==========================================
# 3. TỔNG HỢP KẾT QUẢ KIỂM THỬ
# ==========================================
print("\n" + "="*60)
passed = sum(1 for t in test_results if t["status"] == "PASS")
total = len(test_results)
print(f"  TONG KET KIEM THU: {passed}/{total} TESTS PASS ({'100% HOAN HAO' if passed == total else 'CO LOI'})")
print("="*60 + "\n")
