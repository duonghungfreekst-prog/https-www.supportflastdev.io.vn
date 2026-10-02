package registry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGitStatusHandler(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/git/status", nil)
	adminUser := User{
		ID:       "usr-admin-001",
		Username: "admin",
		Role:     "admin",
	}
	adminToken, err := IssueRS256Token(adminUser, time.Hour)
	if err != nil {
		t.Fatalf("Failed to generate admin token: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)
	w := httptest.NewRecorder()

	GitStatusHandler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", resp.StatusCode)
	}

	var data GitStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if data.Status != "success" {
		t.Errorf("Expected status success, got %s", data.Status)
	}
	if data.Branch == "" {
		t.Errorf("Expected branch name, got empty")
	}
	t.Logf("Git status test passed: branch=%s, clean=%v, last_commit=%s", data.Branch, data.IsClean, data.LastCommit.Hash)
}
