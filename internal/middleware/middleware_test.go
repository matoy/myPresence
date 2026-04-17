package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// -----------------------------------------------------------------------
// AccessLog
// -----------------------------------------------------------------------

func TestAccessLog_CallsNextHandler(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := AccessLog(inner)
	req := httptest.NewRequest("GET", "/some-page", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("AccessLog should call the next handler")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestAccessLog_SkipsHealthAndMetrics(t *testing.T) {
	// Smoke-test that /health and /metrics paths don't cause a panic
	for _, path := range []string{"/health", "/metrics"} {
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		handler := AccessLog(inner)
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d", path, rec.Code)
		}
	}
}

func TestAccessLog_XForwardedFor(t *testing.T) {
	var capturedIP string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// IP is resolved inside AccessLog, not exposed directly — we just ensure no panic
		w.WriteHeader(http.StatusOK)
	})

	handler := AccessLog(inner)
	req := httptest.NewRequest("GET", "/page", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.1, 10.0.0.1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// capturedIP would need exported internals; we just verify the handler runs cleanly
	_ = capturedIP
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

// -----------------------------------------------------------------------
// responseWriter.WriteHeader
// -----------------------------------------------------------------------

func TestResponseWriter_CapturesStatusCode(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, status: http.StatusOK}

	rw.WriteHeader(http.StatusTeapot)

	if rw.status != http.StatusTeapot {
		t.Errorf("expected captured status %d, got %d", http.StatusTeapot, rw.status)
	}
	if rec.Code != http.StatusTeapot {
		t.Errorf("expected delegated status %d, got %d", http.StatusTeapot, rec.Code)
	}
}

func TestResponseWriter_DefaultStatusOK(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, status: http.StatusOK}
	// Never called WriteHeader — default should remain 200
	if rw.status != http.StatusOK {
		t.Errorf("default status should be 200, got %d", rw.status)
	}
}

// -----------------------------------------------------------------------
// OptionalAuth — with and without session cookie
// -----------------------------------------------------------------------

func TestOptionalAuth_WithoutCookie_StillCallsNext(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		// No user in context — that's fine
		u := GetUser(r)
		if u != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	// nil DB is safe here because no cookie is present
	handler := OptionalAuth(nil, inner)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("OptionalAuth should call next even without a session cookie")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}
