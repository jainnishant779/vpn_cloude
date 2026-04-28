package api

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeRelayEndpointCandidateRejectsInternalHost(t *testing.T) {
	_, ok := normalizeRelayEndpointCandidate("relay:3478", 3478, true)
	require.False(t, ok)
}

func TestNormalizePublicHostCandidateFromServerURL(t *testing.T) {
	host, ok := normalizePublicHostCandidate("http://3.93.162.156:3000")
	require.True(t, ok)
	require.Equal(t, "3.93.162.156", host)
}

func TestNormalizePublicHostCandidateHandlesConcatenatedEnvGarbage(t *testing.T) {
	host, ok := normalizePublicHostCandidate("http://3.93.162.156:3000RELAY_ENDPOINT=3.93.162.156:3478")
	require.True(t, ok)
	require.Equal(t, "3.93.162.156", host)
}

func TestDerivePublicRelayEndpointUsesPublicServerURL(t *testing.T) {
	t.Setenv("RELAY_ENDPOINT", "")
	t.Setenv("PUBLIC_SERVER_URL", "http://3.93.162.156:3000")

	req := httptest.NewRequest("GET", "/api/v1/coord/relay/assign?peer_id=p1", nil)
	endpoint, err := derivePublicRelayEndpoint(req)
	require.NoError(t, err)
	require.Equal(t, "3.93.162.156:3478", endpoint)
}

func TestDerivePublicRelayEndpointUsesForwardedHostFallback(t *testing.T) {
	t.Setenv("RELAY_ENDPOINT", "")
	t.Setenv("PUBLIC_SERVER_URL", "")

	req := httptest.NewRequest("GET", "/api/v1/coord/relay/assign?peer_id=p1", nil)
	req.Header.Set("X-Forwarded-Host", "3.93.162.156:3000")

	endpoint, err := derivePublicRelayEndpoint(req)
	require.NoError(t, err)
	require.Equal(t, "3.93.162.156:3478", endpoint)
}
