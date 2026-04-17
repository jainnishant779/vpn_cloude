package coordinator

import (
	"fmt"

	"quicktunnel.local/pkg/netutil"
)

// AllocateIP picks the next sequential virtual IP for a network.
func AllocateIP(networkCIDR string, usedIPs []string) (string, error) {
	allocated := netutil.AllocateIP(networkCIDR, usedIPs)
	if allocated == "" {
		return "", fmt.Errorf("allocate ip: no available addresses in %s", networkCIDR)
	}
	return allocated, nil
}
