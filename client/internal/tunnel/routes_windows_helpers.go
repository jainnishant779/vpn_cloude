//go:build windows
// +build windows

package tunnel

import (
	"fmt"
	"strconv"
	"strings"
)

func maskBitsFromCIDR(cidr string) (int, error) {
	parts := strings.Split(cidr, "/")
	if len(parts) == 2 {
		bits, err := strconv.Atoi(parts[1])
		if err == nil {
			return bits, nil
		}
	}
	bits, err := strconv.Atoi(cidr)
	if err != nil {
		return 24, nil
	}
	return bits, nil
}

func maskStringFromCIDR(cidr string) (string, error) {
	bits, err := maskBitsFromCIDR(cidr)
	if err != nil {
		return "255.255.255.0", nil
	}
	switch bits {
	case 8:
		return "255.0.0.0", nil
	case 16:
		return "255.255.0.0", nil
	case 24:
		return "255.255.255.0", nil
	case 32:
		return "255.255.255.255", nil
	default:
		return fmt.Sprintf("%d.%d.%d.%d",
			255&(0xFFFFFFFF<<(32-bits)>>24),
			255&(0xFFFFFFFF<<(32-bits)>>16),
			255&(0xFFFFFFFF<<(32-bits)>>8),
			255&(0xFFFFFFFF<<(32-bits))), nil
	}
}
