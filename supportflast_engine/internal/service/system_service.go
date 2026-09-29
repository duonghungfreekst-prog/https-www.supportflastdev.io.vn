package service

import "fmt"

// HealthInfo chứa thông tin sức khỏe hệ thống
type HealthInfo struct {
	Status string `json:"status"`
	Uptime string `json:"uptime"`
}

// StatusInfo chứa thông tin trạng thái hệ thống
type StatusInfo struct {
	Version   string `json:"version"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
	NumCPU    int    `json:"num_cpu"`
}

// DiagnosticsInfo chứa thông tin chẩn đoán hệ thống
type DiagnosticsInfo struct {
	MemoryUsageMB float64 `json:"memory_usage_mb"`
	NumGoroutine  int     `json:"num_goroutine"`
	DBStatus      string  `json:"db_status"`
}

// SystemService định nghĩa các operations giám sát hệ thống
type SystemService interface {
	GetHealth() (*HealthInfo, error)
	GetStatus() (*StatusInfo, error)
	GetDiagnostics() (*DiagnosticsInfo, error)
}

// systemServiceImpl là implementation chính
type systemServiceImpl struct{}

// NewSystemService tạo system service (không cần dependency)
func NewSystemService() SystemService {
	return &systemServiceImpl{}
}

// GetHealth trả về thông tin sức khỏe hệ thống (stub Phase 1)
func (s *systemServiceImpl) GetHealth() (*HealthInfo, error) {
	return nil, fmt.Errorf("system: GetHealth chưa được triển khai")
}

// GetStatus trả về trạng thái hệ thống (stub Phase 1)
func (s *systemServiceImpl) GetStatus() (*StatusInfo, error) {
	return nil, fmt.Errorf("system: GetStatus chưa được triển khai")
}

// GetDiagnostics trả về thông tin chẩn đoán hệ thống (stub Phase 1)
func (s *systemServiceImpl) GetDiagnostics() (*DiagnosticsInfo, error) {
	return nil, fmt.Errorf("system: GetDiagnostics chưa được triển khai")
}
