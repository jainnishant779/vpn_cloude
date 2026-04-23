//go:build windows
// +build windows

package tunnel

import (
	"fmt"
	"os/exec"
	"strings"
)

func enableIPForwarding() {
	_ = exec.Command("powershell", "-Command",
		"Set-NetIPInterface -Forwarding Enabled").Run()
}

func addSubnetRoute(networkCIDR, deviceName string) error {
	out, err := exec.Command("netsh", "interface", "ipv4", "add", "route",
		networkCIDR, deviceName, "store=active").CombinedOutput()
	if err != nil && !strings.Contains(string(out), "exists") && !strings.Contains(string(out), "already") {
		fmt.Printf("[WARN] add subnet route failed: %s — %v\n", strings.TrimSpace(string(out)), err)
		return err
	}
	return nil
}

func addHostRoute(peerIP, deviceName string) error {
	out, err := exec.Command("netsh", "interface", "ipv4", "add", "route",
		peerIP+"/32", deviceName, "store=active").CombinedOutput()
	if err != nil && !strings.Contains(string(out), "exists") && !strings.Contains(string(out), "already") {
		fmt.Printf("[WARN] add peer route failed: %s — %v\n", strings.TrimSpace(string(out)), err)
		return err
	}
	return nil
}

func removeHostRoute(peerIP, deviceName string) error {
	out, err := exec.Command("netsh", "interface", "ipv4", "delete", "route",
		peerIP+"/32", deviceName).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "not found") {
		fmt.Printf("[WARN] remove peer route failed: %s — %v\n", strings.TrimSpace(string(out)), err)
		return err
	}
	return nil
}

func configureWG(deviceName, privateKey string, listenPort int) (bool, error) {
	// Windows WireGuard uses config files, not wg set
	// Write temp config and install tunnel service
	_, err := exec.LookPath("wireguard.exe")
	if err != nil {
		return false, fmt.Errorf("wireguard.exe not found")
	}
	// For now, attempt wg.exe if available
	wgPath, err := exec.LookPath("wg")
	if err != nil {
		return false, fmt.Errorf("wg.exe not found")
	}
	cmd := exec.Command(wgPath, "set", deviceName,
		"listen-port", fmt.Sprintf("%d", listenPort),
		"private-key", "CON")
	cmd.Stdin = strings.NewReader(privateKey)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("%s — %v", strings.TrimSpace(string(out)), err)
	}
	return true, nil
}

func addWGPeer(deviceName, publicKey, endpoint, allowedIP string) error {
	out, err := exec.Command("wg", "set", deviceName,
		"peer", publicKey,
		"endpoint", endpoint,
		"allowed-ips", allowedIP,
		"persistent-keepalive", "25").CombinedOutput()
	if err != nil {
		fmt.Printf("[WARN] add wg peer failed: %s — %v\n", strings.TrimSpace(string(out)), err)
		return err
	}
	return nil
}

func removeWGPeer(deviceName, publicKey string) error {
	out, err := exec.Command("wg", "set", deviceName,
		"peer", publicKey, "remove").CombinedOutput()
	if err != nil {
		fmt.Printf("[WARN] remove wg peer failed: %s — %v\n", strings.TrimSpace(string(out)), err)
		return err
	}
	return nil
}

func updateWGPeerEndpoint(deviceName, publicKey, endpoint string) error {
	out, err := exec.Command("wg", "set", deviceName,
		"peer", publicKey,
		"endpoint", endpoint).CombinedOutput()
	if err != nil {
		fmt.Printf("[WARN] update peer endpoint failed: %s — %v\n", strings.TrimSpace(string(out)), err)
		return err
	}
	return nil
}

