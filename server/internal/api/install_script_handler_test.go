package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

func TestInstallScriptHandlerServeJoinReturnsScript(t *testing.T) {
	handler := NewInstallScriptHandler("http://demo.example:3000")

	router := chi.NewRouter()
	router.Get("/join/{network_id}", handler.ServeJoin)

	req := httptest.NewRequest(http.MethodGet, "/join/test-network", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))

	body := rec.Body.String()
	require.True(t, strings.HasPrefix(body, "#!/usr/bin/env bash"))
	require.Contains(t, body, `SERVER_URL="http://demo.example:3000"`)
	require.Contains(t, body, `NETWORK_ID="test-network"`)
	require.Contains(t, body, `DOWNLOAD_URL="$SERVER_URL/api/v1/downloads/client/$OS/$ARCH"`)
	require.Contains(t, body, `exec "$BINARY" join "$SERVER_URL" "$NETWORK_ID"`)
}

func TestInstallScriptHandlerServeScriptFallsBackToRequestHost(t *testing.T) {
	handler := NewInstallScriptHandler("")

	req := httptest.NewRequest(http.MethodGet, "http://vpn.example/install.sh", nil)
	rec := httptest.NewRecorder()

	handler.ServeScript(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, `SERVER_URL="http://vpn.example"`)
	require.Contains(t, body, `curl http://vpn.example/install.sh | sudo bash -s -- <network_id>`)
	require.Contains(t, body, `curl http://vpn.example/join/<network_id> | sudo bash`)
}
