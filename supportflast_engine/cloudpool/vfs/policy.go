package vfs

import (
	"errors"
	"sort"
	"strings"
	"time"

	"supportflast_engine/cloudpool/models"
)

// ExcludedUploadAccounts danh sách các tài khoản Google Drive tuyệt đối KHÔNG được lưu trữ tệp mới khi upload.
// Tuân thủ triệt để yêu cầu hệ thống nhằm bảo vệ tài khoản quản trị và tài khoản backup.
var ExcludedUploadAccounts = map[string]bool{
	"duongmanhhung9900@gmail.com":     true,
	"duongmanhhunghospitol@gmail.com": true,
	"duongmanhhunghospital@gmail.com": true, // Bảo vệ cả biến thể chính tả
	"phephabaylac@gmail.com":          true,
}

// ExcludedUploadAccountIDs danh sách ID tương ứng của các tài khoản bị cấm
var ExcludedUploadAccountIDs = map[string]bool{
	"acc_18cf4b1b8adc5be4": true, // duongmanhhung9900@gmail.com
	"acc_18d9741a9a288708": true, // duongmanhhunghospitol@gmail.com
	"acc_18cf4b57bb6f3d58": true, // phephabaylac@gmail.com
}

// IsExcludedUploadAccount kiểm tra một tài khoản Google Drive có thuộc diện loại trừ khi upload hay không.
// Đảm bảo không có bất kỳ chunk mới nào được ghi vào các tài khoản này.
func IsExcludedUploadAccount(a *models.Account) bool {
	if a == nil {
		return true
	}

	emailClean := strings.ToLower(strings.TrimSpace(a.Email))
	if ExcludedUploadAccounts[emailClean] {
		return true
	}

	if a.ID != "" && ExcludedUploadAccountIDs[a.ID] {
		return true
	}

	if a.IsUploadExcludedAccount() || a.IsUploadExcluded {
		return true
	}

	// Quét thêm trường Name phòng trường hợp email nằm trong tên tài khoản
	nameClean := strings.ToLower(strings.TrimSpace(a.Name))
	for bannedEmail := range ExcludedUploadAccounts {
		if strings.Contains(nameClean, bannedEmail) || strings.Contains(emailClean, bannedEmail) {
			return true
		}
	}

	return false
}

// IsExcludedEmail kiểm tra một địa chỉ email có nằm trong danh sách cấm lưu trữ upload hay không
func IsExcludedEmail(email string) bool {
	emailClean := strings.ToLower(strings.TrimSpace(email))
	if ExcludedUploadAccounts[emailClean] {
		return true
	}
	for banned := range ExcludedUploadAccounts {
		if strings.Contains(emailClean, banned) {
			return true
		}
	}
	return false
}

// IsRateLimitError nhận diện xem lỗi trả về từ Google Drive API có phải do giới hạn tần suất (Rate Limit) hay không.
func IsRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	rateLimitKeywords := []string{
		"429",
		"ratelimitexceeded",
		"rate_limit_exceeded",
		"rate limit exceeded",
		"userratelimitexceeded",
		"user_rate_limit_exceeded",
		"quotaexceeded",
		"quota_exceeded",
		"dailylimitexceeded",
		"userratelimitexceededunreg",
		"bandwidthlimitexceeded",
		"too many requests",
		"resource_exhausted",
		"resourceexhausted",
	}
	for _, kw := range rateLimitKeywords {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}

// MarkAccountRateLimited đánh dấu một tài khoản bị rate-limit và đặt thời gian cooldown tạm thời
func (v *VFS) MarkAccountRateLimited(accountID string, duration time.Duration) {
	if accountID == "" {
		return
	}
	if duration <= 0 {
		duration = 60 * time.Second
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.rateLimits == nil {
		v.rateLimits = make(map[string]time.Time)
	}
	v.rateLimits[accountID] = time.Now().Add(duration)
}

// IsAccountRateLimited kiểm tra xem tài khoản có đang trong thời gian cooldown rate-limit hay không
func (v *VFS) IsAccountRateLimited(accountID string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.isRateLimitedLocked(accountID)
}

// isRateLimitedLocked kiểm tra cooldown bên trong lock
func (v *VFS) isRateLimitedLocked(accountID string) bool {
	if v.rateLimits == nil {
		return false
	}
	expiry, exists := v.rateLimits[accountID]
	if !exists {
		return false
	}
	if time.Now().Before(expiry) {
		return true
	}
	// Đã hết thời gian cooldown, tự động xóa
	delete(v.rateLimits, accountID)
	return false
}

// ClearAccountRateLimit xóa trạng thái rate-limit của tài khoản
func (v *VFS) ClearAccountRateLimit(accountID string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.rateLimits != nil {
		delete(v.rateLimits, accountID)
	}
}

// PickAccount lựa chọn tài khoản Google Drive tối ưu cho chunk tiếp theo.
// Tuân thủ tuyệt đối quy tắc loại trừ 3 tài khoản quản trị/backup, tự động né rate-limit và hỗ trợ failover.
func (v *VFS) PickAccount(strategy string, requiredBytes int64, excludeAccountIDs ...string) (*models.Account, error) {
	return v.SelectAccountForChunk(strategy, requiredBytes, excludeAccountIDs...)
}

// SelectAccountForChunk chooses a Google Drive account based on the configured allocation strategy,
// accounting for in-flight reserved quota, rate-limit cooldowns, and strictly excluding banned accounts.
func (v *VFS) SelectAccountForChunk(strategy string, requiredBytes int64, excludeAccountIDs ...string) (*models.Account, error) {
	accounts, err := v.db.ListAccounts()
	if err != nil {
		return nil, err
	}

	excludeMap := make(map[string]bool)
	for _, id := range excludeAccountIDs {
		if id != "" {
			excludeMap[id] = true
		}
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	var healthyAccounts []models.Account
	var rateLimitedAccounts []models.Account

	for _, a := range accounts {
		// 1. Kiểm tra trạng thái hoạt động
		if a.Status != "active" {
			continue
		}

		// 2. Kiểm tra danh sách loại trừ tạm thời (vd failover trong phiên upload hiện tại)
		if excludeMap[a.ID] {
			continue
		}

		// 3. TUÂN THỦ TUYỆT ĐỐI: NÉ VÀ LOẠI TRỪ 3 TÀI KHOẢN BỊ CẤM
		// (duongmanhhung9900@gmail.com, duongmanhhunghospitol@gmail.com, phephabaylac@gmail.com)
		if IsExcludedUploadAccount(&a) {
			continue
		}

		// 4. Tính dung lượng ảo khả dụng: trừ đi dung lượng in-flight đang chờ upload
		inflight := int64(0)
		if v.inFlightQuota != nil {
			inflight = v.inFlightQuota[a.ID]
		}
		effectiveFree := a.FreeQuotaBytes - inflight
		if effectiveFree < requiredBytes {
			continue
		}

		accCopy := a
		accCopy.FreeQuotaBytes = effectiveFree

		// 5. Phân nhóm theo trạng thái Rate Limit
		if v.isRateLimitedLocked(a.ID) {
			rateLimitedAccounts = append(rateLimitedAccounts, accCopy)
		} else {
			healthyAccounts = append(healthyAccounts, accCopy)
		}
	}

	// Ưu tiên các tài khoản hoàn toàn khỏe mạnh, không bị rate-limit
	activeAccounts := healthyAccounts
	if len(activeAccounts) == 0 {
		// Nếu không còn tài khoản khỏe mạnh nào, nhưng có tài khoản đang trong cooldown:
		// nếu tất cả đều bị rate-limit thì không thể tải lên, báo lỗi để worker backoff retry
		if len(rateLimitedAccounts) > 0 {
			return nil, errors.New("tất cả tài khoản Google Drive khả dụng hiện đang bị giới hạn tần suất (rate-limit), vui lòng thử lại sau")
		}
		return nil, errors.New("không có tài khoản Google Drive nào còn đủ dung lượng khả dụng")
	}

	normStrategy := strings.ToLower(strings.TrimSpace(strategy))
	normStrategy = strings.ReplaceAll(normStrategy, "-", "_")

	switch normStrategy {
	case "waterfill":
		// Rót đầy tuần tự từng tài khoản
		chosen := activeAccounts[0]
		return &chosen, nil

	case "round_robin":
		// Luân phiên vòng tròn đều đặn
		if v.rrIndex >= len(activeAccounts) {
			v.rrIndex = 0
		}
		chosen := activeAccounts[v.rrIndex]
		v.rrIndex = (v.rrIndex + 1) % len(activeAccounts)
		return &chosen, nil

	case "least_used", "balanced", "scatter", "":
		fallthrough
	default:
		// Mặc định: Least-Used kết hợp phân tán luân phiên (stripe/scatter)
		// Sắp xếp giảm dần theo dung lượng ảo còn trống (nhiều chỗ trống nhất lên đầu)
		sort.Slice(activeAccounts, func(i, j int) bool {
			return activeAccounts[i].FreeQuotaBytes > activeAccounts[j].FreeQuotaBytes
		})

		// Hỗ trợ phân tán đều chunk đa ổ đĩa (Striping):
		// Nếu có nhiều tài khoản dồi dào dung lượng khả dụng (>= requiredBytes*5 và >= maxFree/5),
		// luân phiên phân bổ giữa các tài khoản đó để các chunk liên tiếp của một tệp lớn
		// được phân tán song song qua nhiều tài khoản Google Drive khác nhau.
		if len(activeAccounts) > 1 {
			maxFree := activeAccounts[0].FreeQuotaBytes
			var candidateCount int
			for _, a := range activeAccounts {
				if a.FreeQuotaBytes >= requiredBytes*5 && a.FreeQuotaBytes >= maxFree/5 {
					candidateCount++
				} else {
					break
				}
			}
			if candidateCount > 1 {
				idx := v.rrIndex % candidateCount
				v.rrIndex = (v.rrIndex + 1) % candidateCount
				chosen := activeAccounts[idx]
				return &chosen, nil
			}
		}
		chosen := activeAccounts[0]
		return &chosen, nil
	}
}
