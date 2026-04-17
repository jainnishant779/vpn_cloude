package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuickConnectHandlerGetLocalhostSuccess(t *testing.T) {
	handler := NewQuickConnectHandler(&QuickConnectBootstrap{
		Enabled:     true,
		OwnerEmail:  "quickconnect@quicktunnel.local",
		NetworkID:   "abc123def456",
		NetworkName: "Quick Connect Network",
		APIKey:      "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		CIDR:        "10.7.0.0/16",
		MaxPeers:    25,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/quick-connect", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	rec := httptest.NewRecorder()

	handler.Get(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var envelope map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	assert.Equal(t, true, envelope["success"])

	data, ok := envelope["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "abc123def456", data["network_id"])
	assert.NotEmpty(t, data["api_key"])
}

func TestQuickConnectHandlerGetRejectsRemote(t *testing.T) {
	handler := NewQuickConnectHandler(&QuickConnectBootstrap{Enabled: true})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/quick-connect", nil)
	req.RemoteAddr = "203.0.113.10:50000"
	rec := httptest.NewRecorder()

	handler.Get(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestQuickConnectHandlerGetDisabled(t *testing.T) {
	handler := NewQuickConnectHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/quick-connect", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	rec := httptest.NewRecorder()

	handler.Get(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
