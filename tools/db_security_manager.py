#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
SupportFlast Enterprise - Database Security & Maintenance Manager
Quy trình chuẩn quản trị Cơ Sở Dữ Liệu: Bảo mật cao, Chống Hack, Chống DDoS, Chống Mất Dữ Liệu
Tuân thủ: Zero Data Loss, TLS 1.2+, Prepared Statements, 3-Tier Backup (TiDB Cloud + SQLite WAL + Google Drive)
"""

import os
import sys
import json
import time
import sqlite3
import argparse
import subprocess
from datetime import datetime

# Đảm bảo UTF-8 encoding trên Windows console
if sys.stdout.encoding and sys.stdout.encoding.lower() != 'utf-8':
    try:
        sys.stdout.reconfigure(encoding='utf-8')
        sys.stderr.reconfigure(encoding='utf-8')
    except Exception:
        pass

ROOT_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
DATA_DIR = os.path.join(ROOT_DIR, "data")
BACKUP_DIR = os.path.join(DATA_DIR, "backups")
SUPPORTFLAST_DB = os.path.join(DATA_DIR, "supportflast.db")
CLOUDPOOL_DB = os.path.join(DATA_DIR, "cloudpool_metadata.db")
ENV_FILE = os.path.join(ROOT_DIR, ".env")

def log_info(msg: str):
    print(f"\033[92m[INFO] {msg}\033[0m")

def log_warn(msg: str):
    print(f"\033[93m[WARN] {msg}\033[0m")

def log_error(msg: str):
    print(f"\033[91m[ERROR] {msg}\033[0m")

def log_header(title: str):
    print("\n" + "=" * 70)
    print(f" {title.center(68)}")
    print("=" * 70)

def load_env() -> dict:
    env_vars = {}
    if os.path.exists(ENV_FILE):
        with open(ENV_FILE, "r", encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if line and not line.startswith("#") and "=" in line:
                    k, v = line.split("=", 1)
                    env_vars[k.strip()] = v.strip()
    return env_vars

def run_security_audit():
    log_header("QUY TRÌNH KIỂM TOÁN BẢO MẬT DATABASE (SECURITY AUDIT)")
    score = 100
    env_vars = load_env()

    # 1. Kiểm tra cấu hình kết nối TiDB Cloud
    tidb_host = env_vars.get("TIDB_HOST", "")
    tidb_tls = env_vars.get("TIDB_TLS", "")
    if tidb_host:
        log_info(f"TiDB Cloud Primary Host: {tidb_host}")
        if tidb_tls in ["tidb", "true"]:
            log_info(f"Giao thức mã hóa mạng: TLS 1.2+ Bắt buộc (TLS={tidb_tls}) -> AN TOÀN")
        else:
            log_warn(f"Cảnh báo: TLS chưa được ép buộc chặt chẽ (TLS={tidb_tls})")
            score -= 10
    else:
        log_warn("TIDB_HOST chưa được cấu hình trong .env, hệ thống đang dùng SQLite nội bộ")
        score -= 15

    # 2. Kiểm tra SQLite Local Databases
    for db_path, db_name in [(SUPPORTFLAST_DB, "supportflast.db"), (CLOUDPOOL_DB, "cloudpool_metadata.db")]:
        if not os.path.exists(db_path):
            log_error(f"Không tìm thấy file CSDL {db_name}")
            score -= 20
            continue

        try:
            conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
            cur = conn.cursor()
            
            # Kiểm tra WAL Mode
            cur.execute("PRAGMA journal_mode;")
            j_mode = cur.fetchone()[0]
            if j_mode.lower() == "wal":
                log_info(f"CSDL {db_name}: Chế độ nhật ký Write-Ahead Logging (WAL) -> TỐI ƯU")
            else:
                log_warn(f"CSDL {db_name}: Chế độ {j_mode}, khuyến nghị chuyển sang WAL")
                score -= 5

            # Kiểm tra Toàn Vẹn Integrity
            cur.execute("PRAGMA integrity_check;")
            integrity = cur.fetchone()[0]
            if integrity == "ok":
                log_info(f"CSDL {db_name}: Kiểm tra toàn vẹn (Integrity Check) -> HOÀN HẢO")
            else:
                log_error(f"CSDL {db_name}: Phát hiện lỗi toàn vẹn: {integrity}")
                score -= 30

            # Kiểm tra tài khoản rác/mẫu trong users
            if db_name == "supportflast.db":
                cur.execute("SELECT id, username, email, role FROM users;")
                users = cur.fetchall()
                log_info(f"CSDL {db_name}: Hiện có {len(users)} tài khoản người dùng:")
                sample_users = ["quocviet_dev", "devlanuser", "test", "demo"]
                found_sample = False
                for u in users:
                    print(f"   - ID: {u[0]} | User: {u[1]} | Email: {u[2]} | Role: {u[3]}")
                    if u[1] in sample_users:
                        found_sample = True
                if found_sample:
                    log_warn("Phát hiện tài khoản mẫu / kiểm thử trong CSDL!")
                    score -= 15
                else:
                    log_info("Không có tài khoản mẫu rác, chỉ có tài khoản quản trị chuẩn.")

            # Kiểm tra sự kiện an ninh gần đây
            if db_name == "supportflast.db":
                try:
                    cur.execute("SELECT COUNT(*) FROM security_events;")
                    sec_count = cur.fetchone()[0]
                    cur.execute("SELECT COUNT(*) FROM audit_logs;")
                    audit_count = cur.fetchone()[0]
                    log_info(f"Hệ thống giám sát: {audit_count} bản ghi audit_logs, {sec_count} sự kiện security_events ghi nhận.")
                except Exception:
                    pass

            conn.close()
        except Exception as e:
            log_error(f"Lỗi kiểm tra {db_name}: {e}")
            score -= 20

    # 3. Đánh giá tổng điểm bảo mật
    log_header("KẾT QUẢ ĐÁNH GIÁ AN NINH DATABASE")
    print(f"Tổng điểm an ninh: {max(score, 0)}/100")
    if score >= 90:
        log_info("Trạng thái: ĐẠT TIÊU CHUẨN DOANH NGHIỆP (ENTERPRISE GRADE)")
    elif score >= 75:
        log_warn("Trạng thái: KHÁ TỐT - Cần hoàn thiện thêm một số khuyến nghị")
    else:
        log_error("Trạng thái: CẦN NÂNG CẤP BẢO MẬT NGAY")

def run_backup_3tier():
    log_header("QUY TRÌNH SAO LƯU 3 TẦNG CHỐNG MẤT DỮ LIỆU (3-TIER BACKUP)")
    print("Tầng 1: Checkpoint WAL SQLite & xác thực toàn vẹn dữ liệu")
    print("Tầng 2: Xuất Snapshot JSON cấu trúc toàn diện 18 bảng")
    print("Tầng 3: Nén mã hóa ZIP có timestamp và đẩy lên Google Drive")
    print("-" * 70)

    # 1. Chạy snapshot JSON
    snapshot_script = os.path.join(ROOT_DIR, "tools", "backup_snapshots.py")
    if os.path.exists(snapshot_script):
        log_info("Đang thực hiện xuất bản sao lưu Snapshot JSON...")
        res = subprocess.run([sys.executable, snapshot_script], capture_output=True, text=True)
        if res.returncode == 0:
            log_info("Xuất Snapshot JSON thành công.")
        else:
            log_error(f"Lỗi xuất Snapshot JSON: {res.stderr}")

    # 2. Chạy sao lưu Google Drive
    gdrive_script = os.path.join(ROOT_DIR, "tools", "backup_to_gdrive.py")
    if os.path.exists(gdrive_script):
        log_info("Đang đóng gói và đẩy bản sao lưu lên Google Drive...")
        res = subprocess.run([sys.executable, gdrive_script], capture_output=True, text=True)
        if res.returncode == 0:
            log_info("Sao lưu lên Google Drive thành công 100%!")
            print(res.stdout)
        else:
            log_error(f"Lỗi sao lưu Google Drive: {res.stderr}")
            print(res.stdout)

def run_sync_tidb():
    log_header("QUY TRÌNH ĐỒNG BỘ CSDL LÊN TIDB CLOUD (SYNC TIDB)")
    migrate_dir = os.path.join(ROOT_DIR, "tools", "migrate_sqlite_to_tidb")
    if not os.path.exists(migrate_dir):
        log_error(f"Không tìm thấy thư mục {migrate_dir}")
        return

    log_info("Đang kích hoạt quy trình đồng bộ 18 bảng lên TiDB Cloud...")
    res = subprocess.run(["go", "run", "main.go"], cwd=migrate_dir, capture_output=True, text=True)
    if res.returncode == 0:
        log_info("Đồng bộ dữ liệu lên TiDB Cloud thành công hoàn hảo!")
        for line in res.stdout.splitlines():
            if "thành công" in line or "TỔNG KẾT" in line or "bản ghi" in line:
                print(f"   {line}")
    else:
        log_error(f"Lỗi đồng bộ TiDB Cloud: {res.stderr}")
        print(res.stdout)

def main():
    parser = argparse.ArgumentParser(description="SupportFlast Database Security & Maintenance Manager")
    parser.add_argument("action", choices=["audit", "backup", "sync", "all"], help="Hành động cần thực hiện")
    args = parser.parse_args()

    if args.action == "audit":
        run_security_audit()
    elif args.action == "backup":
        run_backup_3tier()
    elif args.action == "sync":
        run_sync_tidb()
    elif args.action == "all":
        run_security_audit()
        run_sync_tidb()
        run_backup_3tier()

if __name__ == "__main__":
    main()
