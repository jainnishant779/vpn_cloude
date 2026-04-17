package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRealIPTrustsConfiguredProxyHeaders(t *testing.T) {
	mw := RealIP([]string{"127.0.0.1"})

	var observed string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed = r.RemoteAddr
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "198.51.100.10")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if observed != "198.51.100.10:0" {
		t.Fatalf("expected forwarded ip to be trusted, got %q", observed)
	}
}

func TestRealIPIgnoresSpoofedHeadersFromUntrustedRemote(t *testing.T) {
	mw := RealIP([]string{"127.0.0.1"})

	var observed string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed = r.RemoteAddr
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.10:12345"
	req.Header.Set("X-Real-IP", "127.0.0.1")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if observed != "203.0.113.10:12345" {
		t.Fatalf("expected direct remote addr to be preserved, got %q", observed)
	}
}

func TestRemoteIPKeyUsesNormalizedRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.50:4321"
	req.Header.Set("X-Forwarded-For", "127.0.0.1")

	if got := RemoteIPKey(req); got != "198.51.100.50" {
		t.Fatalf("RemoteIPKey() = %q, want %q", got, "198.51.100.50")
	}
}
