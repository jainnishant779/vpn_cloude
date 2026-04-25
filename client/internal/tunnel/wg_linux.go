//go:build linux
// +build linux

package tunnel

import (
	"fmt"
	"os/exec"
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
		return fmt.Errorf("wg remove peer: %s: %w", string(out), err)
	}
	return nil
}

func updateWGPeerEndpoint(ifName, publicKey, endpoint string) error {
	out, err := exec.Command("wg", "set", ifName, "peer", publicKey, "endpoint", endpoint).CombinedOutput()
	if err != nil {
		return fmt.Errorf("wg update endpoint: %s: %w", string(out), err)
	}
	return nil
}

func enableIPForwarding() {
	_ = exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run()
}
