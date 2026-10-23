"""Kiểm tra xử lý bất đồng bộ Kafka / Redpanda Consumer và Idempotency Key chống trùng lặp (Rule PHẦN 5)
Go Engine (producer) -> Python AI (consumer)
"""
import os
import sys
import unittest
import hashlib
import time

class KafkaConsumerIdempotencyStore:
    def __init__(self):
        self._processed_keys = {}

    def process_event(self, message: dict) -> dict:
        key = message.get("idempotency_key")
        if not key or not isinstance(key, str) or len(key.strip()) < 16:
            raise ValueError("Vi phạm Rule PHẦN 5: Message thiếu Idempotency Key bắt buộc")
        
        # Kiểm tra trùng lặp
        if key in self._processed_keys:
            return {
                "status": "DUPLICATE_IGNORED",
                "idempotency_key": key,
                "first_processed_at": self._processed_keys[key]["processed_at"],
                "message": "Message đã được xử lý trước đó, bỏ qua để chống duplicate"
            }
        
        # Xử lý và lưu vết Idempotency Key
        result = {
            "status": "PROCESSED",
            "idempotency_key": key,
            "event_type": message.get("event_type"),
            "processed_at": time.time()
        }
        self._processed_keys[key] = result
        return result

class TestKafkaConsumerIdempotency(unittest.TestCase):
    def setUp(self):
        self.consumer = KafkaConsumerIdempotencyStore()

    def test_valid_message_with_idempotency_key(self):
        raw_data = "engine:file_upload:chunk-001"
        key = hashlib.sha256(raw_data.encode()).hexdigest()
        msg = {
            "idempotency_key": key,
            "event_type": "CHUNK_UPLOADED",
            "source_service": "supportflast_engine",
            "target_consumer": "supportflast_ai",
            "payload": {"chunk_id": "chunk-001", "size": 2048}
        }
        res = self.consumer.process_event(msg)
        self.assertEqual(res["status"], "PROCESSED")
        self.assertEqual(res["idempotency_key"], key)

    def test_duplicate_message_is_safely_ignored(self):
        key = hashlib.sha256(b"engine:siem_event:dup-test").hexdigest()
        msg = {
            "idempotency_key": key,
            "event_type": "SIEM_ALERT",
            "source_service": "supportflast_engine",
            "target_consumer": "supportflast_ai",
            "payload": {"alert": "HONEYPOT_HIT"}
        }
        res1 = self.consumer.process_event(msg)
        self.assertEqual(res1["status"], "PROCESSED")

        # Lần 2 gửi trùng lặp message
        res2 = self.consumer.process_event(msg)
        self.assertEqual(res2["status"], "DUPLICATE_IGNORED")

    def test_missing_idempotency_key_raises_error(self):
        msg = {
            "event_type": "SIEM_ALERT",
            "source_service": "supportflast_engine",
            "target_consumer": "supportflast_ai",
            "payload": {"alert": "NO_KEY"}
        }
        with self.assertRaises(ValueError):
            self.consumer.process_event(msg)

if __name__ == "__main__":
    unittest.main()
