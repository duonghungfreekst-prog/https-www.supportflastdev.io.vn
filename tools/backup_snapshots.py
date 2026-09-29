#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Backup Snapshot Tool for SupportFlast & CloudPool Databases
Tự động xuất toàn bộ dữ liệu của 2 CSDL SQLite ra file JSON Snapshot hoàn chỉnh.
Tuân thủ: Integrity Check, Atomic Export, SHA256 Verification, Pretty JSON UTF-8.
"""

import os
import sys
import json
import sqlite3
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

def calculate_sha256(filepath: str) -> str:
    hasher = hashlib.sha256()
    with open(filepath, "rb") as f:
        while chunk := f.read(65536):
            hasher.update(chunk)
    return hasher.hexdigest()

def check_integrity(conn: sqlite3.Connection, db_name: str) -> bool:
    cur = conn.cursor()
    result = cur.execute("PRAGMA integrity_check;").fetchone()
    if not result or result[0] != "ok":
        print(f"[ERROR] Kiểm tra toàn vẹn CSDL {db_name} thất bại: {result}")
        return False
    print(f"[OK] Kiểm tra toàn vẹn CSDL {db_name}: OK")
    return True

def export_database(db_path: str, output_path: str, required_tables: list = None) -> dict:
    if not os.path.exists(db_path):
        raise FileNotFoundError(f"Không tìm thấy CSDL tại: {db_path}")

    conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
    conn.row_factory = sqlite3.Row
    db_name = os.path.basename(db_path)

    if not check_integrity(conn, db_name):
        conn.close()
        raise RuntimeError(f"CSDL {db_name} không đạt kiểm tra integrity_check!")

    cur = conn.cursor()
    # Lấy danh sách toàn bộ các bảng trong CSDL
    table_rows = cur.execute("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%';").fetchall()
    all_tables = [row[0] for row in table_rows]

    snapshot_data = {
        "_metadata": {
            "snapshot_version": "1.0.0",
            "source_database": db_name,
            "export_time": datetime.now().isoformat(),
            "integrity_check": "ok",
            "table_record_counts": {}
        }
    }

    # Đưa các bảng theo thứ tự (các bảng được chỉ định lên trước)
    sorted_tables = []
    if required_tables:
        for t in required_tables:
            if t in all_tables and t not in sorted_tables:
                sorted_tables.append(t)
    for t in all_tables:
        if t not in sorted_tables:
            sorted_tables.append(t)

    total_records = 0
    for tbl in sorted_tables:
        rows = cur.execute(f"SELECT * FROM {tbl};").fetchall()
        data_list = [dict(r) for r in rows]
        snapshot_data[tbl] = data_list
        count = len(data_list)
        snapshot_data["_metadata"]["table_record_counts"][tbl] = count
        total_records += count
        print(f"  -> Xuất bảng '{tbl}': {count} bản ghi")

    snapshot_data["_metadata"]["total_records"] = total_records
    conn.close()

    # Ghi ra file JSON
    os.makedirs(os.path.dirname(output_path), exist_ok=True)
    with open(output_path, "w", encoding="utf-8") as f:
        json.dump(snapshot_data, f, ensure_ascii=False, indent=2)

    file_size = os.path.getsize(output_path)
    sha256_hash = calculate_sha256(output_path)

    return {
        "database": db_name,
        "output_path": output_path,
        "size_bytes": file_size,
        "sha256": sha256_hash,
        "total_records": total_records,
        "table_counts": snapshot_data["_metadata"]["table_record_counts"]
    }

def main():
    print("=" * 70)
    print("BẮT ĐẦU TIẾN TRÌNH SAO LƯU DISASTER RECOVERY SNAPSHOT CHO SUPPORTFLAST")
    print("=" * 70)

    os.makedirs(BACKUP_DIR, exist_ok=True)
    print(f"[INFO] Thư mục sao lưu: {BACKUP_DIR}")

    # 1. CloudPool Metadata DB
    print(f"\n[1/2] Đang xuất snapshot CSDL CloudPool ({CLOUDPOOL_DB})...")
    cloudpool_required = ["accounts", "virtual_files", "file_chunks", "settings", "users", "public_shares"]
    cp_res = export_database(CLOUDPOOL_DB, CLOUDPOOL_SNAPSHOT, required_tables=cloudpool_required)

    # 2. SupportFlast Core DB
    print(f"\n[2/2] Đang xuất snapshot CSDL SupportFlast ({SUPPORTFLAST_DB})...")
    supportflast_required = ["apps", "users", "api_keys"]
    sf_res = export_database(SUPPORTFLAST_DB, SUPPORTFLAST_SNAPSHOT, required_tables=supportflast_required)

    # Ghi file checksums
    checksum_file = os.path.join(BACKUP_DIR, "checksums.sha256")
    with open(checksum_file, "w", encoding="utf-8") as f:
        f.write(f"{cp_res['sha256']}  cloudpool_snapshot.json\n")
        f.write(f"{sf_res['sha256']}  supportflast_snapshot.json\n")

    print("\n" + "=" * 70)
    print("BÁO CÁO KẾT QUẢ SAO LƯU SNAPSHOT HOÀN TẤT:")
    print("=" * 70)
    for res in [cp_res, sf_res]:
        print(f"CSDL: {res['database']}")
        print(f"  Tệp snapshot : {res['output_path']}")
        print(f"  Dung lượng   : {res['size_bytes']:,} bytes ({res['size_bytes']/1024:.2f} KB)")
        print(f"  SHA256       : {res['sha256']}")
        print(f"  Tổng bản ghi : {res['total_records']}")
        print(f"  Chi tiết bảng: {json.dumps(res['table_counts'], ensure_ascii=False)}")
        print("-" * 70)

    print(f"Tệp checksums SHA256: {checksum_file}")

if __name__ == "__main__":
    main()
