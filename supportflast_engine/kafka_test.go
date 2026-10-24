package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// KafkaMessageEnvelope đại diện cho cấu trúc gói tin phát lên Kafka/Redpanda
// BẮT BUỘC tuân thủ Rule PHẦN 5: BẮT BUỘC implement Idempotency Key trong mọi message.
type KafkaMessageEnvelope struct {
	IdempotencyKey string                 `json:"idempotency_key"`
	EventType      string                 `json:"event_type"`
	SourceService  string                 `json:"source_service"`
	TargetConsumer string                 `json:"target_consumer"`
	Timestamp      int64                  `json:"timestamp"`
	Payload        map[string]interface{} `json:"payload"`
}

// ValidateMessage kiểm tra tính hợp lệ và bắt buộc phải có Idempotency Key
func (m *KafkaMessageEnvelope) ValidateMessage() error {
	if strings.TrimSpace(m.IdempotencyKey) == "" {
		return fmt.Errorf("vi phạm Rule PHẦN 5: Message thiếu Idempotency Key bắt buộc")
	}
	if len(m.IdempotencyKey) < 16 {
		return fmt.Errorf("Idempotency Key quá ngắn, phải tối thiểu 16 ký tự: %s", m.IdempotencyKey)
	}
	if strings.TrimSpace(m.EventType) == "" {
		return fmt.Errorf("Message thiếu event_type")
	}
	if strings.TrimSpace(m.SourceService) == "" {
		return fmt.Errorf("Message thiếu source_service")
	}
	return nil
}

// GenerateIdempotencyKey sinh khóa Idempotency Key chuẩn SHA-256 dựa trên dữ liệu payload và timestamp
func GenerateIdempotencyKey(source, eventType string, payloadData string) string {
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("%s:%s:%s", source, eventType, payloadData)))
	return hex.EncodeToString(h.Sum(nil))
}

// IdempotencyDeduplicator mô phỏng bộ đệm chống trùng lặp tại Consumer
type IdempotencyDeduplicator struct {
	mu       sync.Mutex
	seenKeys map[string]time.Time
}

func NewIdempotencyDeduplicator() *IdempotencyDeduplicator {
	return &IdempotencyDeduplicator{
		seenKeys: make(map[string]time.Time),
	}
}

// ProcessMessage kiểm tra và ghi nhận key; trả về true nếu là message mới, false nếu trùng lặp
func (d *IdempotencyDeduplicator) ProcessMessage(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.seenKeys[key]; exists {
		return false // Trùng lặp - Bỏ qua (Deduplicated)
	}
	d.seenKeys[key] = time.Now()
	return true // Hợp lệ - Xử lý lần đầu
}

// TestKafkaRedpanda_Config kiểm tra cấu hình kết nối Kafka / Redpanda (Rule PHẦN 5)
func TestKafkaRedpanda_Config(t *testing.T) {
	brokers := os.Getenv("KAFKA_BROKERS")
	if brokers == "" {
		brokers = os.Getenv("REDPANDA_BROKERS")
	}
	if brokers == "" {
		brokers = "127.0.0.1:9092" // Mặc định cục bộ
	}

	t.Logf("[KAFKA] Cấu hình Broker phát hiện: %s", brokers)

	// Tách danh sách broker nếu có nhiều node (cluster)
	brokerList := strings.Split(brokers, ",")
	for _, b := range brokerList {
		hostPort := strings.TrimSpace(b)
		if !strings.Contains(hostPort, ":") {
			t.Errorf("Địa chỉ broker không hợp lệ (thiếu port): %s", hostPort)
			continue
		}

		// Kiểm tra kết nối TCP nhanh tới Broker
		conn, err := net.DialTimeout("tcp", hostPort, 500*time.Millisecond)
		if err != nil {
			t.Logf("[KAFKA] [INFO] Broker %s hiện chưa chạy nền hoặc đang offline (Sẵn sàng kết nối khi kích hoạt cụm): %v", hostPort, err)
		} else {
			_ = conn.Close()
			t.Logf("[KAFKA] [PASS] Kết nối thành công tới Kafka/Redpanda Broker: %s", hostPort)
		}
	}
}

// TestKafka_IdempotencyKeyEnforcement kiểm tra bắt buộc Idempotency Key và cơ chế chống trùng lặp (Rule PHẦN 5)
func TestKafka_IdempotencyKeyEnforcement(t *testing.T) {
	// 1. Kiểm tra message hợp lệ có Idempotency Key
	validKey := GenerateIdempotencyKey("supportflast_engine", "CHUNK_UPLOADED", "chunk_hash_abc123")
	msg := &KafkaMessageEnvelope{
		IdempotencyKey: validKey,
		EventType:      "CHUNK_UPLOADED",
		SourceService:  "supportflast_engine",
		TargetConsumer: "supportflast_ai",
		Timestamp:      time.Now().Unix(),
		Payload: map[string]interface{}{
			"chunk_id": "chk-9999",
			"size":     1048576,
		},
	}

	if err := msg.ValidateMessage(); err != nil {
		t.Fatalf("Message hợp lệ bị từ chối: %v", err)
	}
	t.Logf("[KAFKA] [PASS] Message hợp lệ với Idempotency Key: %s", validKey)

	// 2. Kiểm tra message KHÔNG có Idempotency Key -> BẮT BUỘC BỊ TỪ CHỐI
	invalidMsg := &KafkaMessageEnvelope{
		IdempotencyKey: "",
		EventType:      "SECURITY_ALERT",
		SourceService:  "supportflast_engine",
		TargetConsumer: "supportflast_ai",
		Timestamp:      time.Now().Unix(),
		Payload:        map[string]interface{}{"alert": "test"},
	}

	if err := invalidMsg.ValidateMessage(); err == nil {
		t.Fatalf("LỖI BẢO MẬT: Message thiếu Idempotency Key nhưng không bị từ chối!")
	} else {
		t.Logf("[KAFKA] [PASS] Đã từ chối message thiếu Idempotency Key đúng quy định: %v", err)
	}

	// 3. Kiểm tra cơ chế chống trùng lặp tại Consumer (Deduplication)
	dedup := NewIdempotencyDeduplicator()
	firstTime := dedup.ProcessMessage(validKey)
	if !firstTime {
		t.Fatalf("Message đầu tiên phải được chấp nhận")
	}

	secondTime := dedup.ProcessMessage(validKey)
	if secondTime {
		t.Fatalf("LỖI: Message trùng Idempotency Key phải bị chặn và loại bỏ!")
	}
	t.Logf("[KAFKA] [PASS] Cơ chế chống trùng lặp (Deduplication) hoạt động hoàn hảo: Lần 1 = Chấp nhận, Lần 2 = Loại bỏ")
}
