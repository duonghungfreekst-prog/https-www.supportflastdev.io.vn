#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Restore Snapshot Tool for SupportFlast & CloudPool Databases
Khôi phục dữ liệu 1-click từ bản sao lưu JSON Snapshot vào CSDL SQLite.
Tính năng:
- Tự động sao lưu file hiện tại thành .bak dự phòng
- Tự động khởi tạo cấu trúc bảng & index nếu CSDL chưa tồn tại
- Import dữ liệu an toàn với Transaction & Foreign Keys Control
- Kiểm tra toàn vẹn PRAGMA integrity_check sau khi khôi phục
"""

import os
import sys
import json
import sqlite3
import shutil
import hashlib
from datetime import datetime

# Đảm bảo UTF-8 encoding trên Windows console
if sys.stdout.encoding.lower() != 'utf-8':
    try:
        sys.stdout.reconfigure(encoding='utf-8')
        sys.stderr.reconfigure(encoding='utf-8')
    except Exception:
        pass

ROOT_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
DATA_DIR = os.path.join(ROOT_DIR, "data")
BACKUP_DIR = os.path.join(DATA_DIR, "backups")

CLOUDPOOL_DB = os.path.join(DATA_DIR, "cloudpool_metadata.db")
SUPPORTFLAST_DB = os.path.join(DATA_DIR, "supportflast.db")

CLOUDPOOL_SNAPSHOT = os.path.join(BACKUP_DIR, "cloudpool_snapshot.json")
SUPPORTFLAST_SNAPSHOT = os.path.join(BACKUP_DIR, "supportflast_snapshot.json")

# Schema DDL cho CloudPool
CLOUDPOOL_SCHEMAS = """
CREATE TABLE IF NOT EXISTS accounts (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    name TEXT,
    avatar_url TEXT,
    auth_type TEXT NOT NULL,
    credentials_json TEXT,
    token_json TEXT,
    root_folder_id TEXT,
    total_quota_bytes INTEGER DEFAULT 0,
    used_quota_bytes INTEGER DEFAULT 0,
    free_quota_bytes INTEGER DEFAULT 0,
    status TEXT DEFAULT 'active',
    last_error TEXT,
    created_at DATETIME,
    updated_at DATETIME,
    email_hash TEXT DEFAULT '',
    name_hash TEXT DEFAULT ''
);

CREATE TABLE IF NOT EXISTS virtual_files (
    id TEXT PRIMARY KEY,
    parent_id TEXT DEFAULT '',
    name TEXT NOT NULL,
    path TEXT NOT NULL UNIQUE,
    is_dir BOOLEAN DEFAULT 0,
    size_bytes INTEGER DEFAULT 0,
    mime_type TEXT,
    sha256 TEXT,
    chunk_count INTEGER DEFAULT 0,
    is_encrypted BOOLEAN DEFAULT 1,
    created_at DATETIME,
    updated_at DATETIME,
    user_id TEXT DEFAULT 'user_admin',
    is_deleted BOOLEAN DEFAULT 0,
    deleted_at DATETIME
);

CREATE TABLE IF NOT EXISTS file_chunks (
    chunk_id TEXT PRIMARY KEY,
    file_id TEXT NOT NULL,
    chunk_index INTEGER NOT NULL,
    account_id TEXT NOT NULL,
    gdrive_file_id TEXT NOT NULL,
    chunk_size_bytes INTEGER DEFAULT 0,
    encrypted_size_bytes INTEGER DEFAULT 0,
    sha256 TEXT,
    status TEXT DEFAULT 'uploaded',
    ref_count INTEGER DEFAULT 1,
    FOREIGN KEY(file_id) REFERENCES virtual_files(id) ON DELETE CASCADE,
    FOREIGN KEY(account_id) REFERENCES accounts(id)
);

CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT
);

CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    display_name TEXT,
    role TEXT DEFAULT 'user',
    quota_bytes INTEGER DEFAULT 0,
    used_bytes INTEGER DEFAULT 0,
    created_at DATETIME,
    updated_at DATETIME,
    email TEXT DEFAULT '',
    security_pin_hash TEXT DEFAULT '',
    security_tier INTEGER DEFAULT 1,
    avatar_url TEXT DEFAULT '',
    status TEXT DEFAULT 'active',
    failed_login_count INTEGER DEFAULT 0,
    locked_until DATETIME,
    last_login_at DATETIME,
    username_hash TEXT DEFAULT '',
    email_hash TEXT DEFAULT ''
);

CREATE TABLE IF NOT EXISTS activity_logs (
    id TEXT PRIMARY KEY,
    user_id TEXT,
    username TEXT,
    action TEXT,
    target TEXT,
    ip_address TEXT,
    details TEXT,
    created_at DATETIME
);

CREATE TABLE IF NOT EXISTS login_sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT,
    username TEXT,
    ip_address TEXT,
    device_info TEXT,
    location_info TEXT,
    status TEXT,
    user_agent TEXT,
    created_at DATETIME
);

CREATE TABLE IF NOT EXISTS file_access_otps (
    id TEXT PRIMARY KEY,
    file_id TEXT NOT NULL,
    file_name TEXT NOT NULL,
    target_user_id TEXT NOT NULL DEFAULT 'all',
    otp_code TEXT NOT NULL,
    created_by TEXT NOT NULL DEFAULT 'user_admin',
    is_used INTEGER DEFAULT 0,
    used_by TEXT DEFAULT '',
    used_at DATETIME,
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS file_access_requests (
    id TEXT PRIMARY KEY,
    file_id TEXT NOT NULL,
    file_name TEXT NOT NULL,
    user_id TEXT NOT NULL,
    username TEXT NOT NULL,
    user_display_name TEXT NOT NULL,
    status TEXT DEFAULT 'pending',
    otp_code TEXT DEFAULT '',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS public_shares (
    id TEXT PRIMARY KEY,
    file_id TEXT NOT NULL,
    created_by TEXT NOT NULL DEFAULT 'user_admin',
    password_hash TEXT DEFAULT '',
    max_downloads INTEGER DEFAULT 0,
    download_count INTEGER DEFAULT 0,
    expires_at DATETIME,
    created_at DATETIME NOT NULL,
    is_active INTEGER DEFAULT 1
);

CREATE INDEX IF NOT EXISTS idx_vfiles_parent ON virtual_files(parent_id);
CREATE INDEX IF NOT EXISTS idx_vfiles_path ON virtual_files(path);
CREATE INDEX IF NOT EXISTS idx_chunks_file ON file_chunks(file_id, chunk_index);
CREATE INDEX IF NOT EXISTS idx_vfiles_user ON virtual_files(user_id);
CREATE INDEX IF NOT EXISTS idx_logs_user ON activity_logs(user_id);
CREATE INDEX IF NOT EXISTS idx_logs_created ON activity_logs(created_at);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON login_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_created ON login_sessions(created_at);
CREATE INDEX IF NOT EXISTS idx_file_otps ON file_access_otps(file_id, otp_code, is_used);
CREATE INDEX IF NOT EXISTS idx_access_req_user ON file_access_requests(user_id, status);
CREATE INDEX IF NOT EXISTS idx_public_shares ON public_shares(id, is_active);
CREATE INDEX IF NOT EXISTS idx_vfiles_deleted ON virtual_files(is_deleted);
CREATE INDEX IF NOT EXISTS idx_chunks_sha256 ON file_chunks(sha256, status);
"""

# Schema DDL cho SupportFlast
SUPPORTFLAST_SCHEMAS = """
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    email TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    display_name TEXT,
    role TEXT NOT NULL DEFAULT 'user',
    avatar TEXT,
    created_at TEXT,
    updated_at TEXT,
    last_login TEXT
);

CREATE TABLE IF NOT EXISTS apps (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    version TEXT NOT NULL,
    platform TEXT,
    category TEXT,
    desc TEXT,
    file_name TEXT,
    size_bytes INTEGER,
    size_formatted TEXT,
    sha256 TEXT,
    author TEXT,
    downloads INTEGER DEFAULT 0,
    status TEXT DEFAULT 'published',
    published_at TEXT,
    download_url TEXT,
    video_url TEXT,
    guide TEXT,
    user_id TEXT,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS api_keys (
    id TEXT PRIMARY KEY,
    user_id TEXT,
    name TEXT,
    key_hash TEXT UNIQUE,
    prefix TEXT,
    status TEXT DEFAULT 'active',
    permissions TEXT,
    created_at TEXT,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS reviews (
    id TEXT PRIMARY KEY,
    app_id TEXT,
    user_id TEXT,
    author_name TEXT NOT NULL,
    author_role TEXT,
    stars INTEGER NOT NULL,
    text TEXT NOT NULL,
    status TEXT DEFAULT 'approved',
    created_at TEXT,
    FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS audit_logs (
    id TEXT PRIMARY KEY,
    user_id TEXT,
    action TEXT NOT NULL,
    ip_address TEXT,
    user_agent TEXT,
    details TEXT,
    created_at TEXT,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS system_releases (
    version TEXT PRIMARY KEY,
    title TEXT,
    date TEXT,
    build_hash TEXT,
    notes TEXT,
    published_by TEXT
);

CREATE TABLE IF NOT EXISTS security_events (
    id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    ip_address TEXT NOT NULL,
    severity TEXT NOT NULL DEFAULT 'warning',
    details TEXT,
    blocked_until TEXT,
    created_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
CREATE INDEX IF NOT EXISTS idx_users_role ON users(role);
CREATE INDEX IF NOT EXISTS idx_users_created_at ON users(created_at);
CREATE INDEX IF NOT EXISTS idx_apps_user_id ON apps(user_id);
CREATE INDEX IF NOT EXISTS idx_apps_status ON apps(status);
CREATE INDEX IF NOT EXISTS idx_apps_category ON apps(category);
CREATE INDEX IF NOT EXISTS idx_apps_platform ON apps(platform);
CREATE INDEX IF NOT EXISTS idx_apps_published_at ON apps(published_at);
CREATE INDEX IF NOT EXISTS idx_apps_downloads ON apps(downloads DESC);
CREATE INDEX IF NOT EXISTS idx_api_keys_user_id ON api_keys(user_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_key_hash ON api_keys(key_hash);
CREATE INDEX IF NOT EXISTS idx_api_keys_prefix ON api_keys(prefix);
CREATE INDEX IF NOT EXISTS idx_api_keys_status ON api_keys(status);
CREATE INDEX IF NOT EXISTS idx_reviews_app_id ON reviews(app_id);
CREATE INDEX IF NOT EXISTS idx_reviews_user_id ON reviews(user_id);
CREATE INDEX IF NOT EXISTS idx_reviews_status ON reviews(status);
CREATE INDEX IF NOT EXISTS idx_reviews_created_at ON reviews(created_at);
CREATE INDEX IF NOT EXISTS idx_audit_logs_user_id ON audit_logs(user_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_action ON audit_logs(action);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_system_releases_date ON system_releases(date DESC);
CREATE INDEX IF NOT EXISTS idx_security_events_ip ON security_events(ip_address);
CREATE INDEX IF NOT EXISTS idx_security_events_type ON security_events(event_type);
CREATE INDEX IF NOT EXISTS idx_security_events_created_at ON security_events(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_security_events_blocked_until ON security_events(blocked_until);
"""

def restore_database(snapshot_path: str, db_path: str, init_schema_sql: str) -> dict:
    if not os.path.exists(snapshot_path):
        raise FileNotFoundError(f"Không tìm thấy tệp snapshot tại: {snapshot_path}")

    print(f"\n[RESTORE] Đang đọc tệp snapshot: {snapshot_path}")
    with open(snapshot_path, "r", encoding="utf-8") as f:
        snapshot = json.load(f)

    # Đảm bảo thư mục đích tồn tại
    os.makedirs(os.path.dirname(db_path), exist_ok=True)

    # Tạo backup tạm thời (.bak) của file DB hiện tại nếu có
    bak_path = f"{db_path}.bak"
    if os.path.exists(db_path):
        shutil.copy2(db_path, bak_path)
        print(f"  -> Đã tạo bản sao lưu dự phòng: {bak_path}")

    conn = sqlite3.connect(db_path)
    cur = conn.cursor()

    try:
        # Tắt foreign keys tạm thời để chèn dữ liệu không bị xung đột thứ tự bảng
        cur.execute("PRAGMA foreign_keys = OFF;")
        # Khởi tạo schema nếu cần
        cur.executescript(init_schema_sql)

        # Lấy danh sách bảng trong DB
        db_tables = [row[0] for row in cur.execute("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%';").fetchall()]

        restored_counts = {}
        # Bắt đầu transaction
        cur.execute("BEGIN TRANSACTION;")

        for key, records in snapshot.items():
            if key.startswith("_") or key not in db_tables:
                continue

            # Xóa sạch dữ liệu cũ của bảng này
            cur.execute(f"DELETE FROM {key};")

            if not records:
                restored_counts[key] = 0
                continue

            # Lấy danh sách cột thực tế của bảng
            cols_info = cur.execute(f"PRAGMA table_info({key});").fetchall()
            valid_cols = set(c[1] for c in cols_info)

            # Chuẩn bị câu lệnh chèn
            sample_record = records[0]
            insert_cols = [c for c in sample_record.keys() if c in valid_cols]
            col_names = ", ".join(insert_cols)
            placeholders = ", ".join([f":{c}" for c in insert_cols])
            insert_sql = f"INSERT INTO {key} ({col_names}) VALUES ({placeholders})"

            cur.executemany(insert_sql, records)
            restored_counts[key] = len(records)
            print(f"  -> Đã khôi phục bảng '{key}': {len(records)} bản ghi")

        conn.commit()
        cur.execute("PRAGMA foreign_keys = ON;")

        # Kiểm tra tính toàn vẹn
        integrity = cur.execute("PRAGMA integrity_check;").fetchone()
        if not integrity or integrity[0] != "ok":
            raise RuntimeError(f"Lỗi toàn vẹn CSDL sau khi khôi phục: {integrity}")

        print(f"  -> Kiểm tra tính toàn vẹn (PRAGMA integrity_check): {integrity[0].upper()}")

        # Xóa file .bak sau khi verify thành công theo quy tắc workspace
        if os.path.exists(bak_path):
            os.remove(bak_path)
            print(f"  -> Đã dọn dẹp file dự phòng: {bak_path}")

        return restored_counts

    except Exception as e:
        conn.rollback()
        conn.close()
        # Khôi phục lại từ file .bak nếu xảy ra sự cố
        if os.path.exists(bak_path):
            shutil.move(bak_path, db_path)
            print(f"  [ROLLBACK] Đã hoàn nguyên CSDL từ file dự phòng: {bak_path}")
        raise e
    finally:
        conn.close()

def main():
    print("=" * 70)
    print("BẮT ĐẦU TIẾN TRÌNH KHÔI PHỤC DISASTER RECOVERY TỪ BẢN SNAPSHOT")
    print("=" * 70)

    # 1. Khôi phục CloudPool
    print("\n[1/2] Đang khôi phục CSDL CloudPool...")
    cp_counts = restore_database(CLOUDPOOL_SNAPSHOT, CLOUDPOOL_DB, CLOUDPOOL_SCHEMAS)

    # 2. Khôi phục SupportFlast
    print("\n[2/2] Đang khôi phục CSDL SupportFlast...")
    sf_counts = restore_database(SUPPORTFLAST_SNAPSHOT, SUPPORTFLAST_DB, SUPPORTFLAST_SCHEMAS)

    print("\n" + "=" * 70)
    print("KẾT QUẢ KHÔI PHỤC HOÀN TẤT VÀ XÁC MINH TOÀN VẸN THÀNH CÔNG:")
    print("=" * 70)
    print("CloudPool:")
    for tbl, cnt in cp_counts.items():
        print(f"  - {tbl}: {cnt} bản ghi")
    print("\nSupportFlast:")
    for tbl, cnt in sf_counts.items():
        print(f"  - {tbl}: {cnt} bản ghi")
    print("=" * 70)

if __name__ == "__main__":
    main()
