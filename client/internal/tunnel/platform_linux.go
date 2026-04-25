//go:build linux
// +build linux

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
	_ = exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run()
}

func addSubnetRoute(cidr, ifName string) error {
	out, err := exec.Command("ip", "route", "add", cidr, "dev", ifName).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "File exists") {
		return fmt.Errorf("add subnet route: %s: %w", string(out), err)
	}
	return nil
}

func addHostRoute(ip, ifName string) error {
	out, err := exec.Command("ip", "route", "add", ip+"/32", "dev", ifName).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "File exists") {
		return fmt.Errorf("add host route: %s: %w", string(out), err)
	}
	return nil
}

func removeHostRoute(ip, ifName string) error {
	_ = exec.Command("ip", "route", "del", ip+"/32", "dev", ifName).Run()
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
