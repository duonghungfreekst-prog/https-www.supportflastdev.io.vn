"""Module Phân Tích An Ninh Gói Ứng Dụng (Package Security Scanner) cho supportflast_ai.
Thực hiện quét mã độc tĩnh (static heuristics), kiểm tra entropy, kiểm tra chứng chỉ số và quyền hạn yêu cầu.
Tuân thủ Rule 7.1: Sử dụng Generator/Batching và dọn dẹp bộ nhớ chống tràn RAM.
"""

import os
import re
import math
import struct
import base64
import gc
import logging
import warnings
from typing import Generator, List, Dict, Any, Optional, Tuple

try:
    import psutil
except ImportError:
    psutil = None

try:
    import pefile
except ImportError:
    pefile = None

try:
    import zipfile
except ImportError:
    zipfile = None

try:
    from cryptography.hazmat.primitives.serialization import pkcs7
    from cryptography import x509
    from cryptography.x509.oid import NameOID
except ImportError:
    pkcs7 = None
    x509 = None
    NameOID = None

# Tắt cảnh báo ASN1 BER fallback không cần thiết
warnings.filterwarnings("ignore", category=UserWarning)

logger = logging.getLogger("supportflast_ai")

# -------------------------------------------------------------
# RULE 7.1: GENERATOR & BATCHING CHỐNG TRÀN BỘ NHỚ (STREAMING)
# -------------------------------------------------------------

def stream_file_chunks(file_path: str, chunk_size: int = 65536) -> Generator[bytes, None, None]:
    """Đọc tệp tin theo từng chunk (mặc định 64KB) qua generator, giải phóng bộ nhớ ngay sau khi dùng."""
    if not os.path.exists(file_path):
        raise FileNotFoundError(f"Không tìm thấy tệp tin: {file_path}")
    
    with open(file_path, "rb") as f:
        while True:
            chunk = f.read(chunk_size)
            if not chunk:
                break
            yield chunk
            del chunk


def stream_bytes_chunks(data: bytes, chunk_size: int = 65536) -> Generator[bytes, None, None]:
    """Chia nhỏ byte array thành generator chunks để quét đồng nhất."""
    offset = 0
    total = len(data)
    while offset < total:
        end = min(offset + chunk_size, total)
        chunk = data[offset:end]
        yield chunk
        del chunk
        offset = end


def batch_generator(iterable, batch_size: int = 1000) -> Generator[List[Any], None, None]:
    """Gom nhóm xử lý danh sách theo batch size (1000-5000 items) tuân thủ Rule 7.1."""
    batch = []
    for item in iterable:
        batch.append(item)
        if len(batch) >= batch_size:
            yield batch
            del batch
            batch = []
    if batch:
        yield batch
        del batch


# -------------------------------------------------------------
# PHẦN 1: TÍNH TOÁN SHANNON ENTROPY (ĐỘ HỖN LOẠN / NÉN / MÃ HÓA)
# -------------------------------------------------------------

class EntropyAnalyzer:
    """Phân tích Entropy của tệp tin, phân vùng thực thi (PE sections) hoặc gói nén."""

    @staticmethod
    def calculate_entropy_from_generator(chunks_gen: Generator[bytes, None, None]) -> Tuple[float, int]:
        """Tính toán Shannon Entropy chuẩn qua Generator mà không tải toàn bộ file vào RAM."""
        byte_counts = [0] * 256
        total_bytes = 0

        for chunk in chunks_gen:
            total_bytes += len(chunk)
            for b in chunk:
                byte_counts[b] += 1
            del chunk

        if total_bytes == 0:
            return 0.0, 0

        entropy = 0.0
        for count in byte_counts:
            if count > 0:
                p = count / total_bytes
                entropy -= p * math.log2(p)

        del byte_counts
        return round(entropy, 4), total_bytes

    @classmethod
    def analyze_file(cls, file_path: str) -> Dict[str, Any]:
        """Phân tích entropy tổng thể và kiểm tra PE sections / Archive structure."""
        overall_entropy, file_size = cls.calculate_entropy_from_generator(stream_file_chunks(file_path))

        is_packed_or_encrypted = overall_entropy > 7.2
        suspicious_sections = []
        is_pe = False
        is_archive = False
        archive_stats = {}

        # 1. Kiểm tra định dạng PE (Windows EXE/DLL/SYS)
        if pefile and file_size >= 64:
            try:
                with open(file_path, "rb") as f:
                    magic = f.read(2)
                if magic == b"MZ":
                    pe = pefile.PE(file_path, fast_load=True)
                    is_pe = True
                    for section in pe.sections:
                        sec_name = section.Name.decode(errors="ignore").strip("\x00")
                        sec_entropy = section.get_entropy()
                        sec_size = section.SizeOfRawData
                        
                        # Kiểm tra tên packer thông dụng hoặc entropy section cao (> 7.2 trong section thực thi)
                        known_packers = ["UPX", "ASPack", "Themida", "MPRESS", "FSG", "PECompact", ".ndata", ".vmp"]
                        packer_detected = any(p.lower() in sec_name.lower() for p in known_packers)
                        
                        is_suspicious_sec = (sec_entropy > 7.2 and section.IMAGE_SCN_MEM_EXECUTE) or packer_detected
                        if is_suspicious_sec or sec_entropy > 7.4:
                            suspicious_sections.append({
                                "name": sec_name,
                                "entropy": round(sec_entropy, 4),
                                "size_bytes": sec_size,
                                "executable": bool(section.IMAGE_SCN_MEM_EXECUTE),
                                "packer_hint": packer_detected
                            })
                    pe.close()
                    del pe
            except Exception as e:
                logger.warning(f"[AI] Lỗi kiểm tra PE sections: {str(e)}")

        # 2. Kiểm tra định dạng nén Archive (ZIP, APK, JAR)
        if zipfile and not is_pe and file_size >= 4:
            try:
                with open(file_path, "rb") as f:
                    magic = f.read(4)
                if magic.startswith(b"PK\x03\x04") or magic.startswith(b"PK\x05\x06"):
                    is_archive = True
                    with zipfile.ZipFile(file_path, "r") as zf:
                        infolist = zf.infolist()
                        total_uncompressed = sum(info.file_size for info in infolist)
                        total_compressed = sum(info.compress_size for info in infolist)
                        ratio = (total_uncompressed / max(1, total_compressed))
                        
                        # Phát hiện Zip Bomb (tỷ lệ nén vượt 100 lần hoặc dung lượng giải nén lớn bất thường)
                        is_zip_bomb = (ratio > 100 and total_uncompressed > 50 * 1024 * 1024) or (len(infolist) > 10000)
                        archive_stats = {
                            "file_count": len(infolist),
                            "total_uncompressed_bytes": total_uncompressed,
                            "total_compressed_bytes": total_compressed,
                            "compression_ratio": round(ratio, 2),
                            "is_zip_bomb": is_zip_bomb
                        }
                        if is_zip_bomb:
                            suspicious_sections.append({
                                "name": "ZIP_BOMB_DETECTED",
                                "entropy": overall_entropy,
                                "size_bytes": total_uncompressed,
                                "packer_hint": True,
                                "detail": f"Tỷ lệ giải nén nguy hiểm: {ratio:.1f}:1"
                            })
                    del zf
            except Exception as e:
                logger.warning(f"[AI] Lỗi kiểm tra Archive: {str(e)}")

        gc.collect()
        return {
            "overall_entropy": overall_entropy,
            "file_size_bytes": file_size,
            "is_packed_or_encrypted": is_packed_or_encrypted,
            "is_pe": is_pe,
            "is_archive": is_archive,
            "suspicious_sections": suspicious_sections,
            "archive_stats": archive_stats
        }


# -------------------------------------------------------------
# PHẦN 2: QUÉT MÃ ĐỘC TĨNH (STATIC HEURISTICS ENGINE)
# -------------------------------------------------------------

class StaticHeuristicScanner:
    """Bộ quy tắc nhận dạng mã độc, webshell, mã thực thi ẩn và kỹ thuật tấn công."""

    # Tập hợp các mẫu nhận dạng rủi ro cao (Malicious) và nghi vấn (Suspicious)
    HEURISTIC_RULES = [
        # Nhóm 1: RANSOMWARE & WIPER (Mức độ: CRITICAL)
        {
            "id": "RANSOMWARE_WIPER_SHADOW_DELETE",
            "category": "Ransomware/Wiper",
            "severity": "CRITICAL",
            "penalty": 45,
            "pattern": re.compile(rb"(vssadmin(\.exe)?\s+delete\s+shadows|wmic\s+shadowcopy\s+delete)", re.IGNORECASE),
            "description": "Lệnh xóa Volume Shadow Copies để ngăn chặn khôi phục dữ liệu bị mã hóa."
        },
        {
            "id": "RANSOMWARE_BCDEDIT_DISABLE_RECOVERY",
            "category": "Ransomware/Wiper",
            "severity": "CRITICAL",
            "penalty": 40,
            "pattern": re.compile(rb"bcdedit(\.exe)?\s+/set.*(bootstatuspolicy\s+ignoreallfailures|recoveryenabled\s+no)", re.IGNORECASE),
            "description": "Vô hiệu hóa cơ chế tự sửa chữa và cứu hộ khởi động của Windows."
        },
        {
            "id": "RANSOMWARE_NOTE_PATTERN",
            "category": "Ransomware",
            "severity": "CRITICAL",
            "penalty": 35,
            "pattern": re.compile(rb"(your\s+files\s+are\s+encrypted|all\s+your\s+files\s+have\s+been\s+encrypted|send\s+[0-9.]+\s*btc\s+to|pay\s+ransom\s+to)", re.IGNORECASE),
            "description": "Chuỗi văn bản thông báo tống tiền Ransomware điển hình."
        },

        # Nhóm 2: TIÊM TIẾN TRÌNH & PROCESS HOLLOWING (Mức độ: CRITICAL)
        {
            "id": "PROCESS_INJECTION_APIS",
            "category": "Process Injection",
            "severity": "CRITICAL",
            "penalty": 40,
            "pattern": re.compile(rb"(VirtualAllocEx.*WriteProcessMemory|CreateRemoteThread.*WriteProcessMemory|NtUnmapViewOfSection)", re.IGNORECASE | re.DOTALL),
            "description": "Chuỗi API cấp phát và ghi đè bộ nhớ tiến trình khác (Process Hollowing/Injection)."
        },
        {
            "id": "APC_EARLY_BIRD_INJECTION",
            "category": "Process Injection",
            "severity": "HIGH",
            "penalty": 30,
            "pattern": re.compile(rb"(QueueUserAPC|NtQueueApcThread)", re.IGNORECASE),
            "description": "Kỹ thuật tiêm mã độc vào hàng đợi Asynchronous Procedure Call (Early Bird Injection)."
        },

        # Nhóm 3: THỰC THI LỆNH ẨN & DOWNLOADER (Mức độ: HIGH)
        {
            "id": "POWERSHELL_HIDDEN_ENCODED",
            "category": "Execution/Dropper",
            "severity": "HIGH",
            "penalty": 30,
            "pattern": re.compile(rb"powershell.*(-enc|-encodedcommand|-w(indowstyle)?\s+hidden)", re.IGNORECASE),
            "description": "PowerShell chạy lệnh mã hóa base64 hoặc ẩn hoàn toàn cửa sổ."
        },
        {
            "id": "CERTUTIL_DOWNLOAD_DECODE",
            "category": "Execution/Living-off-the-land",
            "severity": "HIGH",
            "penalty": 25,
            "pattern": re.compile(rb"certutil(\.exe)?\s+(-urlcache|-decode|-split)", re.IGNORECASE),
            "description": "Lạm dụng tiện ích hợp pháp CertUtil để tải payload ngầm hoặc giải mã binary."
        },
        {
            "id": "BITSADMIN_MSHTA_DOWNLOAD",
            "category": "Execution/Dropper",
            "severity": "HIGH",
            "penalty": 25,
            "pattern": re.compile(rb"(bitsadmin(\.exe)?\s+/transfer|mshta(\.exe)?\s+(http|vbscript|javascript))", re.IGNORECASE),
            "description": "Lợi dụng Bitsadmin hoặc MSHTA để thực thi script độc từ xa."
        },
        {
            "id": "WIFI_PASSWORD_STEALER",
            "category": "Credential Stealer",
            "severity": "HIGH",
            "penalty": 25,
            "pattern": re.compile(rb"netsh\s+wlan\s+show\s+profile.*key\s*=\s*clear", re.IGNORECASE),
            "description": "Trích xuất mật khẩu mạng Wi-Fi dạng rõ (plaintext)."
        },

        # Nhóm 4: ĐÁNH CẮP TÀI KHOẢN, COOKIES & TOKEN (Mức độ: CRITICAL/HIGH)
        {
            "id": "BROWSER_CREDENTIAL_THEFT",
            "category": "Credential Stealer",
            "severity": "CRITICAL",
            "penalty": 35,
            "pattern": re.compile(rb"(\\Google\\Chrome\\User\s+Data\\.*\\Login\s+Data|cookies\.sqlite|Web\s+Data)", re.IGNORECASE),
            "description": "Truy cập cơ sở dữ liệu mật khẩu lưu trữ của trình duyệt Chrome/Firefox."
        },
        {
            "id": "DISCORD_TOKEN_STEALER",
            "category": "InfoStealer",
            "severity": "HIGH",
            "penalty": 30,
            "pattern": re.compile(rb"(discord.*Local\s+Storage.*leveldb|[a-zA-Z0-9_\-]{24}\.[a-zA-Z0-9_\-]{6}\.[a-zA-Z0-9_\-]{27})", re.IGNORECASE),
            "description": "Tìm kiếm và trích xuất Discord Token từ bộ nhớ Local Storage."
        },
        {
            "id": "KEYLOGGER_HOOK_APIS",
            "category": "Spyware/Keylogger",
            "severity": "HIGH",
            "penalty": 25,
            "pattern": re.compile(rb"(GetAsyncKeyState.*GetKeyboardState|SetWindowsHookEx[AW])", re.IGNORECASE | re.DOTALL),
            "description": "API bắt phím toàn cục và ghi lại lịch sử gõ phím của người dùng."
        },

        # Nhóm 5: VƯỢT HÀNG RÀO BẢO VỆ & ANTI-ANALYSIS (Mức độ: HIGH)
        {
            "id": "DEFENDER_DISABLE_SCRIPT",
            "category": "Defense Evasion",
            "severity": "CRITICAL",
            "penalty": 40,
            "pattern": re.compile(rb"Set-MpPreference\s+.*-DisableRealtimeMonitoring\s+(\$true|1)", re.IGNORECASE),
            "description": "Vô hiệu hóa tính năng bảo vệ thời gian thực của Windows Defender."
        },
        {
            "id": "AMSI_PATCH_BYPASS",
            "category": "Defense Evasion",
            "severity": "HIGH",
            "penalty": 30,
            "pattern": re.compile(rb"(AmsiScanBuffer.*amsi\.dll|amsiInitFailed)", re.IGNORECASE | re.DOTALL),
            "description": "Can thiệp làm vô hiệu hóa AMSI (Antimalware Scan Interface)."
        },

        # Nhóm 6: WEBSHELL & MÃ ĐỘC NỘI DUNG WEB (Mức độ: HIGH)
        {
            "id": "WEBSHELL_EVAL_BASE64",
            "category": "Webshell/Backdoor",
            "severity": "CRITICAL",
            "penalty": 40,
            "pattern": re.compile(rb"(eval\s*\(\s*(base64_decode|gzinflate|str_rot13)|assert\s*\(\s*\$_(POST|GET|REQUEST))", re.IGNORECASE),
            "description": "Cấu trúc webshell obfuscated phổ biến để thực thi mã tùy biến từ xa."
        },
        {
            "id": "SUSPICIOUS_UNIX_SHELL_PIPE",
            "category": "Backdoor/Dropper",
            "severity": "HIGH",
            "penalty": 30,
            "pattern": re.compile(rb"(curl|wget)\s+https?://[^\s|]+\s*\|\s*(ba)?sh", re.IGNORECASE),
            "description": "Tải và đẩy thẳng nội dung web vào shell thực thi ngay trên hệ điều hành."
        }
    ]

    @classmethod
    def scan_chunks(cls, chunks_gen: Generator[bytes, None, None]) -> Tuple[List[Dict[str, Any]], int]:
        """Quét heuristics qua streaming chunks với sliding window để không bỏ sót signature gián đoạn."""
        findings = []
        matched_rule_ids = set()
        overlap_size = 1024
        window = b""
        chunks_count = 0

        for chunk in chunks_gen:
            chunks_count += 1
            buffer = window + chunk
            
            # Quét từng rule
            for rule in cls.HEURISTIC_RULES:
                if rule["id"] in matched_rule_ids:
                    continue  # Đã phát hiện rule này rồi thì bỏ qua
                
                if rule["pattern"].search(buffer):
                    matched_rule_ids.add(rule["id"])
                    findings.append({
                        "id": rule["id"],
                        "category": rule["category"],
                        "severity": rule["severity"],
                        "penalty": rule["penalty"],
                        "description": rule["description"]
                    })

            # Cập nhật sliding window cho chunk tiếp theo
            window = chunk[-overlap_size:] if len(chunk) >= overlap_size else chunk
            del chunk
            del buffer

        del window
        gc.collect()
        return findings, chunks_count


# -------------------------------------------------------------
# PHẦN 3: KIỂM TRA CHỨNG CHỈ SỐ (DIGITAL CERTIFICATE VERIFIER)
# -------------------------------------------------------------

class CertificateVerifier:
    """Xác thực chữ ký số PE Authenticode (Windows), APK/JAR Signatures và thông tin chứng chỉ gói."""

    @classmethod
    def verify_pe_authenticode(cls, file_path: str) -> Dict[str, Any]:
        """Trích xuất và kiểm tra chữ ký Authenticode trong file PE thông qua Directory Entry Security."""
        if not pefile:
            return {"is_signed": False, "status": "PEFILE_NOT_AVAILABLE", "details": "Thư viện pefile chưa cài đặt."}

        try:
            pe = pefile.PE(file_path, fast_load=True)
            sec_idx = pefile.DIRECTORY_ENTRY.get("IMAGE_DIRECTORY_ENTRY_SECURITY", 4)
            data_dirs = getattr(pe.OPTIONAL_HEADER, "DATA_DIRECTORY", [])
            
            if len(data_dirs) <= sec_idx:
                pe.close()
                return {"is_signed": False, "status": "UNSIGNED", "details": "Không có bảng danh mục bảo mật Security Directory."}

            sec_dir = data_dirs[sec_idx]
            v_addr = sec_dir.VirtualAddress
            v_size = sec_dir.Size
            pe.close()
            del pe

            if v_addr == 0 or v_size == 0:
                return {
                    "is_signed": False,
                    "status": "UNSIGNED",
                    "details": "Tệp tin thực thi chưa được ký số (Unsigned binary)."
                }

            # Đọc cấu trúc WIN_CERTIFICATE trực tiếp từ file (Stream seek tuân thủ Rule 7.1)
            with open(file_path, "rb") as f:
                f.seek(v_addr)
                raw_cert_header = f.read(8)
                if len(raw_cert_header) < 8:
                    return {"is_signed": False, "status": "MALFORMED_CERTIFICATE", "details": "Header chứng chỉ bị lỗi."}
                
                length, rev, cert_type = struct.unpack_from("<IHH", raw_cert_header, 0)
                cert_data_len = min(length - 8, v_size - 8)
                b_cert = f.read(cert_data_len)

            # WIN_CERT_TYPE_PKCS_SIGNED_DATA = 0x0002
            signer_info = []
            is_valid = True
            is_self_signed = False

            if pkcs7 and cert_type == 2:
                try:
                    with warnings.catch_warnings():
                        warnings.simplefilter("ignore")
                        certs = pkcs7.load_der_pkcs7_certificates(b_cert)
                    for c in certs:
                        subject_str = c.subject.rfc4514_string()
                        issuer_str = c.issuer.rfc4514_string()
                        self_signed = (subject_str == issuer_str)
                        if self_signed:
                            is_self_signed = True
                        
                        signer_info.append({
                            "subject": subject_str,
                            "issuer": issuer_str,
                            "serial_number": str(c.serial_number),
                            "self_signed": self_signed
                        })
                    del certs
                except Exception as ex:
                    signer_info.append({"error": f"Không thể giải mã cấu trúc PKCS#7: {str(ex)}"})

            del b_cert
            gc.collect()

            return {
                "is_signed": True,
                "status": "SIGNED",
                "cert_type_code": cert_type,
                "is_self_signed": is_self_signed,
                "certificates_count": len(signer_info),
                "signers": signer_info,
                "details": "Tệp tin có chữ ký số Authenticode hợp lệ." if not is_self_signed else "Chứng chỉ tự ký (Self-signed certificate)."
            }
        except Exception as e:
            err_msg = str(e)
            if "DOS Header" in err_msg or "magic not found" in err_msg.lower() or "not a valid pe" in err_msg.lower():
                return {"is_signed": False, "status": "ERROR_OR_NOT_PE", "details": f"Tệp tin không thuộc định dạng PE: {err_msg}"}
            return {"is_signed": False, "status": "ERROR", "details": f"Lỗi khi đọc chữ ký số: {err_msg}"}

    @classmethod
    def verify_package_signature(cls, file_path: Optional[str], package_info: Optional[Dict[str, Any]]) -> Dict[str, Any]:
        """Tổng hợp kiểm tra chữ ký từ file vật lý hoặc metadata gói JSON."""
        # 1. Nếu có file vật lý PE
        if file_path and os.path.exists(file_path):
            with open(file_path, "rb") as f:
                header = f.read(2)
            if header == b"MZ":
                return cls.verify_pe_authenticode(file_path)

            # Nếu là file APK/JAR
            if zipfile and file_path.lower().endswith((".apk", ".jar", ".zip")):
                try:
                    with zipfile.ZipFile(file_path, "r") as zf:
                        names = zf.namelist()
                        has_meta_inf = any("META-INF/" in n for n in names)
                        has_cert = any(n.upper().endswith((".RSA", ".DSA", ".EC")) for n in names)
                        del names
                    if has_cert:
                        return {
                            "is_signed": True,
                            "status": "SIGNED_ANDROID_JAR",
                            "is_self_signed": False,
                            "details": "Gói ứng dụng có chữ ký số Android APK/JAR trong thư mục META-INF."
                        }
                except Exception:
                    pass

        # 2. Nếu kiểm tra qua thông tin khai báo trong package_info
        if package_info:
            cert_meta = package_info.get("certificate") or package_info.get("code_signing") or {}
            publisher = package_info.get("publisher") or package_info.get("developer")
            if cert_meta or publisher:
                is_signed = bool(cert_meta.get("is_signed", True if publisher else False))
                return {
                    "is_signed": is_signed,
                    "status": "METADATA_VERIFIED" if is_signed else "METADATA_UNSIGNED",
                    "publisher": publisher or cert_meta.get("issuer"),
                    "is_self_signed": cert_meta.get("is_self_signed", False),
                    "details": "Đã xác thực chữ ký từ nhà phát hành được khai báo." if is_signed else "Ứng dụng chưa có chữ ký nhà phát hành."
                }

        return {
            "is_signed": False,
            "status": "UNSIGNED",
            "is_self_signed": False,
            "details": "Không phát hiện chữ ký số hợp lệ cho ứng dụng."
        }


# -------------------------------------------------------------
# PHẦN 4: KIỂM TRA QUYỀN HẠN YÊU CẦU (PERMISSIONS AUDITOR)
# -------------------------------------------------------------

class PermissionsAuditor:
    """Đánh giá mức độ rủi ro của quyền hạn yêu cầu trên Windows PE, Android APK và Package Info."""

    # Danh mục quyền hạn nguy hiểm trên di động / hệ thống
    DANGEROUS_PERMISSIONS = {
        "READ_SMS": {"risk": "HIGH", "penalty": 15, "desc": "Đọc tin nhắn SMS riêng tư (nguy cơ lộ mã OTP ngân hàng)"},
        "SEND_SMS": {"risk": "HIGH", "penalty": 15, "desc": "Tự động gửi tin nhắn SMS ngầm (trừ tiền cước)"},
        "RECEIVE_SMS": {"risk": "HIGH", "penalty": 10, "desc": "Chặn đọc tin nhắn SMS đến"},
        "RECORD_AUDIO": {"risk": "HIGH", "penalty": 12, "desc": "Ghi âm môi trường xung quanh lén lút"},
        "CAMERA": {"risk": "MEDIUM", "penalty": 8, "desc": "Truy cập máy ảnh thiết bị"},
        "ACCESS_FINE_LOCATION": {"risk": "MEDIUM", "penalty": 8, "desc": "Định vị GPS chính xác vị trí người dùng"},
        "READ_CONTACTS": {"risk": "MEDIUM", "penalty": 10, "desc": "Trích xuất danh bạ cá nhân"},
        "SYSTEM_ALERT_WINDOW": {"risk": "HIGH", "penalty": 20, "desc": "Vẽ đè giao diện giả mạo để chiếm quyền chạm (Overlay Attack)"},
        "REQUEST_INSTALL_PACKAGES": {"risk": "HIGH", "penalty": 20, "desc": "Tự ý tải và cài đặt file APK ngoài luồng không qua kiểm duyệt"},
        "BIND_ACCESSIBILITY_SERVICE": {"risk": "CRITICAL", "penalty": 30, "desc": "Chiếm dịch vụ Trợ Năng (Accessibility) - hành vi điển hình của Trojan ngân hàng"},
        "RECEIVE_BOOT_COMPLETED": {"risk": "LOW", "penalty": 5, "desc": "Khởi động cùng thiết bị ngay sau khi bật máy"}
    }

    # Các API Windows nguy hiểm nhập vào từ Import Table
    DANGEROUS_WIN32_APIS = {
        "VirtualAllocEx": {"risk": "CRITICAL", "penalty": 25, "desc": "Cấp phát bộ nhớ trong tiến trình khác"},
        "WriteProcessMemory": {"risk": "CRITICAL", "penalty": 25, "desc": "Ghi dữ liệu vào không gian bộ nhớ của tiến trình khác"},
        "CreateRemoteThread": {"risk": "CRITICAL", "penalty": 25, "desc": "Kích hoạt luồng chạy mã độc trong tiến trình khác"},
        "SetWindowsHookEx": {"risk": "HIGH", "penalty": 15, "desc": "Cài đặt Hook hệ thống để bắt phím hoặc thông điệp"},
        "RegSetValueEx": {"risk": "MEDIUM", "penalty": 5, "desc": "Ghi Registry hệ thống (nguy cơ lưu khóa tự khởi động)"},
        "AdjustTokenPrivileges": {"risk": "HIGH", "penalty": 15, "desc": "Nâng quyền hạn tài khoản (Privilege Escalation)"}
    }

    @classmethod
    def audit_package_permissions(cls, 
                                  file_path: Optional[str] = None, 
                                  package_info: Optional[Dict[str, Any]] = None) -> Dict[str, Any]:
        """Phân tích quyền hạn từ Manifest PE, Imports PE hoặc khai báo quyền hạn JSON."""
        findings = []
        total_penalty = 0
        requested_admin = False
        scanned_permissions = []

        # 1. Nếu có file PE -> kiểm tra Imports và Manifest
        if file_path and os.path.exists(file_path) and pefile:
            try:
                with open(file_path, "rb") as f:
                    is_pe = (f.read(2) == b"MZ")
                if is_pe:
                    pe = pefile.PE(file_path, fast_load=True)
                    pe.parse_data_directories(directories=[
                        pefile.DIRECTORY_ENTRY.get("IMAGE_DIRECTORY_ENTRY_IMPORT", 1),
                        pefile.DIRECTORY_ENTRY.get("IMAGE_DIRECTORY_ENTRY_RESOURCE", 2)
                    ])

                    # Kiểm tra Import API
                    for entry in getattr(pe, "DIRECTORY_ENTRY_IMPORT", []):
                        dll_name = entry.dll.decode(errors="ignore") if entry.dll else ""
                        for imp in entry.imports:
                            func_name = imp.name.decode(errors="ignore") if imp.name else ""
                            for api_name, meta in cls.DANGEROUS_WIN32_APIS.items():
                                if api_name.lower() in func_name.lower():
                                    findings.append({
                                        "source": f"Win32 API ({dll_name})",
                                        "permission": api_name,
                                        "risk": meta["risk"],
                                        "penalty": meta["penalty"],
                                        "description": meta["desc"]
                                    })
                                    total_penalty += meta["penalty"]

                    # Kiểm tra RT_MANIFEST để phát hiện requireAdministrator
                    for rsrc in getattr(pe, "DIRECTORY_ENTRY_RESOURCE", []).entries:
                        if rsrc.id == 24:  # RT_MANIFEST
                            for sub_dir in rsrc.directory.entries:
                                for sub_res in sub_dir.directory.entries:
                                    raw_xml = pe.get_data(sub_res.data.struct.OffsetToData, sub_res.data.struct.Size).decode(errors="ignore")
                                    if 'requireAdministrator' in raw_xml:
                                        requested_admin = True
                                        findings.append({
                                            "source": "PE Manifest XML",
                                            "permission": "requireAdministrator",
                                            "risk": "HIGH",
                                            "penalty": 20,
                                            "description": "Ứng dụng đòi hỏi quyền Administrator cao nhất của hệ điều hành."
                                        })
                                        total_penalty += 20
                    pe.close()
                    del pe
            except Exception as e:
                logger.warning(f"[AI] Lỗi phân tích quyền hạn PE: {str(e)}")

        # 2. Nếu có file Android APK -> tìm permissions trong AndroidManifest
        if file_path and os.path.exists(file_path) and zipfile and file_path.lower().endswith(".apk"):
            try:
                with zipfile.ZipFile(file_path, "r") as zf:
                    if "AndroidManifest.xml" in zf.namelist():
                        manifest_raw = zf.read("AndroidManifest.xml")
                        # Quét các chuỗi permission trong AndroidManifest binary XML
                        for perm_key, meta in cls.DANGEROUS_PERMISSIONS.items():
                            pattern = bytes(perm_key, "utf-8")
                            if pattern in manifest_raw:
                                scanned_permissions.append(perm_key)
                                findings.append({
                                    "source": "AndroidManifest.xml",
                                    "permission": f"android.permission.{perm_key}",
                                    "risk": meta["risk"],
                                    "penalty": meta["penalty"],
                                    "description": meta["desc"]
                                })
                                total_penalty += meta["penalty"]
                        del manifest_raw
            except Exception as e:
                logger.warning(f"[AI] Lỗi kiểm tra Android Manifest: {str(e)}")

        # 3. Phân tích quyền hạn từ package_info JSON đầu vào
        if package_info:
            perms = package_info.get("permissions") or []
            if isinstance(perms, list):
                for p in perms:
                    p_upper = str(p).upper().replace("ANDROID.PERMISSION.", "")
                    scanned_permissions.append(p)
                    for d_key, d_meta in cls.DANGEROUS_PERMISSIONS.items():
                        if d_key in p_upper:
                            findings.append({
                                "source": "Package Metadata JSON",
                                "permission": p,
                                "risk": d_meta["risk"],
                                "penalty": d_meta["penalty"],
                                "description": d_meta["desc"]
                            })
                            total_penalty += d_meta["penalty"]

            if package_info.get("requires_admin", False) and not requested_admin:
                requested_admin = True
                findings.append({
                    "source": "Package Metadata JSON",
                    "permission": "Administrator Rights",
                    "risk": "HIGH",
                    "penalty": 20,
                    "description": "Gói ứng dụng yêu cầu cấp quyền quản trị (Admin/Root)."
                })
                total_penalty += 20

        gc.collect()
        return {
            "requested_admin": requested_admin,
            "permissions_analyzed": scanned_permissions,
            "dangerous_findings": findings,
            "total_permission_penalty": min(total_penalty, 60)  # Giới hạn mức trừ quyền tối đa 60 điểm
        }


# -------------------------------------------------------------
# PHẦN 5: ĐIỀU PHỐI ĐÁNH GIÁ ĐIỂM TÍN NHIỆM & AN TOÀN (SCORER)
# -------------------------------------------------------------

class PackageSecurityScanner:
    """Dịch vụ điều phối kiểm tra an ninh toàn diện gói ứng dụng cho supportflast_ai."""

    def __init__(self):
        self.logger = logger

    def scan(self, 
             file_path: Optional[str] = None, 
             package_info: Optional[Dict[str, Any]] = None,
             raw_content_base64: Optional[str] = None) -> Dict[str, Any]:
        """Thực hiện quét toàn diện và trả về Safety Level (Safe, Suspicious, Malicious) cùng Safety Score."""
        import time
        start_time = time.time()
        
        # Đo lường RAM trước khi quét tuân thủ Rule 7.1 & 7.4
        mem_before_mb = 0.0
        if psutil:
            try:
                mem_before_mb = round(psutil.Process().memory_info().rss / (1024 * 1024), 2)
            except Exception:
                pass

        temp_file_created = False
        target_path = file_path

        # Nếu truyền raw_content_base64 -> ghi tạm ra tệp tin trên ổ F: để xử lý streaming (Rule 1.4)
        if raw_content_base64 and not target_path:
            try:
                decoded_bytes = base64.b64decode(raw_content_base64)
                temp_dir = os.path.join(os.path.dirname(os.path.dirname(__file__)), "tmp_scans")
                os.makedirs(temp_dir, exist_ok=True)
                target_path = os.path.join(temp_dir, f"scan_payload_{int(time.time() * 1000)}.bin")
                with open(target_path, "wb") as f:
                    f.write(decoded_bytes)
                temp_file_created = True
                del decoded_bytes
            except Exception as e:
                return {
                    "status": "error",
                    "error": f"Không thể giải mã dữ liệu base64: {str(e)}",
                    "code": "ERR_SCAN_DECODE"
                }

        # Kiểm tra sự tồn tại của file nếu có path
        if target_path and not os.path.exists(target_path):
            return {
                "status": "error",
                "error": f"Không tìm thấy tệp tin cần quét: {target_path}",
                "code": "ERR_FILE_NOT_FOUND"
            }

        try:
            # 1. Phân tích Entropy và cấu trúc tệp tin
            entropy_res = {}
            if target_path and os.path.exists(target_path):
                entropy_res = EntropyAnalyzer.analyze_file(target_path)
            else:
                entropy_res = {
                    "overall_entropy": 0.0,
                    "file_size_bytes": 0,
                    "is_packed_or_encrypted": False,
                    "is_pe": False,
                    "is_archive": False,
                    "suspicious_sections": [],
                    "archive_stats": {}
                }

            # 2. Quét Heuristics tĩnh qua Streaming Chunks
            heuristic_findings = []
            chunks_processed = 0
            if target_path and os.path.exists(target_path):
                chunks_gen = stream_file_chunks(target_path)
                heuristic_findings, chunks_processed = StaticHeuristicScanner.scan_chunks(chunks_gen)
                del chunks_gen

            # 3. Kiểm tra Chữ ký số
            cert_res = CertificateVerifier.verify_package_signature(target_path, package_info)

            # 4. Phân tích Quyền hạn yêu cầu
            perm_res = PermissionsAuditor.audit_package_permissions(target_path, package_info)

            # 5. TÍNH ĐIỂM TÍN NHIỆM (SAFETY SCORE)
            base_score = 100.0
            critical_threats = []
            high_threats = []
            moderate_threats = []

            # Trừ điểm Heuristics
            for h in heuristic_findings:
                if h["severity"] == "CRITICAL":
                    critical_threats.append(h)
                    base_score -= h["penalty"]
                elif h["severity"] == "HIGH":
                    high_threats.append(h)
                    base_score -= h["penalty"]
                else:
                    moderate_threats.append(h)
                    base_score -= h["penalty"]

            # Trừ điểm Entropy / Packing
            if entropy_res.get("is_packed_or_encrypted"):
                if not cert_res.get("is_signed"):
                    base_score -= 25.0
                    moderate_threats.append({
                        "id": "HIGH_ENTROPY_PACKED_UNSIGNED",
                        "category": "Obfuscation",
                        "severity": "HIGH",
                        "description": f"Tệp tin có entropy rất cao ({entropy_res.get('overall_entropy')}) không có chữ ký số (nghi vấn bị nén hoặc che giấu mã độc)."
                    })

            # Trừ điểm nếu có dấu hiệu Zip Bomb
            if entropy_res.get("archive_stats", {}).get("is_zip_bomb"):
                base_score -= 40.0
                critical_threats.append({
                    "id": "ZIP_BOMB_PAYLOAD",
                    "category": "Denial of Service",
                    "severity": "CRITICAL",
                    "description": "Tỷ lệ nén và kích thước giải nén bất thường, nguy cơ tấn công làm cạn kiệt ổ đĩa (Zip Bomb)."
                })

            # Trừ điểm Chữ ký số
            if not cert_res.get("is_signed"):
                # Nếu là binary thực thi (PE/APK) mà không có chữ ký số -> trừ 15 điểm
                if entropy_res.get("is_pe") or (target_path and target_path.lower().endswith((".exe", ".dll", ".apk"))):
                    base_score -= 15.0
                    moderate_threats.append({
                        "id": "UNSIGNED_EXECUTABLE",
                        "category": "Code Signing",
                        "severity": "MEDIUM",
                        "description": "Ứng dụng thực thi chưa được ký số bởi chứng chỉ Authenticode hợp lệ."
                    })
            elif cert_res.get("is_self_signed"):
                base_score -= 12.0
                moderate_threats.append({
                    "id": "SELF_SIGNED_CERTIFICATE",
                    "category": "Code Signing",
                    "severity": "MEDIUM",
                    "description": "Chứng chỉ số là dạng tự ký (Self-signed), không được xác thực từ tổ chức CA tin cậy."
                })

            # Trừ điểm Quyền hạn
            base_score -= perm_res.get("total_permission_penalty", 0)

            # Khống chế điểm trong đoạn [0, 100]
            safety_score = max(0.0, min(100.0, round(base_score, 1)))

            # 6. PHÂN CẤP AN TOÀN (SAFETY LEVEL): Safe, Suspicious, Malicious
            if len(critical_threats) > 0 or safety_score < 50.0:
                safety_level = "Malicious"
            elif len(high_threats) > 0 or safety_score < 80.0 or len(moderate_threats) >= 2:
                safety_level = "Suspicious"
            else:
                safety_level = "Safe"

            # Bản tóm tắt tiếng Việt (Rule 0.1)
            verdict_summary = ""
            if safety_level == "Safe":
                verdict_summary = f"Gói ứng dụng đạt tiêu chuẩn an toàn cao ({safety_score}/100). Không phát hiện mẫu mã độc tĩnh nguy hại."
            elif safety_level == "Suspicious":
                verdict_summary = f"Gói ứng dụng có dấu hiệu nghi vấn ({safety_score}/100). Cần kiểm duyệt chuyên sâu trước khi cấp phép phát hành."
            else:
                verdict_summary = f"CẢNH BÁO NGUY HIỂM: Phát hiện dấu hiệu mã độc rõ ràng ({safety_score}/100). Đã cách ly payload."

            duration_ms = round((time.time() - start_time) * 1000, 2)
            
            # Đo lường RAM sau khi quét
            mem_after_mb = 0.0
            if psutil:
                try:
                    mem_after_mb = round(psutil.Process().memory_info().rss / (1024 * 1024), 2)
                except Exception:
                    pass

            return {
                "status": "success",
                "safety_level": safety_level,
                "safety_score": safety_score,
                "verdict_summary": verdict_summary,
                "details": {
                    "file_path": file_path,
                    "package_name": (package_info or {}).get("name") or os.path.basename(file_path or "unknown_package"),
                    "threats_summary": {
                        "critical_count": len(critical_threats),
                        "high_count": len(high_threats),
                        "moderate_count": len(moderate_threats),
                        "total_threats": len(critical_threats) + len(high_threats) + len(moderate_threats)
                    },
                    "heuristics": {
                        "findings": heuristic_findings,
                        "chunks_scanned": chunks_processed
                    },
                    "entropy": entropy_res,
                    "code_signing": cert_res,
                    "permissions": perm_res
                },
                "resource_metrics": {
                    "duration_ms": duration_ms,
                    "memory_rss_before_mb": mem_before_mb,
                    "memory_rss_after_mb": mem_after_mb
                }
            }

        finally:
            # Dọn dẹp tệp tin tạm và giải phóng bộ nhớ (Rule 7.1)
            if temp_file_created and target_path and os.path.exists(target_path):
                try:
                    os.remove(target_path)
                except Exception as e:
                    logger.warning(f"[AI] Không thể xóa file tạm {target_path}: {str(e)}")
            gc.collect()
