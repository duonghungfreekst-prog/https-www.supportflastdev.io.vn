#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
================================================================================
SUPPORTFLAST.DEV - UNIVERSAL HOSTING & RELEASE PACKAGER
Công cụ đóng gói tự động toàn bộ hệ thống supportflast.dev
thành gói phân phối zip sẵn sàng triển khai trên mọi nền tảng hosting:
  - cPanel / DirectAdmin / Shared Hosting (.htaccess, PHP Proxy)
  - VPS Linux (Ubuntu / Debian / CentOS / Rocky) (install.sh, Docker, Binary)
  - Docker & Docker Compose (Production ready)
  - Cloud PaaS (Render, Railway, Fly.io, Heroku)
  - Windows Server & IIS (web.config, HttpPlatformHandler)
================================================================================
"""

import os
import sys
import time
import zipfile
import hashlib
from pathlib import Path

# Đảm bảo UTF-8 encoding trên mọi thiết bị console (đặc biệt là Windows)
if hasattr(sys.stdout, "reconfigure"):
    try:
        sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    except Exception:
        pass
if hasattr(sys.stderr, "reconfigure"):
    try:
        sys.stderr.reconfigure(encoding="utf-8", errors="replace")
    except Exception:
        pass

# Màu ANSI console
GREEN = "\033[92m"
YELLOW = "\033[93m"
CYAN = "\033[96m"
RED = "\033[91m"
BOLD = "\033[1m"
RESET = "\033[0m"

# Danh sách tệp tin và thư mục BẮT BUỘC phải có trong gói phát hành
MANDATORY_ENTRIES = [
    # Binaries độc lập cho cả 2 hệ điều hành phổ biến nhất
    "supportflast.exe",
    "supportflast_linux_amd64",
    "supportflast_core.dll",

    # Web UI & Static Assets (3D HUD Three.js + Tools + Cloud Storage UI)
    "supportflast_ui",

    # Mã nguồn Engine & Cấu hình Go
    "supportflast_engine",

    # Phân hệ Multi-Agent AI Python
    "supportflast_ai",

    # Cấu hình Web Server & Reverse Proxy đa nền tảng
    ".htaccess",
    "web.config",
    "Caddyfile",
    "nginx_supportflastdev.io.vn.conf",
    "cloudflared_config.yml",

    # Trình cài đặt tự động 1-Click cho Linux VPS
    "install.sh",
    "deploy_vps.sh",
    "backup_sqlite.sh",

    # Cấu hình Container & PaaS Cloud
    "Dockerfile",
    "docker-compose.yml",
    "fly.toml",
    "render.yaml",
    "Procfile",
    "railway.json",
    ".dockerignore",

    # Kịch bản khởi chạy trên Windows & XAMPP
    "RUN_FULL_SYSTEM.bat",
    "STOP_FULL_SYSTEM.bat",
    "setup_xampp.bat",
    "setup_local_dns.bat",

    # Hạ tầng Reverse Proxy & Công cụ bổ trợ
    "infra",
    "tools",
    "docs",

    # Tài liệu hướng dẫn triển khai tối thượng
    "UNIVERSAL_HOSTING_GUIDE.md",
    "HOSTING_DEPLOYMENT_GUIDE.md",
    "cloudflare_security_guide.md",
    "cloudflare_waf_rules.json",
    "README.md",
    "Makefile"
]

# Các mẫu tệp tin/thư mục cần BỎ QUA khi đóng gói (tránh rác)
EXCLUDE_PATTERNS = {
    "__pycache__",
    ".pytest_cache",
    ".git",
    ".github",
    ".vscode",
    ".idea",
    ".vs",
    "bin",
    "obj",
    "target",
    ".gemini",
    "scratch",
    "node_modules",
    "vendor",
    "engine.log",
    "engine_stdout.log",
    "engine_stderr.log",
    "supportflast-hosting-package.zip",
    "release_checksums.txt"
}

EXCLUDE_EXTENSIONS = {
    ".pyc",
    ".pyo",
    ".pyd",
    ".log",
    ".tmp",
    ".bak",
    ".swp"
}

def calculate_sha256(filepath: Path) -> str:
    """Tính toán mã băm SHA-256 của một file"""
    hasher = hashlib.sha256()
    with open(filepath, "rb") as f:
        while chunk := f.read(65536):
            hasher.update(chunk)
    return hasher.hexdigest()

def format_size(size_bytes: int) -> str:
    """Định dạng byte sang KB, MB dễ đọc"""
    for unit in ['B', 'KB', 'MB', 'GB']:
        if size_bytes < 1024.0:
            return f"{size_bytes:.2f} {unit}"
        size_bytes /= 1024.0
    return f"{size_bytes:.2f} TB"

def is_excluded(path: Path) -> bool:
    """Kiểm tra đường dẫn có nằm trong danh sách loại trừ không"""
    for part in path.parts:
        if part in EXCLUDE_PATTERNS:
            return True
    if path.suffix.lower() in EXCLUDE_EXTENSIONS:
        return True
    return False

def build_package():
    start_time = time.time()
    workspace = Path(__file__).resolve().parent
    output_zip = workspace / "supportflast-hosting-package.zip"
    checksum_file = workspace / "release_checksums.txt"

    print(f"\n{CYAN}{BOLD}==========================================================================")
    print("     [PACKAGE] SUPPORTFLAST.DEV - TRINH DONG GOI UNIVERSAL RELEASE        ")
    print(f"=========================================================================={RESET}\n")
    print(f"Thu muc lam viec : {YELLOW}{workspace}{RESET}")
    print(f"Tep zip dau ra   : {GREEN}{output_zip.name}{RESET}\n")

    # 1. Kiểm tra tính toàn vẹn của các file bắt buộc
    print(f"{CYAN}>>> Buoc 1: Kiem tra tinh san sang cua cac thanh phan...{RESET}")
    missing_items = []
    for item in MANDATORY_ENTRIES:
        target = workspace / item
        if not target.exists():
            missing_items.append(item)
            print(f"  [-] {RED}Thieu: {item}{RESET}")
        else:
            if target.is_dir():
                print(f"  [+] {GREEN}[Thu muc] {item}{RESET}")
            else:
                size_str = format_size(target.stat().st_size)
                print(f"  [+] {GREEN}[Tep tin] {item:<32} ({size_str}){RESET}")

    if missing_items:
        print(f"\n{RED}{BOLD}[CANH BAO] Thieu {len(missing_items)} tep/thu muc quan trong!{RESET}")
        sys.exit(1)

    # Đảm bảo có thư mục data và storage cấu trúc chuẩn
    (workspace / "data").mkdir(exist_ok=True)
    (workspace / "storage").mkdir(exist_ok=True)
    readme_data = workspace / "data" / "README.txt"
    if not readme_data.exists():
        readme_data.write_text("Thu muc luu tru SQLite WAL Database (data/supportflast.db). Tu dong khoi tao khi chay.", encoding="utf-8")
    readme_storage = workspace / "storage" / "README.txt"
    if not readme_storage.exists():
        readme_storage.write_text("Thu muc luu tru tep tin tai len cua nguoi dung (Cloud Storage).", encoding="utf-8")

    # 2. Bắt đầu nén file ZIP
    print(f"\n{CYAN}>>> Buoc 2: Dang tao goi nen ZIP {output_zip.name}...{RESET}")
    if output_zip.exists():
        try:
            output_zip.unlink()
        except Exception as e:
            print(f"{RED}[LOI] Khong the xoa file zip cu: {e}{RESET}")
            sys.exit(1)

    total_files = 0
    total_uncompressed_size = 0

    with zipfile.ZipFile(output_zip, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as zipf:
        # Thêm các tệp tin và thư mục bắt buộc
        added_paths = set()

        def add_file_to_zip(file_path: Path, arcname: str):
            nonlocal total_files, total_uncompressed_size
            if file_path in added_paths or is_excluded(file_path):
                return
            zipf.write(file_path, arcname=arcname)
            added_paths.add(file_path)
            total_files += 1
            total_uncompressed_size += file_path.stat().st_size

        def add_dir_to_zip(dir_path: Path, base_arcname: str):
            for root, dirs, files in os.walk(dir_path):
                # Lọc bỏ thư mục loại trừ
                dirs[:] = [d for d in dirs if d not in EXCLUDE_PATTERNS]
                for file in files:
                    fp = Path(root) / file
                    rel_p = fp.relative_to(dir_path)
                    arc_p = f"{base_arcname}/{rel_p.as_posix()}"
                    add_file_to_zip(fp, arc_p)

        # Quét và thêm các mục bắt buộc
        for item in MANDATORY_ENTRIES:
            target = workspace / item
            if not target.exists():
                continue
            if target.is_file():
                add_file_to_zip(target, item)
            elif target.is_dir():
                add_dir_to_zip(target, item)

        # Thêm thư mục data/ và storage/ chuẩn
        add_dir_to_zip(workspace / "data", "data")
        add_dir_to_zip(workspace / "storage", "storage")

    # 3. Tính toán Checksums và thông số hoàn thành
    zip_size = output_zip.stat().st_size
    zip_sha256 = calculate_sha256(output_zip)
    elapsed = time.time() - start_time

    # Ghi mã SHA-256 vào tệp release_checksums.txt
    checksum_lines = [
        "# ==============================================================================",
        "# SUPPORTFLAST.DEV - RELEASE CHECKSUMS (SHA-256)",
        f"# Ngay tao: {time.strftime('%Y-%m-%d %H:%M:%S')}",
        "# ==============================================================================",
        f"{zip_sha256}  {output_zip.name}",
    ]

    for fname in ["supportflast.exe", "supportflast_linux_amd64", "supportflast_core.dll"]:
        fp = workspace / fname
        if fp.exists():
            checksum_lines.append(f"{calculate_sha256(fp)}  {fname}")

    checksum_file.write_text("\n".join(checksum_lines) + "\n", encoding="utf-8")

    # 4. Hiển thị báo cáo kết quả đẹp mắt
    print(f"\n{GREEN}{BOLD}==========================================================================")
    print("      [THANH CONG] DONG GOI PHAN PHOI HOAN TAT THANH CONG RUC RO!         ")
    print(f"=========================================================================={RESET}")
    print(f" Tep phan phoi  : {BOLD}{output_zip.name}{RESET}")
    print(f" Vi tri luu     : {output_zip.resolve()}")
    print(f" So luong tep   : {BOLD}{total_files}{RESET} tep tin")
    print(f" Dung luong goc : {format_size(total_uncompressed_size)}")
    if total_uncompressed_size > 0:
        ratio = (float(zip_size) / float(total_uncompressed_size)) * 100.0
        ratio_str = f"{ratio:.1f}%"
    else:
        ratio_str = "N/A"
    print(f" Dung luong nen : {BOLD}{format_size(zip_size)}{RESET} ({ratio_str} kich thuoc goc)")
    print(f" Thoi gian dong : {elapsed:.2f} giay")
    print(f" SHA-256 Hash   : {CYAN}{zip_sha256}{RESET}")
    print(f" File Checksum  : {checksum_file.name}")
    print(f"--------------------------------------------------------------------------")
    print(f"{YELLOW}Goi ZIP da san sang de nguoi dung tai ve va tai len bat ky hosting nao!{RESET}\n")

if __name__ == "__main__":
    build_package()
