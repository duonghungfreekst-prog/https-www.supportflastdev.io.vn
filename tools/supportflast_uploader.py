#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
SupportFlast App Hub - CLI & CI/CD Deployment Tool
Tu dong day ung dung va goi cai dat len SupportFlast App Hub (supportflastdev.io.vn)
Chay duoc tren Windows, Linux va macOS voi Python 3 tieu chuan (khong can pip install bat ky thu vien nao).
"""

import os
import sys
import json
import argparse
import hashlib
import mimetypes
import uuid
import urllib.request
import urllib.error

# Dam bao terminal Windows in tieng Viet UTF-8 an toan
if sys.platform == "win32":
    try:
        sys.stdout.reconfigure(encoding="utf-8", errors="replace")
        sys.stderr.reconfigure(encoding="utf-8", errors="replace")
    except Exception:
        pass

# ANSI Color codes cho terminal
GREEN = "\033[92m"
BLUE = "\033[94m"
YELLOW = "\033[93m"
RED = "\033[91m"
CYAN = "\033[96m"
BOLD = "\033[1m"
RESET = "\033[0m"

DEFAULT_SERVER_URL = "https://supportflastdev.io.vn"
LOCAL_FALLBACK_URL = "http://127.0.0.1:8080"

def parse_simple_yaml(filepath):
    """
    Parser YAML toi gian bang Python thuan khong can thu vien pyyaml.
    Ho tro cac cap key: value co ban cho file manifest app.yaml
    """
    result = {}
    if not os.path.exists(filepath):
        return result
    with open(filepath, "r", encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            if ":" in line:
                key, val = line.split(":", 1)
                key = key.strip()
                val = val.strip()
                # Xu ly loai bo inline comment neu co
                if "#" in val:
                    # Neu gia tri duoc boc trong ngoac kep hoac ngoac don
                    if val.startswith('"') and '"' in val[1:]:
                        end_quote = val[1:].index('"') + 1
                        val = val[1:end_quote]
                    elif val.startswith("'") and "'" in val[1:]:
                        end_quote = val[1:].index("'") + 1
                        val = val[1:end_quote]
                    else:
                        val = val.split("#", 1)[0].strip()
                val = val.strip().strip("\"'").strip()
                if val.lower() == "true":
                    val = True
                elif val.lower() == "false":
                    val = False
                result[key] = val
    return result

def load_manifest():
    """Tim va nap cau hinh tu app.yaml hoac app.json trong thu muc hien tai"""
    manifest = {}
    if os.path.exists("app.yaml"):
        manifest = parse_simple_yaml("app.yaml")
    elif os.path.exists("app.yml"):
        manifest = parse_simple_yaml("app.yml")
    elif os.path.exists("app.json"):
        try:
            with open("app.json", "r", encoding="utf-8") as f:
                manifest = json.load(f)
        except Exception as e:
            print(f"{YELLOW}[CANH BAO] Khong the doc app.json: {e}{RESET}")
    return manifest

def calculate_sha256(filepath):
    """Tinh ma bam SHA-256 cua file theo chunk 64KB de tranh ton RAM"""
    hasher = hashlib.sha256()
    with open(filepath, "rb") as f:
        while chunk := f.read(65536):
            hasher.update(chunk)
    return hasher.hexdigest()

def format_bytes(size):
    for unit in ['B', 'KB', 'MB', 'GB']:
        if size < 1024.0:
            return f"{size:.1f} {unit}"
        size /= 1024.0
    return f"{size:.1f} TB"

def build_multipart_payload(fields, file_field, filepath):
    """Tao multipart/form-data payload stream an toan"""
    boundary = "----SupportFlastBoundary" + uuid.uuid4().hex
    body = bytearray()

    for k, v in fields.items():
        if v is not None:
            body.extend(f"--{boundary}\r\n".encode("utf-8"))
            body.extend(f'Content-Disposition: form-data; name="{k}"\r\n\r\n'.encode("utf-8"))
            body.extend(f"{v}\r\n".encode("utf-8"))

    filename = os.path.basename(filepath)
    mime_type, _ = mimetypes.guess_type(filepath)
    if not mime_type:
        mime_type = "application/octet-stream"

    body.extend(f"--{boundary}\r\n".encode("utf-8"))
    body.extend(f'Content-Disposition: form-data; name="{file_field}"; filename="{filename}"\r\n'.encode("utf-8"))
    body.extend(f"Content-Type: {mime_type}\r\n\r\n".encode("utf-8"))

    with open(filepath, "rb") as f:
        body.extend(f.read())

    body.extend(b"\r\n")
    body.extend(f"--{boundary}--\r\n".encode("utf-8"))

    content_type = f"multipart/form-data; boundary={boundary}"
    return bytes(body), content_type

def main():
    print(f"\n{BOLD}{CYAN}======================================================={RESET}")
    print(f"{BOLD}{CYAN}    SUPPORTFLAST APP HUB - AUTOMATED PUBLISHER CLI    {RESET}")
    print(f"{CYAN}          Domain: supportflastdev.io.vn (Cloudflare)   {RESET}")
    print(f"{BOLD}{CYAN}======================================================={RESET}\n")

    manifest = load_manifest()

    parser = argparse.ArgumentParser(description="Upload ung dung len SupportFlast App Hub")
    parser.add_argument("--file", "-f", default=manifest.get("file_path") or manifest.get("file"), help="Duong dan den file cai dat (.exe, .msix, .zip, .dmg, .deb)")
    parser.add_argument("--name", "-n", default=manifest.get("name"), help="Ten ung dung")
    parser.add_argument("--version", "-v", default=manifest.get("version"), help="Phien ban (vi du: 1.0.0)")
    parser.add_argument("--platform", "-p", default=manifest.get("platform", "Windows / Linux / macOS"), help="Nen tang ho tro")
    parser.add_argument("--category", "-c", default=manifest.get("category", "Tien ich & He thong"), help="DanhMuc")
    parser.add_argument("--desc", "-d", default=manifest.get("desc", "Ung dung duoc phat hanh tu dong qua SupportFlast CLI."), help="Mo ta ung dung")
    parser.add_argument("--author", "-a", default=manifest.get("author", "SupportFlast Dev"), help="Tac gia hoac to chuc phat trien")
    parser.add_argument("--guide", default=manifest.get("guide"), help="Noi dung bai viet huong dan hoac file markdown")
    parser.add_argument("--guide-file", default=manifest.get("guide_file") or manifest.get("guide_path"), help="Duong dan den file markdown huong dan (.md)")
    parser.add_argument("--video-url", default=manifest.get("video_url") or manifest.get("video"), help="URL video huong dan (YouTube hoac MP4)")
    parser.add_argument("--token", "-t", default=os.environ.get("SUPPORTFLAST_API_KEY") or manifest.get("api_key"), help="API Token de xac thuc")
    parser.add_argument("--server", "-s", default=os.environ.get("SUPPORTFLAST_SERVER_URL") or manifest.get("server_url") or DEFAULT_SERVER_URL, help="URL Backend SupportFlast")
    parser.add_argument("--dry-run", action="store_true", help="Kiem tra tham so ma khong upload thuc te")

    args = parser.parse_args()

    # Kiem tra Token
    token = args.token
    if not token:
        token = "sf_live_default_dev_token_2026"
        print(f"{YELLOW}[CHU Y] Khong tim thay API token trong app.yaml hay bien SUPPORTFLAST_API_KEY.{RESET}")
        print(f"{YELLOW}        Dang su dung token mac dinh san co: {token}{RESET}")

    # Kiem tra file binary neu co
    filepath = args.file
    has_file = False
    file_size = 0
    file_sha256 = ""

    if filepath and os.path.exists(filepath):
        has_file = True
        file_size = os.path.getsize(filepath)
        print(f"{BLUE}[INFO] Phat hien file goi cai dat: {filepath}{RESET}")
        print(f"{BLUE}[INFO] Dung luong file: {format_bytes(file_size)}{RESET}")
        print(f"{BLUE}[INFO] Dang tinh toan ma bam SHA-256 kiem tra toan ven...{RESET}")
        file_sha256 = calculate_sha256(filepath)
        print(f"{GREEN}[OK] SHA-256: {file_sha256}{RESET}")
    elif filepath:
        print(f"{RED}[LOI] File duoc chi dinh khong ton tai: {filepath}{RESET}")
        sys.exit(1)

    # Validate thong tin co ban
    app_name = args.name or (os.path.basename(os.getcwd()) if not filepath else os.path.splitext(os.path.basename(filepath))[0])
    app_version = args.version or "1.0.0"

    # Nap noi dung bai viet huong dan (Guide) neu co
    guide_content = args.guide or ""
    guide_file_path = args.guide_file
    if not guide_content and guide_file_path and os.path.exists(guide_file_path):
        try:
            with open(guide_file_path, "r", encoding="utf-8") as gf:
                guide_content = gf.read()
            print(f"{BLUE}[INFO] Da nap noi dung bai viet huong dan tu: {guide_file_path}{RESET}")
        except Exception as e:
            print(f"{YELLOW}[CANH BAO] Khong doc duoc file huong dan: {e}{RESET}")
    elif guide_content and os.path.exists(guide_content):
        try:
            with open(guide_content, "r", encoding="utf-8") as gf:
                guide_content = gf.read()
            print(f"{BLUE}[INFO] Da nap noi dung bai viet huong dan tu: {args.guide}{RESET}")
        except Exception:
            pass

    video_url = args.video_url or ""

    print(f"\n{BOLD}Thong Tin Xuat Ban:{RESET}")
    print(f"  • Ten Ung Dung : {BOLD}{app_name}{RESET}")
    print(f"  • Phien Ban    : {BOLD}v{app_version}{RESET}")
    print(f"  • Nen Tang     : {args.platform}")
    print(f"  • Danh Muc     : {args.category}")
    print(f"  • Tac Gia      : {args.author}")
    print(f"  • File Goi     : {os.path.basename(filepath) if has_file else 'Metadata Only'}")
    if guide_content:
        print(f"  • Bai Viet HD  : {len(guide_content)} ky tu ({guide_file_path or 'Inline'})")
    if video_url:
        print(f"  • Video HD     : {video_url}")
    print(f"  • Server Dich  : {args.server}")
    print("-" * 55)

    if args.dry_run:
        print(f"\n{YELLOW}[DRY-RUN] Kiem tra hop le hoan tat. Khong co yeu cau mang nao duoc gui di.{RESET}")
        return

    # Chuan bi payload
    publish_endpoint = f"{args.server.rstrip('/')}/api/apps/publish"

    headers = {
        "Authorization": f"Bearer {token}",
        "User-Agent": "SupportFlast-CLI-Uploader/1.0"
    }

    if has_file:
        print(f"\n{CYAN}[UPLOADING] Dang dong goi va tai len {publish_endpoint}...{RESET}")
        fields = {
            "name": app_name,
            "version": app_version,
            "platform": args.platform,
            "category": args.category,
            "desc": args.desc,
            "author": args.author,
            "video_url": video_url,
            "guide": guide_content
        }
        body, content_type = build_multipart_payload(fields, "pkg_file", filepath)
        headers["Content-Type"] = content_type
        headers["Content-Length"] = str(len(body))
    else:
        print(f"\n{CYAN}[PUBLISHING] Dang gui metadata len {publish_endpoint}...{RESET}")
        fields = {
            "name": app_name,
            "version": app_version,
            "platform": args.platform,
            "category": args.category,
            "desc": args.desc,
            "author": args.author,
            "video_url": video_url,
            "guide": guide_content,
            "file_name": f"{app_name.lower().replace(' ', '-')}-v{app_version}.zip",
            "size_bytes": 0
        }
        body = json.dumps(fields).encode("utf-8")
        headers["Content-Type"] = "application/json"
        headers["Content-Length"] = str(len(body))

    # Gui request toi server voi fallback
    req = urllib.request.Request(publish_endpoint, data=body, headers=headers, method="POST")

    def execute_request(request_obj, target_url):
        try:
            with urllib.request.urlopen(request_obj, timeout=60) as resp:
                resp_text = resp.read().decode("utf-8")
                return True, resp.status, resp_text
        except urllib.error.HTTPError as e:
            return False, e.code, e.read().decode("utf-8", errors="ignore")
        except urllib.error.URLError as e:
            return False, 0, str(e.reason)
        except Exception as e:
            return False, -1, str(e)

    success, code, result = execute_request(req, publish_endpoint)

    # Neu goi domain that bai (vi du dang chay local test ma chua set DNS / Cloudflare Tunnel), thu fallback local
    if not success and ("timed out" in result.lower() or "refused" in result.lower() or "getaddrinfo" in result.lower()):
        if args.server != LOCAL_FALLBACK_URL:
            print(f"{YELLOW}[CHU Y] Khong ket noi duoc toi {args.server}. Dang thu tu dong ket noi den Gateway Local ({LOCAL_FALLBACK_URL})...{RESET}")
            fallback_endpoint = f"{LOCAL_FALLBACK_URL}/api/apps/publish"
            req_local = urllib.request.Request(fallback_endpoint, data=body, headers=headers, method="POST")
            success, code, result = execute_request(req_local, fallback_endpoint)

    if success:
        print(f"\n{BOLD}{GREEN}[SUCCESS] DANG TAI THANH CONG! (HTTP {code}){RESET}")
        try:
            res_data = json.loads(result)
            app_info = res_data.get("app", {})
            print(f"  • App ID       : {BOLD}{app_info.get('id')}{RESET}")
            print(f"  • Ten Ung Dung : {app_info.get('name')} (v{app_info.get('version')})")
            print(f"  • Trang Thai   : {app_info.get('status')}")
            print(f"  • SHA-256 Kiem : {app_info.get('sha256')}")
            print(f"  • URL Tai Xuong: {args.server}{app_info.get('download_url')}")
            print(f"\n{GREEN}Ung dung cua ban da xuat hien ngay tren SupportFlast App Hub!{RESET}\n")
        except Exception:
            print("Phan hoi tu server:", result)
    else:
        print(f"\n{BOLD}{RED}[LOI] DANG TAI THAT BAI (Ma loi: {code}){RESET}")
        print(f"{RED}{result}{RESET}\n")
        print(f"{YELLOW}Goi y khac phuc:{RESET}")
        print("  1. Kiem tra lai API token trong app.yaml hoac bien moi truong SUPPORTFLAST_API_KEY.")
        print("  2. Kiem tra xem SupportFlast Engine dang chay tren cong 8080 hoac qua Cloudflare chua.")
        print("  3. Thu chay voi tham so: --server http://127.0.0.1:8080")
        sys.exit(1)

if __name__ == "__main__":
    main()
