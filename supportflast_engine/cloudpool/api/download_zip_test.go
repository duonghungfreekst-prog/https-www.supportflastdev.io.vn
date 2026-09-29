package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestFormatContentDisposition(t *testing.T) {
	tests := []struct {
		name            string
		dispositionType string
		filename        string
		expectedInASCII string
		expectedRFC     string
	}{
		{
			name:            "Vietnamese with diacritics",
			dispositionType: "attachment",
			filename:        "Tài liệu hướng dẫn.pdf",
			expectedInASCII: `filename="Tai lieu huong dan.pdf"`,
			expectedRFC:     `filename*=UTF-8''T%C3%A0i%20li%E1%BB%87u%20h%C6%B0%E1%BB%9Bng%20d%E1%BA%ABn.pdf`,
		},
		{
			name:            "Vietnamese folder zip",
			dispositionType: "attachment",
			filename:        "Hình ảnh & Video.zip",
			expectedInASCII: `filename="Hinh anh & Video.zip"`,
			expectedRFC:     `filename*=UTF-8''H%C3%ACnh%20%E1%BA%A3nh%20&%20Video.zip`,
		},
		{
			name:            "Inline with special characters and CRLF protection",
			dispositionType: "inline",
			filename:        "Bản vẽ \"Thiết kế\"\r\n2026.png",
			expectedInASCII: `inline; filename="Ban ve _Thiet ke_2026.png"`,
			expectedRFC:     `filename*=UTF-8''B%E1%BA%A3n%20v%E1%BA%BD%20_Thi%E1%BA%BFt%20k%E1%BA%BF_2026.png`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatContentDisposition(tc.dispositionType, tc.filename)

			if !strings.Contains(got, tc.expectedInASCII) {
				t.Errorf("Expected fallback ASCII %q in %q", tc.expectedInASCII, got)
			}
			if !strings.Contains(got, tc.expectedRFC) {
				t.Errorf("Expected RFC 5987 %q in %q", tc.expectedRFC, got)
			}
			if strings.Contains(got, "\r") || strings.Contains(got, "\n") {
				t.Errorf("CRLF characters found in header: %q", got)
			}

			// Verify that RFC 5987 portion correctly unescapes to cleaned filename
			parts := strings.Split(got, "filename*=UTF-8''")
			if len(parts) != 2 {
				t.Fatalf("Header does not have proper RFC 5987 syntax: %s", got)
			}
			unescaped, err := url.PathUnescape(parts[1])
			if err != nil {
				t.Fatalf("Failed to unescape RFC 5987 filename: %v", err)
			}
			expectedClean := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(tc.filename, "\r", ""), "\n", ""), `"`, `_`)
			if unescaped != expectedClean {
				t.Errorf("Unescaped RFC 5987 name %q != expected clean %q", unescaped, expectedClean)
			}
		})
	}
}

func TestDownloadZipValidation(t *testing.T) {
	srv := &Server{}
	req, _ := http.NewRequest("GET", "/api/files/download-zip", nil)
	rr := httptest.NewRecorder()

	srv.handleDownloadZip(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 Bad Request for missing params, got %d", rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "folder_id") && !strings.Contains(body, "file_ids") {
		t.Errorf("Expected error message mentioning folder_id or file_ids, got: %s", body)
	}
}

