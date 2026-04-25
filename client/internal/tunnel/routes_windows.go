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
		"Set-NetIPInterface -Forwarding Enabled 2>$null").Run()
}

func addSubnetRoute(networkCIDR, deviceName string) error {
	out, err := exec.Command("netsh", "interface", "ipv4", "add", "route",
		networkCIDR, deviceName, "store=active").CombinedOutput()
	if err != nil && !strings.Contains(string(out), "exists") && !strings.Contains(string(out), "already") {
		fmt.Printf("[WARN] add subnet route: %s\n", strings.TrimSpace(string(out)))
	}
	return nil
}

func addHostRoute(peerIP, deviceName string) error {
	out, err := exec.Command("netsh", "interface", "ipv4", "add", "route",
		peerIP+"/32", deviceName, "store=active").CombinedOutput()
	if err != nil && !strings.Contains(string(out), "exists") && !strings.Contains(string(out), "already") {
		fmt.Printf("[WARN] add host route: %s\n", strings.TrimSpace(string(out)))
	}
	return nil
}

func removeHostRoute(peerIP, deviceName string) error {
	_, _ = exec.Command("netsh", "interface", "ipv4", "delete", "route",
		peerIP+"/32", deviceName).CombinedOutput()
	return nil
}

// configureWG on Windows delegates to the WindowsTUNDevice's in-process
// wireguard-go device. wg.exe is NOT needed — everything runs in-process.
func configureWG(deviceName, privateKey string, listenPort int) (bool, error) {
	// The actual WireGuard setup is done via WindowsTUNDevice.SetupWireGuard()
	// which is called from wireguard.go after CreateTUN().
	// This function is a no-op on Windows — return true to signal WG is ready.
	return true, nil
}

func addWGPeer(deviceName, publicKey, endpoint, allowedIP string) error {
	// Delegated to WindowsTUNDevice.AddWGPeer() in wireguard.go
	return nil
}

func removeWGPeer(deviceName, publicKey string) error {
	return nil
}

func updateWGPeerEndpoint(deviceName, publicKey, endpoint string) error {
	return nil
}