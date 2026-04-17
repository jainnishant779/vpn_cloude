package tunnel

import (
	"fmt"
	"net"
)

// TUNDevice provides a platform-neutral adapter for tunnel interfaces.
type TUNDevice interface {
	Create(name string, mtu int) error
	Configure(ip string, cidr string) error
	Read(buf []byte) (int, error)
	Write(buf []byte) (int, error)
	Close() error
	Name() string
}

func maskBitsFromCIDR(cidr string) (int, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return 0, fmt.Errorf("mask bits from cidr: parse cidr: %w", err)
	}
	ones, bits := network.Mask.Size()
	if bits != 32 {
		return 0, fmt.Errorf("mask bits from cidr: only ipv4 is supported")
	}
	return ones, nil
}

func maskStringFromCIDR(cidr string) (string, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("mask string from cidr: parse cidr: %w", err)
	}
	mask := network.Mask
	if len(mask) != net.IPv4len {
		return "", fmt.Errorf("mask string from cidr: only ipv4 is supported")
	}
	return net.IP(mask).String(), nil
}
