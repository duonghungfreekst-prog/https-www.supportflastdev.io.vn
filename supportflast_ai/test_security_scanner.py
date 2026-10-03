"""Bộ kiểm thử đơn vị (Unit Tests) cho Module Phân Tích An Ninh Gói Ứng Dụng (PackageSecurityScanner)
và API nội bộ /scan của supportflast_ai.
Kiểm tra toàn diện: Heuristics, Entropy, Chữ ký số, Quyền hạn, Điểm tín nhiệm và Rule 7.1.
"""

import os
import sys
import base64
import unittest
from starlette.testclient import TestClient

import app
from core.security_scanner import (
    PackageSecurityScanner,
    EntropyAnalyzer,
    StaticHeuristicScanner,
    CertificateVerifier,
    PermissionsAuditor,
    stream_file_chunks,
    batch_generator
)

class TestPackageSecurityScanner(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.client = TestClient(app.app)
        cls.scanner = PackageSecurityScanner()
        cls.test_dir = os.path.join(os.path.dirname(__file__), "test_tmp")
        os.makedirs(cls.test_dir, exist_ok=True)

    @classmethod
    def tearDownClass(cls):
        # Dọn dẹp thư mục test tạm
        if os.path.exists(cls.test_dir):
            for f in os.listdir(cls.test_dir):
                try:
                    os.remove(os.path.join(cls.test_dir, f))
                except Exception:
                    pass
            try:
                os.rmdir(cls.test_dir)
            except Exception:
                pass

    def test_generator_and_batching_rule_7_1(self):
        """Kiểm tra việc sử dụng Generator và Batching tuân thủ Rule 7.1."""
        test_file = os.path.join(self.test_dir, "batch_test.bin")
        data = b"SupportFlast AI Engine " * 100
        with open(test_file, "wb") as f:
            f.write(data)

        # 1. Kiểm tra streaming generator
        chunks = list(stream_file_chunks(test_file, chunk_size=64))
        self.assertGreater(len(chunks), 1)
        self.assertEqual(b"".join(chunks), data)

        # 2. Kiểm tra batching generator
        items = list(range(2500))
        batches = list(batch_generator(items, batch_size=1000))
        self.assertEqual(len(batches), 3)
        self.assertEqual(len(batches[0]), 1000)
        self.assertEqual(len(batches[1]), 1000)
        self.assertEqual(len(batches[2]), 500)

    def test_entropy_calculation(self):
        """Kiểm tra tính toán Shannon Entropy cho dữ liệu có cấu trúc và ngẫu nhiên."""
        # Dữ liệu lặp lại đơn điệu có entropy thấp
        low_entropy_file = os.path.join(self.test_dir, "low_entropy.bin")
        with open(low_entropy_file, "wb") as f:
            f.write(b"A" * 1024)

        low_res = EntropyAnalyzer.analyze_file(low_entropy_file)
        self.assertAlmostEqual(low_res["overall_entropy"], 0.0, places=2)
        self.assertFalse(low_res["is_packed_or_encrypted"])

        # Dữ liệu giả lập ngẫu nhiên cao (high entropy)
        high_entropy_file = os.path.join(self.test_dir, "high_entropy.bin")
        with open(high_entropy_file, "wb") as f:
            f.write(os.urandom(2048))

        high_res = EntropyAnalyzer.analyze_file(high_entropy_file)
        self.assertGreater(high_res["overall_entropy"], 7.0)

    def test_static_heuristics_malware_patterns(self):
        """Kiểm tra phát hiện mẫu mã độc tĩnh: Ransomware, Process Injection, Droppers."""
        malicious_payload = (
            b"echo Starting...\n"
            b"vssadmin.exe delete shadows /all /quiet\n"
            b"powershell -windowstyle hidden -enc aW52b2tl\n"
            b"bcdedit /set {default} recoveryenabled no\n"
        )
        mal_file = os.path.join(self.test_dir, "malware_sample.bat")
        with open(mal_file, "wb") as f:
            f.write(malicious_payload)

        res = self.scanner.scan(file_path=mal_file)
        self.assertEqual(res["status"], "success")
        self.assertEqual(res["safety_level"], "Malicious")
        self.assertLess(res["safety_score"], 50.0)

        threat_ids = [h["id"] for h in res["details"]["heuristics"]["findings"]]
        self.assertIn("RANSOMWARE_WIPER_SHADOW_DELETE", threat_ids)
        self.assertIn("RANSOMWARE_BCDEDIT_DISABLE_RECOVERY", threat_ids)
        self.assertIn("POWERSHELL_HIDDEN_ENCODED", threat_ids)

    def test_certificate_verification_pe(self):
        """Kiểm tra thẩm định chữ ký số PE Authenticode trên tệp thực thi chuẩn (đa nền tảng)."""
        if sys.platform != "win32":
            # Trên Linux / Ubuntu CI runner, sys.executable là tệp ELF chứ không phải Windows PE (.exe)
            res = CertificateVerifier.verify_pe_authenticode(sys.executable)
            self.assertFalse(res["is_signed"])
            self.assertIn(res["status"], ["ERROR_OR_NOT_PE", "UNSIGNED"])
            return

        res = CertificateVerifier.verify_pe_authenticode(sys.executable)
        self.assertTrue(res["is_signed"])
        self.assertEqual(res["status"], "SIGNED")
        self.assertGreater(res["certificates_count"], 0)

    def test_permissions_auditor_dangerous_mobile(self):
        """Kiểm tra phân tích quyền hạn nguy hiểm trên di động."""
        pkg_info = {
            "name": "MaliciousBankingTrojan.apk",
            "permissions": [
                "BIND_ACCESSIBILITY_SERVICE",
                "SYSTEM_ALERT_WINDOW",
                "SEND_SMS",
                "RECEIVE_BOOT_COMPLETED"
            ]
        }
        res = PermissionsAuditor.audit_package_permissions(package_info=pkg_info)
        self.assertGreater(res["total_permission_penalty"], 40)
        found_perms = [d["permission"] for d in res["dangerous_findings"]]
        self.assertIn("BIND_ACCESSIBILITY_SERVICE", found_perms)
        self.assertIn("SYSTEM_ALERT_WINDOW", found_perms)

    def test_scan_api_safe_package(self):
        """Kiểm tra API /scan với gói phần mềm an toàn."""
        payload = {
            "package_name": "SupportFlastAssistant.exe",
            "package_info": {
                "publisher": "Supportflast Dev Team",
                "version": "1.0.0",
                "permissions": ["INTERNET", "ACCESS_NETWORK_STATE"]
            }
        }
        response = self.client.post("/scan", json=payload)
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertEqual(data["status"], "success")
        self.assertEqual(data["safety_level"], "Safe")
        self.assertGreaterEqual(data["safety_score"], 80.0)

    def test_scan_api_suspicious_package(self):
        """Kiểm tra API /scan với gói phần mềm có nghi vấn."""
        payload = {
            "package_name": "UnverifiedTool.exe",
            "package_info": {
                "requires_admin": True,
                "permissions": ["CAMERA", "RECORD_AUDIO", "ACCESS_FINE_LOCATION"]
            }
        }
        response = self.client.post("/scan", json=payload)
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertEqual(data["status"], "success")
        self.assertEqual(data["safety_level"], "Suspicious")
        self.assertLess(data["safety_score"], 80.0)

    def test_scan_api_malicious_base64_payload(self):
        """Kiểm tra API /scan gửi payload base64 chứa mã độc."""
        raw_evil = b"Set-MpPreference -DisableRealtimeMonitoring $true\nvssadmin delete shadows"
        b64_content = base64.b64encode(raw_evil).decode()
        
        response = self.client.post("/scan", json={"raw_content_base64": b64_content})
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertEqual(data["status"], "success")
        self.assertEqual(data["safety_level"], "Malicious")
        self.assertLess(data["safety_score"], 50.0)

    def test_scan_api_path_traversal_rejection(self):
        """Kiểm tra từ chối yêu cầu quét file chứa ký tự Path Traversal (Rule 3.1 & 3.5)."""
        response = self.client.post("/scan", json={"file_path": "../windows/system32/cmd.exe"})
        self.assertEqual(response.status_code, 400)
        self.assertIn("không hợp lệ", response.json()["detail"])

    def test_scan_api_file_not_found(self):
        """Kiểm tra lỗi 404 khi đường dẫn tệp tin không tồn tại."""
        response = self.client.post("/scan", json={"file_path": "f:/supportflast.dev/supportflast_ai/missing_file.exe"})
        self.assertEqual(response.status_code, 404)

    def test_scan_api_empty_request(self):
        """Kiểm tra lỗi 400 khi không truyền bất kỳ trường dữ liệu nào."""
        response = self.client.post("/scan", json={})
        self.assertEqual(response.status_code, 400)

    def test_api_scan_alias_route(self):
        """Kiểm tra endpoint phụ /api/scan hoạt động đồng nhất."""
        payload = {
            "package_name": "AliasTestApp",
            "package_info": {"permissions": ["RECEIVE_BOOT_COMPLETED"]}
        }
        response = self.client.post("/api/scan", json=payload)
        self.assertEqual(response.status_code, 200)
        self.assertEqual(response.json()["safety_level"], "Safe")


if __name__ == "__main__":
    unittest.main()
