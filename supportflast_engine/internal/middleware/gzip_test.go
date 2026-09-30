package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGzipMiddleware_NoAcceptEncoding(t *testing.T) {
	largeData := strings.Repeat("{\"message\": \"hello world!\"}\n", 100) // ~2.8KB
	handler := Gzip(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(largeData))
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") != "" {
		t.Errorf("expected no Content-Encoding, got %s", rec.Header().Get("Content-Encoding"))
	}
	if rec.Body.String() != largeData {
		t.Errorf("expected exact body match without compression")
	}
}

func TestGzipMiddleware_SmallPayloadNoCompress(t *testing.T) {
	smallData := "{\"status\": \"ok\"}" // ~16 bytes (< 1KB)
	handler := Gzip(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(smallData))
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") != "" {
		t.Errorf("expected no Content-Encoding for small payload <= 1KB, got %s", rec.Header().Get("Content-Encoding"))
	}
	if rec.Body.String() != smallData {
		t.Errorf("expected small body uncompressed, got %s", rec.Body.String())
	}
}

func TestGzipMiddleware_LargePayloadCompressed(t *testing.T) {
	largeData := strings.Repeat("{\"id\": 1, \"name\": \"Nguyen Van A\", \"role\": \"developer\"}\n", 100) // ~6KB
	handler := Gzip(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(largeData))
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected Content-Encoding 'gzip', got '%s'", rec.Header().Get("Content-Encoding"))
	}
	if !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
		t.Errorf("expected Vary header to contain Accept-Encoding, got '%s'", rec.Header().Get("Vary"))
	}

	// Decompress and verify content
	gr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer gr.Close()

	decompressed, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("failed to read decompressed data: %v", err)
	}

	if string(decompressed) != largeData {
		t.Errorf("decompressed content does not match original data")
	}
}

func TestGzipMiddleware_IgnoredMediaTypes(t *testing.T) {
	largeBinary := bytes.Repeat([]byte{0x00, 0x01, 0x02, 0x03}, 1000) // 4KB
	typesToSkip := []string{
		"application/octet-stream",
		"video/mp4",
		"audio/mpeg",
		"image/jpeg",
		"image/png",
		"application/zip",
	}

	for _, ct := range typesToSkip {
		t.Run(ct, func(t *testing.T) {
			handler := Gzip(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", ct)
				_, _ = w.Write(largeBinary)
			}))

			req := httptest.NewRequest(http.MethodGet, "/media/file", nil)
			req.Header.Set("Accept-Encoding", "gzip")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Header().Get("Content-Encoding") != "" {
				t.Errorf("Content-Type %s should NOT be gzipped, but got Content-Encoding: %s", ct, rec.Header().Get("Content-Encoding"))
			}
			if !bytes.Equal(rec.Body.Bytes(), largeBinary) {
				t.Errorf("binary content modified unexpectedly for %s", ct)
			}
		})
	}
}

func TestGzipMiddleware_SpecialStatusCodes(t *testing.T) {
	// Status 204 No Content
	t.Run("Status 204", func(t *testing.T) {
		handler := Gzip(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		req := httptest.NewRequest(http.MethodDelete, "/api/item", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Errorf("expected 204, got %d", rec.Code)
		}
		if rec.Header().Get("Content-Encoding") != "" {
			t.Errorf("status 204 should not have Content-Encoding")
		}
	})

	// Status 206 Partial Content (Range request)
	t.Run("Status 206 Range", func(t *testing.T) {
		largeRange := strings.Repeat("A", 4096)
		handler := Gzip(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte(largeRange))
		}))
		req := httptest.NewRequest(http.MethodGet, "/video/stream", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		req.Header.Set("Range", "bytes=0-4095")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusPartialContent {
			t.Errorf("expected 206, got %d", rec.Code)
		}
		if rec.Header().Get("Content-Encoding") != "" {
			t.Errorf("status 206 Partial Content should NOT be gzipped")
		}
	})
}
