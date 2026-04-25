//go:build darwin
// +build darwin

package tunnel

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func configureWG(ifName, privateKey string, listenPort int) (bool, error) {
	cmd := exec.Command("wg", "set", ifName,
		"private-key", "/dev/stdin",
		"listen-port", fmt.Sprintf("%d", listenPort))
	cmd.Stdin = strings.NewReader(privateKey)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("wg set: %s: %w", string(out), err)
	}
	return true, nil
}

func addWGPeer(ifName, publicKey, endpoint, allowedIP string) error {
	args := []string{"set", ifName, "peer", publicKey, "allowed-ips", allowedIP}
	if endpoint != "" {
		args = append(args, "endpoint", endpoint)
	}
	args = append(args, "persistent-keepalive", "25")
	out, err := exec.Command("wg", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("wg set peer: %s: %w", string(out), err)
	}
	return nil
}

func removeWGPeer(ifName, publicKey string) error {
	out, err := exec.Command("wg", "set", ifName, "peer", publicKey, "remove").CombinedOutput()
	if err != nil {
		return fmt.Errorf("wg remove: %s: %w", string(out), err)
	}
	return nil
}

func updateWGPeerEndpoint(ifName, publicKey, endpoint string) error {
	out, err := exec.Command("wg", "set", ifName, "peer", publicKey, "endpoint", endpoint).CombinedOutput()
	if err != nil {
		return fmt.Errorf("wg update: %s: %w", string(out), err)
	}
	return nil
}

func enableIPForwarding() {
	_ = exec.Command("sysctl", "-w", "net.inet.ip.forwarding=1").Run()
}

func addSubnetRoute(cidr, ifName string) error {
	parts := strings.Split(cidr, "/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid cidr: %s", cidr)
	}
	out, err := exec.Command("route", "-n", "add", "-net", cidr, "-interface", ifName).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "exists") {
		return fmt.Errorf("add subnet route: %s: %w", string(out), err)
	}
	return nil
}

func addHostRoute(ip, ifName string) error {
	out, err := exec.Command("route", "-n", "add", "-host", ip, "-interface", ifName).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "exists") {
		return fmt.Errorf("add host route: %s: %w", string(out), err)
	}
	return nil
}

func removeHostRoute(ip, ifName string) error {
	_ = exec.Command("route", "-n", "delete", "-host", ip).Run()
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
