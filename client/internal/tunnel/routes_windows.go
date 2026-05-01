//go:build windows
// +build windows

package tunnel

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func configureWG(ifName, privateKey string, listenPort int) (bool, error) {
	// Windows uses in-process wireguard-go, not the `wg` command
	return false, nil
}

func addWGPeer(ifName, publicKey, endpoint, allowedIP string) error {
	// Windows uses in-process wireguard-go, not the `wg` command
	return nil
}

func removeWGPeer(ifName, publicKey string) error {
	// Windows uses in-process wireguard-go
	return nil
}

func updateWGPeerEndpoint(ifName, publicKey, endpoint string) error {
	// Windows uses in-process wireguard-go
	return nil
}

func enableIPForwarding() {
	// Enable IP forwarding via netsh
	_ = exec.Command("netsh", "int", "ipv4", "set", "global", "forwarding=enabled").Run()
}

func addSubnetRoute(cidr, ifName string) error {
	parts := strings.Split(cidr, "/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid cidr: %s", cidr)
	}

	subnet := parts[0]
	maskBits, err := strconv.Atoi(parts[1])
	if err != nil {
		return fmt.Errorf("invalid mask bits: %s", parts[1])
	}

	// Convert mask bits to netmask
	m := uint32(0xFFFFFFFF) << (32 - maskBits)
	mask := fmt.Sprintf("%d.%d.%d.%d", m>>24&0xFF, m>>16&0xFF, m>>8&0xFF, m&0xFF)

	// netsh int ipv4 add route 10.7.0.0 mask 255.255.0.0 qtun0
	out, err := exec.Command("netsh", "int", "ipv4", "add", "route",
		subnet, "mask", mask, ifName).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "exists") {
		logW("ROUTE", "[WARN] Add subnet route failed: %s (may already exist)", string(out))
	}
	return nil
}

func addHostRoute(ip, ifName string) error {
	// netsh int ipv4 add route 10.7.0.2 mask 255.255.255.255 qtun0
	out, err := exec.Command("netsh", "int", "ipv4", "add", "route",
		ip, "mask", "255.255.255.255", ifName).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "exists") {
		logW("ROUTE", "[WARN] Add host route failed: %s (may already exist)", string(out))
	}
	return nil
}

func removeHostRoute(ip, ifName string) error {
	_ = exec.Command("netsh", "int", "ipv4", "delete", "route", ip, "mask", "255.255.255.255").Run()
	return nil
}

func maskBitsFromCIDR(cidr string) (int, error) {
	parts := strings.Split(cidr, "/")
	if len(parts) == 2 {
		if bits, err := strconv.Atoi(parts[1]); err == nil {
			return bits, nil
		}
	}
	if bits, err := strconv.Atoi(cidr); err == nil {
		return bits, nil
	}
	return 24, nil
}

func maskStringFromCIDR(cidr string) (string, error) {
	bits, err := maskBitsFromCIDR(cidr)
	if err != nil {
		return "255.255.255.0", nil
	}
	m := uint32(0xFFFFFFFF) << (32 - bits)
	return fmt.Sprintf("%d.%d.%d.%d", m>>24&0xFF, m>>16&0xFF, m>>8&0xFF, m&0xFF), nil
}
