package peer

import (
	"testing"

	"quicktunnel/client/internal/api_client"

	"github.com/stretchr/testify/require"
)

func TestSanitizeEndpointReplacesInternalHostWithPublicServerHost(t *testing.T) {
	pm := &PeerManager{
		apiClient: api_client.NewClient("http://3.93.162.156:3000", ""),
	}

	got := pm.sanitizeEndpoint("relay:3478", 3478)
	require.Equal(t, "3.93.162.156:3478", got)
}

func TestSanitizeEndpointKeepsValidPublicEndpoint(t *testing.T) {
	pm := &PeerManager{
		apiClient: api_client.NewClient("http://3.93.162.156:3000", ""),
	}

	got := pm.sanitizeEndpoint("152.56.169.16:51820", 51820)
	require.Equal(t, "152.56.169.16:51820", got)
}
