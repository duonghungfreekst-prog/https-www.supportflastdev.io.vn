package middleware

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestChainOrder kiểm tra Chain() áp dụng middleware theo đúng thứ tự (đầu tiên = ngoài cùng)
func TestChainOrder(t *testing.T) {
	var order []string

	mw1 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "mw1-before")
			next.ServeHTTP(w, r)
			order = append(order, "mw1-after")
		})
	}

	mw2 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "mw2-before")
			next.ServeHTTP(w, r)
			order = append(order, "mw2-after")
		})
	}

	mw3 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "mw3-before")
			next.ServeHTTP(w, r)
			order = append(order, "mw3-after")
		})
	}

	core := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "core")
		w.WriteHeader(http.StatusOK)
	})

	handler := Chain(core, Middleware(mw1), Middleware(mw2), Middleware(mw3))

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	expected := []string{"mw1-before", "mw2-before", "mw3-before", "core", "mw3-after", "mw2-after", "mw1-after"}
	if len(order) != len(expected) {
		t.Fatalf("expected %d calls, got %d: %v", len(expected), len(order), order)
	}
	for i, v := range expected {
		if order[i] != v {
			t.Errorf("position %d: expected %q, got %q", i, v, order[i])
		}
	}
}

// TestWrapFunc kiểm tra WrapFunc chuyển đổi đúng kiểu
func TestWrapFunc(t *testing.T) {
	called := false
	fn := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			next.ServeHTTP(w, r)
		})
	}

	mw := WrapFunc(fn)
	core := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := mw(core)
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("WrapFunc middleware was not called")
	}
}

// TestRecoveryMiddleware kiểm tra Recovery bắt panic và trả 500
func TestRecoveryMiddleware(t *testing.T) {
	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	handler := Recovery(panicHandler)
	req := httptest.NewRequest("GET", "/panic", nil)
	rr := httptest.NewRecorder()

	// Không được panic ra ngoài
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "Internal server error") {
		t.Errorf("expected error message in body, got %q", body)
	}
}

// TestAccessLogMiddleware kiểm tra AccessLog ghi log đúng format
func TestAccessLogMiddleware(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(nil)

	core := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	handler := AccessLog(core)
	req := httptest.NewRequest("POST", "/api/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	logOutput := buf.String()
	if !strings.Contains(logOutput, "[ENGINE] [ACCESS]") {
		t.Errorf("expected [ENGINE] [ACCESS] in log, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "POST") {
		t.Errorf("expected POST method in log, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "/api/test") {
		t.Errorf("expected /api/test path in log, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "201") {
		t.Errorf("expected status 201 in log, got %q", logOutput)
	}
}

// TestChainEmpty kiểm tra Chain() với 0 middleware trả handler gốc
func TestChainEmpty(t *testing.T) {
	called := false
	core := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := Chain(core)
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("core handler was not called with empty chain")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}
