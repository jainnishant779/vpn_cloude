package nat

import (
	"fmt"
	"strings"

	"quicktunnel.local/pkg/netutil"
)

// NATType describes how restrictive a network address translator is.
type NATType string

const (
	NATTypeNone           NATType = "None"
	NATTypeFullCone       NATType = "FullCone"
	NATTypeRestrictedCone NATType = "RestrictedCone"
	NATTypePortRestricted NATType = "PortRestricted"
	NATTypeSymmetric      NATType = "Symmetric"
)

// DetectNATType infers NAT behavior from repeated STUN observations.
func DetectNATType(stunServer string) (NATType, error) {
	if strings.TrimSpace(stunServer) == "" {
		return NATTypeSymmetric, fmt.Errorf("detect nat type: stun server is required")
	}

	ip1, port1, err := DiscoverPublicEndpoint(stunServer)
	if err != nil {
		return NATTypeSymmetric, fmt.Errorf("detect nat type: first discovery: %w", err)
	}

	ip2, port2, err := DiscoverPublicEndpoint(stunServer)
	if err != nil {
		return NATTypeSymmetric, fmt.Errorf("detect nat type: second discovery: %w", err)
	}

	outboundIP := netutil.GetOutboundIP()
	if outboundIP != "" && outboundIP == ip1 && !netutil.IsPrivateIP(outboundIP) {
		return NATTypeNone, nil
	}

	if ip1 != ip2 {
		return NATTypeSymmetric, nil
	}
	if port1 != port2 {
		return NATTypeRestrictedCone, nil
	}

	// Stable endpoint mapping across requests generally indicates cone NAT.
	if !netutil.IsPrivateIP(ip1) {
		return NATTypeFullCone, nil
	}
	return NATTypePortRestricted, nil
}
