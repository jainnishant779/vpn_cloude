//go:build linux
// +build linux

package tunnel

import (
	"fmt"
	"os/exec"
	"strings"
)

func enableIPForwarding() {
	_ = exec.Command("sh", "-c", "sysctl -w net.ipv4.ip_forward=1").Run()
}

func addSubnetRoute(networkCIDR, deviceName string) error {
	cmd := fmt.Sprintf("ip route replace %s dev %s 2>/dev/null || true", networkCIDR, deviceName)
	return runWGCmd("add subnet route", cmd)
}

func addHostRoute(peerIP, deviceName string) error {
	cmd := fmt.Sprintf("ip route replace %s dev %s 2>/dev/null || true", peerIP, deviceName)
	return runWGCmd("add peer route", cmd)
}

func removeHostRoute(peerIP, deviceName string) error {
	cmd := fmt.Sprintf("ip route del %s dev %s 2>/dev/null || true", peerIP, deviceName)
	return runWGCmd("remove peer route", cmd)
}

func runWGCmd(description, command string) error {
	out, err := exec.Command("sh", "-c", command).CombinedOutput()
	if err != nil {
		fmt.Printf("[WARN] %s failed: %s — %v\n", description, strings.TrimSpace(string(out)), err)
		return err
	}
	return nil
}

func configureWG(deviceName, privateKey string, listenPort int, wgPath string) (bool, error) {
	if wgPath == "" {
		wgPath = "wg"
	}
	cmd := exec.Command(wgPath, "set", deviceName,
		"listen-port", fmt.Sprintf("%d", listenPort),
		"private-key", "/dev/stdin")
	cmd.Stdin = strings.NewReader(privateKey)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("%s — %v", strings.TrimSpace(string(out)), err)
	}
	return true, nil
}

func addWGPeer(deviceName, publicKey, endpoint, allowedIP string, wgPath string) error {
	if wgPath == "" {
		wgPath = "wg"
	}
	cmd := fmt.Sprintf(
		"%s set %s peer %s endpoint %s allowed-ips %s persistent-keepalive 25",
		wgPath, deviceName, publicKey, endpoint, allowedIP,
	)
	return runWGCmd("add wg peer", cmd)
}

func removeWGPeer(deviceName, publicKey string, wgPath string) error {
	if wgPath == "" {
		wgPath = "wg"
	}
	cmd := fmt.Sprintf("%s set %s peer %s remove", wgPath, deviceName, publicKey)
	return runWGCmd("remove wg peer", cmd)
}

func updateWGPeerEndpoint(deviceName, publicKey, endpoint string, wgPath string) error {
	if wgPath == "" {
		wgPath = "wg"
	}
	cmd := fmt.Sprintf("%s set %s peer %s endpoint %s", wgPath, deviceName, publicKey, endpoint)
	return runWGCmd("update peer endpoint", cmd)
}
