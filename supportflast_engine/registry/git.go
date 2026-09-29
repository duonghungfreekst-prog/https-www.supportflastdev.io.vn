package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"supportflast_engine/security"
)

var (
	gitSyncMutex   sync.Mutex
	gitIsSyncing   bool
	gitLastSyncTime time.Time
	gitLastSyncMsg  string
)

// getGitRepoRoot tìm đường dẫn thư mục gốc chứa .git
func getGitRepoRoot() string {
	// Kiểm tra biến môi trường hoặc thư mục làm việc
	if root := os.Getenv("WORKSPACE_DIR"); root != "" {
		if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
			return root
		}
	}
	dir, err := os.Getwd()
	if err == nil {
		for {
			if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "F:\\supportflast.dev"
}

// runGitCommand thực thi lệnh git trong thư mục gốc của repository
func runGitCommand(ctx context.Context, args ...string) (string, error) {
	repoDir := getGitRepoRoot()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoDir
	// Tránh git treo chờ nhập tài khoản mật khẩu trên terminal và đảm bảo định danh khi chạy dưới dịch vụ Windows SYSTEM
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=duonghungfreekst-prog",
		"GIT_AUTHOR_EMAIL=duonghungfreekst-prog@users.noreply.github.com",
		"GIT_COMMITTER_NAME=duonghungfreekst-prog",
		"GIT_COMMITTER_EMAIL=duonghungfreekst-prog@users.noreply.github.com",
	)

	output, err := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(output))
	if err != nil {
		return outStr, fmt.Errorf("git %s thất bại: %w (output: %s)", strings.Join(args, " "), err, outStr)
	}
	return outStr, nil
}

// GitStatusResponse chứa thông tin trạng thái Git hiện tại
type GitStatusResponse struct {
	Status            string    `json:"status"`
	Repository        string    `json:"repository"`
	Branch            string    `json:"branch"`
	IsClean           bool      `json:"is_clean"`
	HasChanges        bool      `json:"has_changes"`
	ChangedFilesCount int       `json:"changed_files_count"`
	ChangedFiles      []string  `json:"changed_files"`
	LastCommit        struct {
		Hash    string `json:"hash"`
		Author  string `json:"author"`
		Date    string `json:"date"`
		Subject string `json:"subject"`
	} `json:"last_commit"`
	LastSyncTime string `json:"last_sync_time,omitempty"`
	LastSyncMsg  string `json:"last_sync_msg,omitempty"`
	IsSyncing    bool   `json:"is_syncing"`
	Timestamp    string `json:"timestamp"`
}

// GitStatusHandler trả về trạng thái Git hiện tại (GET /api/git/status hoặc /api/admin/git/status)
func GitStatusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var resp GitStatusResponse
	resp.Status = "success"
	resp.Timestamp = time.Now().Format(time.RFC3339)
	resp.IsSyncing = gitIsSyncing
	if !gitLastSyncTime.IsZero() {
		resp.LastSyncTime = gitLastSyncTime.Format(time.RFC3339)
		resp.LastSyncMsg = gitLastSyncMsg
	}

	// 1. Lấy thông tin Remote URL
	if remURL, err := runGitCommand(ctx, "remote", "get-url", "origin"); err == nil {
		resp.Repository = remURL
	} else {
		resp.Repository = "https://github.com/duonghungfreekst-prog/https-www.supportflastdev.io.vn"
	}

	// 2. Lấy nhánh hiện tại
	if branch, err := runGitCommand(ctx, "branch", "--show-current"); err == nil && branch != "" {
		resp.Branch = branch
	} else {
		resp.Branch = "main"
	}

	// 3. Kiểm tra thay đổi chưa commit
	statusOut, _ := runGitCommand(ctx, "status", "--porcelain")
	if strings.TrimSpace(statusOut) == "" {
		resp.IsClean = true
		resp.HasChanges = false
		resp.ChangedFilesCount = 0
		resp.ChangedFiles = []string{}
	} else {
		lines := strings.Split(statusOut, "\n")
		var files []string
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if l != "" {
				files = append(files, l)
			}
		}
		resp.IsClean = false
		resp.HasChanges = true
		resp.ChangedFilesCount = len(files)
		resp.ChangedFiles = files
	}

	// 4. Lấy commit gần nhất
	if logOut, err := runGitCommand(ctx, "log", "-1", "--pretty=format:%h|%an|%ad|%s", "--date=iso"); err == nil {
		parts := strings.Split(logOut, "|")
		if len(parts) >= 4 {
			resp.LastCommit.Hash = parts[0]
			resp.LastCommit.Author = parts[1]
			resp.LastCommit.Date = parts[2]
			resp.LastCommit.Subject = parts[3]
		}
	}

	json.NewEncoder(w).Encode(resp)
}

// GitSyncRequest tham số tùy chọn khi gọi API đồng bộ
type GitSyncRequest struct {
	Message string `json:"message"`
	Force   bool   `json:"force"`
}

// GitSyncHandler xử lý kích hoạt tự động đồng bộ lên GitHub (POST /api/git/sync hoặc /api/admin/git/sync)
func GitSyncHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed, use POST"}`, http.StatusMethodNotAllowed)
		return
	}

	// Kiểm tra xem có tiến trình đồng bộ nào đang chạy không
	gitSyncMutex.Lock()
	if gitIsSyncing {
		gitSyncMutex.Unlock()
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "busy",
			"error":  "Đang có tiến trình đồng bộ Git khác đang chạy, vui lòng chờ.",
		})
		return
	}
	gitIsSyncing = true
	gitSyncMutex.Unlock()

	defer func() {
		gitSyncMutex.Lock()
		gitIsSyncing = false
		gitSyncMutex.Unlock()
	}()

	var req GitSyncRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	log.Println("[ENGINE] [GIT] Bắt đầu quy trình tự động đồng bộ lên GitHub...")

	// 1. Kiểm tra trạng thái Git
	statusOut, _ := runGitCommand(ctx, "status", "--porcelain")
	var committed bool
	var commitHash string

	if strings.TrimSpace(statusOut) != "" {
		// Stage tất cả tệp tin hợp lệ
		if _, err := runGitCommand(ctx, "add", "."); err != nil {
			log.Printf("[ENGINE] [GIT] Lỗi git add: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "error",
				"error":  fmt.Sprintf("Lỗi thêm tệp tin vào Git: %v", err),
			})
			return
		}

		// Tạo commit
		commitMsg := req.Message
		if strings.TrimSpace(commitMsg) == "" {
			commitMsg = fmt.Sprintf("auto-update: Đồng bộ qua SupportFlast Web API lúc %s", time.Now().Format("2006-01-02 15:04:05"))
		} else {
			commitMsg = fmt.Sprintf("%s (%s)", commitMsg, time.Now().Format("2006-01-02 15:04:05"))
		}

		if _, err := runGitCommand(ctx, "commit", "-m", commitMsg); err != nil {
			log.Printf("[ENGINE] [GIT] Lỗi git commit: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "error",
				"error":  fmt.Sprintf("Lỗi tạo commit: %v", err),
			})
			return
		}
		committed = true
	}

	// 2. Kéo rebase nhẹ nhàng từ remote
	_, _ = runGitCommand(ctx, "pull", "--rebase", "origin", "main")

	// 3. Đẩy lên GitHub
	pushArgs := []string{"push", "-u", "origin", "main"}
	if req.Force {
		pushArgs = append(pushArgs, "--force")
	}

	pushOut, err := runGitCommand(ctx, pushArgs...)
	if err != nil {
		log.Printf("[ENGINE] [GIT] Lỗi git push: %v (out: %s)", err, pushOut)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "error",
			"error":  fmt.Sprintf("Lỗi đẩy lên GitHub: %v", err),
			"output": pushOut,
		})
		return
	}

	// 4. Lấy hash commit hiện tại
	if h, err := runGitCommand(ctx, "rev-parse", "--short", "HEAD"); err == nil {
		commitHash = h
	}

	now := time.Now()
	gitLastSyncTime = now
	if committed {
		gitLastSyncMsg = fmt.Sprintf("Đã tạo commit %s và đẩy lên GitHub thành công", commitHash)
	} else {
		gitLastSyncMsg = "Kho chứa đã ở trạng thái mới nhất, đã kiểm tra và đồng bộ hoàn tất"
	}

	clientIP := security.GetRealClientIP(r)
	log.Printf("[ENGINE] [GIT] Đồng bộ GitHub thành công bởi IP: %s (Commit: %s)", clientIP, commitHash)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "success",
		"message":     gitLastSyncMsg,
		"committed":   committed,
		"commit_hash": commitHash,
		"repository":  "https://github.com/duonghungfreekst-prog/https-www.supportflastdev.io.vn",
		"branch":      "main",
		"output":      pushOut,
		"timestamp":   now.Format(time.RFC3339),
	})
}
